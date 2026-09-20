package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/campaign"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

// Authority is not a parameter of this command. Every attempt to name who is
// acting is refused as a usage error BEFORE any runtime or database is opened
// (these tests have neither), so there is nothing to inject.
func TestCampaignPromoteAcceptsNoActorFlags(t *testing.T) {
	for _, args := range [][]string{
		{"--approval", "4", "--actor-role", "empresa/human"},
		{"--approval", "4", "--owner-role", "empresa/human"},
		{"--approval=4", "--role", "empresa/human"},
		{"--approval", "4", "--capability", "campaign.promotion.execute"},
		{"--approval", "4", "--principal", "1"},
		{"--approval", "4", "extra-positional"},
		{"--approval", "0"},
		{"--approval", "-3"},
		{},
	} {
		var stdout, stderr bytes.Buffer
		if code := runCampaignPromote(args, &stdout, &stderr); code != exitUsage {
			t.Errorf("runCampaignPromote(%v) = %d, want exitUsage %d (stderr=%q)", args, code, exitUsage, stderr.String())
		}
	}
}

func TestCampaignCommandRouting(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runCampaign(nil, &stdout, &stderr); code != exitUsage {
		t.Fatalf("no subcommand = %d, want usage", code)
	}
	if code := runCampaign([]string{"nope"}, &stdout, &stderr); code != exitUsage {
		t.Fatalf("unknown subcommand = %d, want usage", code)
	}
	stdout.Reset()
	if code := runCampaign([]string{"help"}, &stdout, &stderr); code != exitOK || !strings.Contains(stdout.String(), "deliberately no --actor-role") {
		t.Fatalf("help = %d %q; it must state that identity cannot be supplied", code, stdout.String())
	}
}

type fakePromoter struct {
	result campaign.OwnerPromotionResult
	err    error
	called int
	// withMode records the modes passed to PromoteWithMode.
	withMode []campaign.ExecutionMode
}

func (f *fakePromoter) PromoteWithMode(_ context.Context, _ int64, mode campaign.ExecutionMode) (campaign.OwnerPromotionResult, error) {
	f.called++
	f.withMode = append(f.withMode, mode)
	return f.result, f.err
}

func (f *fakePromoter) Promote(context.Context, int64) (campaign.OwnerPromotionResult, error) {
	f.called++
	return f.result, f.err
}

func TestOwnerPromotionExitCodesFollowTheErrorClass(t *testing.T) {
	cases := map[error]int{
		campaign.ErrUnauthorized:                     exitDenied,
		campaign.ErrOwnerIdentityUnavailable:         exitDenied,
		campaign.ErrApprovalNotApproved:              exitApprovalRequired,
		campaign.ErrApprovalNotFound:                 exitInvalid,
		campaign.ErrInfeasibleExecutionBudget:        exitInvalid,
		campaign.ErrInvalidExecutionBudget:           exitInvalid,
		campaign.ErrInvalidInput:                     exitInvalid,
		tasks.ErrIdempotencyConflict:                 exitInvalid,
		campaign.ErrExecutionRequirementsUnavailable: exitInternal,
		errors.New("unexpected"):                     exitInternal,
	}
	for err, want := range cases {
		var stdout, stderr bytes.Buffer
		promoter := &fakePromoter{err: fmt.Errorf("executive submit: %w", err)}
		if got := executeOwnerPromotion(context.Background(), promoter, 4, "", false, false, &stdout, &stderr); got != want {
			t.Errorf("exit code for %v = %d, want %d", err, got, want)
		}
		if !strings.Contains(stderr.String(), campaign.OwnerPromotionErrorClass(err)) || stdout.Len() != 0 {
			t.Errorf("failure for %v must name its error class on stderr and print nothing on stdout: stderr=%q stdout=%q", err, stderr.String(), stdout.String())
		}
	}
}

func TestOwnerPromotionOutputCarriesTheAuditedFields(t *testing.T) {
	promoter := &fakePromoter{result: campaign.OwnerPromotionResult{
		ApprovalID: 4, ActorRoleID: "empresa/human", ActorAuthorityClass: "owner", OrganizationRevisionID: 5,
		PromotionID: 3, ExecutiveRootTaskID: 830, ExecutiveCorrelationID: "executive:x",
		ExecutiveSubmitIdempotencyKey: "campaign-promotion:4:0123456789abcdef", TrustedRootCausation: "owner:campaign-promotion-4-0123456789abcdef",
		PromotionIdempotencyKey: "campaign-promotion:explorarte:4",
	}}
	var stdout, stderr bytes.Buffer
	if code := executeOwnerPromotion(context.Background(), promoter, 4, "", false, true, &stdout, &stderr); code != exitOK {
		t.Fatalf("exit = %d (%s)", code, stderr.String())
	}
	var decoded map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"approval_id", "actor_role_id", "promotion_id", "executive_root_task_id", "executive_submit_idempotency_key", "trusted_root_causation", "reused"} {
		if _, ok := decoded[key]; !ok {
			t.Errorf("JSON output lacks %q: %s", key, stdout.String())
		}
	}
	var text bytes.Buffer
	if code := executeOwnerPromotion(context.Background(), promoter, 4, "", false, false, &text, &stderr); code != exitOK ||
		!strings.Contains(text.String(), "campaign-promotion:4:0123456789abcdef") || !strings.Contains(text.String(), "owner:campaign-promotion-4-0123456789abcdef") {
		t.Fatalf("text output = %q", text.String())
	}
}

func TestOwnerPromotionAuditLineIsStructured(t *testing.T) {
	var out bytes.Buffer
	emitOwnerPromotionAudit(&out, campaign.OwnerPromotionAudit{ApprovalID: 4, ActorRoleID: "empresa/human", Outcome: "failed", ErrorClass: "unauthorized", Error: "no"})
	var line map[string]any
	if err := json.Unmarshal(out.Bytes(), &line); err != nil {
		t.Fatalf("the audit line must be one JSON object: %v (%q)", err, out.String())
	}
	if line["event"] != "campaign.owner_promotion" || line["error_class"] != "unauthorized" || line["actor_role_id"] != "empresa/human" || line["level"] != "WARN" {
		t.Fatalf("audit line = %v", line)
	}
}

// The mode reaches the promoter only when the owner passed the flag: omitting it
// calls the mode-less path, so absence can never opt in.
func TestExecuteOwnerPromotionRoutesTheModeOnlyWhenChosen(t *testing.T) {
	var stdout, stderr bytes.Buffer
	none := &fakePromoter{}
	if code := executeOwnerPromotion(context.Background(), none, 4, "", false, false, &stdout, &stderr); code != exitOK {
		t.Fatalf("no mode = %d (%s)", code, stderr.String())
	}
	if len(none.withMode) != 0 {
		t.Fatalf("PromoteWithMode called %v without --execution-mode", none.withMode)
	}
	chosen := &fakePromoter{result: campaign.OwnerPromotionResult{ExecutionMode: campaign.ExecutionModeGovernedImplementation}}
	stdout.Reset()
	if code := executeOwnerPromotion(context.Background(), chosen, 4, campaign.ExecutionModeGovernedImplementation, true, false, &stdout, &stderr); code != exitOK {
		t.Fatalf("chosen mode = %d (%s)", code, stderr.String())
	}
	if len(chosen.withMode) != 1 || chosen.withMode[0] != campaign.ExecutionModeGovernedImplementation {
		t.Fatalf("PromoteWithMode calls = %v, want one governed_implementation", chosen.withMode)
	}
	if !strings.Contains(stdout.String(), "execution mode:  governed_implementation") {
		t.Fatalf("output must state the mode the campaign runs under: %q", stdout.String())
	}
}

// A mode that is not one of the two known values is a usage error before any
// runtime or database is opened (these tests have neither).
func TestCampaignPromoteRejectsUnknownExecutionMode(t *testing.T) {
	for _, mode := range []string{"governed", "GOVERNED_IMPLEMENTATION", "design-freeze", "code_runner_execution_evidence", "analysis_only,governed_implementation"} {
		var stdout, stderr bytes.Buffer
		if code := runCampaignPromote([]string{"--approval", "4", "--execution-mode", mode}, &stdout, &stderr); code != exitUsage {
			t.Errorf("--execution-mode %q = %d, want exitUsage (stderr=%q)", mode, code, stderr.String())
		}
	}
}

func TestOwnerPromotionModeConflictIsAnInvalidRequestNotAnInternalError(t *testing.T) {
	err := fmt.Errorf("promote: %w", campaign.ErrExecutionModeConflict)
	if got := campaign.OwnerPromotionErrorClass(err); got != "execution_mode_conflict" {
		t.Fatalf("class = %q", got)
	}
	if got := ownerPromotionExitCode(err); got != exitInvalid {
		t.Fatalf("exit = %d, want exitInvalid %d", got, exitInvalid)
	}
}

// Approving is the owner's act and the CLI is only an adapter: no flag can name
// who approves, what it costs or which content is meant.
func TestCampaignApproveAcceptsNoAuthorityFlags(t *testing.T) {
	for _, args := range [][]string{
		{"--proposal", "1", "--review", "2", "--owner", "empresa/human"},
		{"--proposal", "1", "--review", "2", "--role", "empresa/human"},
		{"--proposal", "1", "--review", "2", "--actor-role", "empresa/human"},
		{"--proposal", "1", "--review", "2", "--budget", "9"},
		{"--proposal", "1", "--review", "2", "--proposal-hash", "aa"},
		{"--proposal", "1", "--review", "2", "extra-positional"},
		{"--proposal", "1"},
		{"--review", "2"},
		{"--proposal", "0", "--review", "2"},
		{"--proposal", "1", "--review", "-2"},
		{},
	} {
		var stdout, stderr bytes.Buffer
		if code := runCampaignApprove(args, &stdout, &stderr); code != exitUsage {
			t.Errorf("runCampaignApprove(%v) = %d, want exitUsage %d (stderr=%q)", args, code, exitUsage, stderr.String())
		}
	}
}

type fakeApprover struct {
	result campaign.OwnerApprovalResult
	err    error
	ids    [][2]int64
}

func (f *fakeApprover) Approve(_ context.Context, proposalID, reviewID int64) (campaign.OwnerApprovalResult, error) {
	f.ids = append(f.ids, [2]int64{proposalID, reviewID})
	return f.result, f.err
}

func TestExecuteOwnerApprovalReportsWhatWasApprovedAndTheNextStep(t *testing.T) {
	approver := &fakeApprover{result: campaign.OwnerApprovalResult{ApprovalID: 7, ActorRoleID: "empresa/human", ProposalID: 11, FinancialReviewID: 9,
		ExecutionBudget: campaign.BudgetRecommendation{MaxUSD: 3.5, MaxTokens: 1000, MaxModelCalls: 5, MaxDepth: 3, MaxSubagents: 5, MaxRetries: 1, MaxWallTimeMS: 60000}}}
	var stdout, stderr bytes.Buffer
	if code := executeOwnerApproval(context.Background(), approver, 11, 9, false, &stdout, &stderr); code != exitOK {
		t.Fatalf("exit %d (%s)", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"proposal 11 + review 9 approved by empresa/human: approval 7", "orgctl campaign promote --approval 7", "$3.500000"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output %q lacks %q", out, want)
		}
	}
	if len(approver.ids) != 1 || approver.ids[0] != [2]int64{11, 9} {
		t.Fatalf("approver called with %v", approver.ids)
	}
	stdout.Reset()
	approver.result.Reused = true
	if code := executeOwnerApproval(context.Background(), approver, 11, 9, true, &stdout, &stderr); code != exitOK || !strings.Contains(stdout.String(), `"reused": true`) {
		t.Fatalf("json output = %q", stdout.String())
	}
}

func TestOwnerApprovalExitCodes(t *testing.T) {
	for err, want := range map[error]int{
		campaign.ErrUnauthorized:               exitDenied,
		campaign.ErrOwnerIdentityUnavailable:   exitDenied,
		campaign.ErrProposalHashMismatch:       exitInvalid,
		campaign.ErrStaleApproval:              exitInvalid,
		campaign.ErrStaleFinancialReview:       exitInvalid,
		campaign.ErrOwnerApprovalGrantMismatch: exitInvalid,
		campaign.ErrInfeasibleExecutionBudget:  exitInvalid,
		campaign.ErrReviewNotRecommended:       exitInvalid,
		errors.New("boom"):                     exitInternal,
	} {
		if got := ownerApprovalExitCode(fmt.Errorf("approve: %w", err)); got != want {
			t.Errorf("exit(%v) = %d, want %d", err, got, want)
		}
	}
	var stdout, stderr bytes.Buffer
	if code := executeOwnerApproval(context.Background(), &fakeApprover{err: campaign.ErrStaleApproval}, 1, 2, false, &stdout, &stderr); code != exitInvalid ||
		!strings.Contains(stderr.String(), "stale") {
		t.Fatalf("stale approval = %d %q", code, stderr.String())
	}
}
