// Package coordinationadapter maps the V2 cross-role coordination port onto
// the existing authorized agent-message ledger.
package coordinationadapter

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/agentmessaging"
	"github.com/Mireuz13/explorarte-organization/internal/workflowruntime"
)

type Clock interface{ Now() time.Time }

type Adapter struct {
	Ledger      agentmessaging.Ledger
	Clock       Clock
	MaxAttempts int
}

func New(ledger agentmessaging.Ledger, clock Clock, maxAttempts int) (*Adapter, error) {
	if ledger == nil || clock == nil || maxAttempts < 1 || maxAttempts > 100 {
		return nil, errors.New("workflow coordination adapter dependencies are invalid")
	}
	return &Adapter{Ledger: ledger, Clock: clock, MaxAttempts: maxAttempts}, nil
}

func (a *Adapter) Send(ctx context.Context, command workflowruntime.CoordinationCommand, sender, recipient workflowruntime.Snapshot) (workflowruntime.CoordinationRecord, bool, error) {
	messageType := agentmessaging.MessageDelegation
	payload, _ := json.Marshal(agentmessaging.DelegationPayloadV1{DelegatedTaskID: recipient.TaskID})
	if command.Kind == workflowruntime.CoordinationCompletion {
		messageType = agentmessaging.MessageCompletion
		payload, _ = json.Marshal(agentmessaging.CompletionPayloadV1{CompletedTaskID: sender.TaskID})
	}
	recipientTaskID := recipient.TaskID
	message, reused, err := a.Ledger.Send(ctx, command.Actor.ExecutionPrincipalID, agentmessaging.SendCommand{
		OrganizationID: command.OrganizationID, SenderRoleID: sender.AssignedRoleID, SenderTaskID: sender.TaskID,
		RecipientRoleID: recipient.AssignedRoleID, RecipientTaskID: &recipientTaskID,
		CorrelationID: command.CorrelationID, CausationID: command.CausationID, MessageType: messageType,
		Payload: payload, IdempotencyKey: command.IdempotencyKey, MaxAttempts: a.MaxAttempts,
		SchemaVersion: agentmessaging.SchemaVersionV1,
	}, a.Clock.Now())
	if err != nil {
		return workflowruntime.CoordinationRecord{}, false, err
	}
	return workflowruntime.CoordinationRecord{
		ID: message.ID, OrganizationID: message.OrganizationID, SenderRoleID: message.SenderRoleID,
		SenderTaskID: message.SenderTaskID, RecipientRoleID: message.RecipientRoleID,
		RecipientTaskID: recipientTaskID, CorrelationID: message.CorrelationID, CausationID: message.CausationID,
		Kind: command.Kind, IdempotencyKey: message.IdempotencyKey,
		DurableProvenance: "agent_messages:" + strconv.FormatInt(message.ID, 10),
	}, reused, nil
}

var _ workflowruntime.CoordinationPort = (*Adapter)(nil)
