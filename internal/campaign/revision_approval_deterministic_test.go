package campaign_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/campaign"
)

type fakeApprovalAuthorizer struct {
	allowed map[string]bool
}

func (a fakeApprovalAuthorizer) Authorize(_ context.Context, _ string, _ int64, roleID, capability string) error {
	key := roleID + ":" + capability
	if a.allowed[key] {
		return nil
	}
	return campaign.ErrUnauthorized
}

func setupRevisionApprovalFixture() (*memCampaignStore, *campaign.ApprovalService, campaign.CampaignProposal, campaign.CampaignFinancialReview) {
	store := newMemCampaignStore()
	auth := fakeApprovalAuthorizer{
		allowed: map[string]bool{
			"empresa/human:" + campaign.CapabilityOwnerApprovalCreate: true,
			"owner:" + campaign.CapabilityOwnerApprovalCreate:         true,
		},
	}
	svc := campaign.NewApprovalService(store, auth, permissiveRequirements())

	p1Hash, _ := campaign.ComputeCanonicalHash(campaign.CanonicalPayload{
		Title: "Campaign v1",
		Goal:  "Acquire users",
	})
	p1, _, _ := store.CreateProposal(context.Background(), campaign.CreateProposalCommand{
		OrganizationID:  "org-1",
		CreatedByRoleID: "empresa/ceo",
		Title:           "Campaign v1",
		Goal:            "Acquire users",
		IdempotencyKey:  "prop-key-1",
		CanonicalHash:   p1Hash,
	})

	r1Hash, _ := campaign.ComputeReviewCanonicalHash(campaign.ReviewCanonicalPayload{ProposalID: p1.ID,
		ProposalCanonicalHash: p1.CanonicalHash,
		ReviewerRoleID:        "empresa/finanzas",
		Verdict:               campaign.VerdictRecommended,
	})
	rev1, _, _ := store.RecordFinancialReview(context.Background(), campaign.RecordFinancialReviewCommand{
		OrganizationID:        "org-1",
		ReviewRequestID:       101,
		ProposalID:            p1.ID,
		ProposalCanonicalHash: p1.CanonicalHash,
		ReviewerRoleID:        "empresa/finanzas",
		Verdict:               campaign.VerdictRecommended,
		RecommendedBudget: &campaign.BudgetRecommendation{
			MaxUSD:        5000.0,
			MaxTokens:     100000,
			MaxModelCalls: 50,
			MaxWallTimeMS: 3600000,
			MaxDepth:      5,
			MaxRetries:    3,
			MaxSubagents:  2,
		},
		CanonicalHash: r1Hash,
	})

	return store, svc, p1, rev1
}

func TestDeterministicMatrix(t *testing.T) {
	ctx := context.Background()

	// A. CREATE REVISION: v1 -> revise -> v2
	t.Run("A_CreateRevision", func(t *testing.T) {
		store, _, p1, _ := setupRevisionApprovalFixture()
		p2Hash, _ := campaign.ComputeCanonicalHash(campaign.CanonicalPayload{
			Title: "Campaign v2",
			Goal:  "Acquire 1000 verified users",
		})
		v2, reused, err := store.CreateRevision(ctx, campaign.CreateRevisionCommand{
			OrganizationID:   "org-1",
			ParentProposalID: p1.ID,
			Title:            "Campaign v2",
			Goal:             "Acquire 1000 verified users",
			IdempotencyKey:   "rev-key-v2",
			CanonicalHash:    p2Hash,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if reused {
			t.Fatal("expected new revision, got reused")
		}
		if v2.RevisionNumber != 2 {
			t.Fatalf("expected revision 2, got %d", v2.RevisionNumber)
		}
		if v2.ParentProposalID == nil || *v2.ParentProposalID != p1.ID {
			t.Fatalf("expected parent ID %d, got %v", p1.ID, v2.ParentProposalID)
		}
		if v2.RootProposalID == nil || *v2.RootProposalID != p1.ID {
			t.Fatalf("expected root ID %d, got %v", p1.ID, v2.RootProposalID)
		}
		if v2.CanonicalHash == p1.CanonicalHash {
			t.Fatal("revision hash must differ from parent")
		}
	})

	// B. REVISION REPLAY: same mutation -> same v2
	t.Run("B_RevisionReplay", func(t *testing.T) {
		store, _, p1, _ := setupRevisionApprovalFixture()
		p2Hash, _ := campaign.ComputeCanonicalHash(campaign.CanonicalPayload{
			Title: "Campaign v2",
			Goal:  "Acquire 1000 users",
		})
		cmd := campaign.CreateRevisionCommand{
			OrganizationID:   "org-1",
			ParentProposalID: p1.ID,
			Title:            "Campaign v2",
			Goal:             "Acquire 1000 users",
			IdempotencyKey:   "rev-key-v2",
			CanonicalHash:    p2Hash,
		}
		v2First, reused1, err := store.CreateRevision(ctx, cmd)
		if err != nil || reused1 {
			t.Fatalf("first create failed: %v, reused=%v", err, reused1)
		}
		v2Second, reused2, err := store.CreateRevision(ctx, cmd)
		if err != nil {
			t.Fatalf("replay failed: %v", err)
		}
		if !reused2 {
			t.Fatal("expected replay to be reused")
		}
		if v2Second.ID != v2First.ID {
			t.Fatalf("expected same ID %d, got %d", v2First.ID, v2Second.ID)
		}
	})

	// C. REVISION PAYLOAD CONFLICT: same key, different payload -> conflict
	t.Run("C_RevisionPayloadConflict", func(t *testing.T) {
		store, _, p1, _ := setupRevisionApprovalFixture()
		p2Hash, _ := campaign.ComputeCanonicalHash(campaign.CanonicalPayload{Title: "v2a"})
		cmd1 := campaign.CreateRevisionCommand{
			OrganizationID:   "org-1",
			ParentProposalID: p1.ID,
			Title:            "v2a",
			IdempotencyKey:   "rev-key-conflict",
			CanonicalHash:    p2Hash,
		}
		_, _, _ = store.CreateRevision(ctx, cmd1)

		p2bHash, _ := campaign.ComputeCanonicalHash(campaign.CanonicalPayload{Title: "v2b"})
		cmd2 := campaign.CreateRevisionCommand{
			OrganizationID:   "org-1",
			ParentProposalID: p1.ID,
			Title:            "v2b",
			IdempotencyKey:   "rev-key-conflict",
			CanonicalHash:    p2bHash,
		}
		_, _, err := store.CreateRevision(ctx, cmd2)
		if !errors.Is(err, campaign.ErrIdempotencyConflict) {
			t.Fatalf("expected ErrIdempotencyConflict, got %v", err)
		}
	})

	// D. STALE PARENT REVISION: v1 -> v2 exists, attempt new child of v1 -> DENY
	t.Run("D_StaleParentRevision", func(t *testing.T) {
		store, _, p1, _ := setupRevisionApprovalFixture()
		h2, _ := campaign.ComputeCanonicalHash(campaign.CanonicalPayload{Title: "v2"})
		_, _, err := store.CreateRevision(ctx, campaign.CreateRevisionCommand{
			OrganizationID:   "org-1",
			ParentProposalID: p1.ID,
			Title:            "v2",
			IdempotencyKey:   "key-v2",
			CanonicalHash:    h2,
		})
		if err != nil {
			t.Fatal(err)
		}

		// Now try to create another revision branching from v1
		hFork, _ := campaign.ComputeCanonicalHash(campaign.CanonicalPayload{Title: "v2-fork"})
		_, _, err = store.CreateRevision(ctx, campaign.CreateRevisionCommand{
			OrganizationID:   "org-1",
			ParentProposalID: p1.ID,
			Title:            "v2-fork",
			IdempotencyKey:   "key-fork",
			CanonicalHash:    hFork,
		})
		if !errors.Is(err, campaign.ErrStaleParentRevision) {
			t.Fatalf("expected ErrStaleParentRevision, got %v", err)
		}
	})

	// E. OLD FINANCE REVIEW INVALID: v2 + review(v1) -> DENY
	t.Run("E_OldFinanceReviewInvalid", func(t *testing.T) {
		store, svc, p1, rev1 := setupRevisionApprovalFixture()
		h2, _ := campaign.ComputeCanonicalHash(campaign.CanonicalPayload{Title: "v2"})
		v2, _, _ := store.CreateRevision(ctx, campaign.CreateRevisionCommand{
			OrganizationID:   "org-1",
			ParentProposalID: p1.ID,
			Title:            "v2",
			IdempotencyKey:   "key-v2",
			CanonicalHash:    h2,
		})

		_, _, err := svc.ApproveForExecution(ctx, campaign.ApproveParams{
			OrganizationID:    "org-1",
			ProposalID:        v2.ID,
			FinancialReviewID: rev1.ID,
			ApprovedByRoleID:  "empresa/human",
			ToolCallID:        "call-1",
		})
		if !errors.Is(err, campaign.ErrProposalHashMismatch) {
			t.Fatalf("expected ErrProposalHashMismatch, got %v", err)
		}
	})

	// F. NEW REVIEW REQUIRED: v2 -> review status absent until requested
	t.Run("F_NewReviewRequired", func(t *testing.T) {
		store, _, p1, _ := setupRevisionApprovalFixture()
		h2, _ := campaign.ComputeCanonicalHash(campaign.CanonicalPayload{Title: "v2"})
		v2, _, _ := store.CreateRevision(ctx, campaign.CreateRevisionCommand{
			OrganizationID:   "org-1",
			ParentProposalID: p1.ID,
			Title:            "v2",
			IdempotencyKey:   "key-v2",
			CanonicalHash:    h2,
		})
		_, err := store.GetLatestFinancialReviewForProposal(ctx, "org-1", v2.ID)
		if !errors.Is(err, campaign.ErrFinancialReviewNotFound) {
			t.Fatalf("expected ErrFinancialReviewNotFound for new revision, got %v", err)
		}
	})

	// G. RECOMMENDED REVIEW APPROVAL: v2, review(v2)=recommended, owner explicit approval -> exactly 1 approval
	t.Run("G_RecommendedReviewApproval", func(t *testing.T) {
		store, svc, p1, _ := setupRevisionApprovalFixture()
		h2, _ := campaign.ComputeCanonicalHash(campaign.CanonicalPayload{Title: "v2"})
		v2, _, _ := store.CreateRevision(ctx, campaign.CreateRevisionCommand{
			OrganizationID:   "org-1",
			ParentProposalID: p1.ID,
			Title:            "v2",
			IdempotencyKey:   "key-v2",
			CanonicalHash:    h2,
		})

		r2Hash, _ := campaign.ComputeReviewCanonicalHash(campaign.ReviewCanonicalPayload{ProposalID: v2.ID,
			ProposalCanonicalHash: v2.CanonicalHash,
			ReviewerRoleID:        "empresa/finanzas",
			Verdict:               campaign.VerdictRecommended,
		})
		budget := &campaign.BudgetRecommendation{
			MaxUSD:        8000.0,
			MaxTokens:     200000,
			MaxModelCalls: 100,
			MaxWallTimeMS: 7200000,
			MaxDepth:      6,
			MaxRetries:    4,
			MaxSubagents:  3,
		}
		rev2, _, _ := store.RecordFinancialReview(ctx, campaign.RecordFinancialReviewCommand{
			OrganizationID:        "org-1",
			ReviewRequestID:       102,
			ProposalID:            v2.ID,
			ProposalCanonicalHash: v2.CanonicalHash,
			ReviewerRoleID:        "empresa/finanzas",
			Verdict:               campaign.VerdictRecommended,
			RecommendedBudget:     budget,
			CanonicalHash:         r2Hash,
		})

		approval, reused, err := svc.ApproveForExecution(ctx, campaign.ApproveParams{
			OrganizationID:    "org-1",
			ProposalID:        v2.ID,
			FinancialReviewID: rev2.ID,
			ApprovedByRoleID:  "empresa/human",
			ToolCallID:        "call-appr-1",
		})
		if err != nil {
			t.Fatalf("approval failed: %v", err)
		}
		if reused {
			t.Fatal("expected initial approval not reused")
		}
		if approval.Status != campaign.StatusApprovedForExecution {
			t.Fatalf("expected status approved_for_execution, got %s", approval.Status)
		}
		if approval.ProposalID != v2.ID || approval.FinancialReviewID != rev2.ID {
			t.Fatal("approval tuple does not match")
		}
	})

	// H. APPROVAL REPLAY: same mutation -> same approval
	t.Run("H_ApprovalReplay", func(t *testing.T) {
		_, svc, p1, rev1 := setupRevisionApprovalFixture()
		params := campaign.ApproveParams{
			OrganizationID:    "org-1",
			ProposalID:        p1.ID,
			FinancialReviewID: rev1.ID,
			ApprovedByRoleID:  "empresa/human",
			ConversationID:    10,
			TurnTaskID:        20,
			ToolCallID:        "call-appr-replay",
		}
		appr1, reused1, err := svc.ApproveForExecution(ctx, params)
		if err != nil || reused1 {
			t.Fatalf("initial approval failed: %v, reused=%v", err, reused1)
		}

		appr2, reused2, err := svc.ApproveForExecution(ctx, params)
		if err != nil {
			t.Fatalf("replay failed: %v", err)
		}
		if !reused2 {
			t.Fatal("expected reused=true on replay")
		}
		if appr2.ID != appr1.ID {
			t.Fatalf("expected ID %d, got %d", appr1.ID, appr2.ID)
		}
	})

	// I. APPROVAL CONFLICT: same mutation identity + different payload -> DENY
	t.Run("I_ApprovalConflict", func(t *testing.T) {
		store, svc, p1, rev1 := setupRevisionApprovalFixture()
		params := campaign.ApproveParams{
			OrganizationID:    "org-1",
			ProposalID:        p1.ID,
			FinancialReviewID: rev1.ID,
			ApprovedByRoleID:  "empresa/human",
			ConversationID:    10,
			TurnTaskID:        20,
			ToolCallID:        "call-appr-conflict",
		}
		_, _, _ = svc.ApproveForExecution(ctx, params)

		// Directly attempt to reuse key with conflict in store
		_, _, err := store.CreateOwnerApproval(ctx, campaign.CreateOwnerApprovalCommand{
			OrganizationID:               "org-1",
			ProposalID:                   p1.ID,
			ProposalCanonicalHash:        "0000000000000000000000000000000000000000000000000000000000000000",
			FinancialReviewID:            rev1.ID,
			FinancialReviewCanonicalHash: rev1.CanonicalHash,
			ApprovedByRoleID:             "empresa/human",
			ToolCallID:                   "call-appr-conflict",
			IdempotencyKey:               "cappr:10:20:call-appr-conflict:" + p1.CanonicalHash + ":" + rev1.CanonicalHash,
			CanonicalHash:                "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
		})
		if !errors.Is(err, campaign.ErrApprovalConflict) {
			t.Fatalf("expected ErrApprovalConflict, got %v", err)
		}
	})

	// J, K, L. NON-RECOMMENDED VERDICTS DENIED
	for _, tc := range []struct {
		name    string
		verdict campaign.FinancialReviewVerdict
	}{
		{"J_ChangesRequested", campaign.VerdictChangesRequested},
		{"K_NotRecommended", campaign.VerdictNotRecommended},
		{"L_InsufficientData", campaign.VerdictInsufficientData},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, svc, p1, _ := setupRevisionApprovalFixture()
			rHash, _ := campaign.ComputeReviewCanonicalHash(campaign.ReviewCanonicalPayload{ProposalID: p1.ID,
				ProposalCanonicalHash: p1.CanonicalHash,
				ReviewerRoleID:        "empresa/finanzas",
				Verdict:               tc.verdict,
			})
			rev, _, _ := store.RecordFinancialReview(ctx, campaign.RecordFinancialReviewCommand{
				OrganizationID:        "org-1",
				ReviewRequestID:       200,
				ProposalID:            p1.ID,
				ProposalCanonicalHash: p1.CanonicalHash,
				ReviewerRoleID:        "empresa/finanzas",
				Verdict:               tc.verdict,
				RecommendedBudget:     &campaign.BudgetRecommendation{MaxUSD: 100},
				CanonicalHash:         rHash,
			})

			_, _, err := svc.ApproveForExecution(ctx, campaign.ApproveParams{
				OrganizationID:    "org-1",
				ProposalID:        p1.ID,
				FinancialReviewID: rev.ID,
				ApprovedByRoleID:  "empresa/human",
				ToolCallID:        "call-deny",
			})
			if !errors.Is(err, campaign.ErrReviewNotRecommended) {
				t.Fatalf("expected ErrReviewNotRecommended for %s, got %v", tc.verdict, err)
			}
		})
	}

	// M. FINANCE SELF-APPROVAL -> DENY
	t.Run("M_FinanceSelfApproval", func(t *testing.T) {
		_, svc, p1, rev1 := setupRevisionApprovalFixture()
		_, _, err := svc.ApproveForExecution(ctx, campaign.ApproveParams{
			OrganizationID:    "org-1",
			ProposalID:        p1.ID,
			FinancialReviewID: rev1.ID,
			ApprovedByRoleID:  "empresa/finanzas", // reviewer attempted self-approval
			ToolCallID:        "call-self",
		})
		if !errors.Is(err, campaign.ErrSeparationOfDutiesViolation) && !errors.Is(err, campaign.ErrUnauthorized) {
			t.Fatalf("expected ErrSeparationOfDutiesViolation or ErrUnauthorized, got %v", err)
		}
	})

	// N. CEO SELF-APPROVAL WITHOUT OWNER -> DENY
	t.Run("N_CEOSelfApprovalWithoutOwner", func(t *testing.T) {
		_, svc, p1, rev1 := setupRevisionApprovalFixture()
		_, _, err := svc.ApproveForExecution(ctx, campaign.ApproveParams{
			OrganizationID:    "org-1",
			ProposalID:        p1.ID,
			FinancialReviewID: rev1.ID,
			ApprovedByRoleID:  "empresa/ceo", // CEO role lacks owner authority
			ToolCallID:        "call-ceo-self",
		})
		if !errors.Is(err, campaign.ErrUnauthorized) {
			t.Fatalf("expected ErrUnauthorized for CEO without owner grant, got %v", err)
		}
	})

	// O. CROSS-ORG -> DENY
	t.Run("O_CrossOrgDeny", func(t *testing.T) {
		_, svc, p1, rev1 := setupRevisionApprovalFixture()
		_, _, err := svc.ApproveForExecution(ctx, campaign.ApproveParams{
			OrganizationID:    "org-other",
			ProposalID:        p1.ID,
			FinancialReviewID: rev1.ID,
			ApprovedByRoleID:  "empresa/human",
			ToolCallID:        "call-cross",
		})
		if err == nil {
			t.Fatal("expected cross-org error, got nil")
		}
	})

	// P. BUDGET EXACT PIN: all 7 dimensions match exactly
	t.Run("P_BudgetExactPin", func(t *testing.T) {
		_, svc, p1, rev1 := setupRevisionApprovalFixture()
		appr, _, err := svc.ApproveForExecution(ctx, campaign.ApproveParams{
			OrganizationID:    "org-1",
			ProposalID:        p1.ID,
			FinancialReviewID: rev1.ID,
			ApprovedByRoleID:  "empresa/human",
			ToolCallID:        "call-pin-test",
		})
		if err != nil {
			t.Fatal(err)
		}
		expected := *rev1.RecommendedBudget
		actual := appr.ExecutionBudget
		if actual.MaxUSD != expected.MaxUSD ||
			actual.MaxTokens != expected.MaxTokens ||
			actual.MaxModelCalls != expected.MaxModelCalls ||
			actual.MaxWallTimeMS != expected.MaxWallTimeMS ||
			actual.MaxDepth != expected.MaxDepth ||
			actual.MaxRetries != expected.MaxRetries ||
			actual.MaxSubagents != expected.MaxSubagents {
			t.Fatalf("budget dimensions mismatch: actual=%+v expected=%+v", actual, expected)
		}
	})

	// Q. REVISION AFTER APPROVAL: approval(v2), create v3 -> approval(v2) not applicable to v3
	t.Run("Q_RevisionAfterApproval", func(t *testing.T) {
		store, svc, p1, _ := setupRevisionApprovalFixture()
		h2, _ := campaign.ComputeCanonicalHash(campaign.CanonicalPayload{Title: "v2"})
		v2, _, _ := store.CreateRevision(ctx, campaign.CreateRevisionCommand{
			OrganizationID:   "org-1",
			ParentProposalID: p1.ID,
			Title:            "v2",
			IdempotencyKey:   "key-v2-q",
			CanonicalHash:    h2,
		})

		r2Hash, _ := campaign.ComputeReviewCanonicalHash(campaign.ReviewCanonicalPayload{ProposalID: v2.ID,
			ProposalCanonicalHash: v2.CanonicalHash,
			ReviewerRoleID:        "empresa/finanzas",
			Verdict:               campaign.VerdictRecommended,
		})
		rev2, _, _ := store.RecordFinancialReview(ctx, campaign.RecordFinancialReviewCommand{
			OrganizationID:        "org-1",
			ReviewRequestID:       102,
			ProposalID:            v2.ID,
			ProposalCanonicalHash: v2.CanonicalHash,
			ReviewerRoleID:        "empresa/finanzas",
			Verdict:               campaign.VerdictRecommended,
			RecommendedBudget: &campaign.BudgetRecommendation{
				MaxUSD: 500, MaxTokens: 1000, MaxModelCalls: 1, MaxWallTimeMS: 60000,
				MaxDepth: 1, MaxRetries: 1, MaxSubagents: 1,
			},
			CanonicalHash: r2Hash,
		})

		apprV2, _, err := svc.ApproveForExecution(ctx, campaign.ApproveParams{
			OrganizationID:    "org-1",
			ProposalID:        v2.ID,
			FinancialReviewID: rev2.ID,
			ApprovedByRoleID:  "empresa/human",
			ToolCallID:        "call-appr-v2",
		})
		if err != nil {
			t.Fatal(err)
		}

		// Now owner creates v3
		h3, _ := campaign.ComputeCanonicalHash(campaign.CanonicalPayload{Title: "v3"})
		v3, _, err := store.CreateRevision(ctx, campaign.CreateRevisionCommand{
			OrganizationID:   "org-1",
			ParentProposalID: v2.ID,
			Title:            "v3",
			IdempotencyKey:   "key-v3-q",
			CanonicalHash:    h3,
		})
		if err != nil {
			t.Fatal(err)
		}

		// Verify approval for v2 is NOT valid for v3
		if apprV2.ProposalID == v3.ID || apprV2.ProposalCanonicalHash == v3.CanonicalHash {
			t.Fatal("approval(v2) cannot match v3")
		}
		_, err = store.GetOwnerApprovalByProposal(ctx, "org-1", v3.ID)
		if !errors.Is(err, campaign.ErrApprovalNotFound) {
			t.Fatalf("expected ErrApprovalNotFound for v3, got %v", err)
		}
	})

	// R. NO EXECUTION SIDE EFFECTS
	t.Run("R_NoExecutionSideEffects", func(t *testing.T) {
		// Verify proposal status and approval do not alter execution flags
		store, svc, p1, rev1 := setupRevisionApprovalFixture()
		appr, _, err := svc.ApproveForExecution(ctx, campaign.ApproveParams{
			OrganizationID:    "org-1",
			ProposalID:        p1.ID,
			FinancialReviewID: rev1.ID,
			ApprovedByRoleID:  "empresa/human",
			ToolCallID:        "call-appr-r",
		})
		if err != nil {
			t.Fatal(err)
		}
		if appr.Status != campaign.StatusApprovedForExecution {
			t.Fatalf("expected approved_for_execution, got %s", appr.Status)
		}
		pCheck, err := store.GetProposal(ctx, "org-1", p1.ID)
		if err != nil {
			t.Fatal(err)
		}
		if pCheck.ExecutionStarted {
			t.Fatal("pCheck.ExecutionStarted must remain false")
		}
	})
}

// TestApprovalHistoricalDefense_ZeroSubagentsRejected proves that a historical-style
// recommended FinancialReview containing max_subagents = 0 cannot produce a new
// owner approval (CAMPAIGN_EXECUTABLE_BUDGET_CONTRACT_HOTFIX_V1 section 20).
func TestApprovalHistoricalDefense_ZeroSubagentsRejected(t *testing.T) {
	ctx := context.Background()
	store, svc, p1, _ := setupRevisionApprovalFixture()

	invalidBudget := campaign.BudgetRecommendation{
		MaxUSD: 500, MaxTokens: 1000, MaxModelCalls: 1, MaxWallTimeMS: 60000,
		MaxDepth: 1, MaxRetries: 1, MaxSubagents: 0, // invalid zero subagents (production historical shape)
	}
	rHash, _ := campaign.ComputeReviewCanonicalHash(campaign.ReviewCanonicalPayload{
		ProposalID:            p1.ID,
		ProposalCanonicalHash: p1.CanonicalHash,
		ReviewerRoleID:        "empresa/finanzas",
		Verdict:               campaign.VerdictRecommended,
		RecommendedBudget:     &invalidBudget,
	})

	rev, _, err := store.RecordFinancialReview(ctx, campaign.RecordFinancialReviewCommand{
		OrganizationID:        "org-1",
		ReviewRequestID:       999,
		ProposalID:            p1.ID,
		ProposalCanonicalHash: p1.CanonicalHash,
		ReviewerRoleID:        "empresa/finanzas",
		Verdict:               campaign.VerdictRecommended,
		RecommendedBudget:     &invalidBudget,
		CanonicalHash:         rHash,
	})
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = svc.ApproveForExecution(ctx, campaign.ApproveParams{
		OrganizationID:    "org-1",
		ProposalID:        p1.ID,
		FinancialReviewID: rev.ID,
		ApprovedByRoleID:  "empresa/human",
		ToolCallID:        "call-hist-appr",
	})
	if err == nil {
		t.Fatal("expected error approving historical review with zero subagents, got nil")
	}
	if !errors.Is(err, campaign.ErrInvalidExecutionBudget) {
		t.Fatalf("expected ErrInvalidExecutionBudget, got: %v", err)
	}

	if len(store.approvals) != 0 {
		t.Fatalf("expected 0 approvals created, found %d", len(store.approvals))
	}
}
