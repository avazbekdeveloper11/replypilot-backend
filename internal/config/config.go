// Package config centralizes all runtime configuration. Every value is
// sourced from the environment (12-factor style) — nothing here reads a
// config file. .env is loaded for local development convenience only; in
// staging/production the process environment is populated by the deploy
// platform and .env is not present.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	App       AppConfig
	DB        DBConfig
	Redis     RedisConfig
	RabbitMQ  RabbitMQConfig
	JWT       JWTConfig
	Meta      MetaConfig
	Telegram  TelegramConfig
	Gemini    GeminiConfig
	Stripe    StripeConfig
	Security  SecurityConfig
	RateLimit RateLimitConfig
	Email     EmailConfig
}

type AppConfig struct {
	Env            string
	Port           string
	AllowedOrigins []string
	// WebURL is the dashboard's own base URL — used to build links that
	// point INTO the frontend (currently: the password-reset link). Not
	// to be confused with AllowedOrigins (CORS) or Meta's RedirectURL
	// (that's the Go API's own callback URL, a different app entirely).
	WebURL string
}

type DBConfig struct {
	Host                   string
	Port                   int
	User                   string
	Password               string
	Name                   string
	SSLMode                string
	MaxOpenConns           int
	MaxIdleConns           int
	ConnMaxLifetimeMinutes int
}

type RedisConfig struct {
	Addr     string
	Password string
	DB       int
}

type RabbitMQConfig struct {
	URL string
}

type JWTConfig struct {
	Secret          string
	Issuer          string
	AccessTokenTTL  time.Duration
	RefreshTokenTTL time.Duration
}

type MetaConfig struct {
	AppID              string
	AppSecret          string
	RedirectURL        string
	WebhookVerifyToken string
	GraphAPIBaseURL    string
}

// TelegramConfig holds the shared infrastructure Telegram bot connections
// need — same "boots without it, feature errors until configured"
// philosophy as GeminiConfig/StripeConfig below, not mustGetEnv (a
// deployment that never uses Telegram shouldn't fail to start over it).
//
// WebhookSecret is deliberately ONE value shared across every organization's
// bot, not one per bot — Telegram's setWebhook secret_token exists to prove
// a delivery genuinely came from Telegram, not to distinguish between
// organizations (the URL path already does that, see
// telegram_webhook_handler.go). A per-bot secret would need its own storage
// and only adds defense against an attacker who can already guess a
// specific org's random account UUID, which the URL alone already resists.
type TelegramConfig struct {
	// WebhookBaseURL is this API service's own public base URL — used to
	// build the webhook URL passed to Telegram's setWebhook (see
	// telegram.ConnectUseCase.Connect), the same role META_REDIRECT_URL
	// plays for Instagram's OAuth callback.
	WebhookBaseURL string
	WebhookSecret  string
}

// GeminiConfig holds the one secret needed to call Google's Gemini API —
// used for both knowledge-base embeddings (internal/usecase/knowledgebase)
// and AI reply generation (internal/usecase/ai). Get a key at
// aistudio.google.com/apikey. Not marked required (mustGetEnv) the way
// META_* and JWT_SECRET are: unlike those, this codebase should still boot
// and serve every other feature with an empty/placeholder key — the
// knowledge-base and AI-reply usecases simply fail their own calls with a
// clear error until a real key is set, rather than the whole API refusing
// to start.
type GeminiConfig struct {
	APIKey string
}

// StripeConfig holds Stripe secrets — same "boots without it, individual
// calls fail with a clear error" philosophy as GeminiConfig, not
// mustGetEnv. WebhookSecret verifies the Stripe-Signature header (see
// stripeapi.VerifyWebhookSignature); it's a DIFFERENT secret from
// SecretKey, generated per webhook endpoint in the Stripe dashboard, not
// derived from it.
type StripeConfig struct {
	SecretKey     string
	WebhookSecret string
}

// SecurityConfig holds secrets unrelated to auth tokens — currently the
// symmetric key used to encrypt Instagram access tokens at rest.
type SecurityConfig struct {
	TokenEncryptionKey string
}

// EmailConfig holds Resend's API key and the verified sender identity —
// same "boots without it, individual calls fail with a clear error"
// philosophy as GeminiConfig/StripeConfig, not mustGetEnv. When
// ResendAPIKey is empty, internal/di/container.go wires
// notify.LogNotifier instead of notify.ResendNotifier, so
// registration/password-reset codes still work locally (logged, not
// emailed) without this being configured.
type EmailConfig struct {
	ResendAPIKey string
	// FromEmail must be on a domain verified in the Resend dashboard
	// (SPF/DKIM) — see internal/integration/resendapi's package doc
	// comment. Format: "Name <noreply@yourdomain.com>" or a bare address.
	FromEmail string
}

// RateLimitConfig has one field, not a token-bucket burst/rate pair,
// because middleware.RateLimiter implements a fixed-window counter (see
// its doc comment for why that's the right tradeoff here). Add a Burst
// field back if that implementation ever changes.
type RateLimitConfig struct {
	RequestsPerMinute int
}

// Load reads configuration from the environment. It panics on a missing
// required secret rather than starting the process in a half-configured
// state — a service that boots with an empty JWT secret or DB password is
// a worse failure mode than one that never boots.
func Load() (*Config, error) {
	_ = godotenv.Load() // optional: only present in local dev

	cfg := &Config{
		App: AppConfig{
			Env:            getEnv("APP_ENV", "development"),
			Port:           getEnv("APP_PORT", "8080"),
			AllowedOrigins: getEnvAsSlice("CORS_ALLOWED_ORIGINS", []string{"http://localhost:3000"}),
			WebURL:         getEnv("WEB_APP_URL", "http://localhost:3000"),
		},
		DB: DBConfig{
			Host:                   getEnv("DB_HOST", "localhost"),
			Port:                   getEnvAsInt("DB_PORT", 5432),
			User:                   getEnv("DB_USER", "replypilot"),
			Password:               mustGetEnv("DB_PASSWORD"),
			Name:                   getEnv("DB_NAME", "replypilot"),
			SSLMode:                getEnv("DB_SSLMODE", "disable"),
			MaxOpenConns:           getEnvAsInt("DB_MAX_OPEN_CONNS", 25),
			MaxIdleConns:           getEnvAsInt("DB_MAX_IDLE_CONNS", 5),
			ConnMaxLifetimeMinutes: getEnvAsInt("DB_CONN_MAX_LIFETIME_MIN", 30),
		},
		Redis: RedisConfig{
			Addr:     getEnv("REDIS_ADDR", "localhost:6379"),
			Password: getEnv("REDIS_PASSWORD", ""),
			DB:       getEnvAsInt("REDIS_DB", 0),
		},
		RabbitMQ: RabbitMQConfig{
			URL: getEnv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/"),
		},
		JWT: JWTConfig{
			Secret:          mustGetEnv("JWT_SECRET"),
			Issuer:          getEnv("JWT_ISSUER", "replypilot"),
			AccessTokenTTL:  getEnvAsDuration("JWT_ACCESS_TTL", 15*time.Minute),
			RefreshTokenTTL: getEnvAsDuration("JWT_REFRESH_TTL", 7*24*time.Hour),
		},
		Meta: MetaConfig{
			AppID:              mustGetEnv("META_APP_ID"),
			AppSecret:          mustGetEnv("META_APP_SECRET"),
			RedirectURL:        mustGetEnv("META_REDIRECT_URL"),
			WebhookVerifyToken: mustGetEnv("META_WEBHOOK_VERIFY_TOKEN"),
			GraphAPIBaseURL:    getEnv("META_GRAPH_API_BASE_URL", "https://graph.instagram.com"),
		},
		Telegram: TelegramConfig{
			WebhookBaseURL: getEnv("TELEGRAM_WEBHOOK_BASE_URL", ""),
			WebhookSecret:  getEnv("TELEGRAM_WEBHOOK_SECRET", ""),
		},
		Gemini: GeminiConfig{
			APIKey: getEnv("GEMINI_API_KEY", ""),
		},
		Stripe: StripeConfig{
			SecretKey:     getEnv("STRIPE_SECRET_KEY", ""),
			WebhookSecret: getEnv("STRIPE_WEBHOOK_SECRET", ""),
		},
		Security: SecurityConfig{
			TokenEncryptionKey: mustGetEnv("TOKEN_ENCRYPTION_KEY"),
		},
		RateLimit: RateLimitConfig{
			RequestsPerMinute: getEnvAsInt("RATE_LIMIT_RPM", 120),
		},
		Email: EmailConfig{
			ResendAPIKey: getEnv("RESEND_API_KEY", ""),
			FromEmail:    getEnv("RESEND_FROM_EMAIL", ""),
		},
	}

	return cfg, nil
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func mustGetEnv(key string) string {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		panic(fmt.Sprintf("config: missing required environment variable %s", key))
	}
	return v
}

func getEnvAsInt(key string, fallback int) int {
	v := getEnv(key, "")
	if v == "" {
		return fallback
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return i
}

func getEnvAsSlice(key string, fallback []string) []string {
	v := getEnv(key, "")
	if v == "" {
		return fallback
	}
	parts := strings.Split(v, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func getEnvAsDuration(key string, fallback time.Duration) time.Duration {
	v := getEnv(key, "")
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}
	return d
}
