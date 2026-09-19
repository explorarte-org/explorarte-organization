package campaign_test

// FINANCE_HARNESS_RUNTIME_HOTFIX_V1: proves runHarnessModel's own
// composition -- provenance, context digest, zero-tool enforcement, and
// model-failure containment -- by driving the REAL
// executionharness.Runtime (real financeToolCatalog/financeToolExecutor,
// real tool-denial logic) through campaign.FinanceService.ExecuteReviewTask
// with MockOutput=nil, using in-memory History/DescriptorStore
// (executionharness.NewMemoryHistoryStore/NewMemoryRunDescriptorStore,
// both already exported for exactly this purpose) and the existing
// fakeTaskCoordinator/memCampaignStore fixtures from
// financial_review_deterministic_test.go. No real PostgreSQL, no real
// Model Runtime, no real provider -- those are proven separately in
// internal/ceochat's own real-infrastructure integration suite. This file
// is purely about the Harness composition seam itself: given a real
// Runtime, does Finance's own RunSpec carry the right provenance, the
// right context digest, and truly zero tool capability.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/campaign"
	"github.com/Mireuz13/explorarte-organization/internal/executionharness"
	"github.com/Mireuz13/explorarte-organization/internal/executionharness/modelruntimeadapter"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

// passAuthority always authorizes: these tests are about Harness
// composition/provenance/tool-denial, not lease/principal authority
// itself, which is proven separately against real PostgreSQL in
// internal/ceochat's own suite.
type passAuthority struct{}

func (passAuthority) AuthorizeExecution(context.Context, executionharness.AuthorityRequest) error {
	return nil
}

// fakeFinanceContextBuilder is a deterministic, in-memory stand-in for the
// real Context Engine (FINANCE_CONTEXT_ENGINE_INTEGRATION_V1): it reads
// the SAME Finance task's own Instructions the fixture's taskCoord holds
// -- exactly the field the real SourceTaskContext provider
// (internal/tasks/contextprovider) renders verbatim in production -- and
// returns a snapshot whose Content IS that task's Instructions and whose
// Digest is that content's own sha256. This proves runHarnessModel's own
// consumption side (validate the snapshot, use ID/Version/Digest/Content
// byte-for-byte, never re-wrap or re-derive) without needing a real
// PostgreSQL-backed Context Engine, which is proven separately against
// real infrastructure in internal/ceochat's own suite.
type fakeFinanceContextBuilder struct {
	taskCoord *fakeTaskCoordinator
	nextID    int64
	calls     int
}

func (b *fakeFinanceContextBuilder) BuildFinanceContext(ctx context.Context, request campaign.FinanceContextRequest) (campaign.FinanceContextSnapshot, error) {
	b.calls++
	task, err := b.taskCoord.GetTask(ctx, request.TaskID)
	if err != nil {
		return campaign.FinanceContextSnapshot{}, err
	}
	b.nextID++
	digest := sha256.Sum256([]byte(task.Instructions))
	return campaign.FinanceContextSnapshot{
		ID:      b.nextID,
		Version: "v1",
		Digest:  hex.EncodeToString(digest[:]),
		Content: task.Instructions,
	}, nil
}

// capturingModel records the exact RunIdentity it was invoked with, so a
// test can assert Harness provenance equals the durable Finance task's own
// lineage rather than a fabricated one, and produces whatever ModelResult
// its script says.
type capturingModel struct {
	capturedIdentity executionharness.RunIdentity
	invocations      int
	script           func(executionharness.NormalizedModelRequest) (executionharness.ModelResult, error)
}

func (c *capturingModel) Invoke(ctx context.Context, identity executionharness.RunIdentity, req executionharness.NormalizedModelRequest) (executionharness.ModelResult, error) {
	c.capturedIdentity = identity
	c.invocations++
	return c.script(req)
}

// validFinanceOutputJSON is a well-formed FinanceReviewOutput, used
// wherever a test needs the model to answer successfully. recommended_budget
// is strictly positive in every dimension -- CAMPAIGN_EXECUTABLE_BUDGET_
// CONTRACT_HOTFIX_V1's validateFinanceReviewOutput now rejects a
// "recommended" verdict without one.
const validFinanceOutputJSON = `{"verdict":"recommended","summary":"Harness composition local test.","recommended_budget":{"max_usd":1.0,"max_tokens":1000,"max_model_calls":1,"max_wall_time_ms":60000,"max_depth":1,"max_retries":1,"max_subagents":1}}`

type harnessLocalFixture struct {
	store       *memCampaignStore
	taskCoord   *fakeTaskCoordinator
	finSvc      *campaign.FinanceService
	descriptors *executionharness.MemoryRunDescriptorStore
	history     *executionharness.MemoryHistoryStore
	proposal    campaign.CampaignProposal
	reviewReq   campaign.CampaignFinancialReviewRequest
	task        tasks.Task
}

func newHarnessLocalFixture(t *testing.T, model *capturingModel) harnessLocalFixture {
	t.Helper()
	store, taskCoord, _, _ := setupDeterministicFixture(t)
	prop := createTestProposal(t, store, "org-test", "Harness Local Campaign", "Goal")

	history := executionharness.NewMemoryHistoryStore()
	descriptors := executionharness.NewMemoryRunDescriptorStore()

	finSvc, err := campaign.NewFinanceService(campaign.FinanceServiceConfig{
		OrganizationID: "org-test",
		Requirements:   permissiveRequirements(),
		Store:          store,
		Tasks:          taskCoord,
		Authorizer: fakeAuthorizer{grants: map[string]bool{
			"empresa/ceo:campaign.financial_review.request":                      true,
			"empresa/ceo:campaign.financial_review.read":                         true,
			"negocio/administrador_financiero:campaign.financial_review.perform": true,
		}},
		Authority:         passAuthority{},
		HarnessHistory:    history,
		DescriptorStore:   descriptors,
		HolderPrincipalID: "finance-principal-local-test",
		ContextBuilder:    &fakeFinanceContextBuilder{taskCoord: taskCoord},
		NewModelExecutor: func(modelruntimeadapter.Config) (executionharness.ModelExecutor, error) {
			return model, nil
		},
	})
	if err != nil {
		t.Fatalf("NewFinanceService: %v", err)
	}

	req, task, _, err := finSvc.RequestReview(context.Background(), campaign.RequestReviewParams{
		OrganizationID:      "org-test",
		ProposalID:          prop.ID,
		RequestedByRoleID:   "empresa/ceo",
		RequestedFromTaskID: 20,
		ToolCallID:          "call_harness_local",
	})
	if err != nil {
		t.Fatalf("RequestReview: %v", err)
	}
	return harnessLocalFixture{
		store: store, taskCoord: taskCoord, finSvc: finSvc,
		descriptors: descriptors, history: history,
		proposal: prop, reviewReq: req, task: task,
	}
}

// expectedFinanceContextDigest mirrors fakeFinanceContextBuilder's own
// digest derivation exactly (sha256 of the Finance task's own
// Instructions -- the real SourceTaskContext content, per FINANCE_
// CONTEXT_ENGINE_INTEGRATION_V1), so the test can assert the persisted
// RunDescriptor.ContextDigest is the ACTUAL context snapshot content's own
// digest -- never the proposal's own canonical hash substituted in its
// place, which is exactly the defect PR #224 corrected, and never a
// fabricated ad-hoc prompt digest, which is exactly the defect this round
// corrects.
func expectedFinanceContextDigest(t *testing.T, taskCoord *fakeTaskCoordinator, taskID int64) string {
	t.Helper()
	task, err := taskCoord.GetTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("read task: %v", err)
	}
	sum := sha256.Sum256([]byte(task.Instructions))
	return hex.EncodeToString(sum[:])
}

// TestRealHarness_ProvenanceAndContextDigest is sections 20/21: the
// Harness RunIdentity's CorrelationID/CausationID/TaskID/RoleID/
// ExecutionPrincipalID must equal the durable Finance task's own fields --
// no "finrev-corr:"/"finrev-cause:" synthetic lineage may appear -- and the
// persisted RunDescriptor's ContextDigest must be the actual rendered
// context content's own digest, never the proposal's canonical hash
// substituted in its place.
func TestRealHarness_ProvenanceAndContextDigest(t *testing.T) {
	model := &capturingModel{script: func(executionharness.NormalizedModelRequest) (executionharness.ModelResult, error) {
		return executionharness.ModelResult{FinishReason: executionharness.FinishFinal, FinalOutput: validFinanceOutputJSON, InvocationRef: "local-inv-1"}, nil
	}}
	fx := newHarnessLocalFixture(t, model)

	review, _, err := fx.finSvc.ExecuteReviewTask(context.Background(), campaign.ExecuteReviewParams{
		OrganizationID: "org-test", TaskID: fx.task.ID, ReviewRequestID: fx.reviewReq.ID,
	})
	if err != nil {
		t.Fatalf("ExecuteReviewTask: %v", err)
	}
	if review.Verdict != campaign.VerdictRecommended {
		t.Fatalf("verdict = %q, want recommended", review.Verdict)
	}
	if model.invocations != 1 {
		t.Fatalf("model invocations = %d, want 1", model.invocations)
	}

	claimedTask, err := fx.taskCoord.GetTask(context.Background(), fx.task.ID)
	if err != nil {
		t.Fatalf("read finance task: %v", err)
	}

	id := model.capturedIdentity
	if id.CorrelationID == "" || claimedTask.CorrelationID == nil || id.CorrelationID != *claimedTask.CorrelationID {
		t.Errorf("Harness CorrelationID = %q, want the Finance task's own %v", id.CorrelationID, claimedTask.CorrelationID)
	}
	if id.CausationID == "" || claimedTask.CausationID == nil || id.CausationID != *claimedTask.CausationID {
		t.Errorf("Harness CausationID = %q, want the Finance task's own %v", id.CausationID, claimedTask.CausationID)
	}
	if strings.HasPrefix(id.CorrelationID, "finrev-corr:") || strings.HasPrefix(id.CausationID, "finrev-cause:") {
		t.Errorf("Harness identity still carries the OLD synthetic lineage prefix: correlation=%q causation=%q", id.CorrelationID, id.CausationID)
	}
	if id.TaskID != fx.task.ID {
		t.Errorf("Harness TaskID = %d, want %d", id.TaskID, fx.task.ID)
	}
	if id.RoleID != claimedTask.AssignedRoleID {
		t.Errorf("Harness RoleID = %q, want %q", id.RoleID, claimedTask.AssignedRoleID)
	}
	if id.ExecutionPrincipalID != "finance-principal-local-test" {
		t.Errorf("Harness ExecutionPrincipalID = %q, want the configured holder principal", id.ExecutionPrincipalID)
	}

	descriptor, err := fx.descriptors.ReadRunDescriptor(context.Background(), "org-test", id.RunID)
	if err != nil {
		t.Fatalf("read run descriptor: %v", err)
	}
	wantDigest := expectedFinanceContextDigest(t, fx.taskCoord, fx.task.ID)
	if descriptor.ContextDigest != wantDigest {
		t.Errorf("RunDescriptor.ContextDigest = %q, want the rendered-content digest %q", descriptor.ContextDigest, wantDigest)
	}
	if descriptor.ContextDigest == fx.proposal.CanonicalHash {
		t.Error("RunDescriptor.ContextDigest equals proposal.CanonicalHash -- the exact substitution this hotfix removes")
	}
}

// TestRealHarness_NoToolsNegative is section 22: a model that returns a
// tool intent must be denied by financeToolCatalog before
// financeToolExecutor is ever reached -- Finance has Tools=nil,
// MaxToolCalls=0, and the Harness's own catalog lookup denies any unknown
// tool before an executor runs. No FinancialReview may be persisted.
func TestRealHarness_NoToolsNegative(t *testing.T) {
	model := &capturingModel{script: func(executionharness.NormalizedModelRequest) (executionharness.ModelResult, error) {
		return executionharness.ModelResult{
			FinishReason: executionharness.FinishTools,
			ToolRequests: []executionharness.ToolRequest{{ToolCallID: "call_1", ToolName: "shell.execute", Arguments: []byte(`{}`)}},
		}, nil
	}}
	fx := newHarnessLocalFixture(t, model)

	_, _, err := fx.finSvc.ExecuteReviewTask(context.Background(), campaign.ExecuteReviewParams{
		OrganizationID: "org-test", TaskID: fx.task.ID, ReviewRequestID: fx.reviewReq.ID,
	})
	if err == nil {
		t.Fatal("expected ExecuteReviewTask to fail closed on a tool intent, got nil error")
	}
	if len(fx.store.financialReviews) != 0 {
		t.Errorf("expected 0 financial reviews persisted, found %d", len(fx.store.financialReviews))
	}
	finalTask, getErr := fx.taskCoord.GetTask(context.Background(), fx.task.ID)
	if getErr == nil && finalTask.Status == tasks.StatusCompleted {
		t.Error("finance task reached StatusCompleted despite a denied tool intent")
	}
}

// TestRealHarness_ProviderFailure is section 23: a model error must
// terminate the Finance task's OWN attempt as a failure (recorded via
// RecordAttemptResult), never persist a review, never execute an external
// side effect, and must not itself crash or destabilize the calling
// process -- ExecuteReviewTask returns an ordinary error, not a panic.
func TestRealHarness_ProviderFailure(t *testing.T) {
	providerErr := fmt.Errorf("simulated provider outage")
	model := &capturingModel{script: func(executionharness.NormalizedModelRequest) (executionharness.ModelResult, error) {
		return executionharness.ModelResult{}, providerErr
	}}
	fx := newHarnessLocalFixture(t, model)

	_, _, err := fx.finSvc.ExecuteReviewTask(context.Background(), campaign.ExecuteReviewParams{
		OrganizationID: "org-test", TaskID: fx.task.ID, ReviewRequestID: fx.reviewReq.ID,
	})
	if err == nil {
		t.Fatal("expected ExecuteReviewTask to return an error on provider failure")
	}
	if len(fx.store.financialReviews) != 0 {
		t.Errorf("expected 0 financial reviews persisted, found %d", len(fx.store.financialReviews))
	}
	finalTask, getErr := fx.taskCoord.GetTask(context.Background(), fx.task.ID)
	if getErr != nil {
		t.Fatalf("read finance task: %v", getErr)
	}
	if finalTask.Status == tasks.StatusCompleted {
		t.Error("finance task reached StatusCompleted despite a provider/model failure")
	}
	// The failure must be attributable to THIS task's own attempt, never a
	// process-level panic: reaching this line at all (ExecuteReviewTask
	// returned normally, not via a recovered panic) is itself part of the
	// proof, together with the durable failure state above.
}
