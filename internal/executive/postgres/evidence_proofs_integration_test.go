//go:build integration

package postgres_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/executive"
	executivepostgres "github.com/Mireuz13/explorarte-organization/internal/executive/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
	taskpostgres "github.com/Mireuz13/explorarte-organization/internal/tasks/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/tasks/registryadapter"
	"github.com/Mireuz13/explorarte-organization/internal/testdbguard"
	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
)

const evidenceProofsIntegrationOrganization = "explorarte"

// TestEvidenceProofStorePostgreSQL proves DURABLE-EVIDENCE-PROOF-CONTRACT's
// storage layer end to end against real PostgreSQL: mint, read-back,
// idempotent re-mint, invalidation, and -- most load-bearing -- that the
// immutability trigger really rejects an arbitrary UPDATE/DELETE and really
// permits only the one sanctioned invalidation transition. A Go-level
// interface satisfied by a mock proves none of this; only the database
// itself enforcing the trigger does.
func TestEvidenceProofStorePostgreSQL(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	store := openEvidenceProofIntegrationStore(t, ctx)
	defer store.Close()
	resetEvidenceProofIntegrationSchema(t, ctx, store)
	syncEvidenceProofIntegrationCanonical(t, ctx, store)

	taskService := openEvidenceProofIntegrationTaskService(t, store)
	created, inserted, err := taskService.CreateTask(ctx, tasks.CreateRequest{
		AssignedRoleID: "empresa/ceo",
		IdempotencyKey: "evidence-proofs-integration-root",
		Title:          "Root task for evidence proof integration test",
		Instructions:   "n/a",
	}, "human", "eduardo")
	if err != nil || !inserted {
		t.Fatalf("create root task: inserted=%v err=%v", inserted, err)
	}

	proofStore, err := executivepostgres.NewEvidenceProofStore(store.Pool())
	if err != nil {
		t.Fatal(err)
	}

	const baseSHA = "c30328eda491241fccb81b8c83feb8a5b1e6cc35"
	const otherSHA = "eedc79f4560701d59c80375bf7f5e19b2a6a8438"
	proof := executive.EvidenceProof{
		OrganizationID:  evidenceProofsIntegrationOrganization,
		RootTaskID:      created.ID,
		Subject:         "validatePackage",
		Relation:        "definition",
		BaseSHA:         baseSHA,
		SourceReference: "repository://explorarte-organization@" + baseSHA + "/internal/coderunner/executor.go#L283-L331",
		ContentDigest:   "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}

	t.Run("mint and read back", func(t *testing.T) {
		if err := proofStore.MintProof(ctx, proof); err != nil {
			t.Fatalf("mint proof: %v", err)
		}
		valid, err := proofStore.ValidProofs(ctx, created.ID, baseSHA)
		if err != nil {
			t.Fatal(err)
		}
		got, ok := valid[executive.EvidenceSlot{Subject: proof.Subject, Relation: proof.Relation}]
		if !ok {
			t.Fatal("minted proof was not found in ValidProofs")
		}
		if got.SourceReference != proof.SourceReference || got.ContentDigest != proof.ContentDigest {
			t.Fatalf("read-back proof does not match what was minted: %+v", got)
		}
	})

	t.Run("re-minting the same slot is idempotent, not an error", func(t *testing.T) {
		if err := proofStore.MintProof(ctx, proof); err != nil {
			t.Fatalf("re-mint must be idempotent (ON CONFLICT DO NOTHING), got: %v", err)
		}
		valid, err := proofStore.ValidProofs(ctx, created.ID, baseSHA)
		if err != nil {
			t.Fatal(err)
		}
		if len(valid) != 1 {
			t.Fatalf("expected exactly one proof for this slot after a duplicate mint, got %d", len(valid))
		}
	})

	t.Run("a proof for a different base_sha is invisible to ValidProofs at this one", func(t *testing.T) {
		valid, err := proofStore.ValidProofs(ctx, created.ID, otherSHA)
		if err != nil {
			t.Fatal(err)
		}
		if len(valid) != 0 {
			t.Fatalf("expected no proofs at an unrelated base_sha, got %+v", valid)
		}
	})

	t.Run("the immutability trigger rejects an arbitrary UPDATE", func(t *testing.T) {
		_, err := store.Pool().Exec(ctx, `UPDATE evidence_proofs SET content_digest = repeat('b', 64) WHERE root_task_id = $1 AND subject = $2`,
			created.ID, proof.Subject)
		if err == nil {
			t.Fatal("expected the database itself to reject a mutation of content_digest, got no error")
		}
	})

	t.Run("the immutability trigger rejects DELETE", func(t *testing.T) {
		_, err := store.Pool().Exec(ctx, `DELETE FROM evidence_proofs WHERE root_task_id = $1 AND subject = $2`,
			created.ID, proof.Subject)
		if err == nil {
			t.Fatal("expected the database itself to reject a delete, got no error")
		}
	})

	t.Run("InvalidateProofs tombstones proofs at a stale base_sha and ValidProofs stops returning them", func(t *testing.T) {
		if err := proofStore.InvalidateProofs(ctx, created.ID, otherSHA); err != nil {
			t.Fatalf("invalidate proofs: %v", err)
		}
		valid, err := proofStore.ValidProofs(ctx, created.ID, baseSHA)
		if err != nil {
			t.Fatal(err)
		}
		if len(valid) != 0 {
			t.Fatalf("expected the proof to be invalidated (world moved to a different SHA), still saw %+v", valid)
		}
	})

	t.Run("the trigger rejects a second invalidation of an already-invalidated proof", func(t *testing.T) {
		_, err := store.Pool().Exec(ctx, `UPDATE evidence_proofs SET invalidated_at = NOW() WHERE root_task_id = $1 AND subject = $2`,
			created.ID, proof.Subject)
		if err == nil {
			t.Fatal("expected the database to reject invalidating an already-invalidated proof, got no error")
		}
	})
}

func openEvidenceProofIntegrationStore(t *testing.T, ctx context.Context) *platformpostgres.Store {
	t.Helper()
	url := os.Getenv("ORG_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("ORG_TEST_DATABASE_URL is required")
	}
	cfg := config.DatabaseConfig{URL: url, SSLMode: "disable", MaxConns: 8, MinConns: 0, MaxConnLifetime: time.Minute, MaxConnIdleTime: time.Minute, HealthCheckPeriod: time.Second, ConnectTimeout: 5 * time.Second, PingTimeout: 5 * time.Second, StatementTimeout: 30 * time.Second, LockTimeout: 5 * time.Second, AutoMigrate: true, MigrationTimeout: 45 * time.Second, MigrationRetry: time.Second}
	store, err := platformpostgres.Open(ctx, cfg, "evidence-proofs-integration")
	if err != nil {
		t.Fatal(err)
	}
	if err := testdbguard.RequireTestDatabase(ctx, url, store.Pool()); err != nil {
		store.Close()
		t.Fatalf("refusing to run against unverified database: %v", err)
	}
	runner, err := platformmigrations.New(store.Pool(), rootmigrations.Files)
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	if _, err = runner.Up(ctx); err != nil {
		store.Close()
		t.Fatal(err)
	}
	return store
}

func resetEvidenceProofIntegrationSchema(t *testing.T, ctx context.Context, store *platformpostgres.Store) {
	t.Helper()
	if err := testdbguard.RequireDestructive(ctx, os.Getenv("ORG_TEST_DATABASE_URL"), store.Pool()); err != nil {
		t.Fatalf("refusing destructive TRUNCATE: %v", err)
	}
	resetCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := store.Pool().Exec(resetCtx, `TRUNCATE organizations, organization_registry_revisions, audit_events,
		evidence_proofs, outbox_events, task_dead_letters, task_events, task_leases, task_attempts,
		task_evidence, task_requirements, task_dependencies, tasks,
		organization_reporting_lines, organization_registry_revision_documents,
		organization_roles, organizational_units RESTART IDENTITY CASCADE`); err != nil {
		t.Fatal(err)
	}
}

func syncEvidenceProofIntegrationCanonical(t *testing.T, ctx context.Context, store *platformpostgres.Store) {
	t.Helper()
	repository, err := registry.NewPostgresRepository(store)
	if err != nil {
		t.Fatal(err)
	}
	loader, err := registry.NewLoader("../../../docs/canonical")
	if err != nil {
		t.Fatal(err)
	}
	service, err := registry.NewService(loader, repository, evidenceProofsIntegrationOrganization, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := service.SynchronizeCanonical(ctx, true); err != nil || !result.Applied {
		t.Fatalf("sync canonical: result=%+v err=%v", result, err)
	}
}

func openEvidenceProofIntegrationTaskService(t *testing.T, store *platformpostgres.Store) *tasks.Service {
	t.Helper()
	repository, err := registry.NewPostgresRepository(store)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := registryadapter.New(repository)
	if err != nil {
		t.Fatal(err)
	}
	database, err := taskpostgres.New(store)
	if err != nil {
		t.Fatal(err)
	}
	service, err := tasks.NewService(database, catalog, tasks.Config{OrganizationID: evidenceProofsIntegrationOrganization, DefaultMaxAttempts: 5, DefaultLeaseDuration: time.Minute, MaxLeaseDuration: 15 * time.Minute, RetryPolicy: tasks.RetryPolicy{BaseDelay: time.Second, MaxDelay: time.Minute}, OutboxMaxAttempts: 3, OutboxClaimDuration: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	return service
}
