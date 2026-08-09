package gemini

import (
	"sync"
	"time"
)

// circuitBreaker mirrors internal/modelruntime/adapter/gemini/breaker.go —
// duplicated deliberately rather than shared, since embeddingruntime must
// not depend on modelruntime (see port.go's package doc).
type circuitBreaker struct {
	mu                sync.Mutex
	failureThreshold  int
	openDuration      time.Duration
	consecutiveErrors int
	openUntil         time.Time
}

func newCircuitBreaker(threshold int, duration time.Duration) *circuitBreaker {
	return &circuitBreaker{failureThreshold: threshold, openDuration: duration}
}

func (b *circuitBreaker) allow(now time.Time) bool {
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

func (b *circuitBreaker) success() {
	b.mu.Lock()
	b.consecutiveErrors = 0
	b.openUntil = time.Time{}
	b.mu.Unlock()
}

func (b *circuitBreaker) failure(now time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.consecutiveErrors++
	if b.consecutiveErrors >= b.failureThreshold {
		b.openUntil = now.Add(b.openDuration)
	}
}
