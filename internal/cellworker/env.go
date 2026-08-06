package cellworker

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type LookupEnv func(string) (string, bool)

const (
	defaultBatchSize     = 10
	defaultConcurrency   = 1
	defaultMinBackoff    = time.Second
	defaultMaxBackoff    = time.Minute
	defaultShutdownGrace = 30 * time.Second
)

// LoadConfig builds a Config from the environment. principalKey is passed in
// rather than re-read here: it is ORG_MODEL_EXECUTION_PRINCIPAL_KEY, already
// owned and parsed by internal/modelruntime.LoadRuntimeConfig, and this
// package must not duplicate that parsing or accept a second, possibly
// divergent, source of the dispatching identity.
func LoadConfig(lookup LookupEnv, principalKey string) (Config, error) {
	cfg := Config{
		PrincipalKey:  strings.TrimSpace(principalKey),
		BatchSize:     defaultBatchSize,
		Concurrency:   defaultConcurrency,
		MinBackoff:    defaultMinBackoff,
		MaxBackoff:    defaultMaxBackoff,
		ShutdownGrace: defaultShutdownGrace,
	}
	var err error
	if cfg.BatchSize, err = envInt(lookup, "ORG_MODEL_WORKER_BATCH_SIZE", cfg.BatchSize); err != nil {
		return Config{}, err
	}
	if cfg.Concurrency, err = envInt(lookup, "ORG_MODEL_WORKER_CONCURRENCY", cfg.Concurrency); err != nil {
		return Config{}, err
	}
	if cfg.MinBackoff, err = envDuration(lookup, "ORG_MODEL_WORKER_MIN_BACKOFF", cfg.MinBackoff); err != nil {
		return Config{}, err
	}
	if cfg.MaxBackoff, err = envDuration(lookup, "ORG_MODEL_WORKER_MAX_BACKOFF", cfg.MaxBackoff); err != nil {
		return Config{}, err
	}
	if cfg.ShutdownGrace, err = envDuration(lookup, "ORG_MODEL_WORKER_SHUTDOWN_GRACE", cfg.ShutdownGrace); err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func envInt(lookup LookupEnv, key string, fallback int) (int, error) {
	raw, ok := lookup(key)
	if !ok || strings.TrimSpace(raw) == "" {
		return fallback, nil
	}
	v, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return v, nil
}

func envDuration(lookup LookupEnv, key string, fallback time.Duration) (time.Duration, error) {
	raw, ok := lookup(key)
	if !ok || strings.TrimSpace(raw) == "" {
		return fallback, nil
	}
	v, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return v, nil
}
