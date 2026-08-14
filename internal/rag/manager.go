package rag

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/costledger"
	"github.com/Mireuz13/explorarte-organization/internal/embeddingruntime"
)

const (
	CapabilityPropose        = "rag.propose_candidate"
	CapabilityPublish        = "rag.publish_approved"
	CapabilityReadDepartment = "rag.read_department"
	CapabilityReadOwn        = "rag.read_own_namespace"
)

type Manager struct {
	domain       *Service
	repository   Repository
	gate         AuthorizationGate
	namespaces   NamespaceResolver
	semantic     *SemanticSearchDeps
	mediaFetcher MediaFetcher
}

// NewManager's semantic parameter may be nil — see SemanticSearchDeps for
// what that degrades to. mediaFetcher may also be nil — see MediaFetcher's
// doc comment.
func NewManager(domain *Service, repository Repository, gate AuthorizationGate, namespaces NamespaceResolver, semantic *SemanticSearchDeps, mediaFetcher MediaFetcher) (*Manager, error) {
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
	if err := semantic.validate(); err != nil {
		return nil, err
	}
	return &Manager{domain: domain, repository: repository, gate: gate, namespaces: namespaces, semantic: semantic, mediaFetcher: mediaFetcher}, nil
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

func (m *Manager) Get(ctx context.Context, organizationID, versionID, actorRoleID string) (KnowledgeVersion, error) {
	organizationID = strings.TrimSpace(organizationID)
	versionID = strings.TrimSpace(versionID)
	actorRoleID = strings.TrimSpace(actorRoleID)
	if organizationID == "" || versionID == "" || actorRoleID == "" {
		return KnowledgeVersion{}, fmt.Errorf("%w: organization_id, version_id, and actor_role_id are required", ErrInvalidRequest)
	}
	version, err := m.repository.Get(ctx, organizationID, versionID)
	if err != nil {
		return KnowledgeVersion{}, err
	}
	if err := m.authorizeNamespaceRead(ctx, organizationID, actorRoleID, version.NamespaceKind, version.NamespaceID,
		ContentHash("rag-get.v1|"+organizationID+"|"+version.ID+"|"+version.CanonicalHash)); err != nil {
		return KnowledgeVersion{}, err
	}
	return version, nil
}

// GetForRevalidation is the deliberately narrow, authorization-free read used
// by the context engine after an already-authorized RAG query has embedded a
// version into a context snapshot. It must not be used for an actor-initiated
// read: callers serving users or agents must use Get so namespace authorization
// is evaluated against the version's persisted namespace.
func (m *Manager) GetForRevalidation(ctx context.Context, organizationID, versionID string) (KnowledgeVersion, error) {
	organizationID = strings.TrimSpace(organizationID)
	versionID = strings.TrimSpace(versionID)
	if organizationID == "" || versionID == "" {
		return KnowledgeVersion{}, fmt.Errorf("%w: organization_id and version_id are required", ErrInvalidRequest)
	}
	return m.repository.Get(ctx, organizationID, versionID)
}

func (m *Manager) List(ctx context.Context, actorRoleID string, filter ListFilter) ([]KnowledgeVersion, error) {
	filter.OrganizationID = strings.TrimSpace(filter.OrganizationID)
	filter.NamespaceID = strings.TrimSpace(filter.NamespaceID)
	actorRoleID = strings.TrimSpace(actorRoleID)
	if filter.OrganizationID == "" || actorRoleID == "" || !filter.NamespaceKind.Valid() || filter.NamespaceID == "" {
		return nil, fmt.Errorf("%w: organization_id, actor_role_id, and an explicit namespace are required", ErrInvalidRequest)
	}
	if filter.Lifecycle != "" && !filter.Lifecycle.Valid() {
		return nil, fmt.Errorf("%w: invalid lifecycle %q", ErrInvalidRequest, filter.Lifecycle)
	}
	if err := m.authorizeNamespaceRead(ctx, filter.OrganizationID, actorRoleID, filter.NamespaceKind, filter.NamespaceID,
		ContentHash("rag-list.v1|"+filter.OrganizationID+"|"+string(filter.NamespaceKind)+"|"+filter.NamespaceID+"|"+string(filter.Lifecycle))); err != nil {
		return nil, err
	}
	return m.repository.List(ctx, filter)
}

func (m *Manager) authorizeNamespaceRead(ctx context.Context, organizationID, actorRoleID string, namespaceKind NamespaceKind, namespaceID, actionDigest string) error {
	resolvedNamespaceID, err := m.namespaces.ResolveNamespace(ctx, organizationID, actorRoleID, namespaceKind)
	if err != nil {
		return err
	}
	if resolvedNamespaceID != namespaceID {
		return fmt.Errorf("%w: actor namespace %s does not match requested namespace %s", ErrInvalidNamespace, resolvedNamespaceID, namespaceID)
	}
	capability := CapabilityReadOwn
	if namespaceKind == NamespaceDepartment {
		capability = CapabilityReadDepartment
	}
	return m.gate.Authorize(ctx, AuthorizationRequest{
		OrganizationID: organizationID, ActorRoleID: actorRoleID, CapabilityID: capability,
		ResourceType: "knowledge_namespace", ResourceID: string(namespaceKind) + ":" + namespaceID,
		ActionDigest: actionDigest,
	})
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
	// PrecomputedChunks, keyed by version ID, supplies the exact chunks to
	// use for specific approved versions instead of recomputing them from
	// version.Body via ChunkBody -- used by out-of-process ingestion
	// pipelines (e.g. PDF page splitting via internal/pdfingest) that
	// already did expensive, external work to produce these chunks and
	// must not have Reindex silently redo it. A version with no entry
	// here still goes through the normal ChunkBody path unchanged. The
	// caller is responsible for each supplied chunk's Ordinal/VersionID/
	// ContentHash being internally consistent -- the same validation the
	// repository already applies to every chunk regardless of origin
	// (see postgres Store.Reindex) is the actual enforcement point.
	PrecomputedChunks map[string][]Chunk
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
		if precomputed, ok := request.PrecomputedChunks[version.ID]; ok {
			chunks = append(chunks, precomputed...)
			continue
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

const defaultBackfillBatchSize = 50
const maxBackfillBatchSize = 500

type BackfillEmbeddingsRequest struct {
	OrganizationID string
	NamespaceKind  NamespaceKind
	NamespaceID    string
	ActorRoleID    string
	// BatchSize caps how many chunks a single call embeds — a backfill is
	// deliberately paged rather than looping to exhaustion internally, so
	// a chunk that permanently fails to embed (e.g. it matches
	// dataclassifier.Detect) can never turn a single call into an infinite
	// loop: the caller (see cmd/orgctl) simply keeps calling until Done is
	// true, and each call's own page always terminates.
	BatchSize         int
	ApprovalRequestID *int64
}

type BackfillEmbeddingsResult struct {
	Embedded int
	Skipped  int
	// Done is true only when this call both (a) found fewer pending
	// chunks than BatchSize -- there was nothing left to page through --
	// and (b) skipped none of them. RAG-EMBED-COMPLETENESS-001: Done used
	// to be decided from (a) alone, before any chunk was even attempted,
	// so a page that was entirely Skipped (e.g. every chunk permanently
	// fails dataclassifier.Detect) still reported Done=true -- the CLI
	// loop (see cmd/orgctl) stops the instant it sees that, so a batch
	// could report completion while chunks with no embedding at all were
	// left behind, silently, forever. A chunk that failed to embed this
	// call is not retried within the same call, but is not gone either:
	// it is still pending and will be returned again by the next call --
	// which is exactly why any Skipped chunk on an otherwise-final page
	// must keep Done false: there is real, unfinished, retriable work
	// left, even though the pending count alone looked like exhaustion.
	Done bool
}

// BackfillEmbeddings is R30.1-2's resumable, idempotent fill for chunks
// that predate the active embedding profile (or a re-embedding under a new
// identity) — Reindex only ever produces chunk rows, never embeddings (see
// its doc comment), so without this, activating BGE-M3 embeds new queries
// against a document index that stays permanently empty. Safe to call
// repeatedly and concurrently with itself: each call reads whichever
// chunks currently lack a row for the active identity and inserts them
// with an idempotent (ON CONFLICT DO NOTHING) insert, so a crash or a
// second concurrent caller can only ever re-do work, never corrupt state.
func (m *Manager) BackfillEmbeddings(ctx context.Context, request BackfillEmbeddingsRequest) (BackfillEmbeddingsResult, error) {
	organizationID := strings.TrimSpace(request.OrganizationID)
	namespaceID := strings.TrimSpace(request.NamespaceID)
	actorRoleID := strings.TrimSpace(request.ActorRoleID)
	namespaceKind := request.NamespaceKind
	if organizationID == "" || namespaceID == "" || actorRoleID == "" || !namespaceKind.Valid() {
		return BackfillEmbeddingsResult{}, fmt.Errorf("%w: organization_id, namespace, and actor_role_id are required", ErrInvalidRequest)
	}
	if m.semantic == nil {
		return BackfillEmbeddingsResult{}, fmt.Errorf("%w: semantic search is not configured, nothing to backfill", ErrInvalidRequest)
	}
	pendingRepo, ok := m.repository.(EmbeddingBackfillRepository)
	if !ok {
		return BackfillEmbeddingsResult{}, fmt.Errorf("%w: repository does not support embedding backfill", ErrInvalidRequest)
	}
	if err := m.gate.Authorize(ctx, AuthorizationRequest{
		OrganizationID: organizationID, ActorRoleID: actorRoleID, CapabilityID: CapabilityPublish,
		ResourceType: "knowledge_index", ResourceID: string(namespaceKind) + ":" + namespaceID,
		ActionDigest: ContentHash("rag-embedding-backfill.v1|" + organizationID + "|" + string(namespaceKind) + "|" + namespaceID), ApprovalRequestID: request.ApprovalRequestID,
	}); err != nil {
		return BackfillEmbeddingsResult{}, err
	}

	batchSize := request.BatchSize
	if batchSize <= 0 {
		batchSize = defaultBackfillBatchSize
	}
	if batchSize > maxBackfillBatchSize {
		batchSize = maxBackfillBatchSize
	}
	identity := m.semantic.Identity
	promptTemplateVersion := m.semantic.PromptTemplateVersion
	pending, err := pendingRepo.PendingChunkEmbeddings(ctx, organizationID, namespaceKind, namespaceID, identity, batchSize)
	if err != nil {
		return BackfillEmbeddingsResult{}, err
	}
	result := BackfillEmbeddingsResult{}
	now := time.Now().UTC()
	for _, chunk := range pending {
		var vector []float32
		if chunk.IsMedia() {
			if m.mediaFetcher == nil {
				slog.Default().Warn("rag embedding skipped: chunk is media-backed but no MediaFetcher is configured", "chunk_id", chunk.ID)
				result.Skipped++
				continue
			}
			data, err := m.mediaFetcher.FetchMedia(ctx, chunk.MediaSourceRef)
			if err != nil {
				slog.Default().Warn("rag embedding skipped: media fetch failed", "chunk_id", chunk.ID, "error", err)
				result.Skipped++
				continue
			}
			vector = m.embedMedia(ctx, organizationID, actorRoleID, chunk.Content, chunk.MediaMimeType, data, nil, embeddingruntime.TaskDocument, costledger.EmbeddingOperationRAGReindex)
		} else {
			vector = m.embed(ctx, organizationID, actorRoleID, chunk.Content, nil, embeddingruntime.TaskDocument, costledger.EmbeddingOperationRAGReindex)
		}
		if vector == nil {
			result.Skipped++
			continue
		}
		inputHash := ContentHash(chunk.Content)
		if identity.ModelVersion != "" {
			emb, ok := m.repository.(EmbeddingRepository)
			if !ok {
				return result, fmt.Errorf("%w: repository does not support gemini chunk embeddings", ErrInvalidRequest)
			}
			if err := emb.InsertChunkEmbedding(ctx, ChunkEmbedding{
				OrganizationID: organizationID, ChunkID: chunk.ID, EmbeddingModelID: identity.ModelID, EmbeddingModelVersion: identity.ModelVersion,
				EmbeddingDimension: len(vector), PromptTemplateVersion: promptTemplateVersion, InputHash: inputHash, Vector: vector, CreatedAt: now,
			}); err != nil {
				return result, err
			}
		} else {
			emb, ok := m.repository.(BGEM3EmbeddingRepository)
			if !ok {
				return result, fmt.Errorf("%w: repository does not support bge-m3 chunk embeddings", ErrInvalidRequest)
			}
			if err := emb.InsertBGEM3ChunkEmbedding(ctx, BGEM3ChunkEmbedding{
				OrganizationID: organizationID, ChunkID: chunk.ID, EmbeddingModelID: identity.ModelID,
				ModelRevision: identity.ModelRevision, ArtifactSHA256: identity.ArtifactSHA256, TokenizerRevision: identity.TokenizerRevision,
				EmbeddingDimension: len(vector), Normalization: identity.Normalization, Pooling: identity.Pooling,
				PromptTemplateVersion: promptTemplateVersion, InputHash: inputHash, Vector: vector, CreatedAt: now,
			}); err != nil {
				return result, err
			}
		}
		result.Embedded++
	}
	// Computed after the loop, not before: see BackfillEmbeddingsResult.
	// Done's doc comment for why a page with any Skipped chunk can never
	// be Done, regardless of how the pending count alone compares to
	// batchSize.
	result.Done = len(pending) < batchSize && result.Skipped == 0
	return result, nil
}

type QueryRequest struct {
	OrganizationID string
	ActorRoleID    string
	Scope          NamespaceKind
	QueryText      string
	Limit          int
	// TaskID attributes an embedding call (if the vector channel is
	// configured, see SemanticSearchDeps) to the agent budget tree that
	// task belongs to. nil means budget tracking is skipped for this
	// query's embedding cost even if a wallet gate still applies —
	// mirrors modelruntime's costgate, where budget tracking is
	// independently optional per task.
	TaskID *int64
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
	queryVector := m.embedQuery(ctx, organizationID, actorRoleID, queryText, request.TaskID)
	command := QueryCommand{OrganizationID: organizationID, NamespaceKind: request.Scope, NamespaceID: namespaceID, QueryText: queryText, QueryVector: queryVector, Limit: limit}
	if len(queryVector) > 0 && m.semantic != nil {
		// The identity travels with the vector so the store can filter the
		// vector channel to rows produced under this exact space — never
		// rows from a different model revision/artifact/prompt template
		// that happen to share a dimension (see QueryCommand.EmbeddingIdentity).
		command.EmbeddingIdentity = m.semantic.Identity
		command.EmbeddingPromptTemplateVersion = m.semantic.PromptTemplateVersion
	}
	return m.repository.Query(ctx, command)
}

func (m *Manager) ActiveGeneration(ctx context.Context, organizationID string, namespaceKind NamespaceKind, namespaceID string) (IndexGeneration, bool, error) {
	return m.repository.ActiveGeneration(ctx, strings.TrimSpace(organizationID), namespaceKind, strings.TrimSpace(namespaceID))
}

// ExistingEvidenceReferences reports which evidence references starting with
// referencePrefix are already attached to some knowledge version for the
// organization. It exists so callers outside internal/rag (e.g. the
// organizational sleep cycle's idempotency check) never need direct SQL
// access to rag_knowledge_evidence_refs; like ActiveGeneration, this is
// system bookkeeping metadata, not namespace-scoped content, so it carries
// no authorization gate.
func (m *Manager) ExistingEvidenceReferences(ctx context.Context, organizationID, referencePrefix string) (map[string]bool, error) {
	return m.repository.ExistingEvidenceReferences(ctx, strings.TrimSpace(organizationID), referencePrefix)
}
