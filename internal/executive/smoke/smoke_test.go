//go:build integration

package smoke_test

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/agentmessaging"
	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/executive/runtimeadapter"
	"github.com/Mireuz13/explorarte-organization/internal/executive/smoke"
	modelegressbootstrap "github.com/Mireuz13/explorarte-organization/internal/modelegress/bootstrap"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/testdbguard"
	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
)

const (
	smokeTestOrg    = "explorarte"
	smokeCEORole    = "empresa/ceo"
	smokeLeaderRole = "ingenieria_ia/orquestador"
	smokeWorkerRole = "ingenieria_ia/qa"
)

type harness struct {
	ctx      context.Context
	cancel   context.CancelFunc
	cfg      config.Config
	store    *platformpostgres.Store
	messages runtimeadapter.AgentMessages
}

// newHarness syncs the REAL canonical registry (docs/canonical) into a
// fresh, isolated test database — the same pattern
// internal/executive/postgres_integration_test.go's newIntegrationHarness
// uses — so the three roles this suite exercises
// (empresa/ceo, ingenieria_ia/orquestador, ingenieria_ia/qa) are the real
// canonical roles, with real, policy-file-backed authorization, not a
// hand-invented fixture that might not match what production's
// authorizer would actually decide.
func newHarness(t *testing.T) *harness {
	t.Helper()
	databaseURL := os.Getenv("ORG_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("ORG_TEST_DATABASE_URL is required")
	}
	canonicalDir := filepath.Join("..", "..", "..", "docs", "canonical")
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	cfg, err := config.LoadFrom(func(key string) (string, bool) {
		values := map[string]string{
			"ORG_ENVIRONMENT": "test", "ORG_DATABASE_URL": databaseURL,
			"ORG_DATABASE_MAX_CONNS": "24", "ORG_DATABASE_MIN_CONNS": "0",
			"ORG_CANONICAL_DIR": canonicalDir, "ORG_TASK_ORGANIZATION_ID": smokeTestOrg,
		}
		v, ok := values[key]
		return v, ok
	})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	store, err := platformpostgres.Open(ctx, cfg.Database, "smoke-integration-test")
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if err := testdbguard.RequireTestDatabase(ctx, databaseURL, store.Pool()); err != nil {
		store.Close()
		cancel()
		t.Fatalf("refusing to run against unverified database: %v", err)
	}
	runner, err := platformmigrations.New(store.Pool(), rootmigrations.Files)
	if err != nil {
		store.Close()
		cancel()
		t.Fatal(err)
	}
	if _, err := runner.Up(ctx); err != nil {
		store.Close()
		cancel()
		t.Fatalf("migrate: %v", err)
	}
	if err := testdbguard.RequireDestructive(ctx, databaseURL, store.Pool()); err != nil {
		store.Close()
		cancel()
		t.Fatalf("refusing destructive reset: %v", err)
	}
	if _, err := store.Pool().Exec(ctx, `
TRUNCATE outbox_events,task_dead_letters,task_events,task_leases,task_attempts,task_evidence,
         task_requirements,task_dependencies,tasks,agent_messages,model_execution_principals,
         organization_reporting_lines,organization_registry_revision_documents,organization_roles,
         organizational_units,organizations,organization_registry_revisions,audit_events
RESTART IDENTITY CASCADE`); err != nil {
		store.Close()
		cancel()
		t.Fatalf("reset schema: %v", err)
	}

	registryRepo, err := registry.NewPostgresRepository(store)
	if err != nil {
		store.Close()
		cancel()
		t.Fatal(err)
	}
	loader, err := registry.NewLoader(canonicalDir)
	if err != nil {
		store.Close()
		cancel()
		t.Fatal(err)
	}
	registryService, err := registry.NewService(loader, registryRepo, smokeTestOrg, 30*time.Second)
	if err != nil {
		store.Close()
		cancel()
		t.Fatal(err)
	}
	if syncResult, syncErr := registryService.SynchronizeCanonical(ctx, true); syncErr != nil || !syncResult.Applied {
		store.Close()
		cancel()
		t.Fatalf("sync registry: result=%+v err=%v", syncResult, syncErr)
	}

	// Registry sync alone leaves the current revision without a durable
	// model-egress binding (they are two independently-maintained canonical
	// documents, synced through two separate services) -- exactly the gap
	// Preflight's EgressBound check exists to catch (added for
	// CUTOVER-REHEARSAL-001). Every test in this file that expects to get
	// PAST Preflight needs this bound, the same way a real deployment's
	// bootstrap already does it via modelegress/bootstrap.Open.
	egressRuntime, err := modelegressbootstrap.Open(cfg, store)
	if err != nil {
		store.Close()
		cancel()
		t.Fatal(err)
	}
	if egressSync, egressErr := egressRuntime.Service.Sync(ctx, true); egressErr != nil || !egressSync.Applied {
		store.Close()
		cancel()
		t.Fatalf("sync model egress policy: result=%+v err=%v", egressSync, egressErr)
	}

	messages, err := smoke.Wire(cfg, store)
	if err != nil {
		store.Close()
		cancel()
		t.Fatal(err)
	}

	return &harness{ctx: ctx, cancel: cancel, cfg: cfg, store: store, messages: messages}
}

func (h *harness) close() {
	h.store.Close()
	h.cancel()
}

func (h *harness) roles() smoke.Roles {
	return smoke.Roles{CEO: smokeCEORole, Leader: smokeLeaderRole, Worker: smokeWorkerRole}
}

// runVerifyDeliver runs a full clean smoke cycle end to end and fails the
// test immediately if any stage does not pass — the "known good baseline"
// several tests below need before they can safely run() again (Run refuses
// to start while a prior run's messages are still pending/claimed).
func (h *harness) runVerifyDeliver(t *testing.T, correlationID string) smoke.Result {
	t.Helper()
	result, err := smoke.Run(h.ctx, h.store.Pool(), h.messages, smokeTestOrg, h.roles(), correlationID, time.Now())
	if err != nil {
		t.Fatalf("run(%q): %v", correlationID, err)
	}
	report, err := smoke.Verify(h.ctx, h.store.Pool(), smokeTestOrg, correlationID)
	if err != nil || !report.AllFourPresent || !report.AllCorrelated || !report.AllIdentical || !report.SupportTasksSafe {
		t.Fatalf("verify(%q) did not pass cleanly: err=%v report=%+v", correlationID, err, report)
	}
	deliverReport, err := smoke.Deliver(h.ctx, h.store.Pool(), h.messages, smokeTestOrg, h.roles(), correlationID, time.Now())
	if err != nil || !deliverReport.AllDelivered {
		t.Fatalf("deliver(%q) did not complete cleanly: err=%v report=%+v", correlationID, err, deliverReport)
	}
	return result
}

// 1. Happy-path four-hop smoke, through to full delivery.
func TestSmokeHappyPathFourHops(t *testing.T) {
	h := newHarness(t)
	defer h.close()

	correlationID := smoke.NewCorrelationID(time.Now())
	result, err := smoke.Run(h.ctx, h.store.Pool(), h.messages, smokeTestOrg, h.roles(), correlationID, time.Now())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(result.Hops) != 4 {
		t.Fatalf("hops=%d want 4: %+v", len(result.Hops), result.Hops)
	}

	report, err := smoke.Verify(h.ctx, h.store.Pool(), smokeTestOrg, correlationID)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !report.AllFourPresent {
		t.Fatalf("expected 4 persisted messages, got %d: %+v", len(report.Messages), report.Messages)
	}
	if !report.AllCorrelated {
		t.Fatalf("expected all 4 messages to share correlation %q: %+v", correlationID, report.Messages)
	}
	if !report.AllIdentical {
		t.Fatalf("expected sender_role_id == task.assigned_role_id == principal.dispatch_actor_role_id for every hop: %+v", report.Messages)
	}
	if !report.SupportTasksSafe {
		t.Fatalf("expected the 3 support tasks to never be executable")
	}

	// Close the loop: only after Verify passes, per the gate Deliver itself
	// requires callers to have already checked.
	deliverReport, err := smoke.Deliver(h.ctx, h.store.Pool(), h.messages, smokeTestOrg, h.roles(), correlationID, time.Now())
	if err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if deliverReport.ClaimedCount != 4 || deliverReport.AckedCount != 4 {
		t.Fatalf("deliver claimed=%d acked=%d, want 4/4: %+v", deliverReport.ClaimedCount, deliverReport.AckedCount, deliverReport)
	}
	if deliverReport.RemainingPending != 0 {
		t.Fatalf("deliver left %d pending, want 0", deliverReport.RemainingPending)
	}
	if !deliverReport.AllDelivered {
		t.Fatalf("expected AllDelivered=true: %+v", deliverReport)
	}

	var statuses []string
	rows, err := h.store.Pool().Query(h.ctx, `SELECT status FROM agent_messages WHERE organization_id=$1 AND correlation_id=$2`, smokeTestOrg, correlationID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		statuses = append(statuses, s)
	}
	if len(statuses) != 4 {
		t.Fatalf("expected 4 messages after delivery, got %d", len(statuses))
	}
	for _, s := range statuses {
		if s != "delivered" {
			t.Fatalf("expected every message status='delivered', found %q", s)
		}
	}
}

// 2. Existing role-bound principals: a second smoke run against the same
// roles must find all three principals already provisioned. The first run
// must be fully delivered first — Run refuses to start while any of its
// own prior messages are still pending.
func TestSmokeExistingRoleBoundPrincipals(t *testing.T) {
	h := newHarness(t)
	defer h.close()

	h.runVerifyDeliver(t, smoke.NewCorrelationID(time.Now()))

	second := smoke.NewCorrelationID(time.Now().Add(time.Second))
	result, err := smoke.Run(h.ctx, h.store.Pool(), h.messages, smokeTestOrg, h.roles(), second, time.Now())
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	for _, hop := range result.Hops {
		if !hop.PrincipalWasExisting {
			t.Fatalf("hop %q: expected PrincipalWasExisting=true on the second run, got false", hop.Label)
		}
	}
}

// 3. Missing principals / expected lazy provisioning: the first run ever
// against fresh roles must report every principal as newly provisioned.
func TestSmokeMissingPrincipalsLazyProvisioning(t *testing.T) {
	h := newHarness(t)
	defer h.close()

	correlationID := smoke.NewCorrelationID(time.Now())
	result, err := smoke.Run(h.ctx, h.store.Pool(), h.messages, smokeTestOrg, h.roles(), correlationID, time.Now())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, hop := range result.Hops {
		if hop.PrincipalWasExisting {
			t.Fatalf("hop %q: expected PrincipalWasExisting=false on a fresh org (no prior principals)", hop.Label)
		}
	}
}

// 4. Wrong organization DENY: a send whose command organization differs
// from where the real tasks/principal live must be rejected.
func TestSmokeWrongOrganizationDenied(t *testing.T) {
	h := newHarness(t)
	defer h.close()

	correlationID := smoke.NewCorrelationID(time.Now())
	result, err := smoke.Run(h.ctx, h.store.Pool(), h.messages, smokeTestOrg, h.roles(), correlationID, time.Now())
	if err != nil {
		t.Fatalf("provisioning run: %v", err)
	}
	principal, err := h.messages.PrincipalStore.ResolveActiveForRole(h.ctx, smokeTestOrg, smokeCEORole)
	if err != nil {
		t.Fatalf("resolve CEO principal: %v", err)
	}

	recipientTaskID := result.LeaderTask.ID
	cmd := agentmessaging.SendCommand{
		OrganizationID: "a-different-organization-entirely", SenderRoleID: smokeCEORole, SenderTaskID: result.CEOTask.ID,
		RecipientRoleID: smokeLeaderRole, RecipientTaskID: &recipientTaskID, CorrelationID: "cross-org-attempt",
		CausationID: "cross-org-attempt", MessageType: agentmessaging.MessageDelegation,
		Payload:        []byte(`{"delegated_task_id":` + strconv.FormatInt(recipientTaskID, 10) + `}`),
		IdempotencyKey: "smoke-wrong-org-attempt", MaxAttempts: 1, SchemaVersion: agentmessaging.SchemaVersionV1,
	}
	if _, _, err := h.messages.Ledger.Send(h.ctx, strconv.FormatInt(principal.ID, 10), cmd, time.Now()); err == nil {
		t.Fatal("send whose command organization_id differs from the resolved principal's organization must be denied")
	}
}

// 5. Wrong role/task ownership DENY: claiming a real, persisted task ID
// belongs to a role it does not actually carry in the tasks table must be
// rejected by the ledger's own task-ownership re-validation.
func TestSmokeWrongRoleTaskOwnershipDenied(t *testing.T) {
	h := newHarness(t)
	defer h.close()

	correlationID := smoke.NewCorrelationID(time.Now())
	result, err := smoke.Run(h.ctx, h.store.Pool(), h.messages, smokeTestOrg, h.roles(), correlationID, time.Now())
	if err != nil {
		t.Fatalf("provisioning run: %v", err)
	}

	// result.CEOTask.ID really is assigned_role_id='empresa/ceo' in the
	// tasks table. Claim it belongs to the leader role instead.
	impersonatedSender := result.CEOTask
	impersonatedSender.AssignedRoleID = smokeLeaderRole

	err = h.messages.SendDelegation(h.ctx, impersonatedSender, result.WorkerTask, time.Now())
	if err == nil {
		t.Fatal("delegation claiming a task belongs to a role it is not actually assigned must be denied")
	}
}

// 6. Disabled principal DENY. The first (provisioning) run is delivered
// before disabling its principal and attempting a second run, so the
// second run's failure is unambiguously attributable to the disabled
// principal rather than to leftover pending traffic from the first.
func TestSmokeDisabledPrincipalDenied(t *testing.T) {
	h := newHarness(t)
	defer h.close()

	h.runVerifyDeliver(t, smoke.NewCorrelationID(time.Now()))

	ceoPrincipal, err := h.messages.PrincipalStore.ResolveActiveForRole(h.ctx, smokeTestOrg, smokeCEORole)
	if err != nil {
		t.Fatalf("resolve CEO principal: %v", err)
	}
	if _, err := h.messages.PrincipalStore.DisablePrincipal(h.ctx, ceoPrincipal.ID, "empresa/human", "smoke_test_disable"); err != nil {
		t.Fatalf("disable principal: %v", err)
	}

	newCorrelation := smoke.NewCorrelationID(time.Now().Add(time.Second))
	if _, err := smoke.Run(h.ctx, h.store.Pool(), h.messages, smokeTestOrg, h.roles(), newCorrelation, time.Now()); err == nil {
		t.Fatal("a run whose CEO principal was disabled must fail at the ceo->leader hop")
	}
}

// 7. Support tasks must never reach an executable status.
func TestSmokeSupportTasksNeverExecutable(t *testing.T) {
	h := newHarness(t)
	defer h.close()

	correlationID := smoke.NewCorrelationID(time.Now())
	result, err := smoke.Run(h.ctx, h.store.Pool(), h.messages, smokeTestOrg, h.roles(), correlationID, time.Now())
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	for label, taskID := range map[string]int64{"ceo": result.CEOTask.ID, "leader": result.LeaderTask.ID, "worker": result.WorkerTask.ID} {
		var status string
		var terminalAtIsNull bool
		if err := h.store.Pool().QueryRow(h.ctx, `SELECT status, terminal_at IS NULL FROM tasks WHERE id=$1`, taskID).Scan(&status, &terminalAtIsNull); err != nil {
			t.Fatalf("read %s support task: %v", label, err)
		}
		if status != "no_action" {
			t.Fatalf("%s support task status=%q, want no_action", label, status)
		}
		if terminalAtIsNull {
			t.Fatalf("%s support task terminal_at is NULL, want populated", label)
		}
	}

	report, err := smoke.Verify(h.ctx, h.store.Pool(), smokeTestOrg, correlationID)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !report.SupportTasksSafe {
		t.Fatal("Verify reported support tasks are not safe (executable)")
	}
}

// 8. Unrelated rows/content must remain unchanged by a smoke run. The
// control fixture is delivered immediately after creation so its messages
// leave 'pending' (a delivered, historical message no longer occupies an
// inbox and must not block a later run's own quiescence check).
func TestSmokeUnrelatedRowsUnchanged(t *testing.T) {
	h := newHarness(t)
	defer h.close()

	controlCorrelation := "pre-existing-control-fixture"
	pre := h.runVerifyDeliver(t, controlCorrelation)

	var controlStatusBefore string
	if err := h.store.Pool().QueryRow(h.ctx, `SELECT status FROM agent_messages WHERE organization_id=$1 AND correlation_id=$2 ORDER BY id LIMIT 1`, smokeTestOrg, controlCorrelation).Scan(&controlStatusBefore); err != nil {
		t.Fatalf("snapshot control message: %v", err)
	}
	if controlStatusBefore != "delivered" {
		t.Fatalf("control fixture status=%q, want delivered before proceeding", controlStatusBefore)
	}
	var controlTaskStatusBefore string
	if err := h.store.Pool().QueryRow(h.ctx, `SELECT status FROM tasks WHERE id=$1`, pre.CEOTask.ID).Scan(&controlTaskStatusBefore); err != nil {
		t.Fatalf("snapshot control task: %v", err)
	}
	var orgRoleCountBefore, orgCountBefore int
	if err := h.store.Pool().QueryRow(h.ctx, `SELECT count(*) FROM organization_roles`).Scan(&orgRoleCountBefore); err != nil {
		t.Fatal(err)
	}
	if err := h.store.Pool().QueryRow(h.ctx, `SELECT count(*) FROM organizations`).Scan(&orgCountBefore); err != nil {
		t.Fatal(err)
	}

	correlationID := smoke.NewCorrelationID(time.Now().Add(time.Second))
	if _, err := smoke.Run(h.ctx, h.store.Pool(), h.messages, smokeTestOrg, h.roles(), correlationID, time.Now()); err != nil {
		t.Fatalf("run under test: %v", err)
	}

	var controlTaskStatusAfter string
	if err := h.store.Pool().QueryRow(h.ctx, `SELECT status FROM tasks WHERE id=$1`, pre.CEOTask.ID).Scan(&controlTaskStatusAfter); err != nil {
		t.Fatalf("re-read control task: %v", err)
	}
	if controlTaskStatusAfter != controlTaskStatusBefore {
		t.Fatalf("control task status changed: before=%q after=%q", controlTaskStatusBefore, controlTaskStatusAfter)
	}
	var controlDeliveredCount int
	if err := h.store.Pool().QueryRow(h.ctx, `SELECT count(*) FROM agent_messages WHERE correlation_id=$1 AND status='delivered'`, controlCorrelation).Scan(&controlDeliveredCount); err != nil {
		t.Fatal(err)
	}
	if controlDeliveredCount != 4 {
		t.Fatalf("control fixture's 4 delivered messages were disturbed: now delivered-count=%d", controlDeliveredCount)
	}

	var orgRoleCountAfter, orgCountAfter int
	if err := h.store.Pool().QueryRow(h.ctx, `SELECT count(*) FROM organization_roles`).Scan(&orgRoleCountAfter); err != nil {
		t.Fatal(err)
	}
	if err := h.store.Pool().QueryRow(h.ctx, `SELECT count(*) FROM organizations`).Scan(&orgCountAfter); err != nil {
		t.Fatal(err)
	}
	if orgRoleCountAfter != orgRoleCountBefore {
		t.Fatalf("organization_roles count changed: before=%d after=%d (smoke must never resync/mutate the registry)", orgRoleCountBefore, orgRoleCountAfter)
	}
	if orgCountAfter != orgCountBefore {
		t.Fatalf("organizations count changed: before=%d after=%d", orgCountBefore, orgCountAfter)
	}
}

// snapshotMessage captures every column that could plausibly be mutated by
// a claim/nack cycle, so a test can assert byte-equivalence, not just
// "still pending".
type snapshotMessage struct {
	status         string
	attemptCount   int
	lastError      *string
	availableAt    time.Time
	updatedAt      time.Time
	claimTokenHash *string
	claimedBy      *string
	claimExpiresAt *time.Time
}

func snapshotAgentMessage(t *testing.T, ctx context.Context, h *harness, id int64) snapshotMessage {
	t.Helper()
	var s snapshotMessage
	if err := h.store.Pool().QueryRow(ctx, `
		SELECT status, attempt_count, last_error, available_at, updated_at, claim_token_hash, claimed_by, claim_expires_at
		FROM agent_messages WHERE id=$1
	`, id).Scan(&s.status, &s.attemptCount, &s.lastError, &s.availableAt, &s.updatedAt, &s.claimTokenHash, &s.claimedBy, &s.claimExpiresAt); err != nil {
		t.Fatalf("snapshot message id=%d: %v", id, err)
	}
	return s
}

func requireByteEquivalent(t *testing.T, before, after snapshotMessage) {
	t.Helper()
	if before.status != after.status {
		t.Fatalf("status changed: before=%q after=%q (must be byte-equivalent — untouched, not merely still 'pending')", before.status, after.status)
	}
	if before.attemptCount != after.attemptCount {
		t.Fatalf("attempt_count changed: before=%d after=%d (a claim, even one immediately released, is not a no-op)", before.attemptCount, after.attemptCount)
	}
	if !before.updatedAt.Equal(after.updatedAt) {
		t.Fatalf("updated_at changed: before=%v after=%v", before.updatedAt, after.updatedAt)
	}
	if !before.availableAt.Equal(after.availableAt) {
		t.Fatalf("available_at changed: before=%v after=%v", before.availableAt, after.availableAt)
	}
	if (before.claimTokenHash == nil) != (after.claimTokenHash == nil) {
		t.Fatalf("claim_token_hash presence changed: before=%v after=%v", before.claimTokenHash, after.claimTokenHash)
	}
	if (before.claimedBy == nil) != (after.claimedBy == nil) {
		t.Fatalf("claimed_by presence changed: before=%v after=%v", before.claimedBy, after.claimedBy)
	}
}

// 9. Run must refuse to start at all — before creating any support tasks —
// when a role it needs is not quiescent (a genuine, unrelated pending
// message already occupies that role's inbox).
func TestSmokeRunRefusesWhenInboxesNotQuiescent(t *testing.T) {
	h := newHarness(t)
	defer h.close()

	h.runVerifyDeliver(t, "genuine-traffic-standing-in-for-production") // provisions principals cleanly, delivered
	foreign := smoke.NewCorrelationID(time.Now().Add(time.Second))
	if _, err := smoke.Run(h.ctx, h.store.Pool(), h.messages, smokeTestOrg, h.roles(), foreign, time.Now()); err != nil {
		t.Fatalf("seed foreign pending traffic: %v", err)
	} // deliberately left pending — stands in for a live, undelivered production message

	var tasksBefore, messagesBefore int
	h.store.Pool().QueryRow(h.ctx, `SELECT count(*) FROM tasks`).Scan(&tasksBefore)
	h.store.Pool().QueryRow(h.ctx, `SELECT count(*) FROM agent_messages`).Scan(&messagesBefore)

	blocked := smoke.NewCorrelationID(time.Now().Add(2 * time.Second))
	if _, err := smoke.Run(h.ctx, h.store.Pool(), h.messages, smokeTestOrg, h.roles(), blocked, time.Now()); err == nil {
		t.Fatal("Run must refuse to start while foreign pending traffic occupies a role inbox it needs")
	}

	var tasksAfter, messagesAfter int
	h.store.Pool().QueryRow(h.ctx, `SELECT count(*) FROM tasks`).Scan(&tasksAfter)
	h.store.Pool().QueryRow(h.ctx, `SELECT count(*) FROM agent_messages`).Scan(&messagesAfter)
	if tasksAfter != tasksBefore {
		t.Fatalf("Run created task rows despite refusing to proceed: before=%d after=%d", tasksBefore, tasksAfter)
	}
	if messagesAfter != messagesBefore {
		t.Fatalf("Run created message rows despite refusing to proceed: before=%d after=%d", messagesBefore, messagesAfter)
	}
}

// 10. Deliver must refuse to claim at all when foreign traffic appears in
// the window between a clean Verify and the Deliver call — and the foreign
// message must come out byte-equivalent to how it went in: Deliver's
// precheck means no ClaimNext is ever issued, so nothing is touched, not
// merely "released back to pending" after being claimed.
func TestSmokeDeliverRefusesWhenInboxesNotQuiescent(t *testing.T) {
	h := newHarness(t)
	defer h.close()

	correlationID := smoke.NewCorrelationID(time.Now())
	result, err := smoke.Run(h.ctx, h.store.Pool(), h.messages, smokeTestOrg, h.roles(), correlationID, time.Now())
	if err != nil {
		t.Fatalf("run under test: %v", err)
	}
	report, err := smoke.Verify(h.ctx, h.store.Pool(), smokeTestOrg, correlationID)
	if err != nil || !report.AllFourPresent || !report.AllCorrelated || !report.AllIdentical || !report.SupportTasksSafe {
		t.Fatalf("run under test did not verify cleanly: err=%v report=%+v", err, report)
	}

	// Inject genuine, unrelated traffic into the leader role's inbox in the
	// window between Verify and Deliver — via a direct Ledger.Send (not
	// smoke.Run, which would itself be blocked by the same precondition
	// this test exists to prove matters).
	ceoPrincipal, err := h.messages.PrincipalStore.ResolveActiveForRole(h.ctx, smokeTestOrg, smokeCEORole)
	if err != nil {
		t.Fatalf("resolve CEO principal: %v", err)
	}
	recipientTaskID := result.LeaderTask.ID
	injected := agentmessaging.SendCommand{
		OrganizationID: smokeTestOrg, SenderRoleID: smokeCEORole, SenderTaskID: result.CEOTask.ID,
		RecipientRoleID: smokeLeaderRole, RecipientTaskID: &recipientTaskID,
		CorrelationID: "genuine-traffic-injected-between-verify-and-deliver", CausationID: "genuine-traffic-injected-between-verify-and-deliver",
		MessageType: agentmessaging.MessageDelegation, Payload: []byte(`{"delegated_task_id":` + strconv.FormatInt(recipientTaskID, 10) + `}`),
		IdempotencyKey: "genuine-traffic-injected-between-verify-and-deliver", MaxAttempts: 1, SchemaVersion: agentmessaging.SchemaVersionV1,
	}
	var injectedID int64
	msg, _, sendErr := h.messages.Ledger.Send(h.ctx, strconv.FormatInt(ceoPrincipal.ID, 10), injected, time.Now())
	if sendErr != nil {
		t.Fatalf("inject genuine unrelated traffic: %v", sendErr)
	}
	injectedID = msg.ID

	before := snapshotAgentMessage(t, h.ctx, h, injectedID)

	_, deliverErr := smoke.Deliver(h.ctx, h.store.Pool(), h.messages, smokeTestOrg, h.roles(), correlationID, time.Now())
	if deliverErr == nil {
		t.Fatal("Deliver must refuse to claim while foreign pending traffic occupies a role inbox it needs")
	}

	after := snapshotAgentMessage(t, h.ctx, h, injectedID)
	requireByteEquivalent(t, before, after)
	if after.status != "pending" {
		t.Fatalf("injected message status=%q, want 'pending' untouched", after.status)
	}
}
