//go:build integration

package executive_test

import (
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/executive"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

// M0 restart/recovery against real PostgreSQL.
//
// The task engine half of this is entirely real: real tasks, real attempts,
// real leases with real opaque tokens, real reconciliation, real role-bound
// execution principals. The model half is the same in-process fake the rest of
// this file uses, because a provider call must never happen in a test -- what
// is being proven here is which decisions the Executive makes about durable
// task state after a process restart, not Model Runtime's own persistence,
// which has its own integration coverage.

// expireLeaseSQL ages a lease into the past. Both timestamps move because the
// table enforces expires_at > issued_at: a lease that expired is one that was
// issued earlier still, not one whose expiry was rewritten behind it.
const expireLeaseSQL = `UPDATE task_leases
	SET issued_at=clock_timestamp()-interval '2 hours',
	    heartbeat_at=clock_timestamp()-interval '2 hours',
	    expires_at=clock_timestamp()-interval '1 hour'
	WHERE id=$1`

type leaseRow struct {
	id        int64
	attemptID int64
	holderID  string
	status    string
}

func readActiveLease(t *testing.T, h *integrationHarness, taskID int64) (leaseRow, bool) {
	t.Helper()
	var row leaseRow
	err := h.store.Pool().QueryRow(h.ctx,
		`SELECT id, attempt_id, holder_id, status FROM task_leases WHERE task_id=$1 ORDER BY id DESC LIMIT 1`,
		taskID).Scan(&row.id, &row.attemptID, &row.holderID, &row.status)
	if err != nil {
		return leaseRow{}, false
	}
	return row, true
}

func readAttempt(t *testing.T, h *integrationHarness, attemptID int64) (string, string) {
	t.Helper()
	var state, workerID string
	if err := h.store.Pool().QueryRow(h.ctx,
		`SELECT state, worker_id FROM task_attempts WHERE id=$1`, attemptID).Scan(&state, &workerID); err != nil {
		t.Fatalf("read attempt %d: %v", attemptID, err)
	}
	return state, workerID
}

func readTaskStatus(t *testing.T, h *integrationHarness, taskID int64) string {
	t.Helper()
	var status string
	if err := h.store.Pool().QueryRow(h.ctx, `SELECT status FROM tasks WHERE id=$1`, taskID).Scan(&status); err != nil {
		t.Fatalf("read task %d: %v", taskID, err)
	}
	return status
}

// leasedChildTask drives a real run until one child task holds a real active
// lease whose execution stopped without a verdict, which is the durable shape a
// crashed Executive leaves behind.
func leasedChildTask(t *testing.T, h *integrationHarness, models *integrationModelRuntime) (int64, int64, leaseRow) {
	t.Helper()
	orchestrator := newOrchestrator(t, h, models, integrationAssignments{}, &countingCompletion{delegate: h.completion})
	run, _, err := orchestrator.Submit(h.ctx, executive.SubmitRequest{
		ActorRoleID: executive.OwnerRoleID, IdempotencyKey: "m0-recovery-" + t.Name(),
		Goal: executive.OwnerGoal{Goal: "Analyze one area.", AcceptanceCriteria: []string{"verified"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Authority that could not be consulted is the one outcome that leaves the
	// attempt running and the lease held, with nothing recorded -- exactly the
	// state a process that died mid-run leaves behind.
	models.stopWithoutVerdict = true
	if _, err = orchestrator.Resume(h.ctx, run.RootTaskID); err == nil {
		t.Fatal("expected the run to stop without a verdict")
	}
	models.stopWithoutVerdict = false

	correlated, err := h.tasks.ListTasks(h.ctx, tasks.TaskFilter{OrganizationID: "explorarte", CorrelationID: run.CorrelationID, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range correlated {
		if task.ID == run.RootTaskID {
			continue
		}
		if lease, ok := readActiveLease(t, h, task.ID); ok && lease.status == "active" {
			return run.RootTaskID, task.ID, lease
		}
	}
	t.Fatal("no child task holds an active lease after the interrupted run")
	return 0, 0, leaseRow{}
}

// TestM0PostgresRestartCannotAdoptActiveLeaseThenReconcilesToFreshAttempt is
// scenario A end to end.
func TestM0PostgresRestartCannotAdoptActiveLeaseThenReconcilesToFreshAttempt(t *testing.T) {
	h := newIntegrationHarness(t)
	defer h.close()
	models := newIntegrationModelRuntime()
	rootID, childID, lease := leasedChildTask(t, h, models)

	// The durable identity split, read from the rows themselves: the lease is
	// held by a canonical numeric execution principal while the attempt records
	// the operational worker name.
	state, workerID := readAttempt(t, h, lease.attemptID)
	if state != "running" {
		t.Fatalf("attempt state=%q want running", state)
	}
	if workerID != "executive-orchestrator" {
		t.Fatalf("attempt worker_id=%q", workerID)
	}
	if lease.holderID == workerID || lease.holderID == "" {
		t.Fatalf("lease holder_id=%q must be a canonical principal, not the worker name", lease.holderID)
	}
	var principalRole, principalStatus, principalOrg string
	if err := h.store.Pool().QueryRow(h.ctx,
		`SELECT dispatch_actor_role_id, status, organization_id FROM model_execution_principals WHERE id=$1::bigint`,
		lease.holderID).Scan(&principalRole, &principalStatus, &principalOrg); err != nil {
		t.Fatalf("lease holder %q is not a canonical execution principal: %v", lease.holderID, err)
	}
	if principalStatus != "active" || principalOrg != "explorarte" {
		t.Fatalf("principal status=%q org=%q", principalStatus, principalOrg)
	}
	firstRunID := models.runs[len(models.runs)-1].RunID
	callsBeforeRestart := models.ensureCalls

	// RESTART: a brand new orchestrator holds no lease tokens at all.
	restarted := newOrchestrator(t, h, models, integrationAssignments{}, &countingCompletion{delegate: h.completion})
	if _, err := restarted.ResumeDurable(h.ctx, rootID); err == nil {
		t.Fatal("a restarted process must not proceed past an active lease it cannot hold")
	}
	if models.ensureCalls != callsBeforeRestart {
		t.Fatalf("provider calls %d -> %d: the restart executed beside an active lease", callsBeforeRestart, models.ensureCalls)
	}
	after, ok := readActiveLease(t, h, childID)
	if !ok || after.id != lease.id || after.status != "active" {
		t.Fatalf("the barrier must leave the durable lease untouched: %+v", after)
	}
	if state, _ := readAttempt(t, h, lease.attemptID); state != "running" {
		t.Fatalf("attempt state=%q after restart: the barrier must not mutate it", state)
	}

	// Expire the lease. This is what the clock does in production; doing it
	// directly keeps the test bounded without weakening what follows, which is
	// the real reconciler acting on a genuinely expired row.
	if _, err := h.store.Pool().Exec(h.ctx,
		expireLeaseSQL, lease.id); err != nil {
		t.Fatal(err)
	}
	if _, err := h.tasks.Reconcile(h.ctx, 100); err != nil {
		t.Fatal(err)
	}
	expiredState, _ := readAttempt(t, h, lease.attemptID)
	if expiredState != "lease_expired" {
		t.Fatalf("attempt state=%q want lease_expired after reconcile", expiredState)
	}
	var leaseStatus, releaseReason string
	if err := h.store.Pool().QueryRow(h.ctx,
		`SELECT status, COALESCE(release_reason,'') FROM task_leases WHERE id=$1`, lease.id).Scan(&leaseStatus, &releaseReason); err != nil {
		t.Fatal(err)
	}
	if leaseStatus != "expired" || releaseReason != "lease_expired" {
		t.Fatalf("lease status=%q release_reason=%q", leaseStatus, releaseReason)
	}
	if status := readTaskStatus(t, h, childID); status != string(tasks.StatusRetryWait) {
		t.Fatalf("task status=%q want retry_wait", status)
	}

	// Readiness promotion is time-gated by the retry policy; bring the clock
	// forward rather than sleeping through it, then let the real reconciler
	// perform the transition.
	if _, err := h.store.Pool().Exec(h.ctx,
		`UPDATE tasks SET available_at=clock_timestamp()-interval '1 second' WHERE id=$1`, childID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.tasks.Reconcile(h.ctx, 100); err != nil {
		t.Fatal(err)
	}
	if status := readTaskStatus(t, h, childID); status != string(tasks.StatusReady) {
		t.Fatalf("task status=%q want ready", status)
	}

	// Only now may a new execution happen, and it must be a new attempt with a
	// new lease and a new run identity.
	if _, err := restarted.ResumeDurable(h.ctx, rootID); err != nil {
		t.Fatalf("resume after reconciliation: %v", err)
	}
	freshLease, ok := readActiveLease(t, h, childID)
	if !ok {
		t.Fatal("no lease after the fresh attempt")
	}
	if freshLease.id == lease.id || freshLease.attemptID == lease.attemptID {
		t.Fatalf("expected a fresh lease and attempt, got lease=%d attempt=%d (was lease=%d attempt=%d)",
			freshLease.id, freshLease.attemptID, lease.id, lease.attemptID)
	}
	if freshLease.holderID != lease.holderID {
		t.Fatalf("the fresh attempt must be held by the same canonical principal: %q vs %q", freshLease.holderID, lease.holderID)
	}
	secondRunID := models.runs[len(models.runs)-1].RunID
	if secondRunID == firstRunID {
		t.Fatalf("a fresh attempt must produce a fresh run identity, got %q twice", secondRunID)
	}
	wantFirst := "executive:explorarte:task:" + itoa(childID) + ":attempt:" + itoa(lease.attemptID) + ":ceo-plan:v1"
	wantSecond := "executive:explorarte:task:" + itoa(childID) + ":attempt:" + itoa(freshLease.attemptID) + ":ceo-plan:v1"
	if firstRunID != wantFirst || secondRunID != wantSecond {
		t.Fatalf("run identities are not deterministic by attempt:\n got  %q / %q\n want %q / %q", firstRunID, secondRunID, wantFirst, wantSecond)
	}
	if models.ensureCalls != callsBeforeRestart+1 {
		t.Fatalf("provider calls=%d want exactly one more after the fresh attempt", models.ensureCalls)
	}
}

// TestM0PostgresRestartWithDurableResultDoesNotCallProviderAgain is scenario B.
func TestM0PostgresRestartWithDurableResultDoesNotCallProviderAgain(t *testing.T) {
	h := newIntegrationHarness(t)
	defer h.close()
	models := newIntegrationModelRuntime()
	rootID, childID, lease := leasedChildTask(t, h, models)

	// The provider answered and Model Runtime persisted the result; the
	// Executive died before it could record the task-level outcome.
	models.seedDurableResult(childID, lease.attemptID, "succeeded")
	callsBefore := models.ensureCalls

	restarted := newOrchestrator(t, h, models, integrationAssignments{}, &countingCompletion{delegate: h.completion})
	if _, err := restarted.ResumeDurable(h.ctx, rootID); err == nil {
		t.Fatal("a restarted process must not proceed past an active lease")
	}
	if models.ensureCalls != callsBefore {
		t.Fatalf("provider calls %d -> %d: a durable result was recomputed", callsBefore, models.ensureCalls)
	}

	// After expiry and reconciliation the durable result is recognised as
	// orphaned rather than being produced a second time.
	if _, err := h.store.Pool().Exec(h.ctx,
		expireLeaseSQL, lease.id); err != nil {
		t.Fatal(err)
	}
	if _, err := h.tasks.Reconcile(h.ctx, 100); err != nil {
		t.Fatal(err)
	}
	_, err := restarted.ResumeDurable(h.ctx, rootID)
	if err == nil {
		t.Fatal("an orphaned durable result must not be silently recomputed")
	}
	if models.ensureCalls != callsBefore {
		t.Fatalf("provider calls %d -> %d after reconciliation", callsBefore, models.ensureCalls)
	}
	if status := readTaskStatus(t, h, rootID); status != "blocked" {
		t.Fatalf("root status=%q want blocked", status)
	}
	var reason string
	if qErr := h.store.Pool().QueryRow(h.ctx, `SELECT COALESCE(status_reason_code,'') FROM tasks WHERE id=$1`, rootID).Scan(&reason); qErr != nil {
		t.Fatal(qErr)
	}
	if reason != "orphaned_model_result" {
		t.Fatalf("root reason=%q want orphaned_model_result (err was %v)", reason, err)
	}
}

// TestM0PostgresRestartWithAmbiguousOutcomeNeverRetries is scenario C.
func TestM0PostgresRestartWithAmbiguousOutcomeNeverRetries(t *testing.T) {
	h := newIntegrationHarness(t)
	defer h.close()
	models := newIntegrationModelRuntime()
	rootID, childID, lease := leasedChildTask(t, h, models)

	models.seedDurableResult(childID, lease.attemptID, "ambiguous")
	callsBefore := models.ensureCalls

	restarted := newOrchestrator(t, h, models, integrationAssignments{}, &countingCompletion{delegate: h.completion})
	for i := 0; i < 3; i++ {
		if _, err := restarted.ResumeDurable(h.ctx, rootID); err == nil {
			t.Fatalf("resume %d: an ambiguous outcome must never resolve itself", i)
		}
	}
	if models.ensureCalls != callsBefore {
		t.Fatalf("provider calls %d -> %d: an ambiguous outcome was retried", callsBefore, models.ensureCalls)
	}
	var status, reason string
	if err := h.store.Pool().QueryRow(h.ctx,
		`SELECT status, COALESCE(status_reason_code,'') FROM tasks WHERE id=$1`, rootID).Scan(&status, &reason); err != nil {
		t.Fatal(err)
	}
	if status != "blocked" || reason != "model_outcome_ambiguous" {
		t.Fatalf("root status=%q reason=%q", status, reason)
	}
}

// TestM0PostgresExecutiveHarnessRunsCarryNoTools is the durable half of the
// tool-less invariant: every productive run the Executive submitted in a real
// end-to-end flow carried no tools and no tool budget.
func TestM0PostgresExecutiveHarnessRunsCarryNoTools(t *testing.T) {
	h := newIntegrationHarness(t)
	defer h.close()
	models := newIntegrationModelRuntime()
	orchestrator := newOrchestrator(t, h, models, integrationAssignments{}, &countingCompletion{delegate: h.completion})
	run, _, err := orchestrator.Submit(h.ctx, executive.SubmitRequest{
		ActorRoleID: executive.OwnerRoleID, IdempotencyKey: "m0-no-tools",
		Goal: executive.OwnerGoal{Goal: "Analyze one area.", AcceptanceCriteria: []string{"verified"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = runUntilTerminalOrError(t, h.ctx, orchestrator, run.RootTaskID, 20); err != nil {
		t.Fatal(err)
	}
	if len(models.runs) == 0 {
		t.Fatal("no harness runs were submitted")
	}
	for _, command := range models.runs {
		if len(command.OutputSchema) == 0 {
			t.Fatalf("run %s has no output contract", command.RunID)
		}
		if !command.Purpose.Valid() {
			t.Fatalf("run %s carries an unknown purpose %q", command.RunID, command.Purpose)
		}
	}
	// The Executive never builds a tool set: HarnessRunCommand has no field
	// that could carry one, and the adapter that turns it into a run spec fixes
	// Tools to nil and MaxToolCalls to zero. Assert the type surface directly so
	// adding such a field to the command has to break this test first.
	harnessCommandCarriesNoToolField(t)
}

func itoa(v int64) string { return strconv.FormatInt(v, 10) }

// harnessCommandCarriesNoToolField proves the Executive's execute-side command
// type has no way to express a tool set. The invariant is structural: if
// nothing in the command can carry tools, no productive Executive run can
// request one, whatever the adapter later does.
func harnessCommandCarriesNoToolField(t *testing.T) {
	t.Helper()
	commandType := reflect.TypeOf(executive.HarnessRunCommand{})
	for i := 0; i < commandType.NumField(); i++ {
		if name := commandType.Field(i).Name; strings.Contains(strings.ToLower(name), "tool") {
			t.Fatalf("HarnessRunCommand.%s could carry a tool set into a productive executive run", name)
		}
	}
}

// TestM0PostgresUnresolvedSendSurvivesTaskReconcileAndNeverDuplicates is the
// case-B adversarial proof, and it is deliberately run in the dangerous order.
//
// Two reconcilers work independently: the Task Engine expires leases and makes
// tasks retryable, Model Runtime classifies expired dispatches. Nothing
// coordinates them. The dangerous interleaving is the one where the task
// becomes eligible for retry FIRST, while the previous attempt's request may
// still be somewhere between this system and the provider. Readiness alone must
// not authorize execution.
func TestM0PostgresUnresolvedSendSurvivesTaskReconcileAndNeverDuplicates(t *testing.T) {
	h := newIntegrationHarness(t)
	defer h.close()
	models := newIntegrationModelRuntime()
	rootID, childID, lease := leasedChildTask(t, h, models)

	// The dead attempt left a request that may already have reached the
	// provider. Model Runtime has not classified it yet.
	invocationID := models.seedUnresolvedSend(childID, lease.attemptID)
	callsBefore := models.ensureCalls

	// TASK RECONCILIATION RUNS FIRST -- before Model Runtime's.
	if _, err := h.store.Pool().Exec(h.ctx, expireLeaseSQL, lease.id); err != nil {
		t.Fatal(err)
	}
	if _, err := h.tasks.Reconcile(h.ctx, 100); err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.Pool().Exec(h.ctx,
		`UPDATE tasks SET available_at=clock_timestamp()-interval '1 second' WHERE id=$1`, childID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.tasks.Reconcile(h.ctx, 100); err != nil {
		t.Fatal(err)
	}
	if status := readTaskStatus(t, h, childID); status != string(tasks.StatusReady) {
		t.Fatalf("task status=%q: the race requires the task to be retryable first", status)
	}

	// The task is ready, the lease is gone, and every task-level obstacle has
	// been removed. The only thing standing between this run and a duplicate
	// provider call is the unresolved invocation.
	restarted := newOrchestrator(t, h, models, integrationAssignments{}, &countingCompletion{delegate: h.completion})
	for i := 0; i < 3; i++ {
		if _, err := restarted.ResumeDurable(h.ctx, rootID); err == nil {
			t.Fatalf("resume %d: a fresh attempt executed beside an unresolved provider call", i)
		}
	}
	if models.ensureCalls != callsBefore {
		t.Fatalf("provider calls %d -> %d: duplicate execution of an unresolved request", callsBefore, models.ensureCalls)
	}
	var attemptCount int
	if err := h.store.Pool().QueryRow(h.ctx, `SELECT count(*) FROM task_attempts WHERE task_id=$1`, childID).Scan(&attemptCount); err != nil {
		t.Fatal(err)
	}
	if attemptCount != 1 {
		t.Fatalf("task_attempts=%d: no fresh attempt may be created while the prior call is unresolved", attemptCount)
	}
	var activeLeases int
	if err := h.store.Pool().QueryRow(h.ctx, `SELECT count(*) FROM task_leases WHERE task_id=$1 AND status='active'`, childID).Scan(&activeLeases); err != nil {
		t.Fatal(err)
	}
	if activeLeases != 0 {
		t.Fatalf("active leases=%d: nothing may be claimed while the prior call is unresolved", activeLeases)
	}

	// NOW Model Runtime reconciles the expired send.
	models.reconcileToAmbiguous(childID, lease.attemptID, invocationID)

	if _, err := restarted.ResumeDurable(h.ctx, rootID); err == nil {
		t.Fatal("an ambiguous outcome must be reported, not resolved")
	}
	var status, reason string
	if err := h.store.Pool().QueryRow(h.ctx,
		`SELECT status, COALESCE(status_reason_code,'') FROM tasks WHERE id=$1`, rootID).Scan(&status, &reason); err != nil {
		t.Fatal(err)
	}
	if status != "blocked" || reason != "model_outcome_ambiguous" {
		t.Fatalf("root status=%q reason=%q want blocked/model_outcome_ambiguous", status, reason)
	}
	if models.ensureCalls != callsBefore {
		t.Fatalf("provider calls=%d want exactly %d for the whole sequence", models.ensureCalls, callsBefore)
	}
	if err := h.store.Pool().QueryRow(h.ctx, `SELECT count(*) FROM task_attempts WHERE task_id=$1`, childID).Scan(&attemptCount); err != nil {
		t.Fatal(err)
	}
	if attemptCount != 1 {
		t.Fatalf("task_attempts=%d after the full hand-off", attemptCount)
	}
}
