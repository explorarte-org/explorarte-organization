package runtimeadapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/agentmessaging"
	"github.com/Mireuz13/explorarte-organization/internal/executive"
	"github.com/Mireuz13/explorarte-organization/internal/modeldispatch"
)

// AgentMessages adapts internal/agentmessaging.Ledger to
// executive.AgentMessagingProvider.
// REQUIRES authentication via configured execution principal - NO fallback.
type AgentMessages struct {
	Ledger              agentmessaging.Ledger
	MaxAttempts         int
	PrincipalStore      modeldispatch.PrincipalStore
	OrganizationID      string
	ConfiguredPrincipal string // Key of the authenticated execution principal
}

var (
	ErrNoActivePrincipal     = errors.New("no active execution principal found")
	ErrPrincipalDisabled     = errors.New("execution principal is disabled")
	ErrPrincipalOrgMismatch  = errors.New("principal organization mismatch")
	ErrPrincipalRoleMismatch = errors.New("principal dispatch_actor_role_id does not match sender role")
	ErrEmptyPrincipal        = errors.New("empty execution principal ID provided")
)

var _ executive.AgentMessagingProvider = AgentMessages{}

// resolvePrincipalByKey resolves an execution principal by its configured key and validates it's active.
func (a AgentMessages) resolvePrincipalByKey(ctx context.Context, principalKey string) (modeldispatch.ExecutionPrincipal, error) {
	if strings.TrimSpace(principalKey) == "" {
		return modeldispatch.ExecutionPrincipal{}, ErrEmptyPrincipal
	}

	principal, err := a.PrincipalStore.ResolveByKey(ctx, a.OrganizationID, principalKey)
	if err != nil {
		return modeldispatch.ExecutionPrincipal{}, fmt.Errorf("%w: %v", ErrNoActivePrincipal, err)
	}

	// Validate organization matches our org
	if principal.OrganizationID != a.OrganizationID {
		return modeldispatch.ExecutionPrincipal{}, ErrPrincipalOrgMismatch
	}

	// Validate status is active
	if principal.Status != modeldispatch.PrincipalActive {
		return modeldispatch.ExecutionPrincipal{}, ErrPrincipalDisabled
	}

	return principal, nil
}

// getPrincipalByID resolves an execution principal by database ID.
func (a AgentMessages) getPrincipalByID(ctx context.Context, id int64) (modeldispatch.ExecutionPrincipal, error) {
	principal, err := a.PrincipalStore.GetPrincipal(ctx, id)
	if err != nil {
		return modeldispatch.ExecutionPrincipal{}, fmt.Errorf("%w: %v", ErrNoActivePrincipal, err)
	}

	if principal.OrganizationID != a.OrganizationID {
		return modeldispatch.ExecutionPrincipal{}, ErrPrincipalOrgMismatch
	}
	if principal.Status != modeldispatch.PrincipalActive {
		return modeldispatch.ExecutionPrincipal{}, ErrPrincipalDisabled
	}

	return principal, nil
}

// validateSenderRoleWithPrincipal validates that principal.dispatch_actor_role_id == sender.role.
func validateSenderRoleWithPrincipal(principal modeldispatch.ExecutionPrincipal, senderRoleID string) error {
	if principal.DispatchActorRoleID != senderRoleID {
		return fmt.Errorf("%w: principal has dispatch_actor_role_id=%q but sender is %q",
			ErrPrincipalRoleMismatch, principal.DispatchActorRoleID, senderRoleID)
	}
	return nil
}

func (a AgentMessages) SendDelegation(ctx context.Context, executionPrincipalID string, sender, recipient executive.TaskRecord, now time.Time) error {
	// CRITICAL FIX 6: No placeholder allowed in production
	if strings.TrimSpace(executionPrincipalID) == "" {
		return fmt.Errorf("execution principal required: no placeholder allowed in production")
	}

	// Resolve principal using the configured key from bootstrap wiring
	var principal modeldispatch.ExecutionPrincipal
	var err error

	if a.ConfiguredPrincipal != "" && executionPrincipalID == a.ConfiguredPrincipal {
		// Fast path: use pre-resolved principal key
		principal, err = a.resolvePrincipalByKey(ctx, a.ConfiguredPrincipal)
	} else if principalID, parseErr := strconv.ParseInt(executionPrincipalID, 10, 64); parseErr == nil && principalID > 0 {
		// Try to parse as numeric ID.
		//
		// This previously used fmt.Sscanf(id, "%d", new(int)), which returns
		// the COUNT of items scanned rather than the value scanned -- the
		// parsed integer went into a discarded new(int), so every numeric
		// principal resolved to ID 1. The adapter then validated principal 1
		// against the sender role while passing the original string on to
		// Ledger.Send, which resolves the real principal: the defence-in-depth
		// check was guarding a different subject than the one actually used.
		principal, err = a.getPrincipalByID(ctx, principalID)
	} else {
		// Last resort: try ResolveByKey with the provided string as key
		principal, err = a.resolvePrincipalByKey(ctx, executionPrincipalID)
	}

	if err != nil {
		return fmt.Errorf("principal resolution failed: %w", err)
	}

	// Validate principal is bound to sender role (NOT spoofable from task/sender input)
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

	_, _, err = a.Ledger.Send(ctx, executionPrincipalID, cmd, now)
	return err
}

func (a AgentMessages) SendCompletion(ctx context.Context, executionPrincipalID string, sender, recipient executive.TaskRecord, now time.Time) error {
	// Same critical principal validation as SendDelegation
	if strings.TrimSpace(executionPrincipalID) == "" {
		return fmt.Errorf("execution principal required: no placeholder allowed in production")
	}

	var principal modeldispatch.ExecutionPrincipal
	var err error

	if a.ConfiguredPrincipal != "" && executionPrincipalID == a.ConfiguredPrincipal {
		principal, err = a.resolvePrincipalByKey(ctx, a.ConfiguredPrincipal)
	} else if id, perr := strconv.ParseInt(executionPrincipalID, 10, 64); perr == nil && id > 0 {
		principal, err = a.getPrincipalByID(ctx, id)
	} else {
		principal, err = a.resolvePrincipalByKey(ctx, executionPrincipalID)
	}

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

	_, _, err = a.Ledger.Send(ctx, executionPrincipalID, cmd, now)
	return err
}
