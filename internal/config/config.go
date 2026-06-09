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

	// Central notifications service (iag-notifications) integration.
	// When NotificationsURL is empty, procurement falls back to a Noop
	// dispatcher so local dev does not require auth + notifications to
	// be running. In production, the four NOTIFICATIONS_* env vars are
	// required and procurement uses an OAuth2 client_credentials token
	// (minted from AuthTokenURL) to call POST /v1/dispatch.
	NotificationsURL          string
	NotificationsClientID     string
	NotificationsClientSecret string
	NotificationsAudience     string
	AuthTokenURL              string
	ServiceClientID           string
	ServiceClientSecret       string

	SignalRedisChannel string

	JWTSecret            string
	JWTTTL               time.Duration
	DefaultAdminPassword string

	KafkaBrokers         []string
	EventBusEnabled      bool
		KafkaCommercialTopic  string
		KafkaSupplyChainTopic string
		KafkaOperationsTopic  string
		KafkaConsumerGroup    string
		KafkaSupplyChainGroup string
		KafkaOperationsGroup  string

	// AuthMode is "jwt" (platform Bearer+aud, production default) or "legacy"
	// (local DB-backed users + HS256, kept for the standalone procurement app
	// and integration tests). The pre-cutover "gateway" header-trust path has
	// been removed.
	AuthMode  string
	JWTIssuer string
	JWKSURL   string
	Audience  string
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

	authMode := strings.ToLower(strings.TrimSpace(getenv("AUTH_MODE", "jwt")))
	switch authMode {
	case "jwt", "legacy":
	default:
		return nil, fmt.Errorf("AUTH_MODE must be jwt or legacy (got %q)", authMode)
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
		CORSAllowOrigin: corsAllowOrigin(),

		NotificationsURL:          strings.TrimSpace(os.Getenv("NOTIFICATIONS_URL")),
		NotificationsClientID:     strings.TrimSpace(os.Getenv("NOTIFICATIONS_CLIENT_ID")),
		NotificationsClientSecret: os.Getenv("NOTIFICATIONS_CLIENT_SECRET"),
		NotificationsAudience:     getenv("NOTIFICATIONS_AUDIENCE", "iag.notifications"),
		AuthTokenURL:              strings.TrimSpace(os.Getenv("AUTH_TOKEN_URL")),
		ServiceClientID:           strings.TrimSpace(getenv("SERVICE_CLIENT_ID", "iag-procurement")),
		ServiceClientSecret:       os.Getenv("SERVICE_CLIENT_SECRET"),

		SignalRedisChannel: getenv("SIGNAL_REDIS_CHANNEL", "procurement:signals"),

		JWTSecret:            jwtSecret,
		JWTTTL:               jwtTTL,
		DefaultAdminPassword: os.Getenv("DEFAULT_ADMIN_PASSWORD"),

		KafkaBrokers:         parseBrokers(os.Getenv("KAFKA_BROKERS")),
		EventBusEnabled:      strings.EqualFold(os.Getenv("EVENT_BUS_ENABLED"), "true"),
		KafkaCommercialTopic:  getenv("KAFKA_COMMERCIAL_TOPIC", "iag.commercial"),
		KafkaSupplyChainTopic: getenv("KAFKA_SUPPLY_CHAIN_TOPIC", "iag.supply-chain"),
		KafkaOperationsTopic:  getenv("KAFKA_OPERATIONS_TOPIC", "iag.operations"),
		KafkaConsumerGroup:    getenv("KAFKA_CONSUMER_GROUP", "iag.procurement.commercial"),
		KafkaSupplyChainGroup: getenv("KAFKA_SUPPLY_CHAIN_GROUP", "iag.procurement.supply-chain"),
		KafkaOperationsGroup:  getenv("KAFKA_OPERATIONS_GROUP", "iag.procurement.operations"),

		AuthMode:  authMode,
		JWTIssuer: getenv("JWT_ISSUER", "http://localhost:3001"),
		JWKSURL:   getenv("JWKS_URL", "http://127.0.0.1:3001/.well-known/jwks.json"),
		Audience:  getenv("AUDIENCE", "iag.procurement"),
	}

	if c.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}
	if c.AuthTokenURL == "" {
		c.AuthTokenURL = strings.TrimRight(c.JWTIssuer, "/") + "/oauth/token"
	}
	return c, c.Validate()
}

func (c *Config) Validate() error {
	if c.AuthMode == "jwt" {
		if c.JWKSURL == "" {
			return fmt.Errorf("AUTH_MODE=jwt requires JWKS_URL")
		}
		if c.Audience == "" {
			return fmt.Errorf("AUTH_MODE=jwt requires AUDIENCE (e.g. iag.procurement)")
		}
	}
	if c.Environment == "production" && c.AuthMode != "jwt" {
		return fmt.Errorf("AUTH_MODE must be jwt in production (got %q)", c.AuthMode)
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
	if c.Environment == "production" && hasWildcardCORS(c.CORSAllowOrigin) {
		return fmt.Errorf("CORS allowlist must not include '*' in production")
	}
	return nil
}

func corsAllowOrigin() string {
	for _, key := range []string{"CORS_ALLOWED_ORIGINS", "CORS_ALLOW_ORIGIN", "CORS_ORIGIN", "ALLOWED_ORIGINS", "CORS_ORIGINS"} {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			return v
		}
	}
	return "http://localhost:3000,http://localhost:5173"
}

func hasWildcardCORS(allowed string) bool {
	if allowed == "*" {
		return true
	}
	for _, o := range strings.Split(allowed, ",") {
		if strings.TrimSpace(o) == "*" {
			return true
		}
	}
	return false
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
