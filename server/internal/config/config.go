// Package config loads runtime configuration from environment variables.
// All keys mirror .env.example. Defaults match local dev so a fresh
// `just up && just serve` works without an explicit .env in the simple case.
package config

import (
	"fmt"
	"os"
	"strconv"
)

type Config struct {
	Env        string // dev | staging | prod
	HTTPAddr   string

	DatabaseURL string
	RedisURL    string

	S3Endpoint        string
	S3Region          string
	S3Bucket          string
	S3AccessKeyID     string
	S3SecretAccessKey string
	S3UsePathStyle    bool
	S3PublicBaseURL   string

	SMTPHost     string
	SMTPPort     int
	SMTPUsername string
	SMTPPassword string
	SMTPFrom     string

	SessionCookieSecret      string
	JWTSigningSecret         string
	DesignerWSTicketSecret   string

	TickHz                 int
	WALFlushTicks          int
	AOIRadiusTiles         int
	ReconnectGapTickLimit  int
}

// Load reads the environment and returns a Config. Returns an error only for
// values that fail to parse (e.g. malformed integers); missing values use
// dev defaults. Production deployments should set everything explicitly.
func Load() (Config, error) {
	cfg := Config{
		Env:      env("BOXLAND_ENV", "dev"),
		HTTPAddr: env("BOXLAND_HTTP_ADDR", ":8080"),

		// Defaults match the docker-compose dev stack, which intentionally uses
		// non-standard host ports (5433/6380) to avoid clashing with locally
		// installed Postgres / Redis on the developer's machine.
		DatabaseURL: env("DATABASE_URL", "postgres://boxland:boxland_dev@localhost:5433/boxland?sslmode=disable"),
		RedisURL:    env("REDIS_URL", "redis://localhost:6380/0"),

		S3Endpoint:        env("S3_ENDPOINT", "http://localhost:9000"),
		S3Region:          env("S3_REGION", "us-east-1"),
		S3Bucket:          env("S3_BUCKET", "boxland-assets"),
		S3AccessKeyID:     env("S3_ACCESS_KEY_ID", "boxland"),
		S3SecretAccessKey: env("S3_SECRET_ACCESS_KEY", "boxland_dev_secret"),
		S3PublicBaseURL:   env("S3_PUBLIC_BASE_URL", "http://localhost:9000/boxland-assets"),

		SMTPHost:     env("SMTP_HOST", "localhost"),
		SMTPUsername: env("SMTP_USERNAME", ""),
		SMTPPassword: env("SMTP_PASSWORD", ""),
		SMTPFrom:     env("SMTP_FROM", "noreply@boxland.local"),

		SessionCookieSecret:    env("SESSION_COOKIE_SECRET", ""),
		JWTSigningSecret:       env("JWT_SIGNING_SECRET", ""),
		DesignerWSTicketSecret: env("DESIGNER_WS_TICKET_SECRET", ""),
	}

	var err error
	if cfg.S3UsePathStyle, err = parseBool("S3_USE_PATH_STYLE", true); err != nil {
		return cfg, err
	}
	if cfg.SMTPPort, err = parseInt("SMTP_PORT", 1025); err != nil {
		return cfg, err
	}
	if cfg.TickHz, err = parseInt("TICK_HZ", 10); err != nil {
		return cfg, err
	}
	if cfg.WALFlushTicks, err = parseInt("WAL_FLUSH_TICKS", 20); err != nil {
		return cfg, err
	}
	if cfg.AOIRadiusTiles, err = parseInt("AOI_RADIUS_TILES", 24); err != nil {
		return cfg, err
	}
	if cfg.ReconnectGapTickLimit, err = parseInt("RECONNECT_GAP_TICK_LIMIT", 600); err != nil {
		return cfg, err
	}

	if cfg.Env == "prod" {
		if err := cfg.validateProd(); err != nil {
			return cfg, err
		}
	}
	return cfg, nil
}

// validateProd asserts that secrets are not the dev defaults / empty in prod.
func (c Config) validateProd() error {
	if c.SessionCookieSecret == "" || c.JWTSigningSecret == "" || c.DesignerWSTicketSecret == "" {
		return fmt.Errorf("prod env requires SESSION_COOKIE_SECRET, JWT_SIGNING_SECRET, DESIGNER_WS_TICKET_SECRET to be set")
	}
	return nil
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}

func parseBool(key string, def bool) (bool, error) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def, fmt.Errorf("env %s: %w", key, err)
	}
	return b, nil
}

func parseInt(key string, def int) (int, error) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def, fmt.Errorf("env %s: %w", key, err)
	}
	return n, nil
}
