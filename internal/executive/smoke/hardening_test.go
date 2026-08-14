//go:build integration

package smoke_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/agentmessaging"
	"github.com/Mireuz13/explorarte-organization/internal/executive/smoke"
)

// requireZeroOpenMessages is the core invariant CUTOVER-REHEARSAL-001's
// second incident violated: any non-PASS Execute result must leave zero
// messages in 'pending' or 'claimed' for the roles this smoke touches.
func requireZeroOpenMessages(t *testing.T, ctx context.Context, h *harness, roles []string) {
	t.Helper()
	var open int
	if err := h.store.Pool().QueryRow(ctx, `
		SELECT count(*) FROM agent_messages
		WHERE organization_id=$1 AND recipient_role_id = ANY($2) AND status IN ('pending','claimed')
	`, smokeTestOrg, roles).Scan(&open); err != nil {
		t.Fatalf("count open messages: %v", err)
	}
	if open != 0 {
		t.Fatalf("expected 0 pending/claimed messages after a non-PASS result, found %d", open)
	}
}

func (h *harness) toolkit(t *testing.T) smoke.Toolkit {
	t.Helper()
	tk, err := smoke.WireToolkit(h.cfg, h.store)
	if err != nil {
		t.Fatalf("wire toolkit: %v", err)
	}
	return tk
}

var smokeAllRoles = []string{smokeCEORole, smokeLeaderRole, smokeWorkerRole}

// 11. Preflight refuses -- and creates nothing -- when a role in the run
// lacks a required capability. transversal_audit (e.g. the RAG auditor
// role) has neither agent.message.send nor agent.message.claim in the
// real canonical matrix, so substituting it for the worker role is a
// faithful, non-synthetic way to reproduce "role cannot do this."
func TestSmokeExecutePreflightRefusesOnMissingCapability(t *testing.T) {
	h := newHarness(t)
	defer h.close()

	roles := smoke.Roles{CEO: smokeCEORole, Leader: smokeLeaderRole, Worker: "investigacion/auditor_cerebro_empresa"}
	tk := h.toolkit(t)

	var tasksBefore, messagesBefore int
	h.store.Pool().QueryRow(h.ctx, `SELECT count(*) FROM tasks`).Scan(&tasksBefore)
	h.store.Pool().QueryRow(h.ctx, `SELECT count(*) FROM agent_messages`).Scan(&messagesBefore)

	report, err := smoke.Execute(h.ctx, h.store.Pool(), tk, roles, time.Now())
	if err == nil || report.Passed {
		t.Fatal("expected Execute to fail when a role lacks required capability")
	}
	if report.Stage != "preflight" {
		t.Fatalf("expected failure at preflight stage, got %q", report.Stage)
	}
	if report.Preflight.AllPassed {
		t.Fatal("expected PreflightReport.AllPassed=false")
	}

	var tasksAfter, messagesAfter int
	h.store.Pool().QueryRow(h.ctx, `SELECT count(*) FROM tasks`).Scan(&tasksAfter)
	h.store.Pool().QueryRow(h.ctx, `SELECT count(*) FROM agent_messages`).Scan(&messagesAfter)
	if tasksAfter != tasksBefore || messagesAfter != messagesBefore {
		t.Fatalf("preflight failure must create nothing: tasks %d->%d messages %d->%d", tasksBefore, tasksAfter, messagesBefore, messagesAfter)
	}
}

// 12. Preflight refuses when the registry is not synchronized -- exactly
// the CUTOVER-REHEARSAL-001 incident: a stale applied revision whose
// canonical_hash no longer matches what's on disk.
func TestSmokeExecutePreflightRefusesOnRegistryDrift(t *testing.T) {
	h := newHarness(t)
	defer h.close()

	// Force drift: mark the applied revision's stored canonical_hash as
	// something that cannot match the loader's freshly-computed hash.
	fakeHash := "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"[:64]
	if _, err := h.store.Pool().Exec(h.ctx, `
		UPDATE organization_registry_revisions
		SET canonical_hash = $1
		WHERE id = (SELECT current_revision_id FROM organizations WHERE id=$2)
	`, fakeHash, smokeTestOrg); err != nil {
		t.Fatalf("force registry drift: %v", err)
	}

	tk := h.toolkit(t)
	report, err := smoke.Execute(h.ctx, h.store.Pool(), tk, h.roles(), time.Now())
	if err == nil || report.Passed {
		t.Fatal("expected Execute to fail when the registry is not synchronized")
	}
	if report.Stage != "preflight" {
		t.Fatalf("expected failure at preflight stage, got %q", report.Stage)
	}
	if report.Preflight.RegistrySynchronized {
		t.Fatal("expected RegistrySynchronized=false")
	}
}

// 13. A Run failure on the very first hop (disabled CEO principal) must
// leave zero open messages after Execute's automatic Cleanup.
func TestSmokeExecuteCleansUpOnFirstHopFailure(t *testing.T) {
	h := newHarness(t)
	defer h.close()

	// Provision the CEO principal via a real clean cycle, then disable it.
	h.runVerifyDeliver(t, smoke.NewCorrelationID(time.Now()))
	ceoPrincipal, err := h.messages.PrincipalStore.ResolveActiveForRole(h.ctx, smokeTestOrg, smokeCEORole)
	if err != nil {
		t.Fatalf("resolve CEO principal: %v", err)
	}
	if _, err := h.messages.PrincipalStore.DisablePrincipal(h.ctx, ceoPrincipal.ID, "empresa/human", "induced_failure_test"); err != nil {
		t.Fatalf("disable CEO principal: %v", err)
	}

	tk := h.toolkit(t)
	report, err := smoke.Execute(h.ctx, h.store.Pool(), tk, h.roles(), time.Now())
	if err == nil || report.Passed {
		t.Fatal("expected Execute to fail with the CEO principal disabled")
	}
	if report.Stage != "run" {
		t.Fatalf("expected failure at run stage, got %q", report.Stage)
	}
	if report.Cleanup == nil {
		t.Fatal("expected a Cleanup report after a run-stage failure")
	}
	requireZeroOpenMessages(t, h.ctx, h, smokeAllRoles)
}

// 14. A Run failure on a LATER hop (worker principal disabled) leaves two
// messages behind: ceo->leader (hop 1, recipient=leader, unaffected by the
// disabled worker) and leader->worker (hop 2, recipient=worker -- Send
// only validates the SENDER's principal, so this send itself still
// succeeds even though the recipient's principal is disabled). Cleanup
// can legitimately resolve the first (leader's principal is fine) but
// cannot resolve the second: ClaimNext requires an ACTIVE principal for
// the recipient role, and there isn't one. This is not a Cleanup bug --
// there is no real API path to claim on behalf of a disabled principal,
// by design. Cleanup must report this honestly via Unresolved rather than
// claiming false success, which is exactly what this test verifies.
func TestSmokeExecuteCleansUpOnLaterHopFailure(t *testing.T) {
	h := newHarness(t)
	defer h.close()

	h.runVerifyDeliver(t, smoke.NewCorrelationID(time.Now()))
	workerPrincipal, err := h.messages.PrincipalStore.ResolveActiveForRole(h.ctx, smokeTestOrg, smokeWorkerRole)
	if err != nil {
		t.Fatalf("resolve worker principal: %v", err)
	}
	if _, err := h.messages.PrincipalStore.DisablePrincipal(h.ctx, workerPrincipal.ID, "empresa/human", "induced_failure_test"); err != nil {
		t.Fatalf("disable worker principal: %v", err)
	}

	tk := h.toolkit(t)
	report, err := smoke.Execute(h.ctx, h.store.Pool(), tk, h.roles(), time.Now())
	if err == nil || report.Passed {
		t.Fatal("expected Execute to fail with the worker principal disabled")
	}
	if report.Stage != "run" {
		t.Fatalf("expected failure at run stage, got %q", report.Stage)
	}
	if report.Cleanup == nil {
		t.Fatal("expected a Cleanup report")
	}
	if report.Cleanup.MessagesFound != 2 {
		t.Fatalf("expected cleanup to find 2 messages (ceo->leader, leader->worker), found %d", report.Cleanup.MessagesFound)
	}
	if report.Cleanup.DeadenedCount != 1 {
		t.Fatalf("expected exactly 1 message deadened (the leader-recipient one), got %d", report.Cleanup.DeadenedCount)
	}
	if len(report.Cleanup.Unresolved) != 1 {
		t.Fatalf("expected exactly 1 unresolved message (the worker-recipient one, disabled principal), got %d: %v", len(report.Cleanup.Unresolved), report.Cleanup.Unresolved)
	}

	// The leader-recipient message must be gone from pending/claimed.
	var leaderRecipientOpen int
	h.store.Pool().QueryRow(h.ctx, `SELECT count(*) FROM agent_messages WHERE organization_id=$1 AND recipient_role_id=$2 AND status IN ('pending','claimed')`, smokeTestOrg, smokeLeaderRole).Scan(&leaderRecipientOpen)
	if leaderRecipientOpen != 0 {
		t.Fatalf("expected 0 open messages for the leader recipient, got %d", leaderRecipientOpen)
	}
	// The worker-recipient message legitimately cannot be resolved while
	// its principal stays disabled -- confirm it is still there, not
	// silently dropped or force-resolved.
	var workerRecipientOpen int
	h.store.Pool().QueryRow(h.ctx, `SELECT count(*) FROM agent_messages WHERE organization_id=$1 AND recipient_role_id=$2 AND status IN ('pending','claimed')`, smokeTestOrg, smokeWorkerRole).Scan(&workerRecipientOpen)
	if workerRecipientOpen != 1 {
		t.Fatalf("expected exactly 1 open (unresolvable) message for the disabled worker recipient, got %d", workerRecipientOpen)
	}
}

// 15. A Verify failure (data tampered between Run and Verify) must also
// trigger Cleanup down to zero open messages. Tampering here is a
// test-only simulation of "something is wrong with what got persisted" --
// never a pattern used by the production code path itself.
func TestSmokeExecuteCleansUpOnVerifyFailure(t *testing.T) {
	h := newHarness(t)
	defer h.close()

	// MaxAttempts pinned to 1, matching what Execute does internally --
	// calling Run directly (bypassing Execute) must replicate that so a
	// single cleanup Nack actually deadens each message.
	smokeMessages := h.messages
	smokeMessages.MaxAttempts = 1

	correlationID := smoke.NewCorrelationID(time.Now())
	result, err := smoke.Run(h.ctx, h.store.Pool(), smokeMessages, smokeTestOrg, h.roles(), correlationID, time.Now())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	// Simulate corruption: break the identity invariant Verify checks.
	if _, err := h.store.Pool().Exec(h.ctx, `UPDATE agent_messages SET sender_role_id='tampered/role' WHERE correlation_id=$1 AND id=(SELECT min(id) FROM agent_messages WHERE correlation_id=$1)`, correlationID); err != nil {
		t.Fatalf("tamper message for test: %v", err)
	}

	verification, err := smoke.Verify(h.ctx, h.store.Pool(), smokeTestOrg, correlationID)
	if err == nil && verification.AllIdentical {
		t.Fatal("expected tampering to break AllIdentical")
	}

	cleanup, err := smoke.Cleanup(h.ctx, h.store.Pool(), smokeMessages, smokeTestOrg, correlationID, time.Now())
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if cleanup.MessagesFound != 4 {
		t.Fatalf("expected cleanup to find all 4 messages, found %d", cleanup.MessagesFound)
	}
	requireZeroOpenMessages(t, h.ctx, h, smokeAllRoles)
	_ = result
}

// 16. A Deliver-stage failure (foreign inbox traffic present) must also
// resolve via Cleanup to zero open messages for this run's own messages
// specifically (the foreign message is deliberately left untouched --
// Cleanup must never resolve traffic it does not own, same rule Deliver
// itself follows).
func TestSmokeExecuteCleansUpOnDeliverFailure(t *testing.T) {
	h := newHarness(t)
	defer h.close()

	foreignCorrelation := "genuine-traffic-blocking-deliver"
	h.runVerifyDeliver(t, foreignCorrelation) // delivered and gone from pending -- not the blocker itself

	smokeMessages := h.messages
	smokeMessages.MaxAttempts = 1

	correlationID := smoke.NewCorrelationID(time.Now().Add(time.Second))
	result, err := smoke.Run(h.ctx, h.store.Pool(), smokeMessages, smokeTestOrg, h.roles(), correlationID, time.Now())
	if err != nil {
		t.Fatalf("run under test: %v", err)
	}
	verification, err := smoke.Verify(h.ctx, h.store.Pool(), smokeTestOrg, correlationID)
	if err != nil || !verification.AllFourPresent || !verification.AllCorrelated || !verification.AllIdentical || !verification.SupportTasksSafe {
		t.Fatalf("run under test did not verify cleanly: err=%v report=%+v", err, verification)
	}

	// Inject foreign traffic directly, bypassing Run's own precheck.
	ceoPrincipal, err := h.messages.PrincipalStore.ResolveActiveForRole(h.ctx, smokeTestOrg, smokeCEORole)
	if err != nil {
		t.Fatalf("resolve CEO principal: %v", err)
	}
	recipientTaskID := result.LeaderTask.ID
	injected := agentmessaging.SendCommand{
		OrganizationID: smokeTestOrg, SenderRoleID: smokeCEORole, SenderTaskID: result.CEOTask.ID,
		RecipientRoleID: smokeLeaderRole, RecipientTaskID: &recipientTaskID,
		CorrelationID: "genuine-traffic-injected-for-deliver-cleanup-test", CausationID: "genuine-traffic-injected-for-deliver-cleanup-test",
		MessageType: agentmessaging.MessageDelegation, Payload: []byte(`{"delegated_task_id":` + strconv.FormatInt(recipientTaskID, 10) + `}`),
		IdempotencyKey: "genuine-traffic-injected-for-deliver-cleanup-test", MaxAttempts: 1, SchemaVersion: agentmessaging.SchemaVersionV1,
	}
	if _, _, sendErr := h.messages.Ledger.Send(h.ctx, strconv.FormatInt(ceoPrincipal.ID, 10), injected, time.Now()); sendErr != nil {
		t.Fatalf("inject foreign traffic: %v", sendErr)
	}

	deliverReport, deliverErr := smoke.Deliver(h.ctx, h.store.Pool(), smokeMessages, smokeTestOrg, h.roles(), correlationID, time.Now())
	if deliverErr == nil {
		t.Fatal("expected Deliver to fail with foreign traffic present")
	}
	_ = deliverReport

	cleanup, err := smoke.Cleanup(h.ctx, h.store.Pool(), smokeMessages, smokeTestOrg, correlationID, time.Now())
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if cleanup.MessagesFound != 4 {
		t.Fatalf("expected cleanup to find this run's 4 messages, found %d", cleanup.MessagesFound)
	}

	var thisRunOpen int
	h.store.Pool().QueryRow(h.ctx, `SELECT count(*) FROM agent_messages WHERE correlation_id=$1 AND status IN ('pending','claimed')`, correlationID).Scan(&thisRunOpen)
	if thisRunOpen != 0 {
		t.Fatalf("expected 0 open messages for this run after cleanup, got %d", thisRunOpen)
	}
}
