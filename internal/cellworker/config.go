package cellworker

import (
	"fmt"
	"time"
)

// Config governs the poll/dispatch loop. It carries no provider, model, or
// credential detail — those stay entirely inside Rama 12's Dispatcher
// implementation.
type Config struct {
	// PrincipalKey identifies which execution principal this worker process
	// claims work as (mirrors ORG_MODEL_EXECUTION_PRINCIPAL_KEY, Rama 10).
	PrincipalKey string

	// BatchSize bounds how many eligible invocation IDs are requested per poll.
	BatchSize int

	// Concurrency bounds how many Dispatch calls may be in flight at once.
	Concurrency int

	// MinBackoff/MaxBackoff bound the poll interval used when a poll finds no
	// eligible work or fails transiently. Backoff resets to MinBackoff after
	// any poll that returns work.
	MinBackoff time.Duration
	MaxBackoff time.Duration

	// ShutdownGrace bounds how long an in-flight Dispatch call may continue
	// running, detached from the caller's context, after Run's context is
	// cancelled. It does not bound how long Run itself waits to return; Run
	// always waits for every in-flight Dispatch to finish or hit this grace
	// deadline before returning.
	ShutdownGrace time.Duration
}

func (c Config) Validate() error {
	if c.PrincipalKey == "" {
		return fmt.Errorf("%w: principal key is required", ErrInvalidConfig)
	}
	if c.BatchSize < 1 {
		return fmt.Errorf("%w: batch size must be positive", ErrInvalidConfig)
	}
	if c.Concurrency < 1 {
		return fmt.Errorf("%w: concurrency must be positive", ErrInvalidConfig)
	}
	if c.MinBackoff <= 0 {
		return fmt.Errorf("%w: min backoff must be positive", ErrInvalidConfig)
	}
	if c.MaxBackoff < c.MinBackoff {
		return fmt.Errorf("%w: max backoff must be >= min backoff", ErrInvalidConfig)
	}
	if c.ShutdownGrace <= 0 {
		return fmt.Errorf("%w: shutdown grace must be positive", ErrInvalidConfig)
	}
	return nil
}
