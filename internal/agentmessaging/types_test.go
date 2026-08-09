package agentmessaging

import (
	"encoding/json"
	"errors"
	"testing"
)

func validSendCommand() SendCommand {
	return SendCommand{
		OrganizationID: "explorarte", SenderRoleID: "empresa/ceo", SenderTaskID: 1,
		RecipientRoleID: "ingenieria_ia/orquestador", CorrelationID: "executive:abc", CausationID: "task:1",
		MessageType: MessageDelegation, Payload: json.RawMessage(`{"ok":true}`),
		IdempotencyKey: "delegation:1:2", MaxAttempts: 5,
	}
}

func TestSendCommandValidateAcceptsWellFormedCommand(t *testing.T) {
	if err := validSendCommand().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestSendCommandValidateRejectsEachMissingField(t *testing.T) {
	cases := []func(*SendCommand){
		func(c *SendCommand) { c.OrganizationID = "" },
		func(c *SendCommand) { c.SenderRoleID = "" },
		func(c *SendCommand) { c.RecipientRoleID = "" },
		func(c *SendCommand) { c.SenderTaskID = 0 },
		func(c *SendCommand) { c.CorrelationID = "" },
		func(c *SendCommand) { c.CausationID = "" },
		func(c *SendCommand) { c.MessageType = "unknown" },
		func(c *SendCommand) { c.Payload = nil },
		func(c *SendCommand) { c.Payload = json.RawMessage(`not json`) },
		func(c *SendCommand) { c.IdempotencyKey = "" },
		func(c *SendCommand) { c.MaxAttempts = 0 },
		func(c *SendCommand) { c.MaxAttempts = 101 },
	}
	for i, mutate := range cases {
		command := validSendCommand()
		mutate(&command)
		if err := command.Validate(); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("case %d: err=%v want ErrInvalidRequest", i, err)
		}
	}
}

func TestSendCommandValidateRejectsInvalidRecipientTaskID(t *testing.T) {
	command := validSendCommand()
	bad := int64(-1)
	command.RecipientTaskID = &bad
	if err := command.Validate(); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("err=%v want ErrInvalidRequest", err)
	}
}
