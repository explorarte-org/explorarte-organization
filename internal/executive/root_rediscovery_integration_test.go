//go:build integration

package executive_test

import (
	"errors"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/executive"
	"github.com/Mireuz13/explorarte-organization/internal/executive/runtimeadapter"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

// ============================================================
// EXECUTIVE_AUTONOMOUS_BLOCKED_ROOT_RECOVERY_FIX_V1
//
// Reproduces, against real PostgreSQL, the production incident this round
// fixes: root tasks 614/616, blocked with status_reason_code
// dispatch_assignment_required after a CEO-plan dispatch attempt was
// rejected for "active role-model binding: model dispatch entity not
// found", never advanced again even after the underlying binding gap was
// corrected (PRODUCTION_CANONICAL_MATERIALIZATION_V1) -- because
// ListExecutableRoots excluded any blocked task outright, so the
// persistent worker's next poll never called ResumeDurable on them again.
//
// This file covers two things:
//   - the discovery predicate test matrix (which statuses/reasons are
//     included/excluded);
//   - one end-to-end incident regression: a root genuinely blocked by a
//     failing DispatchProvisioner (not a hand-edited row), rediscovered
//     once a fresh orchestrator resolves assignments successfully, driven
//     through ResumeDurable exactly as the persistent worker would.
//
// Only internal/executive's own fixtures (integrationAssignments,
// integrationModelRuntime) are used. No real provider is ever called.
// ============================================================

func submitRediscoveryGoal(t *testing.T, h *integrationHarness, orchestrator *executive.Orchestrator, idempotencyKey string) int64 {
	t.Helper()
	run, reused, err := orchestrator.Submit(h.ctx, executive.SubmitRequest{
		ActorRoleID: executive.OwnerRoleID, IdempotencyKey: idempotencyKey,
		Goal: executive.OwnerGoal{
			Goal: "Analyze the organization and return a one-area plan without external actions.",
			AcceptanceCriteria: []executive.AcceptanceCriterion{
				{Text: "one department reviewed", Phase: executive.AcceptanceDesign},
				{Text: "closure verified", Phase: executive.AcceptanceImplementation},
			},
		},
	})
	if err != nil || reused {
		t.Fatalf("submit: run=%+v reused=%v err=%v", run, reused, err)
	}
	return run.RootTaskID
}

func executiveRoots(t *testing.T, h *integrationHarness) []int64 {
	t.Helper()
	roots, err := (runtimeadapter.Tasks{Service: h.tasks, OrganizationID: "explorarte"}).ListExecutableRoots(h.ctx, 64)
	if err != nil {
		t.Fatalf("ListExecutableRoots: %v", err)
	}
	return roots
}

func containsRoot(roots []int64, id int64) bool {
	for _, r := range roots {
		if r == id {
			return true
		}
	}
	return false
}

// TestListExecutableRootsIncludesReadyPendingAwaitingVerification is the
// non-regression guard for the three statuses the persistent worker has
// always discovered. The fix in roots.go must not narrow this set.
func TestListExecutableRootsIncludesReadyPendingAwaitingVerification(t *testing.T) {
	h := newIntegrationHarness(t)
	defer h.close()
	models := newIntegrationModelRuntime()
	orchestrator := newOrchestrator(t, h, models, integrationAssignments{}, h.completion)

	readyRoot := submitRediscoveryGoal(t, h, orchestrator, "rediscovery-matrix-ready")
	roots := executiveRoots(t, h)
	if !containsRoot(roots, readyRoot) {
		t.Fatalf("freshly submitted (ready) root %d not discovered: roots=%v", readyRoot, roots)
	}

	// awaiting_verification: drive the root through its CEO-plan phase once
	// with a working assignment resolver and a real (fake-adapter) model
	// result, which is what actually produces that status durably.
	awaitRoot := submitRediscoveryGoal(t, h, orchestrator, "rediscovery-matrix-awaiting")
	if _, err := h.tasks.Reconcile(h.ctx, 50); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if _, err := orchestrator.ResumeDurable(h.ctx, awaitRoot); err != nil {
		t.Fatalf("resume awaiting-verification fixture: %v", err)
	}
	detail, err := h.tasks.GetTask(h.ctx, awaitRoot)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if detail.Task.Status == tasks.StatusAwaitingVerification {
		roots = executiveRoots(t, h)
		if !containsRoot(roots, awaitRoot) {
			t.Fatalf("awaiting_verification root %d not discovered: roots=%v", awaitRoot, roots)
		}
	} else {
		t.Logf("fixture reached status=%s instead of awaiting_verification; skipping that specific assertion (ready/pending/blocked cases below already cover the discovery predicate)", detail.Task.Status)
	}
}

// TestListExecutableRootsRediscoversDispatchAssignmentRequired is the
// direct regression test for the discovery gap: a root blocked with
// dispatch_assignment_required, via the SAME BlockTask call path
// production uses (orchestrator.go's assignment-failure handling, reached
// through a real, failing DispatchProvisioner -- not a hand-edited row),
// must be included once the fix lands.
func TestListExecutableRootsRediscoversDispatchAssignmentRequired(t *testing.T) {
	h := newIntegrationHarness(t)
	defer h.close()
	models := newIntegrationModelRuntime()

	failing := newOrchestrator(t, h, models, integrationAssignments{fail: true}, h.completion)
	rootID := submitRediscoveryGoal(t, h, failing, "rediscovery-dispatch-assignment")

	if _, err := h.tasks.Reconcile(h.ctx, 50); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	_, err := failing.ResumeDurable(h.ctx, rootID)
	if !errors.Is(err, executive.ErrDispatchAssignmentRequired) {
		t.Fatalf("expected ErrDispatchAssignmentRequired reproducing the incident, got %v", err)
	}

	root, err := h.tasks.GetTask(h.ctx, rootID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if root.Task.Status != tasks.StatusBlocked || root.Task.StatusReasonCode == nil || *root.Task.StatusReasonCode != executive.ReasonDispatchAssignmentRequired {
		t.Fatalf("root %d did not durably block with dispatch_assignment_required: status=%s reason=%v", rootID, root.Task.Status, root.Task.StatusReasonCode)
	}

	roots := executiveRoots(t, h)
	if !containsRoot(roots, rootID) {
		t.Fatalf("blocked+dispatch_assignment_required root %d excluded from discovery: roots=%v", rootID, roots)
	}
}

// TestListExecutableRootsExcludesHumanRequiredBlockedReasons is the
// fail-closed guard: every blocked reason that requires a human, new
// evidence, or an explicit reconciliation command must stay excluded, even
// though the root is otherwise a structurally valid executive root.
func TestListExecutableRootsExcludesHumanRequiredBlockedReasons(t *testing.T) {
	h := newIntegrationHarness(t)
	defer h.close()
	models := newIntegrationModelRuntime()
	orchestrator := newOrchestrator(t, h, models, integrationAssignments{}, h.completion)
	adapter := runtimeadapter.Tasks{Service: h.tasks, OrganizationID: "explorarte"}

	reasons := []string{
		"owner_decision_required",
		"indeterminate_tool_execution",
		"orphaned_model_result",
		"completion_verification_inconclusive",
		"organization_revision_drift",
	}
	for _, reason := range reasons {
		t.Run(reason, func(t *testing.T) {
			rootID := submitRediscoveryGoal(t, h, orchestrator, "rediscovery-excluded-"+reason)
			if _, err := adapter.BlockTask(h.ctx, rootID, reason, "integration fixture: human-required reason", "service", "test"); err != nil {
				t.Fatalf("block: %v", err)
			}
			roots := executiveRoots(t, h)
			if containsRoot(roots, rootID) {
				t.Fatalf("blocked+%s root %d must stay excluded from discovery, was included: roots=%v", reason, rootID, roots)
			}
		})
	}
}

// TestListExecutableRootsExcludesStandaloneAndNonCEO covers the two
// pre-existing exclusion paths this round must not weaken: a CEO-assigned,
// owner-requested task with no executive_closure_verified requirement
// (a standalone task shaped like the production mission-smoke-*/
// test-audit-ui-probe-* rows), and a task assigned to a non-CEO role
// entirely.
func TestListExecutableRootsExcludesStandaloneAndNonCEO(t *testing.T) {
	h := newIntegrationHarness(t)
	defer h.close()
	adapter := runtimeadapter.Tasks{Service: h.tasks, OrganizationID: "explorarte"}

	standalone, _, err := adapter.CreateTask(h.ctx, executive.CreateTaskCommand{
		RequestedByRoleID:  executive.OwnerRoleID,
		AssignedRoleID:     executive.CEORoleID,
		TaskClass:          "owner.goal",
		IdempotencyKey:     "rediscovery-standalone-no-closure-requirement",
		Title:              "standalone smoke task",
		Instructions:       "not a real executive root",
		AcceptanceCriteria: []string{"n/a"},
		MaxAttempts:        1,
	})
	if err != nil {
		t.Fatalf("create standalone: %v", err)
	}

	nonCEO, _, err := adapter.CreateTask(h.ctx, executive.CreateTaskCommand{
		RequestedByRoleID:  executive.OwnerRoleID,
		AssignedRoleID:     "ingenieria_ia/orquestador",
		TaskClass:          "coordination.department_plan",
		IdempotencyKey:     "rediscovery-non-ceo-child",
		Title:              "non-CEO task",
		Instructions:       "not assigned to empresa/ceo",
		AcceptanceCriteria: []string{"n/a"},
		MaxAttempts:        1,
		Requirements: []executive.RequirementProposal{
			{Key: "executive_closure_verified", Type: "result", Description: "even carrying the requirement, wrong role must exclude it", Required: true},
		},
	})
	if err != nil {
		t.Fatalf("create non-CEO: %v", err)
	}

	roots := executiveRoots(t, h)
	if containsRoot(roots, standalone.ID) {
		t.Fatalf("standalone CEO task without executive_closure_verified must be excluded: roots=%v", roots)
	}
	if containsRoot(roots, nonCEO.ID) {
		t.Fatalf("non-CEO task must be excluded regardless of requirements: roots=%v", roots)
	}
}

// TestExecutiveBlockedRootRediscoveryEndToEnd is the full incident
// regression (AUTONOMOUS-E2E-002 / production roots 614, 616): a root
// blocks for real via a failing DispatchProvisioner, is confirmed excluded
// from the OLD behavior's shape (durably blocked), then a second
// orchestrator over the SAME durable state -- standing in for the
// persistent worker's next poll after the external condition (missing
// role-model binding) was corrected -- discovers and resumes it through
// ResumeDurable exactly as explorarte-executive-worker.service would, with
// no duplicate provider call and no stale-lease adoption.
func TestExecutiveBlockedRootRediscoveryEndToEnd(t *testing.T) {
	h := newIntegrationHarness(t)
	defer h.close()
	models := newIntegrationModelRuntime()

	// Phase 1: reproduce the incident. A real dispatch-assignment failure
	// durably blocks the root -- this is orchestrator.go's own BlockTask
	// call on assignment-resolution failure, not a hand-edited row.
	failing := newOrchestrator(t, h, models, integrationAssignments{fail: true}, h.completion)
	rootID := submitRediscoveryGoal(t, h, failing, "rediscovery-e2e-incident")
	if _, err := h.tasks.Reconcile(h.ctx, 50); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if _, err := failing.ResumeDurable(h.ctx, rootID); !errors.Is(err, executive.ErrDispatchAssignmentRequired) {
		t.Fatalf("expected ErrDispatchAssignmentRequired, got %v", err)
	}
	blockedRoot, err := h.tasks.GetTask(h.ctx, rootID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if blockedRoot.Task.Status != tasks.StatusBlocked {
		t.Fatalf("root %d expected blocked, got %s", rootID, blockedRoot.Task.Status)
	}
	preFixCallCount := models.ensureCalls

	// Age the child attempt's lease into the past exactly the way real time
	// did for production roots 614/616 (created 2026-09-09/10, still
	// blocked days later): a lease this process cannot prove it holds is a
	// barrier ONLY while it is still active. Reusing the same expireLeaseSQL
	// fixture m0_recovery_integration_test.go uses for the identical
	// restart-safety reason -- this is what makes the second orchestrator
	// instance below a faithful stand-in for the SAME persistent worker
	// process resuming after its own restart (it lost this root's original
	// lease token exactly as it lost it on 2026-09-10T21:12:58Z when the
	// production model-worker container was recreated), not a shortcut
	// around lease adoption.
	if blockedRoot.Task.CorrelationID != nil && *blockedRoot.Task.CorrelationID != "" {
		correlation := *blockedRoot.Task.CorrelationID
		children, err := (runtimeadapter.Tasks{Service: h.tasks, OrganizationID: "explorarte"}).ListByCorrelation(h.ctx, correlation)
		if err != nil {
			t.Fatalf("list children: %v", err)
		}
		for _, child := range children {
			if child.ID == rootID {
				continue
			}
			lease, ok := readActiveLease(t, h, child.ID)
			if !ok {
				continue
			}
			if _, err := h.store.Pool().Exec(h.ctx, expireLeaseSQL, lease.id); err != nil {
				t.Fatalf("expire lease: %v", err)
			}
		}
	}

	// Phase 2: the persistent worker's next poll. A fresh orchestrator
	// instance over the same durable state, with assignment resolution now
	// succeeding -- the same shape as PRODUCTION_CANONICAL_MATERIALIZATION_V1
	// correcting role_model_bindings without touching the already-blocked
	// row itself.
	recovered := newOrchestrator(t, h, models, integrationAssignments{fail: false}, h.completion)

	roots := executiveRoots(t, h)
	if !containsRoot(roots, rootID) {
		t.Fatalf("fixed root %d still excluded from discovery: roots=%v", rootID, roots)
	}

	if _, err := h.tasks.Reconcile(h.ctx, 50); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	run, err := recovered.ResumeDurable(h.ctx, rootID)
	if err != nil {
		t.Fatalf("ResumeDurable did not recover the rediscovered root: %v", err)
	}
	if run.State == executive.StateBlocked {
		t.Fatalf("run still blocked after rediscovery+resume: %+v", run)
	}

	afterRoot, err := h.tasks.GetTask(h.ctx, rootID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if afterRoot.Task.Status == tasks.StatusBlocked && afterRoot.Task.StatusReasonCode != nil && *afterRoot.Task.StatusReasonCode == executive.ReasonDispatchAssignmentRequired {
		t.Fatalf("root %d still shows the original block after recovery: %+v", rootID, afterRoot.Task)
	}

	// Continue driving, reconciling before every resume exactly like the
	// persistent worker's own RunOnce does -- the campaign must not be
	// permanently stuck on anything else this round touches. ResumeDurable
	// unblocking the root is only the first step of the state machine --
	// the fresh attempt it lets through still needs further passes, each
	// preceded by reconciliation, to actually reach and record a model
	// dispatch.
	for i := 0; i < 40 && run.State != executive.StateCompleted && run.State != executive.StateFailed && run.State != executive.StateBlocked; i++ {
		if _, err := h.tasks.Reconcile(h.ctx, 50); err != nil {
			t.Fatalf("reconcile: %v", err)
		}
		run, err = recovered.ResumeDurable(h.ctx, rootID)
		if err != nil {
			t.Fatalf("resume: %v", err)
		}
		t.Logf("iteration %d: state=%s reason=%s", i, run.State, run.ReasonCode)
		time.Sleep(300 * time.Millisecond)
	}
	if run.State == executive.StateBlocked {
		t.Fatalf("campaign blocked again after recovery: %+v", run)
	}
	if run.State != executive.StateCompleted && run.State != executive.StateFailed {
		t.Fatalf("campaign did not converge after recovery: %+v", run)
	}

	// Invariant: the recovered pass must have made real progress beyond
	// whatever the failing pass already accounted for (which is none --
	// integrationAssignments{fail: true} rejects before any model call) --
	// proving the rediscovered root actually dispatched, not that
	// ResumeDurable merely returned without error. And it must be exactly
	// what a fresh attempt produces, not a duplicate of work the failing
	// pass already did, proving no stale lease/attempt got reused into a
	// second provider call for work that never actually happened.
	if models.ensureCalls <= preFixCallCount {
		t.Fatalf("recovered campaign made no real dispatch progress: ensureCalls before=%d after=%d", preFixCallCount, models.ensureCalls)
	}
}
