package executionharness

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

// MemoryHistoryStore is a deterministic, concurrency-safe implementation for
// core validation. It is not represented as production-durable persistence.
type MemoryHistoryStore struct {
	mu     sync.Mutex
	events map[string][]Event
}

func NewMemoryHistoryStore() *MemoryHistoryStore {
	return &MemoryHistoryStore{events: make(map[string][]Event)}
}

func (s *MemoryHistoryStore) Append(ctx context.Context, runID string, expectedSequence uint64, event Event) (Event, error) {
	if err := ctx.Err(); err != nil {
		return Event{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.events[runID]
	if uint64(len(current)) != expectedSequence {
		return Event{}, fmt.Errorf("%w: expected %d, actual %d", ErrHistoryConflict, expectedSequence, len(current))
	}
	if event.RunID != runID || event.Sequence != 0 {
		return Event{}, fmt.Errorf("%w: invalid append identity or caller-assigned sequence", ErrHistoryCorrupt)
	}
	event.Sequence = expectedSequence + 1
	copyEvent, err := cloneEvent(event)
	if err != nil {
		return Event{}, err
	}
	s.events[runID] = append(current, copyEvent)
	return cloneEvent(copyEvent)
}

func (s *MemoryHistoryStore) Read(ctx context.Context, runID string) ([]Event, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]Event, len(s.events[runID]))
	for i, event := range s.events[runID] {
		copyEvent, err := cloneEvent(event)
		if err != nil {
			return nil, err
		}
		result[i] = copyEvent
	}
	return result, nil
}

func cloneEvent(event Event) (Event, error) {
	body, err := json.Marshal(event)
	if err != nil {
		return Event{}, err
	}
	var result Event
	if err := json.Unmarshal(body, &result); err != nil {
		return Event{}, err
	}
	return result, nil
}
