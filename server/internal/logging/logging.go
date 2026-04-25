// Package logging configures the process-wide structured logger.
//
// Format and level come from environment variables (BOXLAND_LOG_FORMAT,
// BOXLAND_LOG_LEVEL). Production uses JSON; dev uses a pretty text handler.
// The default slog.Default() is replaced so any package using slog directly
// gets the configured handler.
package logging

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

// Format selects the slog handler.
type Format int

const (
	FormatPretty Format = iota
	FormatJSON
)

// Config controls the global slog.Default() handler.
type Config struct {
	Format Format
	Level  slog.Level
	Out    io.Writer // defaults to os.Stdout
}

// FromEnv reads BOXLAND_LOG_FORMAT and BOXLAND_LOG_LEVEL, returning a Config
// suitable for Init. Unknown values fall back to safe defaults.
func FromEnv() Config {
	cfg := Config{
		Format: FormatPretty,
		Level:  slog.LevelInfo,
		Out:    os.Stdout,
	}
	switch strings.ToLower(os.Getenv("BOXLAND_LOG_FORMAT")) {
	case "json":
		cfg.Format = FormatJSON
	case "pretty", "":
		cfg.Format = FormatPretty
	}
	switch strings.ToLower(os.Getenv("BOXLAND_LOG_LEVEL")) {
	case "debug":
		cfg.Level = slog.LevelDebug
	case "info", "":
		cfg.Level = slog.LevelInfo
	case "warn", "warning":
		cfg.Level = slog.LevelWarn
	case "error":
		cfg.Level = slog.LevelError
	}
	return cfg
}

// Init builds the configured handler and installs it as slog.Default().
// It returns the *slog.Logger for callers that want it explicitly.
func Init(cfg Config) *slog.Logger {
	out := cfg.Out
	if out == nil {
		out = os.Stdout
	}
	opts := &slog.HandlerOptions{
		Level:     cfg.Level,
		AddSource: cfg.Format == FormatPretty, // useful in dev, noisy in prod
	}
	var handler slog.Handler
	switch cfg.Format {
	case FormatJSON:
		handler = slog.NewJSONHandler(out, opts)
	default:
		handler = slog.NewTextHandler(out, opts)
	}
	logger := slog.New(handler)
	slog.SetDefault(logger)
	return logger
}
