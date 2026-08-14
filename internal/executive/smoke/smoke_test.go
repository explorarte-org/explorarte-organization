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

	messages, err := smoke.Wire(cfg, store)
	if err != nil {
		store.Close()
		cancel()
		t.Fatal(err)
	}

	return &harness{ctx: ctx, cancel: cancel, store: store, messages: messages}
}

func (h *harness) close() {
	h.store.Close()
	h.cancel()
}

func (h *harness) roles() smoke.Roles {
	return smoke.Roles{CEO: smokeCEORole, Leader: smokeLeaderRole, Worker: smokeWorkerRole}
}

// 1. Happy-path four-hop smoke.
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
	deliverReport, err := smoke.Deliver(h.ctx, h.store.Pool(), h.messages, smokeTestOrg, correlationID, time.Now())
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
// roles must find all three principals already provisioned.
func TestSmokeExistingRoleBoundPrincipals(t *testing.T) {
	h := newHarness(t)
	defer h.close()

	first := smoke.NewCorrelationID(time.Now())
	if _, err := smoke.Run(h.ctx, h.store.Pool(), h.messages, smokeTestOrg, h.roles(), first, time.Now()); err != nil {
		t.Fatalf("first run (provisions principals): %v", err)
	}

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

// 6. Disabled principal DENY.
func TestSmokeDisabledPrincipalDenied(t *testing.T) {
	h := newHarness(t)
	defer h.close()

	correlationID := smoke.NewCorrelationID(time.Now())
	if _, err := smoke.Run(h.ctx, h.store.Pool(), h.messages, smokeTestOrg, h.roles(), correlationID, time.Now()); err != nil {
		t.Fatalf("provisioning run: %v", err)
	}
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

// 8. Unrelated rows/content must remain unchanged by a smoke run.
func TestSmokeUnrelatedRowsUnchanged(t *testing.T) {
	h := newHarness(t)
	defer h.close()

	// A pre-existing, unrelated control task+message pair created BEFORE
	// the smoke run — a real, non-smoke row the run must never touch.
	controlCorrelation := "pre-existing-control-fixture"
	pre, err := smoke.Run(h.ctx, h.store.Pool(), h.messages, smokeTestOrg, h.roles(), controlCorrelation, time.Now())
	if err != nil {
		t.Fatalf("seed control fixture: %v", err)
	}
	var controlSnapshotBefore struct {
		status        string
		balanceUnused int
	}
	if err := h.store.Pool().QueryRow(h.ctx, `SELECT status FROM tasks WHERE id=$1`, pre.CEOTask.ID).Scan(&controlSnapshotBefore.status); err != nil {
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

	var controlStatusAfter string
	if err := h.store.Pool().QueryRow(h.ctx, `SELECT status FROM tasks WHERE id=$1`, pre.CEOTask.ID).Scan(&controlStatusAfter); err != nil {
		t.Fatalf("re-read control task: %v", err)
	}
	if controlStatusAfter != controlSnapshotBefore.status {
		t.Fatalf("control task status changed: before=%q after=%q", controlSnapshotBefore.status, controlStatusAfter)
	}
	var controlMessageCount int
	if err := h.store.Pool().QueryRow(h.ctx, `SELECT count(*) FROM agent_messages WHERE correlation_id=$1`, controlCorrelation).Scan(&controlMessageCount); err != nil {
		t.Fatal(err)
	}
	if controlMessageCount != 4 {
		t.Fatalf("control fixture's 4 messages were disturbed: now count=%d", controlMessageCount)
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

// 9. Deliver must defend against foreign traffic in the same role's inbox:
// if a genuine, older, unrelated pending message for the leader role exists
// when Deliver runs, ClaimNext will surface it first (oldest-first FIFO) --
// Deliver must detect it does not belong to this run, release it back to
// 'pending' via Nack (never Ack it, never leave it dangling 'claimed'), and
// fail loudly rather than silently acknowledging traffic it does not own.
func TestSmokeDeliverDefendsAgainstForeignInboxTraffic(t *testing.T) {
	h := newHarness(t)
	defer h.close()

	// A genuine-looking, older smoke run standing in for real production
	// traffic to the same leader role, deliberately left undelivered
	// (pending) -- exactly what a real consumer's queued message looks
	// like from Deliver's point of view.
	foreignCorrelation := "genuine-production-traffic-not-part-of-this-smoke"
	if _, err := smoke.Run(h.ctx, h.store.Pool(), h.messages, smokeTestOrg, h.roles(), foreignCorrelation, time.Now()); err != nil {
		t.Fatalf("seed foreign traffic: %v", err)
	}
	var foreignLeaderMessageID int64
	if err := h.store.Pool().QueryRow(h.ctx,
		`SELECT id FROM agent_messages WHERE organization_id=$1 AND correlation_id=$2 AND recipient_role_id=$3 ORDER BY id LIMIT 1`,
		smokeTestOrg, foreignCorrelation, smokeLeaderRole,
	).Scan(&foreignLeaderMessageID); err != nil {
		t.Fatalf("locate foreign leader-inbox message: %v", err)
	}

	// The run actually under test, created strictly after the foreign
	// traffic, so ClaimNext's oldest-first ordering would surface the
	// foreign message before this run's own leader-inbox messages if
	// Deliver only trusted batch size instead of checking identity.
	correlationID := smoke.NewCorrelationID(time.Now().Add(time.Second))
	if _, err := smoke.Run(h.ctx, h.store.Pool(), h.messages, smokeTestOrg, h.roles(), correlationID, time.Now()); err != nil {
		t.Fatalf("run under test: %v", err)
	}
	report, err := smoke.Verify(h.ctx, h.store.Pool(), smokeTestOrg, correlationID)
	if err != nil || !report.AllFourPresent || !report.AllCorrelated || !report.AllIdentical || !report.SupportTasksSafe {
		t.Fatalf("run under test did not verify cleanly: err=%v report=%+v", err, report)
	}

	_, deliverErr := smoke.Deliver(h.ctx, h.store.Pool(), h.messages, smokeTestOrg, correlationID, time.Now())
	if deliverErr == nil {
		t.Fatal("expected Deliver to fail when a foreign message occupies the same role's inbox ahead of this run's own messages")
	}

	var foreignStatus string
	if err := h.store.Pool().QueryRow(h.ctx, `SELECT status FROM agent_messages WHERE id=$1`, foreignLeaderMessageID).Scan(&foreignStatus); err != nil {
		t.Fatalf("re-read foreign message: %v", err)
	}
	if foreignStatus != "pending" {
		t.Fatalf("foreign message status=%q, want 'pending' (Deliver must release, never ack or strand, traffic it does not own)", foreignStatus)
	}
}
