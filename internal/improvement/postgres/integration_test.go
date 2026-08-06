//go:build integration

package postgres_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/evaluation"
	"github.com/Mireuz13/explorarte-organization/internal/improvement"
	improvementpostgres "github.com/Mireuz13/explorarte-organization/internal/improvement/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
)

const improvementIntegrationOrganization = "explorarte"

func TestImprovementPostgresCandidateStore(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	platform := openImprovementStore(t, ctx)
	t.Cleanup(platform.Close)
	runner, err := platformmigrations.New(platform.Pool(), rootmigrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	result, err := runner.Up(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Current != 13 {
		t.Fatalf("current migration=%d, want 13", result.Current)
	}
	resetImprovementSchema(t, ctx, platform)
	t.Cleanup(func() { resetImprovementSchema(t, context.Background(), platform) })
	syncImprovementCanonical(t, ctx, platform)

	store, err := improvementpostgres.New(platform, improvementIntegrationOrganization)
	if err != nil {
		t.Fatal(err)
	}
	var _ improvement.CandidateStore = store

	now := time.Now().UTC().Truncate(time.Microsecond)

	t.Run("propose, get, and save with optimistic concurrency", func(t *testing.T) {
		candidate := improvement.Candidate{
			ID:         "cand-main",
			Artifact:   improvement.ArtifactRef{ArtifactID: "art-1", ContentHash: digest("art-1"), SchemaVersion: "artifact/v1"},
			Lineage:    improvement.Lineage{},
			State:      improvement.StateProposed,
			ProposedAt: now,
			UpdatedAt:  now,
		}
		revision, err := store.ProposeCandidate(ctx, candidate, "integration/proposer")
		if err != nil {
			t.Fatal(err)
		}
		if revision != 1 {
			t.Fatalf("initial revision=%d, want 1", revision)
		}

		loaded, loadedRevision, err := store.GetCandidate(ctx, "cand-main")
		if err != nil {
			t.Fatal(err)
		}
		if loadedRevision != 1 || loaded.State != improvement.StateProposed || loaded.Artifact.ContentHash != candidate.Artifact.ContentHash {
			t.Fatalf("loaded=%+v revision=%d", loaded, loadedRevision)
		}

		loaded.State = improvement.StateValidated
		loaded.UpdatedAt = now.Add(time.Second)
		newRevision, err := store.SaveCandidate(ctx, loaded, loadedRevision)
		if err != nil {
			t.Fatal(err)
		}
		if newRevision != 2 {
			t.Fatalf("new revision=%d, want 2", newRevision)
		}

		reloaded, reloadedRevision, err := store.GetCandidate(ctx, "cand-main")
		if err != nil {
			t.Fatal(err)
		}
		if reloadedRevision != 2 || reloaded.State != improvement.StateValidated {
			t.Fatalf("reloaded=%+v revision=%d", reloaded, reloadedRevision)
		}

		// Saving again against the now-stale revision 1 must fail.
		stale := reloaded
		stale.State = improvement.StateEvaluating
		stale.UpdatedAt = now.Add(2 * time.Second)
		if _, err := store.SaveCandidate(ctx, stale, 1); !errors.Is(err, improvement.ErrRevisionConflict) {
			t.Fatalf("expected ErrRevisionConflict for a stale revision, got %v", err)
		}
	})

	t.Run("SaveCandidate on an unknown candidate returns ErrCandidateNotFound", func(t *testing.T) {
		unknown := improvement.Candidate{
			ID:         "cand-does-not-exist",
			Artifact:   improvement.ArtifactRef{ArtifactID: "art-x", ContentHash: digest("art-x"), SchemaVersion: "artifact/v1"},
			State:      improvement.StateValidated,
			ProposedAt: now,
			UpdatedAt:  now,
		}
		if _, err := store.SaveCandidate(ctx, unknown, 1); !errors.Is(err, improvement.ErrCandidateNotFound) {
			t.Fatalf("expected ErrCandidateNotFound, got %v", err)
		}
	})

	t.Run("database-level guard rejects an unlisted transition even via raw SQL", func(t *testing.T) {
		candidate := improvement.Candidate{
			ID:         "cand-guard",
			Artifact:   improvement.ArtifactRef{ArtifactID: "art-guard", ContentHash: digest("art-guard"), SchemaVersion: "artifact/v1"},
			State:      improvement.StateProposed,
			ProposedAt: now,
			UpdatedAt:  now,
		}
		if _, err := store.ProposeCandidate(ctx, candidate, "integration/proposer"); err != nil {
			t.Fatal(err)
		}
		// proposed -> active is not in the default-deny map at all, and this
		// bypasses the Go Service entirely: the database trigger must still
		// reject it.
		if _, err := platform.Pool().Exec(ctx, `
UPDATE improvement_candidates
SET state='active', updated_at=$3, revision=revision+1
WHERE organization_id=$1 AND candidate_key=$2`, improvementIntegrationOrganization, "cand-guard", now.Add(time.Second)); err == nil {
			t.Fatal("expected the database guard to reject proposed -> active")
		}
	})

	t.Run("rollback target round-trips through save and get", func(t *testing.T) {
		candidate := improvement.Candidate{
			ID:         "cand-rollback",
			Artifact:   improvement.ArtifactRef{ArtifactID: "art-rollback", ContentHash: digest("art-rollback"), SchemaVersion: "artifact/v1"},
			State:      improvement.StateProposed,
			ProposedAt: now,
			UpdatedAt:  now,
		}
		revision, err := store.ProposeCandidate(ctx, candidate, "integration/proposer")
		if err != nil {
			t.Fatal(err)
		}
		candidate.State = improvement.StateValidated
		candidate.UpdatedAt = now.Add(time.Second)
		revision, err = store.SaveCandidate(ctx, candidate, revision)
		if err != nil {
			t.Fatal(err)
		}
		candidate.State = improvement.StateEvaluating
		candidate.UpdatedAt = now.Add(2 * time.Second)
		revision, err = store.SaveCandidate(ctx, candidate, revision)
		if err != nil {
			t.Fatal(err)
		}
		candidate.State = improvement.StateApproved
		candidate.UpdatedAt = now.Add(3 * time.Second)
		revision, err = store.SaveCandidate(ctx, candidate, revision)
		if err != nil {
			t.Fatal(err)
		}
		candidate.State = improvement.StateCanary
		candidate.UpdatedAt = now.Add(4 * time.Second)
		revision, err = store.SaveCandidate(ctx, candidate, revision)
		if err != nil {
			t.Fatal(err)
		}
		candidate.State = improvement.StateRolledBack
		candidate.UpdatedAt = now.Add(5 * time.Second)
		candidate.RollbackTarget = &improvement.RollbackTarget{
			CandidateID: "cand-main", ArtifactHash: digest("art-1"), FromState: improvement.StateCanary,
		}
		if _, err := store.SaveCandidate(ctx, candidate, revision); err != nil {
			t.Fatal(err)
		}

		reloaded, _, err := store.GetCandidate(ctx, "cand-rollback")
		if err != nil {
			t.Fatal(err)
		}
		if reloaded.RollbackTarget == nil || reloaded.RollbackTarget.CandidateID != "cand-main" || reloaded.RollbackTarget.FromState != improvement.StateCanary {
			t.Fatalf("rollback target did not round-trip: %+v", reloaded.RollbackTarget)
		}
	})

	t.Run("promotion decisions are recorded and immutable", func(t *testing.T) {
		candidate := improvement.Candidate{
			ID:         "cand-promotion",
			Artifact:   improvement.ArtifactRef{ArtifactID: "art-promotion", ContentHash: digest("art-promotion"), SchemaVersion: "artifact/v1"},
			State:      improvement.StateProposed,
			ProposedAt: now,
			UpdatedAt:  now,
		}
		if _, err := store.ProposeCandidate(ctx, candidate, "integration/proposer"); err != nil {
			t.Fatal(err)
		}

		request := improvement.PromotionRequest{
			CandidateID: "cand-promotion", CandidateHash: digest("candidate-hash"),
			FromState: improvement.StateApproved, ToState: improvement.StateCanary, Kind: improvement.PromotionToCanary,
			Evaluation:  passingEvaluation(),
			RequestedAt: now, RequestedBy: "integration/requester",
		}
		decision := improvement.PromotionDecision{
			CandidateID: "cand-promotion", Kind: improvement.PromotionToCanary, Outcome: improvement.PromotionAuthorized,
			DecidedAt: now.Add(time.Second), DecidedBy: "integration/approver",
		}
		if err := store.RecordPromotionDecision(ctx, request, decision); err != nil {
			t.Fatal(err)
		}

		var count int
		if err := platform.Pool().QueryRow(ctx, `
SELECT count(*) FROM improvement_promotion_decisions d
JOIN improvement_candidates c ON c.id=d.candidate_id
WHERE c.candidate_key=$1 AND d.outcome='authorized'`, "cand-promotion").Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("promotion decisions recorded=%d, want 1", count)
		}

		if _, err := platform.Pool().Exec(ctx, `UPDATE improvement_promotion_decisions SET decided_by='tampered'`); err == nil {
			t.Fatal("expected the promotion decision audit row to be immutable")
		}
	})
}

func passingEvaluation() evaluation.SuiteComparisonResult {
	return evaluation.SuiteComparisonResult{
		SuiteID:        "suite-1",
		OverallVerdict: evaluation.VerdictPass,
		CaseResults: []evaluation.ComparisonResult{
			{CaseID: "case-1", BaselineVerdict: evaluation.VerdictPass, CandidateVerdict: evaluation.VerdictPass, OverallVerdict: evaluation.VerdictPass},
		},
	}
}

func openImprovementStore(t *testing.T, ctx context.Context) *platformpostgres.Store {
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
	store, err := platformpostgres.Open(ctx, cfg, "improvement-integration")
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func resetImprovementSchema(t *testing.T, ctx context.Context, store *platformpostgres.Store) {
	t.Helper()
	resetCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := store.Pool().Exec(resetCtx, `
TRUNCATE organizations, organization_registry_revisions, audit_events
RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("reset improvement schema: %v", err)
	}
}

func syncImprovementCanonical(t *testing.T, ctx context.Context, store *platformpostgres.Store) *registry.Revision {
	t.Helper()
	repo, err := registry.NewPostgresRepository(store)
	if err != nil {
		t.Fatal(err)
	}
	loader, err := registry.NewLoader(filepath.Join("..", "..", "..", "docs", "canonical"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := registry.NewService(loader, repo, improvementIntegrationOrganization, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	res, err := service.SynchronizeCanonical(ctx, true)
	if err != nil || !res.Applied {
		t.Fatalf("sync=%+v err=%v", res, err)
	}
	revision, err := repo.GetCurrentRevision(ctx, improvementIntegrationOrganization)
	if err != nil || revision == nil {
		t.Fatalf("revision=%+v err=%v", revision, err)
	}
	return revision
}

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
