//go:build integration

package postgres_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/cellworker"
	cellworkerpostgres "github.com/Mireuz13/explorarte-organization/internal/cellworker/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
)

const cellworkerIntegrationOrganization = "explorarte"

func TestCellWorkerPostgresWorkSource(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	platform := openCellWorkerStore(t, ctx)
	// Registered before the cleanup below, so LIFO order runs the reset
	// first and closes the pool last.
	t.Cleanup(platform.Close)
	runner, err := platformmigrations.New(platform.Pool(), rootmigrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = runner.Up(ctx); err != nil {
		t.Fatal(err)
	}
	resetCellWorkerSchema(t, ctx, platform)
	// This suite inserts model_providers/model_profile_versions fixtures to
	// satisfy model_invocations' FKs — tables the shared model registry sync
	// (exercised later by the CLI smoke test in the same "all" run) also
	// reads. Leave no trace regardless of run order or pass/fail.
	t.Cleanup(func() { resetCellWorkerSchema(t, context.Background(), platform) })
	revision := syncCellWorkerCanonical(t, ctx, platform)

	store, err := cellworkerpostgres.New(platform, cellworkerIntegrationOrganization)
	if err != nil {
		t.Fatal(err)
	}
	var _ cellworker.WorkSource = store

	activePrincipal := insertCellWorkerPrincipal(t, ctx, platform, revision.ID, "active", "worker-01/active")
	disabledPrincipal := insertCellWorkerPrincipal(t, ctx, platform, revision.ID, "disabled", "worker-01/disabled")

	requested := insertCellWorkerInvocation(t, ctx, platform, revision.ID, activePrincipal, "requested", "eligible-requested")
	claimed := insertCellWorkerInvocation(t, ctx, platform, revision.ID, activePrincipal, "claimed", "eligible-claimed")
	insertCellWorkerInvocation(t, ctx, platform, revision.ID, activePrincipal, "succeeded", "terminal-not-eligible")
	insertCellWorkerInvocation(t, ctx, platform, revision.ID, disabledPrincipal, "requested", "disabled-principal-not-eligible")
	insertUnpinnedCellWorkerInvocation(t, ctx, platform, revision.ID, "unpinned-legacy-not-eligible")

	t.Run("returns only requested/claimed invocations pinned to the active principal", func(t *testing.T) {
		ids, listErr := store.ListEligible(ctx, "worker-01/active", 100)
		if listErr != nil {
			t.Fatal(listErr)
		}
		if len(ids) != 2 || ids[0] != requested || ids[1] != claimed {
			t.Fatalf("ids=%v, want [%d %d]", ids, requested, claimed)
		}
	})

	t.Run("never returns invocations pinned to a disabled principal", func(t *testing.T) {
		ids, listErr := store.ListEligible(ctx, "worker-01/disabled", 100)
		if listErr != nil {
			t.Fatal(listErr)
		}
		if len(ids) != 0 {
			t.Fatalf("ids=%v, want none", ids)
		}
	})

	t.Run("unknown principal key returns no rows, not an error", func(t *testing.T) {
		ids, listErr := store.ListEligible(ctx, "worker-01/does-not-exist", 100)
		if listErr != nil || len(ids) != 0 {
			t.Fatalf("ids=%v err=%v", ids, listErr)
		}
	})

	t.Run("limit is respected", func(t *testing.T) {
		ids, listErr := store.ListEligible(ctx, "worker-01/active", 1)
		if listErr != nil {
			t.Fatal(listErr)
		}
		if len(ids) != 1 || ids[0] != requested {
			t.Fatalf("ids=%v, want [%d]", ids, requested)
		}
	})

	t.Run("rejects blank principal key and non-positive limit", func(t *testing.T) {
		if _, listErr := store.ListEligible(ctx, "", 10); listErr == nil {
			t.Fatal("expected error for blank principal key")
		}
		if _, listErr := store.ListEligible(ctx, "worker-01/active", 0); listErr == nil {
			t.Fatal("expected error for non-positive limit")
		}
	})
}

func openCellWorkerStore(t *testing.T, ctx context.Context) *platformpostgres.Store {
	t.Helper()
	url := os.Getenv("ORG_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("ORG_TEST_DATABASE_URL is required")
	}
	cfg := config.DatabaseConfig{URL: url, SSLMode: "disable", MaxConns: 30, MinConns: 0, MaxConnLifetime: time.Minute, MaxConnIdleTime: time.Minute, HealthCheckPeriod: time.Second, ConnectTimeout: 5 * time.Second, PingTimeout: 5 * time.Second, StatementTimeout: 30 * time.Second, LockTimeout: 5 * time.Second, AutoMigrate: true, MigrationTimeout: 45 * time.Second, MigrationRetry: time.Second}
	store, err := platformpostgres.Open(ctx, cfg, "cellworker-integration")
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func resetCellWorkerSchema(t *testing.T, ctx context.Context, store *platformpostgres.Store) {
	t.Helper()
	_, err := store.Pool().Exec(ctx, `TRUNCATE model_invocations,model_dispatcher_assignments,model_execution_principals,model_provider_outcomes,model_provider_requests,model_egress_evaluations,model_egress_revision_bindings,model_egress_rules,model_egress_policy_versions,role_model_bindings,model_capability_snapshots,model_profile_versions,model_profiles,model_providers,context_segments,context_snapshots,authorization_uses,authorization_decisions,authorization_requests,staging_events,staging_reviews,staging_promotions,staging_checks,staging_workspace_artifacts,staging_artifacts,staging_workspaces,outbox_events,task_dead_letters,task_events,task_leases,task_attempts,task_evidence,task_requirements,task_dependencies,tasks,organization_reporting_lines,organization_registry_revision_documents,organization_roles,organizational_units,organizations,organization_registry_revisions,audit_events RESTART IDENTITY CASCADE`)
	if err != nil {
		t.Fatal(err)
	}
}

func syncCellWorkerCanonical(t *testing.T, ctx context.Context, store *platformpostgres.Store) *registry.Revision {
	t.Helper()
	repo, err := registry.NewPostgresRepository(store)
	if err != nil {
		t.Fatal(err)
	}
	loader, err := registry.NewLoader(filepath.Join("..", "..", "..", "docs", "canonical"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := registry.NewService(loader, repo, cellworkerIntegrationOrganization, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.SynchronizeCanonical(ctx, true)
	if err != nil || !result.Applied {
		t.Fatalf("sync=%+v err=%v", result, err)
	}
	revision, err := repo.GetCurrentRevision(ctx, cellworkerIntegrationOrganization)
	if err != nil || revision == nil {
		t.Fatalf("revision=%+v err=%v", revision, err)
	}
	return revision
}

func hash(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:])
}

// insertCellWorkerPrincipal registers an execution principal and returns its
// durable ID.
func insertCellWorkerPrincipal(t *testing.T, ctx context.Context, store *platformpostgres.Store, revisionID int64, status, principalKey string) int64 {
	t.Helper()
	now := time.Now().UTC()
	var disabledAt *time.Time
	if status == "disabled" {
		disabledAt = &now
	}
	var id int64
	if err := store.Pool().QueryRow(ctx, `
INSERT INTO model_execution_principals(organization_id,principal_key,dispatch_actor_role_id,principal_kind,status,idempotency_key,request_hash,registered_by_role_id,created_at,updated_at,disabled_at,disabled_by_role_id,disable_reason_code)
VALUES($1,$2,'ingenieria_ia/code-runner','local_process',$3,$4,$5,'empresa/human',$6,$6,$7,CASE WHEN $7::timestamptz IS NULL THEN NULL ELSE 'empresa/human' END,CASE WHEN $7::timestamptz IS NULL THEN NULL ELSE 'test_fixture' END)
RETURNING id`, cellworkerIntegrationOrganization, principalKey, status, "principal-"+principalKey, hash("principal-"+principalKey), now, disabledAt).Scan(&id); err != nil {
		t.Fatal(err)
	}
	_ = revisionID
	return id
}

// insertCellWorkerInvocation creates a fully-pinned model_invocations row
// (with a real dispatcher assignment behind it, matching Rama 10's paired
// dispatcher_assignment_id/execution_principal_id invariant) in the given
// status, and returns its ID.
func insertCellWorkerInvocation(t *testing.T, ctx context.Context, store *platformpostgres.Store, revisionID, principalID int64, status, suffix string) int64 {
	t.Helper()
	taskID, attemptID := insertCellWorkerTaskAttempt(t, ctx, store, revisionID, suffix)
	snapshotID := insertCellWorkerContextSnapshot(t, ctx, store, revisionID, taskID, suffix)
	profileVersionID := insertCellWorkerModelProfile(t, ctx, store, revisionID, suffix)
	now := time.Now().UTC()

	var assignmentID int64
	if err := store.Pool().QueryRow(ctx, `
INSERT INTO model_dispatcher_assignments(organization_id,organization_revision_id,task_id,attempt_id,subject_role_id,dispatch_actor_role_id,execution_principal_id,status,valid_from,valid_until,max_invocations,assignment_hash,idempotency_key,request_hash,created_by_role_id,created_at,updated_at)
VALUES($1,$2,$3,$4,'ingenieria_ia/code-runner','ingenieria_ia/code-runner',$5,'active',$6,$7,10,$8,$9,$10,'empresa/human',$6,$6)
RETURNING id`, cellworkerIntegrationOrganization, revisionID, taskID, attemptID, principalID, now, now.Add(time.Hour),
		hash("assignment-"+suffix), "assignment-"+suffix, hash("assignment-request-"+suffix)).Scan(&assignmentID); err != nil {
		t.Fatal(err)
	}

	var terminalAt *time.Time
	switch status {
	case "succeeded", "failed", "cancelled", "ambiguous":
		terminalAt = &now
	}

	var invocationID int64
	if err := store.Pool().QueryRow(ctx, `
INSERT INTO model_invocations(organization_id,organization_revision_id,task_id,attempt_id,dispatch_actor_role_id,subject_role_id,context_snapshot_id,purpose,model_profile_id,model_profile_version_id,provider_id,provider_model_id,output_mode,max_output_tokens,thinking_mode,idempotency_key,request_hash,status,deadline,dispatcher_assignment_id,execution_principal_id,created_at,updated_at,terminal_at)
VALUES($1,$2,$3,$4,'ingenieria_ia/code-runner','ingenieria_ia/code-runner',$5,'cellworker fixture','cellworker-fixture-'||$11,$6,'test.fake','deterministic-v1','text',256,'disabled',$7,$8,$9,$10,$12,$13,$14,$14,$15)
RETURNING id`, cellworkerIntegrationOrganization, revisionID, taskID, attemptID, snapshotID, profileVersionID,
		"invocation-"+suffix, hash("invocation-request-"+suffix), status, now.Add(time.Hour), suffix, assignmentID, principalID, now, terminalAt).Scan(&invocationID); err != nil {
		t.Fatal(err)
	}
	return invocationID
}

// insertUnpinnedCellWorkerInvocation creates a legacy-shaped invocation with
// no dispatcher assignment or execution principal at all, to prove the
// WorkSource never treats "unpinned" as "eligible for anyone".
func insertUnpinnedCellWorkerInvocation(t *testing.T, ctx context.Context, store *platformpostgres.Store, revisionID int64, suffix string) int64 {
	t.Helper()
	taskID, attemptID := insertCellWorkerTaskAttempt(t, ctx, store, revisionID, suffix)
	snapshotID := insertCellWorkerContextSnapshot(t, ctx, store, revisionID, taskID, suffix)
	profileVersionID := insertCellWorkerModelProfile(t, ctx, store, revisionID, suffix)
	now := time.Now().UTC()
	var invocationID int64
	if err := store.Pool().QueryRow(ctx, `
INSERT INTO model_invocations(organization_id,organization_revision_id,task_id,attempt_id,dispatch_actor_role_id,subject_role_id,context_snapshot_id,purpose,model_profile_id,model_profile_version_id,provider_id,provider_model_id,output_mode,max_output_tokens,thinking_mode,idempotency_key,request_hash,status,deadline,created_at,updated_at)
VALUES($1,$2,$3,$4,'ingenieria_ia/code-runner','ingenieria_ia/code-runner',$5,'cellworker fixture','cellworker-fixture-'||$9,$6,'test.fake','deterministic-v1','text',256,'disabled',$7,$8,'requested',$10,$11,$11)
RETURNING id`, cellworkerIntegrationOrganization, revisionID, taskID, attemptID, snapshotID, profileVersionID,
		"invocation-"+suffix, hash("invocation-request-"+suffix), suffix, now.Add(time.Hour), now).Scan(&invocationID); err != nil {
		t.Fatal(err)
	}
	return invocationID
}

func insertCellWorkerTaskAttempt(t *testing.T, ctx context.Context, store *platformpostgres.Store, revisionID int64, suffix string) (taskID, attemptID int64) {
	t.Helper()
	now := time.Now().UTC()
	if err := store.Pool().QueryRow(ctx, `
INSERT INTO tasks(organization_id,organization_revision_id,assigned_role_id,assigned_unit_id,idempotency_key,request_hash,title,instructions,acceptance_criteria,status,priority,available_at,max_attempts,attempt_count,version)
VALUES($1,$2,'ingenieria_ia/code-runner','ingenieria_ia',$3,$4,'Cellworker fixture','Evaluate cellworker eligibility.','[]','running',0,$5,3,1,1)
RETURNING id`, cellworkerIntegrationOrganization, revisionID, "cellworker-task-"+suffix, hash("task-"+suffix), now).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	if err := store.Pool().QueryRow(ctx, `
INSERT INTO task_attempts(task_id,ordinal,state,worker_id,leased_at,started_at,created_at,updated_at)
VALUES($1,1,'running','cellworker-integration',$2,$2,$2,$2)
RETURNING id`, taskID, now).Scan(&attemptID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool().Exec(ctx, `
INSERT INTO task_leases(task_id,attempt_id,token_hash,holder_id,status,issued_at,heartbeat_at,expires_at)
VALUES($1,$2,$3,'cellworker-integration','active',$4,$4,$5)`,
		taskID, attemptID, hash("lease-"+suffix), now, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	return taskID, attemptID
}

func insertCellWorkerContextSnapshot(t *testing.T, ctx context.Context, store *platformpostgres.Store, revisionID, taskID int64, suffix string) int64 {
	t.Helper()
	now := time.Now().UTC()
	var snapshotID int64
	if err := store.Pool().QueryRow(ctx, `
INSERT INTO context_snapshots(organization_id,organization_revision_id,actor_role_id,purpose,task_ref,idempotency_key,request_hash,precedence_hash,canonical_bundle_hash,rendered_hash,status,version,segment_count,included_segment_count,omitted_segment_count,total_bytes,created_at)
VALUES($1,$2,'ingenieria_ia/code-runner','cellworker fixture',$3,$4,$5,$6,$7,$8,'ready',1,0,0,0,0,$9)
RETURNING id`, cellworkerIntegrationOrganization, revisionID, fmt.Sprint(taskID), "cellworker-context-"+suffix,
		hash("context-"+suffix), hash("precedence-"+suffix), hash("bundle-"+suffix), hash("rendered-"+suffix), now).Scan(&snapshotID); err != nil {
		t.Fatal(err)
	}
	return snapshotID
}

func insertCellWorkerModelProfile(t *testing.T, ctx context.Context, store *platformpostgres.Store, revisionID int64, suffix string) int64 {
	t.Helper()
	if _, err := store.Pool().Exec(ctx, `
INSERT INTO model_providers(organization_id,id,transport,adapter_status,dispatch_enabled,direct_http_forbidden,canonical_hash,organization_revision_id)
VALUES($1,'test.fake','fake_adapter','available',TRUE,FALSE,$2,$3) ON CONFLICT DO NOTHING`,
		cellworkerIntegrationOrganization, hash("provider-fixture"), revisionID); err != nil {
		t.Fatal(err)
	}
	profileID := "cellworker-fixture-" + suffix
	if _, err := store.Pool().Exec(ctx, `
INSERT INTO model_profiles(organization_id,id,policy_id) VALUES($1,$2,$3)`,
		cellworkerIntegrationOrganization, profileID, "cellworker.fixture."+suffix); err != nil {
		t.Fatal(err)
	}
	var profileVersionID int64
	if err := store.Pool().QueryRow(ctx, `
INSERT INTO model_profile_versions(organization_id,profile_id,version_number,organization_revision_id,canonical_document_hash,version_hash,provider_id,provider_model_id,transport,adapter_status,dispatch_enabled)
VALUES($1,$2,1,$3,$4,$5,'test.fake','deterministic-v1','fake_adapter','available',TRUE)
RETURNING id`, cellworkerIntegrationOrganization, profileID, revisionID, hash("routing-"+suffix), hash("version-"+suffix)).Scan(&profileVersionID); err != nil {
		t.Fatal(err)
	}
	return profileVersionID
}
