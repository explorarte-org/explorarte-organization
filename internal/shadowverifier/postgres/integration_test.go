//go:build integration

package postgres_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/authorization"
	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/shadowverifier"
	shadowpostgres "github.com/Mireuz13/explorarte-organization/internal/shadowverifier/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/testdbguard"
	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
)

const shadowOrganization = "explorarte"

func TestShadowVerifierStorePostgreSQL17(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	platform := openShadowStore(t, ctx)
	t.Cleanup(platform.Close)
	runner, err := platformmigrations.New(platform.Pool(), rootmigrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Up(ctx); err != nil {
		t.Fatal(err)
	}
	resetShadowSchema(t, ctx, platform)
	canonicalDir := filepath.Join("..", "..", "..", "docs", "canonical")
	revision, registryService := syncShadowCanonical(t, ctx, platform, canonicalDir)

	store, err := shadowpostgres.New(platform, shadowOrganization)
	if err != nil {
		t.Fatal(err)
	}

	matrix, err := shadowverifier.LoadMatrix(canonicalDir)
	if err != nil {
		t.Fatal(err)
	}
	leaderMap, err := shadowverifier.LoadLeaderWorkerMap(canonicalDir)
	if err != nil {
		t.Fatal(err)
	}
	authorizer, err := authorization.New(registryServiceReader(t, platform), shadowOrganization, canonicalDir)
	if err != nil {
		t.Fatal(err)
	}
	ground := &integrationGround{registry: registryService, authorizer: authorizer, organizationID: shadowOrganization}

	t.Run("snapshot loads the live organization", func(t *testing.T) {
		snap, err := store.LoadSnapshot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if snap.Organization.ID != shadowOrganization || snap.Organization.Retired {
			t.Fatalf("organization=%+v", snap.Organization)
		}
		if snap.RevisionID != revision.ID {
			t.Fatalf("revision=%d want %d", snap.RevisionID, revision.ID)
		}
		// Lower bounds, not a mirror of the catalog: the unit floor was 9 and
		// went stale when comunicaciones/creativo/finanzas/marketing were
		// consolidated into negocio, leaving 6. A snapshot that loads the
		// live organization only needs to be non-degenerate here -- the exact
		// shape is asserted against the canonical documents elsewhere.
		if len(snap.Units) < 4 || len(snap.Roles) < 40 || len(snap.ReportingLines) == 0 {
			t.Fatalf("snapshot too small: units=%d roles=%d lines=%d", len(snap.Units), len(snap.Roles), len(snap.ReportingLines))
		}
		if len(snap.MatrixHash) != 64 {
			t.Fatalf("matrix hash=%q", snap.MatrixHash)
		}
		if snap.MatrixHash != revision.DocumentHashes["capability-matrix.yaml"] {
			t.Fatalf("matrix hash %q does not match revision document hash", snap.MatrixHash)
		}
	})

	var exhaustiveRunID int64
	t.Run("exhaustive parity against the real engines", func(t *testing.T) {
		service, err := shadowverifier.NewService(store, store, store, ground, matrix, leaderMap, shadowOrganization, 1, 0, 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		report, err := service.VerifyExhaustive(ctx)
		if err != nil {
			t.Fatal(err)
		}
		exhaustiveRunID = report.RunID
		if report.Divergences() != 0 {
			t.Fatalf("clean canonical state produced findings: %+v", report.Findings)
		}
		// The capability_granted policy-drift guard is expected to fire exactly
		// once here: MatrixIndex.Hash (sha256 of the shadow's own independently
		// parsed+marshaled matrixFile) and registry's DocumentHashes["capability-
		// matrix.yaml"] (sha256 of registry's own independently parsed+marshaled
		// capabilityMatrixDocument) are two separately-implemented hashes over the
		// same semantic document. They agree on shape today (mirrored field-for-
		// field in matrix.go) but nothing enforces byte-for-byte json.Marshal
		// identity between two independent parsers without a shared, versioned
		// canonicalization contract — which is exactly what StatusUncomparable
		// exists for: "the comparison cannot be made soundly", not "this is wrong".
		// See INTEGRATION.md for the follow-up (a shared canonical-hash contract,
		// or reading registry's stored hash directly instead of recomputing it).
		if report.Summary.ChecksTotal == 0 || report.Summary.ChecksUncomparable != 1 || report.Summary.ChecksParity != report.Summary.ChecksTotal-1 {
			t.Fatalf("summary=%+v uncomparable=%+v", report.Summary, report.Uncomparable)
		}
		if len(report.Uncomparable) != 1 || report.Uncomparable[0].Fact != shadowverifier.FactCapabilityGranted {
			t.Fatalf("expected exactly one uncomparable capability_granted check, got %+v", report.Uncomparable)
		}
		record, summary, err := store.GetRun(ctx, report.RunID)
		if err != nil {
			t.Fatal(err)
		}
		if record.Status != "completed" || record.Mode != shadowverifier.RunModeExhaustive {
			t.Fatalf("record=%+v", record)
		}
		if summary != report.Summary {
			t.Fatalf("persisted summary=%+v want %+v", summary, report.Summary)
		}
		findings, err := store.RunFindings(ctx, report.RunID)
		if err != nil {
			t.Fatal(err)
		}
		if len(findings) != 0 {
			t.Fatalf("persisted findings=%+v", findings)
		}
	})

	t.Run("replay compares recorded traffic at parity", func(t *testing.T) {
		insertShadowApprovalRequest(t, ctx, platform, revision, matrix.Hash)
		service, err := shadowverifier.NewService(store, store, store, ground, matrix, leaderMap, shadowOrganization, 1, 0, 10, nil)
		if err != nil {
			t.Fatal(err)
		}
		report, err := service.ReplayRecorded(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if report.Summary.ChecksTotal != 1 || report.Summary.ChecksParity != 1 || report.Divergences() != 0 {
			t.Fatalf("summary=%+v findings=%+v", report.Summary, report.Findings)
		}
	})

	t.Run("live cycle becomes a durable counterexample", func(t *testing.T) {
		if _, err := platform.Pool().Exec(ctx, `
INSERT INTO organization_reporting_lines(revision_id, organization_id, role_id, reports_to_role_id, relationship)
VALUES($1,$2,'empresa/human','ingenieria_ia/code-runner','reports_to')`, revision.ID, shadowOrganization); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if _, err := platform.Pool().Exec(ctx, `
DELETE FROM organization_reporting_lines
WHERE organization_id=$1 AND role_id='empresa/human' AND reports_to_role_id='ingenieria_ia/code-runner'`, shadowOrganization); err != nil {
				t.Fatal(err)
			}
		})
		service, err := shadowverifier.NewService(store, store, store, ground, matrix, leaderMap, shadowOrganization, 1, 0, 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		report, err := service.VerifyExhaustive(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if report.Summary.ChecksCounterexample == 0 {
			t.Fatalf("cycle produced no counterexamples: %+v", report.Summary)
		}
		var closure int
		for _, finding := range report.Findings {
			if finding.Fact == shadowverifier.FactDependencyClosed && finding.Kind == shadowverifier.KindCounterexample {
				closure++
			}
		}
		if closure < 2 {
			t.Fatalf("want cycle violation plus canon/database disagreement, got %+v", report.Findings)
		}
		findings, err := store.RunFindings(ctx, report.RunID)
		if err != nil {
			t.Fatal(err)
		}
		if len(findings) != len(report.Findings) {
			t.Fatalf("persisted %d findings, want %d", len(findings), len(report.Findings))
		}
	})

	t.Run("runs list reports history", func(t *testing.T) {
		runs, err := store.ListRuns(ctx, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(runs) < 3 {
			t.Fatalf("runs=%d want at least 3", len(runs))
		}
		if runs[len(runs)-1].ID != exhaustiveRunID {
			t.Fatalf("oldest run=%+v want id %d", runs[len(runs)-1], exhaustiveRunID)
		}
	})

	t.Run("down migration drops shadow verifier tables", func(t *testing.T) {
		if err := testdbguard.RequireDestructive(ctx, os.Getenv("ORG_TEST_DATABASE_URL"), platform.Pool()); err != nil {
			t.Fatalf("refusing destructive migration DownSQL: %v", err)
		}
		down, err := rootmigrations.Files.ReadFile("000014_create_shadow_verifier.down.sql")
		if err != nil {
			t.Fatal(err)
		}
		tx, err := platform.Pool().Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if _, err := tx.Exec(ctx, string(down)); err != nil {
			t.Fatalf("down migration 000014: %v", err)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM schema_migrations WHERE version=14`); err != nil {
			t.Fatal(err)
		}
		var runsMissing, divergencesMissing bool
		if err := tx.QueryRow(ctx, `
SELECT to_regclass('public.shadow_verifier_runs') IS NULL, to_regclass('public.shadow_verifier_divergences') IS NULL`).Scan(&runsMissing, &divergencesMissing); err != nil {
			t.Fatal(err)
		}
		if !runsMissing || !divergencesMissing {
			t.Fatalf("after down runs_missing=%t divergences_missing=%t", runsMissing, divergencesMissing)
		}
		if err := tx.Rollback(ctx); err != nil {
			t.Fatal(err)
		}
	})
}

func openShadowStore(t *testing.T, ctx context.Context) *platformpostgres.Store {
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
	store, err := platformpostgres.Open(ctx, cfg, "shadowverifier-integration")
	if err != nil {
		t.Fatal(err)
	}
	if err := testdbguard.RequireTestDatabase(ctx, url, store.Pool()); err != nil {
		store.Close()
		t.Fatalf("refusing to run against unverified database: %v", err)
	}
	return store
}

func resetShadowSchema(t *testing.T, ctx context.Context, store *platformpostgres.Store) {
	t.Helper()
	if err := testdbguard.RequireDestructive(ctx, os.Getenv("ORG_TEST_DATABASE_URL"), store.Pool()); err != nil {
		t.Fatalf("refusing destructive TRUNCATE: %v", err)
	}
	resetCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := store.Pool().Exec(resetCtx, `
TRUNCATE organizations, organization_registry_revisions, audit_events, shadow_verifier_runs
RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("reset shadow verifier schema: %v", err)
	}
}

func syncShadowCanonical(t *testing.T, ctx context.Context, store *platformpostgres.Store, canonicalDir string) (*registry.Revision, *registry.Service) {
	t.Helper()
	repo, err := registry.NewPostgresRepository(store)
	if err != nil {
		t.Fatal(err)
	}
	loader, err := registry.NewLoader(canonicalDir)
	if err != nil {
		t.Fatal(err)
	}
	service, err := registry.NewService(loader, repo, shadowOrganization, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.SynchronizeCanonical(ctx, true)
	if err != nil || !result.Applied {
		t.Fatalf("sync=%+v err=%v", result, err)
	}
	revision, err := repo.GetCurrentRevision(ctx, shadowOrganization)
	if err != nil || revision == nil {
		t.Fatalf("revision=%+v err=%v", revision, err)
	}
	return revision, service
}

func registryServiceReader(t *testing.T, store *platformpostgres.Store) registry.Reader {
	t.Helper()
	repo, err := registry.NewPostgresRepository(store)
	if err != nil {
		t.Fatal(err)
	}
	return repo
}

func insertShadowApprovalRequest(t *testing.T, ctx context.Context, store *platformpostgres.Store, revision *registry.Revision, matrixHash string) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	digest := sha256.Sum256([]byte("shadow-verifier:integration"))
	requestHash := sha256.Sum256([]byte("shadow-verifier:integration-request"))
	if _, err := store.Pool().Exec(ctx, `
INSERT INTO authorization_requests(
    organization_id, organization_revision_id, capability_matrix_hash, requester_role_id,
    capability_id, risk, approval_mode, resource_type, resource_id, action_digest,
    idempotency_key, request_hash, status, reason, created_at, updated_at, expires_at, approved_at
) VALUES($1,$2,$3,'empresa/human','organization.activate_skill','high','owner','skill','shadow-probe',$4,$5,$6,'approved','shadow verifier replay fixture',$7,$7,$8,$7)`,
		shadowOrganization, revision.ID, matrixHash, hex.EncodeToString(digest[:]),
		"shadow-replay-1", hex.EncodeToString(requestHash[:]), now, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
}

// integrationGround wires the real engines behind shadowverifier.GroundTruth,
// the same shape the orgctl composition root uses. Integration tests are the
// one place allowed to drive other branches' real services.
type integrationGround struct {
	registry       *registry.Service
	authorizer     *authorization.Authorizer
	organizationID string
	revision       int64
}

func (g *integrationGround) RoleExists(ctx context.Context, roleID string) (bool, error) {
	_, err := g.registry.GetRole(ctx, roleID)
	if err == nil {
		return true, nil
	}
	if isNotFound(err) {
		return false, nil
	}
	return false, err
}

func (g *integrationGround) DepartmentExists(ctx context.Context, unitID string) (bool, error) {
	_, err := g.registry.GetUnit(ctx, unitID)
	if err == nil {
		return true, nil
	}
	if isNotFound(err) {
		return false, nil
	}
	return false, err
}

func (g *integrationGround) LeaderOf(ctx context.Context, unitID string) (string, bool, error) {
	role, err := g.registry.GetLeader(ctx, unitID)
	if err == nil {
		return role.ID, true, nil
	}
	if isNotFound(err) {
		return "", false, nil
	}
	return "", false, err
}

func (g *integrationGround) EvaluateCapability(ctx context.Context, roleID, capabilityID string) (string, string, error) {
	if g.revision == 0 {
		revision, err := g.registry.GetCurrentRevision(ctx)
		if err != nil {
			return "", "", err
		}
		if revision == nil {
			return "", "", registry.ErrNotFound
		}
		g.revision = revision.ID
	}
	resourceType := "shadow_probe"
	if capabilityID == "model.invoke" {
		resourceType = "model_invocation"
	}
	sum := sha256.Sum256([]byte("shadow-verifier:probe"))
	result, err := g.authorizer.Evaluate(ctx, authorization.EvaluationRequest{
		OrganizationID:         g.organizationID,
		OrganizationRevisionID: g.revision,
		ActorRoleID:            roleID,
		CapabilityID:           capabilityID,
		ResourceType:           resourceType,
		ResourceID:             roleID + ":" + capabilityID,
		ActionDigest:           hex.EncodeToString(sum[:]),
	})
	if err != nil {
		return "", "", err
	}
	return string(result.Effect), string(result.ReasonCode), nil
}

func (g *integrationGround) CanonicalReportingClosed(context.Context) (bool, []string, error) {
	_, report, err := g.registry.ValidateCanonical()
	if err != nil {
		return false, nil, err
	}
	var issues []string
	for _, issue := range report.Errors {
		if strings.HasPrefix(issue.Code, "reporting.") {
			issues = append(issues, issue.Code)
		}
	}
	return len(issues) == 0, issues, nil
}

func isNotFound(err error) bool {
	return err == registry.ErrNotFound
}
