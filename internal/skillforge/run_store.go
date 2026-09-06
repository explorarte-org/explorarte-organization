package skillforge

import (
	"context"
	"errors"
	"sync"
	"time"
)

var (
	ErrRunNotFound = errors.New("forge run not found")
	ErrRunConflict = errors.New("forge run already exists")
)

type RunRepository interface {
	CreateRun(ctx context.Context, run ForgeRun) (ForgeRun, error)
	GetRun(ctx context.Context, organizationID, runID string) (ForgeRun, error)
	GetLatestRunByNeed(ctx context.Context, organizationID, needID string) (ForgeRun, error)
	SaveRun(ctx context.Context, run ForgeRun) (ForgeRun, error)
	RecordEvent(ctx context.Context, event Event) error
	ListEvents(ctx context.Context, organizationID, runID string) ([]Event, error)
}

type MemoryRunStore struct {
	mu     sync.Mutex
	runs   map[string]ForgeRun
	events map[string][]Event
}

func NewMemoryRunStore() *MemoryRunStore {
	return &MemoryRunStore{
		runs:   make(map[string]ForgeRun),
		events: make(map[string][]Event),
	}
}

func (m *MemoryRunStore) key(orgID, runID string) string {
	return orgID + ":" + runID
}

func (m *MemoryRunStore) CreateRun(_ context.Context, run ForgeRun) (ForgeRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	k := m.key(run.OrganizationID, run.ID)
	if _, exists := m.runs[k]; exists {
		return ForgeRun{}, ErrRunConflict
	}
	if run.Revision <= 0 {
		run.Revision = 1
	}
	if run.StartedAt.IsZero() {
		run.StartedAt = time.Now().UTC()
	}
	m.runs[k] = run
	return run, nil
}

func (m *MemoryRunStore) GetRun(_ context.Context, organizationID, runID string) (ForgeRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	k := m.key(organizationID, runID)
	run, exists := m.runs[k]
	if !exists {
		return ForgeRun{}, ErrRunNotFound
	}
	return run, nil
}

func (m *MemoryRunStore) GetLatestRunByNeed(_ context.Context, organizationID, needID string) (ForgeRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var latest ForgeRun
	var found bool
	for _, r := range m.runs {
		if r.OrganizationID == organizationID && r.NeedID == needID {
			if !found || r.StartedAt.After(latest.StartedAt) {
				latest = r
				found = true
			}
		}
	}
	if !found {
		return ForgeRun{}, ErrRunNotFound
	}
	return latest, nil
}

func (m *MemoryRunStore) SaveRun(_ context.Context, run ForgeRun) (ForgeRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	k := m.key(run.OrganizationID, run.ID)
	existing, exists := m.runs[k]
	if !exists {
		return ForgeRun{}, ErrRunNotFound
	}
	run.Revision = existing.Revision + 1
	m.runs[k] = run
	return run, nil
}

func (m *MemoryRunStore) RecordEvent(_ context.Context, ev Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	k := m.key(ev.OrganizationID, ev.RunID)
	ev.Sequence = int64(len(m.events[k]) + 1)
	if ev.RecordedAt.IsZero() {
		ev.RecordedAt = time.Now().UTC()
	}
	m.events[k] = append(m.events[k], ev)
	return nil
}

func (m *MemoryRunStore) ListEvents(_ context.Context, organizationID, runID string) ([]Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	k := m.key(organizationID, runID)
	evs := m.events[k]
	out := make([]Event, len(evs))
	copy(out, evs)
	return out, nil
}
