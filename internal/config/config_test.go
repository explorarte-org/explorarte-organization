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
	if cfg.Registry.CanonicalDir != "docs/canonical" || cfg.Registry.SyncTimeout != 30*time.Second {
		t.Fatalf("unexpected registry defaults: %+v", cfg.Registry)
	}
	if cfg.Tasks.OrganizationID != "explorarte" || !cfg.Tasks.ReconcilerEnabled || cfg.Tasks.ReconcileInterval != 5*time.Second || cfg.Tasks.ReconcileBatchSize != 100 {
		t.Fatalf("unexpected task defaults: %+v", cfg.Tasks)
	}
	if cfg.Tasks.DefaultMaxAttempts != 5 || cfg.Tasks.DefaultLeaseDuration != 2*time.Minute || cfg.Tasks.MaxLeaseDuration != 15*time.Minute {
		t.Fatalf("unexpected task retry/lease defaults: %+v", cfg.Tasks)
	}
	if cfg.Staging.Enabled || cfg.Staging.ReconcileBatchSize != 100 || cfg.Staging.MaxArtifactBytes != 64<<20 || cfg.Staging.GitBinary != "git" {
		t.Fatalf("unexpected staging defaults: %+v", cfg.Staging)
	}
}

func TestLoadFromOverrides(t *testing.T) {
	values := map[string]string{
		"ORG_APP_NAME":                        "kernel-test",
		"ORG_ENVIRONMENT":                     "test",
		"ORG_HTTP_ADDR":                       "127.0.0.1:9090",
		"ORG_SHUTDOWN_TIMEOUT":                "7s",
		"ORG_LOG_LEVEL":                       "debug",
		"ORG_LOG_FORMAT":                      "text",
		"ORG_DATABASE_HOST":                   "postgres.internal",
		"ORG_DATABASE_PORT":                   "5544",
		"ORG_DATABASE_NAME":                   "org_test",
		"ORG_DATABASE_USER":                   "org_app",
		"ORG_DATABASE_PASSWORD":               "secret with symbols:/@",
		"ORG_DATABASE_SSLMODE":                "require",
		"ORG_DATABASE_MAX_CONNS":              "6",
		"ORG_DATABASE_MIN_CONNS":              "0",
		"ORG_DATABASE_AUTO_MIGRATE":           "false",
		"ORG_DATABASE_MIGRATION_TIMEOUT":      "20s",
		"ORG_CANONICAL_DIR":                   "/opt/explorarte/docs/canonical",
		"ORG_REGISTRY_SYNC_TIMEOUT":           "17s",
		"ORG_TASK_ORGANIZATION_ID":            "explorarte",
		"ORG_TASK_RECONCILER_ENABLED":         "false",
		"ORG_TASK_RECONCILE_INTERVAL":         "11s",
		"ORG_TASK_RECONCILE_BATCH_SIZE":       "77",
		"ORG_TASK_DEFAULT_MAX_ATTEMPTS":       "7",
		"ORG_TASK_DEFAULT_LEASE_DURATION":     "3m",
		"ORG_TASK_MAX_LEASE_DURATION":         "20m",
		"ORG_TASK_RETRY_BASE_DELAY":           "9s",
		"ORG_TASK_RETRY_MAX_DELAY":            "12m",
		"ORG_TASK_OUTBOX_MAX_ATTEMPTS":        "8",
		"ORG_TASK_OUTBOX_CLAIM_DURATION":      "90s",
		"ORG_TASK_COMMAND_TIMEOUT":            "40s",
		"ORG_AUTHORIZATION_DEFAULT_TTL":       "45m",
		"ORG_AUTHORIZATION_MAX_TTL":           "12h",
		"ORG_AUTHORIZATION_COMMAND_TIMEOUT":   "25s",
		"ORG_AUTHORIZATION_EXPIRE_BATCH_SIZE": "77",
		"ORG_STAGING_ENABLED":                 "true",
		"ORG_STAGING_REPOSITORIES_FILE":       "/tmp/explorarte/repos.yaml",
		"ORG_STAGING_WORKSPACE_ROOT":          "/tmp/explorarte/workspaces",
		"ORG_STAGING_ARTIFACT_ROOT":           "/tmp/explorarte/artifacts",
		"ORG_STAGING_QUARANTINE_ROOT":         "/tmp/explorarte/quarantine",
		"ORG_STAGING_COMMAND_TIMEOUT":         "45s",
		"ORG_STAGING_MAX_ARTIFACT_BYTES":      "1048576",
		"ORG_STAGING_MAX_CHANGED_FILES":       "25",
		"ORG_STAGING_STALE_AFTER":             "10m",
		"ORG_STAGING_RECONCILE_INTERVAL":      "13s",
		"ORG_STAGING_RECONCILE_BATCH_SIZE":    "21",
		"ORG_STAGING_GIT_BINARY":              "/usr/bin/git",
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
	if cfg.Registry.CanonicalDir != "/opt/explorarte/docs/canonical" || cfg.Registry.SyncTimeout != 17*time.Second {
		t.Fatalf("unexpected registry config: %+v", cfg.Registry)
	}
	if cfg.Tasks.ReconcilerEnabled || cfg.Tasks.ReconcileInterval != 11*time.Second || cfg.Tasks.ReconcileBatchSize != 77 || cfg.Tasks.DefaultMaxAttempts != 7 {
		t.Fatalf("unexpected task config: %+v", cfg.Tasks)
	}
	if cfg.Tasks.DefaultLeaseDuration != 3*time.Minute || cfg.Tasks.MaxLeaseDuration != 20*time.Minute || cfg.Tasks.RetryBaseDelay != 9*time.Second || cfg.Tasks.RetryMaxDelay != 12*time.Minute {
		t.Fatalf("unexpected task durations: %+v", cfg.Tasks)
	}
	if cfg.Authorization.DefaultTTL != 45*time.Minute || cfg.Authorization.MaxTTL != 12*time.Hour || cfg.Authorization.CommandTimeout != 25*time.Second || cfg.Authorization.ExpireBatchSize != 77 {
		t.Fatalf("unexpected authorization config: %+v", cfg.Authorization)
	}
	if !cfg.Staging.Enabled || cfg.Staging.CommandTimeout != 45*time.Second || cfg.Staging.MaxArtifactBytes != 1048576 || cfg.Staging.MaxChangedFiles != 25 || cfg.Staging.ReconcileBatchSize != 21 || cfg.Staging.GitBinary != "/usr/bin/git" {
		t.Fatalf("unexpected staging config: %+v", cfg.Staging)
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
		"invalid address":                   {"ORG_HTTP_ADDR": "8080"},
		"invalid duration":                  {"ORG_SHUTDOWN_TIMEOUT": "soon"},
		"zero duration":                     {"ORG_HTTP_READ_TIMEOUT": "0s"},
		"invalid level":                     {"ORG_LOG_LEVEL": "verbose"},
		"invalid format":                    {"ORG_LOG_FORMAT": "yaml"},
		"invalid database port":             {"ORG_DATABASE_PORT": "70000"},
		"invalid max conns":                 {"ORG_DATABASE_MAX_CONNS": "0"},
		"min exceeds max":                   {"ORG_DATABASE_MAX_CONNS": "2", "ORG_DATABASE_MIN_CONNS": "3"},
		"invalid ssl mode":                  {"ORG_DATABASE_SSLMODE": "trust-me"},
		"invalid boolean":                   {"ORG_DATABASE_AUTO_MIGRATE": "sometimes"},
		"invalid database URL":              {"ORG_DATABASE_URL": "mysql://localhost/db"},
		"empty canonical dir":               {"ORG_CANONICAL_DIR": "   "},
		"invalid registry timeout":          {"ORG_REGISTRY_SYNC_TIMEOUT": "0s"},
		"invalid task organization":         {"ORG_TASK_ORGANIZATION_ID": "Bad ID"},
		"invalid reconciler flag":           {"ORG_TASK_RECONCILER_ENABLED": "sometimes"},
		"invalid reconcile batch":           {"ORG_TASK_RECONCILE_BATCH_SIZE": "0"},
		"invalid task attempts":             {"ORG_TASK_DEFAULT_MAX_ATTEMPTS": "101"},
		"invalid lease ordering":            {"ORG_TASK_DEFAULT_LEASE_DURATION": "20m", "ORG_TASK_MAX_LEASE_DURATION": "5m"},
		"invalid retry ordering":            {"ORG_TASK_RETRY_BASE_DELAY": "20m", "ORG_TASK_RETRY_MAX_DELAY": "1m"},
		"invalid authorization default ttl": {"ORG_AUTHORIZATION_DEFAULT_TTL": "0s"},
		"invalid authorization max ttl":     {"ORG_AUTHORIZATION_DEFAULT_TTL": "2h", "ORG_AUTHORIZATION_MAX_TTL": "1h"},
		"invalid authorization timeout":     {"ORG_AUTHORIZATION_COMMAND_TIMEOUT": "0s"},
		"invalid authorization batch":       {"ORG_AUTHORIZATION_EXPIRE_BATCH_SIZE": "1001"},
		"invalid staging flag":              {"ORG_STAGING_ENABLED": "sometimes"},
		"relative staging root":             {"ORG_STAGING_WORKSPACE_ROOT": "relative/path"},
		"overlapping staging roots":         {"ORG_STAGING_WORKSPACE_ROOT": "/tmp/staging", "ORG_STAGING_ARTIFACT_ROOT": "/tmp/staging/artifacts"},
		"invalid staging batch":             {"ORG_STAGING_RECONCILE_BATCH_SIZE": "501"},
		"invalid staging git binary":        {"ORG_STAGING_GIT_BINARY": "git --no-pager"},
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
