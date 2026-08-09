package memory

import (
	"context"
	"errors"
	"testing"
	"time"
)

type recordingGate struct {
	requests []AuthorizationRequest
	err      error
}

func (g *recordingGate) Authorize(_ context.Context, request AuthorizationRequest) error {
	g.requests = append(g.requests, request)
	return g.err
}

type memoryRepository struct {
	entries     map[string]Entry
	idempotency map[string]string
	createCalls int
	saveCalls   int
	lastSave    SaveCommand
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{entries: map[string]Entry{}, idempotency: map[string]string{}}
}

func repoKey(org, id string) string { return org + "\x00" + id }

func (r *memoryRepository) CreateCandidate(_ context.Context, command CreateCandidateCommand) (Entry, bool, error) {
	r.createCalls++
	hash, err := command.Entry.CanonicalHash()
	if err != nil {
		return Entry{}, false, err
	}
	if priorHash, ok := r.idempotency[command.IdempotencyKey]; ok {
		if priorHash != hash {
			return Entry{}, false, ErrConflict
		}
		for _, entry := range r.entries {
			entryHash, _ := entry.CanonicalHash()
			if entryHash == hash {
				return entry, true, nil
			}
		}
	}
	key := repoKey(command.Entry.OrganizationID, command.Entry.ID)
	if _, exists := r.entries[key]; exists {
		return Entry{}, false, ErrConflict
	}
	r.entries[key] = command.Entry
	r.idempotency[command.IdempotencyKey] = hash
	return command.Entry, false, nil
}

func (r *memoryRepository) Get(_ context.Context, organizationID, entryID string) (Entry, error) {
	entry, ok := r.entries[repoKey(organizationID, entryID)]
	if !ok {
		return Entry{}, ErrEntryNotFound
	}
	return entry, nil
}

func (r *memoryRepository) Save(_ context.Context, command SaveCommand) (Entry, error) {
	r.saveCalls++
	r.lastSave = command
	key := repoKey(command.Entry.OrganizationID, command.Entry.ID)
	current, ok := r.entries[key]
	if !ok {
		return Entry{}, ErrEntryNotFound
	}
	if current.Revision != command.ExpectedRevision {
		return Entry{}, ErrRevisionConflict
	}
	r.entries[key] = command.Entry
	return command.Entry, nil
}

func (r *memoryRepository) List(_ context.Context, filter ListFilter) ([]Entry, error) {
	values := []Entry{}
	for _, entry := range r.entries {
		if entry.OrganizationID != filter.OrganizationID {
			continue
		}
		if filter.RoleID != "" && entry.RoleID != filter.RoleID {
			continue
		}
		if filter.Status != "" && entry.Status != filter.Status {
			continue
		}
		values = append(values, entry)
	}
	return values, nil
}

func (r *memoryRepository) ListApproved(_ context.Context, filter ApprovedFilter) ([]Entry, error) {
	return r.List(context.Background(), ListFilter{OrganizationID: filter.OrganizationID, RoleID: filter.RoleID, Status: StatusApproved, Limit: filter.Limit})
}

func TestManagerDoesNotPersistUnauthorizedProposal(t *testing.T) {
	now := time.Date(2026, 8, 7, 5, 0, 0, 0, time.UTC)
	repository := newMemoryRepository()
	denied := errors.New("denied")
	gate := &recordingGate{err: denied}
	manager, err := NewManager(NewService(&fixedClock{now: now}), repository, gate, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = manager.Propose(context.Background(), ProposeRequest{Command: validCommand(now), IdempotencyKey: "proposal-1"})
	if !errors.Is(err, denied) {
		t.Fatalf("error=%v want denied", err)
	}
	if repository.createCalls != 0 {
		t.Fatalf("unauthorized proposal persisted %d time(s)", repository.createCalls)
	}
	if len(gate.requests) != 1 || gate.requests[0].CapabilityID != CapabilityPropose {
		t.Fatalf("unexpected authorization requests: %+v", gate.requests)
	}
}

func TestManagerProposalAndReviewUseSeparateCapabilities(t *testing.T) {
	now := time.Date(2026, 8, 7, 5, 0, 0, 0, time.UTC)
	clock := &fixedClock{now: now}
	repository := newMemoryRepository()
	gate := &recordingGate{}
	manager, err := NewManager(NewService(clock), repository, gate, nil)
	if err != nil {
		t.Fatal(err)
	}
	entry, reused, err := manager.Propose(context.Background(), ProposeRequest{Command: validCommand(now), IdempotencyKey: "proposal-1"})
	if err != nil || reused {
		t.Fatalf("propose entry=%+v reused=%v err=%v", entry, reused, err)
	}
	if gate.requests[0].CapabilityID != CapabilityPropose {
		t.Fatalf("proposal capability=%s", gate.requests[0].CapabilityID)
	}
	beforeHash, err := entry.CanonicalHash()
	if err != nil {
		t.Fatal(err)
	}

	clock.now = now.Add(time.Minute)
	approved, err := manager.Review(context.Background(), ReviewRequest{
		Mutation: MutationRequest{OrganizationID: entry.OrganizationID, EntryID: entry.ID, ExpectedRevision: entry.Revision, ActorRoleID: "empresa/human", Reason: "reviewed evidence and admission provenance"},
		Outcome:  ReviewApprove,
	})
	if err != nil {
		t.Fatal(err)
	}
	if approved.Status != StatusApproved || approved.ReviewerID != "empresa/human" {
		t.Fatalf("approved entry=%+v", approved)
	}
	if len(gate.requests) != 2 || gate.requests[1].CapabilityID != CapabilityApprove {
		t.Fatalf("review authorization requests=%+v", gate.requests)
	}
	afterHash, err := approved.CanonicalHash()
	if err != nil {
		t.Fatal(err)
	}
	if beforeHash != afterHash {
		t.Fatalf("review mutated immutable content hash: %s != %s", beforeHash, afterHash)
	}
	if repository.lastSave.ExpectedRevision != 1 || repository.lastSave.Entry.Revision != 2 {
		t.Fatalf("save command=%+v", repository.lastSave)
	}
}

func TestManagerRejectsStaleRevisionBeforeAuthorization(t *testing.T) {
	now := time.Date(2026, 8, 7, 5, 0, 0, 0, time.UTC)
	clock := &fixedClock{now: now}
	repository := newMemoryRepository()
	gate := &recordingGate{}
	manager, err := NewManager(NewService(clock), repository, gate, nil)
	if err != nil {
		t.Fatal(err)
	}
	entry, _, err := manager.Propose(context.Background(), ProposeRequest{Command: validCommand(now), IdempotencyKey: "proposal-1"})
	if err != nil {
		t.Fatal(err)
	}
	gate.requests = nil
	_, err = manager.Review(context.Background(), ReviewRequest{
		Mutation: MutationRequest{OrganizationID: entry.OrganizationID, EntryID: entry.ID, ExpectedRevision: entry.Revision + 1, ActorRoleID: "empresa/human", Reason: "stale"},
		Outcome:  ReviewApprove,
	})
	if !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("error=%v want ErrRevisionConflict", err)
	}
	if len(gate.requests) != 0 {
		t.Fatalf("stale mutation reached authorization: %+v", gate.requests)
	}
	if repository.saveCalls != 0 {
		t.Fatalf("stale mutation reached repository save: %d", repository.saveCalls)
	}
}

func TestMutationDigestRejectsContentMutation(t *testing.T) {
	now := time.Date(2026, 8, 7, 5, 0, 0, 0, time.UTC)
	service := NewService(&fixedClock{now: now})
	entry, err := service.Propose(validCommand(now))
	if err != nil {
		t.Fatal(err)
	}
	changed := entry
	changed.Correction = "mutated"
	if _, err := mutationDigest(entry, changed, "empresa/human", "reason"); !errors.Is(err, ErrConflict) {
		t.Fatalf("error=%v want ErrConflict", err)
	}
}

func TestManagerRequiresMutationReason(t *testing.T) {
	now := time.Date(2026, 8, 7, 5, 0, 0, 0, time.UTC)
	repository := newMemoryRepository()
	gate := &recordingGate{}
	manager, err := NewManager(NewService(&fixedClock{now: now}), repository, gate, nil)
	if err != nil {
		t.Fatal(err)
	}
	entry, _, err := manager.Propose(context.Background(), ProposeRequest{Command: validCommand(now), IdempotencyKey: "proposal-1"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = manager.Review(context.Background(), ReviewRequest{Mutation: MutationRequest{OrganizationID: entry.OrganizationID, EntryID: entry.ID, ExpectedRevision: 1, ActorRoleID: "empresa/human"}, Outcome: ReviewApprove})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("error=%v want ErrInvalidRequest", err)
	}
}

func TestManagerExactDuplicateCanBeIdempotent(t *testing.T) {
	now := time.Date(2026, 8, 7, 5, 0, 0, 0, time.UTC)
	repository := newMemoryRepository()
	gate := &recordingGate{}
	manager, err := NewManager(NewService(&fixedClock{now: now}), repository, gate, nil)
	if err != nil {
		t.Fatal(err)
	}
	first, reused, err := manager.Propose(context.Background(), ProposeRequest{Command: validCommand(now), IdempotencyKey: "proposal-1"})
	if err != nil || reused {
		t.Fatalf("first proposal reused=%v err=%v", reused, err)
	}
	second, reused, err := manager.Propose(context.Background(), ProposeRequest{Command: validCommand(now), IdempotencyKey: "proposal-1"})
	if err != nil || !reused {
		t.Fatalf("second proposal reused=%v err=%v", reused, err)
	}
	if first.ID != second.ID {
		t.Fatalf("idempotent proposal changed entry: %s != %s", first.ID, second.ID)
	}
}

func TestManagerSameIdempotencyKeyDifferentContentConflicts(t *testing.T) {
	now := time.Date(2026, 8, 7, 5, 0, 0, 0, time.UTC)
	repository := newMemoryRepository()
	gate := &recordingGate{}
	manager, err := NewManager(NewService(&fixedClock{now: now}), repository, gate, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.Propose(context.Background(), ProposeRequest{Command: validCommand(now), IdempotencyKey: "proposal-1"}); err != nil {
		t.Fatal(err)
	}
	changed := validCommand(now)
	changed.ID = "mem-2"
	changed.Correction = "different correction"
	_, _, err = manager.Propose(context.Background(), ProposeRequest{Command: changed, IdempotencyKey: "proposal-1"})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("error=%v want ErrConflict", err)
	}
}
