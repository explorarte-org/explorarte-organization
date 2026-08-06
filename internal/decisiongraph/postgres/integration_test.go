//go:build integration

package postgres_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/decisiongraph"
	decisionpostgres "github.com/Mireuz13/explorarte-organization/internal/decisiongraph/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
)

const decisionGraphOrganization = "explorarte"

var fixtureHash = digest("decisiongraph-fixture")

type mutableClock struct {
	mu  sync.RWMutex
	now time.Time
}

func (c *mutableClock) Now() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.now
}

func (c *mutableClock) Set(value time.Time) {
	c.mu.Lock()
	c.now = value
	c.mu.Unlock()
}

func TestDecisionGraphPostgresLedger(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	platform := openDecisionGraphStore(t, ctx)
	t.Cleanup(platform.Close)
	runner, err := platformmigrations.New(platform.Pool(), rootmigrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	result, err := runner.Up(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Current != 12 {
		t.Fatalf("current migration=%d, want 12", result.Current)
	}
	resetDecisionGraphSchema(t, ctx, platform)
	t.Cleanup(func() { resetDecisionGraphSchema(t, context.Background(), platform) })
	revision := syncDecisionGraphCanonical(t, ctx, platform)
	taskID, attemptID := insertDecisionGraphTaskAttempt(t, ctx, platform, revision.ID, "main")

	store, err := decisionpostgres.New(platform, decisionGraphOrganization)
	if err != nil {
		t.Fatal(err)
	}
	var _ decisiongraph.Ledger = store

	now := time.Now().UTC().Truncate(time.Microsecond)
	clock := &mutableClock{now: now}
	service, err := decisiongraph.NewService(store, clock)
	if err != nil {
		t.Fatal(err)
	}

	limits := decisiongraph.BudgetLimits{
		MaxNodes:         32,
		MaxDepth:         8,
		MaxParallelNodes: 2,
		MaxModelCalls:    16,
		MaxInputTokens:   10000,
		MaxOutputTokens:  5000,
		MaxReplans:       4,
		MaxVerifications: 8,
		MaxWallTime:      10 * time.Minute,
	}
	createRequest := decisiongraph.CreateRunRequest{
		TaskID:                       taskID,
		AttemptID:                    attemptID,
		ReasoningPolicySchemaVersion: "0.1.0",
		ReasoningPolicyHash:          digest("reasoning-policy"),
		IdempotencyKey:               "decisiongraph-main",
		BudgetLimits:                 limits,
		Deadline:                     now.Add(5*time.Minute + 321*time.Nanosecond),
		CreatedBy:                    "integration/worker",
	}

	run, err := service.CreateRun(ctx, createRequest)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := service.CreateRun(ctx, createRequest)
	if err != nil {
		t.Fatalf("exact idempotent retry: %v", err)
	}
	if retry.ID != run.ID {
		t.Fatalf("retry run=%d, want %d", retry.ID, run.ID)
	}
	conflict := createRequest
	conflict.BudgetLimits.MaxNodes++
	if _, err := service.CreateRun(ctx, conflict); !errors.Is(err, decisiongraph.ErrIdempotencyConflict) {
		t.Fatalf("idempotency conflict=%v", err)
	}

	version, err := service.AppendGraph(ctx, decisiongraph.AppendGraphRequest{
		RunID: run.ID,
		Nodes: []decisiongraph.Node{
			node(1, decisiongraph.NodeGoal, decisiongraph.BranchActive, decisiongraph.ExecutionSucceeded),
			node(2, decisiongraph.NodeCandidateAction, decisiongraph.BranchActive, decisiongraph.ExecutionPending),
			node(3, decisiongraph.NodeVerification, decisiongraph.BranchActive, decisiongraph.ExecutionPending),
			node(4, decisiongraph.NodeDecision, decisiongraph.BranchActive, decisiongraph.ExecutionPending),
		},
		Edges: []decisiongraph.Edge{
			{FromNodeID: 2, ToNodeID: 1, Type: decisiongraph.EdgeDependsOn},
			{FromNodeID: 3, ToNodeID: 2, Type: decisiongraph.EdgeDependsOn},
			{FromNodeID: 4, ToNodeID: 3, Type: decisiongraph.EdgeDependsOn},
		},
		Depths:    map[int64]int{1: 0, 2: 1, 3: 2, 4: 3},
		CreatedBy: "integration/planner",
	})
	if err != nil {
		t.Fatal(err)
	}

	assertDatabaseCycleGuard(t, ctx, platform, run.ID, version.ID)
	if err := service.StartRun(ctx, run.ID); err != nil {
		t.Fatal(err)
	}

	candidate, err := service.ClaimReadyNode(ctx, decisiongraph.ClaimNodeRequest{
		RunID: run.ID, ClaimedBy: "integration/worker", LeaseDuration: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if candidate.LogicalNodeID != 2 || candidate.NodeType != decisiongraph.NodeCandidateAction {
		t.Fatalf("candidate claim=%+v", candidate)
	}
	assertClaimTokenHashed(t, ctx, platform, candidate)

	if err := service.FinishExecution(ctx, decisiongraph.FinishExecutionRequest{
		ExecutionID:  candidate.ExecutionID,
		ClaimToken:   candidate.ClaimToken,
		FinalState:   decisiongraph.ExecutionWaitingVerification,
		InputTokens:  120,
		OutputTokens: 40,
		OutcomeHash:  digest("candidate-outcome"),
		ReasonCode:   "awaiting_process_verification",
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.RecordObservation(ctx, decisiongraph.ObservationRecord{
		ExecutionID:         candidate.ExecutionID,
		SchemaVersion:       "observation/v1",
		ObservationHash:     digest("candidate-observation"),
		SourceKind:          "model_result",
		SourceReferenceHash: digest("opaque-model-result-ref"),
	}); err != nil {
		t.Fatal(err)
	}
	candidateExecutionID := candidate.ExecutionID
	if err := service.RecordVerification(ctx, decisiongraph.VerificationRecord{
		RunID:           run.ID,
		NodeID:          candidate.NodeID,
		ExecutionID:     &candidateExecutionID,
		Label:           decisiongraph.VerificationVerified,
		VerifierRef:     "integration/process-verifier",
		VerifierVersion: "v1",
		EvidenceSetHash: digest("candidate-evidence-set"),
		ReasonCodes:     []string{"requirements_satisfied"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.TransitionBranch(ctx, decisiongraph.BranchTransitionRequest{
		RunID: run.ID, NodeID: candidate.NodeID,
		ToState:    decisiongraph.BranchRejectedByEvidence,
		ReasonCode: "candidate_temporarily_rejected", Actor: "integration/verifier",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := platform.Pool().Exec(ctx, `
UPDATE decision_graph_nodes SET branch_state='active', updated_at=$2 WHERE id=$1`, candidate.NodeID, clock.Now()); err == nil {
		t.Fatal("expected direct evidence-less branch reopen to fail")
	}
	if err := service.TransitionBranch(ctx, decisiongraph.BranchTransitionRequest{
		RunID: run.ID, NodeID: candidate.NodeID, ToState: decisiongraph.BranchActive,
		EvidenceHash: digest("new-candidate-evidence"), ReasonCode: "new_evidence", Actor: "integration/verifier",
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.TransitionBranch(ctx, decisiongraph.BranchTransitionRequest{
		RunID: run.ID, NodeID: candidate.NodeID, ToState: decisiongraph.BranchSelected,
		ReasonCode: "candidate_selected", Actor: "integration/decider",
	}); err != nil {
		t.Fatal(err)
	}

	verification, err := service.ClaimReadyNode(ctx, decisiongraph.ClaimNodeRequest{
		RunID: run.ID, ClaimedBy: "integration/worker", LeaseDuration: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if verification.LogicalNodeID != 3 || verification.NodeType != decisiongraph.NodeVerification {
		t.Fatalf("verification claim=%+v", verification)
	}
	if err := service.FinishExecution(ctx, decisiongraph.FinishExecutionRequest{
		ExecutionID: verification.ExecutionID,
		ClaimToken:  verification.ClaimToken,
		FinalState:  decisiongraph.ExecutionSucceeded,
		OutcomeHash: digest("verification-node-outcome"),
	}); err != nil {
		t.Fatal(err)
	}

	decision, err := service.ClaimReadyNode(ctx, decisiongraph.ClaimNodeRequest{
		RunID: run.ID, ClaimedBy: "integration/worker", LeaseDuration: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.LogicalNodeID != 4 || decision.NodeType != decisiongraph.NodeDecision {
		t.Fatalf("decision claim=%+v", decision)
	}
	if err := service.FinishExecution(ctx, decisiongraph.FinishExecutionRequest{
		ExecutionID: decision.ExecutionID,
		ClaimToken:  decision.ClaimToken,
		FinalState:  decisiongraph.ExecutionSucceeded,
		OutcomeHash: digest("decision-node-outcome"),
	}); err != nil {
		t.Fatal(err)
	}

	if err := service.RecordTerminalDecision(ctx, decisiongraph.TerminalDecisionRequest{
		RunID:                   run.ID,
		DecisionNodeID:          decision.NodeID,
		SelectedCandidateNodeID: candidate.NodeID,
		EvidenceSetHash:         digest("terminal-evidence-set"),
		VerificationSetHash:     digest("terminal-verification-set"),
		DecisionHash:            digest("terminal-decision"),
		VerificationLabel:       decisiongraph.VerificationVerified,
		CreatedBy:               "integration/decider",
	}); err != nil {
		t.Fatal(err)
	}
	trace, err := service.TraceRef(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if trace.OrganizationID != decisionGraphOrganization || trace.RunID != run.ID || trace.SchemaVersion != "decision-trace/v1" || len(trace.TraceHash) != 64 {
		t.Fatalf("trace=%+v", trace)
	}
	assertTerminalDecisionImmutable(t, ctx, platform, run.ID)

	t.Run("concurrent claim has one winner", func(t *testing.T) {
		claimRun := createSimpleRun(t, ctx, service, taskID, attemptID, limits, "concurrent", now)
		results := make(chan error, 2)
		for i := 0; i < 2; i++ {
			go func(worker int) {
				_, claimErr := service.ClaimReadyNode(ctx, decisiongraph.ClaimNodeRequest{
					RunID: claimRun.ID, ClaimedBy: fmt.Sprintf("integration/concurrent-%d", worker), LeaseDuration: time.Minute,
				})
				results <- claimErr
			}(i)
		}
		successes := 0
		failures := 0
		for i := 0; i < 2; i++ {
			if claimErr := <-results; claimErr == nil {
				successes++
			} else {
				failures++
			}
		}
		if successes != 1 || failures != 1 {
			t.Fatalf("successes=%d failures=%d, want 1/1", successes, failures)
		}
		var active int
		if err := platform.Pool().QueryRow(ctx, `
SELECT count(*) FROM decision_node_executions
WHERE run_id=$1 AND status IN ('claimed','running','waiting_verification')`, claimRun.ID).Scan(&active); err != nil {
			t.Fatal(err)
		}
		if active != 1 {
			t.Fatalf("active executions=%d, want 1", active)
		}
	})

	t.Run("expired claim recovers without redispatch", func(t *testing.T) {
		recoveryRun := createSimpleRun(t, ctx, service, taskID, attemptID, limits, "recovery", now)
		claim, claimErr := service.ClaimReadyNode(ctx, decisiongraph.ClaimNodeRequest{
			RunID: recoveryRun.ID, ClaimedBy: "integration/recovery", LeaseDuration: time.Second,
		})
		if claimErr != nil {
			t.Fatal(claimErr)
		}
		clock.Set(now.Add(2 * time.Second))
		recovered, recoverErr := service.RecoverExpiredExecutions(ctx, 32)
		if recoverErr != nil {
			t.Fatal(recoverErr)
		}
		if recovered < 1 {
			t.Fatalf("recovered=%d, want at least 1", recovered)
		}
		var state decisiongraph.ExecutionState
		if err := platform.Pool().QueryRow(ctx, `SELECT execution_state FROM decision_graph_nodes WHERE id=$1`, claim.NodeID).Scan(&state); err != nil {
			t.Fatal(err)
		}
		if state != decisiongraph.ExecutionFailed {
			t.Fatalf("recovered state=%s, want failed", state)
		}
		var active int64
		if err := platform.Pool().QueryRow(ctx, `SELECT active_parallel_nodes FROM decision_graph_budgets WHERE run_id=$1`, recoveryRun.ID).Scan(&active); err != nil {
			t.Fatal(err)
		}
		if active != 0 {
			t.Fatalf("active parallel=%d, want 0", active)
		}
		clock.Set(now)
	})
}

func createSimpleRun(t *testing.T, ctx context.Context, service *decisiongraph.Service, taskID, attemptID int64, limits decisiongraph.BudgetLimits, suffix string, now time.Time) decisiongraph.Run {
	t.Helper()
	run, err := service.CreateRun(ctx, decisiongraph.CreateRunRequest{
		TaskID: taskID, AttemptID: attemptID,
		ReasoningPolicySchemaVersion: "0.1.0",
		ReasoningPolicyHash:          digest("reasoning-policy"),
		IdempotencyKey:               "decisiongraph-" + suffix,
		BudgetLimits:                 limits,
		Deadline:                     now.Add(5 * time.Minute),
		CreatedBy:                    "integration/worker",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.AppendGraph(ctx, decisiongraph.AppendGraphRequest{
		RunID: run.ID,
		Nodes: []decisiongraph.Node{
			node(1, decisiongraph.NodeGoal, decisiongraph.BranchActive, decisiongraph.ExecutionSucceeded),
			node(2, decisiongraph.NodeCandidateAction, decisiongraph.BranchActive, decisiongraph.ExecutionPending),
		},
		Edges:     []decisiongraph.Edge{{FromNodeID: 2, ToNodeID: 1, Type: decisiongraph.EdgeDependsOn}},
		Depths:    map[int64]int{1: 0, 2: 1},
		CreatedBy: "integration/planner",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.StartRun(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	return run
}

func node(id int64, kind decisiongraph.NodeType, branch decisiongraph.BranchState, execution decisiongraph.ExecutionState) decisiongraph.Node {
	return decisiongraph.Node{
		ID: id, Type: kind, BranchState: branch, ExecutionState: execution,
		PayloadSchemaVersion: "node/v1", PayloadHash: digest(fmt.Sprintf("node-%d-%s", id, kind)), CreatedBy: "integration/planner",
	}
}

func assertClaimTokenHashed(t *testing.T, ctx context.Context, store *platformpostgres.Store, claim decisiongraph.NodeClaim) {
	t.Helper()
	var stored string
	if err := store.Pool().QueryRow(ctx, `SELECT claim_token_hash FROM decision_node_executions WHERE id=$1`, claim.ExecutionID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(claim.ClaimToken))
	want := hex.EncodeToString(sum[:])
	if stored != want || stored == claim.ClaimToken {
		t.Fatalf("stored claim token is not the expected SHA-256 hash")
	}
}

func assertDatabaseCycleGuard(t *testing.T, ctx context.Context, store *platformpostgres.Store, runID, versionID int64) {
	t.Helper()
	var goalID, candidateID int64
	if err := store.Pool().QueryRow(ctx, `
SELECT
  max(id) FILTER (WHERE logical_node_id=1),
  max(id) FILTER (WHERE logical_node_id=2)
FROM decision_graph_nodes
WHERE run_id=$1 AND graph_version_id=$2`, runID, versionID).Scan(&goalID, &candidateID); err != nil {
		t.Fatal(err)
	}
	_, err := store.Pool().Exec(ctx, `
INSERT INTO decision_graph_edges(organization_id,run_id,graph_version_id,from_node_id,to_node_id,edge_type)
VALUES($1,$2,$3,$4,$5,'depends_on')`, decisionGraphOrganization, runID, versionID, goalID, candidateID)
	if err == nil {
		t.Fatal("expected database dependency-cycle rejection")
	}
}

func assertTerminalDecisionImmutable(t *testing.T, ctx context.Context, store *platformpostgres.Store, runID int64) {
	t.Helper()
	if _, err := store.Pool().Exec(ctx, `UPDATE decision_records SET created_by='tampered' WHERE run_id=$1`, runID); err == nil {
		t.Fatal("expected immutable decision record rejection")
	}
}

func openDecisionGraphStore(t *testing.T, ctx context.Context) *platformpostgres.Store {
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
	store, err := platformpostgres.Open(ctx, cfg, "decisiongraph-integration")
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func resetDecisionGraphSchema(t *testing.T, ctx context.Context, store *platformpostgres.Store) {
	t.Helper()
	resetCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := store.Pool().Exec(resetCtx, `
TRUNCATE organizations, organization_registry_revisions, audit_events
RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("reset decision graph schema: %v", err)
	}
}

func syncDecisionGraphCanonical(t *testing.T, ctx context.Context, store *platformpostgres.Store) *registry.Revision {
	t.Helper()
	repo, err := registry.NewPostgresRepository(store)
	if err != nil {
		t.Fatal(err)
	}
	loader, err := registry.NewLoader(filepath.Join("..", "..", "..", "docs", "canonical"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := registry.NewService(loader, repo, decisionGraphOrganization, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.SynchronizeCanonical(ctx, true)
	if err != nil || !result.Applied {
		t.Fatalf("sync=%+v err=%v", result, err)
	}
	revision, err := repo.GetCurrentRevision(ctx, decisionGraphOrganization)
	if err != nil || revision == nil {
		t.Fatalf("revision=%+v err=%v", revision, err)
	}
	return revision
}

func insertDecisionGraphTaskAttempt(t *testing.T, ctx context.Context, store *platformpostgres.Store, revisionID int64, suffix string) (taskID, attemptID int64) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := store.Pool().QueryRow(ctx, `
INSERT INTO tasks(
    organization_id,organization_revision_id,assigned_role_id,assigned_unit_id,
    idempotency_key,request_hash,title,instructions,acceptance_criteria,status,
    priority,available_at,max_attempts,attempt_count,version,created_at,updated_at
) VALUES($1,$2,'ingenieria_ia/qa','ingenieria_ia',$3,$4,
         'Decision graph fixture','Exercise durable decision graph.','[]','running',
         0,$5,3,1,1,$5,$5)
RETURNING id`, decisionGraphOrganization, revisionID, "decisiongraph-task-"+suffix, digest("task-"+suffix), now).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	if err := store.Pool().QueryRow(ctx, `
INSERT INTO task_attempts(task_id,ordinal,state,worker_id,leased_at,started_at,created_at,updated_at)
VALUES($1,1,'running','decisiongraph-integration',$2,$2,$2,$2)
RETURNING id`, taskID, now).Scan(&attemptID); err != nil {
		t.Fatal(err)
	}
	return taskID, attemptID
}

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
