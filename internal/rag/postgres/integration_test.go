//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/rag"
	ragpostgres "github.com/Mireuz13/explorarte-organization/internal/rag/postgres"
	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
)

const (
	ragIntegrationOrganization = "explorarte"
	ragIntegrationProposer     = "investigacion/research_worker_hourly"
	ragIntegrationReviewer     = "empresa/human"
	ragIntegrationNamespace    = "ingenieria_ia"
)

type fixedClock struct{ now time.Time }

func (c *fixedClock) Now() time.Time { return c.now }

func TestApprovedKnowledgeRAGPostgresRepository(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	platform := openRAGStore(t, ctx)
	t.Cleanup(platform.Close)
	runner, err := platformmigrations.New(platform.Pool(), rootmigrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	result, err := runner.Up(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Compared against the runner's own Latest (the real migration tip
	// baked into this binary via rootmigrations.Files), not a hardcoded
	// version number that goes stale the moment a new migration lands.
	if status, statusErr := runner.Status(ctx); statusErr != nil {
		t.Fatal(statusErr)
	} else if result.Current != status.Latest {
		t.Fatalf("current migration=%d, want latest=%d", result.Current, status.Latest)
	}
	resetRAGSchema(t, ctx, platform)
	t.Cleanup(func() { resetRAGSchema(t, context.Background(), platform) })
	syncRAGCanonical(t, ctx, platform)
	store, err := ragpostgres.New(platform, ragIntegrationOrganization)
	if err != nil {
		t.Fatal(err)
	}
	var _ rag.Repository = store
	now := time.Now().UTC().Truncate(time.Microsecond)
	clock := &fixedClock{now: now}
	domain := rag.NewService(clock)

	t.Run("candidate creation is idempotent and conflicts on different content", func(t *testing.T) {
		version := proposeVersion(t, domain, clock, now, "know-roundtrip")
		created, reused, err := store.CreateCandidate(ctx, rag.CreateCandidateCommand{Version: version, IdempotencyKey: "idem-roundtrip"})
		if err != nil || reused {
			t.Fatalf("created=%+v reused=%v err=%v", created, reused, err)
		}
		again, reused, err := store.CreateCandidate(ctx, rag.CreateCandidateCommand{Version: version, IdempotencyKey: "idem-roundtrip"})
		if err != nil || !reused || again.ID != version.ID {
			t.Fatalf("again=%+v reused=%v err=%v", again, reused, err)
		}
		changed := version
		changed.ID = "know-other"
		changed.Title = "different"
		if _, _, err := store.CreateCandidate(ctx, rag.CreateCandidateCommand{Version: mustReconanonicalize(t, changed), IdempotencyKey: "idem-roundtrip"}); !errors.Is(err, rag.ErrIdempotencyConflict) {
			t.Fatalf("conflict=%v", err)
		}
	})

	t.Run("candidate is invisible to query and approved knowledge becomes retrievable after reindex", func(t *testing.T) {
		version := proposeVersion(t, domain, clock, now.Add(5*time.Second), "know-lifecycle")
		created, _, err := store.CreateCandidate(ctx, rag.CreateCandidateCommand{Version: version, IdempotencyKey: "idem-lifecycle"})
		if err != nil {
			t.Fatal(err)
		}
		results, err := store.Query(ctx, rag.QueryCommand{OrganizationID: ragIntegrationOrganization, NamespaceKind: rag.NamespaceDepartment, NamespaceID: ragIntegrationNamespace, QueryText: "egress", Limit: 10})
		if err != nil || len(results) != 0 {
			t.Fatalf("query before approval results=%+v err=%v", results, err)
		}
		clock.now = created.UpdatedAt.Add(time.Second)
		approved, err := domain.Review(created, rag.ReviewApprove, ragIntegrationReviewer)
		if err != nil {
			t.Fatal(err)
		}
		approved, err = store.Save(ctx, rag.SaveCommand{Version: approved, ExpectedRevision: 1, ActorID: ragIntegrationReviewer, Reason: "content verified"})
		if err != nil {
			t.Fatal(err)
		}

		chunks, err := rag.ChunkBody(approved.ID, rag.DefaultChunkerID, rag.DefaultChunkerVersion, approved.Body)
		if err != nil {
			t.Fatal(err)
		}
		generation, err := store.Reindex(ctx, rag.ReindexCommand{OrganizationID: ragIntegrationOrganization, NamespaceKind: rag.NamespaceDepartment, NamespaceID: ragIntegrationNamespace, ChunkerID: rag.DefaultChunkerID, ChunkerVersion: rag.DefaultChunkerVersion, Chunks: chunks})
		if err != nil {
			t.Fatal(err)
		}
		if generation.Status != rag.GenerationActive {
			t.Fatalf("generation=%+v", generation)
		}
		active, ok, err := store.ActiveGeneration(ctx, ragIntegrationOrganization, rag.NamespaceDepartment, ragIntegrationNamespace)
		if err != nil || !ok || active.ID != generation.ID {
			t.Fatalf("active=%+v ok=%v err=%v", active, ok, err)
		}

		results, err = store.Query(ctx, rag.QueryCommand{OrganizationID: ragIntegrationOrganization, NamespaceKind: rag.NamespaceDepartment, NamespaceID: ragIntegrationNamespace, QueryText: "egress", Limit: 10})
		if err != nil || len(results) == 0 {
			t.Fatalf("query after approval results=%+v err=%v", results, err)
		}
		if results[0].DocumentID != approved.DocumentID || results[0].CanonicalHash != approved.CanonicalHash || len(results[0].EvidenceRefs) == 0 {
			t.Fatalf("query result missing citation metadata: %+v", results[0])
		}

		clock.now = clock.now.Add(time.Minute)
		deprecated, err := domain.Deprecate(approved)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.Save(ctx, rag.SaveCommand{Version: deprecated, ExpectedRevision: 2, ActorID: ragIntegrationReviewer, Reason: "superseded"}); err != nil {
			t.Fatal(err)
		}
		results, err = store.Query(ctx, rag.QueryCommand{OrganizationID: ragIntegrationOrganization, NamespaceKind: rag.NamespaceDepartment, NamespaceID: ragIntegrationNamespace, QueryText: "egress", Limit: 10})
		if err != nil || len(results) != 0 {
			t.Fatalf("deprecated knowledge still retrievable: %+v err=%v", results, err)
		}
		var eventCount int
		if err := platform.Pool().QueryRow(ctx, `SELECT count(*) FROM rag_knowledge_lifecycle_events WHERE organization_id=$1 AND version_id=$2`, ragIntegrationOrganization, approved.ID).Scan(&eventCount); err != nil {
			t.Fatal(err)
		}
		if eventCount != 3 {
			t.Fatalf("expected candidate+approve+deprecate audit events, got %d", eventCount)
		}
	})

	t.Run("reindex activation is atomic across generations", func(t *testing.T) {
		const generationsNamespace = "ingenieria_ia_generations"
		version := proposeVersionInNamespace(t, domain, clock, now.Add(15*time.Second), "know-generations", generationsNamespace)
		created, _, err := store.CreateCandidate(ctx, rag.CreateCandidateCommand{Version: version, IdempotencyKey: "idem-generations"})
		if err != nil {
			t.Fatal(err)
		}
		clock.now = created.UpdatedAt.Add(time.Second)
		approved, err := domain.Review(created, rag.ReviewApprove, ragIntegrationReviewer)
		if err != nil {
			t.Fatal(err)
		}
		approved, err = store.Save(ctx, rag.SaveCommand{Version: approved, ExpectedRevision: 1, ActorID: ragIntegrationReviewer, Reason: "ok"})
		if err != nil {
			t.Fatal(err)
		}
		chunks, err := rag.ChunkBody(approved.ID, rag.DefaultChunkerID, rag.DefaultChunkerVersion, approved.Body)
		if err != nil {
			t.Fatal(err)
		}
		first, err := store.Reindex(ctx, rag.ReindexCommand{OrganizationID: ragIntegrationOrganization, NamespaceKind: rag.NamespaceDepartment, NamespaceID: generationsNamespace, ChunkerID: rag.DefaultChunkerID, ChunkerVersion: rag.DefaultChunkerVersion, Chunks: chunks})
		if err != nil {
			t.Fatal(err)
		}
		second, err := store.Reindex(ctx, rag.ReindexCommand{OrganizationID: ragIntegrationOrganization, NamespaceKind: rag.NamespaceDepartment, NamespaceID: generationsNamespace, ChunkerID: rag.DefaultChunkerID, ChunkerVersion: rag.DefaultChunkerVersion, Chunks: chunks})
		if err != nil {
			t.Fatal(err)
		}
		if second.Generation != first.Generation+1 {
			t.Fatalf("generation did not advance: first=%d second=%d", first.Generation, second.Generation)
		}
		var activeCount int
		if err := platform.Pool().QueryRow(ctx, `SELECT count(*) FROM rag_index_generations WHERE organization_id=$1 AND namespace_kind='department' AND namespace_id=$2 AND status='active'`, ragIntegrationOrganization, generationsNamespace).Scan(&activeCount); err != nil {
			t.Fatal(err)
		}
		if activeCount != 1 {
			t.Fatalf("expected exactly one active generation, found %d", activeCount)
		}
		var supersededCount int
		if err := platform.Pool().QueryRow(ctx, `SELECT count(*) FROM rag_index_generations WHERE organization_id=$1 AND namespace_kind='department' AND namespace_id=$2 AND status='superseded'`, ragIntegrationOrganization, generationsNamespace).Scan(&supersededCount); err != nil {
			t.Fatal(err)
		}
		if supersededCount != 1 {
			t.Fatalf("expected the first generation to become superseded, found %d", supersededCount)
		}
	})

	t.Run("stale revision is rejected", func(t *testing.T) {
		version := proposeVersion(t, domain, clock, now.Add(25*time.Second), "know-stale")
		created, _, err := store.CreateCandidate(ctx, rag.CreateCandidateCommand{Version: version, IdempotencyKey: "idem-stale"})
		if err != nil {
			t.Fatal(err)
		}
		clock.now = created.UpdatedAt.Add(time.Second)
		approved, err := domain.Review(created, rag.ReviewApprove, ragIntegrationReviewer)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.Save(ctx, rag.SaveCommand{Version: approved, ExpectedRevision: 99, ActorID: ragIntegrationReviewer, Reason: "stale"}); !errors.Is(err, rag.ErrRevisionConflict) {
			t.Fatalf("stale=%v", err)
		}
	})

	t.Run("database rejects illegal lifecycle transitions and non-approved indexing", func(t *testing.T) {
		version := proposeVersion(t, domain, clock, now.Add(35*time.Second), "know-db-guard")
		created, _, err := store.CreateCandidate(ctx, rag.CreateCandidateCommand{Version: version, IdempotencyKey: "idem-db-guard"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := platform.Pool().Exec(ctx, `UPDATE rag_knowledge_versions SET lifecycle='deprecated',reviewer_role_id=$3,reviewed_at=$4,revision=2,updated_at=$4 WHERE organization_id=$1 AND version_id=$2`, ragIntegrationOrganization, created.ID, ragIntegrationReviewer, created.UpdatedAt.Add(time.Second)); err == nil {
			t.Fatal("illegal candidate -> deprecated transition succeeded")
		}
		if _, err := platform.Pool().Exec(ctx, `INSERT INTO rag_knowledge_chunks (organization_id,chunk_id,generation_id,version_id,chunker_id,chunker_version,ordinal,start_offset,end_offset,content,content_hash) VALUES ($1,'bad-chunk','missing-generation',$2,'x','v1',1,0,4,'test',repeat('a',64))`, ragIntegrationOrganization, created.ID); err == nil {
			t.Fatal("indexing a non-approved version succeeded")
		}
	})

	t.Run("immutable rows cannot mutate", func(t *testing.T) {
		for name, statement := range map[string]string{
			"content":        `UPDATE rag_knowledge_versions SET body='tampered' WHERE organization_id='explorarte' AND version_id='know-roundtrip'`,
			"event":          `UPDATE rag_knowledge_lifecycle_events SET reason='tampered' WHERE organization_id='explorarte' AND version_id='know-roundtrip'`,
			"idempotency":    `DELETE FROM rag_knowledge_idempotency WHERE organization_id='explorarte' AND idempotency_key='idem-roundtrip'`,
			"version_delete": `DELETE FROM rag_knowledge_versions WHERE organization_id='explorarte' AND version_id='know-roundtrip'`,
		} {
			t.Run(name, func(t *testing.T) {
				if _, err := platform.Pool().Exec(ctx, statement); err == nil {
					t.Fatalf("%s mutation succeeded", name)
				}
			})
		}
	})

	t.Run("ApprovedForNamespace paginates past the single-query limit instead of silently truncating", func(t *testing.T) {
		namespaceID := "ingenieria_ia_pagination"
		const total = 1050 // maxListLimit (1000) + 50, to prove pagination actually ran, not just a coincidental single page.
		pageClock := &fixedClock{now: now.Add(time.Hour)}
		for i := 0; i < total; i++ {
			docID := fmt.Sprintf("know-page-%04d", i)
			pageClock.now = pageClock.now.Add(time.Millisecond)
			candidate, err := rag.NewService(pageClock).Propose(rag.ProposeCommand{
				ID: docID, DocumentID: docID, OrganizationID: ragIntegrationOrganization,
				NamespaceKind: rag.NamespaceDepartment, NamespaceID: namespaceID, Version: 1,
				Title: "pagination fixture", Body: fmt.Sprintf("pagination fixture body %d", i),
				SourceKind: rag.SourceOperational, SourceReference: "pagination:fixture", ProposedBy: ragIntegrationProposer,
				EvidenceRefs: []rag.EvidenceRef{{Reference: "evidence:pagination", Digest: "aaa"}},
				Admission:    rag.AdmissionAttestation{DataClass: rag.DataOrganizational, AttestedBy: ragIntegrationProposer, SourceBoundary: "organization", EvidenceRef: "admission:" + docID, AttestedAt: pageClock.now.Add(-time.Second)},
			})
			if err != nil {
				t.Fatalf("propose fixture %d: %v", i, err)
			}
			created, _, err := store.CreateCandidate(ctx, rag.CreateCandidateCommand{Version: candidate, IdempotencyKey: "idem-page-" + docID})
			if err != nil {
				t.Fatalf("create candidate fixture %d: %v", i, err)
			}
			pageClock.now = pageClock.now.Add(time.Millisecond)
			approved, err := rag.NewService(pageClock).Review(created, rag.ReviewApprove, ragIntegrationReviewer)
			if err != nil {
				t.Fatalf("review fixture %d: %v", i, err)
			}
			if _, err := store.Save(ctx, rag.SaveCommand{Version: approved, ExpectedRevision: 1, ActorID: ragIntegrationReviewer, Reason: "pagination fixture"}); err != nil {
				t.Fatalf("save fixture %d: %v", i, err)
			}
		}

		approved, err := store.ApprovedForNamespace(ctx, ragIntegrationOrganization, rag.NamespaceDepartment, namespaceID)
		if err != nil {
			t.Fatal(err)
		}
		if len(approved) != total {
			t.Fatalf("ApprovedForNamespace returned %d approved versions, want %d (silently truncated at the single-query limit)", len(approved), total)
		}
		seen := make(map[string]bool, total)
		for _, version := range approved {
			if seen[version.ID] {
				t.Fatalf("duplicate version %s across pages", version.ID)
			}
			seen[version.ID] = true
		}
	})

	t.Run("database rejects clinical and secret data classes", func(t *testing.T) {
		for _, class := range []string{"clinical", "secret"} {
			_, err := platform.Pool().Exec(ctx, `INSERT INTO rag_knowledge_versions (organization_id,version_id,document_id,namespace_kind,namespace_id,version,title,body,source_kind,source_reference,proposed_by_role_id,data_class,admission_attested_by,source_boundary,admission_evidence_ref,admission_attested_at,content_hash,canonical_hash,lifecycle,revision,created_at,updated_at) VALUES ($1,$2,'know-db-guard','department','ingenieria_ia',99,'t','b','research','ref',$3,$4,$3,'organization','ref',NOW(),repeat('a',64),repeat('b',64),'candidate',1,NOW(),NOW())`,
				ragIntegrationOrganization, "raw-forbidden-"+class, ragIntegrationProposer, class)
			if err == nil {
				t.Fatalf("accepted %s", class)
			}
		}
	})

	t.Run("chunk embeddings live in a derived table and never block lexical retrieval", func(t *testing.T) {
		const embeddingsNamespace = "ingenieria_ia_embeddings"
		version := proposeVersionInNamespace(t, domain, clock, now.Add(45*time.Second), "know-embeddings", embeddingsNamespace)
		created, _, err := store.CreateCandidate(ctx, rag.CreateCandidateCommand{Version: version, IdempotencyKey: "idem-embeddings"})
		if err != nil {
			t.Fatal(err)
		}
		clock.now = created.UpdatedAt.Add(time.Second)
		approved, err := domain.Review(created, rag.ReviewApprove, ragIntegrationReviewer)
		if err != nil {
			t.Fatal(err)
		}
		approved, err = store.Save(ctx, rag.SaveCommand{Version: approved, ExpectedRevision: 1, ActorID: ragIntegrationReviewer, Reason: "ok"})
		if err != nil {
			t.Fatal(err)
		}
		chunks, err := rag.ChunkBody(approved.ID, rag.DefaultChunkerID, rag.DefaultChunkerVersion, approved.Body)
		if err != nil {
			t.Fatal(err)
		}
		generation, err := store.Reindex(ctx, rag.ReindexCommand{OrganizationID: ragIntegrationOrganization, NamespaceKind: rag.NamespaceDepartment, NamespaceID: embeddingsNamespace, ChunkerID: rag.DefaultChunkerID, ChunkerVersion: rag.DefaultChunkerVersion, Chunks: chunks})
		if err != nil {
			t.Fatal(err)
		}
		results, err := store.Query(ctx, rag.QueryCommand{OrganizationID: ragIntegrationOrganization, NamespaceKind: rag.NamespaceDepartment, NamespaceID: embeddingsNamespace, QueryText: "egress", Limit: 10})
		if err != nil || len(results) == 0 {
			t.Fatalf("lexical query before any embedding exists: results=%+v err=%v", results, err)
		}
		chunkID := results[0].Chunk.ID

		vector := make([]float32, 768)
		vector[0] = 1
		if err := store.InsertChunkEmbedding(ctx, rag.ChunkEmbedding{
			OrganizationID: ragIntegrationOrganization, ChunkID: chunkID,
			EmbeddingModelID: "gemini-embedding-2", EmbeddingModelVersion: "v1", EmbeddingDimension: 768,
			PromptTemplateVersion: "prompt-template.v1", InputHash: fmt.Sprintf("%064x", 1), Vector: vector, CreatedAt: clock.now,
		}); err != nil {
			t.Fatal(err)
		}
		// Inserting an embedding must never touch the immutable chunk row
		// itself — this is the entire point of the derived-table design.
		results, err = store.Query(ctx, rag.QueryCommand{OrganizationID: ragIntegrationOrganization, NamespaceKind: rag.NamespaceDepartment, NamespaceID: embeddingsNamespace, QueryText: "egress", Limit: 10})
		if err != nil || len(results) == 0 || results[0].Chunk.ID != chunkID {
			t.Fatalf("lexical query after embedding exists: results=%+v err=%v", results, err)
		}

		nearest, err := store.NearestChunks(ctx, ragIntegrationOrganization, generation.ID, vector, 5)
		if err != nil {
			t.Fatal(err)
		}
		if len(nearest) != 1 || nearest[0].ChunkID != chunkID || nearest[0].Distance > 0.0001 {
			t.Fatalf("nearest=%+v want exactly chunkID=%s at ~0 distance", nearest, chunkID)
		}

		// A second insert for the same (chunk, model, model version) must be
		// a no-op (ON CONFLICT DO NOTHING) — re-embedding under a new
		// version is a new row, never a silent overwrite of an existing one.
		otherVector := make([]float32, 768)
		otherVector[1] = 1
		if err := store.InsertChunkEmbedding(ctx, rag.ChunkEmbedding{
			OrganizationID: ragIntegrationOrganization, ChunkID: chunkID,
			EmbeddingModelID: "gemini-embedding-2", EmbeddingModelVersion: "v1", EmbeddingDimension: 768,
			PromptTemplateVersion: "prompt-template.v1", InputHash: fmt.Sprintf("%064x", 2), Vector: otherVector, CreatedAt: clock.now,
		}); err != nil {
			t.Fatal(err)
		}
		nearest, err = store.NearestChunks(ctx, ragIntegrationOrganization, generation.ID, vector, 5)
		if err != nil || len(nearest) != 1 || nearest[0].Distance > 0.0001 {
			t.Fatalf("second insert for the same model version must not have overwritten the first: nearest=%+v err=%v", nearest, err)
		}

		if _, err := platform.Pool().Exec(ctx, `UPDATE rag_chunk_embeddings SET embedding_dimension=768 WHERE organization_id=$1 AND chunk_id=$2`, ragIntegrationOrganization, chunkID); err == nil {
			t.Fatal("expected rag_chunk_embeddings to reject UPDATE the same way canonical rag tables do")
		}
	})

	t.Run("embedding batch job bookkeeping tracks per-item outcomes", func(t *testing.T) {
		const batchNamespace = "ingenieria_ia_batch"
		version := proposeVersionInNamespace(t, domain, clock, now.Add(55*time.Second), "know-batch", batchNamespace)
		created, _, err := store.CreateCandidate(ctx, rag.CreateCandidateCommand{Version: version, IdempotencyKey: "idem-batch"})
		if err != nil {
			t.Fatal(err)
		}
		clock.now = created.UpdatedAt.Add(time.Second)
		approved, err := domain.Review(created, rag.ReviewApprove, ragIntegrationReviewer)
		if err != nil {
			t.Fatal(err)
		}
		approved, err = store.Save(ctx, rag.SaveCommand{Version: approved, ExpectedRevision: 1, ActorID: ragIntegrationReviewer, Reason: "ok"})
		if err != nil {
			t.Fatal(err)
		}
		chunks, err := rag.ChunkBody(approved.ID, rag.DefaultChunkerID, rag.DefaultChunkerVersion, approved.Body)
		if err != nil {
			t.Fatal(err)
		}
		generation, err := store.Reindex(ctx, rag.ReindexCommand{OrganizationID: ragIntegrationOrganization, NamespaceKind: rag.NamespaceDepartment, NamespaceID: batchNamespace, ChunkerID: rag.DefaultChunkerID, ChunkerVersion: rag.DefaultChunkerVersion, Chunks: chunks})
		if err != nil {
			t.Fatal(err)
		}
		results, err := store.Query(ctx, rag.QueryCommand{OrganizationID: ragIntegrationOrganization, NamespaceKind: rag.NamespaceDepartment, NamespaceID: batchNamespace, QueryText: "egress", Limit: 10})
		if err != nil || len(results) == 0 {
			t.Fatal(err)
		}
		chunkID := results[0].Chunk.ID

		job, err := store.CreateEmbeddingBatchJob(ctx, rag.EmbeddingBatchJob{
			OrganizationID: ragIntegrationOrganization, NamespaceKind: rag.NamespaceDepartment, NamespaceID: batchNamespace,
			GenerationID: generation.ID, ProviderID: "gemini", ProviderModelID: "gemini-embedding-2",
			Status: "running", ShardIndex: 0, ItemCount: 2, CreatedAt: clock.now,
		}, []rag.EmbeddingBatchJobItem{
			{ItemKey: "item-ok", ChunkID: chunkID},
			{ItemKey: "item-fail", ChunkID: chunkID},
		})
		if err != nil {
			t.Fatal(err)
		}
		if job.ID == 0 {
			t.Fatal("expected a generated job id")
		}

		successVector := make([]float32, 768)
		successVector[2] = 1
		if err := store.RecordEmbeddingBatchJobItemResult(ctx, job.ID, "item-ok", &rag.ChunkEmbedding{
			OrganizationID: ragIntegrationOrganization, ChunkID: chunkID,
			EmbeddingModelID: "gemini-embedding-2", EmbeddingModelVersion: "batch-v1", EmbeddingDimension: 768,
			PromptTemplateVersion: "prompt-template.v1", InputHash: fmt.Sprintf("%064x", 3), Vector: successVector, CreatedAt: clock.now,
		}, ""); err != nil {
			t.Fatal(err)
		}
		if err := store.RecordEmbeddingBatchJobItemResult(ctx, job.ID, "item-fail", nil, "input token count exceeds the maximum limit"); err != nil {
			t.Fatal(err)
		}

		var succeeded, failed int
		if err := platform.Pool().QueryRow(ctx, `SELECT count(*) FILTER (WHERE status='succeeded'), count(*) FILTER (WHERE status='failed') FROM rag_embedding_batch_job_items WHERE job_id=$1`, job.ID).Scan(&succeeded, &failed); err != nil {
			t.Fatal(err)
		}
		if succeeded != 1 || failed != 1 {
			t.Fatalf("succeeded=%d failed=%d want 1/1", succeeded, failed)
		}

		nearest, err := store.NearestChunks(ctx, ragIntegrationOrganization, generation.ID, successVector, 5)
		if err != nil || len(nearest) != 1 || nearest[0].Distance > 0.0001 {
			t.Fatalf("batch-produced embedding not queryable: nearest=%+v err=%v", nearest, err)
		}

		if err := store.CompleteEmbeddingBatchJob(ctx, job.ID, "succeeded", clock.now.Add(time.Minute), 1); err != nil {
			t.Fatal(err)
		}
		var status string
		var failedCount int
		if err := platform.Pool().QueryRow(ctx, `SELECT status, failed_item_count FROM rag_embedding_batch_jobs WHERE id=$1`, job.ID).Scan(&status, &failedCount); err != nil {
			t.Fatal(err)
		}
		if status != "succeeded" || failedCount != 1 {
			t.Fatalf("status=%q failedCount=%d", status, failedCount)
		}
	})

	t.Run("identifier_tokens exact-match channel never conflates different numbers", func(t *testing.T) {
		const identifierNamespace = "ingenieria_ia_identifiers"
		clock.now = now.Add(65 * time.Second)
		hyphenated, err := domain.Propose(rag.ProposeCommand{
			ID: "know-identifier-hyphenated", DocumentID: "know-identifier-hyphenated", OrganizationID: ragIntegrationOrganization,
			NamespaceKind: rag.NamespaceDepartment, NamespaceID: identifierNamespace, Version: 1,
			Title: "identifier fixture", Body: "agent hit error-20 during dispatch", SourceKind: rag.SourceOperational,
			SourceReference: "identifier:fixture", ProposedBy: ragIntegrationProposer,
			EvidenceRefs: []rag.EvidenceRef{{Reference: "evidence:id20", Digest: "aaa"}},
			Admission:    rag.AdmissionAttestation{DataClass: rag.DataOrganizational, AttestedBy: ragIntegrationProposer, SourceBoundary: "organization", EvidenceRef: "admission:know-identifier-hyphenated", AttestedAt: clock.now.Add(-time.Second)},
		})
		if err != nil {
			t.Fatal(err)
		}
		hyphenatedCreated, _, err := store.CreateCandidate(ctx, rag.CreateCandidateCommand{Version: hyphenated, IdempotencyKey: "idem-identifier-hyphenated"})
		if err != nil {
			t.Fatal(err)
		}
		clock.now = clock.now.Add(time.Second)
		hyphenatedApproved, err := domain.Review(hyphenatedCreated, rag.ReviewApprove, ragIntegrationReviewer)
		if err != nil {
			t.Fatal(err)
		}
		hyphenatedApproved, err = store.Save(ctx, rag.SaveCommand{Version: hyphenatedApproved, ExpectedRevision: 1, ActorID: ragIntegrationReviewer, Reason: "ok"})
		if err != nil {
			t.Fatal(err)
		}

		clock.now = clock.now.Add(time.Second)
		larger, err := domain.Propose(rag.ProposeCommand{
			ID: "know-identifier-larger", DocumentID: "know-identifier-larger", OrganizationID: ragIntegrationOrganization,
			NamespaceKind: rag.NamespaceDepartment, NamespaceID: identifierNamespace, Version: 1,
			Title: "identifier fixture", Body: "agent hit error 2000 during dispatch", SourceKind: rag.SourceOperational,
			SourceReference: "identifier:fixture", ProposedBy: ragIntegrationProposer,
			EvidenceRefs: []rag.EvidenceRef{{Reference: "evidence:id2000", Digest: "bbb"}},
			Admission:    rag.AdmissionAttestation{DataClass: rag.DataOrganizational, AttestedBy: ragIntegrationProposer, SourceBoundary: "organization", EvidenceRef: "admission:know-identifier-larger", AttestedAt: clock.now.Add(-time.Second)},
		})
		if err != nil {
			t.Fatal(err)
		}
		largerCreated, _, err := store.CreateCandidate(ctx, rag.CreateCandidateCommand{Version: larger, IdempotencyKey: "idem-identifier-larger"})
		if err != nil {
			t.Fatal(err)
		}
		clock.now = clock.now.Add(time.Second)
		largerApproved, err := domain.Review(largerCreated, rag.ReviewApprove, ragIntegrationReviewer)
		if err != nil {
			t.Fatal(err)
		}
		largerApproved, err = store.Save(ctx, rag.SaveCommand{Version: largerApproved, ExpectedRevision: 1, ActorID: ragIntegrationReviewer, Reason: "ok"})
		if err != nil {
			t.Fatal(err)
		}

		for _, approved := range []rag.KnowledgeVersion{hyphenatedApproved, largerApproved} {
			chunks, err := rag.ChunkBody(approved.ID, rag.DefaultChunkerID, rag.DefaultChunkerVersion, approved.Body)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.Reindex(ctx, rag.ReindexCommand{OrganizationID: ragIntegrationOrganization, NamespaceKind: rag.NamespaceDepartment, NamespaceID: identifierNamespace, ChunkerID: rag.DefaultChunkerID, ChunkerVersion: rag.DefaultChunkerVersion, Chunks: chunks}); err != nil {
				t.Fatal(err)
			}
		}

		var tokens []string
		if err := platform.Pool().QueryRow(ctx, `SELECT c.identifier_tokens FROM rag_knowledge_chunks c JOIN rag_knowledge_versions v ON v.organization_id=c.organization_id AND v.version_id=c.version_id WHERE c.organization_id=$1 AND v.document_id=$2`, ragIntegrationOrganization, "know-identifier-hyphenated").Scan(&tokens); err != nil {
			t.Fatal(err)
		}
		if len(tokens) != 1 || tokens[0] != "20" {
			t.Fatalf("hyphenated chunk identifier_tokens=%v want [20]", tokens)
		}

		var matchedDocument string
		err = platform.Pool().QueryRow(ctx, `SELECT v.document_id FROM rag_knowledge_chunks c JOIN rag_knowledge_versions v ON v.organization_id=c.organization_id AND v.version_id=c.version_id WHERE c.organization_id=$1 AND c.identifier_tokens && ARRAY['20']`, ragIntegrationOrganization).Scan(&matchedDocument)
		if err != nil {
			t.Fatal(err)
		}
		if matchedDocument != "know-identifier-hyphenated" {
			t.Fatalf("searching for identifier '20' matched document %q, want know-identifier-hyphenated (never know-identifier-larger's '2000')", matchedDocument)
		}
	})

	t.Run("Query fuses exact, lexical, and vector channels by RRF", func(t *testing.T) {
		const hybridNamespace = "ingenieria_ia_hybrid"
		clock.now = now.Add(75 * time.Second)

		// lexicalOnly is findable by plain full-text search (shares words
		// with the query) but has no embedding and no shared identifier —
		// it must still surface with QueryVector set, proving the lexical
		// channel keeps contributing even when the vector channel is
		// active, not just when it's the only channel available.
		lexicalOnly := proposeVersionInNamespace(t, domain, clock, clock.now, "know-hybrid-lexical", hybridNamespace)
		lexicalOnly.Title = "hybrid fixture"
		lexicalOnly.Body = "the coolant pump failed during the overnight shift"
		lexicalOnly = mustReconanonicalize(t, lexicalOnly)
		lexicalCreated, _, err := store.CreateCandidate(ctx, rag.CreateCandidateCommand{Version: lexicalOnly, IdempotencyKey: "idem-hybrid-lexical"})
		if err != nil {
			t.Fatal(err)
		}
		clock.now = clock.now.Add(time.Second)
		lexicalApproved, err := domain.Review(lexicalCreated, rag.ReviewApprove, ragIntegrationReviewer)
		if err != nil {
			t.Fatal(err)
		}
		lexicalApproved, err = store.Save(ctx, rag.SaveCommand{Version: lexicalApproved, ExpectedRevision: 1, ActorID: ragIntegrationReviewer, Reason: "ok"})
		if err != nil {
			t.Fatal(err)
		}

		// vectorOnly shares no words and no digits with the query text — a
		// pure lexical+exact search must never find it. It is only
		// reachable through a chunk embedding placed at ~0 distance from
		// the query vector.
		clock.now = clock.now.Add(time.Second)
		vectorOnly := proposeVersionInNamespace(t, domain, clock, clock.now, "know-hybrid-vector", hybridNamespace)
		vectorOnly.Title = "hybrid fixture"
		vectorOnly.Body = "reactor core temperature exceeded the safety threshold"
		vectorOnly = mustReconanonicalize(t, vectorOnly)
		vectorCreated, _, err := store.CreateCandidate(ctx, rag.CreateCandidateCommand{Version: vectorOnly, IdempotencyKey: "idem-hybrid-vector"})
		if err != nil {
			t.Fatal(err)
		}
		clock.now = clock.now.Add(time.Second)
		vectorApproved, err := domain.Review(vectorCreated, rag.ReviewApprove, ragIntegrationReviewer)
		if err != nil {
			t.Fatal(err)
		}
		vectorApproved, err = store.Save(ctx, rag.SaveCommand{Version: vectorApproved, ExpectedRevision: 1, ActorID: ragIntegrationReviewer, Reason: "ok"})
		if err != nil {
			t.Fatal(err)
		}

		// A single Reindex call replaces the whole active generation with
		// exactly the chunks it's given — it does not merge into whatever
		// generation already existed. Both documents' chunks must be
		// gathered and indexed together in one call, or the second call
		// would supersede the first document clean out of the active
		// generation Query() reads from.
		var hybridChunks []rag.Chunk
		for _, approved := range []rag.KnowledgeVersion{lexicalApproved, vectorApproved} {
			chunks, err := rag.ChunkBody(approved.ID, rag.DefaultChunkerID, rag.DefaultChunkerVersion, approved.Body)
			if err != nil {
				t.Fatal(err)
			}
			hybridChunks = append(hybridChunks, chunks...)
		}
		if _, err := store.Reindex(ctx, rag.ReindexCommand{OrganizationID: ragIntegrationOrganization, NamespaceKind: rag.NamespaceDepartment, NamespaceID: hybridNamespace, ChunkerID: rag.DefaultChunkerID, ChunkerVersion: rag.DefaultChunkerVersion, Chunks: hybridChunks}); err != nil {
			t.Fatal(err)
		}

		var vectorChunkID string
		if err := platform.Pool().QueryRow(ctx, `SELECT c.chunk_id FROM rag_knowledge_chunks c JOIN rag_knowledge_versions v ON v.organization_id=c.organization_id AND v.version_id=c.version_id WHERE c.organization_id=$1 AND v.document_id=$2`, ragIntegrationOrganization, "know-hybrid-vector").Scan(&vectorChunkID); err != nil {
			t.Fatal(err)
		}
		queryVector := make([]float32, 768)
		queryVector[5] = 1
		if err := store.InsertChunkEmbedding(ctx, rag.ChunkEmbedding{
			OrganizationID: ragIntegrationOrganization, ChunkID: vectorChunkID,
			EmbeddingModelID: "gemini-embedding-2", EmbeddingModelVersion: "v1", EmbeddingDimension: 768,
			PromptTemplateVersion: "prompt-template.v1", InputHash: fmt.Sprintf("%064x", 9), Vector: queryVector, CreatedAt: clock.now,
		}); err != nil {
			t.Fatal(err)
		}

		// Without a query vector, only the lexical channel can find
		// anything — vectorOnly shares no words with the query.
		lexicalOnlyResults, err := store.Query(ctx, rag.QueryCommand{OrganizationID: ragIntegrationOrganization, NamespaceKind: rag.NamespaceDepartment, NamespaceID: hybridNamespace, QueryText: "overnight coolant pump", Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if len(lexicalOnlyResults) != 1 || lexicalOnlyResults[0].DocumentID != "know-hybrid-lexical" {
			t.Fatalf("lexical-only query results=%+v", lexicalOnlyResults)
		}

		// With the query vector supplied, the vector channel surfaces
		// vectorOnly even though it shares zero lexical/identifier overlap
		// with the query text — RRF fusion, not replacement: the lexical
		// hit must still be present too.
		fused, err := store.Query(ctx, rag.QueryCommand{OrganizationID: ragIntegrationOrganization, NamespaceKind: rag.NamespaceDepartment, NamespaceID: hybridNamespace, QueryText: "overnight coolant pump", QueryVector: queryVector, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		documents := make(map[string]bool, len(fused))
		for _, result := range fused {
			documents[result.DocumentID] = true
		}
		if !documents["know-hybrid-lexical"] || !documents["know-hybrid-vector"] {
			t.Fatalf("fused query documents=%v want both know-hybrid-lexical and know-hybrid-vector", documents)
		}
	})
}

func proposeVersion(t *testing.T, domain *rag.Service, clock *fixedClock, now time.Time, id string) rag.KnowledgeVersion {
	t.Helper()
	return proposeVersionInNamespace(t, domain, clock, now, id, ragIntegrationNamespace)
}

func proposeVersionInNamespace(t *testing.T, domain *rag.Service, clock *fixedClock, now time.Time, id string, namespaceID string) rag.KnowledgeVersion {
	t.Helper()
	clock.now = now
	version, err := domain.Propose(rag.ProposeCommand{
		ID: id, DocumentID: id, OrganizationID: ragIntegrationOrganization, NamespaceKind: rag.NamespaceDepartment, NamespaceID: namespaceID,
		Version: 1, Title: "Gestión de riesgos en despliegues de modelos", Body: "Antes de desplegar un modelo nuevo, valida la política de egress y el owner del dataset.\n\nRegistra la evidencia de validación en el ticket de staging.",
		SourceKind: rag.SourceResearch, SourceReference: "investigacion:report:41", ProposedBy: ragIntegrationProposer,
		EvidenceRefs: []rag.EvidenceRef{{Reference: "evidence:a", Digest: "aaa"}},
		Admission:    rag.AdmissionAttestation{DataClass: rag.DataOrganizational, AttestedBy: ragIntegrationProposer, SourceBoundary: "organization", EvidenceRef: "admission:" + id, AttestedAt: now.Add(-time.Second)},
	})
	if err != nil {
		t.Fatal(err)
	}
	return version
}

func mustReconanonicalize(t *testing.T, version rag.KnowledgeVersion) rag.KnowledgeVersion {
	t.Helper()
	version.ContentHash = rag.ContentHash(version.Body)
	hash, err := version.ComputeCanonicalHash()
	if err != nil {
		t.Fatal(err)
	}
	version.CanonicalHash = hash
	return version
}

func openRAGStore(t *testing.T, ctx context.Context) *platformpostgres.Store {
	t.Helper()
	url := os.Getenv("ORG_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("ORG_TEST_DATABASE_URL is required")
	}
	cfg := config.DatabaseConfig{URL: url, SSLMode: "disable", MaxConns: 30, MinConns: 0, MaxConnLifetime: time.Minute, MaxConnIdleTime: time.Minute, HealthCheckPeriod: time.Second, ConnectTimeout: 5 * time.Second, PingTimeout: 5 * time.Second, StatementTimeout: 30 * time.Second, LockTimeout: 5 * time.Second, AutoMigrate: true, MigrationTimeout: 45 * time.Second, MigrationRetry: time.Second}
	store, err := platformpostgres.Open(ctx, cfg, "rag-integration")
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func resetRAGSchema(t *testing.T, ctx context.Context, store *platformpostgres.Store) {
	t.Helper()
	resetCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// rag_index_generations and rag_knowledge_chunks carry organization_id
	// as a plain column with no foreign key back to organizations (see
	// migrations/000017_create_approved_knowledge_rag.up.sql) — TRUNCATE
	// ... CASCADE from organizations never reaches them, so they must be
	// listed explicitly or rows accumulate across every test run against a
	// long-lived database instead of being reset.
	if _, err := store.Pool().Exec(resetCtx, `TRUNCATE organizations, organization_registry_revisions, audit_events,
		rag_index_generations, rag_knowledge_chunks RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("reset: %v", err)
	}
}

func syncRAGCanonical(t *testing.T, ctx context.Context, store *platformpostgres.Store) *registry.Revision {
	t.Helper()
	repo, err := registry.NewPostgresRepository(store)
	if err != nil {
		t.Fatal(err)
	}
	loader, err := registry.NewLoader(filepath.Join("..", "..", "..", "docs", "canonical"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := registry.NewService(loader, repo, ragIntegrationOrganization, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	res, err := service.SynchronizeCanonical(ctx, true)
	if err != nil || !res.Applied {
		t.Fatalf("sync=%+v err=%v", res, err)
	}
	revision, err := repo.GetCurrentRevision(ctx, ragIntegrationOrganization)
	if err != nil || revision == nil {
		t.Fatalf("revision=%+v err=%v", revision, err)
	}
	return revision
}
