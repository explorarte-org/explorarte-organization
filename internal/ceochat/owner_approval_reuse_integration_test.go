//go:build integration

// CEO_CONVERSATIONAL_FULL_STACK_PREMERGE_CLOSURE_V1 BLOCKER 2: proves
// campaign_postgres.Store.CreateOwnerApproval's reused/conflict semantics
// against REAL PostgreSQL -- the in-memory fake store used by every other
// campaign unit test cannot exercise the two real unique constraints
// (uq_approval_org_key, uq_approval_exact_tuple) or the ON CONFLICT
// resolution between them, which is exactly what was wrong before this
// round's fix.
package ceochat_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/campaign"
	campaignpostgres "github.com/Mireuz13/explorarte-organization/internal/campaign/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/ceochat"
	"github.com/Mireuz13/explorarte-organization/internal/executionharness"
)

// oneShotFinalAnswerModel is the simplest possible scripted ModelExecutor:
// it answers immediately with no tool calls, purely to produce one real
// conversation/message/task tuple for this file's own campaign_proposals
// row to reference (campaign_proposals' own FKs require a real
// conversation, message, and task -- there is no lighter way to get one).
type oneShotFinalAnswerModel struct{}

func (oneShotFinalAnswerModel) Invoke(context.Context, executionharness.RunIdentity, executionharness.NormalizedModelRequest) (executionharness.ModelResult, error) {
	return executionharness.ModelResult{FinishReason: executionharness.FinishFinal, FinalOutput: "Entendido.", InvocationRef: "one-shot-1"}, nil
}

// ownerApprovalReuseFixture seeds exactly one real proposal and one real
// recommended financial review -- the fixed tuple every case in this
// file's test matrix approves against.
type ownerApprovalReuseFixture struct {
	store          *campaignpostgres.Store
	conversationID int64
	messageID      int64
	taskID         int64
	proposalID     int64
	proposalHash   string
	reviewID       int64
	reviewHash     string
	budget         campaign.BudgetRecommendation
}

func newOwnerApprovalReuseFixture(t *testing.T) (*chatFixture, *ownerApprovalReuseFixture) {
	t.Helper()
	return newOwnerApprovalReuseFixtureOn(t, newChatFixture(t), campaign.BudgetRecommendation{
		MaxUSD: 1000, MaxTokens: 50000, MaxModelCalls: 20, MaxWallTimeMS: 3600000,
		MaxDepth: 3, MaxRetries: 2, MaxSubagents: 1,
	})
}

// newOwnerApprovalReuseFixtureOn seeds the fixture's proposal and recommended
// review on an already-open chat fixture, with the given recommended budget --
// so a test can hold a review (and later an approval) whose budget is whatever
// it needs to prove, on a runtime it composed itself.
func newOwnerApprovalReuseFixtureOn(t *testing.T, f *chatFixture, budget campaign.BudgetRecommendation) (*chatFixture, *ownerApprovalReuseFixture) {
	t.Helper()
	ctx := context.Background()
	service := f.withScriptedModel(t, oneShotFinalAnswerModel{})

	conversation, err := service.CreateConversation(ctx, ceochat.CreateConversationRequest{
		ActorRoleID: "empresa/human", OwnerRoleID: "empresa/human",
	})
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	// This whole database is shared across every test function in this
	// package's run: t.Name() makes each test's proposal/review content
	// (and therefore its canonical hashes) and idempotency keys genuinely
	// unique, so no two test functions ever collide on
	// uq_approval_exact_tuple or an idempotency key -- without this,
	// tests with identical hardcoded content would see each other's rows
	// and report reused=true for what looks, from that test's own
	// perspective, like a first creation.
	unique := t.Name()
	send, err := service.Send(ctx, ceochat.SendRequest{
		ConversationID: conversation.ID, ActorRoleID: "empresa/human",
		IdempotencyKey: "owner-approval-reuse-init-" + unique, Content: "Hola.",
	})
	if err != nil || send.Outcome != ceochat.RunOutcomeCompleted {
		t.Fatalf("seed turn: outcome=%v err=%v", send.Outcome, err)
	}

	campStore, err := campaignpostgres.New(f.store)
	if err != nil {
		t.Fatalf("open campaign store: %v", err)
	}

	pPayload := campaign.CanonicalPayload{
		Title: "Owner Approval Reuse Fixture " + unique, Goal: "Prove reused/conflict semantics against real Postgres.",
		AcceptanceCriteria: []string{"Deterministic reuse semantics"},
	}
	pHash, err := campaign.ComputeCanonicalHash(pPayload)
	if err != nil {
		t.Fatalf("ComputeCanonicalHash: %v", err)
	}
	proposal, _, err := campStore.CreateProposal(ctx, campaign.CreateProposalCommand{
		OrganizationID: chatTestOrganization, ConversationID: conversation.ID,
		CreatedFromMessageID: send.OwnerMessage.ID, TaskID: send.OwnerMessage.TaskID, AttemptID: 1,
		ToolCallID: "call-prop-reuse-1", Title: pPayload.Title, Goal: pPayload.Goal,
		AcceptanceCriteria: pPayload.AcceptanceCriteria, CanonicalHash: pHash,
		IdempotencyKey: "owner-approval-reuse-prop-1-" + unique, CreatedByRoleID: "empresa/ceo",
	})
	if err != nil {
		t.Fatalf("CreateProposal: %v", err)
	}

	rHash, err := campaign.ComputeReviewCanonicalHash(campaign.ReviewCanonicalPayload{
		ProposalID: proposal.ID, ProposalCanonicalHash: pHash, ReviewerRoleID: "negocio/administrador_financiero",
		Verdict: campaign.VerdictRecommended, RecommendedBudget: &budget, Summary: "Sound.",
	})
	if err != nil {
		t.Fatalf("ComputeReviewCanonicalHash: %v", err)
	}
	reviewReq, _, err := campStore.CreateReviewRequest(ctx, campaign.CreateReviewRequestCommand{
		OrganizationID: chatTestOrganization, ProposalID: proposal.ID, ProposalCanonicalHash: pHash,
		RequestedByRoleID: "empresa/ceo", RequestedFromConversationID: conversation.ID,
		RequestedFromMessageID: send.OwnerMessage.ID, RequestedFromTaskID: send.OwnerMessage.TaskID,
		ReviewerRoleID: "negocio/administrador_financiero", ReviewTaskID: send.OwnerMessage.TaskID,
		IdempotencyKey: "owner-approval-reuse-revreq-1-" + unique,
	})
	if err != nil {
		t.Fatalf("CreateReviewRequest: %v", err)
	}
	review, _, err := campStore.RecordFinancialReview(ctx, campaign.RecordFinancialReviewCommand{
		OrganizationID: chatTestOrganization, ReviewRequestID: reviewReq.ID, ProposalID: proposal.ID,
		ProposalCanonicalHash: pHash, ReviewerRoleID: "negocio/administrador_financiero",
		ReviewTaskID: send.OwnerMessage.TaskID, ReviewAttemptID: 1,
		Verdict: campaign.VerdictRecommended, RecommendedBudget: &budget, CanonicalHash: rHash, Summary: "Sound.",
	})
	if err != nil {
		t.Fatalf("RecordFinancialReview: %v", err)
	}

	return f, &ownerApprovalReuseFixture{
		store: campStore, conversationID: conversation.ID, messageID: send.OwnerMessage.ID, taskID: send.OwnerMessage.TaskID,
		proposalID: proposal.ID, proposalHash: pHash,
		reviewID: review.ID, reviewHash: rHash, budget: budget,
	}
}

func (rf *ownerApprovalReuseFixture) approvalCmd(t *testing.T, idempotencyKey, toolCallID string, budget campaign.BudgetRecommendation) campaign.CreateOwnerApprovalCommand {
	t.Helper()
	hash, err := campaign.ComputeApprovalCanonicalHash(campaign.ApprovalCanonicalPayload{
		OrganizationID: chatTestOrganization, ProposalID: rf.proposalID, ProposalCanonicalHash: rf.proposalHash,
		FinancialReviewID: rf.reviewID, FinancialReviewCanonicalHash: rf.reviewHash,
		ApprovedByRoleID: "empresa/human", ExecutionBudget: budget,
	})
	if err != nil {
		t.Fatalf("ComputeApprovalCanonicalHash: %v", err)
	}
	return campaign.CreateOwnerApprovalCommand{
		OrganizationID: chatTestOrganization, ProposalID: rf.proposalID, ProposalCanonicalHash: rf.proposalHash,
		FinancialReviewID: rf.reviewID, FinancialReviewCanonicalHash: rf.reviewHash,
		ApprovedByRoleID: "empresa/human",
		ConversationID:   rf.conversationID, MessageID: rf.messageID, TurnTaskID: rf.taskID,
		ToolCallID:      toolCallID,
		ExecutionBudget: budget, CanonicalHash: hash, IdempotencyKey: idempotencyKey,
	}
}

// TestCreateOwnerApprovalReusedSemantics is BLOCKER 2's core matrix: first
// creation reports reused=false; an exact replay (same idempotency key,
// same payload) reports reused=true with the SAME approval ID; the same
// idempotency key with a genuinely different payload fails closed with
// ErrApprovalConflict.
func TestCreateOwnerApprovalReusedSemantics(t *testing.T) {
	f, rf := newOwnerApprovalReuseFixture(t)
	defer f.cleanup()
	ctx := context.Background()

	first, reused, err := rf.store.CreateOwnerApproval(ctx, rf.approvalCmd(t, "reuse-key-1", "call-appr-1", rf.budget))
	if err != nil {
		t.Fatalf("first CreateOwnerApproval: %v", err)
	}
	if reused {
		t.Error("first CreateOwnerApproval reported reused=true, want false")
	}
	if first.ID == 0 {
		t.Fatal("first CreateOwnerApproval returned a zero approval ID")
	}

	replay, replayReused, err := rf.store.CreateOwnerApproval(ctx, rf.approvalCmd(t, "reuse-key-1", "call-appr-1", rf.budget))
	if err != nil {
		t.Fatalf("replay CreateOwnerApproval: %v", err)
	}
	if !replayReused {
		t.Error("replay CreateOwnerApproval reported reused=false, want true")
	}
	if replay.ID != first.ID {
		t.Errorf("replay approval ID = %d, want %d (same row)", replay.ID, first.ID)
	}

	var rowCount int
	if err := f.store.Pool().QueryRow(ctx, "SELECT count(*) FROM campaign_owner_approvals WHERE organization_id=$1 AND proposal_id=$2", chatTestOrganization, rf.proposalID).Scan(&rowCount); err != nil {
		t.Fatalf("count approvals: %v", err)
	}
	if rowCount != 1 {
		t.Errorf("total approval rows = %d, want exactly 1", rowCount)
	}

	differentBudget := rf.budget
	differentBudget.MaxUSD = rf.budget.MaxUSD + 1
	_, _, err = rf.store.CreateOwnerApproval(ctx, rf.approvalCmd(t, "reuse-key-1", "call-appr-1", differentBudget))
	if !errors.Is(err, campaign.ErrApprovalConflict) {
		t.Errorf("same idempotency key with different payload: err=%v, want ErrApprovalConflict", err)
	}
}

// TestCreateOwnerApprovalCrossTurnSameTupleConverges is BLOCKER 2's
// cross-turn case: the SAME proposal/review tuple approved again from a
// genuinely different turn (a new idempotency key, a new tool_call_id --
// exactly what a real "Apruébala nuevamente" turn produces) must converge
// on the already-durable approval, not raise a raw unique_violation on
// uq_approval_exact_tuple.
func TestCreateOwnerApprovalCrossTurnSameTupleConverges(t *testing.T) {
	f, rf := newOwnerApprovalReuseFixture(t)
	defer f.cleanup()
	ctx := context.Background()

	turnA, reusedA, err := rf.store.CreateOwnerApproval(ctx, rf.approvalCmd(t, "cross-turn-key-A", "call-turn-a", rf.budget))
	if err != nil {
		t.Fatalf("turn A CreateOwnerApproval: %v", err)
	}
	if reusedA {
		t.Error("turn A reported reused=true, want false (this is the first approval)")
	}

	turnB, reusedB, err := rf.store.CreateOwnerApproval(ctx, rf.approvalCmd(t, "cross-turn-key-B", "call-turn-b", rf.budget))
	if err != nil {
		t.Fatalf("turn B CreateOwnerApproval (different idempotency key, same tuple) unexpectedly returned an error instead of converging: %v", err)
	}
	if !reusedB {
		t.Error("turn B reported reused=false, want true (same tuple, different turn, must converge)")
	}
	if turnB.ID != turnA.ID {
		t.Errorf("turn B approval ID = %d, want %d (same tuple must converge to the same durable approval)", turnB.ID, turnA.ID)
	}

	var rowCount int
	if err := f.store.Pool().QueryRow(ctx, "SELECT count(*) FROM campaign_owner_approvals WHERE organization_id=$1 AND proposal_id=$2", chatTestOrganization, rf.proposalID).Scan(&rowCount); err != nil {
		t.Fatalf("count approvals: %v", err)
	}
	if rowCount != 1 {
		t.Errorf("total approval rows after cross-turn convergence = %d, want exactly 1", rowCount)
	}
}

// TestCreateOwnerApprovalDifferentTurnConflictDeniesOverwrite proves the
// round's DIFFERENT-TURN CONFLICT requirement: if the same tuple is
// already approved and a later attempt disagrees with that immutable
// approval (a different execution budget, here), it is denied --
// ErrApprovalConflict -- never silently overwritten, never a second
// approval.
func TestCreateOwnerApprovalDifferentTurnConflictDeniesOverwrite(t *testing.T) {
	f, rf := newOwnerApprovalReuseFixture(t)
	defer f.cleanup()
	ctx := context.Background()

	original, _, err := rf.store.CreateOwnerApproval(ctx, rf.approvalCmd(t, "conflict-key-A", "call-conflict-a", rf.budget))
	if err != nil {
		t.Fatalf("original CreateOwnerApproval: %v", err)
	}

	conflictingBudget := rf.budget
	conflictingBudget.MaxUSD = rf.budget.MaxUSD * 2
	_, _, err = rf.store.CreateOwnerApproval(ctx, rf.approvalCmd(t, "conflict-key-B", "call-conflict-b", conflictingBudget))
	if !errors.Is(err, campaign.ErrApprovalConflict) {
		t.Errorf("conflicting later attempt on the same tuple: err=%v, want ErrApprovalConflict", err)
	}

	unchanged, err := rf.store.GetOwnerApproval(ctx, chatTestOrganization, original.ID)
	if err != nil {
		t.Fatalf("read back original approval: %v", err)
	}
	if unchanged.CanonicalHash != original.CanonicalHash {
		t.Error("original approval's canonical hash changed -- it was overwritten")
	}
	var rowCount int
	if err := f.store.Pool().QueryRow(ctx, "SELECT count(*) FROM campaign_owner_approvals WHERE organization_id=$1 AND proposal_id=$2", chatTestOrganization, rf.proposalID).Scan(&rowCount); err != nil {
		t.Fatalf("count approvals: %v", err)
	}
	if rowCount != 1 {
		t.Errorf("total approval rows after denied conflict = %d, want exactly 1 (no second approval)", rowCount)
	}
}

// TestCreateOwnerApprovalConcurrentSameTupleIsRaceSafe is BLOCKER 2's
// concurrency requirement: two real callers racing to approve the exact
// same tuple with different idempotency identities must converge to
// exactly one row, one reused=false and the other reused=true, both
// returning the same approval ID, and no raw PostgreSQL 23505 ever
// escapes CreateOwnerApproval's own domain-level return values.
func TestCreateOwnerApprovalConcurrentSameTupleIsRaceSafe(t *testing.T) {
	f, rf := newOwnerApprovalReuseFixture(t)
	defer f.cleanup()
	ctx := context.Background()

	const callers = 2
	var wg sync.WaitGroup
	ids := make([]int64, callers)
	reusedFlags := make([]bool, callers)
	errs := make([]error, callers)
	wg.Add(callers)
	for i := 0; i < callers; i++ {
		go func(i int) {
			defer wg.Done()
			cmd := rf.approvalCmd(t, "concurrent-key", "call-concurrent", rf.budget)
			cmd.IdempotencyKey = cmd.IdempotencyKey + "-" + []string{"A", "B"}[i]
			cmd.ToolCallID = cmd.ToolCallID + "-" + []string{"A", "B"}[i]
			approval, reused, err := rf.store.CreateOwnerApproval(ctx, cmd)
			ids[i], reusedFlags[i], errs[i] = approval.ID, reused, err
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d: unexpected error (raw constraint violation must never escape as a bare error): %v", i, err)
		}
	}
	if ids[0] != ids[1] || ids[0] == 0 {
		t.Fatalf("callers returned different/zero approval IDs: %v, want both equal and non-zero", ids)
	}
	insertedCount, reusedCount := 0, 0
	for _, r := range reusedFlags {
		if r {
			reusedCount++
		} else {
			insertedCount++
		}
	}
	if insertedCount != 1 || reusedCount != 1 {
		t.Errorf("reused flags = %v, want exactly one false (inserted) and one true (reused)", reusedFlags)
	}

	var rowCount int
	if err := f.store.Pool().QueryRow(ctx, "SELECT count(*) FROM campaign_owner_approvals WHERE organization_id=$1 AND proposal_id=$2", chatTestOrganization, rf.proposalID).Scan(&rowCount); err != nil {
		t.Fatalf("count approvals: %v", err)
	}
	if rowCount != 1 {
		t.Errorf("total approval rows after concurrent race = %d, want exactly 1", rowCount)
	}
}

// TestApprovalServicePropagatesReusedFlag confirms
// ApprovalService.ApproveForExecution -- the layer campaign.approve_for_execution's
// own tool handler actually calls -- propagates the store's now-corrected
// reused boolean untouched: first conversational approval reused=false,
// repeat reused=true. Authorizer is nil (ApprovalService only authorizes
// when one is supplied) since this test is about reuse propagation, not
// authorization -- REVIEW 8's authorization behavior is covered elsewhere.
func TestApprovalServicePropagatesReusedFlag(t *testing.T) {
	f, rf := newOwnerApprovalReuseFixture(t)
	defer f.cleanup()
	ctx := context.Background()

	approvalSvc := campaign.NewApprovalService(rf.store, nil, permissiveExecutionRequirements())
	params := campaign.ApproveParams{
		OrganizationID: chatTestOrganization, ProposalID: rf.proposalID, FinancialReviewID: rf.reviewID,
		ApprovedByRoleID: "empresa/human",
		ConversationID:   rf.conversationID, MessageID: rf.messageID, TurnTaskID: rf.taskID,
		ToolCallID: "svc-call-1",
	}

	first, firstReused, err := approvalSvc.ApproveForExecution(ctx, params)
	if err != nil {
		t.Fatalf("first ApproveForExecution: %v", err)
	}
	if firstReused {
		t.Error("first ApproveForExecution reported reused=true, want false")
	}

	second, secondReused, err := approvalSvc.ApproveForExecution(ctx, params)
	if err != nil {
		t.Fatalf("repeat ApproveForExecution: %v", err)
	}
	if !secondReused {
		t.Error("repeat ApproveForExecution reported reused=false, want true")
	}
	if second.ID != first.ID {
		t.Errorf("repeat approval ID = %d, want %d", second.ID, first.ID)
	}
}
