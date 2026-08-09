package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/costledger"
	"github.com/Mireuz13/explorarte-organization/internal/embeddingruntime"
)

const (
	CapabilityPropose = "memory.propose"
	CapabilityApprove = "memory.approve"
)

type AuthorizationRequest struct {
	OrganizationID string
	ActorRoleID    string
	CapabilityID   string
	ResourceType   string
	ResourceID     string
	ActionDigest   string
}

type AuthorizationGate interface {
	Authorize(context.Context, AuthorizationRequest) error
}

type Manager struct {
	domain     *Service
	repository Repository
	gate       AuthorizationGate
	semantic   *SemanticSearchDeps
}

// NewManager's semantic parameter may be nil — see SemanticSearchDeps for
// what that degrades to.
func NewManager(domain *Service, repository Repository, gate AuthorizationGate, semantic *SemanticSearchDeps) (*Manager, error) {
	if domain == nil {
		return nil, errors.New("memory manager requires domain service")
	}
	if repository == nil {
		return nil, errors.New("memory manager requires repository")
	}
	if gate == nil {
		return nil, errors.New("memory manager requires authorization gate")
	}
	if err := semantic.validate(); err != nil {
		return nil, err
	}
	return &Manager{domain: domain, repository: repository, gate: gate, semantic: semantic}, nil
}

type ProposeRequest struct {
	Command        ProposeCommand
	IdempotencyKey string
}

type MutationRequest struct {
	OrganizationID   string
	EntryID          string
	ExpectedRevision int64
	ActorRoleID      string
	Reason           string
}

type ReviewRequest struct {
	Mutation MutationRequest
	Outcome  ReviewOutcome
}

func (m *Manager) Propose(ctx context.Context, request ProposeRequest) (Entry, bool, error) {
	if strings.TrimSpace(request.IdempotencyKey) == "" {
		return Entry{}, false, fmt.Errorf("%w: idempotency_key is required", ErrInvalidRequest)
	}
	entry, err := m.domain.Propose(request.Command)
	if err != nil {
		return Entry{}, false, err
	}
	hash, err := entry.CanonicalHash()
	if err != nil {
		return Entry{}, false, err
	}
	if err := m.gate.Authorize(ctx, AuthorizationRequest{
		OrganizationID: entry.OrganizationID,
		ActorRoleID:    entry.ProposedBy,
		CapabilityID:   CapabilityPropose,
		ResourceType:   "organizational_memory",
		ResourceID:     entry.RoleID,
		ActionDigest:   hash,
	}); err != nil {
		return Entry{}, false, err
	}
	return m.repository.CreateCandidate(ctx, CreateCandidateCommand{Entry: entry, IdempotencyKey: strings.TrimSpace(request.IdempotencyKey)})
}

func (m *Manager) Review(ctx context.Context, request ReviewRequest) (Entry, error) {
	current, err := m.loadMutationTarget(ctx, request.Mutation)
	if err != nil {
		return Entry{}, err
	}
	updated, err := m.domain.Review(current, Review{Outcome: request.Outcome, ReviewerID: request.Mutation.ActorRoleID})
	if err != nil {
		return Entry{}, err
	}
	if err := m.authorizeMutation(ctx, request.Mutation, current, updated); err != nil {
		return Entry{}, err
	}
	saved, err := m.repository.Save(ctx, SaveCommand{
		Entry:            updated,
		ExpectedRevision: request.Mutation.ExpectedRevision,
		ActorID:          strings.TrimSpace(request.Mutation.ActorRoleID),
		Reason:           strings.TrimSpace(request.Mutation.Reason),
	})
	if err != nil {
		return Entry{}, err
	}
	// Embedding happens only after the approval is already durably saved —
	// never before, and never for a rejected entry. Best-effort: a failed
	// embed here must not undo or fail an approval that already succeeded.
	if saved.Status == StatusApproved {
		m.embedApprovedEntry(ctx, saved)
	}
	return saved, nil
}

func (m *Manager) Deprecate(ctx context.Context, request MutationRequest) (Entry, error) {
	current, err := m.loadMutationTarget(ctx, request)
	if err != nil {
		return Entry{}, err
	}
	updated, err := m.domain.Deprecate(current)
	if err != nil {
		return Entry{}, err
	}
	if err := m.authorizeMutation(ctx, request, current, updated); err != nil {
		return Entry{}, err
	}
	return m.repository.Save(ctx, SaveCommand{Entry: updated, ExpectedRevision: request.ExpectedRevision, ActorID: strings.TrimSpace(request.ActorRoleID), Reason: strings.TrimSpace(request.Reason)})
}

func (m *Manager) Archive(ctx context.Context, request MutationRequest) (Entry, error) {
	current, err := m.loadMutationTarget(ctx, request)
	if err != nil {
		return Entry{}, err
	}
	updated, err := m.domain.Archive(current)
	if err != nil {
		return Entry{}, err
	}
	if err := m.authorizeMutation(ctx, request, current, updated); err != nil {
		return Entry{}, err
	}
	return m.repository.Save(ctx, SaveCommand{Entry: updated, ExpectedRevision: request.ExpectedRevision, ActorID: strings.TrimSpace(request.ActorRoleID), Reason: strings.TrimSpace(request.Reason)})
}

func (m *Manager) Get(ctx context.Context, organizationID, entryID string) (Entry, error) {
	organizationID, entryID = strings.TrimSpace(organizationID), strings.TrimSpace(entryID)
	if organizationID == "" || entryID == "" {
		return Entry{}, fmt.Errorf("%w: organization_id and entry_id are required", ErrInvalidRequest)
	}
	return m.repository.Get(ctx, organizationID, entryID)
}

func (m *Manager) List(ctx context.Context, filter ListFilter) ([]Entry, error) {
	if strings.TrimSpace(filter.OrganizationID) == "" {
		return nil, fmt.Errorf("%w: organization_id is required", ErrInvalidRequest)
	}
	return m.repository.List(ctx, filter)
}

type SearchRequest struct {
	OrganizationID string
	ActorRoleID    string
	// RoleID is the memory namespace being searched — R29 only supports
	// RoleID == ActorRoleID (search your own role's memory). Get/List
	// today have no gate at all (see internal/memory/manager.go's existing
	// methods above); Search deliberately does not inherit that gap by
	// widening to cross-role reads without a real capability behind it —
	// that is left for a future branch to add deliberately, not defaulted
	// into here.
	RoleID    string
	QueryText string
	TaskID    *int64
	Limit     int
}

// Search requires ActorRoleID == RoleID and falls back to ListApproved's
// recency ordering when neither the exact-identifier nor the vector
// channel is available (semantic search not configured, or the query
// embedded to nothing) — Search is strictly additive over what already
// existed, never worse.
func (m *Manager) Search(ctx context.Context, request SearchRequest) ([]Entry, error) {
	organizationID := strings.TrimSpace(request.OrganizationID)
	actorRoleID := strings.TrimSpace(request.ActorRoleID)
	roleID := strings.TrimSpace(request.RoleID)
	queryText := strings.TrimSpace(request.QueryText)
	if organizationID == "" || actorRoleID == "" || roleID == "" || queryText == "" {
		return nil, fmt.Errorf("%w: organization_id, actor_role_id, role_id, and query_text are required", ErrInvalidRequest)
	}
	if actorRoleID != roleID {
		return nil, fmt.Errorf("%w: search is limited to the actor's own role memory", ErrInvalidRequest)
	}
	limit := request.Limit
	if limit <= 0 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}
	embeddingRepo, ok := m.repository.(EmbeddingRepository)
	if !ok {
		return m.ListApproved(ctx, organizationID, roleID, limit)
	}
	queryVector := m.embed(ctx, organizationID, actorRoleID, queryText, request.TaskID, embeddingruntime.TaskQuery, costledger.EmbeddingOperationMemorySearch)
	var identity EmbeddingIdentity
	var promptTemplateVersion string
	if len(queryVector) > 0 && m.semantic != nil {
		// The identity travels with the vector so the store can filter the
		// vector channel to rows produced under this exact space — see
		// EmbeddingRepository.Search's doc comment.
		identity = m.semantic.Identity
		promptTemplateVersion = m.semantic.PromptTemplateVersion
	}
	results, err := embeddingRepo.Search(ctx, organizationID, roleID, queryText, queryVector, identity, promptTemplateVersion, limit)
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return m.ListApproved(ctx, organizationID, roleID, limit)
	}
	return results, nil
}

// ListApproved is the same recency-ordered read this package has always
// had (see internal/memory/contextprovider), exposed as a Manager method so
// Search can fall back to it without duplicating the ordering rule.
func (m *Manager) ListApproved(ctx context.Context, organizationID, roleID string, limit int) ([]Entry, error) {
	return m.repository.ListApproved(ctx, ApprovedFilter{OrganizationID: organizationID, RoleID: roleID, Limit: limit})
}

func (m *Manager) loadMutationTarget(ctx context.Context, request MutationRequest) (Entry, error) {
	if strings.TrimSpace(request.OrganizationID) == "" || strings.TrimSpace(request.EntryID) == "" || strings.TrimSpace(request.ActorRoleID) == "" {
		return Entry{}, fmt.Errorf("%w: organization_id, entry_id, and actor_role_id are required", ErrInvalidRequest)
	}
	if request.ExpectedRevision <= 0 {
		return Entry{}, fmt.Errorf("%w: expected_revision must be positive", ErrInvalidRequest)
	}
	if strings.TrimSpace(request.Reason) == "" {
		return Entry{}, fmt.Errorf("%w: mutation reason is required", ErrInvalidRequest)
	}
	entry, err := m.repository.Get(ctx, strings.TrimSpace(request.OrganizationID), strings.TrimSpace(request.EntryID))
	if err != nil {
		return Entry{}, err
	}
	if entry.Revision != request.ExpectedRevision {
		return Entry{}, fmt.Errorf("%w: entry %s expected revision %d current %d", ErrRevisionConflict, entry.ID, request.ExpectedRevision, entry.Revision)
	}
	return entry, nil
}

func (m *Manager) authorizeMutation(ctx context.Context, request MutationRequest, before, after Entry) error {
	digest, err := mutationDigest(before, after, request.ActorRoleID, request.Reason)
	if err != nil {
		return err
	}
	return m.gate.Authorize(ctx, AuthorizationRequest{
		OrganizationID: before.OrganizationID,
		ActorRoleID:    strings.TrimSpace(request.ActorRoleID),
		CapabilityID:   CapabilityApprove,
		ResourceType:   "organizational_memory",
		ResourceID:     before.ID,
		ActionDigest:   digest,
	})
}

func mutationDigest(before, after Entry, actor, reason string) (string, error) {
	contentHash, err := before.CanonicalHash()
	if err != nil {
		return "", err
	}
	afterHash, err := after.CanonicalHash()
	if err != nil {
		return "", err
	}
	if contentHash != afterHash {
		return "", fmt.Errorf("%w: lifecycle mutation changed immutable content", ErrConflict)
	}
	body := struct {
		SchemaVersion string `json:"schema_version"`
		EntryID       string `json:"entry_id"`
		ContentHash   string `json:"content_hash"`
		From          Status `json:"from"`
		To            Status `json:"to"`
		Revision      int64  `json:"revision"`
		Actor         string `json:"actor"`
		Reason        string `json:"reason"`
	}{
		SchemaVersion: "organizational-memory.mutation.v1",
		EntryID:       before.ID,
		ContentHash:   contentHash,
		From:          before.Status,
		To:            after.Status,
		Revision:      before.Revision,
		Actor:         strings.TrimSpace(actor),
		Reason:        strings.TrimSpace(reason),
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("canonicalize memory mutation: %w", err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
