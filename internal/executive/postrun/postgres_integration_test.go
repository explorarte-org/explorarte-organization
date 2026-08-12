//go:build integration

package postrun_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/completion"
	completionpostgres "github.com/Mireuz13/explorarte-organization/internal/completion/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/contextengine/canonical"
	"github.com/Mireuz13/explorarte-organization/internal/decisiongraph"
	decisiongraphpostgres "github.com/Mireuz13/explorarte-organization/internal/decisiongraph/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/decisiongraphtrace"
	"github.com/Mireuz13/explorarte-organization/internal/executive"
	"github.com/Mireuz13/explorarte-organization/internal/executive/postrun"
	"github.com/Mireuz13/explorarte-organization/internal/executive/runtimeadapter"
	"github.com/Mireuz13/explorarte-organization/internal/memory"
	memorybootstrap "github.com/Mireuz13/explorarte-organization/internal/memory/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
	taskpostgres "github.com/Mireuz13/explorarte-organization/internal/tasks/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/tasks/registryadapter"
	"github.com/Mireuz13/explorarte-organization/internal/testdbguard"
	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
)

const postrunOrganization = "explorarte"

func TestPostrunProcessRunAgainstRealPostgres(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	store := openPostrunStore(t, ctx)
	t.Cleanup(store.Close)
	runner, err := platformmigrations.New(store.Pool(), rootmigrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Up(ctx); err != nil {
		t.Fatal(err)
	}
	resetPostrunSchema(t, ctx, store)
	revision := syncPostrunCanonical(t, ctx, store)

	canonicalDir := filepath.Join("..", "..", "..", "docs", "canonical")
	canonicalProvider, err := canonical.New(canonicalDir, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	decisionLedger, err := decisiongraphpostgres.New(store, postrunOrganization)
	if err != nil {
		t.Fatal(err)
	}
	decisionService, err := decisiongraph.NewService(decisionLedger, decisiongraph.SystemClock{})
	if err != nil {
		t.Fatal(err)
	}
	decisionRecorder := runtimeadapter.DecisionGraph{
		Service: decisionService, Canonical: canonicalProvider,
		Limits: executive.DefaultLimits(), Clock: executive.ClockFunc(time.Now),
	}

	traces, err := decisiongraphtrace.New(store, postrunOrganization)
	if err != nil {
		t.Fatal(err)
	}
	completionReader, err := completionpostgres.New(store, postrunOrganization)
	if err != nil {
		t.Fatal(err)
	}
	completionService, err := completion.NewService(completionReader, completionReader, completionReader, completionReader, completionReader, nil)
	if err != nil {
		t.Fatal(err)
	}
	memoryRuntime, err := memorybootstrap.Open(config.Config{Tasks: config.TaskConfig{OrganizationID: postrunOrganization}, Registry: config.RegistryConfig{CanonicalDir: canonicalDir}}, store)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("real completion failure produces a real memory candidate", func(t *testing.T) {
		taskID, attemptID := insertPostrunTaskAttempt(t, ctx, store, revision.ID, "fail", "ingenieria_ia/qa", "ingenieria_ia")
		// Deliberately left unsatisfied: no evidence recorded, so
		// completion's real requirements_satisfied obligation genuinely
		// fails re-verification below — this is not a synthetic verdict.
		insertRequirement(t, ctx, store, taskID, "deliverable", "result", true)
		runID := recordRealAttemptDecision(t, ctx, store, decisionRecorder, taskID, attemptID)

		roleResolver := realTaskRoleResolver(t, store)
		service, err := postrun.NewService(traces, completionService, roleResolver, memoryRuntime.Manager)
		if err != nil {
			t.Fatal(err)
		}

		outcome, err := service.ProcessRun(ctx, postrunOrganization, runID)
		if err != nil {
			t.Fatal(err)
		}
		if outcome.Kind != postrun.KindProposed {
			t.Fatalf("kind=%s want=%s (outcome=%+v)", outcome.Kind, postrun.KindProposed, outcome)
		}
		if outcome.Entry.Status != memory.StatusCandidate {
			t.Fatalf("entry status=%s want=candidate", outcome.Entry.Status)
		}

		var roleID, problem, correction string
		var sourceRunID int64
		var status string
		if err := store.Pool().QueryRow(ctx, `
SELECT v.role_id, v.problem, v.correction, v.source_run_id, e.status
FROM organizational_memory_versions v
JOIN organizational_memory_entries e USING (organization_id, entry_key)
WHERE v.organization_id=$1 AND v.entry_key=$2`, postrunOrganization, outcome.Entry.ID).
			Scan(&roleID, &problem, &correction, &sourceRunID, &status); err != nil {
			t.Fatalf("query proposed entry: %v", err)
		}
		if roleID != "ingenieria_ia/qa" {
			t.Fatalf("role_id=%q want=ingenieria_ia/qa", roleID)
		}
		if sourceRunID != runID {
			t.Fatalf("source_run_id=%d want=%d", sourceRunID, runID)
		}
		if status != "candidate" {
			t.Fatalf("status=%q want=candidate", status)
		}
		if problem == "" || correction == "" {
			t.Fatalf("problem/correction must not be empty: problem=%q correction=%q", problem, correction)
		}

		// Idempotent: processing the same run again must not create a second row.
		again, err := service.ProcessRun(ctx, postrunOrganization, runID)
		if err != nil {
			t.Fatal(err)
		}
		if again.Kind != postrun.KindReused {
			t.Fatalf("second call kind=%s want=%s", again.Kind, postrun.KindReused)
		}
	})

	t.Run("real completion pass skips proposing anything", func(t *testing.T) {
		taskID, attemptID := insertPostrunTaskAttempt(t, ctx, store, revision.ID, "pass", "ingenieria_ia/qa", "ingenieria_ia")
		reqID := insertRequirement(t, ctx, store, taskID, "deliverable", "result", true)
		insertEvidence(t, ctx, store, taskID, reqID, "result", "fixture:evidence:pass", digest("pass-evidence"))
		markRequirementSatisfied(t, ctx, store, reqID)
		runID := recordRealAttemptDecision(t, ctx, store, decisionRecorder, taskID, attemptID)

		roleResolver := realTaskRoleResolver(t, store)
		service, err := postrun.NewService(traces, completionService, roleResolver, memoryRuntime.Manager)
		if err != nil {
			t.Fatal(err)
		}
		outcome, err := service.ProcessRun(ctx, postrunOrganization, runID)
		if err != nil {
			t.Fatal(err)
		}
		if outcome.Kind != postrun.KindSkippedPass {
			t.Fatalf("kind=%s want=%s", outcome.Kind, postrun.KindSkippedPass)
		}

		var count int
		if err := store.Pool().QueryRow(ctx, `SELECT count(*) FROM organizational_memory_versions WHERE organization_id=$1 AND source_run_id=$2`, postrunOrganization, runID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("expected no memory version for a clean pass, found %d", count)
		}
	})

	t.Run("CEO closure role is not eligible to propose", func(t *testing.T) {
		taskID, attemptID := insertPostrunTaskAttempt(t, ctx, store, revision.ID, "ceo", "empresa/ceo", "empresa")
		insertRequirement(t, ctx, store, taskID, "deliverable", "result", true) // left unsatisfied -> real non-pass verdict
		runID := recordRealAttemptDecision(t, ctx, store, decisionRecorder, taskID, attemptID)

		roleResolver := realTaskRoleResolver(t, store)
		service, err := postrun.NewService(traces, completionService, roleResolver, memoryRuntime.Manager)
		if err != nil {
			t.Fatal(err)
		}
		outcome, err := service.ProcessRun(ctx, postrunOrganization, runID)
		if err != nil {
			t.Fatal(err)
		}
		if outcome.Kind != postrun.KindSkippedRoleNotEligible {
			t.Fatalf("kind=%s want=%s (CEO role should not hold memory.propose)", outcome.Kind, postrun.KindSkippedRoleNotEligible)
		}
	})
}

func realTaskRoleResolver(t *testing.T, store *platformpostgres.Store) postrun.TaskRoleResolver {
	t.Helper()
	registryRepo, err := registry.NewPostgresRepository(store)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := registryadapter.New(registryRepo)
	if err != nil {
		t.Fatal(err)
	}
	taskDB, err := taskpostgres.New(store)
	if err != nil {
		t.Fatal(err)
	}
	taskService, err := tasks.NewService(taskDB, catalog, tasks.Config{
		OrganizationID:       postrunOrganization,
		DefaultMaxAttempts:   3,
		DefaultLeaseDuration: time.Minute,
		MaxLeaseDuration:     15 * time.Minute,
		RetryPolicy:          tasks.RetryPolicy{BaseDelay: time.Second, MaxDelay: time.Minute},
		OutboxMaxAttempts:    3,
		OutboxClaimDuration:  time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	return postrun.TaskRoleResolver{Service: taskService}
}

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func openPostrunStore(t *testing.T, ctx context.Context) *platformpostgres.Store {
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
	store, err := platformpostgres.Open(ctx, cfg, "postrun-integration")
	if err != nil {
		t.Fatal(err)
	}
	if err := testdbguard.RequireTestDatabase(ctx, url, store.Pool()); err != nil {
		store.Close()
		t.Fatalf("refusing to run against unverified database: %v", err)
	}
	return store
}

func resetPostrunSchema(t *testing.T, ctx context.Context, store *platformpostgres.Store) {
	t.Helper()
	if err := testdbguard.RequireDestructive(ctx, os.Getenv("ORG_TEST_DATABASE_URL"), store.Pool()); err != nil {
		t.Fatalf("refusing destructive TRUNCATE: %v", err)
	}
	if _, err := store.Pool().Exec(ctx, `
TRUNCATE outbox_events,task_dead_letters,task_events,task_leases,task_attempts,task_evidence,
         task_requirements,task_dependencies,tasks,organization_reporting_lines,
         organization_registry_revision_documents,organization_roles,organizational_units,
         organizations,organization_registry_revisions,audit_events,
         organizational_memory_state_events,organizational_memory_idempotency,
         organizational_memory_evidence_refs,organizational_memory_entries,organizational_memory_versions,
         decision_records,decision_budget_events,decision_verifications,decision_observations,
         decision_node_executions,decision_graph_budgets,decision_branch_events,decision_graph_edges,
         decision_graph_nodes,decision_graph_versions,decision_graph_runs RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("reset postrun schema: %v", err)
	}
}

func syncPostrunCanonical(t *testing.T, ctx context.Context, store *platformpostgres.Store) *registry.Revision {
	t.Helper()
	repo, err := registry.NewPostgresRepository(store)
	if err != nil {
		t.Fatal(err)
	}
	loader, err := registry.NewLoader(filepath.Join("..", "..", "..", "docs", "canonical"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := registry.NewService(loader, repo, postrunOrganization, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.SynchronizeCanonical(ctx, true)
	if err != nil || !result.Applied {
		t.Fatalf("sync=%+v err=%v", result, err)
	}
	revision, err := repo.GetCurrentRevision(ctx, postrunOrganization)
	if err != nil || revision == nil {
		t.Fatalf("revision=%+v err=%v", revision, err)
	}
	return revision
}

func insertPostrunTaskAttempt(t *testing.T, ctx context.Context, store *platformpostgres.Store, revisionID int64, suffix, roleID, unitID string) (taskID, attemptID int64) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := store.Pool().QueryRow(ctx, `
INSERT INTO tasks(
    organization_id,organization_revision_id,assigned_role_id,assigned_unit_id,
    idempotency_key,request_hash,title,instructions,acceptance_criteria,status,
    priority,available_at,max_attempts,attempt_count,version,created_at,updated_at
) VALUES($1,$2,$3,$4,$5,$6,
         'Postrun fixture','Exercise the postrun lesson job.','[]','awaiting_verification',
         0,$7,3,1,1,$7,$7)
RETURNING id`, postrunOrganization, revisionID, roleID, unitID, "postrun-task-"+suffix, digest("postrun-task-"+suffix), now).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	if err := store.Pool().QueryRow(ctx, `
INSERT INTO task_attempts(task_id,ordinal,state,worker_id,leased_at,started_at,finished_at,created_at,updated_at)
VALUES($1,1,'finished','postrun-integration',$2,$2,$2,$2,$2)
RETURNING id`, taskID, now).Scan(&attemptID); err != nil {
		t.Fatal(err)
	}
	return taskID, attemptID
}

func insertRequirement(t *testing.T, ctx context.Context, store *platformpostgres.Store, taskID int64, key, reqType string, required bool) int64 {
	t.Helper()
	var id int64
	if err := store.Pool().QueryRow(ctx, `
INSERT INTO task_requirements(task_id,requirement_key,requirement_type,description,required,status)
VALUES($1,$2,$3,'fixture requirement',$4,'pending') RETURNING id`,
		taskID, key, reqType, required).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func markRequirementSatisfied(t *testing.T, ctx context.Context, store *platformpostgres.Store, requirementID int64) {
	t.Helper()
	if _, err := store.Pool().Exec(ctx, `
UPDATE task_requirements SET status='satisfied', satisfied_at=NOW() WHERE id=$1`, requirementID); err != nil {
		t.Fatal(err)
	}
}

func insertEvidence(t *testing.T, ctx context.Context, store *platformpostgres.Store, taskID, requirementID int64, evidenceType, reference, digestValue string) {
	t.Helper()
	if _, err := store.Pool().Exec(ctx, `
INSERT INTO task_evidence(task_id,requirement_id,evidence_type,reference,digest,recorded_by)
VALUES($1,$2,$3,$4,$5,'postrun-integration')`,
		taskID, requirementID, evidenceType, reference, digestValue); err != nil {
		t.Fatal(err)
	}
}

// recordRealAttemptDecision uses the exact production adapter Rama 25 wired
// into internal/executive (runtimeadapter.DecisionGraph) to produce a real,
// terminal decisiongraph run for taskID/attemptID — the same kind of run
// gatedComplete leaves behind in production. Verdict is always Pass here:
// it only governs whether *this* run's own terminal decision gets recorded
// (required for decisiongraphtrace to read it back at all, since only a
// 'succeeded' run is readable) — it has no bearing on what
// postrun.Service.ProcessRun independently re-derives from the real task
// data via completion.Service.Verify.
//
// RecordAttemptDecision returns only an error, not the run it created, so
// the run id is looked up the same way internal/decisiongraphtrace itself
// reads decisiongraph's tables directly rather than through its Go API.
func recordRealAttemptDecision(t *testing.T, ctx context.Context, store *platformpostgres.Store, recorder runtimeadapter.DecisionGraph, taskID, attemptID int64) int64 {
	t.Helper()
	if err := recorder.RecordAttemptDecision(ctx, executive.AttemptDecisionRecord{
		TaskID: taskID, AttemptID: attemptID, Verdict: executive.CompletionPass, Detail: "postrun integration fixture",
	}); err != nil {
		t.Fatal(err)
	}
	var runID int64
	if err := store.Pool().QueryRow(ctx, `
SELECT id FROM decision_graph_runs
WHERE organization_id=$1 AND task_id=$2 AND attempt_id=$3 AND status='succeeded'`,
		postrunOrganization, taskID, attemptID).Scan(&runID); err != nil {
		t.Fatalf("look up decision graph run for task %d attempt %d: %v", taskID, attemptID, err)
	}
	return runID
}
