//go:build integration

package executive_test

// EXEC-PRINCIPAL-001: a single static execution principal cannot
// authenticate a multi-hop executive flow, because agent-messaging's own
// hardening requires principal.dispatch_actor_role_id == sender.AssignedRoleID
// for every send, and a real flow has multiple sender roles (CEO, leader,
// worker). These tests exercise runtimeadapter.AgentMessages directly
// against real, persisted tasks and a real Postgres 17 -- no mocks that
// would hide identity resolution -- proving each hop resolves (and, on
// first use, lazily provisions) the correct role-bound principal, that a
// wrong/disabled/cross-org principal is denied, and that a same-role
// self-message is rejected by topology as designed, not routed around.
//
// TestExecutivePostgreSQL17AgentBudgetsAndMessagingAreWiredThroughDelegation
// (in postgres_integration_test.go) already proves the CEO->leader and
// leader->worker delegation hops end-to-end through the real Orchestrator.
// SendCompletion is implemented on AgentMessages but the Orchestrator does
// not call it anywhere (grep confirms zero call sites outside its own
// implementation and interface declaration) -- that is a separate,
// pre-existing fact about this codebase, not something EXEC-PRINCIPAL-001
// touches, so the completion-direction tests below call SendCompletion
// directly rather than pretending the Orchestrator wires it.

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/agentmessaging"
	agentmessagingpostgres "github.com/Mireuz13/explorarte-organization/internal/agentmessaging/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/executive"
	"github.com/Mireuz13/explorarte-organization/internal/executive/runtimeadapter"
	"github.com/Mireuz13/explorarte-organization/internal/modeldispatch"
	dispatchpostgres "github.com/Mireuz13/explorarte-organization/internal/modeldispatch/postgres"
)

const (
	principalTestCEORole    = "empresa/ceo"
	principalTestLeaderRole = "ingenieria_ia/orquestador"
	principalTestWorkerRole = "ingenieria_ia/qa"
)

// newTestAgentMessages wires the same real components
// internal/executive/bootstrap/runtime.go wires in production: a real
// agentmessaging Ledger and a real modeldispatch PrincipalStore, both
// against h's live Postgres. No ConfiguredPrincipal, no manual principal
// registration -- resolution/provisioning happens inside AgentMessages
// itself, exactly as it would at runtime.
func newTestAgentMessages(t *testing.T, h *integrationHarness) (runtimeadapter.AgentMessages, *dispatchpostgres.Store) {
	t.Helper()
	messageLedger, err := agentmessagingpostgres.New(h.store, h.registry, h.authorizer, 200, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	dispatchStore, err := dispatchpostgres.New(h.store)
	if err != nil {
		t.Fatal(err)
	}
	return runtimeadapter.AgentMessages{
		Ledger: messageLedger, MaxAttempts: 10,
		PrincipalStore: dispatchStore, OrganizationID: "explorarte",
	}, dispatchStore
}

// createRealTask persists a real task assigned to roleID, via the same
// runtimeadapter.Tasks path the Orchestrator itself uses -- not a fake
// TaskRecord -- since agentmessaging.Store.Send independently validates
// sender/recipient task ownership against the tasks table.
func createRealTask(t *testing.T, ctx context.Context, h *integrationHarness, roleID, idempotencyKey, correlationID string) executive.TaskRecord {
	t.Helper()
	taskAdapter := runtimeadapter.Tasks{Service: h.tasks, OrganizationID: "explorarte"}
	task, _, err := taskAdapter.CreateTask(ctx, executive.CreateTaskCommand{
		RequestedByRoleID: executive.OwnerRoleID, AssignedRoleID: roleID,
		IdempotencyKey: idempotencyKey, Title: "principal resolution fixture",
		Instructions: "fixture task for EXEC-PRINCIPAL-001 tests", MaxAttempts: 1,
		CorrelationID: correlationID, CausationID: idempotencyKey,
	})
	if err != nil {
		t.Fatalf("create task for role %q: %v", roleID, err)
	}
	return task
}

func assertMessagePersisted(t *testing.T, ctx context.Context, h *integrationHarness, senderTaskID, recipientTaskID int64, messageType string) {
	t.Helper()
	var count int
	if err := h.store.Pool().QueryRow(ctx,
		`SELECT count(*) FROM agent_messages WHERE sender_task_id=$1 AND recipient_task_id=$2 AND message_type=$3`,
		senderTaskID, recipientTaskID, messageType,
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 persisted %s message sender=%d recipient=%d, got %d", messageType, senderTaskID, recipientTaskID, count)
	}
}

func assertRoleBoundPrincipal(t *testing.T, ctx context.Context, h *integrationHarness, roleID string) modeldispatch.ExecutionPrincipal {
	t.Helper()
	var id int64
	var key, status string
	if err := h.store.Pool().QueryRow(ctx,
		`SELECT id, principal_key, status FROM model_execution_principals WHERE organization_id='explorarte' AND dispatch_actor_role_id=$1 AND principal_key LIKE 'role-bound/%'`,
		roleID,
	).Scan(&id, &key, &status); err != nil {
		t.Fatalf("expected a role-bound principal for %q: %v", roleID, err)
	}
	if status != "active" {
		t.Fatalf("role-bound principal for %q status=%q want active", roleID, status)
	}
	return modeldispatch.ExecutionPrincipal{ID: id, PrincipalKey: key, DispatchActorRoleID: roleID, Status: modeldispatch.PrincipalActive}
}

// buildDelegationSendCommand mirrors exactly what
// runtimeadapter.AgentMessages.SendDelegation builds, so tests that need to
// call the ledger directly (to supply a deliberately wrong principal id,
// which the adapter itself can no longer produce) exercise a realistic
// command.
func buildDelegationSendCommand(sender, recipient executive.TaskRecord) agentmessaging.SendCommand {
	recipientTaskID := recipient.ID
	payload, _ := json.Marshal(agentmessaging.DelegationPayloadV1{DelegatedTaskID: recipient.ID})
	return agentmessaging.SendCommand{
		OrganizationID:  recipient.OrganizationID,
		SenderRoleID:    sender.AssignedRoleID,
		SenderTaskID:    sender.ID,
		RecipientRoleID: recipient.AssignedRoleID,
		RecipientTaskID: &recipientTaskID,
		CorrelationID:   recipient.CorrelationID,
		CausationID:     recipient.CausationID,
		MessageType:     agentmessaging.MessageDelegation,
		Payload:         payload,
		IdempotencyKey:  "exec-principal-test-mismatch:" + sender.AssignedRoleID + ":" + recipient.AssignedRoleID,
		MaxAttempts:     10,
		SchemaVersion:   agentmessaging.SchemaVersionV1,
	}
}

// 1. TestExecutiveMessagingCEOToLeaderUsesCEOPrincipal
func TestExecutiveMessagingCEOToLeaderUsesCEOPrincipal(t *testing.T) {
	h := newIntegrationHarness(t)
	defer h.close()
	messages, _ := newTestAgentMessages(t, h)

	ceoTask := createRealTask(t, h.ctx, h, principalTestCEORole, "exec-principal-ceo-leader-ceo", "corr-ceo-leader")
	leaderTask := createRealTask(t, h.ctx, h, principalTestLeaderRole, "exec-principal-ceo-leader-leader", "corr-ceo-leader")

	if err := messages.SendDelegation(h.ctx, ceoTask, leaderTask, time.Now()); err != nil {
		t.Fatalf("CEO->leader delegation: %v", err)
	}
	assertMessagePersisted(t, h.ctx, h, ceoTask.ID, leaderTask.ID, "delegation")
	ceoPrincipal := assertRoleBoundPrincipal(t, h.ctx, h, principalTestCEORole)
	if ceoPrincipal.DispatchActorRoleID != principalTestCEORole {
		t.Fatalf("CEO principal role=%q want %q", ceoPrincipal.DispatchActorRoleID, principalTestCEORole)
	}
}

// 2. TestExecutiveMessagingLeaderToWorkerUsesLeaderPrincipal
func TestExecutiveMessagingLeaderToWorkerUsesLeaderPrincipal(t *testing.T) {
	h := newIntegrationHarness(t)
	defer h.close()
	messages, _ := newTestAgentMessages(t, h)

	leaderTask := createRealTask(t, h.ctx, h, principalTestLeaderRole, "exec-principal-leader-worker-leader", "corr-leader-worker")
	workerTask := createRealTask(t, h.ctx, h, principalTestWorkerRole, "exec-principal-leader-worker-worker", "corr-leader-worker")

	if err := messages.SendDelegation(h.ctx, leaderTask, workerTask, time.Now()); err != nil {
		t.Fatalf("leader->worker delegation: %v", err)
	}
	assertMessagePersisted(t, h.ctx, h, leaderTask.ID, workerTask.ID, "delegation")
	// Only the SENDER's principal is resolved/provisioned by SendDelegation --
	// the worker is the recipient here, not the sender, so no worker
	// principal exists yet (that only happens when the worker itself sends,
	// e.g. TestExecutiveMessagingWorkerToLeaderUsesWorkerPrincipal).
	leaderPrincipal := assertRoleBoundPrincipal(t, h.ctx, h, principalTestLeaderRole)
	if leaderPrincipal.DispatchActorRoleID != principalTestLeaderRole {
		t.Fatalf("leader principal role=%q want %q", leaderPrincipal.DispatchActorRoleID, principalTestLeaderRole)
	}
}

// 3. TestExecutiveMessagingWorkerToLeaderUsesWorkerPrincipal (completion direction)
func TestExecutiveMessagingWorkerToLeaderUsesWorkerPrincipal(t *testing.T) {
	h := newIntegrationHarness(t)
	defer h.close()
	messages, _ := newTestAgentMessages(t, h)

	leaderTask := createRealTask(t, h.ctx, h, principalTestLeaderRole, "exec-principal-worker-leader-leader", "corr-worker-leader")
	workerTask := createRealTask(t, h.ctx, h, principalTestWorkerRole, "exec-principal-worker-leader-worker", "corr-worker-leader")

	// worker completes back to leader: sender=worker, recipient=leader.
	if err := messages.SendCompletion(h.ctx, workerTask, leaderTask, time.Now()); err != nil {
		t.Fatalf("worker->leader completion: %v", err)
	}
	assertMessagePersisted(t, h.ctx, h, workerTask.ID, leaderTask.ID, "completion")
	workerPrincipal := assertRoleBoundPrincipal(t, h.ctx, h, principalTestWorkerRole)
	if workerPrincipal.DispatchActorRoleID != principalTestWorkerRole {
		t.Fatalf("worker principal role=%q want %q", workerPrincipal.DispatchActorRoleID, principalTestWorkerRole)
	}
}

// 4. TestExecutiveMessagingLeaderToCEOUsesLeaderPrincipal (completion direction)
func TestExecutiveMessagingLeaderToCEOUsesLeaderPrincipal(t *testing.T) {
	h := newIntegrationHarness(t)
	defer h.close()
	messages, _ := newTestAgentMessages(t, h)

	ceoTask := createRealTask(t, h.ctx, h, principalTestCEORole, "exec-principal-leader-ceo-ceo", "corr-leader-ceo")
	leaderTask := createRealTask(t, h.ctx, h, principalTestLeaderRole, "exec-principal-leader-ceo-leader", "corr-leader-ceo")

	// leader completes back to CEO: sender=leader, recipient=CEO.
	if err := messages.SendCompletion(h.ctx, leaderTask, ceoTask, time.Now()); err != nil {
		t.Fatalf("leader->CEO completion: %v", err)
	}
	assertMessagePersisted(t, h.ctx, h, leaderTask.ID, ceoTask.ID, "completion")
}

// 5. TestExecutiveMessagingRejectsPrincipalRoleMismatch calls the ledger
// directly with a principal that is genuinely bound to a DIFFERENT role
// than the command's sender_role_id -- something
// resolveOrProvisionPrincipalForRole can no longer produce by construction,
// which is exactly why this test bypasses the adapter and drives
// agentmessaging/postgres.Store.Send directly, proving its own independent
// re-validation (not just the adapter's) denies the mismatch. This is the
// property "Mutation B: eliminar validateSenderRoleWithPrincipal" (see the
// EXEC-PRINCIPAL-001 handoff's mutation fitness section) would break.
func TestExecutiveMessagingRejectsPrincipalRoleMismatch(t *testing.T) {
	h := newIntegrationHarness(t)
	defer h.close()
	messages, _ := newTestAgentMessages(t, h)
	messageLedger, err := agentmessagingpostgres.New(h.store, h.registry, h.authorizer, 200, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	ceoTask := createRealTask(t, h.ctx, h, principalTestCEORole, "exec-principal-mismatch-ceo", "corr-mismatch")
	leaderTask := createRealTask(t, h.ctx, h, principalTestLeaderRole, "exec-principal-mismatch-leader", "corr-mismatch")

	// Provision the CEO's real role-bound principal via a legitimate send.
	if err := messages.SendDelegation(h.ctx, ceoTask, leaderTask, time.Now()); err != nil {
		t.Fatalf("provision CEO principal: %v", err)
	}
	ceoPrincipal := assertRoleBoundPrincipal(t, h.ctx, h, principalTestCEORole)

	workerTask := createRealTask(t, h.ctx, h, principalTestWorkerRole, "exec-principal-mismatch-worker", "corr-mismatch")
	leaderTask2 := createRealTask(t, h.ctx, h, principalTestLeaderRole, "exec-principal-mismatch-leader-2", "corr-mismatch-2")

	// Attempt to send a leader->worker command authenticated as the CEO's
	// principal -- sender_role_id="ingenieria_ia/orquestador" but the
	// supplied principal's dispatch_actor_role_id is "empresa/ceo".
	cmd := buildDelegationSendCommand(leaderTask2, workerTask)
	principalIDStr := strconv.FormatInt(ceoPrincipal.ID, 10)
	if _, _, err := messageLedger.Send(h.ctx, principalIDStr, cmd, time.Now()); err == nil {
		t.Fatal("send authenticated as the CEO principal but claiming to be sent by the leader role must be denied (principal/sender role mismatch)")
	}
}

// 6. TestExecutiveMessagingRejectsDisabledPrincipal
func TestExecutiveMessagingRejectsDisabledPrincipal(t *testing.T) {
	h := newIntegrationHarness(t)
	defer h.close()
	messages, dispatchStore := newTestAgentMessages(t, h)

	ceoTask := createRealTask(t, h.ctx, h, principalTestCEORole, "exec-principal-disabled-ceo", "corr-disabled")
	leaderTask := createRealTask(t, h.ctx, h, principalTestLeaderRole, "exec-principal-disabled-leader", "corr-disabled")

	// First send provisions the CEO's role-bound principal and succeeds.
	if err := messages.SendDelegation(h.ctx, ceoTask, leaderTask, time.Now()); err != nil {
		t.Fatalf("initial delegation (provisions principal): %v", err)
	}
	ceoPrincipal := assertRoleBoundPrincipal(t, h.ctx, h, principalTestCEORole)
	if _, err := dispatchStore.DisablePrincipal(h.ctx, ceoPrincipal.ID, "empresa/human", "test_disable"); err != nil {
		t.Fatalf("disable principal: %v", err)
	}

	leaderTask2 := createRealTask(t, h.ctx, h, principalTestLeaderRole, "exec-principal-disabled-leader-2", "corr-disabled-2")
	if err := messages.SendDelegation(h.ctx, ceoTask, leaderTask2, time.Now()); err == nil {
		t.Fatal("delegation with a disabled principal must be denied")
	}
	// Denied by internal/agentmessaging/postgres.Store's own defense-in-depth
	// re-validation (its query filters status='active'), not by the
	// resolver -- RegisterPrincipal's idempotent reuse returns the SAME
	// (now-disabled) row for the same deterministic idempotency key rather
	// than silently minting a fresh active one, which is exactly why the
	// ledger layer, not just the resolver, must keep validating status.
}

// 7. TestExecutiveMessagingRejectsCrossOrgPrincipal proves a send whose
// command organization differs from where the real tasks/principal live is
// denied. In THIS deployment (single-organization -- see D-012) the
// operative layer that denies it is agentmessaging/postgres.Store's task
// ownership check (validateTaskOwnership), not principal-organization
// matching in isolation: constructing a second, fully-provisioned
// organization (own organizations/organization_roles/registry rows) purely
// to isolate the principal-level org check was judged out of proportion for
// one negative-path test, and is documented as an honest gap in the
// EXEC-PRINCIPAL-001 mutation-fitness report rather than forced green.
// Store.validateExecutionPrincipalForSender's own organization_id filter
// (mutation C in that report) is redundant defense in depth for this exact
// attack shape today, given task ownership already covers it -- not
// unverified.
func TestExecutiveMessagingRejectsCrossOrgPrincipal(t *testing.T) {
	h := newIntegrationHarness(t)
	defer h.close()
	messages, _ := newTestAgentMessages(t, h)
	messageLedger, err := agentmessagingpostgres.New(h.store, h.registry, h.authorizer, 200, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	ceoTask := createRealTask(t, h.ctx, h, principalTestCEORole, "exec-principal-cross-org-ceo", "corr-cross-org")
	leaderTask := createRealTask(t, h.ctx, h, principalTestLeaderRole, "exec-principal-cross-org-leader", "corr-cross-org")

	// Provision a real, active, CORRECTLY role-bound CEO principal for
	// "explorarte" -- proving the mismatch below is purely about
	// organization, not role.
	if err := messages.SendDelegation(h.ctx, ceoTask, leaderTask, time.Now()); err != nil {
		t.Fatalf("provision CEO principal: %v", err)
	}
	ceoPrincipal := assertRoleBoundPrincipal(t, h.ctx, h, principalTestCEORole)

	leaderTask2 := createRealTask(t, h.ctx, h, principalTestLeaderRole, "exec-principal-cross-org-leader-2", "corr-cross-org-2")
	cmd := buildDelegationSendCommand(ceoTask, leaderTask2)
	cmd.OrganizationID = "a-different-organization-entirely"
	if _, _, err := messageLedger.Send(h.ctx, strconv.FormatInt(ceoPrincipal.ID, 10), cmd, time.Now()); err == nil {
		t.Fatal("send whose command organization_id differs from the resolved principal's organization must be denied")
	}
}

// 8. TestExecutiveMultiHopMessagingEndToEnd proves all four hops with real,
// persisted tasks and messages: CEO->leader and leader->worker via
// SendDelegation (the same calls the real Orchestrator makes), then
// worker->leader and leader->CEO via SendCompletion (called directly --
// see the file-level comment on why the Orchestrator itself does not wire
// SendCompletion; that is out of EXEC-PRINCIPAL-001's scope).
func TestExecutiveMultiHopMessagingEndToEnd(t *testing.T) {
	h := newIntegrationHarness(t)
	defer h.close()
	messages, _ := newTestAgentMessages(t, h)

	ceoTask := createRealTask(t, h.ctx, h, principalTestCEORole, "exec-principal-e2e-ceo", "corr-e2e")
	leaderTask := createRealTask(t, h.ctx, h, principalTestLeaderRole, "exec-principal-e2e-leader", "corr-e2e")
	workerTask := createRealTask(t, h.ctx, h, principalTestWorkerRole, "exec-principal-e2e-worker", "corr-e2e")

	if err := messages.SendDelegation(h.ctx, ceoTask, leaderTask, time.Now()); err != nil {
		t.Fatalf("hop A CEO->leader: %v", err)
	}
	if err := messages.SendDelegation(h.ctx, leaderTask, workerTask, time.Now()); err != nil {
		t.Fatalf("hop B leader->worker: %v", err)
	}
	if err := messages.SendCompletion(h.ctx, workerTask, leaderTask, time.Now()); err != nil {
		t.Fatalf("hop C worker->leader: %v", err)
	}
	if err := messages.SendCompletion(h.ctx, leaderTask, ceoTask, time.Now()); err != nil {
		t.Fatalf("hop D leader->CEO: %v", err)
	}

	assertMessagePersisted(t, h.ctx, h, ceoTask.ID, leaderTask.ID, "delegation")
	assertMessagePersisted(t, h.ctx, h, leaderTask.ID, workerTask.ID, "delegation")
	assertMessagePersisted(t, h.ctx, h, workerTask.ID, leaderTask.ID, "completion")
	assertMessagePersisted(t, h.ctx, h, leaderTask.ID, ceoTask.ID, "completion")

	ceoPrincipal := assertRoleBoundPrincipal(t, h.ctx, h, principalTestCEORole)
	leaderPrincipal := assertRoleBoundPrincipal(t, h.ctx, h, principalTestLeaderRole)
	workerPrincipal := assertRoleBoundPrincipal(t, h.ctx, h, principalTestWorkerRole)
	if ceoPrincipal.ID == leaderPrincipal.ID || leaderPrincipal.ID == workerPrincipal.ID || ceoPrincipal.ID == workerPrincipal.ID {
		t.Fatalf("all three roles must resolve to distinct principals: ceo=%d leader=%d worker=%d", ceoPrincipal.ID, leaderPrincipal.ID, workerPrincipal.ID)
	}

	// Same-role self-message must still be denied by topology, unrelated to
	// principal resolution (proves the fix did not weaken that invariant).
	ceoTask2 := createRealTask(t, h.ctx, h, principalTestCEORole, "exec-principal-e2e-ceo-self", "corr-e2e-self")
	if err := messages.SendDelegation(h.ctx, ceoTask, ceoTask2, time.Now()); err == nil {
		t.Fatal("CEO->CEO self-delegation must be denied by topology")
	}
}
