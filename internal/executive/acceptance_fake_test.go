package executive

import (
	"context"
	"sync"
)

// memoryAcceptance is the in-memory AcceptanceRecorder tests run against.
//
// It keeps the same idempotency the durable store has: a second record for a
// root that already has one is ignored rather than overwriting it, so a test
// that resumes a submit observes what the first one stored.
type memoryAcceptance struct {
	mu       sync.Mutex
	recorded map[int64][]AcceptanceCriterion
}

func newMemoryAcceptance() *memoryAcceptance {
	return &memoryAcceptance{recorded: map[int64][]AcceptanceCriterion{}}
}

func (m *memoryAcceptance) RecordAcceptance(_ context.Context, rootTaskID int64, criteria []AcceptanceCriterion) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.recorded[rootTaskID]; exists {
		return nil
	}
	m.recorded[rootTaskID] = append([]AcceptanceCriterion(nil), criteria...)
	return nil
}

func (m *memoryAcceptance) Acceptance(_ context.Context, rootTaskID int64) ([]AcceptanceCriterion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]AcceptanceCriterion(nil), m.recorded[rootTaskID]...), nil
}
