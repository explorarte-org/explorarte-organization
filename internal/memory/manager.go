package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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
}

func NewManager(domain *Service, repository Repository, gate AuthorizationGate) (*Manager, error) {
	if domain == nil {
		return nil, errors.New("memory manager requires domain service")
	}
	if repository == nil {
		return nil, errors.New("memory manager requires repository")
	}
	if gate == nil {
		return nil, errors.New("memory manager requires authorization gate")
	}
	return &Manager{domain: domain, repository: repository, gate: gate}, nil
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
	return m.repository.Save(ctx, SaveCommand{
		Entry:            updated,
		ExpectedRevision: request.Mutation.ExpectedRevision,
		ActorID:          strings.TrimSpace(request.Mutation.ActorRoleID),
		Reason:           strings.TrimSpace(request.Mutation.Reason),
	})
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
