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
	if result.Current != 15 {
		t.Fatalf("current migration=%d, want 15", result.Current)
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

	t.Run("candidate round trip, idempotency, and evidence", func(t *testing.T) {
		entry := proposeEntry(t, domain, now, "mem-roundtrip", memoryIntegrationRole, memory.DataOrganizational, "")
		created, reused, err := store.CreateCandidate(ctx, memory.CreateCandidateCommand{Entry: entry, IdempotencyKey: "idem-roundtrip"})
		if err != nil {
			t.Fatal(err)
		}
		if reused || created.Status != memory.StatusCandidate || created.Revision != 1 {
			t.Fatalf("created=%+v reused=%v", created, reused)
		}
		loaded, err := store.Get(ctx, memoryIntegrationOrganization, entry.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(loaded.EvidenceRefs) != 2 || loaded.EvidenceRefs[0].Reference != "evidence:a" || loaded.Admission.EvidenceRef != "admission:mem-roundtrip" {
			t.Fatalf("loaded candidate drifted: %+v", loaded)
		}
		again, reused, err := store.CreateCandidate(ctx, memory.CreateCandidateCommand{Entry: entry, IdempotencyKey: "idem-roundtrip"})
		if err != nil || !reused || again.ID != entry.ID {
			t.Fatalf("idempotent create=%+v reused=%v err=%v", again, reused, err)
		}
		changed := entry
		changed.Correction = "different immutable correction"
		changed.ID = "mem-roundtrip-other"
		if _, _, err := store.CreateCandidate(ctx, memory.CreateCandidateCommand{Entry: changed, IdempotencyKey: "idem-roundtrip"}); !errors.Is(err, memory.ErrConflict) {
			t.Fatalf("same idempotency key with different content error=%v", err)
		}
	})

	t.Run("review lifecycle is durable and optimistic", func(t *testing.T) {
		entry := proposeEntry(t, domain, now.Add(10*time.Second), "mem-lifecycle", memoryIntegrationRole, memory.DataOrganizational, "")
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
		if approved.Status != memory.StatusApproved || approved.Revision != 2 || approved.ReviewerID != memoryIntegrationReviewer {
			t.Fatalf("approved=%+v", approved)
		}
		var events int
		if err := platform.Pool().QueryRow(ctx, `SELECT count(*) FROM organizational_memory_state_events WHERE organization_id=$1 AND entry_key=$2`, memoryIntegrationOrganization, entry.ID).Scan(&events); err != nil {
			t.Fatal(err)
		}
		if events != 2 {
			t.Fatalf("state events=%d, want 2", events)
		}
		clock.now = approved.UpdatedAt.Add(time.Second)
		deprecated, err := domain.Deprecate(approved)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.Save(ctx, memory.SaveCommand{Entry: deprecated, ExpectedRevision: 1, ActorID: memoryIntegrationReviewer, Reason: "stale revision"}); !errors.Is(err, memory.ErrRevisionConflict) {
			t.Fatalf("stale revision error=%v", err)
		}
		deprecated, err = store.Save(ctx, memory.SaveCommand{Entry: deprecated, ExpectedRevision: 2, ActorID: memoryIntegrationReviewer, Reason: "superseded by newer guidance"})
		if err != nil {
			t.Fatal(err)
		}
		if deprecated.ReviewerID != approved.ReviewerID || deprecated.ReviewedAt == nil || !deprecated.ReviewedAt.Equal(*approved.ReviewedAt) {
			t.Fatalf("review provenance changed during deprecation: before=%+v after=%+v", approved, deprecated)
		}
	})

	t.Run("approved list excludes candidate and scopes role", func(t *testing.T) {
		approvedEntries, err := store.ListApproved(ctx, memory.ApprovedFilter{OrganizationID: memoryIntegrationOrganization, RoleID: memoryIntegrationRole, Limit: 20})
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range approvedEntries {
			if entry.Status != memory.StatusApproved || entry.RoleID != memoryIntegrationRole {
				t.Fatalf("approved list leaked entry %+v", entry)
			}
		}
	})

	t.Run("database rejects forbidden clinical and secret classes", func(t *testing.T) {
		for _, class := range []string{"clinical", "secret"} {
			_, err := platform.Pool().Exec(ctx, `
INSERT INTO organizational_memory_versions (
 organization_id, entry_key, role_id, category, problem, correction, source_run_id,
 canonical_hash, proposed_by_role_id, data_class, admission_attested_by, source_boundary,
 admission_evidence_ref, admission_attested_at, created_at
) VALUES ($1,$2,$3,'x','p','c',1,repeat('a',64),$3,$4,$3,'organization','admission',NOW(),NOW())`,
				memoryIntegrationOrganization, "raw-forbidden-"+class, memoryIntegrationRole, class)
			if err == nil {
				t.Fatalf("database accepted forbidden data class %s", class)
			}
		}
	})

	t.Run("database requires audit event before lifecycle mutation", func(t *testing.T) {
		entry := proposeEntry(t, domain, now.Add(20*time.Second), "mem-db-guard", memoryIntegrationRole, memory.DataOrganizational, "")
		created, _, err := store.CreateCandidate(ctx, memory.CreateCandidateCommand{Entry: entry, IdempotencyKey: "idem-db-guard"})
		if err != nil {
			t.Fatal(err)
		}
		_, err = platform.Pool().Exec(ctx, `
UPDATE organizational_memory_entries
SET status='approved', reviewer_role_id=$3, reviewed_at=$4, revision=2, updated_at=$4
WHERE organization_id=$1 AND entry_key=$2`, memoryIntegrationOrganization, created.ID, memoryIntegrationReviewer, created.UpdatedAt.Add(time.Second))
		if err == nil {
			t.Fatal("direct lifecycle update without audit event unexpectedly succeeded")
		}
	})

	t.Run("content, evidence, events, idempotency, and lifecycle deletion are immutable", func(t *testing.T) {
		for name, statement := range map[string]string{
			"content":     `UPDATE organizational_memory_versions SET correction='tampered' WHERE organization_id='explorarte' AND entry_key='mem-roundtrip'`,
			"evidence":    `UPDATE organizational_memory_evidence_refs SET digest='tampered' WHERE organization_id='explorarte' AND entry_key='mem-roundtrip'`,
			"event":       `UPDATE organizational_memory_state_events SET reason='tampered' WHERE organization_id='explorarte' AND entry_key='mem-roundtrip'`,
			"idempotency": `DELETE FROM organizational_memory_idempotency WHERE organization_id='explorarte' AND idempotency_key='idem-roundtrip'`,
			"lifecycle":   `DELETE FROM organizational_memory_entries WHERE organization_id='explorarte' AND entry_key='mem-roundtrip'`,
		} {
			t.Run(name, func(t *testing.T) {
				if _, err := platform.Pool().Exec(ctx, statement); err == nil {
					t.Fatalf("immutable %s mutation unexpectedly succeeded", name)
				}
			})
		}
	})

	t.Run("sanitized entries require sanitization evidence", func(t *testing.T) {
		command := memory.ProposeCommand{
			ID: "mem-sanitized", OrganizationID: memoryIntegrationOrganization, RoleID: memoryIntegrationRole,
			Category: "sanitized_learning", Problem: "sanitized source", Correction: "bounded correction", SourceRunID: 900,
			EvidenceRefs: []memory.EvidenceRef{{Reference: "evidence:sanitized", Digest: "digest"}}, ProposedBy: memoryIntegrationRole,
			Admission: memory.AdmissionAttestation{DataClass: memory.DataSanitized, AttestedBy: "cell-gateway/clinical", SourceBoundary: "cell_gateway", EvidenceRef: "classification:sanitized", AttestedAt: now},
		}
		if _, err := domain.Propose(command); !errors.Is(err, memory.ErrInvalidAdmission) {
			t.Fatalf("sanitized entry without sanitization evidence error=%v", err)
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
}

func proposeEntry(t *testing.T, domain *memory.Service, now time.Time, id, role string, class memory.DataClass, sanitizationRef string) memory.Entry {
	t.Helper()
	clock, ok := domainClock(domain)
	if ok {
		clock.now = now
	}
	entry, err := domain.Propose(memory.ProposeCommand{
		ID: id, OrganizationID: memoryIntegrationOrganization, RoleID: role,
		Category: "incident_learning", Problem: "A verified failure occurred.", Correction: "Apply the verified correction.",
		SourceRunID: 42,
		EvidenceRefs: []memory.EvidenceRef{{Reference: "evidence:b", Digest: "bbb"}, {Reference: "evidence:a", Digest: "aaa"}},
		ProposedBy: role,
		Admission: memory.AdmissionAttestation{
			DataClass: class, AttestedBy: role, SourceBoundary: "organization",
			EvidenceRef: "admission:" + id, SanitizationEvidenceRef: sanitizationRef, AttestedAt: now.Add(-time.Second),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return entry
}

// domainClock is intentionally narrow: all tests construct the service with a
// *fixedClock and need deterministic transition timestamps.
func domainClock(domain *memory.Service) (*fixedClock, bool) {
	// The pure Service deliberately does not expose its clock. Integration
	// tests therefore create a fresh service whenever they need a new proposal
	// timestamp; this helper remains false and documents that no reflection or
	// unsafe access is used.
	return nil, false
}

func openMemoryStore(t *testing.T, ctx context.Context) *platformpostgres.Store {
	t.Helper()
	url := os.Getenv("ORG_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("ORG_TEST_DATABASE_URL is required")
	}
	cfg := config.DatabaseConfig{
		URL: url, SSLMode: "disable", MaxConns: 30, MinConns: 0,
		MaxConnLifetime: time.Minute, MaxConnIdleTime: time.Minute,
		HealthCheckPeriod: time.Second, ConnectTimeout: 5 * time.Second,
		PingTimeout: 5 * time.Second, StatementTimeout: 30 * time.Second,
		LockTimeout: 5 * time.Second, AutoMigrate: true,
		MigrationTimeout: 45 * time.Second, MigrationRetry: time.Second,
	}
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
	if _, err := store.Pool().Exec(resetCtx, `
TRUNCATE organizations, organization_registry_revisions, audit_events
RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("reset memory schema: %v", err)
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
