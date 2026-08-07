//go:build integration

package postgres_test

import (
	"context"
	"errors"
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
	if result.Current != 17 {
		t.Fatalf("current migration=%d want=17", result.Current)
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
		version := proposeVersion(t, domain, clock, now.Add(15*time.Second), "know-generations")
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
		first, err := store.Reindex(ctx, rag.ReindexCommand{OrganizationID: ragIntegrationOrganization, NamespaceKind: rag.NamespaceOwn, NamespaceID: ragIntegrationProposer, ChunkerID: rag.DefaultChunkerID, ChunkerVersion: rag.DefaultChunkerVersion, Chunks: chunks})
		if err != nil {
			t.Fatal(err)
		}
		second, err := store.Reindex(ctx, rag.ReindexCommand{OrganizationID: ragIntegrationOrganization, NamespaceKind: rag.NamespaceOwn, NamespaceID: ragIntegrationProposer, ChunkerID: rag.DefaultChunkerID, ChunkerVersion: rag.DefaultChunkerVersion, Chunks: chunks})
		if err != nil {
			t.Fatal(err)
		}
		if second.Generation != first.Generation+1 {
			t.Fatalf("generation did not advance: first=%d second=%d", first.Generation, second.Generation)
		}
		var activeCount int
		if err := platform.Pool().QueryRow(ctx, `SELECT count(*) FROM rag_index_generations WHERE organization_id=$1 AND namespace_kind='own' AND namespace_id=$2 AND status='active'`, ragIntegrationOrganization, ragIntegrationProposer).Scan(&activeCount); err != nil {
			t.Fatal(err)
		}
		if activeCount != 1 {
			t.Fatalf("expected exactly one active generation, found %d", activeCount)
		}
		var supersededCount int
		if err := platform.Pool().QueryRow(ctx, `SELECT count(*) FROM rag_index_generations WHERE organization_id=$1 AND namespace_kind='own' AND namespace_id=$2 AND status='superseded'`, ragIntegrationOrganization, ragIntegrationProposer).Scan(&supersededCount); err != nil {
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

	t.Run("database rejects clinical and secret data classes", func(t *testing.T) {
		for _, class := range []string{"clinical", "secret"} {
			_, err := platform.Pool().Exec(ctx, `INSERT INTO rag_knowledge_versions (organization_id,version_id,document_id,namespace_kind,namespace_id,version,title,body,source_kind,source_reference,proposed_by_role_id,data_class,admission_attested_by,source_boundary,admission_evidence_ref,admission_attested_at,content_hash,canonical_hash,lifecycle,revision,created_at,updated_at) VALUES ($1,$2,'know-db-guard','department','ingenieria_ia',99,'t','b','research','ref',$3,$4,$3,'organization','ref',NOW(),repeat('a',64),repeat('b',64),'candidate',1,NOW(),NOW())`,
				ragIntegrationOrganization, "raw-forbidden-"+class, ragIntegrationProposer, class)
			if err == nil {
				t.Fatalf("accepted %s", class)
			}
		}
	})
}

func proposeVersion(t *testing.T, domain *rag.Service, clock *fixedClock, now time.Time, id string) rag.KnowledgeVersion {
	t.Helper()
	clock.now = now
	version, err := domain.Propose(rag.ProposeCommand{
		ID: id, DocumentID: id, OrganizationID: ragIntegrationOrganization, NamespaceKind: rag.NamespaceDepartment, NamespaceID: ragIntegrationNamespace,
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
	if _, err := store.Pool().Exec(resetCtx, `TRUNCATE organizations, organization_registry_revisions, audit_events RESTART IDENTITY CASCADE`); err != nil {
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
