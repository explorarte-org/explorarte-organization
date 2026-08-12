package agentmessagingfixtures

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/agentmessaging"
	agentmessagingpostgres "github.com/Mireuz13/explorarte-organization/internal/agentmessaging/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/evaluation/fixtures"
	"github.com/Mireuz13/explorarte-organization/internal/evaluationdb"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
)

const (
	fixtureOrganization = "explorarte"
	fixtureSender       = "ingenieria_ia/orquestador"
	fixtureRecipient    = "ingenieria_ia/qa"
	fixtureUnit         = "ingenieria_ia"
	// FIX 6: Execution principal required for ALL ledger operations.
	// In production this comes from ORG_MODEL_EXECUTION_PRINCIPAL_KEY config.
	// For fixtures we use a hardcoded ID that MUST exist as an active principal
	// in the disposable test database's model_execution_principals table.
	fixtureExecutionPrincipalID = "1" // Must match an actual principal DB row
)

// Runner exercises lease expiry, recovery, stale-token rejection and the
// terminal dead-letter path through the production inbox implementation.
type Runner struct{ Store *platformpostgres.Store }

var _ fixtures.Runner = Runner{}

func (Runner) Supports(f fixtures.Fixture) bool {
	return f.ID == fixtureLeaseRecovery && f.RunnerKind == "agentmessaging" && f.Status == fixtures.StatusRunnerReady
}

func (r Runner) Run(ctx context.Context, f fixtures.Fixture, subjectID string) (fixtures.RunOutcome, error) {
	if err := evaluationdb.RequireDisposable(ctx, r.Store); err != nil {
		return fixtures.RunOutcome{}, err
	}
	scenario, ok := f.Scenario.(*Scenario)
	if !ok || scenario.FixtureID != f.ID {
		return fixtures.RunOutcome{}, fmt.Errorf("fixture %s was not activated by agentmessagingfixtures.Activate", f.ID)
	}
	registryReader, err := registry.NewPostgresRepository(r.Store)
	if err != nil {
		return fixtures.RunOutcome{}, err
	}
	ledger, err := agentmessagingpostgres.New(r.Store, registryReader, 1_000_000, 24*time.Hour)
	if err != nil {
		return fixtures.RunOutcome{}, err
	}
	record := newRecorder(f, subjectID)
	prefix := "r30-10-" + stableSuffix(subjectID)
	// Replaying an evaluation must start from the same inbox state. This
	// narrowly removes only this synthetic subject's two rows, after the
	// server-side disposable-database check above. All behavior under test is
	// still driven through Send/ClaimNext/Ack below.
	if _, err := r.Store.Pool().Exec(ctx, `DELETE FROM agent_messages WHERE organization_id=$1 AND idempotency_key IN ($2,$3)`, fixtureOrganization, prefix+"-recover", prefix+"-dead"); err != nil {
		return fixtures.RunOutcome{}, fmt.Errorf("reset messaging fixture rows: %w", err)
	}
	taskID, err := r.ensureTask(ctx, prefix)
	if err != nil {
		return fixtures.RunOutcome{}, err
	}

	// FIX 6: All ledger operations now require execution principal authentication.
	base := time.Unix(f.Seed, 0).UTC()

	recoveryCommand := agentmessaging.SendCommand{
		OrganizationID: fixtureOrganization, SenderRoleID: fixtureSender, SenderTaskID: taskID,
		RecipientRoleID: fixtureRecipient, CorrelationID: prefix + "-correlation", CausationID: fmt.Sprintf("task:%d", taskID),
		MessageType:    agentmessaging.MessageDelegation,
		Payload:        json.RawMessage(`{"fixture":"lease-recovery"}`),
		IdempotencyKey: prefix + "-recover", MaxAttempts: 3,
		SchemaVersion: agentmessaging.SchemaVersionV1,
		RequestHash:   computeRequestHash(fixtureOrganization, fixtureSender, taskID, fixtureRecipient, prefix+"-correlation", prefix+"-causation", 3, fixtureRecipient, prefix+"-recover"),
	}
	sent, reused, err := ledger.Send(ctx, fixtureExecutionPrincipalID, recoveryCommand, base)
	if err != nil {
		return fixtures.RunOutcome{}, err
	}
	record.record("first_send_creates_exactly_one_message", !reused)
	retried, reused, err := ledger.Send(ctx, fixtureExecutionPrincipalID, recoveryCommand, base.Add(time.Second))
	if err != nil {
		return fixtures.RunOutcome{}, err
	}
	record.record("idempotent_send_never_duplicates_message", reused && retried.ID == sent.ID)
	activeBefore, err := r.activeCount(ctx, sent.ID)
	if err != nil {
		return fixtures.RunOutcome{}, err
	}

	lease := time.Minute
	// FIX 6: ClaimNext requires executionPrincipalID (must match recipient role scope).
	first, err := ledger.ClaimNext(ctx, fixtureExecutionPrincipalID, fixtureOrganization, fixtureRecipient, 1, lease, base)
	if err != nil {
		return fixtures.RunOutcome{}, err
	}
	if len(first) != 1 || first[0].Message.ID != sent.ID {
		return fixtures.RunOutcome{}, fmt.Errorf("first claim selected %+v, want message %d", first, sent.ID)
	}
	expiredAt := base.Add(2 * time.Minute)
	// Ack/Nack require execution principal + token verification.
	// FIX 6: ConsumerID set to executionPrincipalID, NOT arbitrary string.
	lateErr := ledger.Ack(ctx, fixtureExecutionPrincipalID, agentmessaging.Disposition{
		MessageID: sent.ID, ConsumerID: fixtureExecutionPrincipalID, ClaimToken: first[0].ClaimToken,
	}, expiredAt)
	record.record("expired_owner_cannot_ack_message", errors.Is(lateErr, agentmessaging.ErrClaimExpired))

	second, err := ledger.ClaimNext(ctx, fixtureExecutionPrincipalID, fixtureOrganization, fixtureRecipient, 1, lease, expiredAt)
	if err != nil {
		return fixtures.RunOutcome{}, err
	}
	if len(second) != 1 || second[0].Message.ID != sent.ID {
		return fixtures.RunOutcome{}, fmt.Errorf("recovery claim selected %+v, want message %d", second, sent.ID)
	}
	recovered := second[0]
	record.record("expired_lease_is_recovered_by_second_consumer", recovered.Message.AttemptCount == 2 && recovered.ClaimToken != first[0].ClaimToken)
	activeAfter, err := r.activeCount(ctx, sent.ID)
	if err != nil {
		return fixtures.RunOutcome{}, err
	}
	record.record("recovery_preserves_one_active_message", activeBefore == 1 && activeAfter == 1)

	staleErr := ledger.Ack(ctx, fixtureExecutionPrincipalID, agentmessaging.Disposition{
		MessageID: sent.ID, ConsumerID: fixtureExecutionPrincipalID, ClaimToken: first[0].ClaimToken,
	}, expiredAt.Add(time.Second))
	record.record("old_claim_token_never_settles_recovered_message", errors.Is(staleErr, agentmessaging.ErrConflict))
	if err := ledger.Ack(ctx, fixtureExecutionPrincipalID, agentmessaging.Disposition{
		MessageID: sent.ID, ConsumerID: fixtureExecutionPrincipalID, ClaimToken: recovered.ClaimToken,
	}, expiredAt.Add(time.Second)); err != nil {
		return fixtures.RunOutcome{}, fmt.Errorf("ack recovered message: %w", err)
	}

	deadCommand := recoveryCommand
	deadCommand.Payload = json.RawMessage(`{"fixture":"expired-dead-letter"}`)
	deadCommand.IdempotencyKey = prefix + "-dead"
	deadCommand.CorrelationID = prefix + "-dead-correlation"
	deadCommand.MaxAttempts = 1
	deadCommand.RequestHash = computeRequestHash(fixtureOrganization, fixtureSender, taskID, fixtureRecipient, prefix+"-dead-correlation", prefix+"-dead-causation", 1, fixtureRecipient, prefix+"-dead")
	dead, _, err := ledger.Send(ctx, fixtureExecutionPrincipalID, deadCommand, base.Add(10*time.Minute))
	if err != nil {
		return fixtures.RunOutcome{}, err
	}
	deadClaim, err := ledger.ClaimNext(ctx, fixtureExecutionPrincipalID, fixtureOrganization, fixtureRecipient, 1, lease, base.Add(10*time.Minute))
	if err != nil {
		return fixtures.RunOutcome{}, err
	}
	if len(deadClaim) != 1 || deadClaim[0].Message.ID != dead.ID {
		return fixtures.RunOutcome{}, fmt.Errorf("dead-letter first claim selected %+v, want message %d", deadClaim, dead.ID)
	}
	redelivered, err := ledger.ClaimNext(ctx, fixtureExecutionPrincipalID, fixtureOrganization, fixtureRecipient, 1, lease, base.Add(12*time.Minute))
	if err != nil {
		return fixtures.RunOutcome{}, err
	}
	deadRedelivered := false
	for _, claimed := range redelivered {
		if claimed.Message.ID == dead.ID {
			deadRedelivered = true
		}
	}
	var deadStatus string
	var deadAttempts int
	var deadLastError *string
	if err := r.Store.Pool().QueryRow(ctx, `SELECT status, attempt_count, last_error FROM agent_messages WHERE id=$1`, dead.ID).Scan(&deadStatus, &deadAttempts, &deadLastError); err != nil {
		return fixtures.RunOutcome{}, err
	}
	record.record("max_attempts_message_never_redelivers", !deadRedelivered && deadStatus == string(agentmessaging.StatusDead) && deadAttempts == 1)
	record.record("expired_lease_recovery_is_accounted_for", deadLastError != nil && *deadLastError == "claim lease expired")

	record.outcome.Metrics["recovered_attempt_count"] = float64(recovered.Message.AttemptCount)
	record.outcome.Metrics["active_message_count_before"] = float64(activeBefore)
	record.outcome.Metrics["active_message_count_after"] = float64(activeAfter)
	record.outcome.Metrics["dead_attempt_count"] = float64(deadAttempts)
	record.outcome.EvidenceRefs = append(record.outcome.EvidenceRefs,
		fmt.Sprintf("agent-message:%d:delivered-after-recovery", sent.ID),
		fmt.Sprintf("agent-message:%d:dead-after-expiry", dead.ID),
	)
	return record.finish("lease expirada recuperada por ClaimNext; intento final expirado enviado a dead-letter"), nil
}

func (r Runner) ensureTask(ctx context.Context, prefix string) (int64, error) {
	var revisionID int64
	if err := r.Store.Pool().QueryRow(ctx, `SELECT COALESCE(current_revision_id,0) FROM organizations WHERE id=$1 AND retired_at IS NULL`, fixtureOrganization).Scan(&revisionID); err != nil {
		return 0, fmt.Errorf("load current organization revision: %w", err)
	}
	if revisionID == 0 {
		return 0, fmt.Errorf("organization %s has no current canonical revision", fixtureOrganization)
	}
	key := prefix + "-task"
	hash := sha256.Sum256([]byte(key))
	now := time.Unix(0, 0).UTC()
	var id int64
	err := r.Store.Pool().QueryRow(ctx, `
INSERT INTO tasks (
 organization_id,organization_revision_id,requested_by_role_id,assigned_role_id,assigned_unit_id,
 idempotency_key,request_hash,title,instructions,acceptance_criteria,status,priority,available_at,
 max_attempts,attempt_count,version,created_at,updated_at
) VALUES ($1,$2,'empresa/ceo',$3,$4,$5,$6,$7,$8,'[]'::jsonb,'running',0,$9,1,1,1,$9,$9)
ON CONFLICT (organization_id,idempotency_key) DO NOTHING
RETURNING id`, fixtureOrganization, revisionID, fixtureSender, fixtureUnit, key, hex.EncodeToString(hash[:]), "R30 messaging lease fixture", "durable synthetic lease recovery", now).Scan(&id)
	if err == nil {
		return id, nil
	}
	if selectErr := r.Store.Pool().QueryRow(ctx, `SELECT id FROM tasks WHERE organization_id=$1 AND idempotency_key=$2`, fixtureOrganization, key).Scan(&id); selectErr != nil {
		return 0, fmt.Errorf("create or load messaging fixture task: insert=%v select=%w", err, selectErr)
	}
	return id, nil
}

func (r Runner) activeCount(ctx context.Context, messageID int64) (int, error) {
	var count int
	err := r.Store.Pool().QueryRow(ctx, `SELECT count(*) FROM agent_messages WHERE id=$1 AND status IN ('pending','claimed')`, messageID).Scan(&count)
	return count, err
}

type recorder struct {
	outcome fixtures.RunOutcome
	passed  bool
}

func newRecorder(f fixtures.Fixture, subjectID string) *recorder {
	return &recorder{passed: true, outcome: fixtures.RunOutcome{
		FixtureID: f.ID, SubjectID: subjectID, InvariantResults: map[string]bool{}, Metrics: map[string]float64{},
		EvidenceRefs: append([]string(nil), f.ExpectedEvidence...),
	}}
}

func (r *recorder) record(name string, passed bool) {
	r.outcome.InvariantResults[name] = passed
	if !passed {
		r.passed = false
		r.outcome.ViolatedInvariants = append(r.outcome.ViolatedInvariants, name)
	}
}

func (r *recorder) finish(notes string) fixtures.RunOutcome {
	r.outcome.Passed = r.passed
	r.outcome.Notes = notes
	return r.outcome
}

func stableSuffix(subjectID string) string {
	sum := sha256.Sum256([]byte(subjectID))
	return hex.EncodeToString(sum[:6])
}

// computeRequestHash computes SHA-256 hash over semantically relevant fields
// for idempotency collision detection. Mirrors postgres/store.computeCanonicalRequestHash.
func computeRequestHash(orgID, senderRole string, senderTask int64, recipientRole string,
	correlationID, causationID string, maxAttempts int, recipientTaskIDRef string, idempotencyKey string) string {
	digest := sha256.Sum256([]byte(fmt.Sprintf("send-v1|%s|%s|%d|%s|%s|%s|%d|%s|%s",
		orgID, senderRole, senderTask, recipientRole, correlationID, causationID, maxAttempts, recipientTaskIDRef, idempotencyKey)))
	return hex.EncodeToString(digest[:])
}
