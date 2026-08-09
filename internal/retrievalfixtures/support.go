package retrievalfixtures

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/embeddingruntime"
	"github.com/Mireuz13/explorarte-organization/internal/memory"
	memorypostgres "github.com/Mireuz13/explorarte-organization/internal/memory/postgres"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/rag"
	ragpostgres "github.com/Mireuz13/explorarte-organization/internal/rag/postgres"
)

const (
	fixtureOrganization = "explorarte"
	fixtureActor        = "ingenieria_ia/orquestador"
	fixtureReviewer     = "empresa/human"
	geminiDimension     = 768
	bgeM3Dimension      = 1024
	geminiPrompt        = "fixture-gemini-query.v1"
	bgeM3Prompt         = "fixture-bge-m3-query.v1"
)

var errFixtureAuthorizationDenied = errors.New("fixture authorization denied")

type fixedClock struct{ now time.Time }

func (c *fixedClock) Now() time.Time { return c.now }

type ragGate struct{ deniedActor string }

func (g ragGate) Authorize(_ context.Context, request rag.AuthorizationRequest) error {
	if g.deniedActor != "" && request.ActorRoleID == g.deniedActor {
		return errFixtureAuthorizationDenied
	}
	return nil
}

type memoryGate struct{}

func (memoryGate) Authorize(context.Context, memory.AuthorizationRequest) error { return nil }

type namespaceResolver struct{ namespaceID string }

func (r namespaceResolver) ResolveNamespace(context.Context, string, string, rag.NamespaceKind) (string, error) {
	return r.namespaceID, nil
}

type deterministicAdapter struct {
	dimension int
	calls     atomic.Int64
}

func (a *deterministicAdapter) Embed(ctx context.Context, request embeddingruntime.EmbedRequest) (embeddingruntime.EmbedResponse, error) {
	if err := ctx.Err(); err != nil {
		return embeddingruntime.EmbedResponse{}, err
	}
	if request.OutputDimensionality != a.dimension {
		return embeddingruntime.EmbedResponse{}, fmt.Errorf("fixture adapter dimension=%d request=%d", a.dimension, request.OutputDimensionality)
	}
	a.calls.Add(1)
	results := make([]embeddingruntime.EmbedResult, 0, len(request.Items))
	var tokens int64
	for _, item := range request.Items {
		results = append(results, embeddingruntime.EmbedResult{Key: item.Key, Vector: semanticVector(a.dimension, item.Text)})
		tokens += int64(len(item.Text)/4) + 1
	}
	return embeddingruntime.EmbedResponse{Results: results, InputTokens: tokens, ProviderRequestID: "fixture-deterministic"}, nil
}

func semanticVector(dimension int, text string) []float32 {
	vector := make([]float32, dimension)
	normalized := strings.ToLower(text)
	switch {
	case strings.Contains(normalized, "error 2000"):
		vector[0] = -1
	case strings.Contains(normalized, "error-20"),
		strings.Contains(normalized, "fallo número veinte"),
		strings.Contains(normalized, "fallo numero veinte"),
		strings.Contains(normalized, "vigésima interrupción"),
		strings.Contains(normalized, "vigesima interrupcion"),
		strings.Contains(normalized, "deadlock"),
		strings.Contains(normalized, "espera circular"),
		strings.Contains(normalized, "bloqueo mutuo"):
		vector[0] = 1
	default:
		vector[1] = 1
	}
	return vector
}

func geminiIdentity() rag.EmbeddingIdentity {
	return rag.EmbeddingIdentity{ModelID: "fixture-gemini-embedding", ModelVersion: "fixture-v1"}
}

func bgeM3Identity() rag.EmbeddingIdentity {
	return rag.EmbeddingIdentity{
		ModelID: "bge-m3-local", ModelRevision: "fixture-revision-v1", ArtifactSHA256: strings.Repeat("a", 64),
		TokenizerRevision: "fixture-tokenizer-v1", Normalization: "l2", Pooling: "cls",
	}
}

func memoryGeminiIdentity(id rag.EmbeddingIdentity) memory.EmbeddingIdentity {
	return memory.EmbeddingIdentity{ModelID: id.ModelID, ModelVersion: id.ModelVersion}
}

func memoryBGEM3Identity(id rag.EmbeddingIdentity) memory.EmbeddingIdentity {
	return memory.EmbeddingIdentity{
		ModelID: id.ModelID, ModelRevision: id.ModelRevision, ArtifactSHA256: id.ArtifactSHA256,
		TokenizerRevision: id.TokenizerRevision, Normalization: id.Normalization, Pooling: id.Pooling,
	}
}

type retrievalProfile struct {
	name      string
	dimension int
	identity  rag.EmbeddingIdentity
	prompt    string
}

func profileForSubject(subjectID string) (retrievalProfile, error) {
	switch strings.TrimSpace(subjectID) {
	case "":
		return retrievalProfile{}, fmt.Errorf("retrieval fixture subject cannot be empty")
	case "lexical":
		return retrievalProfile{name: "lexical"}, nil
	case "gemini-hybrid":
		return retrievalProfile{name: "gemini-hybrid", dimension: geminiDimension, identity: geminiIdentity(), prompt: geminiPrompt}, nil
	case "bge-m3-hybrid":
		return retrievalProfile{name: "bge-m3-hybrid", dimension: bgeM3Dimension, identity: bgeM3Identity(), prompt: bgeM3Prompt}, nil
	default:
		// Organization-wide smoke subjects (for example a deployment or
		// coverage build identifier) exercise the target operational
		// profile. Explicit lexical/Gemini comparisons still select their
		// own arms above; an arbitrary non-empty label is never interpreted
		// as either of those reference profiles.
		return retrievalProfile{name: "bge-m3-hybrid", dimension: bgeM3Dimension, identity: bgeM3Identity(), prompt: bgeM3Prompt}, nil
	}
}

func scopedMemoryProfile(profile retrievalProfile, fixtureID, subjectID string) retrievalProfile {
	if profile.dimension == 0 {
		return profile
	}
	suffix := fixtureSuffix(fixtureID, subjectID)
	profile.prompt = "fixture-memory-" + suffix + ".v1"
	if profile.dimension == geminiDimension {
		profile.identity.ModelVersion = "fixture-" + suffix
		return profile
	}
	profile.identity.ModelRevision = "fixture-" + suffix
	hash := sha256.Sum256([]byte("fixture-bge-artifact|" + suffix))
	profile.identity.ArtifactSHA256 = hex.EncodeToString(hash[:])
	return profile
}

func semanticDepsForRAG(profile retrievalProfile, adapter *deterministicAdapter) *rag.SemanticSearchDeps {
	if profile.dimension == 0 {
		return nil
	}
	return &rag.SemanticSearchDeps{
		OnlineAdapter: adapter, LocalComputeOnly: true,
		ProviderID: "fixture-local", ProviderModelID: profile.identity.ModelID,
		OutputDimensionality: profile.dimension, PromptTemplateVersion: profile.prompt, Identity: profile.identity,
	}
}

func semanticDepsForMemory(profile retrievalProfile, adapter *deterministicAdapter, store *memorypostgres.Store) *memory.SemanticSearchDeps {
	if profile.dimension == 0 {
		return nil
	}
	deps := &memory.SemanticSearchDeps{
		OnlineAdapter: adapter, LocalComputeOnly: true,
		ProviderID: "fixture-local", ProviderModelID: profile.identity.ModelID,
		OutputDimensionality: profile.dimension, PromptTemplateVersion: profile.prompt,
	}
	if profile.dimension == geminiDimension {
		identity := memoryGeminiIdentity(profile.identity)
		deps.Identity = identity
		deps.InsertVector = func(ctx context.Context, organizationID, entryID, inputHash string, vector []float32, createdAt time.Time) error {
			return store.InsertEntryEmbedding(ctx, memory.EntryEmbedding{
				OrganizationID: organizationID, EntryID: entryID, EmbeddingModelID: identity.ModelID,
				EmbeddingModelVersion: identity.ModelVersion, EmbeddingDimension: len(vector),
				PromptTemplateVersion: profile.prompt, InputHash: inputHash, Vector: vector, CreatedAt: createdAt,
			})
		}
		return deps
	}
	identity := memoryBGEM3Identity(profile.identity)
	deps.Identity = identity
	deps.InsertVector = func(ctx context.Context, organizationID, entryID, inputHash string, vector []float32, createdAt time.Time) error {
		return store.InsertBGEM3EntryEmbedding(ctx, memory.BGEM3EntryEmbedding{
			OrganizationID: organizationID, EntryID: entryID, EmbeddingModelID: identity.ModelID,
			ModelRevision: identity.ModelRevision, ArtifactSHA256: identity.ArtifactSHA256,
			TokenizerRevision: identity.TokenizerRevision, EmbeddingDimension: len(vector),
			Normalization: identity.Normalization, Pooling: identity.Pooling,
			PromptTemplateVersion: profile.prompt, InputHash: inputHash, Vector: vector, CreatedAt: createdAt,
		})
	}
	return deps
}

func newRAGManager(store *ragpostgres.Store, clock *fixedClock, namespaceID string, gate ragGate, semantic *rag.SemanticSearchDeps) (*rag.Manager, error) {
	return rag.NewManager(rag.NewService(clock), store, gate, namespaceResolver{namespaceID: namespaceID}, semantic)
}

func newMemoryManager(store *memorypostgres.Store, clock *fixedClock, semantic *memory.SemanticSearchDeps) (*memory.Manager, error) {
	return memory.NewManager(memory.NewService(clock), store, memoryGate{}, semantic)
}

func fixtureSuffix(fixtureID, subjectID string) string {
	sum := sha256.Sum256([]byte("retrieval-runner-v5|" + fixtureID + "|" + subjectID))
	return hex.EncodeToString(sum[:6])
}

func fixtureNamespace(fixtureID, subjectID string) string {
	return "eval_" + strings.ReplaceAll(strings.TrimPrefix(fixtureID, "r30-"), "-", "_") + "_" + fixtureSuffix(fixtureID, subjectID)
}

func fixtureID(prefix, fixtureID, subjectID string) string {
	return prefix + "-" + fixtureSuffix(fixtureID, subjectID)
}

func fixtureBaseTime(seed int64) time.Time {
	return time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(seed) * time.Second)
}

func proposeApprovedRAG(ctx context.Context, manager *rag.Manager, clock *fixedClock, namespaceID, id, body string) (rag.KnowledgeVersion, error) {
	clock.now = clock.now.Add(time.Second)
	version, _, err := manager.Propose(ctx, rag.ProposeRequest{
		IdempotencyKey: "idem-" + id,
		Command: rag.ProposeCommand{
			ID: id, DocumentID: id, OrganizationID: fixtureOrganization,
			NamespaceKind: rag.NamespaceDepartment, NamespaceID: namespaceID, Version: 1,
			Title: "R30 retrieval fixture", Body: body, SourceKind: rag.SourceOperational,
			SourceReference: "evaluation:" + id, SourceRunRef: "r30-fixture",
			EvidenceRefs: []rag.EvidenceRef{{Reference: "evidence:" + id, Digest: rag.ContentHash(body)}},
			ProposedBy:   fixtureActor,
			Admission: rag.AdmissionAttestation{
				DataClass: rag.DataOrganizational, AttestedBy: fixtureActor, SourceBoundary: "evaluation_fixture",
				EvidenceRef: "admission:" + id, AttestedAt: clock.now.Add(-time.Second),
			},
		},
	})
	if err != nil {
		return rag.KnowledgeVersion{}, err
	}
	if version.Lifecycle == rag.LifecycleApproved {
		return version, nil
	}
	if version.Lifecycle != rag.LifecycleCandidate {
		return rag.KnowledgeVersion{}, fmt.Errorf("knowledge %s already has lifecycle %s", id, version.Lifecycle)
	}
	clock.now = clock.now.Add(time.Second)
	return manager.Review(ctx, rag.ReviewRequest{Mutation: rag.MutationRequest{
		OrganizationID: fixtureOrganization, VersionID: version.ID, ExpectedRevision: version.Revision,
		ActorRoleID: fixtureReviewer, Reason: "fixture approval",
	}, Outcome: rag.ReviewApprove})
}

func proposeMemory(ctx context.Context, manager *memory.Manager, clock *fixedClock, id, roleID, problem, correction string, approve bool) (memory.Entry, error) {
	clock.now = clock.now.Add(time.Second)
	entry, _, err := manager.Propose(ctx, memory.ProposeRequest{
		IdempotencyKey: "idem-" + id,
		Command: memory.ProposeCommand{
			ID: id, OrganizationID: fixtureOrganization, RoleID: roleID, Category: "evaluation_fixture",
			Problem: problem, Correction: correction, SourceKind: memory.SourceSyntheticTest, SourceRunID: 30,
			EvidenceRefs: []memory.EvidenceRef{{Reference: "evidence:" + id, Digest: rag.ContentHash(problem + correction)}},
			ProposedBy:   roleID,
			Admission: memory.AdmissionAttestation{
				DataClass: memory.DataOrganizational, AttestedBy: roleID, SourceBoundary: "evaluation_fixture",
				EvidenceRef: "admission:" + id, AttestedAt: clock.now.Add(-time.Second),
			},
		},
	})
	if err != nil {
		return memory.Entry{}, err
	}
	if !approve || entry.Status == memory.StatusApproved {
		return entry, nil
	}
	if entry.Status != memory.StatusCandidate {
		return memory.Entry{}, fmt.Errorf("memory %s already has status %s", id, entry.Status)
	}
	clock.now = clock.now.Add(time.Second)
	return manager.Review(ctx, memory.ReviewRequest{Mutation: memory.MutationRequest{
		OrganizationID: fixtureOrganization, EntryID: entry.ID, ExpectedRevision: entry.Revision,
		ActorRoleID: fixtureReviewer, Reason: "fixture approval",
	}, Outcome: memory.ReviewApprove})
}

type ragCorpus struct {
	generation rag.IndexGeneration
	chunkByDoc map[string]string
}

func prepareRAGCorpus(ctx context.Context, platform *platformpostgres.Store, store *ragpostgres.Store, manager *rag.Manager, clock *fixedClock, namespaceID string, docs map[string]string) (ragCorpus, error) {
	ids := make([]string, 0, len(docs))
	for id := range docs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	baseTime := clock.now
	for index, id := range ids {
		// Reset to a per-document instant instead of advancing from the
		// previous document. On a replay, an already-approved document skips
		// Review and therefore consumes fewer Clock.Now calls; a cumulative
		// clock would change the next document's admission hash and turn an
		// otherwise identical replay into an idempotency conflict.
		clock.now = baseTime.Add(time.Duration(index) * 10 * time.Second)
		body := docs[id]
		if _, err := proposeApprovedRAG(ctx, manager, clock, namespaceID, id, body); err != nil {
			return ragCorpus{}, fmt.Errorf("prepare rag document %s: %w", id, err)
		}
	}
	generation, err := manager.Reindex(ctx, rag.ReindexRequest{
		OrganizationID: fixtureOrganization, NamespaceKind: rag.NamespaceDepartment,
		NamespaceID: namespaceID, ActorRoleID: fixtureReviewer,
	})
	if err != nil {
		return ragCorpus{}, fmt.Errorf("reindex fixture corpus: %w", err)
	}
	rows, err := platform.Pool().Query(ctx, `
		SELECT c.chunk_id, v.document_id
		FROM rag_knowledge_chunks c
		JOIN rag_knowledge_versions v
		  ON v.organization_id=c.organization_id AND v.version_id=c.version_id
		WHERE c.organization_id=$1 AND c.generation_id=$2`, fixtureOrganization, generation.ID)
	if err != nil {
		return ragCorpus{}, fmt.Errorf("read fixture chunks: %w", err)
	}
	defer rows.Close()
	chunkByDoc := make(map[string]string, len(docs))
	for rows.Next() {
		var chunkID, documentID string
		if err := rows.Scan(&chunkID, &documentID); err != nil {
			return ragCorpus{}, err
		}
		chunkByDoc[documentID] = chunkID
	}
	if err := rows.Err(); err != nil {
		return ragCorpus{}, err
	}
	if len(chunkByDoc) != len(docs) {
		return ragCorpus{}, fmt.Errorf("fixture chunk cardinality=%d documents=%d", len(chunkByDoc), len(docs))
	}
	return ragCorpus{generation: generation, chunkByDoc: chunkByDoc}, nil
}

func seedRAGEmbeddings(ctx context.Context, store *ragpostgres.Store, corpus ragCorpus, vectorKinds map[string]string, now time.Time) error {
	gemini := geminiIdentity()
	bge := bgeM3Identity()
	for documentID, chunkID := range corpus.chunkByDoc {
		kind := vectorKinds[documentID]
		inputHash := rag.ContentHash("fixture-vector|" + documentID)
		if err := store.InsertChunkEmbedding(ctx, rag.ChunkEmbedding{
			OrganizationID: fixtureOrganization, ChunkID: chunkID,
			EmbeddingModelID: gemini.ModelID, EmbeddingModelVersion: gemini.ModelVersion,
			EmbeddingDimension: geminiDimension, PromptTemplateVersion: geminiPrompt,
			InputHash: inputHash, Vector: vectorByKind(geminiDimension, kind), CreatedAt: now,
		}); err != nil {
			return fmt.Errorf("insert gemini fixture embedding for %s: %w", documentID, err)
		}
		if err := store.InsertBGEM3ChunkEmbedding(ctx, rag.BGEM3ChunkEmbedding{
			OrganizationID: fixtureOrganization, ChunkID: chunkID, EmbeddingModelID: bge.ModelID,
			ModelRevision: bge.ModelRevision, ArtifactSHA256: bge.ArtifactSHA256, TokenizerRevision: bge.TokenizerRevision,
			EmbeddingDimension: bgeM3Dimension, Normalization: bge.Normalization, Pooling: bge.Pooling,
			PromptTemplateVersion: bgeM3Prompt, InputHash: inputHash, Vector: vectorByKind(bgeM3Dimension, kind), CreatedAt: now,
		}); err != nil {
			return fmt.Errorf("insert bge-m3 fixture embedding for %s: %w", documentID, err)
		}
	}
	return nil
}

func vectorByKind(dimension int, kind string) []float32 {
	vector := make([]float32, dimension)
	switch kind {
	case "close":
		vector[0] = 1
	case "opposite":
		vector[0] = -1
	default:
		vector[1] = 1
	}
	return vector
}

func seedMemoryEmbeddingSpace(ctx context.Context, store *memorypostgres.Store, profile retrievalProfile, entries map[string]string, now time.Time) error {
	if profile.dimension == 0 {
		return nil
	}
	for entryID, kind := range entries {
		inputHash := rag.ContentHash("fixture-memory-vector|" + entryID)
		if profile.dimension == geminiDimension {
			gemini := memoryGeminiIdentity(profile.identity)
			if err := store.InsertEntryEmbedding(ctx, memory.EntryEmbedding{
				OrganizationID: fixtureOrganization, EntryID: entryID, EmbeddingModelID: gemini.ModelID,
				EmbeddingModelVersion: gemini.ModelVersion, EmbeddingDimension: geminiDimension,
				PromptTemplateVersion: profile.prompt, InputHash: inputHash, Vector: vectorByKind(geminiDimension, kind), CreatedAt: now,
			}); err != nil {
				return err
			}
			continue
		}
		bge := memoryBGEM3Identity(profile.identity)
		if err := store.InsertBGEM3EntryEmbedding(ctx, memory.BGEM3EntryEmbedding{
			OrganizationID: fixtureOrganization, EntryID: entryID, EmbeddingModelID: bge.ModelID,
			ModelRevision: bge.ModelRevision, ArtifactSHA256: bge.ArtifactSHA256, TokenizerRevision: bge.TokenizerRevision,
			EmbeddingDimension: bgeM3Dimension, Normalization: bge.Normalization, Pooling: bge.Pooling,
			PromptTemplateVersion: profile.prompt, InputHash: inputHash, Vector: vectorByKind(bgeM3Dimension, kind), CreatedAt: now,
		}); err != nil {
			return err
		}
	}
	return nil
}

func containsDocument(results []rag.QueryResult, documentID string) bool {
	for _, result := range results {
		if result.DocumentID == documentID {
			return true
		}
	}
	return false
}

func containsEntry(results []memory.Entry, entryID string) bool {
	for _, result := range results {
		if result.ID == entryID {
			return true
		}
	}
	return false
}
