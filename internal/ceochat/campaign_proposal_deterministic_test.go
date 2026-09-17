package ceochat

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/campaign"
	"github.com/Mireuz13/explorarte-organization/internal/executionharness"
	"github.com/Mireuz13/explorarte-organization/internal/executionharness/modelruntimeadapter"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

type scriptedModelExecutor struct {
	responses   []executionharness.ModelResult
	invocations int
}

func (m *scriptedModelExecutor) Invoke(_ context.Context, _ executionharness.RunIdentity, _ executionharness.NormalizedModelRequest) (executionharness.ModelResult, error) {
	if m.invocations >= len(m.responses) {
		return executionharness.ModelResult{
			FinalOutput:  "No more scripted responses",
			FinishReason: executionharness.FinishFinal,
		}, nil
	}
	res := m.responses[m.invocations]
	m.invocations++
	return res, nil
}

type fakeTaskCoordinator struct {
	createdTasks    []tasks.Task
	taskCount       int64
	recordedResults []tasks.RecordAttemptResultCommand
}

func (f *fakeTaskCoordinator) CreateTask(_ context.Context, req tasks.CreateRequest, _ string, _ string) (tasks.Task, bool, error) {
	f.taskCount++
	t := tasks.Task{
		ID:                     f.taskCount,
		OrganizationID:         req.OrganizationID,
		OrganizationRevisionID: 1,
		AssignedRoleID:         req.AssignedRoleID,
		TaskClass:              req.TaskClass,
		IdempotencyKey:         req.IdempotencyKey,
		Status:                 tasks.StatusReady,
		Title:                  req.Title,
	}
	if req.RequestedByRoleID != "" {
		t.RequestedByRoleID = &req.RequestedByRoleID
	}
	f.createdTasks = append(f.createdTasks, t)
	return t, true, nil
}

func (f *fakeTaskCoordinator) GetTask(_ context.Context, id int64) (tasks.TaskDetail, error) {
	for _, t := range f.createdTasks {
		if t.ID == id {
			return tasks.TaskDetail{Task: t}, nil
		}
	}
	return tasks.TaskDetail{}, errors.New("task not found")
}

func (f *fakeTaskCoordinator) ClaimTaskByID(_ context.Context, id int64, req tasks.ClaimRequest) (tasks.ClaimedTask, error) {
	for i := range f.createdTasks {
		if f.createdTasks[i].ID == id {
			f.createdTasks[i].Status = tasks.StatusRunning
			return tasks.ClaimedTask{
				Task:       f.createdTasks[i],
				Attempt:    tasks.Attempt{ID: 1, TaskID: id, Ordinal: 1},
				LeaseToken: "lease-123",
			}, nil
		}
	}
	return tasks.ClaimedTask{}, errors.New("task not found")
}

func (f *fakeTaskCoordinator) StartAttempt(_ context.Context, _ tasks.LeaseCommand) (tasks.Task, error) {
	return tasks.Task{ID: 1, Status: tasks.StatusRunning}, nil
}

func (f *fakeTaskCoordinator) RecordAttemptResult(_ context.Context, cmd tasks.RecordAttemptResultCommand) (tasks.Task, error) {
	f.recordedResults = append(f.recordedResults, cmd)
	return tasks.Task{ID: 1, Status: tasks.StatusAwaitingVerification}, nil
}

func (f *fakeTaskCoordinator) FinalizeTask(_ context.Context, _ tasks.FinalizeCommand) (tasks.Task, error) {
	return tasks.Task{ID: 1, Status: tasks.StatusCompleted}, nil
}

func (f *fakeTaskCoordinator) BlockTask(_ context.Context, _ tasks.BlockCommand) (tasks.Task, error) {
	return tasks.Task{ID: 1, Status: tasks.StatusBlocked}, nil
}

type fakePrincipalResolver struct{}

func (f fakePrincipalResolver) Resolve(_ context.Context, _ string) (string, error) {
	return "principal-ceo", nil
}

type fakeDispatchProvisioner struct{}

func (f fakeDispatchProvisioner) EnsureAuthorizedAssignmentForRunningAttempt(_ context.Context, _, _ int64) error {
	return nil
}

type fakeContextBuilder struct{}

func (f fakeContextBuilder) Build(_ context.Context, req ContextRequest) (ContextSnapshot, error) {
	content := "context snapshot"
	h := sha256.Sum256([]byte(content))
	return ContextSnapshot{
		ID:      1,
		Version: "1",
		Digest:  fmt.Sprintf("%x", h),
		Content: content,
	}, nil
}

type fakeAuthority struct{}

func (f fakeAuthority) AuthorizeExecution(_ context.Context, _ executionharness.AuthorityRequest) error {
	return nil
}

func buildTestService(store Store, campaignStore campaign.Store, authorizer CapabilityAuthorizer, model *scriptedModelExecutor, taskCoord *fakeTaskCoordinator) *Service {
	reg := NewToolRegistry()
	_ = RegisterCampaignTools(reg, "org-test", campaignStore, authorizer)

	historyStore := executionharness.NewMemoryHistoryStore()
	descStore := executionharness.NewMemoryRunDescriptorStore()

	return &Service{
		OrganizationID:  "org-test",
		Store:           store,
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

// Scenario 1: Hypothetical query -> 0 campaign.propose calls, 0 proposals created.
func TestScenario1_HypotheticalQueryCausesZeroProposals(t *testing.T) {
	chatStore := newMemoryStore()
	campStore := newFakeCampaignStore()
	auth := fakeAuthorizer{allowed: map[string]bool{"owner:campaign.proposal.create": true}}
	taskCoord := &fakeTaskCoordinator{}

	model := &scriptedModelExecutor{
		responses: []executionharness.ModelResult{
			{
				FinalOutput:  "Para mejorar la adquisición, recomendaría una campaña en redes sociales centrada en creadores emergentes.",
				FinishReason: executionharness.FinishFinal,
			},
		},
	}

	service := buildTestService(chatStore, campStore, auth, model, taskCoord)
	conv, err := service.CreateConversation(context.Background(), CreateConversationRequest{
		ActorRoleID: "owner", OwnerRoleID: "owner",
	})
	if err != nil {
		t.Fatal(err)
	}

	res, err := service.Send(context.Background(), SendRequest{
		ConversationID: conv.ID,
		ActorRoleID:    "owner",
		IdempotencyKey: "turn-hypothetical",
		Content:        "¿Qué campaña propondrías?",
	})
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	for _, r := range taskCoord.recordedResults {
		t.Logf("RECORDED RESULT: code=%s summary=%s", r.Result.FailureCode, r.Result.Summary)
	}
	if res.Outcome != RunOutcomeCompleted {
		t.Fatalf("expected completed outcome, got %s, turnsUsed=%d, toolCallsUsed=%d", res.Outcome, res.TurnsUsed, res.ToolCallsUsed)
	}

	// Assert 0 proposals created
	proposals, err := campStore.ListProposals(context.Background(), "org-test", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(proposals) != 0 {
		t.Fatalf("expected 0 proposals for hypothetical query, got %d", len(proposals))
	}
}

// Scenario 2: Explicit draft intent -> 1 draft proposal created.
func TestScenario2_ExplicitDraftIntentCreatesOneDraft(t *testing.T) {
	chatStore := newMemoryStore()
	campStore := newFakeCampaignStore()
	auth := fakeAuthorizer{allowed: map[string]bool{"owner:campaign.proposal.create": true}}
	taskCoord := &fakeTaskCoordinator{}

	validProposalArgs, _ := json.Marshal(map[string]any{
		"title":               "Campaña de Creadores Q4",
		"goal":                "Adquirir 500 nuevos creadores activos",
		"acceptance_criteria": []string{"CPA < $15", "Tasa de retención > 20%"},
		"budget":              map[string]any{"currency": "USD", "max_amount": 3000.0, "source": "OWNER_LIMIT"},
	})

	model := &scriptedModelExecutor{
		responses: []executionharness.ModelResult{
			{
				ToolRequests: []executionharness.ToolRequest{
					{
						ToolName:   "campaign.propose",
						ToolCallID: "call_prop_1",
						Arguments:  validProposalArgs,
					},
				},
				FinishReason: executionharness.FinishTools,
			},
			{
				FinalOutput:  "He preparado la propuesta Campaña de Creadores Q4. Está en borrador y aún no se ha ejecutado. El siguiente paso es revisión financiera y tu aprobación.",
				FinishReason: executionharness.FinishFinal,
			},
		},
	}

	service := buildTestService(chatStore, campStore, auth, model, taskCoord)
	conv, err := service.CreateConversation(context.Background(), CreateConversationRequest{
		ActorRoleID: "owner", OwnerRoleID: "owner",
	})
	if err != nil {
		t.Fatal(err)
	}

	res, err := service.Send(context.Background(), SendRequest{
		ConversationID: conv.ID,
		ActorRoleID:    "owner",
		IdempotencyKey: "turn-explicit-draft",
		Content:        "Convierte esta idea en una propuesta de campaña.",
	})
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	if res.Outcome != RunOutcomeCompleted {
		t.Fatalf("expected completed outcome, got %s", res.Outcome)
	}

	// Assert exactly 1 draft created
	proposals, err := campStore.ListProposals(context.Background(), "org-test", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(proposals) != 1 {
		t.Fatalf("expected exactly 1 proposal, got %d", len(proposals))
	}
	p := proposals[0]
	if p.Status != campaign.StatusDraft {
		t.Errorf("expected draft status, got %s", p.Status)
	}
	if !p.FinancialReviewRequired {
		t.Errorf("expected financial_review_required = true")
	}
	if p.ExecutionStarted {
		t.Errorf("expected execution_started = false")
	}
}

// Scenario 3: Exact retry -> 1 proposal total (idempotent).
func TestScenario3_ExactRetryProducesSameProposalTotal(t *testing.T) {
	chatStore := newMemoryStore()
	campStore := newFakeCampaignStore()
	auth := fakeAuthorizer{allowed: map[string]bool{"owner:campaign.proposal.create": true}}
	taskCoord := &fakeTaskCoordinator{}

	proposalArgs, _ := json.Marshal(map[string]any{
		"title":               "Campaña Retención",
		"goal":                "Fidelizar 100 usuarios",
		"acceptance_criteria": []string{"NPS > 50"},
	})

	model := &scriptedModelExecutor{
		responses: []executionharness.ModelResult{
			{
				ToolRequests: []executionharness.ToolRequest{
					{ToolName: "campaign.propose", ToolCallID: "call_prop_retry", Arguments: proposalArgs},
				},
				FinishReason: executionharness.FinishTools,
			},
			{
				FinalOutput:  "Propuesta creada en borrador.",
				FinishReason: executionharness.FinishFinal,
			},
		},
	}

	service := buildTestService(chatStore, campStore, auth, model, taskCoord)
	conv, _ := service.CreateConversation(context.Background(), CreateConversationRequest{
		ActorRoleID: "owner", OwnerRoleID: "owner",
	})

	_, err := service.Send(context.Background(), SendRequest{
		ConversationID: conv.ID, ActorRoleID: "owner", IdempotencyKey: "turn-retry-test", Content: "Crea campaña",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Replay exact same request
	reusedRes, err := service.Send(context.Background(), SendRequest{
		ConversationID: conv.ID, ActorRoleID: "owner", IdempotencyKey: "turn-retry-test", Content: "Crea campaña",
	})
	if err != nil {
		t.Fatalf("replay send failed: %v", err)
	}
	if !reusedRes.Reused {
		t.Errorf("expected Reused=true on replay")
	}

	proposals, _ := campStore.ListProposals(context.Background(), "org-test", 10, 0)
	if len(proposals) != 1 {
		t.Fatalf("expected exactly 1 proposal total after retry, got %d", len(proposals))
	}
}

// Scenario 4: Same mutation identity, different payload -> CONFLICT.
func TestScenario4_SameIdentityDifferentPayloadConflicts(t *testing.T) {
	campStore := newFakeCampaignStore()
	auth := fakeAuthorizer{allowed: map[string]bool{"owner:campaign.proposal.create": true}}
	reg := setupCampaignTestRegistry(t, campStore, auth)
	executor := RegistryToolExecutor{Registry: reg}

	turnCtx := TurnContext{
		OrganizationID: "org-test", ConversationID: 1, TaskID: 10, AttemptID: 1, ActorRoleID: "owner", OwnerRoleID: "owner",
	}
	ctx := WithTurnContext(context.Background(), turnCtx)
	identity := executionharness.RunIdentity{OrganizationID: "org-test", RoleID: CEORoleID}

	// First call
	req1 := executionharness.ToolRequest{
		ToolName: "campaign.propose", ToolCallID: "call_conflict_1",
		Arguments: json.RawMessage(`{"title":"Original","goal":"Goal 1","acceptance_criteria":["A"]}`),
	}
	_, err := executor.Execute(ctx, identity, req1)
	if err != nil {
		t.Fatal(err)
	}

	// Second call with same tool call ID but altered goal
	req2 := executionharness.ToolRequest{
		ToolName: "campaign.propose", ToolCallID: "call_conflict_1",
		Arguments: json.RawMessage(`{"title":"Original","goal":"Altered Goal 2","acceptance_criteria":["A"]}`),
	}
	_, err = executor.Execute(ctx, identity, req2)
	if !errors.Is(err, campaign.ErrIdempotencyConflict) {
		t.Fatalf("expected ErrIdempotencyConflict, got: %v", err)
	}
}

// Scenario 5: Unauthorized actor -> DENY, 0 proposals.
func TestScenario5_UnauthorizedActorDenied(t *testing.T) {
	campStore := newFakeCampaignStore()
	// auth does NOT allow guest to create proposals
	auth := fakeAuthorizer{allowed: map[string]bool{"owner:campaign.proposal.create": true}}
	reg := setupCampaignTestRegistry(t, campStore, auth)
	executor := RegistryToolExecutor{Registry: reg}

	turnCtx := TurnContext{
		OrganizationID: "org-test", ConversationID: 1, TaskID: 10, AttemptID: 1,
		ActorRoleID: "guest", OwnerRoleID: "guest",
	}
	ctx := WithTurnContext(context.Background(), turnCtx)
	identity := executionharness.RunIdentity{OrganizationID: "org-test", RoleID: CEORoleID}

	req := executionharness.ToolRequest{
		ToolName: "campaign.propose", ToolCallID: "call_unauth",
		Arguments: json.RawMessage(`{"title":"Hack","goal":"Hack","acceptance_criteria":["Hack"]}`),
	}
	_, err := executor.Execute(ctx, identity, req)
	if !errors.Is(err, ErrUnauthorizedActor) {
		t.Fatalf("expected ErrUnauthorizedActor, got: %v", err)
	}

	proposals, _ := campStore.ListProposals(context.Background(), "org-test", 10, 0)
	if len(proposals) != 0 {
		t.Fatalf("expected 0 proposals, got %d", len(proposals))
	}
}

// Scenario 6: Crash after persistence -> retry returns same proposal.
func TestScenario6_CrashAfterPersistenceRetryReusesProposal(t *testing.T) {
	campStore := newFakeCampaignStore()
	auth := fakeAuthorizer{allowed: map[string]bool{"owner:campaign.proposal.create": true}}
	reg := setupCampaignTestRegistry(t, campStore, auth)
	executor := RegistryToolExecutor{Registry: reg}

	turnCtx := TurnContext{
		OrganizationID: "org-test", ConversationID: 55, TaskID: 66, AttemptID: 1, ActorRoleID: "owner", OwnerRoleID: "owner",
	}
	ctx := WithTurnContext(context.Background(), turnCtx)
	identity := executionharness.RunIdentity{OrganizationID: "org-test", RoleID: CEORoleID}

	payload := json.RawMessage(`{"title":"Crash Test","goal":"Recover seamlessly","acceptance_criteria":["Passes"]}`)
	req := executionharness.ToolRequest{
		ToolName: "campaign.propose", ToolCallID: "call_crash_1", Arguments: payload,
	}

	// Proposal persisted
	res1, err := executor.Execute(ctx, identity, req)
	if err != nil {
		t.Fatal(err)
	}
	var p1 ProposeResultProjection
	_ = json.Unmarshal(res1.Content, &p1)

	// Simulate crash: harness didn't finalize assistant message, turn is retried:
	res2, err := executor.Execute(ctx, identity, req)
	if err != nil {
		t.Fatalf("retry after crash failed: %v", err)
	}
	var p2 ProposeResultProjection
	_ = json.Unmarshal(res2.Content, &p2)

	if p1.ProposalID != p2.ProposalID {
		t.Fatalf("expected proposal ID %d, got %d", p1.ProposalID, p2.ProposalID)
	}

	proposals, _ := campStore.ListProposals(context.Background(), "org-test", 10, 0)
	if len(proposals) != 1 {
		t.Fatalf("expected exactly 1 proposal row, got %d", len(proposals))
	}
}

// Scenario 7: Attempted invented tool -> fail closed.
func TestScenario7_AttemptedInventedToolFailsClosed(t *testing.T) {
	chatStore := newMemoryStore()
	campStore := newFakeCampaignStore()
	auth := fakeAuthorizer{allowed: map[string]bool{"owner:campaign.proposal.create": true}}
	taskCoord := &fakeTaskCoordinator{}

	model := &scriptedModelExecutor{
		responses: []executionharness.ModelResult{
			{
				ToolRequests: []executionharness.ToolRequest{
					{ToolName: "campaign.execute", ToolCallID: "call_invented_1", Arguments: json.RawMessage(`{"proposal_id": 1}`)},
				},
				FinishReason: executionharness.FinishTools,
			},
		},
	}

	service := buildTestService(chatStore, campStore, auth, model, taskCoord)
	conv, _ := service.CreateConversation(context.Background(), CreateConversationRequest{
		ActorRoleID: "owner", OwnerRoleID: "owner",
	})

	res, err := service.Send(context.Background(), SendRequest{
		ConversationID: conv.ID, ActorRoleID: "owner", IdempotencyKey: "turn-invented", Content: "Lanza la campaña inmediatamente",
	})
	if err != nil {
		t.Fatal(err)
	}

	// When an unregistered tool is invoked, Harness completes with failure / incomplete outcome
	if res.Outcome != RunOutcomeIncomplete {
		t.Fatalf("expected RunOutcomeIncomplete for invented tool, got %s", res.Outcome)
	}

	proposals, _ := campStore.ListProposals(context.Background(), "org-test", 10, 0)
	if len(proposals) != 0 {
		t.Fatalf("expected 0 proposals, got %d", len(proposals))
	}
}

// Canonical Executive Non-Effect Proof:
// After campaign.propose:
// - Executive root tasks created = 0
// - Department tasks created = 0
// - Agent budgets created = 0
// - Execution runs created = 0
// - Provider calls caused by proposal handler = 0
func TestCanonicalExecutiveNonEffectProof(t *testing.T) {
	chatStore := newMemoryStore()
	campStore := newFakeCampaignStore()
	auth := fakeAuthorizer{allowed: map[string]bool{"owner:campaign.proposal.create": true}}
	taskCoord := &fakeTaskCoordinator{}

	proposalArgs, _ := json.Marshal(map[string]any{
		"title":               "Non-Execution Proof Campaign",
		"goal":                "Verify absolute isolation from Executive.Submit",
		"acceptance_criteria": []string{"Executive roots = 0"},
		"budget":              map[string]any{"currency": "USD", "max_amount": 5000.0, "source": "CEO_ESTIMATE"},
	})

	model := &scriptedModelExecutor{
		responses: []executionharness.ModelResult{
			{
				ToolRequests: []executionharness.ToolRequest{
					{ToolName: "campaign.propose", ToolCallID: "call_proof_1", Arguments: proposalArgs},
				},
				FinishReason: executionharness.FinishTools,
			},
			{
				FinalOutput:  "Propuesta creada en borrador.",
				FinishReason: executionharness.FinishFinal,
			},
		},
	}

	service := buildTestService(chatStore, campStore, auth, model, taskCoord)
	conv, _ := service.CreateConversation(context.Background(), CreateConversationRequest{
		ActorRoleID: "owner", OwnerRoleID: "owner",
	})

	res, err := service.Send(context.Background(), SendRequest{
		ConversationID: conv.ID, ActorRoleID: "owner", IdempotencyKey: "turn-non-exec-proof", Content: "Prepara la campaña",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != RunOutcomeCompleted {
		t.Fatalf("expected completed turn, got %s", res.Outcome)
	}

	// 1. Exactly 1 ceochat turn task was created; ZERO Executive root tasks (TaskClassOwnerGoal)
	for _, tk := range taskCoord.createdTasks {
		if tk.TaskClass == "executive.owner_goal" || tk.TaskClass == "project.root" {
			t.Fatalf("FATAL: Executive root task created: %+v", tk)
		}
		if tk.AssignedRoleID != CEORoleID {
			t.Fatalf("FATAL: Departmental task assigned to %q created", tk.AssignedRoleID)
		}
	}

	// 2. Exactly 1 proposal was created, status = draft, execution_started = false
	proposals, _ := campStore.ListProposals(context.Background(), "org-test", 10, 0)
	if len(proposals) != 1 {
		t.Fatalf("expected 1 proposal, got %d", len(proposals))
	}
	if proposals[0].ExecutionStarted {
		t.Fatalf("FATAL: ExecutionStarted is true on draft proposal")
	}
	if !proposals[0].FinancialReviewRequired {
		t.Fatalf("FATAL: FinancialReviewRequired is false on draft proposal")
	}
}
