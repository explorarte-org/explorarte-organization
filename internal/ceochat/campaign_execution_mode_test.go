package ceochat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
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

// The mode is durable provenance, so the CEO must be able to SEE it: a governed
// promotion read back through either tool reports governed_implementation, and a
// legacy row (no mode) reports analysis_only. The tool still cannot set it.
func TestPromotionProjectionsReportTheExecutionMode(t *testing.T) {
	ctx := context.Background()
	store := newFakeCampaignStore()
	submitter := &fakeSubmitter{}
	auth := fakeAuthorizer{allowed: map[string]bool{
		"owner:campaign.promotion.execute": true, "owner:campaign.promotion.read": true, "empresa/ceo:campaign.promotion.read": true,
	}}
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
	hash := func(c string) string { return strings.Repeat(c, 64) }

	seed := func(approvalID int64, mode campaign.ExecutionMode) campaign.CampaignPromotion {
		prom, _, err := store.CreatePromotion(ctx, campaign.CreatePromotionCommand{
			OrganizationID: "org-test", OwnerApprovalID: approvalID, OwnerApprovalCanonicalHash: hash("a"), ProposalID: approvalID,
			ProposalCanonicalHash: hash("b"), FinancialReviewID: approvalID, FinancialReviewCanonicalHash: hash("c"),
			ExecutionMode: mode, ExecutiveRootTaskID: 900 + approvalID, ExecutiveCorrelationID: "executive:x",
			ExecutiveSubmitIdempotencyKey: "k", Status: campaign.StatusSubmitted, PromotedByRoleID: "owner", ToolCallID: "t",
			IdempotencyKey: fmt.Sprintf("campaign-promotion:org-test:%d", approvalID), CanonicalHash: hash("d"),
		})
		if err != nil {
			t.Fatal(err)
		}
		return prom
	}
	governed, legacy := seed(1, campaign.ExecutionModeGovernedImplementation), seed(2, "")

	read := func(tool string, arguments string, into any) {
		t.Helper()
		result, err := executor.Execute(turn, identity, executionharness.ToolRequest{ToolName: tool, ToolCallID: "call-" + tool, Arguments: json.RawMessage(arguments)})
		if err != nil {
			t.Fatalf("%s: %v", tool, err)
		}
		if err := json.Unmarshal(result.Content, into); err != nil {
			t.Fatal(err)
		}
	}
	var got PromotionResultProjection
	read("campaign.get_promotion", fmt.Sprintf(`{"promotion_id": %d}`, governed.ID), &got)
	if got.ExecutionMode != "governed_implementation" {
		t.Fatalf("get_promotion(governed) mode = %q", got.ExecutionMode)
	}
	read("campaign.get_promotion", fmt.Sprintf(`{"promotion_id": %d}`, legacy.ID), &got)
	if got.ExecutionMode != "analysis_only" {
		t.Fatalf("get_promotion(legacy) mode = %q, want analysis_only", got.ExecutionMode)
	}
	// promote_to_executive re-reading the governed promotion must report it as it is.
	var promoted PromoteResultProjection
	read("campaign.promote_to_executive", `{"owner_approval_id": 1}`, &promoted)
	if !promoted.Reused || promoted.ExecutionMode != "governed_implementation" {
		t.Fatalf("promote_to_executive(reused governed) = %+v", promoted)
	}
	if submitter.submitCalls != 0 {
		t.Fatalf("Executive.Submit called %d time(s) while reading existing promotions", submitter.submitCalls)
	}
}
