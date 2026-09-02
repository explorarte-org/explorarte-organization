// Package circuitbreaker is the single shared circuit-breaker implementation
// every compiled provider adapter (gemini, deepseek, xai, openaicompat,
// openairesponses) uses, replacing 5 byte-for-byte identical per-adapter
// copies of the same logic (G3-002). Each adapter still configures its own
// failure threshold and open duration via its own Config -- consolidation
// removes duplicated code, not per-adapter tuning.
package circuitbreaker

import (
	"sync"
	"time"
)

// Breaker is a simple consecutive-failure circuit breaker: after
// failureThreshold consecutive failures it opens for openDuration, then
// resets on the next allowed call regardless of outcome. Safe for
// concurrent use.
type Breaker struct {
	mu                sync.Mutex
	failureThreshold  int
	openDuration      time.Duration
	consecutiveErrors int
	openUntil         time.Time
}

// New returns a Breaker configured with the given adapter's own threshold
// and open duration (e.g. Config.FailureThreshold, Config.OpenDuration).
func New(threshold int, duration time.Duration) *Breaker {
	return &Breaker{failureThreshold: threshold, openDuration: duration}
}

// Allow reports whether a call may proceed right now. Calling it while the
// breaker is open-but-expired implicitly resets the breaker.
func (b *Breaker) Allow(now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.openUntil.IsZero() {
		return true
	}
	if now.Before(b.openUntil) {
		return false
	}
	b.openUntil = time.Time{}
	b.consecutiveErrors = 0
	return true
}

// Success resets the breaker to closed.
func (b *Breaker) Success() {
	b.mu.Lock()
	b.consecutiveErrors = 0
	b.openUntil = time.Time{}
	b.mu.Unlock()
}

// Failure records one failure, opening the breaker once failureThreshold
// consecutive failures have been recorded.
func (b *Breaker) Failure(now time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.consecutiveErrors++
	if b.consecutiveErrors >= b.failureThreshold {
		b.openUntil = now.Add(b.openDuration)
	}
}
