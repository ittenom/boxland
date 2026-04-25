package config

import (
	"strings"
	"testing"
)

func TestLoadDevDefaults(t *testing.T) {
	t.Setenv("BOXLAND_ENV", "dev")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.HTTPAddr != ":8080" {
		t.Errorf("HTTPAddr default: got %q", cfg.HTTPAddr)
	}
	if cfg.TickHz != 10 {
		t.Errorf("TickHz default: got %d, want 10", cfg.TickHz)
	}
	if cfg.WALFlushTicks != 20 {
		t.Errorf("WALFlushTicks default: got %d, want 20", cfg.WALFlushTicks)
	}
}

func TestLoadProdRequiresSecrets(t *testing.T) {
	t.Setenv("BOXLAND_ENV", "prod")
	t.Setenv("SESSION_COOKIE_SECRET", "")
	t.Setenv("JWT_SIGNING_SECRET", "")
	t.Setenv("DESIGNER_WS_TICKET_SECRET", "")

	_, err := Load()
	if err == nil {
		t.Fatal("expected validation error for empty prod secrets")
	}
	if !strings.Contains(err.Error(), "prod env requires") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestLoadProdAcceptsSecrets(t *testing.T) {
	t.Setenv("BOXLAND_ENV", "prod")
	t.Setenv("SESSION_COOKIE_SECRET", "x")
	t.Setenv("JWT_SIGNING_SECRET", "y")
	t.Setenv("DESIGNER_WS_TICKET_SECRET", "z")

	if _, err := Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
}

func TestParseBadInt(t *testing.T) {
	t.Setenv("TICK_HZ", "not-a-number")
	_, err := Load()
	if err == nil {
		t.Fatal("expected parse error for bad TICK_HZ")
	}
}
