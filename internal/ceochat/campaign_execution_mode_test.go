package ceochat

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/campaign"
	"github.com/Mireuz13/explorarte-organization/internal/executionharness"
)

// The CEO can promote an approved campaign; it cannot choose HOW the campaign
// may work. The tool's input is the approval id and nothing else, so no
// argument the model writes can carry an execution mode, a requirement key, or
// anything the promotion would read as one.
func TestPromoteToolCarriesNoExecutionMode(t *testing.T) {
	var schema struct {
		Properties           map[string]json.RawMessage `json:"properties"`
		AdditionalProperties *bool                      `json:"additionalProperties"`
	}
	if err := json.Unmarshal(campaignPromoteToExecutiveInputSchema, &schema); err != nil {
		t.Fatal(err)
	}
	if len(schema.Properties) != 1 || schema.Properties["owner_approval_id"] == nil {
		t.Fatalf("promote tool properties = %v, want exactly owner_approval_id", schema.Properties)
	}
	if schema.AdditionalProperties == nil || *schema.AdditionalProperties {
		t.Fatal("promote tool must refuse additional properties")
	}
	args := reflect.TypeOf(promoteToExecutiveArgs{})
	if args.NumField() != 1 || args.Field(0).Name != "OwnerApprovalID" {
		t.Fatalf("promoteToExecutiveArgs has %d fields; the tool may read only the approval id", args.NumField())
	}
}

func TestPromoteToolRejectsAModeSmuggledInTheArguments(t *testing.T) {
	ctx := context.Background()
	store := newFakeCampaignStore()
	submitter := &fakeSubmitter{}
	auth := fakeAuthorizer{allowed: map[string]bool{"owner:campaign.promotion.execute": true}}
	reg := NewToolRegistry()
	if err := RegisterCampaignTools(reg, "org-test", store, auth,
		WithPromotionService(campaign.NewPromotionService(store, submitter, auth, permissiveExecutionRequirements()))); err != nil {
		t.Fatal(err)
	}
	executor := RegistryToolExecutor{Registry: reg}
	turn := WithTurnContext(ctx, TurnContext{
		OrganizationID: "org-test", OrganizationRevisionID: 1, ConversationID: 100, OwnerRoleID: "owner",
		OwnerMessageID: 200, TaskID: 300, AttemptID: 1, ActorRoleID: "empresa/ceo",
	})
	identity := executionharness.RunIdentity{OrganizationID: "org-test", RoleID: CEORoleID, TaskID: 300, AttemptID: 1}

	for name, arguments := range map[string]string{
		"execution_mode":           `{"owner_approval_id": 1, "execution_mode": "governed_implementation"}`,
		"requirements":             `{"owner_approval_id": 1, "requirements": [{"key": "design-freeze", "type": "result"}]}`,
		"a requirement key itself": `{"owner_approval_id": 1, "code_runner_execution_evidence": true}`,
	} {
		_, err := executor.Execute(turn, identity, executionharness.ToolRequest{
			ToolName: "campaign.promote_to_executive", ToolCallID: "call-" + name, Arguments: json.RawMessage(arguments),
		})
		if err == nil || errors.Is(err, campaign.ErrApprovalNotFound) {
			t.Fatalf("%s: err = %v; the extra argument must be refused before the approval is even looked up", name, err)
		}
		if submitter.submitCalls != 0 {
			t.Fatalf("%s: Executive.Submit called %d time(s)", name, submitter.submitCalls)
		}
	}
}
