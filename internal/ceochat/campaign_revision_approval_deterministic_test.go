package ceochat

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/campaign"
	"github.com/Mireuz13/explorarte-organization/internal/executionharness"
	"github.com/Mireuz13/explorarte-organization/internal/executionharness/modelruntimeadapter"
)

func buildRevisionApprovalTestService(chatStore Store, campStore campaign.Store, auth CapabilityAuthorizer, model *scriptedModelExecutor, taskCoord *fakeTaskCoordinator) *Service {
	reg := NewToolRegistry()
	_ = RegisterCampaignTools(reg, "org-test", campStore, auth)

	historyStore := executionharness.NewMemoryHistoryStore()
	descStore := executionharness.NewMemoryRunDescriptorStore()

	return &Service{
		OrganizationID:  "org-test",
		Store:           chatStore,
		Tasks:           taskCoord,
		Principals:      fakePrincipalResolver{},
		Assignments:     fakeDispatchProvisioner{},
		Contexts:        fakeContextBuilder{},
		Authority:       fakeAuthority{},
		HarnessHistory:  historyStore,
		DescriptorStore: descStore,
		NewModelExecutor: func(_ modelruntimeadapter.Config) (executionharness.ModelExecutor, error) {
			return model, nil
		},
		Catalog:         RegistryToolCatalog{Registry: reg},
		ToolExecutor:    RegistryToolExecutor{Registry: reg},
		ToolDefinitions: reg.Definitions(),
		MaxOutputTokens: 2000,
	}
}

// Scenario 1: Finance changes requested query -> Read-only, 0 revisions, 0 approvals
func TestScriptedScenario1_FinanceChangesRequested_ReadOnly(t *testing.T) {
	ctx := context.Background()
	chatStore := newMemoryStore()
	campStore := newFakeCampaignStore()
	auth := fakeAuthorizer{
		allowed: map[string]bool{
			"owner:campaign.proposal.read":               true,
			"owner:campaign.financial_review.read":       true,
			"empresa/ceo:campaign.proposal.read":         true,
			"empresa/ceo:campaign.financial_review.read": true,
		},
	}
	taskCoord := &fakeTaskCoordinator{}

	// Create initial proposal v1 and a changes_requested review
	p1Hash, _ := campaign.ComputeCanonicalHash(campaign.CanonicalPayload{Title: "Camp v1", Goal: "Initial"})
	p1, _, _ := campStore.CreateProposal(ctx, campaign.CreateProposalCommand{
		OrganizationID: "org-test",
		Title:          "Camp v1",
		Goal:           "Initial",
		IdempotencyKey: "k1",
		CanonicalHash:  p1Hash,
	})

	r1Hash, _ := campaign.ComputeReviewCanonicalHash(campaign.ReviewCanonicalPayload{
		ProposalID:            p1.ID,
		ProposalCanonicalHash: p1.CanonicalHash,
		ReviewerRoleID:        "negocio/administrador_financiero",
		Verdict:               campaign.VerdictChangesRequested,
		RequiredCorrections:   []string{"Reduce CPA target to $10"},
		Summary:               "Changes requested",
	})
	_, _, _ = campStore.RecordFinancialReview(ctx, campaign.RecordFinancialReviewCommand{
		OrganizationID:        "org-test",
		ReviewRequestID:       101,
		ProposalID:            p1.ID,
		ProposalCanonicalHash: p1.CanonicalHash,
		ReviewerRoleID:        "negocio/administrador_financiero",
		Verdict:               campaign.VerdictChangesRequested,
		RequiredCorrections:   []string{"Reduce CPA target to $10"},
		Summary:               "Changes requested",
		CanonicalHash:         r1Hash,
	})

	model := &scriptedModelExecutor{
		responses: []executionharness.ModelResult{
			{
				ToolRequests: []executionharness.ToolRequest{
					{
						ToolCallID: "call_get_fin_1",
						ToolName:   "campaign.get_financial_review",
						Arguments:  json.RawMessage(fmt.Sprintf(`{"proposal_id": %d}`, p1.ID)),
					},
				},
				FinishReason: executionharness.FinishTools,
			},
			{
				FinalOutput:  "Finanzas solicitó reducir el CPA objetivo a $10.",
				FinishReason: executionharness.FinishFinal,
			},
		},
	}

	service := buildRevisionApprovalTestService(chatStore, campStore, auth, model, taskCoord)
	conv, _ := service.CreateConversation(ctx, CreateConversationRequest{ActorRoleID: "owner", OwnerRoleID: "owner"})

	res, err := service.Send(ctx, SendRequest{
		ConversationID: conv.ID,
		ActorRoleID:    "owner",
		IdempotencyKey: "turn-scen-1",
		Content:        "¿Qué pidió Finanzas?",
	})
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	// When the tool fails or model explains, outcome is recorded and 0 approvals are created
	if res.Outcome != RunOutcomeCompleted && res.Outcome != RunOutcomeIncomplete {
		t.Fatalf("expected completed or incomplete, got %s", res.Outcome)
	}

	// Invariants: 0 revisions created, 0 approvals
	proposals, _ := campStore.ListProposals(ctx, "org-test", 10, 0)
	if len(proposals) != 1 {
		t.Fatalf("expected exactly 1 proposal (v1), got %d", len(proposals))
	}
	_, err = campStore.GetOwnerApprovalByProposal(ctx, "org-test", p1.ID)
	if err == nil {
		t.Fatal("expected 0 approvals")
	}
}

// Scenario 2: Explicit revision -> exactly 1 new revision (v2) created
func TestScriptedScenario2_ExplicitRevision(t *testing.T) {
	ctx := context.Background()
	chatStore := newMemoryStore()
	campStore := newFakeCampaignStore()
	auth := fakeAuthorizer{
		allowed: map[string]bool{
			"owner:campaign.proposal.read":         true,
			"owner:campaign.proposal.revise":       true,
			"empresa/ceo:campaign.proposal.revise": true,
		},
	}
	taskCoord := &fakeTaskCoordinator{}

	p1Hash, _ := campaign.ComputeCanonicalHash(campaign.CanonicalPayload{Title: "Camp v1", Goal: "Initial"})
	p1, _, _ := campStore.CreateProposal(ctx, campaign.CreateProposalCommand{
		OrganizationID: "org-test",
		Title:          "Camp v1",
		Goal:           "Initial",
		IdempotencyKey: "k1",
		CanonicalHash:  p1Hash,
	})

	model := &scriptedModelExecutor{
		responses: []executionharness.ModelResult{
			{
				ToolRequests: []executionharness.ToolRequest{
					{
						ToolCallID: "call_rev_scen2",
						ToolName:   "campaign.revise_proposal",
						Arguments: json.RawMessage(fmt.Sprintf(`{
							"proposal_id": %d,
							"title": "Camp v2",
							"goal": "Initial with reduced CPA",
							"acceptance_criteria": ["CPA < $10"]
						}`, p1.ID)),
					},
				},
				FinishReason: executionharness.FinishTools,
			},
			{
				FinalOutput:  "He generado la versión 2 de la propuesta incorporando las correcciones.",
				FinishReason: executionharness.FinishFinal,
			},
		},
	}

	service := buildRevisionApprovalTestService(chatStore, campStore, auth, model, taskCoord)
	conv, _ := service.CreateConversation(ctx, CreateConversationRequest{ActorRoleID: "owner", OwnerRoleID: "owner"})

	res, err := service.Send(ctx, SendRequest{
		ConversationID: conv.ID,
		ActorRoleID:    "owner",
		IdempotencyKey: "turn-scen-2",
		Content:        "Aplica las correcciones y crea una nueva versión.",
	})
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	if res.Outcome != RunOutcomeCompleted {
		t.Fatalf("expected completed, got %s", res.Outcome)
	}

	// Assertions: exactly 2 proposals (v1 and v2), v2 parent is v1
	v2, err := campStore.GetProposal(ctx, "org-test", 2)
	if err != nil {
		t.Fatalf("v2 not found: %v", err)
	}
	if v2.RevisionNumber != 2 {
		t.Fatalf("expected revision 2, got %d", v2.RevisionNumber)
	}
	if v2.ParentProposalID == nil || *v2.ParentProposalID != p1.ID {
		t.Fatalf("expected parent ID %d, got %v", p1.ID, v2.ParentProposalID)
	}
	// Approvals = 0
	_, err = campStore.GetOwnerApprovalByProposal(ctx, "org-test", v2.ID)
	if err == nil {
		t.Fatal("expected 0 approvals for v2")
	}
}

// Scenario 3: Premature approval before new finance review -> DENY, 0 approvals
func TestScriptedScenario3_PrematureApprovalBeforeFinanceReview(t *testing.T) {
	ctx := context.Background()
	chatStore := newMemoryStore()
	campStore := newFakeCampaignStore()
	auth := fakeAuthorizer{
		allowed: map[string]bool{
			"owner:campaign.owner_approval.create":       true,
			"empresa/ceo:campaign.owner_approval.create": true,
			"empresa/ceo:campaign.financial_review.read": true,
		},
	}
	taskCoord := &fakeTaskCoordinator{}

	// Create v1 with review(v1)
	p1Hash, _ := campaign.ComputeCanonicalHash(campaign.CanonicalPayload{Title: "Camp v1", Goal: "Initial"})
	p1, _, _ := campStore.CreateProposal(ctx, campaign.CreateProposalCommand{
		OrganizationID: "org-test",
		Title:          "Camp v1",
		Goal:           "Initial",
		IdempotencyKey: "k1",
		CanonicalHash:  p1Hash,
	})

	r1Hash, _ := campaign.ComputeReviewCanonicalHash(campaign.ReviewCanonicalPayload{
		ProposalID:            p1.ID,
		ProposalCanonicalHash: p1.CanonicalHash,
		ReviewerRoleID:        "negocio/administrador_financiero",
		Verdict:               campaign.VerdictRecommended,
	})
	rev1, _, _ := campStore.RecordFinancialReview(ctx, campaign.RecordFinancialReviewCommand{
		OrganizationID:        "org-test",
		ReviewRequestID:       101,
		ProposalID:            p1.ID,
		ProposalCanonicalHash: p1.CanonicalHash,
		ReviewerRoleID:        "negocio/administrador_financiero",
		Verdict:               campaign.VerdictRecommended,
		RecommendedBudget:     &campaign.BudgetRecommendation{MaxUSD: 1000},
		CanonicalHash:         r1Hash,
	})

	// Create v2 (no financial review for v2 yet)
	p2Hash, _ := campaign.ComputeCanonicalHash(campaign.CanonicalPayload{Title: "Camp v2", Goal: "Revised"})
	v2, _, _ := campStore.CreateRevision(ctx, campaign.CreateRevisionCommand{
		OrganizationID:   "org-test",
		ParentProposalID: p1.ID,
		Title:            "Camp v2",
		Goal:             "Revised",
		IdempotencyKey:   "k2",
		CanonicalHash:    p2Hash,
	})

	// User immediately says "Apruébala", model tries to prepare an approval of v2 with old review v1
	model := &scriptedModelExecutor{
		responses: []executionharness.ModelResult{
			{
				ToolRequests: []executionharness.ToolRequest{
					{
						ToolCallID: "call_appr_scen3",
						ToolName:   "campaign.prepare_owner_approval",
						Arguments: json.RawMessage(fmt.Sprintf(`{
							"proposal_id": %d,
							"financial_review_id": %d
						}`, v2.ID, rev1.ID)),
					},
				},
				FinishReason: executionharness.FinishTools,
			},
			{
				FinalOutput:  "No se puede aprobar la versión 2 porque requiere una nueva revisión financiera.",
				FinishReason: executionharness.FinishFinal,
			},
		},
	}

	service := buildRevisionApprovalTestService(chatStore, campStore, auth, model, taskCoord)
	conv, _ := service.CreateConversation(ctx, CreateConversationRequest{ActorRoleID: "owner", OwnerRoleID: "owner"})

	res, err := service.Send(ctx, SendRequest{
		ConversationID: conv.ID,
		ActorRoleID:    "owner",
		IdempotencyKey: "turn-scen-3",
		Content:        "Apruébala.",
	})
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	if res.Outcome != RunOutcomeCompleted && res.Outcome != RunOutcomeIncomplete {
		t.Fatalf("expected completed, got %s", res.Outcome)
	}

	// Assertions: 0 approvals created for v2
	_, err = campStore.GetOwnerApprovalByProposal(ctx, "org-test", v2.ID)
	if err == nil {
		t.Fatal("expected 0 approvals for v2 due to review hash mismatch")
	}
}

// Scenario 4: Hypothetical question after recommended review -> 0 approvals
func TestScriptedScenario4_HypotheticalQuestion(t *testing.T) {
	ctx := context.Background()
	chatStore := newMemoryStore()
	campStore := newFakeCampaignStore()
	auth := fakeAuthorizer{
		allowed: map[string]bool{
			"owner:campaign.owner_approval.create": true,
		},
	}
	taskCoord := &fakeTaskCoordinator{}

	model := &scriptedModelExecutor{
		responses: []executionharness.ModelResult{
			{
				FinalOutput:  "Sí, la propuesta v2 cuenta con recomendación favorable de Finanzas y está lista para que usted decida si la aprueba.",
				FinishReason: executionharness.FinishFinal,
			},
		},
	}

	service := buildRevisionApprovalTestService(chatStore, campStore, auth, model, taskCoord)
	conv, _ := service.CreateConversation(ctx, CreateConversationRequest{ActorRoleID: "owner", OwnerRoleID: "owner"})

	res, err := service.Send(ctx, SendRequest{
		ConversationID: conv.ID,
		ActorRoleID:    "owner",
		IdempotencyKey: "turn-scen-4",
		Content:        "¿Está lista para aprobar?",
	})
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	if res.Outcome != RunOutcomeCompleted {
		t.Fatalf("expected completed, got %s", res.Outcome)
	}

	// Assertions: 0 approvals
	if len(campStore.approvals) != 0 {
		t.Fatalf("expected 0 approvals, got %d", len(campStore.approvals))
	}
}

// Scenario 5: the owner asks for approval -> the CEO prepares it (0 approvals);
// the owner's own act creates exactly 1 approval; 0 execution calls
func TestScriptedScenario5_ExplicitApproval_ZeroExecution(t *testing.T) {
	ctx := context.Background()
	chatStore := newMemoryStore()
	campStore := newFakeCampaignStore()
	auth := fakeAuthorizer{
		allowed: map[string]bool{
			"owner:campaign.owner_approval.create":       true,
			"empresa/ceo:campaign.owner_approval.create": true,
			"owner:campaign.financial_review.read":       true,
		},
	}
	taskCoord := &fakeTaskCoordinator{}

	// Setup v2 with recommended review
	p2Hash, _ := campaign.ComputeCanonicalHash(campaign.CanonicalPayload{Title: "Camp v2", Goal: "Approved version"})
	v2, _, _ := campStore.CreateProposal(ctx, campaign.CreateProposalCommand{
		OrganizationID: "org-test",
		Title:          "Camp v2",
		Goal:           "Approved version",
		IdempotencyKey: "k2-scen5",
		CanonicalHash:  p2Hash,
	})

	r2Hash, _ := campaign.ComputeReviewCanonicalHash(campaign.ReviewCanonicalPayload{
		ProposalID:            v2.ID,
		ProposalCanonicalHash: v2.CanonicalHash,
		ReviewerRoleID:        "negocio/administrador_financiero",
		Verdict:               campaign.VerdictRecommended,
	})
	recBudget := &campaign.BudgetRecommendation{
		MaxUSD:        15000,
		MaxTokens:     500000,
		MaxModelCalls: 150,
		MaxWallTimeMS: 86400000,
		MaxDepth:      8,
		MaxRetries:    5,
		MaxSubagents:  4,
	}
	rev2, _, _ := campStore.RecordFinancialReview(ctx, campaign.RecordFinancialReviewCommand{
		OrganizationID:        "org-test",
		ReviewRequestID:       302,
		ProposalID:            v2.ID,
		ProposalCanonicalHash: v2.CanonicalHash,
		ReviewerRoleID:        "negocio/administrador_financiero",
		Verdict:               campaign.VerdictRecommended,
		RecommendedBudget:     recBudget,
		CanonicalHash:         r2Hash,
	})

	model := &scriptedModelExecutor{
		responses: []executionharness.ModelResult{
			{
				ToolRequests: []executionharness.ToolRequest{
					{
						ToolCallID: "call_appr_scen5",
						ToolName:   "campaign.prepare_owner_approval",
						Arguments: json.RawMessage(fmt.Sprintf(`{
							"proposal_id": %d,
							"financial_review_id": %d
						}`, v2.ID, rev2.ID)),
					},
				},
				FinishReason: executionharness.FinishTools,
			},
			{
				FinalOutput:  "La aprobación está lista para que usted la haga: ejecute el comando indicado. La promoción a Executive es un paso posterior.",
				FinishReason: executionharness.FinishFinal,
			},
		},
	}

	service := buildRevisionApprovalTestService(chatStore, campStore, auth, model, taskCoord)
	conv, _ := service.CreateConversation(ctx, CreateConversationRequest{ActorRoleID: "owner", OwnerRoleID: "owner"})

	res, err := service.Send(ctx, SendRequest{
		ConversationID: conv.ID,
		ActorRoleID:    "owner",
		IdempotencyKey: "turn-scen-5",
		Content:        "La apruebo para ejecución con el presupuesto recomendado.",
	})
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	if res.Outcome != RunOutcomeCompleted {
		t.Fatalf("expected completed, got %s (%+v)", res.Outcome, res)
	}

	// 0. The CEO's turn created NO approval: it can only prepare one.
	if _, err := campStore.GetOwnerApprovalByProposal(ctx, "org-test", v2.ID); err == nil {
		t.Fatal("the CEO turn created an owner approval; only the owner can")
	}
	// 1. The owner's own act creates exactly 1 approval.
	approveAsOwner(t, campStore, auth, v2.ID, rev2.ID)
	appr, err := campStore.GetOwnerApprovalByProposal(ctx, "org-test", v2.ID)
	if err != nil {
		t.Fatalf("owner approval not found: %v", err)
	}
	if appr.Status != campaign.StatusApprovedForExecution {
		t.Fatalf("expected status approved_for_execution, got %s", appr.Status)
	}
	if appr.ExecutionBudget.MaxUSD != 15000 {
		t.Fatalf("expected budget MaxUSD 15000, got %v", appr.ExecutionBudget.MaxUSD)
	}

	// 2. Hard invariant: NO execution side effects
	// Executive.Submit calls = 0
	// execution roots = 0
	// AgentBudget roots = 0
	// workers launched = 0
	pCheck, _ := campStore.GetProposal(ctx, "org-test", v2.ID)
	if pCheck.ExecutionStarted {
		t.Fatal("HARD INVARIANT VIOLATION: ExecutionStarted must be false")
	}
	// Assert no department execution tasks or execution roots created
	for _, tk := range taskCoord.createdTasks {
		if tk.TaskClass != "executive.ceo_chat_turn" {
			t.Fatalf("HARD INVARIANT VIOLATION: department execution task created: class=%s title=%s", tk.TaskClass, tk.Title)
		}
	}
}
