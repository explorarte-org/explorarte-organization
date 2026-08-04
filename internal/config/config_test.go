package config

import (
	"log/slog"
	"testing"
	"time"
)

func TestLoadFromDefaults(t *testing.T) {
	cfg, err := LoadFrom(func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatalf("LoadFrom() error = %v", err)
	}

	if cfg.App.Name != defaultAppName {
		t.Fatalf("App.Name = %q, want %q", cfg.App.Name, defaultAppName)
	}
	if cfg.HTTP.Addr != defaultHTTPAddr {
		t.Fatalf("HTTP.Addr = %q, want %q", cfg.HTTP.Addr, defaultHTTPAddr)
	}
	if cfg.Logging.Level != slog.LevelInfo {
		t.Fatalf("Logging.Level = %v, want %v", cfg.Logging.Level, slog.LevelInfo)
	}
}

func TestLoadFromOverrides(t *testing.T) {
	values := map[string]string{
		"ORG_APP_NAME":         "kernel-test",
		"ORG_ENVIRONMENT":      "test",
		"ORG_HTTP_ADDR":        "127.0.0.1:9090",
		"ORG_SHUTDOWN_TIMEOUT": "7s",
		"ORG_LOG_LEVEL":        "debug",
		"ORG_LOG_FORMAT":       "text",
	}

	cfg, err := LoadFrom(func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	})
	if err != nil {
		t.Fatalf("LoadFrom() error = %v", err)
	}

	if cfg.App.Name != "kernel-test" {
		t.Fatalf("App.Name = %q, want kernel-test", cfg.App.Name)
	}
	if cfg.App.ShutdownTimeout != 7*time.Second {
		t.Fatalf("ShutdownTimeout = %s, want 7s", cfg.App.ShutdownTimeout)
	}
	if cfg.Logging.Level != slog.LevelDebug {
		t.Fatalf("Logging.Level = %v, want debug", cfg.Logging.Level)
	}
	if cfg.Logging.Format != "text" {
		t.Fatalf("Logging.Format = %q, want text", cfg.Logging.Format)
	}
}

func TestLoadFromRejectsInvalidValues(t *testing.T) {
	tests := map[string]map[string]string{
		"invalid address":  {"ORG_HTTP_ADDR": "8080"},
		"invalid duration": {"ORG_SHUTDOWN_TIMEOUT": "soon"},
		"zero duration":    {"ORG_HTTP_READ_TIMEOUT": "0s"},
		"invalid level":    {"ORG_LOG_LEVEL": "verbose"},
		"invalid format":   {"ORG_LOG_FORMAT": "yaml"},
	}

	for name, values := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := LoadFrom(func(key string) (string, bool) {
				value, ok := values[key]
				return value, ok
			})
			if err == nil {
				t.Fatal("LoadFrom() error = nil, want error")
			}
		})
	}
}
