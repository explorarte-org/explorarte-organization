package need

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	ErrNotFound         = errors.New("procedure need not found")
	ErrConflict         = errors.New("procedure need conflict")
	ErrRevisionMismatch = errors.New("procedure need revision mismatch")
)

type Repository interface {
	CreateNeed(ctx context.Context, need ProcedureNeed) (ProcedureNeed, error)
	GetNeed(ctx context.Context, organizationID, needID string) (ProcedureNeed, error)
	ListNeeds(ctx context.Context, organizationID string, status ProcedureNeedStatus) ([]ProcedureNeed, error)
	SaveNeed(ctx context.Context, need ProcedureNeed, expectedRevision int64) (ProcedureNeed, error)
}

type MemoryRepository struct {
	mu    sync.Mutex
	needs map[string]ProcedureNeed
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{needs: make(map[string]ProcedureNeed)}
}

func (m *MemoryRepository) key(orgID, needID string) string {
	return orgID + ":" + needID
}

func (m *MemoryRepository) CreateNeed(_ context.Context, n ProcedureNeed) (ProcedureNeed, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	k := m.key(n.OrganizationID, n.ID)
	if _, exists := m.needs[k]; exists {
		return ProcedureNeed{}, ErrConflict
	}

	if err := n.Validate(); err != nil {
		return ProcedureNeed{}, err
	}

	m.needs[k] = n
	return n, nil
}

func (m *MemoryRepository) GetNeed(_ context.Context, orgID, needID string) (ProcedureNeed, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	n, exists := m.needs[m.key(orgID, needID)]
	if !exists {
		return ProcedureNeed{}, ErrNotFound
	}
	return n, nil
}

func (m *MemoryRepository) ListNeeds(_ context.Context, orgID string, status ProcedureNeedStatus) ([]ProcedureNeed, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var out []ProcedureNeed
	for _, n := range m.needs {
		if n.OrganizationID == orgID {
			if status != "" && n.Status != status {
				continue
			}
			out = append(out, n)
		}
	}
	return out, nil
}

func (m *MemoryRepository) SaveNeed(_ context.Context, n ProcedureNeed, expectedRevision int64) (ProcedureNeed, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	k := m.key(n.OrganizationID, n.ID)
	existing, exists := m.needs[k]
	if !exists {
		return ProcedureNeed{}, ErrNotFound
	}

	if existing.Revision != expectedRevision {
		return ProcedureNeed{}, fmt.Errorf("%w: expected %d, current %d", ErrRevisionMismatch, expectedRevision, existing.Revision)
	}

	n.Revision = existing.Revision + 1
	n.UpdatedAt = time.Now().UTC()
	if err := n.Validate(); err != nil {
		return ProcedureNeed{}, err
	}

	m.needs[k] = n
	return n, nil
}
