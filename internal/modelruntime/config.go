package modelruntime

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	defaultCommandTimeout      = 30 * time.Second
	defaultGlobalConcurrency   = 4
	defaultMaxResponseBytes    = 1 << 20
	defaultMaxToolIntents      = 8
	defaultClaimTTL            = 2 * time.Minute
	defaultReconcileBatchSize  = 100
	maxConfiguredResponseBytes = 16 << 20
)

type RuntimeConfig struct {
	Enabled               bool
	CommandTimeout        time.Duration
	GlobalConcurrency     int
	MaxResponseBytes      int
	MaxToolIntents        int
	ClaimTTL              time.Duration
	ReconcileBatchSize    int
	OutboxMaxAttempts     int
	ExecutionPrincipalKey string
}

type LookupEnv func(string) (string, bool)

func LoadRuntimeConfig(lookup LookupEnv, outboxMaxAttempts int) (RuntimeConfig, error) {
	if lookup == nil {
		return RuntimeConfig{}, errors.New("model runtime environment lookup is nil")
	}
	cfg := RuntimeConfig{Enabled: false, CommandTimeout: defaultCommandTimeout, GlobalConcurrency: defaultGlobalConcurrency, MaxResponseBytes: defaultMaxResponseBytes, MaxToolIntents: defaultMaxToolIntents, ClaimTTL: defaultClaimTTL, ReconcileBatchSize: defaultReconcileBatchSize, OutboxMaxAttempts: outboxMaxAttempts}
	var err error
	if cfg.Enabled, err = envBool(lookup, "ORG_MODEL_RUNTIME_ENABLED", false); err != nil {
		return RuntimeConfig{}, err
	}
	if cfg.CommandTimeout, err = envDuration(lookup, "ORG_MODEL_RUNTIME_COMMAND_TIMEOUT", defaultCommandTimeout); err != nil {
		return RuntimeConfig{}, err
	}
	if cfg.GlobalConcurrency, err = envInt(lookup, "ORG_MODEL_RUNTIME_GLOBAL_CONCURRENCY", defaultGlobalConcurrency); err != nil {
		return RuntimeConfig{}, err
	}
	if cfg.MaxResponseBytes, err = envInt(lookup, "ORG_MODEL_RUNTIME_MAX_RESPONSE_BYTES", defaultMaxResponseBytes); err != nil {
		return RuntimeConfig{}, err
	}
	if cfg.MaxToolIntents, err = envInt(lookup, "ORG_MODEL_RUNTIME_MAX_TOOL_INTENTS", defaultMaxToolIntents); err != nil {
		return RuntimeConfig{}, err
	}
	if cfg.ClaimTTL, err = envDuration(lookup, "ORG_MODEL_RUNTIME_CLAIM_TTL", defaultClaimTTL); err != nil {
		return RuntimeConfig{}, err
	}
	if cfg.ReconcileBatchSize, err = envInt(lookup, "ORG_MODEL_RUNTIME_RECONCILE_BATCH_SIZE", defaultReconcileBatchSize); err != nil {
		return RuntimeConfig{}, err
	}
	if raw, ok := lookup("ORG_MODEL_EXECUTION_PRINCIPAL_KEY"); ok {
		cfg.ExecutionPrincipalKey = strings.TrimSpace(raw)
	}
	if err = cfg.Validate(); err != nil {
		return RuntimeConfig{}, err
	}
	return cfg, nil
}

func (c RuntimeConfig) Validate() error {
	if c.CommandTimeout <= 0 || c.CommandTimeout > 30*time.Minute {
		return fmt.Errorf("%w: command timeout outside allowed range", ErrInvalidRequest)
	}
	if c.GlobalConcurrency < 1 || c.GlobalConcurrency > 1024 {
		return fmt.Errorf("%w: global concurrency outside allowed range", ErrInvalidRequest)
	}
	if c.MaxResponseBytes < 1024 || c.MaxResponseBytes > maxConfiguredResponseBytes {
		return fmt.Errorf("%w: maximum response bytes outside allowed range", ErrInvalidRequest)
	}
	if c.MaxToolIntents < 0 || c.MaxToolIntents > 128 {
		return fmt.Errorf("%w: maximum tool intents outside allowed range", ErrInvalidRequest)
	}
	if c.ClaimTTL < 5*time.Second || c.ClaimTTL > 30*time.Minute {
		return fmt.Errorf("%w: claim TTL outside allowed range", ErrInvalidRequest)
	}
	if c.ReconcileBatchSize < 1 || c.ReconcileBatchSize > 10000 {
		return fmt.Errorf("%w: reconcile batch size outside allowed range", ErrInvalidRequest)
	}
	if c.OutboxMaxAttempts < 1 || c.OutboxMaxAttempts > 100 {
		return fmt.Errorf("%w: outbox attempts outside allowed range", ErrInvalidRequest)
	}
	return nil
}

func envBool(lookup LookupEnv, key string, fallback bool) (bool, error) {
	raw, ok := lookup(key)
	if !ok || strings.TrimSpace(raw) == "" {
		return fallback, nil
	}
	v, err := strconv.ParseBool(strings.TrimSpace(raw))
	if err != nil {
		return false, fmt.Errorf("%s: %w", key, err)
	}
	return v, nil
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
