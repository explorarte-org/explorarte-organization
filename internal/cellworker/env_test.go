package cellworker

import (
	"testing"
	"time"
)

func lookupFrom(values map[string]string) LookupEnv {
	return func(key string) (string, bool) {
		v, ok := values[key]
		return v, ok
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	cfg, err := LoadConfig(lookupFrom(nil), "worker-01/local")
	if err != nil {
		t.Fatal(err)
	}
	want := Config{
		PrincipalKey: "worker-01/local", BatchSize: defaultBatchSize, Concurrency: defaultConcurrency,
		MinBackoff: defaultMinBackoff, MaxBackoff: defaultMaxBackoff, ShutdownGrace: defaultShutdownGrace,
	}
	if cfg != want {
		t.Fatalf("cfg=%+v want=%+v", cfg, want)
	}
}

func TestLoadConfigOverrides(t *testing.T) {
	cfg, err := LoadConfig(lookupFrom(map[string]string{
		"ORG_MODEL_WORKER_BATCH_SIZE":     "25",
		"ORG_MODEL_WORKER_CONCURRENCY":    "4",
		"ORG_MODEL_WORKER_MIN_BACKOFF":    "500ms",
		"ORG_MODEL_WORKER_MAX_BACKOFF":    "10s",
		"ORG_MODEL_WORKER_SHUTDOWN_GRACE": "2m",
	}), "worker-01/local")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BatchSize != 25 || cfg.Concurrency != 4 || cfg.MinBackoff != 500*time.Millisecond || cfg.MaxBackoff != 10*time.Second || cfg.ShutdownGrace != 2*time.Minute {
		t.Fatalf("cfg=%+v", cfg)
	}
}

func TestLoadConfigRejectsInvalidValues(t *testing.T) {
	if _, err := LoadConfig(lookupFrom(map[string]string{"ORG_MODEL_WORKER_BATCH_SIZE": "not-a-number"}), "worker-01/local"); err == nil {
		t.Fatal("expected error for non-numeric batch size")
	}
	if _, err := LoadConfig(lookupFrom(map[string]string{"ORG_MODEL_WORKER_MIN_BACKOFF": "not-a-duration"}), "worker-01/local"); err == nil {
		t.Fatal("expected error for non-duration backoff")
	}
	if _, err := LoadConfig(lookupFrom(nil), ""); err == nil {
		t.Fatal("expected error for empty principal key")
	}
	if _, err := LoadConfig(lookupFrom(map[string]string{"ORG_MODEL_WORKER_MAX_BACKOFF": "1ms"}), "worker-01/local"); err == nil {
		t.Fatal("expected error when max backoff falls below min backoff")
	}
}
