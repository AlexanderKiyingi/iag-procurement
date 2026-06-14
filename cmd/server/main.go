package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/alvor-technologies/iag-authclient"
	platformotel "github.com/alvor-technologies/iag-platform-go/otel"
	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"

	"iag-procurement/backend/internal/auditlog"
	"iag-procurement/backend/internal/cache"
	"iag-procurement/backend/internal/config"
	"iag-procurement/backend/internal/consumer"
	"iag-procurement/backend/internal/db"
	procevents "iag-procurement/backend/internal/events"
	"iag-procurement/backend/internal/handlers"
	"iag-procurement/backend/internal/iam"
	"iag-procurement/backend/internal/middleware"
	"iag-procurement/backend/internal/migrate"
	"iag-procurement/backend/internal/notifications"
	"iag-procurement/backend/internal/notifyclient"
	"iag-procurement/backend/internal/outbox"
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

	// OpenTelemetry → otel-collector:4317 (non-blocking dial).
	if tp, err := platformotel.Init(ctx, platformotel.Config{
		ServiceName: cfg.ServiceName,
		Environment: cfg.Environment,
	}); err != nil {
		log.Printf("otel disabled: %v", err)
	} else {
		defer func() {
			sc, c := context.WithTimeout(context.Background(), 5*time.Second)
			defer c()
			_ = tp.Shutdown(sc)
		}()
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
		verifier = authclient.NewVerifier(cfg.JWKSURL, cfg.JWTIssuer, cfg.Audience)
		initCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		if err := verifier.Refresh(initCtx); err != nil {
			cancel()
			log.Fatalf("jwks refresh: %v", err)
		}
		cancel()
		go jwksRefreshLoop(verifier)
	}
	platformAuth := middleware.NewPlatformAuth(middleware.PlatformAuthOptions{
		Mode:     cfg.AuthMode,
		Verifier: verifier,
	})
	log.Printf("auth: AUTH_MODE=%s", cfg.AuthMode)

	if cfg.AuthMode == "jwt" && cfg.ServiceClientSecret != "" {
		go registerPermissionsLoop(ctx, cfg)
	} else if cfg.AuthMode == "jwt" {
		log.Printf("procurement: SERVICE_CLIENT_SECRET unset — skipping permissions registration")
	}

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

	// Build the notify dispatcher. When NOTIFICATIONS_URL is set we
	// require the OAuth2 client_credentials inputs and mint Bearer
	// tokens to call iag-notifications POST /v1/dispatch. Otherwise
	// fall back to a logging-only Noop so local dev does not need the
	// auth + notifications dependency chain running.
	var notifyDispatcher notifyclient.Dispatcher = notifyclient.Noop{}
	if cfg.NotificationsURL != "" {
		tokenURL := cfg.AuthTokenURL
		if tokenURL == "" {
			tokenURL = strings.TrimRight(cfg.JWTIssuer, "/") + "/oauth/token"
		}
		if cfg.NotificationsClientID == "" || cfg.NotificationsClientSecret == "" {
			log.Fatal("notifications: NOTIFICATIONS_URL set but NOTIFICATIONS_CLIENT_ID/SECRET missing")
		}
		auth := notifyclient.NewServiceAuth(notifyclient.ServiceAuthOptions{
			TokenURL:     tokenURL,
			ClientID:     cfg.NotificationsClientID,
			ClientSecret: cfg.NotificationsClientSecret,
			Audience:     cfg.NotificationsAudience,
		})
		notifyDispatcher = notifyclient.NewClient(cfg.NotificationsURL, auth)
		log.Printf("notifications: dispatching to %s (audience=%s)", cfg.NotificationsURL, cfg.NotificationsAudience)
	} else {
		log.Printf("notifications: NOTIFICATIONS_URL unset; using noop dispatcher (set NOTIFICATIONS_URL to send via iag-notifications)")
	}

	notifyStore := notifications.NewStore(pool)
	notifySvc := notifications.NewService(notifyStore, notifyDispatcher)
	bus := signals.NewBus()
	notifySvc.Register(bus)

	workerCtx, workerCancel := context.WithCancel(context.Background())
	defer workerCancel()

	procurementRepo := repo.NewProcurement(pool)
	procurementRepo.SetApprovalThreshold(cfg.ApprovalThreshold)

	// Daily budget period-close job: closes budgets whose period_end has passed,
	// applying the configured policy (lapse / carry). Runs once shortly after
	// boot, then every 24h, until shutdown.
	if cfg.PeriodCloseEnabled {
		go func() {
			ticker := time.NewTicker(24 * time.Hour)
			defer ticker.Stop()
			for {
				ids, err := procurementRepo.CloseBudgetPeriod(workerCtx, cfg.PeriodClosePolicy, repo.BudgetCloseFilter{DueOnly: true}, "system")
				if err != nil {
					log.Printf("budget period close: %v", err)
				} else if len(ids) > 0 {
					log.Printf("budget period close: closed %d budget(s) (policy=%s)", len(ids), cfg.PeriodClosePolicy)
				}
				select {
				case <-workerCtx.Done():
					return
				case <-ticker.C:
				}
			}
		}()
		log.Printf("budget period close job enabled (policy=%s)", cfg.PeriodClosePolicy)
	}

	var commercialConsumer *consumer.Commercial
	var supplyChainConsumer *consumer.SupplyChain
	publisher := procevents.NewPublisher(procevents.PublisherConfig{
		Brokers: cfg.KafkaBrokers,
		Enabled: cfg.EventBusEnabled && len(cfg.KafkaBrokers) > 0,
	})
	defer func() { _ = publisher.Close() }()
	if cfg.EventBusEnabled && len(cfg.KafkaBrokers) > 0 {
		// Transactional outbox: requisition approval/rejection, invoice-received,
		// and GRN-posted events are enqueued in their writing tx by the repo and
		// drained to Kafka here, so a broker outage delays delivery instead of
		// dropping events.
		outboxStore := outbox.NewStore(pool)
		procurementRepo.SetOutbox(outboxStore)
		go outbox.NewPublisher(outboxStore, publisher).Run(workerCtx)
		log.Printf("event bus: outbox publisher started")
	}
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

		supplyChainConsumer = consumer.NewSupplyChain(consumer.Config{
			Brokers: cfg.KafkaBrokers,
			GroupID: cfg.KafkaSupplyChainGroup,
			Topic:   cfg.KafkaSupplyChainTopic,
		}, procurementRepo)
		go func() {
			if err := supplyChainConsumer.Run(workerCtx); err != nil && workerCtx.Err() == nil {
				log.Printf("supply-chain consumer stopped: %v", err)
			}
		}()
		log.Printf("event bus: consuming %s as group %s (party sync)", cfg.KafkaSupplyChainTopic, cfg.KafkaSupplyChainGroup)

		opsConsumer := consumer.NewOperations(consumer.OperationsConfig{
			Brokers: cfg.KafkaBrokers,
			GroupID: cfg.KafkaOperationsGroup,
			Topic:   cfg.KafkaOperationsTopic,
		}, procurementRepo)
		go func() {
			if err := opsConsumer.Run(workerCtx); err != nil && workerCtx.Err() == nil {
				log.Printf("operations consumer stopped: %v", err)
			}
		}()
		log.Printf("event bus: consuming %s as group %s (warehouse low stock)", cfg.KafkaOperationsTopic, cfg.KafkaOperationsGroup)
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
	r.Use(otelgin.Middleware(cfg.ServiceName))
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
		Publisher:    publisher,
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
		log.Printf("%s listening on %s (env=%s, signals=%q)", cfg.ServiceName, addr, cfg.Environment, cfg.SignalRedisChannel)
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
