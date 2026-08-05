package modelruntime

import (
	"testing"
	"time"
)

func TestLoadRuntimeConfigDefaultsAndOverrides(t *testing.T) {
	lookup := func(key string) (string, bool) {
		values := map[string]string{
			"ORG_MODEL_RUNTIME_ENABLED":              "true",
			"ORG_MODEL_RUNTIME_COMMAND_TIMEOUT":      "45s",
			"ORG_MODEL_RUNTIME_GLOBAL_CONCURRENCY":   "7",
			"ORG_MODEL_RUNTIME_MAX_RESPONSE_BYTES":   "2097152",
			"ORG_MODEL_RUNTIME_MAX_TOOL_INTENTS":     "3",
			"ORG_MODEL_RUNTIME_CLAIM_TTL":            "90s",
			"ORG_MODEL_RUNTIME_RECONCILE_BATCH_SIZE": "23",
		}
		value, ok := values[key]
		return value, ok
	}
	cfg, err := LoadRuntimeConfig(lookup, 11)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Enabled || cfg.CommandTimeout != 45*time.Second || cfg.GlobalConcurrency != 7 || cfg.MaxResponseBytes != 2<<20 || cfg.MaxToolIntents != 3 || cfg.ClaimTTL != 90*time.Second || cfg.ReconcileBatchSize != 23 || cfg.OutboxMaxAttempts != 11 {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

func TestLoadRuntimeConfigRejectsUnknownRanges(t *testing.T) {
	lookup := func(key string) (string, bool) {
		if key == "ORG_MODEL_RUNTIME_GLOBAL_CONCURRENCY" {
			return "0", true
		}
		return "", false
	}
	if _, err := LoadRuntimeConfig(lookup, 10); err == nil {
		t.Fatal("expected invalid concurrency")
	}
}
