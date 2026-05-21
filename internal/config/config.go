package config

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

const devJWTSecret = "dev-insecure-change-me"

type Config struct {
	Environment string
	ServiceName string
	Port        string
	DatabaseURL string
	RedisURL    string
	AutoMigrate bool
	SeedOnStartup bool
	SeedCacheTTL    time.Duration
	CORSAllowOrigin string

	SMTPHost     string
	SMTPPort     int
	SMTPUser     string
	SMTPPassword string
	SMTPFrom     string

	NotifyQueueKey        string
	NotifyConsumerWorkers int

	SignalRedisChannel string

	JWTSecret            string
	JWTTTL               time.Duration
	DefaultAdminPassword string

	KafkaBrokers         []string
	EventBusEnabled      bool
	KafkaCommercialTopic string
	KafkaConsumerGroup   string

	AuthMode        string
	GatewaySecret   string
	JWTIssuer       string
	JWKSURL         string
}

func Load() (*Config, error) {
	_ = godotenv.Load()

	env := strings.ToLower(strings.TrimSpace(getenv("ENVIRONMENT", "development")))
	if env != "development" && env != "staging" && env != "production" {
		return nil, fmt.Errorf("ENVIRONMENT must be development, staging, or production")
	}

	ttl := 30 * time.Second
	if s := os.Getenv("SEED_CACHE_TTL_SECONDS"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			ttl = time.Duration(n) * time.Second
		}
	}

	smtpPort := 587
	if s := os.Getenv("SMTP_PORT"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			smtpPort = n
		}
	}

	workers := 2
	if s := os.Getenv("NOTIFY_CONSUMER_WORKERS"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			workers = n
		}
	}

	jwtTTL := 72 * time.Hour
	if s := os.Getenv("JWT_TTL_HOURS"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			jwtTTL = time.Duration(n) * time.Hour
		}
	}

	seedDefault := "true"
	if env == "production" {
		seedDefault = "false"
	}

	authMode := strings.ToLower(strings.TrimSpace(getenv("AUTH_MODE", "gateway")))
	switch authMode {
	case "gateway", "jwt", "legacy":
	default:
		return nil, fmt.Errorf("AUTH_MODE must be gateway, jwt, or legacy (got %q)", authMode)
	}

	jwtSecret := getenv("JWT_SECRET", devJWTSecret)
	if jwtSecret == devJWTSecret && env == "development" {
		log.Printf("config: using default JWT_SECRET (set JWT_SECRET in production)")
	}

	c := &Config{
		Environment:     env,
		ServiceName:     getenv("SERVICE_NAME", "procurement"),
		Port:            getenv("PORT", "4009"),
		DatabaseURL:     strings.TrimSpace(os.Getenv("DATABASE_URL")),
		RedisURL:        getenv("REDIS_URL", "redis://127.0.0.1:6379/0"),
		AutoMigrate:     getenv("AUTO_MIGRATE", "true") != "false",
		SeedOnStartup:   getenv("SEED_ON_STARTUP", seedDefault) == "true",
		SeedCacheTTL:    ttl,
		CORSAllowOrigin: getenv("CORS_ALLOW_ORIGIN", "http://localhost:3000"),

		SMTPHost:     os.Getenv("SMTP_HOST"),
		SMTPPort:     smtpPort,
		SMTPUser:     os.Getenv("SMTP_USER"),
		SMTPPassword: os.Getenv("SMTP_PASSWORD"),
		SMTPFrom:     getenv("SMTP_FROM", "procurement@localhost"),

		NotifyQueueKey:        getenv("NOTIFY_QUEUE_KEY", "procurement:notify:queue"),
		NotifyConsumerWorkers: workers,

		SignalRedisChannel: getenv("SIGNAL_REDIS_CHANNEL", "procurement:signals"),

		JWTSecret:            jwtSecret,
		JWTTTL:               jwtTTL,
		DefaultAdminPassword: os.Getenv("DEFAULT_ADMIN_PASSWORD"),

		KafkaBrokers:         parseBrokers(os.Getenv("KAFKA_BROKERS")),
		EventBusEnabled:      strings.EqualFold(os.Getenv("EVENT_BUS_ENABLED"), "true"),
		KafkaCommercialTopic: getenv("KAFKA_COMMERCIAL_TOPIC", "iag.commercial"),
		KafkaConsumerGroup:   getenv("KAFKA_CONSUMER_GROUP", "iag.procurement.commercial"),

		AuthMode:      authMode,
		GatewaySecret: strings.TrimSpace(os.Getenv("GATEWAY_INTERNAL_SECRET")),
		JWTIssuer:     getenv("JWT_ISSUER", "http://localhost:3001"),
		JWKSURL:       getenv("JWKS_URL", "http://127.0.0.1:3001/.well-known/jwks.json"),
	}

	if c.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}
	return c, c.Validate()
}

func (c *Config) Validate() error {
	if c.AuthMode == "gateway" {
		if c.GatewaySecret == "" {
			return fmt.Errorf("AUTH_MODE=gateway requires GATEWAY_INTERNAL_SECRET")
		}
		if len(c.GatewaySecret) < 16 {
			return fmt.Errorf("GATEWAY_INTERNAL_SECRET must be at least 16 characters")
		}
	}
	if c.AuthMode == "jwt" && c.JWKSURL == "" {
		return fmt.Errorf("AUTH_MODE=jwt requires JWKS_URL")
	}
	if c.Environment == "production" && c.AuthMode == "legacy" {
		if c.JWTSecret == "" || c.JWTSecret == devJWTSecret {
			return fmt.Errorf("production legacy auth requires JWT_SECRET (not the dev default)")
		}
		if len(c.JWTSecret) < 32 {
			return fmt.Errorf("production JWT_SECRET must be at least 32 characters")
		}
	}
	if c.Environment == "production" && c.SeedOnStartup && c.AuthMode == "legacy" {
		if len(c.DefaultAdminPassword) < 12 {
			return fmt.Errorf("production with SEED_ON_STARTUP=true requires DEFAULT_ADMIN_PASSWORD (min 12 chars)")
		}
	}
	return nil
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func parseBrokers(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}
