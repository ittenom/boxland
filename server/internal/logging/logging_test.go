package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestFromEnvDefaults(t *testing.T) {
	t.Setenv("BOXLAND_LOG_FORMAT", "")
	t.Setenv("BOXLAND_LOG_LEVEL", "")
	cfg := FromEnv()
	if cfg.Format != FormatPretty {
		t.Errorf("default format: got %v, want FormatPretty", cfg.Format)
	}
	if cfg.Level != slog.LevelInfo {
		t.Errorf("default level: got %v, want Info", cfg.Level)
	}
}

func TestFromEnvJSON(t *testing.T) {
	t.Setenv("BOXLAND_LOG_FORMAT", "json")
	t.Setenv("BOXLAND_LOG_LEVEL", "debug")
	cfg := FromEnv()
	if cfg.Format != FormatJSON {
		t.Errorf("format: got %v, want FormatJSON", cfg.Format)
	}
	if cfg.Level != slog.LevelDebug {
		t.Errorf("level: got %v, want Debug", cfg.Level)
	}
}

func TestInitJSONOutput(t *testing.T) {
	var buf bytes.Buffer
	Init(Config{Format: FormatJSON, Level: slog.LevelInfo, Out: &buf})
	slog.Info("hello", "key", "value")

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("expected JSON output, got %q: %v", buf.String(), err)
	}
	if entry["msg"] != "hello" {
		t.Errorf("msg: got %v, want hello", entry["msg"])
	}
	if entry["key"] != "value" {
		t.Errorf("key: got %v, want value", entry["key"])
	}
}

func TestInitPrettyOutput(t *testing.T) {
	var buf bytes.Buffer
	Init(Config{Format: FormatPretty, Level: slog.LevelInfo, Out: &buf})
	slog.Info("hello", "key", "value")

	out := buf.String()
	if !strings.Contains(out, "hello") || !strings.Contains(out, "key=value") {
		t.Errorf("pretty output missing fields: %q", out)
	}
}

func TestLevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	Init(Config{Format: FormatJSON, Level: slog.LevelWarn, Out: &buf})
	slog.Info("filtered out")
	slog.Warn("kept")

	if strings.Contains(buf.String(), "filtered out") {
		t.Errorf("info-level message should have been filtered, got: %q", buf.String())
	}
	if !strings.Contains(buf.String(), "kept") {
		t.Errorf("warn-level message should have been kept, got: %q", buf.String())
	}
}
