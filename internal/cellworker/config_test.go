package cellworker

import (
	"errors"
	"testing"
	"time"
)

func validConfig() Config {
	return Config{
		PrincipalKey:  "p",
		BatchSize:     1,
		Concurrency:   1,
		MinBackoff:    time.Millisecond,
		MaxBackoff:    time.Second,
		ShutdownGrace: time.Second,
	}
}

func TestConfigValidate(t *testing.T) {
	cases := map[string]func(*Config){
		"missing principal key": func(c *Config) { c.PrincipalKey = "" },
		"zero batch size":       func(c *Config) { c.BatchSize = 0 },
		"zero concurrency":      func(c *Config) { c.Concurrency = 0 },
		"zero min backoff":      func(c *Config) { c.MinBackoff = 0 },
		"max below min backoff": func(c *Config) { c.MaxBackoff = c.MinBackoff - time.Nanosecond },
		"zero shutdown grace":   func(c *Config) { c.ShutdownGrace = 0 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := validConfig()
			mutate(&cfg)
			if err := cfg.Validate(); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("expected ErrInvalidConfig, got %v", err)
			}
		})
	}
}

func TestConfigValidateAcceptsValidConfig(t *testing.T) {
	if err := validConfig().Validate(); err != nil {
		t.Fatalf("expected valid config to pass, got %v", err)
	}
}
