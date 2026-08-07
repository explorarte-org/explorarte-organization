package rag

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

const (
	CapabilityPropose        = "rag.propose_candidate"
	CapabilityPublish        = "rag.publish_approved"
	CapabilityReadDepartment = "rag.read_department"
	CapabilityReadOwn        = "rag.read_own_namespace"
)

type Manager struct {
	domain     *Service
	repository Repository
	gate       AuthorizationGate
	namespaces NamespaceResolver
}

func NewManager(domain *Service, repository Repository, gate AuthorizationGate, namespaces NamespaceResolver) (*Manager, error) {
	if domain == nil {
		return nil, errors.New("rag manager requires domain service")
	}
	if repository == nil {
		return nil, errors.New("rag manager requires repository")
	}
	if gate == nil {
		return nil, errors.New("rag manager requires authorization gate")
	}
	if namespaces == nil {
		return nil, errors.New("rag manager requires namespace resolver")
	}
	return &Manager{domain: domain, repository: repository, gate: gate, namespaces: namespaces}, nil
}

type ProposeRequest struct {
	Command        ProposeCommand
	IdempotencyKey string
}

func (m *Manager) Propose(ctx context.Context, request ProposeRequest) (KnowledgeVersion, bool, error) {
	key := strings.TrimSpace(request.IdempotencyKey)
	if key == "" {
		return KnowledgeVersion{}, false, fmt.Errorf("%w: idempotency_key is required", ErrInvalidRequest)
	}
	version, err := m.domain.Propose(request.Command)
	if err != nil {
		return KnowledgeVersion{}, false, err
	}
	if err := m.gate.Authorize(ctx, AuthorizationRequest{
		OrganizationID: version.OrganizationID, ActorRoleID: version.ProposedBy, CapabilityID: CapabilityPropose,
		ResourceType: "knowledge_document", ResourceID: version.DocumentID, ActionDigest: version.CanonicalHash,
	}); err != nil {
		return KnowledgeVersion{}, false, err
	}
	return m.repository.CreateCandidate(ctx, CreateCandidateCommand{Version: version, IdempotencyKey: key})
}

type MutationRequest struct {
	OrganizationID   string
	VersionID        string
	ExpectedRevision int64
	ActorRoleID      string
	Reason           string
	// ApprovalRequestID carries a prior decided orgctl authorization
	// request/decide sequence, required because rag.publish_approved
	// always evaluates as approval-required regardless of grants.
	ApprovalRequestID *int64
}

type ReviewRequest struct {
	Mutation MutationRequest
	Outcome  ReviewOutcome
}

func (m *Manager) Review(ctx context.Context, request ReviewRequest) (KnowledgeVersion, error) {
	current, err := m.loadMutationTarget(ctx, request.Mutation)
	if err != nil {
		return KnowledgeVersion{}, err
	}
	updated, err := m.domain.Review(current, request.Outcome, request.Mutation.ActorRoleID)
	if err != nil {
		return KnowledgeVersion{}, err
	}
	if err := m.authorizeMutation(ctx, request.Mutation, current, updated); err != nil {
		return KnowledgeVersion{}, err
	}
	return m.repository.Save(ctx, SaveCommand{Version: updated, ExpectedRevision: request.Mutation.ExpectedRevision, ActorID: strings.TrimSpace(request.Mutation.ActorRoleID), Reason: strings.TrimSpace(request.Mutation.Reason)})
}

func (m *Manager) Deprecate(ctx context.Context, request MutationRequest) (KnowledgeVersion, error) {
	current, err := m.loadMutationTarget(ctx, request)
	if err != nil {
		return KnowledgeVersion{}, err
	}
	updated, err := m.domain.Deprecate(current)
	if err != nil {
		return KnowledgeVersion{}, err
	}
	if err := m.authorizeMutation(ctx, request, current, updated); err != nil {
		return KnowledgeVersion{}, err
	}
	return m.repository.Save(ctx, SaveCommand{Version: updated, ExpectedRevision: request.ExpectedRevision, ActorID: strings.TrimSpace(request.ActorRoleID), Reason: strings.TrimSpace(request.Reason)})
}

func (m *Manager) Archive(ctx context.Context, request MutationRequest) (KnowledgeVersion, error) {
	current, err := m.loadMutationTarget(ctx, request)
	if err != nil {
		return KnowledgeVersion{}, err
	}
	updated, err := m.domain.Archive(current)
	if err != nil {
		return KnowledgeVersion{}, err
	}
	if err := m.authorizeMutation(ctx, request, current, updated); err != nil {
		return KnowledgeVersion{}, err
	}
	return m.repository.Save(ctx, SaveCommand{Version: updated, ExpectedRevision: request.ExpectedRevision, ActorID: strings.TrimSpace(request.ActorRoleID), Reason: strings.TrimSpace(request.Reason)})
}

func (m *Manager) Get(ctx context.Context, organizationID, versionID string) (KnowledgeVersion, error) {
	organizationID, versionID = strings.TrimSpace(organizationID), strings.TrimSpace(versionID)
	if organizationID == "" || versionID == "" {
		return KnowledgeVersion{}, fmt.Errorf("%w: organization_id and version_id are required", ErrInvalidRequest)
	}
	return m.repository.Get(ctx, organizationID, versionID)
}

func (m *Manager) List(ctx context.Context, filter ListFilter) ([]KnowledgeVersion, error) {
	if strings.TrimSpace(filter.OrganizationID) == "" {
		return nil, fmt.Errorf("%w: organization_id is required", ErrInvalidRequest)
	}
	return m.repository.List(ctx, filter)
}

func (m *Manager) loadMutationTarget(ctx context.Context, request MutationRequest) (KnowledgeVersion, error) {
	if strings.TrimSpace(request.OrganizationID) == "" || strings.TrimSpace(request.VersionID) == "" || strings.TrimSpace(request.ActorRoleID) == "" {
		return KnowledgeVersion{}, fmt.Errorf("%w: organization_id, version_id, and actor_role_id are required", ErrInvalidRequest)
	}
	if request.ExpectedRevision <= 0 {
		return KnowledgeVersion{}, fmt.Errorf("%w: expected_revision must be positive", ErrInvalidRequest)
	}
	if strings.TrimSpace(request.Reason) == "" {
		return KnowledgeVersion{}, fmt.Errorf("%w: mutation reason is required", ErrInvalidRequest)
	}
	version, err := m.repository.Get(ctx, strings.TrimSpace(request.OrganizationID), strings.TrimSpace(request.VersionID))
	if err != nil {
		return KnowledgeVersion{}, err
	}
	if version.Revision != request.ExpectedRevision {
		return KnowledgeVersion{}, fmt.Errorf("%w: version %s expected revision %d current %d", ErrRevisionConflict, version.ID, request.ExpectedRevision, version.Revision)
	}
	return version, nil
}

func (m *Manager) authorizeMutation(ctx context.Context, request MutationRequest, before, after KnowledgeVersion) error {
	if before.CanonicalHash != after.CanonicalHash {
		return fmt.Errorf("%w: lifecycle mutation changed immutable knowledge content", ErrConflict)
	}
	digest := mutationDigest(before, after, request.ActorRoleID, request.Reason)
	return m.gate.Authorize(ctx, AuthorizationRequest{
		OrganizationID: before.OrganizationID, ActorRoleID: strings.TrimSpace(request.ActorRoleID), CapabilityID: CapabilityPublish,
		ResourceType: "knowledge_version", ResourceID: before.ID, ActionDigest: digest, ApprovalRequestID: request.ApprovalRequestID,
	})
}

func mutationDigest(before, after KnowledgeVersion, actor, reason string) string {
	return ContentHash(strings.Join([]string{"rag-mutation.v1", before.ID, before.CanonicalHash, string(before.Lifecycle), string(after.Lifecycle), fmt.Sprint(before.Revision), strings.TrimSpace(actor), strings.TrimSpace(reason)}, "|"))
}

type ReindexRequest struct {
	OrganizationID    string
	NamespaceKind     NamespaceKind
	NamespaceID       string
	ActorRoleID       string
	ApprovalRequestID *int64
}

// Reindex builds a new index generation over every approved, non-superseded
// knowledge version in a namespace and atomically activates it once complete.
func (m *Manager) Reindex(ctx context.Context, request ReindexRequest) (IndexGeneration, error) {
	organizationID := strings.TrimSpace(request.OrganizationID)
	namespaceID := strings.TrimSpace(request.NamespaceID)
	actorRoleID := strings.TrimSpace(request.ActorRoleID)
	namespaceKind := request.NamespaceKind
	if organizationID == "" || namespaceID == "" || actorRoleID == "" || !namespaceKind.Valid() {
		return IndexGeneration{}, fmt.Errorf("%w: organization_id, namespace, and actor_role_id are required", ErrInvalidGeneration)
	}
	if err := m.gate.Authorize(ctx, AuthorizationRequest{
		OrganizationID: organizationID, ActorRoleID: actorRoleID, CapabilityID: CapabilityPublish,
		ResourceType: "knowledge_index", ResourceID: string(namespaceKind) + ":" + namespaceID,
		ActionDigest: ContentHash("rag-reindex.v1|" + organizationID + "|" + string(namespaceKind) + "|" + namespaceID), ApprovalRequestID: request.ApprovalRequestID,
	}); err != nil {
		return IndexGeneration{}, err
	}
	versions, err := m.repository.ApprovedForNamespace(ctx, organizationID, namespaceKind, namespaceID)
	if err != nil {
		return IndexGeneration{}, err
	}
	chunks := make([]Chunk, 0, len(versions))
	for _, version := range versions {
		if version.Lifecycle != LifecycleApproved {
			return IndexGeneration{}, fmt.Errorf("%w: cannot index non-approved knowledge version %s", ErrVersionNotApproved, version.ID)
		}
		versionChunks, err := ChunkBody(version.ID, DefaultChunkerID, DefaultChunkerVersion, version.Body)
		if err != nil {
			return IndexGeneration{}, err
		}
		chunks = append(chunks, versionChunks...)
	}
	return m.repository.Reindex(ctx, ReindexCommand{
		OrganizationID: organizationID, NamespaceKind: namespaceKind, NamespaceID: namespaceID,
		ChunkerID: DefaultChunkerID, ChunkerVersion: DefaultChunkerVersion, Chunks: chunks,
	})
}

type QueryRequest struct {
	OrganizationID string
	ActorRoleID    string
	Scope          NamespaceKind
	QueryText      string
	Limit          int
}

func (m *Manager) Query(ctx context.Context, request QueryRequest) ([]QueryResult, error) {
	organizationID := strings.TrimSpace(request.OrganizationID)
	actorRoleID := strings.TrimSpace(request.ActorRoleID)
	queryText := strings.TrimSpace(request.QueryText)
	if organizationID == "" || actorRoleID == "" || queryText == "" || !request.Scope.Valid() {
		return nil, fmt.Errorf("%w: organization_id, actor_role_id, scope, and query_text are required", ErrInvalidRequest)
	}
	namespaceID, err := m.namespaces.ResolveNamespace(ctx, organizationID, actorRoleID, request.Scope)
	if err != nil {
		return nil, err
	}
	capability := CapabilityReadOwn
	if request.Scope == NamespaceDepartment {
		capability = CapabilityReadDepartment
	}
	if err := m.gate.Authorize(ctx, AuthorizationRequest{
		OrganizationID: organizationID, ActorRoleID: actorRoleID, CapabilityID: capability,
		ResourceType: "knowledge_namespace", ResourceID: string(request.Scope) + ":" + namespaceID,
		ActionDigest: ContentHash("rag-query.v1|" + organizationID + "|" + string(request.Scope) + "|" + namespaceID + "|" + queryText),
	}); err != nil {
		return nil, err
	}
	limit := request.Limit
	if limit <= 0 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}
	return m.repository.Query(ctx, QueryCommand{OrganizationID: organizationID, NamespaceKind: request.Scope, NamespaceID: namespaceID, QueryText: queryText, Limit: limit})
}

func (m *Manager) ActiveGeneration(ctx context.Context, organizationID string, namespaceKind NamespaceKind, namespaceID string) (IndexGeneration, bool, error) {
	return m.repository.ActiveGeneration(ctx, strings.TrimSpace(organizationID), namespaceKind, strings.TrimSpace(namespaceID))
}
