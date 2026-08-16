package runtimeadapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/agentmessaging"
	"github.com/Mireuz13/explorarte-organization/internal/executive"
	"github.com/Mireuz13/explorarte-organization/internal/modeldispatch"
)

// AgentMessages adapts internal/agentmessaging.Ledger to
// executive.AgentMessagingProvider.
//
// EXEC-PRINCIPAL-001: a single static execution principal cannot
// authenticate a multi-hop executive flow (CEO->leader, leader->worker,
// worker->leader, leader->CEO each have a different sender role), because
// agent-messaging's own hardening requires
// principal.dispatch_actor_role_id == sender.AssignedRoleID for every
// send. This adapter resolves -- server-side, from the sender TaskRecord's
// own persisted, already-registry-validated AssignedRoleID, never from
// caller/model/task-text input -- the one active principal bound to that
// role (roleBoundPrincipal), lazily provisioning it on first use if none
// exists yet. See migration 000048 for the database-enforced "at most one
// active principal per role" invariant this depends on.
type AgentMessages struct {
	Ledger         agentmessaging.Ledger
	MaxAttempts    int
	PrincipalStore modeldispatch.PrincipalStore
	OrganizationID string
}

// ErrNoActivePrincipal used to be returned here when the principal store
// failed. It no longer exists: a store that could not answer is reported with
// its own typed cause, and an identity that exists but is unusable is
// ErrRoleBoundPrincipalNotActive. Neither is "no principal found".
var ErrPrincipalRoleMismatch = errors.New("principal dispatch_actor_role_id does not match sender role")

var _ executive.AgentMessagingProvider = AgentMessages{}

// resolveOrProvisionPrincipalForRole delegates to the shared resolver so that
// messaging and every other consumer of role-bound identity derive the same
// principal from the same rows. The mechanism lives in
// RoleBoundPrincipalResolver; the messaging policy around it stays here.
func (a AgentMessages) resolveOrProvisionPrincipalForRole(ctx context.Context, roleID string) (modeldispatch.ExecutionPrincipal, error) {
	return RoleBoundPrincipalResolver{Principals: a.PrincipalStore, OrganizationID: a.OrganizationID}.Resolve(ctx, roleID)
}

// validateSenderRoleWithPrincipal validates that principal.dispatch_actor_role_id == sender.role.
//
// Defense in depth: resolveOrProvisionPrincipalForRole already resolves
// strictly by role, so this can only fail if the resolver's own contract is
// violated -- but internal/agentmessaging/postgres.Store.Send re-derives and
// re-checks the same invariant independently on the ledger side regardless,
// and this layer keeps its own check rather than trust the layer below it
// alone.
func validateSenderRoleWithPrincipal(principal modeldispatch.ExecutionPrincipal, senderRoleID string) error {
	if principal.DispatchActorRoleID != senderRoleID {
		return fmt.Errorf("%w: principal has dispatch_actor_role_id=%q but sender is %q",
			ErrPrincipalRoleMismatch, principal.DispatchActorRoleID, senderRoleID)
	}
	return nil
}

func (a AgentMessages) SendDelegation(ctx context.Context, sender, recipient executive.TaskRecord, now time.Time) error {
	principal, err := a.resolveOrProvisionPrincipalForRole(ctx, sender.AssignedRoleID)
	if err != nil {
		return fmt.Errorf("principal resolution failed: %w", err)
	}
	if err := validateSenderRoleWithPrincipal(principal, sender.AssignedRoleID); err != nil {
		return fmt.Errorf("sender role validation failed: %w", err)
	}

	recipientTaskID := recipient.ID

	// Fixed schema payload with semantic invariant: DelegatedTaskID MUST equal RecipientTaskID
	payload := agentmessaging.DelegationPayloadV1{DelegatedTaskID: recipient.ID}
	payloadBytes, _ := json.Marshal(payload)

	cmd := agentmessaging.SendCommand{
		OrganizationID:  recipient.OrganizationID,
		SenderRoleID:    sender.AssignedRoleID,
		SenderTaskID:    sender.ID,
		RecipientRoleID: recipient.AssignedRoleID,
		RecipientTaskID: &recipientTaskID,
		CorrelationID:   recipient.CorrelationID,
		CausationID:     recipient.CausationID,
		MessageType:     agentmessaging.MessageDelegation,
		Payload:         json.RawMessage(payloadBytes),
		IdempotencyKey:  fmt.Sprintf("delegation:%d", recipient.ID),
		MaxAttempts:     a.MaxAttempts,
		SchemaVersion:   agentmessaging.SchemaVersionV1,
	}

	// Ledger.Send's executionPrincipalID is the principal's numeric database
	// ID as a string (see internal/agentmessaging/postgres.Store.
	// validateExecutionPrincipalForSender's "WHERE id = $1"), not its key --
	// passing the key here (as this adapter previously did, unconditionally
	// forwarding whatever string the caller supplied) made the ledger's own
	// defense-in-depth re-validation fail on every call for a non-numeric
	// key, which is exactly what oracle-01/model-runtime-01 always was.
	_, _, err = a.Ledger.Send(ctx, strconv.FormatInt(principal.ID, 10), cmd, now)
	return err
}

func (a AgentMessages) SendCompletion(ctx context.Context, sender, recipient executive.TaskRecord, now time.Time) error {
	principal, err := a.resolveOrProvisionPrincipalForRole(ctx, sender.AssignedRoleID)
	if err != nil {
		return fmt.Errorf("principal resolution failed: %w", err)
	}
	if err := validateSenderRoleWithPrincipal(principal, sender.AssignedRoleID); err != nil {
		return fmt.Errorf("sender role validation failed: %w", err)
	}

	recipientTaskID := recipient.ID

	// Fixed schema payload with semantic invariant: CompletedTaskID MUST equal SenderTaskID
	payload := agentmessaging.CompletionPayloadV1{CompletedTaskID: sender.ID}
	payloadBytes, _ := json.Marshal(payload)

	cmd := agentmessaging.SendCommand{
		OrganizationID:  sender.OrganizationID,
		SenderRoleID:    sender.AssignedRoleID,
		SenderTaskID:    sender.ID,
		RecipientRoleID: recipient.AssignedRoleID,
		RecipientTaskID: &recipientTaskID,
		CorrelationID:   sender.CorrelationID,
		CausationID:     sender.CausationID,
		MessageType:     agentmessaging.MessageCompletion,
		Payload:         json.RawMessage(payloadBytes),
		IdempotencyKey:  fmt.Sprintf("completion:%d", sender.ID),
		MaxAttempts:     a.MaxAttempts,
		SchemaVersion:   agentmessaging.SchemaVersionV1,
	}

	_, _, err = a.Ledger.Send(ctx, strconv.FormatInt(principal.ID, 10), cmd, now)
	return err
}
