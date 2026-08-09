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
	"github.com/Mireuz13/explorarte-organization/internal/memory"
	memorypostgres "github.com/Mireuz13/explorarte-organization/internal/memory/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
)

const (
	memoryIntegrationOrganization = "explorarte"
	memoryIntegrationRole         = "ingenieria_ia/orquestador"
	memoryIntegrationReviewer     = "empresa/human"
)

type fixedClock struct{ now time.Time }

func (c *fixedClock) Now() time.Time { return c.now }

func TestOrganizationalMemoryPostgresRepository(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	platform := openMemoryStore(t, ctx)
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
	resetMemorySchema(t, ctx, platform)
	t.Cleanup(func() { resetMemorySchema(t, context.Background(), platform) })
	syncMemoryCanonical(t, ctx, platform)
	store, err := memorypostgres.New(platform, memoryIntegrationOrganization)
	if err != nil {
		t.Fatal(err)
	}
	var _ memory.Repository = store
	now := time.Now().UTC().Truncate(time.Microsecond)
	clock := &fixedClock{now: now}
	domain := memory.NewService(clock)
	t.Run("candidate round trip idempotency and evidence", func(t *testing.T) {
		entry := proposeEntry(t, domain, clock, now, "mem-roundtrip", memory.SourceOperational, memory.DataOrganizational, "")
		created, reused, err := store.CreateCandidate(ctx, memory.CreateCandidateCommand{Entry: entry, IdempotencyKey: "idem-roundtrip"})
		if err != nil {
			t.Fatal(err)
		}
		if reused || created.Status != memory.StatusCandidate {
			t.Fatalf("created=%+v reused=%v", created, reused)
		}
		loaded, err := store.Get(ctx, memoryIntegrationOrganization, entry.ID)
		if err != nil {
			t.Fatal(err)
		}
		if loaded.SourceKind != memory.SourceOperational || len(loaded.EvidenceRefs) != 2 || loaded.EvidenceRefs[0].Reference != "evidence:a" {
			t.Fatalf("loaded=%+v", loaded)
		}
		again, reused, err := store.CreateCandidate(ctx, memory.CreateCandidateCommand{Entry: entry, IdempotencyKey: "idem-roundtrip"})
		if err != nil || !reused || again.ID != entry.ID {
			t.Fatalf("again=%+v reused=%v err=%v", again, reused, err)
		}
		changed := entry
		changed.ID = "mem-other"
		changed.Correction = "different"
		if _, _, err := store.CreateCandidate(ctx, memory.CreateCandidateCommand{Entry: changed, IdempotencyKey: "idem-roundtrip"}); !errors.Is(err, memory.ErrConflict) {
			t.Fatalf("conflict=%v", err)
		}
	})
	t.Run("simulation provenance survives durable round trip", func(t *testing.T) {
		entry := proposeEntry(t, domain, clock, now.Add(5*time.Second), "mem-simulation", memory.SourceSimulation, memory.DataOrganizational, "")
		created, _, err := store.CreateCandidate(ctx, memory.CreateCandidateCommand{Entry: entry, IdempotencyKey: "idem-simulation"})
		if err != nil {
			t.Fatal(err)
		}
		if created.SourceKind != memory.SourceSimulation {
			t.Fatalf("created source=%s", created.SourceKind)
		}
		loaded, err := store.Get(ctx, memoryIntegrationOrganization, entry.ID)
		if err != nil {
			t.Fatal(err)
		}
		if loaded.SourceKind != memory.SourceSimulation {
			t.Fatalf("loaded source=%s", loaded.SourceKind)
		}
	})
	t.Run("review lifecycle durable and optimistic", func(t *testing.T) {
		entry := proposeEntry(t, domain, clock, now.Add(10*time.Second), "mem-lifecycle", memory.SourceOperational, memory.DataOrganizational, "")
		created, _, err := store.CreateCandidate(ctx, memory.CreateCandidateCommand{Entry: entry, IdempotencyKey: "idem-lifecycle"})
		if err != nil {
			t.Fatal(err)
		}
		clock.now = created.UpdatedAt.Add(time.Second)
		approved, err := domain.Review(created, memory.Review{Outcome: memory.ReviewApprove, ReviewerID: memoryIntegrationReviewer})
		if err != nil {
			t.Fatal(err)
		}
		approved, err = store.Save(ctx, memory.SaveCommand{Entry: approved, ExpectedRevision: 1, ActorID: memoryIntegrationReviewer, Reason: "integration approval"})
		if err != nil {
			t.Fatal(err)
		}
		var events int
		if err := platform.Pool().QueryRow(ctx, `SELECT count(*) FROM organizational_memory_state_events WHERE organization_id=$1 AND entry_key=$2`, memoryIntegrationOrganization, entry.ID).Scan(&events); err != nil {
			t.Fatal(err)
		}
		if events != 2 {
			t.Fatalf("events=%d", events)
		}
		clock.now = approved.UpdatedAt.Add(time.Second)
		deprecated, err := domain.Deprecate(approved)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.Save(ctx, memory.SaveCommand{Entry: deprecated, ExpectedRevision: 1, ActorID: memoryIntegrationReviewer, Reason: "stale"}); !errors.Is(err, memory.ErrRevisionConflict) {
			t.Fatalf("stale=%v", err)
		}
		deprecated, err = store.Save(ctx, memory.SaveCommand{Entry: deprecated, ExpectedRevision: 2, ActorID: memoryIntegrationReviewer, Reason: "superseded"})
		if err != nil {
			t.Fatal(err)
		}
		if deprecated.ReviewerID != approved.ReviewerID || deprecated.ReviewedAt == nil || !deprecated.ReviewedAt.Equal(*approved.ReviewedAt) {
			t.Fatal("review provenance changed")
		}
	})
	t.Run("database rejects forbidden data classes", func(t *testing.T) {
		for _, class := range []string{"clinical", "secret"} {
			_, err := platform.Pool().Exec(ctx, `INSERT INTO organizational_memory_versions (organization_id,entry_key,role_id,category,problem,correction,source_kind,source_run_id,canonical_hash,proposed_by_role_id,data_class,admission_attested_by,source_boundary,admission_evidence_ref,admission_attested_at,created_at) VALUES ($1,$2,$3,'x','p','c','operational',1,repeat('a',64),$3,$4,$3,'organization','admission',NOW(),NOW())`, memoryIntegrationOrganization, "raw-forbidden-"+class, memoryIntegrationRole, class)
			if err == nil {
				t.Fatalf("accepted %s", class)
			}
		}
	})
	t.Run("database requires audit event before lifecycle mutation", func(t *testing.T) {
		entry := proposeEntry(t, domain, clock, now.Add(20*time.Second), "mem-db-guard", memory.SourceOperational, memory.DataOrganizational, "")
		created, _, err := store.CreateCandidate(ctx, memory.CreateCandidateCommand{Entry: entry, IdempotencyKey: "idem-db-guard"})
		if err != nil {
			t.Fatal(err)
		}
		_, err = platform.Pool().Exec(ctx, `UPDATE organizational_memory_entries SET status='approved',reviewer_role_id=$3,reviewed_at=$4,revision=2,updated_at=$4 WHERE organization_id=$1 AND entry_key=$2`, memoryIntegrationOrganization, created.ID, memoryIntegrationReviewer, created.UpdatedAt.Add(time.Second))
		if err == nil {
			t.Fatal("direct update succeeded")
		}
	})
	t.Run("immutable rows cannot mutate", func(t *testing.T) {
		for name, statement := range map[string]string{"content": `UPDATE organizational_memory_versions SET correction='tampered' WHERE organization_id='explorarte' AND entry_key='mem-roundtrip'`, "evidence": `UPDATE organizational_memory_evidence_refs SET digest='tampered' WHERE organization_id='explorarte' AND entry_key='mem-roundtrip'`, "event": `UPDATE organizational_memory_state_events SET reason='tampered' WHERE organization_id='explorarte' AND entry_key='mem-roundtrip'`, "idempotency": `DELETE FROM organizational_memory_idempotency WHERE organization_id='explorarte' AND idempotency_key='idem-roundtrip'`, "lifecycle": `DELETE FROM organizational_memory_entries WHERE organization_id='explorarte' AND entry_key='mem-roundtrip'`} {
			t.Run(name, func(t *testing.T) {
				if _, err := platform.Pool().Exec(ctx, statement); err == nil {
					t.Fatalf("%s mutation succeeded", name)
				}
			})
		}
	})
	t.Run("sanitized requires evidence", func(t *testing.T) {
		clock.now = now.Add(30 * time.Second)
		command := memory.ProposeCommand{ID: "mem-sanitized", OrganizationID: memoryIntegrationOrganization, RoleID: memoryIntegrationRole, Category: "sanitized_learning", Problem: "sanitized source", Correction: "bounded correction", SourceKind: memory.SourceOperational, SourceRunID: 900, EvidenceRefs: []memory.EvidenceRef{{Reference: "evidence:sanitized", Digest: "digest"}}, ProposedBy: memoryIntegrationRole, Admission: memory.AdmissionAttestation{DataClass: memory.DataSanitized, AttestedBy: "cell-gateway/clinical", SourceBoundary: "cell_gateway", EvidenceRef: "classification:sanitized", AttestedAt: clock.now.Add(-time.Second)}}
		if _, err := domain.Propose(command); !errors.Is(err, memory.ErrInvalidAdmission) {
			t.Fatalf("error=%v", err)
		}
		command.Admission.SanitizationEvidenceRef = "sanitization:sanitized"
		entry, err := domain.Propose(command)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := store.CreateCandidate(ctx, memory.CreateCandidateCommand{Entry: entry, IdempotencyKey: "idem-sanitized"}); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("identifier_tokens exact-match channel never conflates different numbers", func(t *testing.T) {
		clock.now = now.Add(40 * time.Second)
		hyphenated, err := domain.Propose(memory.ProposeCommand{
			ID: "mem-identifier-hyphenated", OrganizationID: memoryIntegrationOrganization, RoleID: memoryIntegrationRole,
			Category: "incident_learning", Problem: "agent hit error-20 during dispatch", Correction: "retried with backoff",
			SourceKind: memory.SourceOperational, SourceRunID: 1001, EvidenceRefs: []memory.EvidenceRef{{Reference: "evidence:id20", Digest: "aaa"}},
			ProposedBy: memoryIntegrationRole, Admission: memory.AdmissionAttestation{DataClass: memory.DataOrganizational, AttestedBy: memoryIntegrationRole, SourceBoundary: "organization", EvidenceRef: "admission:mem-identifier-hyphenated", AttestedAt: clock.now.Add(-time.Second)},
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := store.CreateCandidate(ctx, memory.CreateCandidateCommand{Entry: hyphenated, IdempotencyKey: "idem-identifier-hyphenated"}); err != nil {
			t.Fatal(err)
		}

		clock.now = clock.now.Add(time.Second)
		larger, err := domain.Propose(memory.ProposeCommand{
			ID: "mem-identifier-larger", OrganizationID: memoryIntegrationOrganization, RoleID: memoryIntegrationRole,
			Category: "incident_learning", Problem: "agent hit error 2000 during dispatch", Correction: "escalated to on-call",
			SourceKind: memory.SourceOperational, SourceRunID: 1002, EvidenceRefs: []memory.EvidenceRef{{Reference: "evidence:id2000", Digest: "bbb"}},
			ProposedBy: memoryIntegrationRole, Admission: memory.AdmissionAttestation{DataClass: memory.DataOrganizational, AttestedBy: memoryIntegrationRole, SourceBoundary: "organization", EvidenceRef: "admission:mem-identifier-larger", AttestedAt: clock.now.Add(-time.Second)},
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := store.CreateCandidate(ctx, memory.CreateCandidateCommand{Entry: larger, IdempotencyKey: "idem-identifier-larger"}); err != nil {
			t.Fatal(err)
		}

		var tokens []string
		if err := platform.Pool().QueryRow(ctx, `SELECT identifier_tokens FROM organizational_memory_versions WHERE organization_id=$1 AND entry_key=$2`, memoryIntegrationOrganization, "mem-identifier-hyphenated").Scan(&tokens); err != nil {
			t.Fatal(err)
		}
		if len(tokens) != 1 || tokens[0] != "20" {
			t.Fatalf("hyphenated identifier_tokens=%v want [20]", tokens)
		}

		var matchedByExactID string
		err = platform.Pool().QueryRow(ctx, `SELECT entry_key FROM organizational_memory_versions WHERE organization_id=$1 AND identifier_tokens && ARRAY['20']`, memoryIntegrationOrganization).Scan(&matchedByExactID)
		if err != nil {
			t.Fatal(err)
		}
		if matchedByExactID != "mem-identifier-hyphenated" {
			t.Fatalf("searching for identifier '20' matched %q, want mem-identifier-hyphenated (never mem-identifier-larger's '2000')", matchedByExactID)
		}
	})

	t.Run("Search fuses exact and vector channels, never crosses roles, and vectors never block lexical-free recency", func(t *testing.T) {
		clock.now = now.Add(50 * time.Second)
		exactEntry, err := domain.Propose(memory.ProposeCommand{
			ID: "mem-search-exact", OrganizationID: memoryIntegrationOrganization, RoleID: memoryIntegrationRole,
			Category: "incident_learning", Problem: "agent hit error-77 during dispatch", Correction: "retried with backoff",
			SourceKind: memory.SourceOperational, SourceRunID: 2001, EvidenceRefs: []memory.EvidenceRef{{Reference: "evidence:search-exact", Digest: "aaa"}},
			ProposedBy: memoryIntegrationRole, Admission: memory.AdmissionAttestation{DataClass: memory.DataOrganizational, AttestedBy: memoryIntegrationRole, SourceBoundary: "organization", EvidenceRef: "admission:mem-search-exact", AttestedAt: clock.now.Add(-time.Second)},
		})
		if err != nil {
			t.Fatal(err)
		}
		exactCreated, _, err := store.CreateCandidate(ctx, memory.CreateCandidateCommand{Entry: exactEntry, IdempotencyKey: "idem-search-exact"})
		if err != nil {
			t.Fatal(err)
		}
		clock.now = clock.now.Add(time.Second)
		exactApproved, err := domain.Review(exactCreated, memory.Review{Outcome: memory.ReviewApprove, ReviewerID: memoryIntegrationReviewer})
		if err != nil {
			t.Fatal(err)
		}
		exactApproved, err = store.Save(ctx, memory.SaveCommand{Entry: exactApproved, ExpectedRevision: 1, ActorID: memoryIntegrationReviewer, Reason: "ok"})
		if err != nil {
			t.Fatal(err)
		}

		clock.now = clock.now.Add(time.Second)
		vectorEntry := proposeEntry(t, domain, clock, clock.now, "mem-search-vector", memory.SourceOperational, memory.DataOrganizational, "")
		vectorCreated, _, err := store.CreateCandidate(ctx, memory.CreateCandidateCommand{Entry: vectorEntry, IdempotencyKey: "idem-search-vector"})
		if err != nil {
			t.Fatal(err)
		}
		clock.now = clock.now.Add(time.Second)
		vectorApproved, err := domain.Review(vectorCreated, memory.Review{Outcome: memory.ReviewApprove, ReviewerID: memoryIntegrationReviewer})
		if err != nil {
			t.Fatal(err)
		}
		vectorApproved, err = store.Save(ctx, memory.SaveCommand{Entry: vectorApproved, ExpectedRevision: 1, ActorID: memoryIntegrationReviewer, Reason: "ok"})
		if err != nil {
			t.Fatal(err)
		}

		queryVector := make([]float32, 768)
		queryVector[9] = 1
		if err := store.InsertEntryEmbedding(ctx, memory.EntryEmbedding{
			OrganizationID: memoryIntegrationOrganization, EntryID: vectorApproved.ID,
			EmbeddingModelID: "gemini-embedding-2", EmbeddingModelVersion: "v1", EmbeddingDimension: 768,
			PromptTemplateVersion: "prompt-template.v1", InputHash: fmt.Sprintf("%064x", 11), Vector: queryVector, CreatedAt: clock.now,
		}); err != nil {
			t.Fatal(err)
		}

		// Exact identifier channel alone (no vector) still finds the
		// error-77 entry — Search must not require the vector channel to
		// be useful.
		exactOnly, err := store.Search(ctx, memoryIntegrationOrganization, memoryIntegrationRole, "error 77", nil, 10)
		if err != nil {
			t.Fatal(err)
		}
		foundExact := false
		for _, entry := range exactOnly {
			if entry.ID == exactApproved.ID {
				foundExact = true
			}
		}
		if !foundExact {
			t.Fatalf("exact-only search=%+v missing %s", exactOnly, exactApproved.ID)
		}

		// With the query vector supplied, the vector-only entry surfaces
		// too, fused alongside the exact hit — RRF, not replacement.
		fused, err := store.Search(ctx, memoryIntegrationOrganization, memoryIntegrationRole, "error 77", queryVector, 10)
		if err != nil {
			t.Fatal(err)
		}
		ids := make(map[string]bool, len(fused))
		for _, entry := range fused {
			ids[entry.ID] = true
		}
		if !ids[exactApproved.ID] || !ids[vectorApproved.ID] {
			t.Fatalf("fused search ids=%v want both %s and %s", ids, exactApproved.ID, vectorApproved.ID)
		}

		// Search never crosses role boundaries at the store level either —
		// a role with no approved entries gets nothing back, never another
		// role's memory.
		crossRole, err := store.Search(ctx, memoryIntegrationOrganization, "empresa/ceo", "error 77", nil, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(crossRole) != 0 {
			t.Fatalf("cross-role search leaked entries: %+v", crossRole)
		}
	})

	t.Run("R30: bge-m3 entry embeddings live in their own vector(1024) table, never mixed with gemini's vector(768) table", func(t *testing.T) {
		clock.now = clock.now.Add(time.Second)
		entry := proposeEntry(t, domain, clock, clock.now, "mem-bge-m3", memory.SourceOperational, memory.DataOrganizational, "")
		created, _, err := store.CreateCandidate(ctx, memory.CreateCandidateCommand{Entry: entry, IdempotencyKey: "idem-bge-m3"})
		if err != nil {
			t.Fatal(err)
		}
		clock.now = clock.now.Add(time.Second)
		approved, err := domain.Review(created, memory.Review{Outcome: memory.ReviewApprove, ReviewerID: memoryIntegrationReviewer})
		if err != nil {
			t.Fatal(err)
		}
		approved, err = store.Save(ctx, memory.SaveCommand{Entry: approved, ExpectedRevision: 1, ActorID: memoryIntegrationReviewer, Reason: "ok"})
		if err != nil {
			t.Fatal(err)
		}

		vector := make([]float32, 1024)
		vector[0] = 1
		if err := store.InsertBGEM3EntryEmbedding(ctx, memory.BGEM3EntryEmbedding{
			OrganizationID: memoryIntegrationOrganization, EntryID: approved.ID, EmbeddingModelID: "bge-m3-local",
			ModelRevision: "bge-m3-2024-06", ArtifactSHA256: strings.Repeat("a", 64), TokenizerRevision: "bge-m3-tokenizer-2024-06",
			EmbeddingDimension: 1024, Normalization: "l2", Pooling: "cls", PromptTemplateVersion: "bge-m3-prompt-template.v1",
			InputHash: fmt.Sprintf("%064x", 12), Vector: vector, CreatedAt: clock.now,
		}); err != nil {
			t.Fatal(err)
		}

		nearest, err := store.NearestBGEM3Entries(ctx, memoryIntegrationOrganization, memoryIntegrationRole, vector, 5)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, hit := range nearest {
			if hit.EntryID == approved.ID {
				found = true
				if hit.Distance > 0.0001 {
					t.Fatalf("distance=%v want ~0 for an exact self-match", hit.Distance)
				}
			}
		}
		if !found {
			t.Fatalf("nearest=%+v missing %s", nearest, approved.ID)
		}

		// The bge-m3 table is genuinely separate: an entry with only a
		// bge-m3 embedding must never surface through the gemini path.
		geminiSideResults, err := store.NearestEntries(ctx, memoryIntegrationOrganization, memoryIntegrationRole, make([]float32, 768), 10)
		if err != nil {
			t.Fatal(err)
		}
		for _, hit := range geminiSideResults {
			if hit.EntryID == approved.ID {
				t.Fatalf("entry %s leaked into the gemini vector(768) index via its bge-m3 embedding", approved.ID)
			}
		}

		if _, err := platform.Pool().Exec(ctx, `UPDATE organizational_memory_embeddings_bge_m3 SET embedding_dimension=1024 WHERE organization_id=$1 AND entry_key=$2`, memoryIntegrationOrganization, approved.ID); err == nil {
			t.Fatal("expected organizational_memory_embeddings_bge_m3 to reject UPDATE the same way canonical memory tables do")
		}
	})
}

func proposeEntry(t *testing.T, domain *memory.Service, clock *fixedClock, now time.Time, id string, kind memory.SourceKind, class memory.DataClass, sanitizationRef string) memory.Entry {
	t.Helper()
	clock.now = now
	entry, err := domain.Propose(memory.ProposeCommand{ID: id, OrganizationID: memoryIntegrationOrganization, RoleID: memoryIntegrationRole, Category: "incident_learning", Problem: "A verified failure occurred.", Correction: "Apply the verified correction.", SourceKind: kind, SourceRunID: 42, EvidenceRefs: []memory.EvidenceRef{{Reference: "evidence:b", Digest: "bbb"}, {Reference: "evidence:a", Digest: "aaa"}}, ProposedBy: memoryIntegrationRole, Admission: memory.AdmissionAttestation{DataClass: class, AttestedBy: memoryIntegrationRole, SourceBoundary: "organization", EvidenceRef: "admission:" + id, SanitizationEvidenceRef: sanitizationRef, AttestedAt: now.Add(-time.Second)}})
	if err != nil {
		t.Fatal(err)
	}
	return entry
}
func openMemoryStore(t *testing.T, ctx context.Context) *platformpostgres.Store {
	t.Helper()
	url := os.Getenv("ORG_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("ORG_TEST_DATABASE_URL is required")
	}
	cfg := config.DatabaseConfig{URL: url, SSLMode: "disable", MaxConns: 30, MinConns: 0, MaxConnLifetime: time.Minute, MaxConnIdleTime: time.Minute, HealthCheckPeriod: time.Second, ConnectTimeout: 5 * time.Second, PingTimeout: 5 * time.Second, StatementTimeout: 30 * time.Second, LockTimeout: 5 * time.Second, AutoMigrate: true, MigrationTimeout: 45 * time.Second, MigrationRetry: time.Second}
	store, err := platformpostgres.Open(ctx, cfg, "memory-integration")
	if err != nil {
		t.Fatal(err)
	}
	return store
}
func resetMemorySchema(t *testing.T, ctx context.Context, store *platformpostgres.Store) {
	t.Helper()
	resetCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := store.Pool().Exec(resetCtx, `TRUNCATE organizations, organization_registry_revisions, audit_events RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("reset: %v", err)
	}
}
func syncMemoryCanonical(t *testing.T, ctx context.Context, store *platformpostgres.Store) *registry.Revision {
	t.Helper()
	repo, err := registry.NewPostgresRepository(store)
	if err != nil {
		t.Fatal(err)
	}
	loader, err := registry.NewLoader(filepath.Join("..", "..", "..", "docs", "canonical"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := registry.NewService(loader, repo, memoryIntegrationOrganization, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	res, err := service.SynchronizeCanonical(ctx, true)
	if err != nil || !res.Applied {
		t.Fatalf("sync=%+v err=%v", res, err)
	}
	revision, err := repo.GetCurrentRevision(ctx, memoryIntegrationOrganization)
	if err != nil || revision == nil {
		t.Fatalf("revision=%+v err=%v", revision, err)
	}
	return revision
}
