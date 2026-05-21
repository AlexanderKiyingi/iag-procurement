package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/alvor-technologies/iag-authclient"
	"github.com/gin-gonic/gin"

	"iag-procurement/backend/internal/auditlog"
	"iag-procurement/backend/internal/cache"
	"iag-procurement/backend/internal/config"
	"iag-procurement/backend/internal/consumer"
	"iag-procurement/backend/internal/db"
	"iag-procurement/backend/internal/email"
	"iag-procurement/backend/internal/handlers"
	"iag-procurement/backend/internal/iam"
	"iag-procurement/backend/internal/middleware"
	"iag-procurement/backend/internal/migrate"
	"iag-procurement/backend/internal/notifications"
	"iag-procurement/backend/internal/rbac"
	"iag-procurement/backend/internal/repo"
	"iag-procurement/backend/internal/signals"
)

func main() {
	ctx := context.Background()
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	if os.Getenv("GIN_MODE") == "release" {
		gin.SetMode(gin.ReleaseMode)
	}

	pool, err := db.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer pool.Close()

	if cfg.AutoMigrate {
		if err := migrate.Up(ctx, pool); err != nil {
			log.Fatalf("migrate: %v", err)
		}
	} else {
		log.Printf("auto-migrate disabled — assuming schema is current")
	}

	if cfg.AuthMode == "legacy" && cfg.SeedOnStartup {
		if err := rbac.Seed(ctx, pool, cfg.DefaultAdminPassword); err != nil {
			log.Fatalf("rbac seed: %v", err)
		}
	} else if cfg.AuthMode == "legacy" {
		log.Printf("rbac seed skipped (SEED_ON_STARTUP=false)")
	} else {
		log.Printf("local rbac seed skipped (AUTH_MODE=%s — use iag-authentication groups)", cfg.AuthMode)
	}

	var verifier *authclient.Verifier
	if cfg.AuthMode == "jwt" {
		verifier = authclient.NewVerifier(cfg.JWKSURL, cfg.JWTIssuer)
		initCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		if err := verifier.Refresh(initCtx); err != nil {
			cancel()
			log.Fatalf("jwks refresh: %v", err)
		}
		cancel()
		go jwksRefreshLoop(verifier)
	}
	platformAuth := middleware.NewPlatformAuth(middleware.PlatformAuthOptions{
		Mode:          cfg.AuthMode,
		GatewaySecret: cfg.GatewaySecret,
		Verifier:      verifier,
	})
	log.Printf("auth: AUTH_MODE=%s", cfg.AuthMode)

	rbacStore := rbac.NewStore(pool)
	var iamSvc *iam.Service
	if cfg.AuthMode == "legacy" {
		iamSvc = iam.NewService(rbacStore, []byte(cfg.JWTSecret), cfg.JWTTTL)
	}
	auditStore := auditlog.NewStore(pool)

	rdb, err := cache.New(cfg.RedisURL)
	if err != nil {
		log.Fatalf("redis: %v", err)
	}
	defer rdb.Close()

	var mailer email.Mailer = email.NoopMailer{}
	if cfg.SMTPHost != "" {
		mailer = &email.SMTPMailer{
			Host: cfg.SMTPHost, Port: cfg.SMTPPort,
			User: cfg.SMTPUser, Password: cfg.SMTPPassword,
			From: cfg.SMTPFrom,
		}
		log.Printf("notifications: SMTP enabled (%s:%d)", cfg.SMTPHost, cfg.SMTPPort)
	} else {
		log.Printf("notifications: SMTP disabled (set SMTP_HOST to send email)")
	}

	notifyStore := notifications.NewStore(pool)
	notifySvc := notifications.NewService(notifyStore, rdb, cfg.NotifyQueueKey, mailer)
	bus := signals.NewBus()
	notifySvc.Register(bus)

	workerCtx, workerCancel := context.WithCancel(context.Background())
	defer workerCancel()
	notifications.StartEmailConsumers(workerCtx, cfg.NotifyConsumerWorkers, rdb, cfg.NotifyQueueKey, notifyStore, mailer)

	procurementRepo := repo.NewProcurement(pool)

	var commercialConsumer *consumer.Commercial
	if cfg.EventBusEnabled && len(cfg.KafkaBrokers) > 0 {
		commercialConsumer = consumer.NewCommercial(consumer.Config{
			Brokers: cfg.KafkaBrokers,
			GroupID: cfg.KafkaConsumerGroup,
			Topic:   cfg.KafkaCommercialTopic,
		}, procurementRepo, bus)
		go func() {
			if err := commercialConsumer.Run(workerCtx); err != nil && workerCtx.Err() == nil {
				log.Printf("commercial consumer stopped: %v", err)
			}
		}()
		log.Printf("event bus: consuming %s as group %s", cfg.KafkaCommercialTopic, cfg.KafkaConsumerGroup)
	} else {
		log.Printf("event bus: disabled (set EVENT_BUS_ENABLED=true and KAFKA_BROKERS)")
	}

	signals.StartSubscriber(workerCtx, rdb.Redis(), cfg.SignalRedisChannel, func(ctx context.Context, e signals.Event) error {
		body := string(e.Payload)
		if len(body) > 4000 {
			body = body[:4000] + "…"
		}
		_, err := notifyStore.InsertInApp(ctx, e.Name, "Broadcast: "+e.Name, body, "info")
		return err
	})

	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery())

	api := handlers.NewAPI(handlers.Deps{
		Pool:         pool,
		Seed:         repo.NewSeed(pool),
		Procurement:  procurementRepo,
		Cache:        rdb,
		Config:       cfg,
		Notify:       notifySvc,
		Bus:          bus,
		IAM:          iamSvc,
		RBAC:         rbacStore,
		Audit:        auditStore,
		PlatformAuth: platformAuth,
	})
	api.Mount(r)

	addr := ":" + cfg.Port
	srv := &http.Server{
		Addr:              addr,
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		log.Printf("%s listening on %s (env=%s, signals=%q, notify=%q)", cfg.ServiceName, addr, cfg.Environment, cfg.SignalRedisChannel, cfg.NotifyQueueKey)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	workerCancel()
	if commercialConsumer != nil {
		_ = commercialConsumer.Close()
	}
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}

func jwksRefreshLoop(v *authclient.Verifier) {
	ticker := time.NewTicker(5 * time.Minute)
	for range ticker.C {
		if err := v.Refresh(context.Background()); err != nil {
			log.Printf("jwks refresh: %v", err)
		}
	}
}
