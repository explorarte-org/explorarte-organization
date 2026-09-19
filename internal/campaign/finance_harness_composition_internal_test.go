package campaign

// FINANCE_HARNESS_RUNTIME_HOTFIX_V1: white-box tests for the pieces of
// runHarnessModel's own composition that are only reachable from inside
// this package -- the unexported financeToolCatalog/financeToolExecutor
// types, computeFinanceRunID's determinism, and
// validateFinanceHarnessPreconditions' fail-closed behavior. The full,
// real Harness execution path (real Postgres, real Task Engine, real
// Model Runtime, test.fake provider) is proven black-box in
// internal/ceochat's own integration test suite -- this file pins the
// composition seam itself.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/executionharness"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

// TestBaseConstructorBugRepro_NilToolDepsRejected pins the exact production
// defect production task 769 hit: executionharness.NewWithDescriptorStore
// requires every one of authority/models/catalog/tools/history/descriptors
// to be non-nil, and internal/campaign/finance_service.go's own
// runHarnessModel used to pass literal nil, nil for catalog/tools on every
// real (non-MockOutput) Finance execution -- deterministically, on every
// call, regardless of task/lineage correctness.
func TestBaseConstructorBugRepro_NilToolDepsRejected(t *testing.T) {
	_, err := executionharness.NewWithDescriptorStore(
		fakeHarnessAuthority{}, fakeHarnessModel{}, nil, nil,
		executionharness.NewMemoryHistoryStore(), executionharness.NewMemoryRunDescriptorStore(),
	)
	if err == nil {
		t.Fatal("expected an error constructing the harness runtime with nil catalog/tools, got nil")
	}
	if !errors.Is(err, executionharness.ErrInvalidRun) {
		t.Errorf("error = %v, want wrapping executionharness.ErrInvalidRun", err)
	}
	if !strings.Contains(err.Error(), "harness dependencies are incomplete") {
		t.Errorf("error = %q, want it to contain %q (production's own exact failure text)", err.Error(), "harness dependencies are incomplete")
	}
}

// TestFixedConstructor_FinanceToolTypesSatisfyDependencies proves the fix:
// the SAME constructor, given financeToolCatalog{}/financeToolExecutor{}
// instead of nil, nil, succeeds.
func TestFixedConstructor_FinanceToolTypesSatisfyDependencies(t *testing.T) {
	runtime, err := executionharness.NewWithDescriptorStore(
		fakeHarnessAuthority{}, fakeHarnessModel{}, financeToolCatalog{}, financeToolExecutor{},
		executionharness.NewMemoryHistoryStore(), executionharness.NewMemoryRunDescriptorStore(),
	)
	if err != nil {
		t.Fatalf("expected the fixed constructor to succeed, got: %v", err)
	}
	if runtime == nil {
		t.Fatal("expected a non-nil runtime")
	}
}

// financeToolCatalog/financeToolExecutor must actually satisfy the ports
// they are meant to fill -- pinned here too, alongside the package-level
// var _ assertions in finance_service.go itself, as a second, independent
// guard against either drifting.
func TestFinanceToolTypes_SatisfyHarnessPorts(t *testing.T) {
	var _ executionharness.ToolCatalog = financeToolCatalog{}
	var _ executionharness.ToolExecutor = financeToolExecutor{}

	if _, ok := (financeToolCatalog{}).Lookup(context.Background(), "anything"); ok {
		t.Error("financeToolCatalog.Lookup found a tool -- expected zero tools, always")
	}
	if err := (financeToolCatalog{}).ValidateArguments(context.Background(), executionharness.ToolDefinition{}, nil); err == nil {
		t.Error("financeToolCatalog.ValidateArguments succeeded -- expected a deny-all error")
	}
	if _, err := (financeToolExecutor{}).Execute(context.Background(), executionharness.RunIdentity{}, executionharness.ToolRequest{}); err == nil {
		t.Error("financeToolExecutor.Execute succeeded -- expected a deny-all error; it must never perform a real side effect")
	}
}

// TestComputeFinanceRunID_Deterministic proves computeFinanceRunID's own
// contract: no clock, no randomness, no process-local state -- the exact
// same durable facts always produce the exact same RunID, and changing any
// one of them (a different attempt, a different review request, a
// different proposal/hash) changes the RunID.
func TestComputeFinanceRunID_Deterministic(t *testing.T) {
	base := financeRunIdentityPayload{
		OrganizationID: "org-test", FinanceTaskID: 100, FinanceAttemptID: 1,
		ReviewRequestID: 10, ProposalID: 1, ProposalCanonicalHash: "hash-1", ReviewerRoleID: "negocio/administrador_financiero",
	}
	id1, err := computeFinanceRunID(base)
	if err != nil {
		t.Fatalf("computeFinanceRunID: %v", err)
	}
	id2, err := computeFinanceRunID(base)
	if err != nil {
		t.Fatalf("computeFinanceRunID (again): %v", err)
	}
	if id1 != id2 {
		t.Errorf("same durable facts produced different RunIDs: %q vs %q", id1, id2)
	}
	if len(id1) < len("finrev-") || id1[:len("finrev-")] != "finrev-" {
		t.Errorf("RunID = %q, want the finrev-<hex> shape", id1)
	}

	variants := []struct {
		name   string
		mutate func(financeRunIdentityPayload) financeRunIdentityPayload
	}{
		{"different_attempt", func(p financeRunIdentityPayload) financeRunIdentityPayload { p.FinanceAttemptID = 2; return p }},
		{"different_review_request", func(p financeRunIdentityPayload) financeRunIdentityPayload { p.ReviewRequestID = 11; return p }},
		{"different_proposal", func(p financeRunIdentityPayload) financeRunIdentityPayload { p.ProposalID = 2; return p }},
		{"different_proposal_hash", func(p financeRunIdentityPayload) financeRunIdentityPayload {
			p.ProposalCanonicalHash = "hash-2"
			return p
		}},
		{"different_task", func(p financeRunIdentityPayload) financeRunIdentityPayload { p.FinanceTaskID = 200; return p }},
		{"different_org", func(p financeRunIdentityPayload) financeRunIdentityPayload { p.OrganizationID = "org-other"; return p }},
		{"different_role", func(p financeRunIdentityPayload) financeRunIdentityPayload {
			p.ReviewerRoleID = "negocio/other"
			return p
		}},
	}
	for _, v := range variants {
		t.Run(v.name, func(t *testing.T) {
			variantID, err := computeFinanceRunID(v.mutate(base))
			if err != nil {
				t.Fatalf("computeFinanceRunID: %v", err)
			}
			if variantID == id1 {
				t.Errorf("%s produced the SAME RunID as the base payload: %q", v.name, variantID)
			}
		})
	}
}

// TestValidateFinanceHarnessPreconditions exercises the fail-closed
// precondition guard directly, one invariant at a time.
func TestValidateFinanceHarnessPreconditions(t *testing.T) {
	corr, cause := "corr:1", "task:1"
	validClaimed := tasks.ClaimedTask{
		Task:       tasks.Task{ID: 100, OrganizationID: "org-test", AssignedRoleID: "negocio/administrador_financiero", CorrelationID: &corr, CausationID: &cause},
		Attempt:    tasks.Attempt{ID: 1},
		LeaseToken: "real-lease-token",
	}
	validProposal := CampaignProposal{OrganizationID: "org-test"}
	validReq := CampaignFinancialReviewRequest{ReviewerRoleID: "negocio/administrador_financiero"}
	validHolder := "finance-principal-1"

	if err := validateFinanceHarnessPreconditions(validClaimed, validProposal, validReq, validHolder); err != nil {
		t.Fatalf("expected the fully-valid case to pass, got: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(c tasks.ClaimedTask, p CampaignProposal, r CampaignFinancialReviewRequest, h string) (tasks.ClaimedTask, CampaignProposal, CampaignFinancialReviewRequest, string)
	}{
		{"zero_task_id", func(c tasks.ClaimedTask, p CampaignProposal, r CampaignFinancialReviewRequest, h string) (tasks.ClaimedTask, CampaignProposal, CampaignFinancialReviewRequest, string) {
			c.Task.ID = 0
			return c, p, r, h
		}},
		{"zero_attempt_id", func(c tasks.ClaimedTask, p CampaignProposal, r CampaignFinancialReviewRequest, h string) (tasks.ClaimedTask, CampaignProposal, CampaignFinancialReviewRequest, string) {
			c.Attempt.ID = 0
			return c, p, r, h
		}},
		{"blank_lease_token", func(c tasks.ClaimedTask, p CampaignProposal, r CampaignFinancialReviewRequest, h string) (tasks.ClaimedTask, CampaignProposal, CampaignFinancialReviewRequest, string) {
			c.LeaseToken = ""
			return c, p, r, h
		}},
		{"organization_mismatch", func(c tasks.ClaimedTask, p CampaignProposal, r CampaignFinancialReviewRequest, h string) (tasks.ClaimedTask, CampaignProposal, CampaignFinancialReviewRequest, string) {
			p.OrganizationID = "org-other"
			return c, p, r, h
		}},
		{"assigned_role_mismatch", func(c tasks.ClaimedTask, p CampaignProposal, r CampaignFinancialReviewRequest, h string) (tasks.ClaimedTask, CampaignProposal, CampaignFinancialReviewRequest, string) {
			r.ReviewerRoleID = "negocio/other"
			return c, p, r, h
		}},
		{"nil_correlation", func(c tasks.ClaimedTask, p CampaignProposal, r CampaignFinancialReviewRequest, h string) (tasks.ClaimedTask, CampaignProposal, CampaignFinancialReviewRequest, string) {
			c.Task.CorrelationID = nil
			return c, p, r, h
		}},
		{"blank_correlation", func(c tasks.ClaimedTask, p CampaignProposal, r CampaignFinancialReviewRequest, h string) (tasks.ClaimedTask, CampaignProposal, CampaignFinancialReviewRequest, string) {
			blank := "   "
			c.Task.CorrelationID = &blank
			return c, p, r, h
		}},
		{"nil_causation", func(c tasks.ClaimedTask, p CampaignProposal, r CampaignFinancialReviewRequest, h string) (tasks.ClaimedTask, CampaignProposal, CampaignFinancialReviewRequest, string) {
			c.Task.CausationID = nil
			return c, p, r, h
		}},
		{"blank_holder_principal", func(c tasks.ClaimedTask, p CampaignProposal, r CampaignFinancialReviewRequest, h string) (tasks.ClaimedTask, CampaignProposal, CampaignFinancialReviewRequest, string) {
			return c, p, r, ""
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, p, r, h := tc.mutate(validClaimed, validProposal, validReq, validHolder)
			if err := validateFinanceHarnessPreconditions(c, p, r, h); err == nil {
				t.Errorf("expected %s to fail closed, got nil error", tc.name)
			} else if !errors.Is(err, ErrInvalidInput) {
				t.Errorf("%s: error = %v, want wrapping ErrInvalidInput", tc.name, err)
			}
		})
	}
}

// fakeHarnessAuthority/fakeHarnessModel are the minimal always-succeed
// stand-ins needed only to satisfy executionharness.NewWithDescriptorStore's
// own non-nil constructor check in this file's two composition tests above
// -- neither is ever actually invoked by those tests.
type fakeHarnessAuthority struct{}

func (fakeHarnessAuthority) AuthorizeExecution(context.Context, executionharness.AuthorityRequest) error {
	return nil
}

type fakeHarnessModel struct{}

func (fakeHarnessModel) Invoke(context.Context, executionharness.RunIdentity, executionharness.NormalizedModelRequest) (executionharness.ModelResult, error) {
	return executionharness.ModelResult{FinishReason: executionharness.FinishFinal, FinalOutput: "{}"}, nil
}
