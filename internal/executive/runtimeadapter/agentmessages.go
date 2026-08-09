package runtimeadapter

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/agentmessaging"
	"github.com/Mireuz13/explorarte-organization/internal/executive"
)

// AgentMessages adapts internal/agentmessaging.Ledger to
// executive.AgentMessagingProvider.
type AgentMessages struct {
	Ledger      agentmessaging.Ledger
	MaxAttempts int
}

var _ executive.AgentMessagingProvider = AgentMessages{}

func (a AgentMessages) SendDelegation(ctx context.Context, sender, recipient executive.TaskRecord, now time.Time) error {
	recipientTaskID := recipient.ID
	_, _, err := a.Ledger.Send(ctx, agentmessaging.SendCommand{
		OrganizationID: recipient.OrganizationID, SenderRoleID: sender.AssignedRoleID, SenderTaskID: sender.ID,
		RecipientRoleID: recipient.AssignedRoleID, RecipientTaskID: &recipientTaskID,
		CorrelationID: recipient.CorrelationID, CausationID: recipient.CausationID,
		MessageType: agentmessaging.MessageDelegation, Payload: json.RawMessage(fmt.Sprintf(`{"delegated_task_id":%d}`, recipient.ID)),
		IdempotencyKey: fmt.Sprintf("delegation:%d", recipient.ID), MaxAttempts: a.MaxAttempts,
	}, now)
	return err
}

func (a AgentMessages) SendCompletion(ctx context.Context, sender, recipient executive.TaskRecord, now time.Time) error {
	recipientTaskID := recipient.ID
	_, _, err := a.Ledger.Send(ctx, agentmessaging.SendCommand{
		OrganizationID: sender.OrganizationID, SenderRoleID: sender.AssignedRoleID, SenderTaskID: sender.ID,
		RecipientRoleID: recipient.AssignedRoleID, RecipientTaskID: &recipientTaskID,
		CorrelationID: sender.CorrelationID, CausationID: sender.CausationID,
		MessageType: agentmessaging.MessageCompletion, Payload: json.RawMessage(fmt.Sprintf(`{"completed_task_id":%d}`, sender.ID)),
		IdempotencyKey: fmt.Sprintf("completion:%d", sender.ID), MaxAttempts: a.MaxAttempts,
	}, now)
	return err
}
