//go:build integration

package postgres_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/agentmessaging"
	agentmessagingpostgres "github.com/Mireuz13/explorarte-organization/internal/agentmessaging/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/authorization"
	authorizationpostgres "github.com/Mireuz13/explorarte-organization/internal/authorization/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/testdbguard"
	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
)

const (
	messagingIntegrationOrg  = "explorarte"
	messagingIntegrationUnit = "ingenieria_ia"
)

type messagingFixture struct {
	store          *platformpostgres.Store
	revisionID     int64
	registryReader registry.Reader
	authorizer     agentmessaging.CapabilityAuthorizer
}

func openMessagingFixture(t *testing.T, ctx context.Context) messagingFixture {
	t.Helper()
	databaseURL := os.Getenv("ORG_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("ORG_TEST_DATABASE_URL is required for integration tests")
	}
	canonicalDir := filepath.Join("..", "..", "..", "docs", "canonical")
	cfg, err := config.LoadFrom(func(key string) (string, bool) {
		values := map[string]string{"ORG_ENVIRONMENT": "test", "ORG_DATABASE_URL": databaseURL, "ORG_DATABASE_MAX_CONNS": "8", "ORG_DATABASE_MIN_CONNS": "0", "ORG_CANONICAL_DIR": canonicalDir}
		v, ok := values[key]
		return v, ok
	})
	if err != nil {
		t.Fatal(err)
	}
	store, err := platformpostgres.Open(ctx, cfg.Database, "agentmessaging-integration-test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	if err := testdbguard.RequireTestDatabase(ctx, databaseURL, store.Pool()); err != nil {
		t.Fatalf("refusing to run against unverified database: %v", err)
	}
	runner, err := platformmigrations.New(store.Pool(), rootmigrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Up(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := testdbguard.RequireDestructive(ctx, databaseURL, store.Pool()); err != nil {
		t.Fatalf("refusing destructive TRUNCATE: %v", err)
	}
	if _, err := store.Pool().Exec(ctx, `TRUNCATE organizations, organization_registry_revisions RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("reset schema: %v", err)
	}

	registryRepo, err := registry.NewPostgresRepository(store)
	if err != nil {
		t.Fatal(err)
	}
	loader, err := registry.NewLoader(canonicalDir)
	if err != nil {
		t.Fatal(err)
	}
	registryService, err := registry.NewService(loader, registryRepo, messagingIntegrationOrg, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	syncResult, err := registryService.SynchronizeCanonical(ctx, true)
	if err != nil || !syncResult.Applied {
		t.Fatalf("sync registry: result=%+v err=%v", syncResult, err)
	}
	revision, err := registryRepo.GetCurrentRevision(ctx, messagingIntegrationOrg)
	if err != nil || revision == nil {
		t.Fatalf("current registry revision=%+v err=%v", revision, err)
	}
	authorizationStore, err := authorizationpostgres.New(store)
	if err != nil {
		t.Fatal(err)
	}
	authorizer, err := authorization.NewWithPolicyReader(authorizationStore, messagingIntegrationOrg, canonicalDir)
	if err != nil {
		t.Fatal(err)
	}
	return messagingFixture{store: store, revisionID: revision.ID, registryReader: registryRepo, authorizer: authorizer}
}

func (f messagingFixture) insertTask(t *testing.T, ctx context.Context, roleID string, ordinal int) int64 {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	var taskID int64
	if err := f.store.Pool().QueryRow(ctx, `
INSERT INTO tasks (
 organization_id,organization_revision_id,requested_by_role_id,assigned_role_id,assigned_unit_id,
 idempotency_key,request_hash,title,instructions,acceptance_criteria,status,priority,available_at,
 max_attempts,attempt_count,version,created_at,updated_at
) VALUES ($1,$2,'empresa/ceo',$3,$4,$5,$6,$7,$8,'[]'::jsonb,'running',0,$9,1,1,1,$9,$9)
RETURNING id`, messagingIntegrationOrg, f.revisionID, roleID, messagingIntegrationUnit,
		fmt.Sprintf("agentmessaging-fixture-task-%d", ordinal), digest(fmt.Sprintf("agentmessaging-task-%d", ordinal)), fmt.Sprintf("Messaging fixture %d", ordinal), "durable messaging fixture", now,
	).Scan(&taskID); err != nil {
		t.Fatalf("insert task: %v", err)
	}
	return taskID
}

// insertExecutionPrincipal inserts a real, active model_execution_principals
// row bound to roleID and returns its id as a string, for use as the
// executionPrincipalID argument to Store.Send -- Send requires an
// authenticated principal whose dispatch_actor_role_id matches
// SenderRoleID (see store.go's validateExecutionPrincipalForSender);
// there is no test-only bypass for this, on purpose.
func (f messagingFixture) insertExecutionPrincipal(t *testing.T, ctx context.Context, roleID string, ordinal int) string {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	principalKey := fmt.Sprintf("agentmessaging-fixture-principal-%d", ordinal)
	var principalID int64
	if err := f.store.Pool().QueryRow(ctx, `
INSERT INTO model_execution_principals (
 organization_id, principal_key, dispatch_actor_role_id, principal_kind, status,
 idempotency_key, request_hash, registered_by_role_id, created_at, updated_at
) VALUES ($1,$2,$3,'local_process','active',$4,$5,'empresa/ceo',$6,$6)
RETURNING id`, messagingIntegrationOrg, principalKey, roleID,
		fmt.Sprintf("agentmessaging-fixture-principal-idem-%d", ordinal), digest(principalKey), now,
	).Scan(&principalID); err != nil {
		t.Fatalf("insert execution principal: %v", err)
	}
	return fmt.Sprintf("%d", principalID)
}

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func TestSendIsIdempotentPerOrganizationAndKey(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	fixture := openMessagingFixture(t, ctx)
	ledger, err := agentmessagingpostgres.New(fixture.store, fixture.registryReader, fixture.authorizer, 100, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	ceoTaskID := fixture.insertTask(t, ctx, "empresa/ceo", 1)
	recipientTaskID := fixture.insertTask(t, ctx, "ingenieria_ia/orquestador", 41)
	principalID := fixture.insertExecutionPrincipal(t, ctx, "empresa/ceo", 1)

	command := agentmessaging.SendCommand{
		OrganizationID: messagingIntegrationOrg, SenderRoleID: "empresa/ceo", SenderTaskID: ceoTaskID,
		RecipientRoleID: "ingenieria_ia/orquestador", RecipientTaskID: &recipientTaskID, CorrelationID: "executive:abc", CausationID: fmt.Sprintf("task:%d", ceoTaskID),
		MessageType: agentmessaging.MessageDelegation, SchemaVersion: agentmessaging.SchemaVersionV1, Payload: json.RawMessage(fmt.Sprintf(`{"delegated_task_id":%d}`, recipientTaskID)),
		IdempotencyKey: "delegation:ceo->leader", MaxAttempts: 5,
	}
	first, reused, err := ledger.Send(ctx, principalID, command, now)
	if err != nil || reused {
		t.Fatalf("first send: message=%+v reused=%v err=%v", first, reused, err)
	}
	if first.Status != agentmessaging.StatusPending {
		t.Fatalf("status=%s want pending", first.Status)
	}
	second, reused, err := ledger.Send(ctx, principalID, command, now.Add(time.Second))
	if err != nil || !reused || second.ID != first.ID {
		t.Fatalf("second send: message=%+v reused=%v err=%v want id=%d", second, reused, err, first.ID)
	}
}

// ORG-AUDIT-001 regression: idempotency dedup must compare the *stored*
// canonical request hash, not a caller-supplied field nobody populates.
// Reusing an idempotency key with a materially different command has to be
// a rejected collision, never a silent replay of someone else's message.
func TestSendRejectsSameKeyWithDifferentCommand(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	fixture := openMessagingFixture(t, ctx)
	ledger, err := agentmessagingpostgres.New(fixture.store, fixture.registryReader, fixture.authorizer, 100, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	ceoTaskID := fixture.insertTask(t, ctx, "empresa/ceo", 1)
	recipientTaskID := fixture.insertTask(t, ctx, "ingenieria_ia/orquestador", 41)
	otherRecipientTaskID := fixture.insertTask(t, ctx, "ingenieria_ia/orquestador", 42)
	principalID := fixture.insertExecutionPrincipal(t, ctx, "empresa/ceo", 1)

	base := agentmessaging.SendCommand{
		OrganizationID: messagingIntegrationOrg, SenderRoleID: "empresa/ceo", SenderTaskID: ceoTaskID,
		RecipientRoleID: "ingenieria_ia/orquestador", RecipientTaskID: &recipientTaskID, CorrelationID: "executive:collision", CausationID: fmt.Sprintf("task:%d", ceoTaskID),
		MessageType: agentmessaging.MessageDelegation, SchemaVersion: agentmessaging.SchemaVersionV1, Payload: json.RawMessage(fmt.Sprintf(`{"delegated_task_id":%d}`, recipientTaskID)),
		IdempotencyKey: "delegation:ceo->leader:collision-test", MaxAttempts: 5,
	}
	first, reused, err := ledger.Send(ctx, principalID, base, now)
	if err != nil || reused {
		t.Fatalf("first send: message=%+v reused=%v err=%v", first, reused, err)
	}

	// Same idempotency key, but a genuinely different command: it points the
	// delegation at a different task. Schema-valid on its own -- the point is
	// that the canonical hash differs from the first Send's, not that the
	// payload is malformed.
	colliding := base
	colliding.RecipientTaskID = &otherRecipientTaskID
	colliding.Payload = json.RawMessage(fmt.Sprintf(`{"delegated_task_id":%d}`, otherRecipientTaskID))
	_, _, err = ledger.Send(ctx, principalID, colliding, now.Add(time.Second))
	if !errors.Is(err, agentmessaging.ErrConflict) {
		t.Fatalf("send with reused key and different payload: err=%v want ErrConflict", err)
	}
}

// ORG-AUDIT-002 regression: TopologyV1EdgeContract and the securityaudit
// catalog tests both exercise ValidateEdge directly, never the real
// Store.Send call site. That leaves the actual production topology
// enforcement mechanically unproven: deleting the ValidateEdge call inside
// Send would not fail a single existing test. This proves the deny through
// the real store, against the real registry, the way production traffic
// actually goes.
func TestSendDeniesWorkerToWorkerViaRealStore(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	fixture := openMessagingFixture(t, ctx)
	ledger, err := agentmessagingpostgres.New(fixture.store, fixture.registryReader, fixture.authorizer, 100, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()

	const senderRole = "ingenieria_ia/qa"
	const recipientRole = "ingenieria_ia/frontend"
	senderTaskID := fixture.insertTask(t, ctx, senderRole, 900)
	recipientTaskID := fixture.insertTask(t, ctx, recipientRole, 901)
	principalID := fixture.insertExecutionPrincipal(t, ctx, senderRole, 900)

	command := agentmessaging.SendCommand{
		OrganizationID: messagingIntegrationOrg, SenderRoleID: senderRole, SenderTaskID: senderTaskID,
		RecipientRoleID: recipientRole, RecipientTaskID: &recipientTaskID, CorrelationID: "peer:deny", CausationID: fmt.Sprintf("task:%d", senderTaskID),
		MessageType: agentmessaging.MessageDelegation, SchemaVersion: agentmessaging.SchemaVersionV1, Payload: json.RawMessage(fmt.Sprintf(`{"delegated_task_id":%d}`, recipientTaskID)),
		IdempotencyKey: "peer-deny:qa->frontend", MaxAttempts: 5,
	}
	_, _, err = ledger.Send(ctx, principalID, command, now)
	if err == nil {
		t.Fatal("expected worker-to-worker Send through the real store to be denied by topology, got nil error")
	}
	if !strings.Contains(err.Error(), "topology validation failed") {
		t.Fatalf("Send err=%v, want a topology validation failure (peer workers %s->%s are not a V1 edge)", err, senderRole, recipientRole)
	}
}

// ORG-AUDIT-005 regression: capability-matrix.yaml grants execution_service
// model.invoke/code.* but NOT agent.message.send. This edge is topologically
// legal (worker->own-leader), so topology alone would let it through; only
// the capability check this test proves is wired can reject it.
func TestSendDeniesRoleWithoutMessageSendCapability(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	fixture := openMessagingFixture(t, ctx)
	ledger, err := agentmessagingpostgres.New(fixture.store, fixture.registryReader, fixture.authorizer, 100, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()

	const senderRole = "ingenieria_ia/code-runner" // authority_class: execution_service, no agent.message.send grant
	const recipientRole = "ingenieria_ia/orquestador"
	senderTaskID := fixture.insertTask(t, ctx, senderRole, 950)
	recipientTaskID := fixture.insertTask(t, ctx, recipientRole, 951)
	principalID := fixture.insertExecutionPrincipal(t, ctx, senderRole, 950)

	command := agentmessaging.SendCommand{
		OrganizationID: messagingIntegrationOrg, SenderRoleID: senderRole, SenderTaskID: senderTaskID,
		RecipientRoleID: recipientRole, RecipientTaskID: &recipientTaskID, CorrelationID: "capability:deny", CausationID: fmt.Sprintf("task:%d", senderTaskID),
		MessageType: agentmessaging.MessageDelegation, SchemaVersion: agentmessaging.SchemaVersionV1, Payload: json.RawMessage(fmt.Sprintf(`{"delegated_task_id":%d}`, recipientTaskID)),
		IdempotencyKey: "capability-deny:code-runner->orquestador", MaxAttempts: 5,
	}
	_, _, err = ledger.Send(ctx, principalID, command, now)
	if err == nil {
		t.Fatal("expected Send from a role without agent.message.send to be denied, got nil error")
	}
	if !strings.Contains(err.Error(), "capability check failed") {
		t.Fatalf("Send err=%v, want a capability check failure (execution_service has no agent.message.send grant)", err)
	}
}

func TestSendEnforcesRateLimit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	fixture := openMessagingFixture(t, ctx)
	ledger, err := agentmessagingpostgres.New(fixture.store, fixture.registryReader, fixture.authorizer, 2, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	ceoTaskID := fixture.insertTask(t, ctx, "empresa/ceo", 2)
	principalID := fixture.insertExecutionPrincipal(t, ctx, "empresa/ceo", 2)
	recipientTaskID := fixture.insertTask(t, ctx, "ingenieria_ia/orquestador", 20)

	for i := 0; i < 2; i++ {
		command := agentmessaging.SendCommand{
			OrganizationID: messagingIntegrationOrg, SenderRoleID: "empresa/ceo", SenderTaskID: ceoTaskID,
			RecipientRoleID: "ingenieria_ia/orquestador", RecipientTaskID: &recipientTaskID,
			CorrelationID: "executive:rate", CausationID: fmt.Sprintf("task:%d", ceoTaskID),
			MessageType: agentmessaging.MessageDelegation, SchemaVersion: agentmessaging.SchemaVersionV1, Payload: json.RawMessage(fmt.Sprintf(`{"delegated_task_id":%d}`, recipientTaskID)),
			IdempotencyKey: fmt.Sprintf("status:%d", i), MaxAttempts: 5,
		}
		if _, _, err := ledger.Send(ctx, principalID, command, now); err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
	}
	over := agentmessaging.SendCommand{
		OrganizationID: messagingIntegrationOrg, SenderRoleID: "empresa/ceo", SenderTaskID: ceoTaskID,
		RecipientRoleID: "ingenieria_ia/orquestador", RecipientTaskID: &recipientTaskID,
		CorrelationID: "executive:rate", CausationID: fmt.Sprintf("task:%d", ceoTaskID),
		MessageType: agentmessaging.MessageDelegation, SchemaVersion: agentmessaging.SchemaVersionV1, Payload: json.RawMessage(fmt.Sprintf(`{"delegated_task_id":%d}`, recipientTaskID)),
		IdempotencyKey: "status:over", MaxAttempts: 5,
	}
	if _, _, err := ledger.Send(ctx, principalID, over, now); !errors.Is(err, agentmessaging.ErrRateLimited) {
		t.Fatalf("err=%v want ErrRateLimited", err)
	}
}

func TestClaimAckDeliversMessageExactlyOnce(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	fixture := openMessagingFixture(t, ctx)
	ledger, err := agentmessagingpostgres.New(fixture.store, fixture.registryReader, fixture.authorizer, 100, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	ceoTaskID := fixture.insertTask(t, ctx, "empresa/ceo", 3)
	recipientTaskID := fixture.insertTask(t, ctx, "ingenieria_ia/orquestador", 43)
	principalID := fixture.insertExecutionPrincipal(t, ctx, "empresa/ceo", 3)
	sent, _, err := ledger.Send(ctx, principalID, agentmessaging.SendCommand{
		OrganizationID: messagingIntegrationOrg, SenderRoleID: "empresa/ceo", SenderTaskID: ceoTaskID,
		RecipientRoleID: "ingenieria_ia/orquestador", RecipientTaskID: &recipientTaskID, CorrelationID: "executive:claim", CausationID: fmt.Sprintf("task:%d", ceoTaskID),
		MessageType: agentmessaging.MessageDelegation, SchemaVersion: agentmessaging.SchemaVersionV1, Payload: json.RawMessage(fmt.Sprintf(`{"delegated_task_id":%d}`, recipientTaskID)),
		IdempotencyKey: "claim-ack", MaxAttempts: 5,
	}, now)
	if err != nil {
		t.Fatal(err)
	}

	recipientPrincipalID := fixture.insertExecutionPrincipal(t, ctx, "ingenieria_ia/orquestador", 30)
	claimed, err := ledger.ClaimNext(ctx, recipientPrincipalID, messagingIntegrationOrg, "ingenieria_ia/orquestador", 10, time.Minute, now)
	if err != nil || len(claimed) != 1 || claimed[0].Message.ID != sent.ID {
		t.Fatalf("claimed=%+v err=%v", claimed, err)
	}
	if claimed[0].Message.Status != agentmessaging.StatusClaimed || claimed[0].Message.AttemptCount != 1 {
		t.Fatalf("claimed message=%+v", claimed[0].Message)
	}

	// A second claim attempt must find nothing pending.
	again, err := ledger.ClaimNext(ctx, recipientPrincipalID, messagingIntegrationOrg, "ingenieria_ia/orquestador", 10, time.Minute, now)
	if err != nil || len(again) != 0 {
		t.Fatalf("second claim=%+v err=%v want empty", again, err)
	}

	if err := ledger.Ack(ctx, recipientPrincipalID, agentmessaging.Disposition{MessageID: sent.ID, ConsumerID: "leader-consumer-1", ClaimToken: claimed[0].ClaimToken}, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	// A wrong claim token must be rejected.
	if err := ledger.Ack(ctx, recipientPrincipalID, agentmessaging.Disposition{MessageID: sent.ID, ConsumerID: "leader-consumer-1", ClaimToken: "wrong-token"}, now); !errors.Is(err, agentmessaging.ErrConflict) {
		t.Fatalf("err=%v want ErrConflict", err)
	}
	// A different principal than the one that claimed must never be able
	// to settle the claim, even with the RIGHT token -- this is the
	// stolen-claim-token defense: possession of the token alone is not
	// sufficient authentication.
	otherPrincipalID := fixture.insertExecutionPrincipal(t, ctx, "ingenieria_ia/orquestador", 31)
	if err := ledger.Ack(ctx, otherPrincipalID, agentmessaging.Disposition{MessageID: sent.ID, ConsumerID: "leader-consumer-1", ClaimToken: claimed[0].ClaimToken}, now.Add(time.Second)); err == nil {
		t.Fatal("expected Ack from a different execution principal with the correct token to be rejected")
	}
}

func TestNackRetriesUntilMaxAttemptsThenDeadLetters(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	fixture := openMessagingFixture(t, ctx)
	ledger, err := agentmessagingpostgres.New(fixture.store, fixture.registryReader, fixture.authorizer, 100, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	ceoTaskID := fixture.insertTask(t, ctx, "empresa/ceo", 4)
	recipientTaskID := fixture.insertTask(t, ctx, "ingenieria_ia/orquestador", 44)
	principalID := fixture.insertExecutionPrincipal(t, ctx, "empresa/ceo", 4)
	sent, _, err := ledger.Send(ctx, principalID, agentmessaging.SendCommand{
		OrganizationID: messagingIntegrationOrg, SenderRoleID: "empresa/ceo", SenderTaskID: ceoTaskID,
		RecipientRoleID: "ingenieria_ia/orquestador", RecipientTaskID: &recipientTaskID, CorrelationID: "executive:nack", CausationID: fmt.Sprintf("task:%d", ceoTaskID),
		MessageType: agentmessaging.MessageDelegation, SchemaVersion: agentmessaging.SchemaVersionV1, Payload: json.RawMessage(fmt.Sprintf(`{"delegated_task_id":%d}`, recipientTaskID)),
		IdempotencyKey: "nack-dead-letter", MaxAttempts: 2,
	}, now)
	if err != nil {
		t.Fatal(err)
	}

	recipientPrincipalID := fixture.insertExecutionPrincipal(t, ctx, "ingenieria_ia/orquestador", 40)
	for attempt := 1; attempt <= 2; attempt++ {
		claimed, err := ledger.ClaimNext(ctx, recipientPrincipalID, messagingIntegrationOrg, "ingenieria_ia/orquestador", 10, time.Minute, now)
		if err != nil || len(claimed) != 1 {
			t.Fatalf("attempt %d: claimed=%+v err=%v", attempt, claimed, err)
		}
		if err := ledger.Nack(ctx, recipientPrincipalID, agentmessaging.Disposition{MessageID: sent.ID, ConsumerID: "leader-consumer", ClaimToken: claimed[0].ClaimToken, Error: "consumer failed"}, now); err != nil {
			t.Fatal(err)
		}
	}

	var status string
	var attemptCount int
	if err := fixture.store.Pool().QueryRow(ctx, `SELECT status, attempt_count FROM agent_messages WHERE id=$1`, sent.ID).Scan(&status, &attemptCount); err != nil {
		t.Fatal(err)
	}
	if status != string(agentmessaging.StatusDead) || attemptCount != 2 {
		t.Fatalf("status=%s attempts=%d want dead/2", status, attemptCount)
	}
	// A dead message must never be claimable again.
	claimed, err := ledger.ClaimNext(ctx, recipientPrincipalID, messagingIntegrationOrg, "ingenieria_ia/orquestador", 10, time.Minute, now)
	if err != nil || len(claimed) != 0 {
		t.Fatalf("claimed dead message: %+v err=%v", claimed, err)
	}
}

func TestExpiredClaimIsRecoveredAndOldTokenCannotSettle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	fixture := openMessagingFixture(t, ctx)
	ledger, err := agentmessagingpostgres.New(fixture.store, fixture.registryReader, fixture.authorizer, 100, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	ceoTaskID := fixture.insertTask(t, ctx, "empresa/ceo", 5)
	recipientTaskID := fixture.insertTask(t, ctx, "ingenieria_ia/orquestador", 45)
	principalID := fixture.insertExecutionPrincipal(t, ctx, "empresa/ceo", 5)
	sent, _, err := ledger.Send(ctx, principalID, agentmessaging.SendCommand{
		OrganizationID: messagingIntegrationOrg, SenderRoleID: "empresa/ceo", SenderTaskID: ceoTaskID,
		RecipientRoleID: "ingenieria_ia/orquestador", RecipientTaskID: &recipientTaskID, CorrelationID: "executive:expired", CausationID: fmt.Sprintf("task:%d", ceoTaskID),
		MessageType: agentmessaging.MessageDelegation, SchemaVersion: agentmessaging.SchemaVersionV1, Payload: json.RawMessage(fmt.Sprintf(`{"delegated_task_id":%d}`, recipientTaskID)),
		IdempotencyKey: "expired-recovery", MaxAttempts: 3,
	}, now)
	if err != nil {
		t.Fatal(err)
	}

	recipientPrincipalID := fixture.insertExecutionPrincipal(t, ctx, "ingenieria_ia/orquestador", 50)
	first, err := ledger.ClaimNext(ctx, recipientPrincipalID, messagingIntegrationOrg, "ingenieria_ia/orquestador", 1, time.Minute, now)
	if err != nil || len(first) != 1 {
		t.Fatalf("first claim=%+v err=%v", first, err)
	}
	expiredAt := now.Add(2 * time.Minute)
	if err := ledger.Ack(ctx, recipientPrincipalID, agentmessaging.Disposition{MessageID: sent.ID, ConsumerID: "consumer-old", ClaimToken: first[0].ClaimToken}, expiredAt); !errors.Is(err, agentmessaging.ErrClaimExpired) {
		t.Fatalf("late ack err=%v want ErrClaimExpired", err)
	}

	second, err := ledger.ClaimNext(ctx, recipientPrincipalID, messagingIntegrationOrg, "ingenieria_ia/orquestador", 1, time.Minute, expiredAt)
	if err != nil || len(second) != 1 {
		t.Fatalf("recovered claim=%+v err=%v", second, err)
	}
	if second[0].Message.ID != sent.ID || second[0].Message.AttemptCount != 2 || second[0].ClaimToken == first[0].ClaimToken {
		t.Fatalf("recovered message=%+v old_token_reused=%v", second[0].Message, second[0].ClaimToken == first[0].ClaimToken)
	}
	if err := ledger.Ack(ctx, recipientPrincipalID, agentmessaging.Disposition{MessageID: sent.ID, ConsumerID: "consumer-old", ClaimToken: first[0].ClaimToken}, expiredAt.Add(time.Second)); !errors.Is(err, agentmessaging.ErrConflict) {
		t.Fatalf("old owner/token after reclaim err=%v want ErrConflict", err)
	}
	if err := ledger.Ack(ctx, recipientPrincipalID, agentmessaging.Disposition{MessageID: sent.ID, ConsumerID: "consumer-new", ClaimToken: second[0].ClaimToken}, expiredAt.Add(time.Second)); err != nil {
		t.Fatalf("ack recovered claim: %v", err)
	}
}

func TestExpiredFinalAttemptDeadLettersInsteadOfRequeue(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	fixture := openMessagingFixture(t, ctx)
	ledger, err := agentmessagingpostgres.New(fixture.store, fixture.registryReader, fixture.authorizer, 100, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	ceoTaskID := fixture.insertTask(t, ctx, "empresa/ceo", 6)
	recipientTaskID := fixture.insertTask(t, ctx, "ingenieria_ia/orquestador", 46)
	principalID := fixture.insertExecutionPrincipal(t, ctx, "empresa/ceo", 6)
	sent, _, err := ledger.Send(ctx, principalID, agentmessaging.SendCommand{
		OrganizationID: messagingIntegrationOrg, SenderRoleID: "empresa/ceo", SenderTaskID: ceoTaskID,
		RecipientRoleID: "ingenieria_ia/orquestador", RecipientTaskID: &recipientTaskID, CorrelationID: "executive:expired-dead", CausationID: fmt.Sprintf("task:%d", ceoTaskID),
		MessageType: agentmessaging.MessageDelegation, SchemaVersion: agentmessaging.SchemaVersionV1, Payload: json.RawMessage(fmt.Sprintf(`{"delegated_task_id":%d}`, recipientTaskID)),
		IdempotencyKey: "expired-dead-letter", MaxAttempts: 1,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	recipientPrincipalID := fixture.insertExecutionPrincipal(t, ctx, "ingenieria_ia/orquestador", 60)
	claimed, err := ledger.ClaimNext(ctx, recipientPrincipalID, messagingIntegrationOrg, "ingenieria_ia/orquestador", 1, time.Minute, now)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim=%+v err=%v", claimed, err)
	}

	reclaimed, err := ledger.ClaimNext(ctx, recipientPrincipalID, messagingIntegrationOrg, "ingenieria_ia/orquestador", 1, time.Minute, now.Add(2*time.Minute))
	if err != nil || len(reclaimed) != 0 {
		t.Fatalf("max-attempt expired message was redelivered: %+v err=%v", reclaimed, err)
	}
	var status string
	var attempts int
	var claimedBy, claimTokenHash, lastError *string
	var claimExpiresAt *time.Time
	if err := fixture.store.Pool().QueryRow(ctx, `SELECT status,attempt_count,claimed_by,claim_token_hash,claim_expires_at,last_error FROM agent_messages WHERE id=$1`, sent.ID).
		Scan(&status, &attempts, &claimedBy, &claimTokenHash, &claimExpiresAt, &lastError); err != nil {
		t.Fatal(err)
	}
	if status != string(agentmessaging.StatusDead) || attempts != 1 || claimedBy != nil || claimTokenHash != nil || claimExpiresAt != nil || lastError == nil || *lastError != "claim lease expired" {
		t.Fatalf("dead-letter state status=%s attempts=%d claimed_by=%v token=%v expiry=%v error=%v", status, attempts, claimedBy, claimTokenHash, claimExpiresAt, lastError)
	}
}
