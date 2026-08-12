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

	"github.com/Mireuz13/explorarte-organization/internal/completion"
	completionpostgres "github.com/Mireuz13/explorarte-organization/internal/completion/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/decisiongraph"
	decisiongraphpostgres "github.com/Mireuz13/explorarte-organization/internal/decisiongraph/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/testdbguard"
	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
)

const completionOrganization = "explorarte"

func TestCompletionStorePostgreSQL17(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	platform := openCompletionStore(t, ctx)
	t.Cleanup(platform.Close)
	runner, err := platformmigrations.New(platform.Pool(), rootmigrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Up(ctx); err != nil {
		t.Fatal(err)
	}
	resetCompletionSchema(t, ctx, platform)
	revision := syncCompletionCanonical(t, ctx, platform)

	store, err := completionpostgres.New(platform, completionOrganization)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("all obligations independently verified", func(t *testing.T) {
		taskID, attemptID := insertCompletionTaskAttempt(t, ctx, platform, revision.ID, "pass")
		artifactReqID := insertRequirement(t, ctx, platform, taskID, "artifact-req", "artifact", true)
		checkReqID := insertRequirement(t, ctx, platform, taskID, "check-req", "check", true)
		approvalReqID := insertRequirement(t, ctx, platform, taskID, "approval-req", "approval", true)
		markRequirementSatisfied(t, ctx, platform, artifactReqID)
		markRequirementSatisfied(t, ctx, platform, checkReqID)
		markRequirementSatisfied(t, ctx, platform, approvalReqID)

		artifactDigest := digest("artifact-body")
		storageKey := "artifact://sha256/" + artifactDigest
		insertStagingArtifact(t, ctx, platform, artifactDigest, storageKey)
		insertEvidence(t, ctx, platform, taskID, artifactReqID, "artifact", storageKey, artifactDigest)

		workspaceID := insertStagingWorkspace(t, ctx, platform, completionOrganization, revision.ID, taskID, attemptID, "pass")
		insertStagingCheck(t, ctx, platform, workspaceID, taskID, checkReqID, "passed")
		insertEvidence(t, ctx, platform, taskID, checkReqID, "check", "staging-check-ref", "")

		actionDigest := digest("approved-action")
		requestID := insertConsumedApprovalRequest(t, ctx, platform, completionOrganization, revision.ID, actionDigest)
		insertEvidence(t, ctx, platform, taskID, approvalReqID, "approval", fmt.Sprintf("%d", requestID), actionDigest)

		decisionService := newCompletionDecisionService(t, platform)
		driveRunToSucceeded(t, ctx, platform, decisionService, taskID, attemptID, time.Now().UTC().Truncate(time.Microsecond))

		service, err := completion.NewService(store, store, store, store, store, nil)
		if err != nil {
			t.Fatal(err)
		}
		result, err := service.Verify(ctx, completion.VerificationRequest{TaskID: taskID, AttemptID: attemptID})
		if err != nil {
			t.Fatal(err)
		}
		if result.Verdict != completion.VerdictPass {
			t.Fatalf("verdict=%s obligations=%+v", result.Verdict, result.Obligations)
		}
		if len(result.Obligations) != 5 {
			t.Fatalf("expected 5 obligations, got %d: %+v", len(result.Obligations), result.Obligations)
		}
		for _, o := range result.Obligations {
			if o.Label != completion.LabelVerified {
				t.Fatalf("obligation %s not verified: %+v", o.Obligation, o)
			}
		}
	})

	t.Run("artifact digest mismatch fails", func(t *testing.T) {
		taskID, attemptID := insertCompletionTaskAttempt(t, ctx, platform, revision.ID, "artifact-mismatch")
		artifactReqID := insertRequirement(t, ctx, platform, taskID, "artifact-req", "artifact", true)
		markRequirementSatisfied(t, ctx, platform, artifactReqID)

		realDigest := digest("real-body")
		claimedDigest := digest("different-body")
		insertStagingArtifact(t, ctx, platform, realDigest, "artifact://sha256/"+realDigest)
		insertEvidence(t, ctx, platform, taskID, artifactReqID, "artifact", "artifact://sha256/"+realDigest, claimedDigest)

		service, err := completion.NewService(store, store, store, store, store, nil)
		if err != nil {
			t.Fatal(err)
		}
		result, err := service.Verify(ctx, completion.VerificationRequest{TaskID: taskID, AttemptID: attemptID})
		if err != nil {
			t.Fatal(err)
		}
		if result.Verdict != completion.VerdictFail {
			t.Fatalf("verdict=%s obligations=%+v", result.Verdict, result.Obligations)
		}
	})

	t.Run("no decision graph run is vacuously verified", func(t *testing.T) {
		taskID, attemptID := insertCompletionTaskAttempt(t, ctx, platform, revision.ID, "no-run")
		service, err := completion.NewService(store, store, store, store, store, nil)
		if err != nil {
			t.Fatal(err)
		}
		result, err := service.Verify(ctx, completion.VerificationRequest{TaskID: taskID, AttemptID: attemptID})
		if err != nil {
			t.Fatal(err)
		}
		if result.Verdict != completion.VerdictPass {
			t.Fatalf("verdict=%s obligations=%+v", result.Verdict, result.Obligations)
		}
	})

	t.Run("unknown task returns ErrTaskNotFound", func(t *testing.T) {
		service, err := completion.NewService(store, store, store, store, store, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := service.Verify(ctx, completion.VerificationRequest{TaskID: 9_999_999, AttemptID: 1}); err == nil {
			t.Fatal("expected error for unknown task")
		}
	})
}

func openCompletionStore(t *testing.T, ctx context.Context) *platformpostgres.Store {
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
	store, err := platformpostgres.Open(ctx, cfg, "completion-integration")
	if err != nil {
		t.Fatal(err)
	}
	if err := testdbguard.RequireTestDatabase(ctx, url, store.Pool()); err != nil {
		store.Close()
		t.Fatalf("refusing to run against unverified database: %v", err)
	}
	return store
}

func resetCompletionSchema(t *testing.T, ctx context.Context, store *platformpostgres.Store) {
	t.Helper()
	if err := testdbguard.RequireDestructive(ctx, os.Getenv("ORG_TEST_DATABASE_URL"), store.Pool()); err != nil {
		t.Fatalf("refusing destructive TRUNCATE: %v", err)
	}
	resetCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := store.Pool().Exec(resetCtx, `
TRUNCATE organizations, organization_registry_revisions, audit_events
RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("reset completion schema: %v", err)
	}
}

func syncCompletionCanonical(t *testing.T, ctx context.Context, store *platformpostgres.Store) *registry.Revision {
	t.Helper()
	repo, err := registry.NewPostgresRepository(store)
	if err != nil {
		t.Fatal(err)
	}
	loader, err := registry.NewLoader(filepath.Join("..", "..", "..", "docs", "canonical"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := registry.NewService(loader, repo, completionOrganization, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.SynchronizeCanonical(ctx, true)
	if err != nil || !result.Applied {
		t.Fatalf("sync=%+v err=%v", result, err)
	}
	revision, err := repo.GetCurrentRevision(ctx, completionOrganization)
	if err != nil || revision == nil {
		t.Fatalf("revision=%+v err=%v", revision, err)
	}
	return revision
}

func insertCompletionTaskAttempt(t *testing.T, ctx context.Context, store *platformpostgres.Store, revisionID int64, suffix string) (taskID, attemptID int64) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := store.Pool().QueryRow(ctx, `
INSERT INTO tasks(
    organization_id,organization_revision_id,assigned_role_id,assigned_unit_id,
    idempotency_key,request_hash,title,instructions,acceptance_criteria,status,
    priority,available_at,max_attempts,attempt_count,version,created_at,updated_at
) VALUES($1,$2,'ingenieria_ia/qa','ingenieria_ia',$3,$4,
         'Completion fixture','Exercise the completion verifier.','[]','awaiting_verification',
         0,$5,3,1,1,$5,$5)
RETURNING id`, completionOrganization, revisionID, "completion-task-"+suffix, digest("task-"+suffix), now).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	if err := store.Pool().QueryRow(ctx, `
INSERT INTO task_attempts(task_id,ordinal,state,worker_id,leased_at,started_at,finished_at,created_at,updated_at)
VALUES($1,1,'finished','completion-integration',$2,$2,$2,$2,$2)
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
	var digestArg any
	if digestValue == "" {
		digestArg = nil
	} else {
		digestArg = digestValue
	}
	if _, err := store.Pool().Exec(ctx, `
INSERT INTO task_evidence(task_id,requirement_id,evidence_type,reference,digest,recorded_by)
VALUES($1,$2,$3,$4,$5,'completion-integration')`,
		taskID, requirementID, evidenceType, reference, digestArg); err != nil {
		t.Fatal(err)
	}
}

func insertStagingArtifact(t *testing.T, ctx context.Context, store *platformpostgres.Store, digestValue, storageKey string) {
	t.Helper()
	if _, err := store.Pool().Exec(ctx, `
INSERT INTO staging_artifacts(digest,size_bytes,media_type,storage_key)
VALUES($1,10,'application/octet-stream',$2)`, digestValue, storageKey); err != nil {
		t.Fatal(err)
	}
}

func insertStagingWorkspace(t *testing.T, ctx context.Context, store *platformpostgres.Store, organizationID string, revisionID, taskID, attemptID int64, suffix string) int64 {
	t.Helper()
	var id int64
	if err := store.Pool().QueryRow(ctx, `
INSERT INTO staging_workspaces(
    organization_id,organization_revision_id,task_id,attempt_id,repository_id,repository_config_hash,
    workspace_key,workspace_ref,base_commit,target_ref,status,holder_id,actor_role_id
) VALUES($1,$2,$3,$4,'explorarte-organization',$5,$6,'','`+fmt.Sprintf("%040x", 0)+`','refs/heads/main','active','completion-integration','ingenieria_ia/qa')
RETURNING id`, organizationID, revisionID, taskID, attemptID, digest("repo-config"), "completion-workspace-"+suffix).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func insertStagingCheck(t *testing.T, ctx context.Context, store *platformpostgres.Store, workspaceID, taskID, requirementID int64, status string) {
	t.Helper()
	if _, err := store.Pool().Exec(ctx, `
INSERT INTO staging_checks(workspace_id,task_id,requirement_id,name,status,reference,actor_role_id)
VALUES($1,$2,$3,'fixture-check',$4,'staging-check-ref','ingenieria_ia/qa')`,
		workspaceID, taskID, requirementID, status); err != nil {
		t.Fatal(err)
	}
}

func insertConsumedApprovalRequest(t *testing.T, ctx context.Context, store *platformpostgres.Store, organizationID string, revisionID int64, actionDigest string) int64 {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	expiresAt := now.Add(time.Hour)
	var id int64
	if err := store.Pool().QueryRow(ctx, `
INSERT INTO authorization_requests(
    organization_id,organization_revision_id,capability_matrix_hash,requester_role_id,capability_id,
    risk,approval_mode,resource_type,resource_id,action_digest,idempotency_key,request_hash,
    status,reason,version,created_at,updated_at,expires_at,approved_at,consumed_at
) VALUES($1,$2,$3,'ingenieria_ia/qa','organization.activate_skill','high','owner','skill','fixture-skill',
         $4,$5,$6,'consumed','fixture approval',1,$7,$7,$8,$7,$7)
RETURNING id`, organizationID, revisionID, digest("capability-matrix"), actionDigest,
		"completion-approval-"+actionDigest[:8], digest("approval-request-"+actionDigest), now, expiresAt).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func newCompletionDecisionService(t *testing.T, store *platformpostgres.Store) *decisiongraph.Service {
	t.Helper()
	ledger, err := decisiongraphpostgres.New(store, completionOrganization)
	if err != nil {
		t.Fatal(err)
	}
	service, err := decisiongraph.NewService(ledger, fixedClock{time.Now().UTC().Truncate(time.Microsecond)})
	if err != nil {
		t.Fatal(err)
	}
	return service
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

// driveRunToSucceeded exercises decisiongraph's own public Service to reach a
// genuine terminal, succeeded run with a recorded decision — the same path
// production code takes — rather than hand-crafting rows that could drift from
// the schema's own invariants. Adapted from internal/decisiongraphtrace's own
// integration test, which established this exact sequence first.
func driveRunToSucceeded(t *testing.T, ctx context.Context, platform *platformpostgres.Store, service *decisiongraph.Service, taskID, attemptID int64, now time.Time) decisiongraph.Run {
	t.Helper()
	candidateEvidenceHash := digest("candidate-evidence-set")
	run, err := service.CreateRun(ctx, decisiongraph.CreateRunRequest{
		TaskID: taskID, AttemptID: attemptID,
		ReasoningPolicySchemaVersion: "0.1.0",
		ReasoningPolicyHash:          digest("reasoning-policy"),
		IdempotencyKey:               fmt.Sprintf("completion-integration-%d-%d", taskID, attemptID),
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
	if err := service.RecordVerification(ctx, decisiongraph.VerificationRecord{
		RunID: run.ID, NodeID: candidate.NodeID, ExecutionID: &candidateExecutionID,
		Label: decisiongraph.VerificationVerified, VerifierRef: "integration/process-verifier",
		VerifierVersion: "v1", EvidenceSetHash: candidateEvidenceHash,
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
		VerificationLabel: decisiongraph.VerificationVerified,
		CreatedBy:         "integration/decider",
	}); err != nil {
		t.Fatal(err)
	}

	// The decision must commit to the evidence that was actually verified,
	// not an arbitrary caller-asserted value — RecordTerminalDecision no
	// longer accepts one at all; it derives it from decision_verifications.
	var storedEvidenceHash string
	if err := platform.Pool().QueryRow(ctx, `SELECT evidence_set_hash FROM decision_records WHERE run_id=$1`, run.ID).Scan(&storedEvidenceHash); err != nil {
		t.Fatalf("read stored terminal decision evidence hash: %v", err)
	}
	if storedEvidenceHash != candidateEvidenceHash {
		t.Fatalf("decision_records.evidence_set_hash=%s want %s (the hash actually verified)", storedEvidenceHash, candidateEvidenceHash)
	}
	return run
}

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
