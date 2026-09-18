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
	"encoding/json"
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
// wherever a test needs the model to answer successfully.
const validFinanceOutputJSON = `{"verdict":"recommended","summary":"Harness composition local test."}`

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

// expectedFinancePromptDigest mirrors internal/campaign/finance_service.go's
// own promptContent rendering exactly, so the test can assert the
// persisted RunDescriptor.ContextDigest is the ACTUAL rendered content's
// digest -- never the proposal's own canonical hash substituted in its
// place, which is exactly the defect this hotfix corrects.
func expectedFinancePromptDigest(t *testing.T, prop campaign.CampaignProposal) string {
	t.Helper()
	proposalData, err := json.MarshalIndent(prop, "", "  ")
	if err != nil {
		t.Fatalf("marshal proposal: %v", err)
	}
	promptContent := fmt.Sprintf(`Proposal ID: %d
Proposal Canonical Hash: %s
Proposal Data:
%s

Perform conservative financial review following instructions.`,
		prop.ID, prop.CanonicalHash, string(proposalData))
	sum := sha256.Sum256([]byte(promptContent))
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
	wantDigest := expectedFinancePromptDigest(t, fx.proposal)
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
