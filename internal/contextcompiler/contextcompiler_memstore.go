package contextcompiler

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// MemoryStore is an in-process ExecutionContextViewStore implementing the
// exact same idempotency/drift/integrity contract as the PostgreSQL store
// (internal/contextcompiler/postgres), for fast unit tests that do not need
// a live database. It is not wired into any productive bootstrap path.
type MemoryStore struct {
	mu     sync.Mutex
	nextID int64
	bySnap map[int64]ExecutionContextView
	byID   map[int64]ExecutionContextView
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{bySnap: map[int64]ExecutionContextView{}, byID: map[int64]ExecutionContextView{}}
}

func (s *MemoryStore) Persist(_ context.Context, view ExecutionContextView) (ExecutionContextView, error) {
	if view.OrganizationID == "" || view.ContextSnapshotID <= 0 {
		return ExecutionContextView{}, fmt.Errorf("execution context view requires organization_id and context_snapshot_id")
	}
	if err := ValidateIntegrity(view); err != nil {
		return ExecutionContextView{}, err
	}
	if view.SegmentDiffs == nil {
		view.SegmentDiffs = []SegmentDiff{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.bySnap[view.ContextSnapshotID]; ok {
		if !SameLogicalView(existing, view) {
			return ExecutionContextView{}, fmt.Errorf("%w: snapshot=%d", ErrExecutionContextViewDrift, view.ContextSnapshotID)
		}
		return existing, nil
	}
	s.nextID++
	view.ID = s.nextID
	view.CreatedAt = time.Now().UTC()
	// Store a defensive copy of the byte slice so a caller mutating its own
	// slice afterward cannot corrupt the durable record in place -- the
	// same "immutable once written" guarantee the PostgreSQL BEFORE
	// UPDATE/DELETE trigger enforces at the database layer.
	stored := view
	stored.ProviderVisibleBytes = append([]byte(nil), view.ProviderVisibleBytes...)
	s.bySnap[view.ContextSnapshotID] = stored
	s.byID[view.ID] = stored
	return stored, nil
}

func (s *MemoryStore) Get(_ context.Context, id int64) (ExecutionContextView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.byID[id]
	if !ok {
		return ExecutionContextView{}, ErrExecutionContextViewNotFound
	}
	return verifyIntegrity(v)
}

func (s *MemoryStore) GetByContextSnapshot(_ context.Context, organizationID string, contextSnapshotID int64) (ExecutionContextView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.bySnap[contextSnapshotID]
	if !ok || v.OrganizationID != organizationID {
		return ExecutionContextView{}, ErrExecutionContextViewNotFound
	}
	return verifyIntegrity(v)
}

// CorruptForTest overwrites a stored view's bytes, digest, or declared byte
// count directly, bypassing Persist's integrity check -- exists ONLY so
// tests can prove Get/GetByContextSnapshot reject tampered records on read
// (section 9E). byteCount < 0 means "leave it unchanged".
func (s *MemoryStore) CorruptForTest(id int64, bytes []byte, digest string, byteCount int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.byID[id]
	if !ok {
		return
	}
	if bytes != nil {
		v.ProviderVisibleBytes = bytes
	}
	if digest != "" {
		v.ProviderVisibleDigest = digest
	}
	if byteCount >= 0 {
		v.ProviderVisibleByteCount = byteCount
	}
	s.byID[id] = v
	s.bySnap[v.ContextSnapshotID] = v
}

func verifyIntegrity(v ExecutionContextView) (ExecutionContextView, error) {
	if err := ValidateIntegrity(v); err != nil {
		return ExecutionContextView{}, fmt.Errorf("view id=%d: %w", v.ID, err)
	}
	return v, nil
}

// memSHA256Hex is kept as a thin alias so existing test call sites do not
// need to change; ValidateIntegrity/sha256Hex (contextcompiler_view.go) are
// the actual single implementation.
func memSHA256Hex(b []byte) string { return sha256Hex(b) }

var _ ExecutionContextViewStore = (*MemoryStore)(nil)
