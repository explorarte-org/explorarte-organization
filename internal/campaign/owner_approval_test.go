package campaign_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/campaign"
	"github.com/Mireuz13/explorarte-organization/internal/executive"
)

// Frontera 3: an owner approval is an act of owner authority. These tests run the
// real ApprovalService (every rule) against the in-memory store, behind the real
// OwnerApprover, and prove the approval is made only by the owner path.

func approvalOwnerGrants() fakeAuthorizer {
	return fakeAuthorizer{grants: map[string]bool{executive.OwnerRoleID + ":" + campaign.CapabilityOwnerApprovalCreate: true}}
}

type approvalRig struct {
	store    *memCampaignStore
	svc      *campaign.ApprovalService
	approver *campaign.OwnerApprover
	audits   []campaign.OwnerApprovalAudit
}

func newApprovalRig(t *testing.T, owner fixedOwner, auth fakeAuthorizer, requirements campaign.ExecutionRequirementsProvider) *approvalRig {
	t.Helper()
	rig := &approvalRig{store: newMemCampaignStore()}
	rig.svc = campaign.NewApprovalService(rig.store, auth, requirements)
	approver, err := campaign.NewOwnerApprover("org-1", rig.store, rig.svc, owner, auth, func(a campaign.OwnerApprovalAudit) { rig.audits = append(rig.audits, a) })
	if err != nil {
		t.Fatal(err)
	}
	rig.approver = approver
	return rig
}

func (r *approvalRig) proposal(t *testing.T, key string) campaign.CampaignProposal {
	t.Helper()
	payload := campaign.CanonicalPayload{Title: "Campaign " + key, Goal: "Goal of " + key}
	hash, err := campaign.ComputeCanonicalHash(payload)
	if err != nil {
		t.Fatal(err)
	}
	p, _, err := r.store.CreateProposal(context.Background(), campaign.CreateProposalCommand{
		OrganizationID: "org-1", CreatedByRoleID: "empresa/ceo", Title: payload.Title, Goal: payload.Goal, IdempotencyKey: key, CanonicalHash: hash,
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func (r *approvalRig) review(t *testing.T, p campaign.CampaignProposal, reviewer string, requestID int64, verdict campaign.FinancialReviewVerdict, budget campaign.BudgetRecommendation) campaign.CampaignFinancialReview {
	t.Helper()
	payload := campaign.ReviewCanonicalPayload{ProposalID: p.ID, ProposalCanonicalHash: p.CanonicalHash, ReviewerRoleID: reviewer, Verdict: verdict, RecommendedBudget: &budget, Summary: "s"}
	hash, err := campaign.ComputeReviewCanonicalHash(payload)
	if err != nil {
		t.Fatal(err)
	}
	rev, _, err := r.store.RecordFinancialReview(context.Background(), campaign.RecordFinancialReviewCommand{
		OrganizationID: "org-1", ReviewRequestID: requestID, ProposalID: p.ID, ProposalCanonicalHash: p.CanonicalHash,
		ReviewerRoleID: reviewer, Verdict: verdict, RecommendedBudget: &budget, Summary: "s", CanonicalHash: hash,
	})
	if err != nil {
		t.Fatal(err)
	}
	return rev
}

func (r *approvalRig) revision(t *testing.T, parent campaign.CampaignProposal, key string) campaign.CampaignProposal {
	t.Helper()
	payload := campaign.CanonicalPayload{Title: "Revised " + key, Goal: "Revised goal " + key}
	hash, _ := campaign.ComputeCanonicalHash(payload)
	v2, _, err := r.store.CreateRevision(context.Background(), campaign.CreateRevisionCommand{
		OrganizationID: "org-1", ParentProposalID: parent.ID, CreatedByRoleID: "empresa/ceo",
		Title: payload.Title, Goal: payload.Goal, IdempotencyKey: key, CanonicalHash: hash,
	})
	if err != nil {
		t.Fatal(err)
	}
	return v2
}

func (r *approvalRig) approvals() int { return len(r.store.approvals) }

func (r *approvalRig) lastAudit(t *testing.T) campaign.OwnerApprovalAudit {
	t.Helper()
	if len(r.audits) == 0 {
		t.Fatal("no audit event was emitted")
	}
	return r.audits[len(r.audits)-1]
}

func standardApprovalRig(t *testing.T) (*approvalRig, campaign.CampaignProposal, campaign.CampaignFinancialReview) {
	t.Helper()
	rig := newApprovalRig(t, fixedOwner{identity: canonicalOwner()}, approvalOwnerGrants(), fixed(floor()))
	p := rig.proposal(t, "p1")
	return rig, p, rig.review(t, p, "empresa/finanzas", 101, campaign.VerdictRecommended, feasibleBudget())
}

// The owner path creates the approval, from canonical identity and the review's
// own budget, and leaves durable provenance that claims no conversation.
func TestOwnerApproverCreatesTheApprovalAsTheCanonicalOwner(t *testing.T) {
	rig, p, r := standardApprovalRig(t)
	result, err := rig.approver.Approve(context.Background(), p.ID, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Reused || result.ActorRoleID != executive.OwnerRoleID || result.ProposalID != p.ID || result.FinancialReviewID != r.ID ||
		result.ProposalCanonicalHash != p.CanonicalHash || result.FinancialReviewCanonicalHash != r.CanonicalHash ||
		result.ExecutionBudget != feasibleBudget() {
		t.Fatalf("result = %+v", result)
	}
	stored, err := rig.store.GetOwnerApproval(context.Background(), "org-1", result.ApprovalID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ApprovedByRoleID != executive.OwnerRoleID || stored.ToolCallID != campaign.OwnerApprovalToolCallID(p.ID, r.ID) ||
		stored.ConversationID != 0 || stored.MessageID != 0 || stored.TurnTaskID != 0 || stored.ExecutionBudget != feasibleBudget() {
		t.Fatalf("stored approval = %+v", stored)
	}
	a := rig.lastAudit(t)
	if len(rig.audits) != 1 || a.Outcome != "approved" || a.ErrorClass != "" || a.ActorRoleID != executive.OwnerRoleID || a.ActorAuthorityClass != "owner" ||
		a.ProposalID != p.ID || a.FinancialReviewID != r.ID || a.ProposalCanonicalHash != p.CanonicalHash || a.FinancialReviewCanonicalHash != r.CanonicalHash ||
		a.ApprovalID != result.ApprovalID {
		t.Fatalf("audit = %+v", a)
	}
}

// Nothing supplied by the caller can name who acts, and no owner means no approval.
func TestOwnerApproverNeedsACanonicalOwnerAndTheCapability(t *testing.T) {
	ctx := context.Background()
	t.Run("owner identity unavailable", func(t *testing.T) {
		rig := newApprovalRig(t, fixedOwner{err: campaign.ErrOwnerIdentityUnavailable}, approvalOwnerGrants(), fixed(floor()))
		p := rig.proposal(t, "p1")
		r := rig.review(t, p, "empresa/finanzas", 101, campaign.VerdictRecommended, feasibleBudget())
		if _, err := rig.approver.Approve(ctx, p.ID, r.ID); !errors.Is(err, campaign.ErrOwnerIdentityUnavailable) {
			t.Fatalf("err = %v", err)
		}
		if rig.approvals() != 0 || rig.lastAudit(t).ErrorClass != "owner_identity_unavailable" {
			t.Fatalf("approvals %d, audit %+v", rig.approvals(), rig.lastAudit(t))
		}
	})
	t.Run("missing owner capability", func(t *testing.T) {
		rig := newApprovalRig(t, fixedOwner{identity: canonicalOwner()}, fakeAuthorizer{}, fixed(floor()))
		p := rig.proposal(t, "p1")
		r := rig.review(t, p, "empresa/finanzas", 101, campaign.VerdictRecommended, feasibleBudget())
		if _, err := rig.approver.Approve(ctx, p.ID, r.ID); !errors.Is(err, campaign.ErrUnauthorized) {
			t.Fatalf("err = %v, want ErrUnauthorized", err)
		}
		if rig.approvals() != 0 || rig.lastAudit(t).ErrorClass != "unauthorized" {
			t.Fatalf("approvals %d, audit %+v", rig.approvals(), rig.lastAudit(t))
		}
	})
}

// Every way the pair can be wrong ends with no approval.
func TestOwnerApproverRefusesAPairTheOwnerCannotApprove(t *testing.T) {
	ctx := context.Background()
	cases := map[string]func(t *testing.T) (*approvalRig, int64, int64, error){
		"unknown proposal": func(t *testing.T) (*approvalRig, int64, int64, error) {
			rig, _, r := standardApprovalRig(t)
			return rig, 9999, r.ID, campaign.ErrProposalNotFound
		},
		"unknown review": func(t *testing.T) (*approvalRig, int64, int64, error) {
			rig, p, _ := standardApprovalRig(t)
			return rig, p.ID, 9999, campaign.ErrFinancialReviewNotFound
		},
		"review of another proposal": func(t *testing.T) (*approvalRig, int64, int64, error) {
			rig, p, _ := standardApprovalRig(t)
			other := rig.proposal(t, "p2")
			otherReview := rig.review(t, other, "empresa/finanzas", 102, campaign.VerdictRecommended, feasibleBudget())
			return rig, p.ID, otherReview.ID, campaign.ErrProposalHashMismatch
		},
		"review not recommended": func(t *testing.T) (*approvalRig, int64, int64, error) {
			rig := newApprovalRig(t, fixedOwner{identity: canonicalOwner()}, approvalOwnerGrants(), fixed(floor()))
			p := rig.proposal(t, "p1")
			r := rig.review(t, p, "empresa/finanzas", 101, campaign.VerdictChangesRequested, feasibleBudget())
			return rig, p.ID, r.ID, campaign.ErrReviewNotRecommended
		},
		"proposal superseded by a newer revision": func(t *testing.T) (*approvalRig, int64, int64, error) {
			rig, p, r := standardApprovalRig(t)
			rig.revision(t, p, "rev2")
			return rig, p.ID, r.ID, campaign.ErrStaleApproval
		},
		"review superseded by a newer review": func(t *testing.T) (*approvalRig, int64, int64, error) {
			rig, p, r := standardApprovalRig(t)
			rig.review(t, p, "empresa/finanzas", 102, campaign.VerdictRecommended, feasibleBudget())
			return rig, p.ID, r.ID, campaign.ErrStaleFinancialReview
		},
		"budget below the current floor": func(t *testing.T) (*approvalRig, int64, int64, error) {
			rig := newApprovalRig(t, fixedOwner{identity: canonicalOwner()}, approvalOwnerGrants(), fixed(floor()))
			p := rig.proposal(t, "p1")
			r := rig.review(t, p, "empresa/finanzas", 101, campaign.VerdictRecommended, productionBudget())
			return rig, p.ID, r.ID, campaign.ErrInfeasibleExecutionBudget
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			rig, proposalID, reviewID, want := setup(t)
			if _, err := rig.approver.Approve(ctx, proposalID, reviewID); !errors.Is(err, want) {
				t.Fatalf("err = %v, want %v", err, want)
			}
			if rig.approvals() != 0 {
				t.Fatalf("%d approval(s) created for a pair the owner cannot approve", rig.approvals())
			}
			if a := rig.lastAudit(t); a.Outcome != "failed" || a.ErrorClass == "" {
				t.Fatalf("audit = %+v", a)
			}
		})
	}
}

// The owner approves what they were shown: if the content read when the grant
// was issued is not what the service reads, nothing is approved.
type driftingReader struct {
	campaign.Store
	proposalHash string
}

func (d driftingReader) GetProposal(ctx context.Context, org string, id int64) (campaign.CampaignProposal, error) {
	p, err := d.Store.GetProposal(ctx, org, id)
	p.CanonicalHash = d.proposalHash
	return p, err
}

func TestOwnerApproverRefusesContentThatChangedAfterTheGrant(t *testing.T) {
	rig, p, r := standardApprovalRig(t)
	drift := driftingReader{Store: rig.store, proposalHash: "ab" + p.CanonicalHash[2:]}
	approver, err := campaign.NewOwnerApprover("org-1", drift, rig.svc, fixedOwner{identity: canonicalOwner()}, approvalOwnerGrants(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := approver.Approve(context.Background(), p.ID, r.ID); !errors.Is(err, campaign.ErrOwnerApprovalGrantMismatch) {
		t.Fatalf("err = %v, want ErrOwnerApprovalGrantMismatch", err)
	}
	if rig.approvals() != 0 {
		t.Fatal("an approval was created for content the owner was not shown")
	}
}

// The same pair converges on one approval; another pair never adopts it.
func TestOwnerApproverRetryReusesAndAnotherPairIsNotAdopted(t *testing.T) {
	ctx := context.Background()
	rig, p, r := standardApprovalRig(t)
	first, err := rig.approver.Approve(ctx, p.ID, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	again, err := rig.approver.Approve(ctx, p.ID, r.ID)
	if err != nil || !again.Reused || again.ApprovalID != first.ApprovalID {
		t.Fatalf("retry = %+v, %v", again, err)
	}
	if rig.approvals() != 1 || rig.audits[1].Outcome != "reused" {
		t.Fatalf("approvals %d, audits %+v", rig.approvals(), rig.audits)
	}
	other := rig.proposal(t, "p2")
	otherReview := rig.review(t, other, "empresa/finanzas", 102, campaign.VerdictRecommended, feasibleBudget())
	second, err := rig.approver.Approve(ctx, other.ID, otherReview.ID)
	if err != nil || second.Reused || second.ApprovalID == first.ApprovalID || second.ProposalID != other.ID {
		t.Fatalf("second pair = %+v, %v; it must be its own approval, not the first one adopted", second, err)
	}
	if rig.approvals() != 2 {
		t.Fatalf("approvals = %d, want 2", rig.approvals())
	}
}

// The service refuses to approve without a grant, and a grant cannot be forged.
func TestOwnerApprovalCannotBeMintedWithoutTheOwnerApprover(t *testing.T) {
	typ := reflect.TypeOf(campaign.OwnerApprovalGrant{})
	for i := 0; i < typ.NumField(); i++ {
		if typ.Field(i).IsExported() {
			t.Fatalf("OwnerApprovalGrant.%s is exported: any package could mint a grant", typ.Field(i).Name)
		}
	}
	rig, p, r := standardApprovalRig(t)
	_, _, err := rig.svc.ApproveForExecution(context.Background(), campaign.ApproveParams{
		OrganizationID: "org-1", ProposalID: p.ID, FinancialReviewID: r.ID, ApprovedByRoleID: executive.OwnerRoleID, ToolCallID: "call",
	}, campaign.OwnerApprovalGrant{})
	if !errors.Is(err, campaign.ErrOwnerApprovalNotAuthorized) {
		t.Fatalf("zero grant: err = %v, want ErrOwnerApprovalNotAuthorized", err)
	}
	if rig.approvals() != 0 {
		t.Fatal("an approval was created without a grant")
	}
	if got := campaign.OwnerApprovalErrorClass(err); got != "unauthorized" {
		t.Fatalf("class = %q", got)
	}
}

func TestOwnerApprovalErrorClasses(t *testing.T) {
	for err, want := range map[error]string{
		campaign.ErrOwnerIdentityUnavailable:    "owner_identity_unavailable",
		campaign.ErrUnauthorized:                "unauthorized",
		campaign.ErrProposalNotFound:            "proposal_not_found",
		campaign.ErrFinancialReviewNotFound:     "financial_review_not_found",
		campaign.ErrProposalHashMismatch:        "tuple_mismatch",
		campaign.ErrOwnerApprovalGrantMismatch:  "content_changed",
		campaign.ErrReviewNotRecommended:        "review_not_recommended",
		campaign.ErrStaleApproval:               "stale",
		campaign.ErrStaleFinancialReview:        "stale",
		campaign.ErrSeparationOfDutiesViolation: "separation_of_duties",
		campaign.ErrApprovalConflict:            "approval_conflict",
		campaign.ErrInfeasibleExecutionBudget:   "infeasible_execution_budget",
	} {
		if got := campaign.OwnerApprovalErrorClass(errors.Join(errors.New("ctx"), err)); got != want {
			t.Errorf("class(%v) = %q, want %q", err, got, want)
		}
	}
	if campaign.OwnerApprovalErrorClass(nil) != "" || campaign.OwnerApprovalErrorClass(errors.New("boom")) != "internal" {
		t.Error("nil must be empty and an unknown error internal")
	}
}

// The approver's own capability check is not decoration: with an approval service
// that has no authorizer at all, a denied owner still approves nothing.
func TestOwnerApproverChecksTheCapabilityItself(t *testing.T) {
	rig := newApprovalRig(t, fixedOwner{identity: canonicalOwner()}, approvalOwnerGrants(), fixed(floor()))
	p := rig.proposal(t, "p1")
	r := rig.review(t, p, "empresa/finanzas", 101, campaign.VerdictRecommended, feasibleBudget())
	noAuthorizerSvc := campaign.NewApprovalService(rig.store, nil, fixed(floor()))
	approver, err := campaign.NewOwnerApprover("org-1", rig.store, noAuthorizerSvc, fixedOwner{identity: canonicalOwner()}, fakeAuthorizer{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := approver.Approve(context.Background(), p.ID, r.ID); !errors.Is(err, campaign.ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
	if rig.approvals() != 0 {
		t.Fatal("the approver skipped its own capability check")
	}
}

type capturingExecutor struct {
	params campaign.ApproveParams
	grant  campaign.OwnerApprovalGrant
}

func (c *capturingExecutor) ApproveForExecution(_ context.Context, params campaign.ApproveParams, grant campaign.OwnerApprovalGrant) (campaign.CampaignOwnerApproval, bool, error) {
	c.params, c.grant = params, grant
	return campaign.CampaignOwnerApproval{}, false, nil
}

// A grant is scoped to one proposal, one review and one owner: what the approver
// issued for one approval authorizes no other.
func TestOwnerApprovalGrantIsScopedToItsProposalReviewAndOwner(t *testing.T) {
	rig, p, r := standardApprovalRig(t)
	captured := &capturingExecutor{}
	approver, err := campaign.NewOwnerApprover("org-1", rig.store, captured, fixedOwner{identity: canonicalOwner()}, approvalOwnerGrants(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := approver.Approve(context.Background(), p.ID, r.ID); err != nil {
		t.Fatal(err)
	}
	if !captured.grant.Chosen() {
		t.Fatal("the approver passed no grant")
	}
	other := rig.proposal(t, "p2")
	otherReview := rig.review(t, other, "empresa/finanzas", 102, campaign.VerdictRecommended, feasibleBudget())
	for name, params := range map[string]campaign.ApproveParams{
		"another proposal": {ProposalID: other.ID, FinancialReviewID: r.ID, ApprovedByRoleID: executive.OwnerRoleID},
		"another review":   {ProposalID: p.ID, FinancialReviewID: otherReview.ID, ApprovedByRoleID: executive.OwnerRoleID},
		"another approver": {ProposalID: p.ID, FinancialReviewID: r.ID, ApprovedByRoleID: executive.CEORoleID},
	} {
		params.OrganizationID, params.ToolCallID = "org-1", "call"
		if _, _, err := rig.svc.ApproveForExecution(context.Background(), params, captured.grant); !errors.Is(err, campaign.ErrOwnerApprovalNotAuthorized) {
			t.Errorf("%s: err = %v, want ErrOwnerApprovalNotAuthorized", name, err)
		}
	}
	if rig.approvals() != 0 {
		t.Fatalf("approvals = %d", rig.approvals())
	}
}
