//go:build integration

package decisiongraphtrace_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/decisiongraph"
	decisionpostgres "github.com/Mireuz13/explorarte-organization/internal/decisiongraph/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/decisiongraphtrace"
	"github.com/Mireuz13/explorarte-organization/internal/evaluation"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
)

const traceIntegrationOrganization = "explorarte"

func TestDecisionGraphTraceStore(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	platform := openTraceStore(t, ctx)
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
		t.Fatalf("current migration=%d, want 17", result.Current)
	}
	resetTraceSchema(t, ctx, platform)
	t.Cleanup(func() { resetTraceSchema(t, context.Background(), platform) })
	revision := syncTraceCanonical(t, ctx, platform)
	taskID, attemptID := insertTraceTaskAttempt(t, ctx, platform, revision.ID, "main")

	ledger, err := decisionpostgres.New(platform, traceIntegrationOrganization)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	service, err := decisiongraph.NewService(ledger, fixedClock{now})
	if err != nil {
		t.Fatal(err)
	}
	run := driveRunToSucceeded(t, ctx, service, taskID, attemptID, now)

	trace, err := decisiongraphtrace.New(platform, traceIntegrationOrganization)
	if err != nil {
		t.Fatal(err)
	}
	var _ evaluation.TraceSource = trace

	t.Run("TraceRefForRun returns a self-consistent, verifiable ref", func(t *testing.T) {
		ref, err := trace.TraceRefForRun(ctx, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if ref.RunID != run.ID || ref.OrganizationID != traceIntegrationOrganization || len(ref.TraceHash) != 64 {
			t.Fatalf("ref=%+v", ref)
		}
		if err := ref.Validate(); err != nil {
			t.Fatalf("ref should validate: %v", err)
		}

		loaded, err := trace.LoadTrace(ctx, ref)
		if err != nil {
			t.Fatal(err)
		}
		if err := loaded.Validate(); err != nil {
			t.Fatalf("loaded trace should validate against its own ref: %v", err)
		}
		if loaded.Ref != ref {
			t.Fatalf("loaded.Ref=%+v, want %+v", loaded.Ref, ref)
		}
	})

	t.Run("LoadTrace is deterministic across repeated calls", func(t *testing.T) {
		ref, err := trace.TraceRefForRun(ctx, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		first, err := trace.LoadTrace(ctx, ref)
		if err != nil {
			t.Fatal(err)
		}
		second, err := trace.LoadTrace(ctx, ref)
		if err != nil {
			t.Fatal(err)
		}
		if string(first.Payload) != string(second.Payload) {
			t.Fatal("two LoadTrace calls for the same run produced different payloads")
		}
	})

	t.Run("LoadTrace rejects a tampered hash", func(t *testing.T) {
		ref, err := trace.TraceRefForRun(ctx, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		tampered := ref
		tampered.TraceHash = digest("not-the-real-hash")
		if _, err := trace.LoadTrace(ctx, tampered); err == nil {
			t.Fatal("expected an error for a tampered trace hash")
		}
	})

	t.Run("LoadTrace rejects an organization mismatch", func(t *testing.T) {
		ref, err := trace.TraceRefForRun(ctx, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		other := ref
		other.OrganizationID = "some-other-org"
		if _, err := trace.LoadTrace(ctx, other); err == nil {
			t.Fatal("expected an error for an organization mismatch")
		}
	})

	t.Run("TraceRefForRun rejects a run that never reached succeeded", func(t *testing.T) {
		unstartedRun, err := service.CreateRun(ctx, decisiongraph.CreateRunRequest{
			TaskID: taskID, AttemptID: attemptID,
			ReasoningPolicySchemaVersion: "0.1.0",
			ReasoningPolicyHash:          digest("reasoning-policy"),
			IdempotencyKey:               "decisiongraphtrace-unstarted",
			BudgetLimits:                 testBudgetLimits(),
			Deadline:                     now.Add(5 * time.Minute),
			CreatedBy:                    "integration/worker",
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := trace.TraceRefForRun(ctx, unstartedRun.ID); err == nil {
			t.Fatal("expected an error for a run that is not succeeded")
		}
	})

	t.Run("TraceRefForRun rejects an unknown run id", func(t *testing.T) {
		if _, err := trace.TraceRefForRun(ctx, 9_999_999); err == nil {
			t.Fatal("expected an error for an unknown run id")
		}
	})
}

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

func testBudgetLimits() decisiongraph.BudgetLimits {
	return decisiongraph.BudgetLimits{
		MaxNodes: 32, MaxDepth: 8, MaxParallelNodes: 2, MaxModelCalls: 16,
		MaxInputTokens: 10000, MaxOutputTokens: 5000, MaxReplans: 4,
		MaxVerifications: 8, MaxWallTime: 10 * time.Minute,
	}
}

func node(id int64, kind decisiongraph.NodeType, branch decisiongraph.BranchState, execution decisiongraph.ExecutionState) decisiongraph.Node {
	return decisiongraph.Node{
		ID: id, Type: kind, BranchState: branch, ExecutionState: execution,
		PayloadSchemaVersion: "node/v1", PayloadHash: digest(fmt.Sprintf("node-%d-%s", id, kind)), CreatedBy: "integration/planner",
	}
}

// driveRunToSucceeded exercises decisiongraph's own public Service to reach
// a genuine terminal, succeeded run with a recorded decision — the same
// path production code takes — rather than hand-crafting rows that could
// drift from the schema's own invariants.
func driveRunToSucceeded(t *testing.T, ctx context.Context, service *decisiongraph.Service, taskID, attemptID int64, now time.Time) decisiongraph.Run {
	t.Helper()
	run, err := service.CreateRun(ctx, decisiongraph.CreateRunRequest{
		TaskID: taskID, AttemptID: attemptID,
		ReasoningPolicySchemaVersion: "0.1.0",
		ReasoningPolicyHash:          digest("reasoning-policy"),
		IdempotencyKey:               "decisiongraphtrace-main",
		BudgetLimits:                 testBudgetLimits(),
		Deadline:                     now.Add(5 * time.Minute),
		CreatedBy:                    "integration/worker",
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := service.AppendGraph(ctx, decisiongraph.AppendGraphRequest{
		RunID: run.ID,
		Nodes: []decisiongraph.Node{
			node(1, decisiongraph.NodeGoal, decisiongraph.BranchActive, decisiongraph.ExecutionSucceeded),
			node(2, decisiongraph.NodeCandidateAction, decisiongraph.BranchActive, decisiongraph.ExecutionPending),
			node(3, decisiongraph.NodeDecision, decisiongraph.BranchActive, decisiongraph.ExecutionPending),
		},
		Edges: []decisiongraph.Edge{
			{FromNodeID: 2, ToNodeID: 1, Type: decisiongraph.EdgeDependsOn},
			{FromNodeID: 3, ToNodeID: 2, Type: decisiongraph.EdgeDependsOn},
		},
		CreatedBy: "integration/planner",
	}); err != nil {
		t.Fatal(err)
	}

	if err := service.StartRun(ctx, run.ID); err != nil {
		t.Fatal(err)
	}

	candidate, err := service.ClaimReadyNode(ctx, decisiongraph.ClaimNodeRequest{
		RunID: run.ID, ClaimedBy: "integration/worker", LeaseDuration: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.FinishExecution(ctx, decisiongraph.FinishExecutionRequest{
		ExecutionID: candidate.ExecutionID, ClaimToken: candidate.ClaimToken,
		FinalState: decisiongraph.ExecutionWaitingVerification, OutcomeHash: digest("candidate-outcome"),
	}); err != nil {
		t.Fatal(err)
	}
	candidateExecutionID := candidate.ExecutionID
	// RecordVerification with a "verified" label transitions both the
	// execution and the node from waiting_verification to succeeded.
	if err := service.RecordVerification(ctx, decisiongraph.VerificationRecord{
		RunID: run.ID, NodeID: candidate.NodeID, ExecutionID: &candidateExecutionID,
		Label: decisiongraph.VerificationVerified, VerifierRef: "integration/process-verifier",
		VerifierVersion: "v1", EvidenceSetHash: digest("candidate-evidence-set"),
		ReasonCodes: []string{"requirements_satisfied"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.TransitionBranch(ctx, decisiongraph.BranchTransitionRequest{
		RunID: run.ID, NodeID: candidate.NodeID, ToState: decisiongraph.BranchSelected,
		ReasonCode: "candidate_selected", Actor: "integration/decider",
	}); err != nil {
		t.Fatal(err)
	}

	decision, err := service.ClaimReadyNode(ctx, decisiongraph.ClaimNodeRequest{
		RunID: run.ID, ClaimedBy: "integration/worker", LeaseDuration: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.FinishExecution(ctx, decisiongraph.FinishExecutionRequest{
		ExecutionID: decision.ExecutionID, ClaimToken: decision.ClaimToken,
		FinalState: decisiongraph.ExecutionSucceeded, OutcomeHash: digest("decision-node-outcome"),
	}); err != nil {
		t.Fatal(err)
	}

	if err := service.RecordTerminalDecision(ctx, decisiongraph.TerminalDecisionRequest{
		RunID: run.ID, DecisionNodeID: decision.NodeID, SelectedCandidateNodeID: candidate.NodeID,
		EvidenceSetHash: digest("terminal-evidence-set"), VerificationSetHash: digest("terminal-verification-set"),
		DecisionHash: digest("terminal-decision"), VerificationLabel: decisiongraph.VerificationVerified,
		CreatedBy: "integration/decider",
	}); err != nil {
		t.Fatal(err)
	}
	return run
}

func openTraceStore(t *testing.T, ctx context.Context) *platformpostgres.Store {
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
	store, err := platformpostgres.Open(ctx, cfg, "decisiongraphtrace-integration")
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func resetTraceSchema(t *testing.T, ctx context.Context, store *platformpostgres.Store) {
	t.Helper()
	resetCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := store.Pool().Exec(resetCtx, `
TRUNCATE organizations, organization_registry_revisions, audit_events
RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("reset decisiongraphtrace schema: %v", err)
	}
}

func syncTraceCanonical(t *testing.T, ctx context.Context, store *platformpostgres.Store) *registry.Revision {
	t.Helper()
	repo, err := registry.NewPostgresRepository(store)
	if err != nil {
		t.Fatal(err)
	}
	loader, err := registry.NewLoader(filepath.Join("..", "..", "docs", "canonical"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := registry.NewService(loader, repo, traceIntegrationOrganization, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.SynchronizeCanonical(ctx, true)
	if err != nil || !result.Applied {
		t.Fatalf("sync=%+v err=%v", result, err)
	}
	revision, err := repo.GetCurrentRevision(ctx, traceIntegrationOrganization)
	if err != nil || revision == nil {
		t.Fatalf("revision=%+v err=%v", revision, err)
	}
	return revision
}

func insertTraceTaskAttempt(t *testing.T, ctx context.Context, store *platformpostgres.Store, revisionID int64, suffix string) (taskID, attemptID int64) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := store.Pool().QueryRow(ctx, `
INSERT INTO tasks(
    organization_id,organization_revision_id,assigned_role_id,assigned_unit_id,
    idempotency_key,request_hash,title,instructions,acceptance_criteria,status,
    priority,available_at,max_attempts,attempt_count,version,created_at,updated_at
) VALUES($1,$2,'ingenieria_ia/qa','ingenieria_ia',$3,$4,
         'Decision graph trace fixture','Exercise the decisiongraphtrace adapter.','[]','running',
         0,$5,3,1,1,$5,$5)
RETURNING id`, traceIntegrationOrganization, revisionID, "decisiongraphtrace-task-"+suffix, digest("task-"+suffix), now).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	if err := store.Pool().QueryRow(ctx, `
INSERT INTO task_attempts(task_id,ordinal,state,worker_id,leased_at,started_at,created_at,updated_at)
VALUES($1,1,'running','decisiongraphtrace-integration',$2,$2,$2,$2)
RETURNING id`, taskID, now).Scan(&attemptID); err != nil {
		t.Fatal(err)
	}
	return taskID, attemptID
}

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
