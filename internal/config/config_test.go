package config

import (
	"log/slog"
	"net/url"
	"strings"
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
	if cfg.Database.MaxConns != 8 || cfg.Database.MinConns != 1 {
		t.Fatalf("database pool = %d/%d, want 1/8", cfg.Database.MinConns, cfg.Database.MaxConns)
	}
	if !cfg.Database.AutoMigrate {
		t.Fatal("Database.AutoMigrate = false, want true")
	}
}

func TestLoadFromOverrides(t *testing.T) {
	values := map[string]string{
		"ORG_APP_NAME":                   "kernel-test",
		"ORG_ENVIRONMENT":                "test",
		"ORG_HTTP_ADDR":                  "127.0.0.1:9090",
		"ORG_SHUTDOWN_TIMEOUT":           "7s",
		"ORG_LOG_LEVEL":                  "debug",
		"ORG_LOG_FORMAT":                 "text",
		"ORG_DATABASE_HOST":              "postgres.internal",
		"ORG_DATABASE_PORT":              "5544",
		"ORG_DATABASE_NAME":              "org_test",
		"ORG_DATABASE_USER":              "org_app",
		"ORG_DATABASE_PASSWORD":          "secret with symbols:/@",
		"ORG_DATABASE_SSLMODE":           "require",
		"ORG_DATABASE_MAX_CONNS":         "6",
		"ORG_DATABASE_MIN_CONNS":         "0",
		"ORG_DATABASE_AUTO_MIGRATE":      "false",
		"ORG_DATABASE_MIGRATION_TIMEOUT": "20s",
	}
	cfg, err := LoadFrom(mapLookup(values))
	if err != nil {
		t.Fatalf("LoadFrom() error = %v", err)
	}
	if cfg.App.Name != "kernel-test" || cfg.App.ShutdownTimeout != 7*time.Second {
		t.Fatalf("unexpected app config: %+v", cfg.App)
	}
	if cfg.Logging.Level != slog.LevelDebug || cfg.Logging.Format != "text" {
		t.Fatalf("unexpected logging config: %+v", cfg.Logging)
	}
	if cfg.Database.Port != 5544 || cfg.Database.MaxConns != 6 || cfg.Database.MinConns != 0 {
		t.Fatalf("unexpected database config: %+v", cfg.Database)
	}
	if cfg.Database.AutoMigrate {
		t.Fatal("Database.AutoMigrate = true, want false")
	}
	parsed, err := url.Parse(cfg.Database.ConnectionString())
	if err != nil {
		t.Fatalf("parse ConnectionString(): %v", err)
	}
	password, ok := parsed.User.Password()
	if !ok || password != "secret with symbols:/@" {
		t.Fatal("encoded password round-trip failed")
	}
	if parsed.Host != "postgres.internal:5544" || parsed.Query().Get("sslmode") != "require" {
		t.Fatalf("unexpected connection URL: %s", parsed.Redacted())
	}
}

func TestDatabaseURLOverride(t *testing.T) {
	cfg, err := LoadFrom(mapLookup(map[string]string{
		"ORG_DATABASE_URL": "postgres://app:secret@db.example:5432/org?sslmode=verify-full",
	}))
	if err != nil {
		t.Fatalf("LoadFrom() error = %v", err)
	}
	if cfg.Database.ConnectionString() != "postgres://app:secret@db.example:5432/org?sslmode=verify-full" {
		t.Fatal("URL override was changed")
	}
}

func TestLoadFromRejectsInvalidValues(t *testing.T) {
	tests := map[string]map[string]string{
		"invalid address":       {"ORG_HTTP_ADDR": "8080"},
		"invalid duration":      {"ORG_SHUTDOWN_TIMEOUT": "soon"},
		"zero duration":         {"ORG_HTTP_READ_TIMEOUT": "0s"},
		"invalid level":         {"ORG_LOG_LEVEL": "verbose"},
		"invalid format":        {"ORG_LOG_FORMAT": "yaml"},
		"invalid database port": {"ORG_DATABASE_PORT": "70000"},
		"invalid max conns":     {"ORG_DATABASE_MAX_CONNS": "0"},
		"min exceeds max":       {"ORG_DATABASE_MAX_CONNS": "2", "ORG_DATABASE_MIN_CONNS": "3"},
		"invalid ssl mode":      {"ORG_DATABASE_SSLMODE": "trust-me"},
		"invalid boolean":       {"ORG_DATABASE_AUTO_MIGRATE": "sometimes"},
		"invalid database URL":  {"ORG_DATABASE_URL": "mysql://localhost/db"},
	}
	for name, values := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := LoadFrom(mapLookup(values))
			if err == nil {
				t.Fatal("LoadFrom() error = nil, want error")
			}
		})
	}
}

func TestConnectionStringCanBeSafelyRedacted(t *testing.T) {
	cfg, err := LoadFrom(mapLookup(map[string]string{"ORG_DATABASE_PASSWORD": "top-secret"}))
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(cfg.Database.ConnectionString())
	if err != nil {
		t.Fatal(err)
	}
	if got := parsed.Redacted(); got == cfg.Database.ConnectionString() || strings.Contains(got, "top-secret") {
		t.Fatalf("connection string was not redacted: %q", got)
	}
}

func mapLookup(values map[string]string) LookupEnv {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}
