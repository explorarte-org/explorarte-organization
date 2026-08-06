package modeldispatch

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	defaultAssignmentTTL         = 15 * time.Minute
	defaultAssignmentMaxTTL      = 24 * time.Hour
	defaultAssignmentExpireBatch = 100
)

type DispatchConfig struct {
	AssignmentDefaultTTL      time.Duration
	AssignmentMaxTTL          time.Duration
	AssignmentExpireBatchSize int
}

type LookupEnv func(string) (string, bool)

func LoadDispatchConfig(lookup LookupEnv) (DispatchConfig, error) {
	if lookup == nil {
		return DispatchConfig{}, errors.New("model dispatch environment lookup is nil")
	}
	cfg := DispatchConfig{AssignmentDefaultTTL: defaultAssignmentTTL, AssignmentMaxTTL: defaultAssignmentMaxTTL, AssignmentExpireBatchSize: defaultAssignmentExpireBatch}
	var err error
	if cfg.AssignmentDefaultTTL, err = envDuration(lookup, "ORG_MODEL_ASSIGNMENT_DEFAULT_TTL", defaultAssignmentTTL); err != nil {
		return DispatchConfig{}, err
	}
	if cfg.AssignmentMaxTTL, err = envDuration(lookup, "ORG_MODEL_ASSIGNMENT_MAX_TTL", defaultAssignmentMaxTTL); err != nil {
		return DispatchConfig{}, err
	}
	if cfg.AssignmentExpireBatchSize, err = envInt(lookup, "ORG_MODEL_ASSIGNMENT_EXPIRE_BATCH_SIZE", defaultAssignmentExpireBatch); err != nil {
		return DispatchConfig{}, err
	}
	if err = cfg.Validate(); err != nil {
		return DispatchConfig{}, err
	}
	return cfg, nil
}

func (c DispatchConfig) Validate() error {
	if c.AssignmentDefaultTTL <= 0 || c.AssignmentDefaultTTL > 24*time.Hour {
		return fmt.Errorf("%w: assignment default TTL outside allowed range", ErrInvalidRequest)
	}
	if c.AssignmentMaxTTL <= 0 || c.AssignmentMaxTTL > 7*24*time.Hour {
		return fmt.Errorf("%w: assignment max TTL outside allowed range", ErrInvalidRequest)
	}
	if c.AssignmentDefaultTTL > c.AssignmentMaxTTL {
		return fmt.Errorf("%w: assignment default TTL exceeds max TTL", ErrInvalidRequest)
	}
	if c.AssignmentExpireBatchSize < 1 || c.AssignmentExpireBatchSize > 10000 {
		return fmt.Errorf("%w: assignment expire batch size outside allowed range", ErrInvalidRequest)
	}
	return nil
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
