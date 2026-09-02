//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/rag"
	ragpostgres "github.com/Mireuz13/explorarte-organization/internal/rag/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/testdbguard"
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

	// ORG-AUDIT-011 regression: a chunk that is internally self-consistent
	// (its own hash matches its own content) and references an approved
	// version in the right namespace must still be rejected if its content
	// is not actually derived from that version's approved body -- neither
	// check alone catches a forged chunk that carries someone else's text
	// under an honest version_id/canonical_hash.
	t.Run("reindex rejects a chunk whose content was not derived from the approved body", func(t *testing.T) {
		const forgeryNamespace = "ingenieria_ia_forgery"
		version := proposeVersionInNamespace(t, domain, clock, now.Add(20*time.Second), "know-forgery", forgeryNamespace)
		created, _, err := store.CreateCandidate(ctx, rag.CreateCandidateCommand{Version: version, IdempotencyKey: "idem-forgery"})
		if err != nil {
			t.Fatal(err)
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
		if err != nil || len(chunks) == 0 {
			t.Fatalf("chunks=%v err=%v", chunks, err)
		}
		forged := make([]rag.Chunk, len(chunks))
		copy(forged, chunks)
		forged[0].Content = "This is not a recording of the approved body at all."
		forged[0].ContentHash = rag.ContentHash(forged[0].Content)

		if _, err := store.Reindex(ctx, rag.ReindexCommand{OrganizationID: ragIntegrationOrganization, NamespaceKind: rag.NamespaceDepartment, NamespaceID: forgeryNamespace, ChunkerID: rag.DefaultChunkerID, ChunkerVersion: rag.DefaultChunkerVersion, Chunks: forged}); err == nil {
			t.Fatal("expected Reindex to reject a chunk not derived from the approved body, got nil error")
		} else if !errors.Is(err, rag.ErrInvalidRequest) {
			t.Fatalf("Reindex err=%v, want rag.ErrInvalidRequest", err)
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

	// §21 P1: rag_guard_version_update (migration 000041) must reject any
	// direct SQL attempt to change a field that is not in the explicitly
	// permitted set (lifecycle, reviewer_role_id, reviewed_at, revision,
	// updated_at), while still allowing the repository's own normal
	// lifecycle transition (Store.Save) to pass.
	//
	// Each tampering attempt below also advances revision by exactly one,
	// sets a matching lifecycle (candidate -> approved), and inserts the
	// matching rag_knowledge_lifecycle_events row -- i.e. it satisfies
	// every invariant the *pre-000041* trigger already enforced. This
	// isolates the assertion to the field-immutability guard added in
	// 000041: without it, an attacker who also gets the revision/audit
	// bookkeeping right could otherwise smuggle a change to namespace_id,
	// source_reference, source_boundary, or sanitization_evidence_ref
	// through what looks like a legitimate lifecycle transition.
	t.Run("database rejects mutation of immutable knowledge version fields", func(t *testing.T) {
		tamperUpdate := func(t *testing.T, version rag.KnowledgeVersion, setClause string) error {
			t.Helper()
			created, _, err := store.CreateCandidate(ctx, rag.CreateCandidateCommand{Version: version, IdempotencyKey: "idem-" + version.ID})
			if err != nil {
				t.Fatal(err)
			}
			reviewedAt := created.UpdatedAt.Add(time.Second)
			if _, err := platform.Pool().Exec(ctx, `INSERT INTO rag_knowledge_lifecycle_events (organization_id,document_id,version_id,from_lifecycle,to_lifecycle,actor_role_id,reason,revision,created_at) VALUES ($1,$2,$3,'candidate','approved',$4,'tamper probe',2,$5)`,
				ragIntegrationOrganization, created.DocumentID, created.ID, ragIntegrationReviewer, reviewedAt); err != nil {
				t.Fatal(err)
			}
			stmt := fmt.Sprintf(`UPDATE rag_knowledge_versions SET lifecycle='approved',reviewer_role_id=$3,reviewed_at=$4,revision=2,updated_at=$4,%s WHERE organization_id=$1 AND version_id=$2`, setClause)
			_, execErr := platform.Pool().Exec(ctx, stmt, ragIntegrationOrganization, created.ID, ragIntegrationReviewer, reviewedAt)
			return execErr
		}

		for name, tampering := range map[string]struct {
			id        string
			setClause string
		}{
			"namespace_id":     {id: "know-immutable-namespace-id", setClause: `namespace_id='tampered-namespace'`},
			"source_reference": {id: "know-immutable-source-reference", setClause: `source_reference='tampered:source'`},
			"source_boundary":  {id: "know-immutable-source-boundary", setClause: `source_boundary='tampered-boundary'`},
		} {
			t.Run(name, func(t *testing.T) {
				version := proposeVersion(t, domain, clock, now.Add(45*time.Second), tampering.id)
				if err := tamperUpdate(t, version, tampering.setClause); err == nil {
					t.Fatalf("direct SQL mutation of %s succeeded (with an otherwise-legitimate revision bump and matching audit event), expected the trigger to reject it", name)
				}
			})
		}

		// sanitization_evidence_ref gets its own case with data_class =
		// sanitized and a non-null starting value: the pre-existing CHECK
		// constraint (rag_knowledge_versions_check3) only ties
		// sanitization_evidence_ref's NULL-ness to data_class, not its
		// value to its old value, so swapping one non-null evidence ref for
		// another non-null one does not trip that CHECK. Only the 000041
		// trigger guard catches this specific tampering.
		t.Run("sanitization_evidence_ref", func(t *testing.T) {
			clock.now = now.Add(45 * time.Second)
			version, err := domain.Propose(rag.ProposeCommand{
				ID: "know-immutable-sanitization-ref", DocumentID: "know-immutable-sanitization-ref", OrganizationID: ragIntegrationOrganization,
				NamespaceKind: rag.NamespaceDepartment, NamespaceID: ragIntegrationNamespace,
				Version: 1, Title: "Sanitized knowledge for immutability probe", Body: "Cuerpo sanitizado para la prueba de inmutabilidad del trigger.",
				SourceKind: rag.SourceHuman, SourceReference: "investigacion:report:sanitized-immutable-probe", ProposedBy: ragIntegrationProposer,
				EvidenceRefs: []rag.EvidenceRef{{Reference: "evidence:sanitized-immutable-probe", Digest: "aaa"}},
				Admission:    rag.AdmissionAttestation{DataClass: rag.DataSanitized, AttestedBy: ragIntegrationProposer, SourceBoundary: "organization", EvidenceRef: "admission:sanitized-immutable-probe", SanitizationEvidenceRef: "sanitization:original", AttestedAt: clock.now.Add(-time.Second)},
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := tamperUpdate(t, version, `sanitization_evidence_ref='sanitization:tampered'`); err == nil {
				t.Fatal("direct SQL mutation of sanitization_evidence_ref (non-null to non-null, data_class=sanitized) succeeded, expected the trigger to reject it")
			}
		})

		// A normal lifecycle transition performed the way the real
		// repository performs it (Store.Save, touching only
		// lifecycle/reviewer_role_id/reviewed_at/revision/updated_at) must
		// still succeed after the tightened trigger.
		version := proposeVersion(t, domain, clock, now.Add(46*time.Second), "know-immutable-fields-control")
		created, _, err := store.CreateCandidate(ctx, rag.CreateCandidateCommand{Version: version, IdempotencyKey: "idem-immutable-fields-control"})
		if err != nil {
			t.Fatal(err)
		}
		clock.now = created.UpdatedAt.Add(time.Second)
		approved, err := domain.Review(created, rag.ReviewApprove, ragIntegrationReviewer)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.Save(ctx, rag.SaveCommand{Version: approved, ExpectedRevision: 1, ActorID: ragIntegrationReviewer, Reason: "normal transition still permitted"}); err != nil {
			t.Fatalf("legitimate lifecycle transition via repository was rejected: %v", err)
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

	t.Run("R30: bge-m3 chunk embeddings live in their own vector(1024) table, never mixed with gemini's vector(768) table", func(t *testing.T) {
		const namespace = "ingenieria_ia_bge_m3"
		version := proposeVersionInNamespace(t, domain, clock, now.Add(46*time.Second), "know-bge-m3", namespace)
		created, _, err := store.CreateCandidate(ctx, rag.CreateCandidateCommand{Version: version, IdempotencyKey: "idem-bge-m3"})
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
		generation, err := store.Reindex(ctx, rag.ReindexCommand{OrganizationID: ragIntegrationOrganization, NamespaceKind: rag.NamespaceDepartment, NamespaceID: namespace, ChunkerID: rag.DefaultChunkerID, ChunkerVersion: rag.DefaultChunkerVersion, Chunks: chunks})
		if err != nil {
			t.Fatal(err)
		}
		results, err := store.Query(ctx, rag.QueryCommand{OrganizationID: ragIntegrationOrganization, NamespaceKind: rag.NamespaceDepartment, NamespaceID: namespace, QueryText: "egress", Limit: 10})
		if err != nil || len(results) == 0 {
			t.Fatalf("lexical query before any embedding exists: results=%+v err=%v", results, err)
		}
		chunkID := results[0].Chunk.ID

		vector := make([]float32, 1024)
		vector[0] = 1
		if err := store.InsertBGEM3ChunkEmbedding(ctx, rag.BGEM3ChunkEmbedding{
			OrganizationID: ragIntegrationOrganization, ChunkID: chunkID, EmbeddingModelID: "bge-m3-local",
			ModelRevision: "bge-m3-2024-06", ArtifactSHA256: strings.Repeat("a", 64), TokenizerRevision: "bge-m3-tokenizer-2024-06",
			EmbeddingDimension: 1024, Normalization: "l2", Pooling: "cls", PromptTemplateVersion: "bge-m3-prompt-template.v1",
			InputHash: fmt.Sprintf("%064x", 3), Vector: vector, CreatedAt: clock.now,
		}); err != nil {
			t.Fatal(err)
		}

		nearest, err := store.NearestBGEM3Chunks(ctx, ragIntegrationOrganization, generation.ID, vector, 5)
		if err != nil {
			t.Fatal(err)
		}
		if len(nearest) != 1 || nearest[0].ChunkID != chunkID || nearest[0].Distance > 0.0001 {
			t.Fatalf("nearest=%+v want exactly chunkID=%s at ~0 distance", nearest, chunkID)
		}

		// The bge-m3 table is genuinely separate: a chunk with only a bge-m3
		// embedding must never surface through the gemini (vector(768)) path.
		geminiSideResults, err := store.NearestChunks(ctx, ragIntegrationOrganization, generation.ID, make([]float32, 768), 5)
		if err != nil {
			t.Fatal(err)
		}
		for _, hit := range geminiSideResults {
			if hit.ChunkID == chunkID {
				t.Fatalf("chunk %s leaked into the gemini vector(768) index via its bge-m3 embedding", chunkID)
			}
		}

		if _, err := platform.Pool().Exec(ctx, `UPDATE rag_chunk_embeddings_bge_m3 SET embedding_dimension=1024 WHERE organization_id=$1 AND chunk_id=$2`, ragIntegrationOrganization, chunkID); err == nil {
			t.Fatal("expected rag_chunk_embeddings_bge_m3 to reject UPDATE the same way canonical rag tables do")
		}
	})

	t.Run("R30.1-2: PendingChunkEmbeddings drives a resumable, idempotent backfill", func(t *testing.T) {
		const namespace = "ingenieria_ia_backfill"
		var chunks []rag.Chunk
		for i, id := range []string{"know-backfill-1", "know-backfill-2", "know-backfill-3"} {
			version := proposeVersionInNamespace(t, domain, clock, now.Add(time.Duration(60+i)*time.Second), id, namespace)
			created, _, err := store.CreateCandidate(ctx, rag.CreateCandidateCommand{Version: version, IdempotencyKey: "idem-" + id})
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
			docChunks, err := rag.ChunkBody(approved.ID, rag.DefaultChunkerID, rag.DefaultChunkerVersion, approved.Body)
			if err != nil {
				t.Fatal(err)
			}
			chunks = append(chunks, docChunks...)
		}
		if _, err := store.Reindex(ctx, rag.ReindexCommand{OrganizationID: ragIntegrationOrganization, NamespaceKind: rag.NamespaceDepartment, NamespaceID: namespace, ChunkerID: rag.DefaultChunkerID, ChunkerVersion: rag.DefaultChunkerVersion, Chunks: chunks}); err != nil {
			t.Fatal(err)
		}
		if len(chunks) != 3 {
			t.Fatalf("chunks=%d want 3 (one document per chunk)", len(chunks))
		}

		geminiIdentity := rag.EmbeddingIdentity{ModelID: "gemini-embedding-2", ModelVersion: "v1"}

		// A page smaller than the total pending set proves paging works —
		// the caller must be able to make progress in bounded steps.
		firstPage, err := store.PendingChunkEmbeddings(ctx, ragIntegrationOrganization, rag.NamespaceDepartment, namespace, geminiIdentity, 2)
		if err != nil {
			t.Fatal(err)
		}
		if len(firstPage) != 2 {
			t.Fatalf("first page=%d want 2", len(firstPage))
		}
		for i, chunk := range firstPage {
			vector := make([]float32, 768)
			vector[i] = 1
			if err := store.InsertChunkEmbedding(ctx, rag.ChunkEmbedding{
				OrganizationID: ragIntegrationOrganization, ChunkID: chunk.ID,
				EmbeddingModelID: "gemini-embedding-2", EmbeddingModelVersion: "v1", EmbeddingDimension: 768,
				PromptTemplateVersion: "prompt-template.v1", InputHash: fmt.Sprintf("%064x", 100+i), Vector: vector, CreatedAt: clock.now,
			}); err != nil {
				t.Fatal(err)
			}
		}

		// The chunks just embedded must never come back — this is the whole
		// resumability contract: a caller that crashed after this point and
		// restarted must only ever see genuinely remaining work.
		secondPage, err := store.PendingChunkEmbeddings(ctx, ragIntegrationOrganization, rag.NamespaceDepartment, namespace, geminiIdentity, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(secondPage) != 1 {
			t.Fatalf("second page=%d want 1 (the one chunk not yet embedded)", len(secondPage))
		}
		for _, embedded := range firstPage {
			for _, stillPending := range secondPage {
				if embedded.ID == stillPending.ID {
					t.Fatalf("chunk %s was already embedded but is still reported pending", embedded.ID)
				}
			}
		}
		if err := store.InsertChunkEmbedding(ctx, rag.ChunkEmbedding{
			OrganizationID: ragIntegrationOrganization, ChunkID: secondPage[0].ID,
			EmbeddingModelID: "gemini-embedding-2", EmbeddingModelVersion: "v1", EmbeddingDimension: 768,
			PromptTemplateVersion: "prompt-template.v1", InputHash: fmt.Sprintf("%064x", 199), Vector: make([]float32, 768), CreatedAt: clock.now,
		}); err != nil {
			t.Fatal(err)
		}

		// Nothing left pending under the gemini identity.
		done, err := store.PendingChunkEmbeddings(ctx, ragIntegrationOrganization, rag.NamespaceDepartment, namespace, geminiIdentity, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(done) != 0 {
			t.Fatalf("pending after full backfill=%+v want none", done)
		}

		// A different embedding identity (bge-m3, 1024-dim) is an entirely
		// separate vector space — none of these chunks have a bge-m3 row, so
		// every one of them must still be pending under that identity, even
		// though they are fully embedded under gemini's.
		bgeM3Identity := rag.EmbeddingIdentity{
			ModelID: "bge-m3-local", ModelRevision: "bge-m3-2024-06", ArtifactSHA256: strings.Repeat("e", 64),
			TokenizerRevision: "bge-m3-tokenizer-2024-06", Normalization: "l2", Pooling: "cls",
		}
		pendingBGEM3, err := store.PendingChunkEmbeddings(ctx, ragIntegrationOrganization, rag.NamespaceDepartment, namespace, bgeM3Identity, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(pendingBGEM3) != 3 {
			t.Fatalf("pending under bge-m3 identity=%d want 3 (gemini completion must not satisfy a different vector space)", len(pendingBGEM3))
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
		fused, err := store.Query(ctx, rag.QueryCommand{
			OrganizationID: ragIntegrationOrganization, NamespaceKind: rag.NamespaceDepartment, NamespaceID: hybridNamespace,
			QueryText: "overnight coolant pump", QueryVector: queryVector, Limit: 10,
			EmbeddingIdentity:              rag.EmbeddingIdentity{ModelID: "gemini-embedding-2", ModelVersion: "v1"},
			EmbeddingPromptTemplateVersion: "prompt-template.v1",
		})
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

		// R30: the same fused Query() entry point Manager.Query uses picks
		// the bge-m3 table instead of gemini's the moment the caller's
		// vector is 1024-dimensional — proving the profile switch works
		// through the real RRF path, not just the raw NearestBGEM3Chunks
		// helper already covered by the earlier bge-m3 subtest.
		bgeM3Vector := make([]float32, 1024)
		bgeM3Vector[7] = 1
		if err := store.InsertBGEM3ChunkEmbedding(ctx, rag.BGEM3ChunkEmbedding{
			OrganizationID: ragIntegrationOrganization, ChunkID: vectorChunkID, EmbeddingModelID: "bge-m3-local",
			ModelRevision: "bge-m3-2024-06", ArtifactSHA256: strings.Repeat("b", 64), TokenizerRevision: "bge-m3-tokenizer-2024-06",
			EmbeddingDimension: 1024, Normalization: "l2", Pooling: "cls", PromptTemplateVersion: "bge-m3-prompt-template.v1",
			InputHash: fmt.Sprintf("%064x", 10), Vector: bgeM3Vector, CreatedAt: clock.now,
		}); err != nil {
			t.Fatal(err)
		}
		fusedBGEM3, err := store.Query(ctx, rag.QueryCommand{
			OrganizationID: ragIntegrationOrganization, NamespaceKind: rag.NamespaceDepartment, NamespaceID: hybridNamespace,
			QueryText: "overnight coolant pump", QueryVector: bgeM3Vector, Limit: 10,
			EmbeddingIdentity: rag.EmbeddingIdentity{
				ModelID: "bge-m3-local", ModelRevision: "bge-m3-2024-06", ArtifactSHA256: strings.Repeat("b", 64),
				TokenizerRevision: "bge-m3-tokenizer-2024-06", Normalization: "l2", Pooling: "cls",
			},
			EmbeddingPromptTemplateVersion: "bge-m3-prompt-template.v1",
		})
		if err != nil {
			t.Fatal(err)
		}
		bgeM3Documents := make(map[string]bool, len(fusedBGEM3))
		for _, result := range fusedBGEM3 {
			bgeM3Documents[result.DocumentID] = true
		}
		if !bgeM3Documents["know-hybrid-lexical"] || !bgeM3Documents["know-hybrid-vector"] {
			t.Fatalf("bge-m3 fused query documents=%v want both know-hybrid-lexical and know-hybrid-vector", bgeM3Documents)
		}

		// A query vector of any other dimension is a hard error, never a
		// silent guess at which table to use.
		if _, err := store.Query(ctx, rag.QueryCommand{OrganizationID: ragIntegrationOrganization, NamespaceKind: rag.NamespaceDepartment, NamespaceID: hybridNamespace, QueryText: "overnight coolant pump", QueryVector: make([]float32, 5), Limit: 10}); err == nil {
			t.Fatal("expected an unexpected-dimension query vector to be rejected")
		}
	})

	// G4-004: reports/database-audit.md found zero namespace_kind=own rows
	// and every real production row under a single department namespace --
	// isolation across namespaces was architecturally present but never
	// empirically observed. This proves it at the deepest layer that could
	// leak: Store.Query resolves the active generation strictly by
	// (namespace_kind, namespace_id) before ever touching a chunk row, so a
	// query scoped to one namespace can only ever see that namespace's own
	// active generation -- not "filtered results from a mixed set", a
	// disjoint generation lookup. Both a positive and negative query prove
	// this: querying the OTHER namespace's own text must still find its
	// own chunk, ruling out a trivially-broken setup passing by accident.
	t.Run("Query never surfaces a chunk indexed under a different namespace", func(t *testing.T) {
		const namespaceA = "ingenieria_ia_isolation_a"
		const namespaceB = "ingenieria_ia_isolation_b"
		clock.now = now.Add(90 * time.Second)

		versionA := proposeVersionInNamespace(t, domain, clock, clock.now, "know-isolation-a", namespaceA)
		versionA.Title = "isolation fixture a"
		versionA.Body = "quarantine namespace alpha contains this unique passphrase zephyrwatch"
		versionA = mustReconanonicalize(t, versionA)
		createdA, _, err := store.CreateCandidate(ctx, rag.CreateCandidateCommand{Version: versionA, IdempotencyKey: "idem-isolation-a"})
		if err != nil {
			t.Fatal(err)
		}
		clock.now = clock.now.Add(time.Second)
		approvedA, err := domain.Review(createdA, rag.ReviewApprove, ragIntegrationReviewer)
		if err != nil {
			t.Fatal(err)
		}
		approvedA, err = store.Save(ctx, rag.SaveCommand{Version: approvedA, ExpectedRevision: 1, ActorID: ragIntegrationReviewer, Reason: "ok"})
		if err != nil {
			t.Fatal(err)
		}
		chunksA, err := rag.ChunkBody(approvedA.ID, rag.DefaultChunkerID, rag.DefaultChunkerVersion, approvedA.Body)
		if err != nil || len(chunksA) == 0 {
			t.Fatalf("chunks=%v err=%v", chunksA, err)
		}
		if _, err := store.Reindex(ctx, rag.ReindexCommand{OrganizationID: ragIntegrationOrganization, NamespaceKind: rag.NamespaceDepartment, NamespaceID: namespaceA, ChunkerID: rag.DefaultChunkerID, ChunkerVersion: rag.DefaultChunkerVersion, Chunks: chunksA}); err != nil {
			t.Fatal(err)
		}

		// namespaceB is deliberately left with NO active generation at all --
		// the realistic shape of the finding's own observation (namespace_kind=
		// own had zero rows in production), and the strictest possible test of
		// the isolation boundary: there is nothing namespaceB's own lookup
		// could accidentally match except namespaceA's generation, if the
		// filter were broken.
		crossResults, err := store.Query(ctx, rag.QueryCommand{OrganizationID: ragIntegrationOrganization, NamespaceKind: rag.NamespaceDepartment, NamespaceID: namespaceB, QueryText: "zephyrwatch", Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if len(crossResults) != 0 {
			t.Fatalf("querying namespace B for namespace A's unique text leaked %d result(s): %+v", len(crossResults), crossResults)
		}

		ownResults, err := store.Query(ctx, rag.QueryCommand{OrganizationID: ragIntegrationOrganization, NamespaceKind: rag.NamespaceDepartment, NamespaceID: namespaceA, QueryText: "zephyrwatch", Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if len(ownResults) == 0 {
			t.Fatal("querying namespace A for its own unique text found nothing -- test setup is broken, the zero-result cross-namespace check above proves nothing")
		}
	})
}

// TestQueryResultSurfacesMediaProvenance is the RAG-QUERY-PROVENANCE-001
// regression: internal/rag/postgres/hybrid_query.go's SELECT list used to
// omit media_source_ref/media_mime_type/source_page_number/media_sha256/
// media_parser/media_parser_version/text_extraction_status, so Store.Query
// never populated those seven fields on the returned rag.Chunk even though
// rag_knowledge_chunks stored them correctly (see PR #27's per-page
// provenance fix and migrations 000035/000036). This is a real, disjoint
// generation with exactly one media-backed chunk and one ordinary text
// chunk, run through the real Reindex + Query path against real Postgres
// -- never a reimplementation of the projection.
func TestQueryResultSurfacesMediaProvenance(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	platform := openRAGStore(t, ctx)
	t.Cleanup(platform.Close)
	runner, err := platformmigrations.New(platform.Pool(), rootmigrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Up(ctx); err != nil {
		t.Fatal(err)
	}
	resetRAGSchema(t, ctx, platform)
	t.Cleanup(func() { resetRAGSchema(t, context.Background(), platform) })
	syncRAGCanonical(t, ctx, platform)
	store, err := ragpostgres.New(platform, ragIntegrationOrganization)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	clock := &fixedClock{now: now}
	domain := rag.NewService(clock)

	version := proposeVersion(t, domain, clock, now, "know-provenance")
	created, _, err := store.CreateCandidate(ctx, rag.CreateCandidateCommand{Version: version, IdempotencyKey: "idem-provenance"})
	if err != nil {
		t.Fatal(err)
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

	// mediaChunk mirrors a real Ghostscript-rebuilt page (see PR #27 /
	// RAG-PDF-CHUNK-OVERFLOW-001): all seven provenance fields set
	// together, exactly the all-or-nothing shape migration 000036
	// enforces. A media-backed chunk's content is exempt from Reindex's
	// "derived from the approved body at these offsets" check (see
	// Store.Reindex), so it can carry the page's own extracted text.
	mediaContent := "contenido de la pagina extraida via ghostscript"
	mediaChunk := rag.Chunk{
		ID: approved.ID + "-1", VersionID: approved.ID, ChunkerID: "pdf-page-window", ChunkerVersion: "v2",
		Ordinal: 1, StartOffset: 0, EndOffset: len(mediaContent),
		Content: mediaContent, ContentHash: rag.ContentHash(mediaContent),
		MediaSourceRef: "raw/papers/audit-corpus-2026-08/testfixture/page-1.pdf", MediaMimeType: "application/pdf",
		SourcePageNumber: 1, MediaSHA256: "deadbeef0123456789deadbeef0123456789deadbeef0123456789deadbeef01",
		MediaParser: "ghostscript/pdfwrite", MediaParserVersion: "10.00.0+poppler-amplification-fallback",
		TextExtractionStatus: rag.TextExtractionOK,
	}
	// textChunk is an ordinary, non-media chunk in the same generation --
	// its seven provenance fields must come back as Go zero values, not a
	// Scan error, proving the NULL-handling side of the fix. Unlike
	// mediaChunk, a non-media chunk IS subject to the "derived from body"
	// check, so its content must be an exact substring of approved.Body at
	// its own offsets -- using the whole body keeps this trivially true
	// without hand-picking a substring.
	textChunk := rag.Chunk{
		ID: approved.ID + "-2", VersionID: approved.ID, ChunkerID: "pdf-page-window", ChunkerVersion: "v2",
		Ordinal: 2, StartOffset: 0, EndOffset: len(approved.Body),
		Content: approved.Body, ContentHash: rag.ContentHash(approved.Body),
	}
	generation, err := store.Reindex(ctx, rag.ReindexCommand{
		OrganizationID: ragIntegrationOrganization, NamespaceKind: rag.NamespaceDepartment, NamespaceID: ragIntegrationNamespace,
		ChunkerID: "pdf-page-window", ChunkerVersion: "v2", Chunks: []rag.Chunk{mediaChunk, textChunk},
	})
	if err != nil {
		t.Fatal(err)
	}
	if generation.Status != rag.GenerationActive {
		t.Fatalf("generation=%+v", generation)
	}

	// rrfSoloMatchScore is the exact score runHybridQuery's documented RRF
	// formula (score = 1/(rrfK+rank)) produces for a chunk that matches
	// exactly one channel (lexical) at rank 1, with no vector channel
	// configured (this test's Manager has no embedding deps) and no
	// digit-run token to trigger the exact-match channel. Asserting this
	// exact value -- not just "some positive score" -- is what proves
	// adding the seven provenance columns to the SELECT list left the
	// fusion arithmetic untouched, not merely still functional.
	const rrfSoloMatchScore = 1.0 / 61.0
	const scoreTolerance = 1e-9

	t.Run("media-backed result surfaces all seven provenance fields and ranks by the unchanged RRF formula", func(t *testing.T) {
		results, err := store.Query(ctx, rag.QueryCommand{OrganizationID: ragIntegrationOrganization, NamespaceKind: rag.NamespaceDepartment, NamespaceID: ragIntegrationNamespace, QueryText: "ghostscript", Limit: 10})
		if err != nil || len(results) != 1 {
			t.Fatalf("results=%+v err=%v", results, err)
		}
		got := results[0].Chunk
		want := mediaChunk
		if got.MediaSourceRef != want.MediaSourceRef || got.MediaMimeType != want.MediaMimeType ||
			got.SourcePageNumber != want.SourcePageNumber || got.MediaSHA256 != want.MediaSHA256 ||
			got.MediaParser != want.MediaParser || got.MediaParserVersion != want.MediaParserVersion ||
			got.TextExtractionStatus != want.TextExtractionStatus {
			t.Fatalf("provenance mismatch: got=%+v want=%+v", got, want)
		}
		if diff := results[0].Score - rrfSoloMatchScore; diff > scoreTolerance || diff < -scoreTolerance {
			t.Fatalf("score=%v want %v (RRF fusion must be unaffected by the provenance projection)", results[0].Score, rrfSoloMatchScore)
		}
	})

	t.Run("text-only result leaves provenance fields empty without a scan error and ranks by the unchanged RRF formula", func(t *testing.T) {
		results, err := store.Query(ctx, rag.QueryCommand{OrganizationID: ragIntegrationOrganization, NamespaceKind: rag.NamespaceDepartment, NamespaceID: ragIntegrationNamespace, QueryText: "egress", Limit: 10})
		if err != nil || len(results) != 1 {
			t.Fatalf("results=%+v err=%v", results, err)
		}
		got := results[0].Chunk
		if got.MediaSourceRef != "" || got.MediaMimeType != "" || got.SourcePageNumber != 0 ||
			got.MediaSHA256 != "" || got.MediaParser != "" || got.MediaParserVersion != "" || got.TextExtractionStatus != "" {
			t.Fatalf("expected zero-value provenance for a text-only chunk, got=%+v", got)
		}
		if diff := results[0].Score - rrfSoloMatchScore; diff > scoreTolerance || diff < -scoreTolerance {
			t.Fatalf("score=%v want %v (RRF fusion must be unaffected by the provenance projection)", results[0].Score, rrfSoloMatchScore)
		}
	})

	t.Run("a query matching both chunks fuses and orders them deterministically", func(t *testing.T) {
		// "de" appears once in mediaChunk's content and multiple times in
		// textChunk's (the full approved body) -- both still match the
		// same single lexical channel at their own best rank, so this
		// exercises the multi-row fused/GROUP BY path (untouched by this
		// fix) without depending on hand-tuned term frequencies.
		results, err := store.Query(ctx, rag.QueryCommand{OrganizationID: ragIntegrationOrganization, NamespaceKind: rag.NamespaceDepartment, NamespaceID: ragIntegrationNamespace, QueryText: "de", Limit: 10})
		if err != nil || len(results) != 2 {
			t.Fatalf("results=%+v err=%v", results, err)
		}
		var sawMedia, sawText bool
		for _, r := range results {
			if r.Chunk.IsMedia() {
				sawMedia = true
			} else {
				sawText = true
			}
		}
		if !sawMedia {
			t.Fatalf("mediaChunk missing from fused results: %+v", results)
		}
		if !sawText {
			t.Fatalf("textChunk missing from fused results: %+v", results)
		}
		if results[0].Score < results[1].Score {
			t.Fatalf("expected results sorted by score descending, got %v then %v", results[0].Score, results[1].Score)
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
	if err := testdbguard.RequireTestDatabase(ctx, url, store.Pool()); err != nil {
		store.Close()
		t.Fatalf("refusing to run against unverified database: %v", err)
	}
	return store
}

func resetRAGSchema(t *testing.T, ctx context.Context, store *platformpostgres.Store) {
	t.Helper()
	if err := testdbguard.RequireDestructive(ctx, os.Getenv("ORG_TEST_DATABASE_URL"), store.Pool()); err != nil {
		t.Fatalf("refusing destructive TRUNCATE: %v", err)
	}
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

// TestKnowledgeVersionSurvivesPostgresRoundtripWithNanosecondPrecisionInput
// is the RAG-INTEGRITY-001 regression: a caller that hands Propose a
// genuinely nanosecond-precision, non-UTC AttestedAt (exactly what
// time.Now() without truncation produces, and what a caller in a
// different timezone would produce) must still round-trip through real
// Postgres and re-validate cleanly on read. Before this fix, this exact
// shape of input produced a version whose ComputeCanonicalHash() at
// propose time never matched what got recomputed after a timestamptz
// round-trip truncated the stored value -- a valid write that became
// permanently unreadable (ErrSourceDrift on every subsequent Get/List).
func TestKnowledgeVersionSurvivesPostgresRoundtripWithNanosecondPrecisionInput(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	platform := openRAGStore(t, ctx)
	t.Cleanup(platform.Close)
	runner, err := platformmigrations.New(platform.Pool(), rootmigrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Up(ctx); err != nil {
		t.Fatal(err)
	}
	resetRAGSchema(t, ctx, platform)
	t.Cleanup(func() { resetRAGSchema(t, context.Background(), platform) })
	syncRAGCanonical(t, ctx, platform)
	store, err := ragpostgres.New(platform, ragIntegrationOrganization)
	if err != nil {
		t.Fatal(err)
	}

	loc := time.FixedZone("UTC-3", -3*60*60)
	nanoAttestedAt := time.Date(2026, 8, 14, 1, 0, 0, 123456789, loc)
	clock := &fixedClock{now: time.Now().UTC()}
	domain := rag.NewService(clock)
	version, err := domain.Propose(rag.ProposeCommand{
		ID: "know-nanosecond-roundtrip", DocumentID: "know-nanosecond-roundtrip", OrganizationID: ragIntegrationOrganization,
		NamespaceKind: rag.NamespaceDepartment, NamespaceID: ragIntegrationNamespace, Version: 1,
		Title: "Nanosecond roundtrip regression", Body: "Body content for the roundtrip regression test.",
		SourceKind: rag.SourceResearch, SourceReference: "investigacion:report:99", ProposedBy: ragIntegrationProposer,
		EvidenceRefs: []rag.EvidenceRef{{Reference: "evidence:nano", Digest: "aaa"}},
		Admission:    rag.AdmissionAttestation{DataClass: rag.DataOrganizational, AttestedBy: ragIntegrationProposer, SourceBoundary: "organization", EvidenceRef: "admission:nano", AttestedAt: nanoAttestedAt},
	})
	if err != nil {
		t.Fatalf("Propose with nanosecond-precision, non-UTC AttestedAt: %v", err)
	}
	if version.Admission.AttestedAt.Location() != time.UTC {
		t.Fatalf("Propose did not canonicalize AttestedAt to UTC: %v", version.Admission.AttestedAt)
	}
	if version.Admission.AttestedAt.Nanosecond()%1000 != 0 {
		t.Fatalf("Propose did not truncate AttestedAt to microsecond precision: %v", version.Admission.AttestedAt)
	}
	created, _, err := store.CreateCandidate(ctx, rag.CreateCandidateCommand{Version: version, IdempotencyKey: "idem-nano-roundtrip"})
	if err != nil {
		t.Fatalf("CreateCandidate: %v", err)
	}
	fetched, err := store.Get(ctx, ragIntegrationOrganization, created.ID)
	if err != nil {
		t.Fatalf("Get after round-trip failed (this is the RAG-INTEGRITY-001 symptom -- a valid write became unreadable): %v", err)
	}
	if fetched.CanonicalHash != created.CanonicalHash {
		t.Fatalf("canonical hash drifted across the round-trip: wrote %s, read %s", created.CanonicalHash, fetched.CanonicalHash)
	}
	if err := fetched.Validate(); err != nil {
		t.Fatalf("fetched version failed re-validation: %v", err)
	}

	// Same input JSON-equivalent command + same idempotency_key, replayed:
	// must produce the identical canonical_hash both times (the second
	// defect the same fix addresses -- omitted/re-derived attested_at
	// made retries nondeterministic).
	replay, err := domain.Propose(rag.ProposeCommand{
		ID: "know-nanosecond-roundtrip", DocumentID: "know-nanosecond-roundtrip", OrganizationID: ragIntegrationOrganization,
		NamespaceKind: rag.NamespaceDepartment, NamespaceID: ragIntegrationNamespace, Version: 1,
		Title: "Nanosecond roundtrip regression", Body: "Body content for the roundtrip regression test.",
		SourceKind: rag.SourceResearch, SourceReference: "investigacion:report:99", ProposedBy: ragIntegrationProposer,
		EvidenceRefs: []rag.EvidenceRef{{Reference: "evidence:nano", Digest: "aaa"}},
		Admission:    rag.AdmissionAttestation{DataClass: rag.DataOrganizational, AttestedBy: ragIntegrationProposer, SourceBoundary: "organization", EvidenceRef: "admission:nano", AttestedAt: nanoAttestedAt},
	})
	if err != nil {
		t.Fatalf("replay Propose: %v", err)
	}
	if replay.CanonicalHash != created.CanonicalHash {
		t.Fatalf("replay with identical input produced a different canonical_hash: first=%s replay=%s", created.CanonicalHash, replay.CanonicalHash)
	}
	replayed, reused, err := store.CreateCandidate(ctx, rag.CreateCandidateCommand{Version: replay, IdempotencyKey: "idem-nano-roundtrip"})
	if err != nil || !reused || replayed.ID != created.ID {
		t.Fatalf("idempotent replay: replayed=%+v reused=%v err=%v", replayed, reused, err)
	}
}
