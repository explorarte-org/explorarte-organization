package campaign

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

// OwnerApprovalGrant is the proof that the owner approver -- and only it --
// resolved the canonical owner and bound one exact proposal and one exact
// financial review for approval.
//
// Its fields are unexported, so no other package can construct a non-zero grant:
// not a CEO tool, not a model-produced argument, not a CLI flag handler. The zero
// value grants nothing. A channel that is not the CLI (a confirmation button, a
// desktop app) reaches the same ApprovalService by going through OwnerApprover,
// so the contract of authority does not change with the channel.
type OwnerApprovalGrant struct {
	proposalID   int64
	proposalHash string
	reviewID     int64
	reviewHash   string
	ownerRoleID  string
}

func newOwnerApprovalGrant(proposal CampaignProposal, review CampaignFinancialReview, ownerRoleID string) (OwnerApprovalGrant, error) {
	if proposal.ID <= 0 || review.ID <= 0 || proposal.CanonicalHash == "" || review.CanonicalHash == "" || strings.TrimSpace(ownerRoleID) == "" {
		return OwnerApprovalGrant{}, fmt.Errorf("%w: a grant names a proposal, a review, both hashes and an owner", ErrInvalidInput)
	}
	return OwnerApprovalGrant{
		proposalID: proposal.ID, proposalHash: proposal.CanonicalHash,
		reviewID: review.ID, reviewHash: review.CanonicalHash, ownerRoleID: ownerRoleID,
	}, nil
}

// Chosen reports whether this is a real grant (the zero value is not).
func (g OwnerApprovalGrant) Chosen() bool { return g.proposalID != 0 }

// authorizes reports whether the grant was issued for exactly this approval request.
func (g OwnerApprovalGrant) authorizes(params ApproveParams) bool {
	return g.Chosen() && g.proposalID == params.ProposalID && g.reviewID == params.FinancialReviewID &&
		g.ownerRoleID == params.ApprovedByRoleID
}

// matches reports whether the content read now is the content the grant was
// issued for.
func (g OwnerApprovalGrant) matches(proposal CampaignProposal, review CampaignFinancialReview) bool {
	return g.proposalID == proposal.ID && g.proposalHash == proposal.CanonicalHash &&
		g.reviewID == review.ID && g.reviewHash == review.CanonicalHash
}

// ApprovalInputReader is the pair of reads OwnerApprover needs to bind a grant.
type ApprovalInputReader interface {
	GetProposal(ctx context.Context, organizationID string, proposalID int64) (CampaignProposal, error)
	GetFinancialReview(ctx context.Context, organizationID string, reviewID int64) (CampaignFinancialReview, error)
}

// ApprovalExecutor is implemented by *ApprovalService.
type ApprovalExecutor interface {
	ApproveForExecution(ctx context.Context, params ApproveParams, grant OwnerApprovalGrant) (CampaignOwnerApproval, bool, error)
}

// OwnerApprovalResult is what an approval (new or already durable) reports.
type OwnerApprovalResult struct {
	ApprovalID                   int64                `json:"approval_id"`
	ActorRoleID                  string               `json:"actor_role_id"`
	ActorAuthorityClass          string               `json:"actor_authority_class"`
	OrganizationRevisionID       int64                `json:"organization_revision_id"`
	ProposalID                   int64                `json:"proposal_id"`
	ProposalCanonicalHash        string               `json:"proposal_canonical_hash"`
	FinancialReviewID            int64                `json:"financial_review_id"`
	FinancialReviewCanonicalHash string               `json:"financial_review_canonical_hash"`
	ExecutionBudget              BudgetRecommendation `json:"execution_budget"`
	ApprovalCanonicalHash        string               `json:"approval_canonical_hash"`
	ToolCallID                   string               `json:"tool_call_id"`
	Reused                       bool                 `json:"reused"`
}

// OwnerApprovalAudit is emitted exactly once per Approve call, success or not.
type OwnerApprovalAudit struct {
	ProposalID                   int64
	FinancialReviewID            int64
	ProposalCanonicalHash        string
	FinancialReviewCanonicalHash string
	ActorRoleID                  string
	ActorAuthorityClass          string
	OrganizationRevisionID       int64
	Outcome                      string // "approved", "reused" or "failed"
	ErrorClass                   string // empty on success
	Error                        string
	ApprovalID                   int64
}

// OwnerApprover is the deterministic owner approval adapter: it resolves who is
// acting from canonical state, checks the approval capability, binds the exact
// proposal and review it read into a grant, and hands that to ApprovalService.
// No language model is anywhere in the path, and no approval rule lives here.
type OwnerApprover struct {
	organizationID string
	inputs         ApprovalInputReader
	approvals      ApprovalExecutor
	owner          OwnerIdentityResolver
	authorizer     CapabilityAuthorizer
	audit          func(OwnerApprovalAudit)
}

// NewOwnerApprover wires the adapter. Every dependency is required, including the
// authorizer: a missing authorizer must never mean "skip the check".
func NewOwnerApprover(organizationID string, inputs ApprovalInputReader, approvals ApprovalExecutor, owner OwnerIdentityResolver, authorizer CapabilityAuthorizer, audit func(OwnerApprovalAudit)) (*OwnerApprover, error) {
	if strings.TrimSpace(organizationID) == "" {
		return nil, fmt.Errorf("%w: organization id is required", ErrInvalidInput)
	}
	if inputs == nil || approvals == nil || owner == nil || authorizer == nil {
		return nil, errors.New("owner approver requires an input reader, an approval service, an owner resolver and an authorizer")
	}
	if audit == nil {
		audit = func(OwnerApprovalAudit) {}
	}
	return &OwnerApprover{organizationID: organizationID, inputs: inputs, approvals: approvals, owner: owner, authorizer: authorizer, audit: audit}, nil
}

// OwnerApprovalErrorClass names a failure for audit and exit-code mapping.
func OwnerApprovalErrorClass(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrOwnerIdentityUnavailable):
		return "owner_identity_unavailable"
	case errors.Is(err, ErrUnauthorized), errors.Is(err, ErrOwnerApprovalNotAuthorized):
		return "unauthorized"
	case errors.Is(err, ErrProposalNotFound):
		return "proposal_not_found"
	case errors.Is(err, ErrFinancialReviewNotFound):
		return "financial_review_not_found"
	case errors.Is(err, ErrProposalHashMismatch):
		return "tuple_mismatch"
	case errors.Is(err, ErrOwnerApprovalGrantMismatch):
		return "content_changed"
	case errors.Is(err, ErrReviewNotRecommended):
		return "review_not_recommended"
	case errors.Is(err, ErrStaleApproval), errors.Is(err, ErrStaleFinancialReview):
		return "stale"
	case errors.Is(err, ErrSeparationOfDutiesViolation):
		return "separation_of_duties"
	case errors.Is(err, ErrApprovalConflict), errors.Is(err, tasks.ErrIdempotencyConflict), errors.Is(err, ErrIdempotencyConflict):
		return "approval_conflict"
	case errors.Is(err, ErrInfeasibleExecutionBudget):
		return "infeasible_execution_budget"
	case errors.Is(err, ErrInvalidExecutionBudget):
		return "invalid_execution_budget"
	case errors.Is(err, ErrExecutionRequirementsUnavailable):
		return "execution_requirements_unavailable"
	case errors.Is(err, ErrInvalidInput):
		return "invalid_input"
	default:
		return "internal"
	}
}

// OwnerApprovalToolCallID is the durable origin recorded for an approval made
// through the owner path. There is no conversation turn behind it, so none is claimed.
func OwnerApprovalToolCallID(proposalID, reviewID int64) string {
	return fmt.Sprintf("owner-cli:campaign-approve:%d:%d", proposalID, reviewID)
}

// Approve approves exactly proposalID + reviewID as the canonical owner.
func (a *OwnerApprover) Approve(ctx context.Context, proposalID, reviewID int64) (result OwnerApprovalResult, err error) {
	audit := OwnerApprovalAudit{ProposalID: proposalID, FinancialReviewID: reviewID}
	defer func() {
		switch {
		case err != nil:
			audit.Outcome, audit.ErrorClass, audit.Error = "failed", OwnerApprovalErrorClass(err), err.Error()
		case result.Reused:
			audit.Outcome = "reused"
		default:
			audit.Outcome = "approved"
		}
		a.audit(audit)
	}()
	if proposalID <= 0 || reviewID <= 0 {
		return OwnerApprovalResult{}, fmt.Errorf("%w: proposal and review ids must be positive", ErrInvalidInput)
	}

	// 1. WHO: derived from canonical state, never supplied by the caller.
	owner, err := a.owner.ResolveOwner(ctx, a.organizationID)
	if err != nil {
		return OwnerApprovalResult{}, err
	}
	audit.ActorRoleID, audit.ActorAuthorityClass, audit.OrganizationRevisionID = owner.RoleID, owner.AuthorityClass, owner.OrganizationRevisionID

	// 2. The resolved owner must hold the approval capability. ApprovalService
	// checks it again; this pre-check exists so a missing authorizer can never
	// turn into an unchecked approval.
	if err = a.authorizer.Authorize(ctx, a.organizationID, owner.OrganizationRevisionID, owner.RoleID, CapabilityOwnerApprovalCreate); err != nil {
		return OwnerApprovalResult{}, fmt.Errorf("%w: actor %q lacks %s: %v", ErrUnauthorized, owner.RoleID, CapabilityOwnerApprovalCreate, err)
	}

	// 3. WHAT: read the exact proposal and review, and bind their identities and
	// hashes into the grant. Which review approves which proposal is the owner's
	// explicit statement; the service rejects a review that belongs to another one.
	proposal, err := a.inputs.GetProposal(ctx, a.organizationID, proposalID)
	if err != nil {
		return OwnerApprovalResult{}, fmt.Errorf("load proposal %d: %w", proposalID, err)
	}
	review, err := a.inputs.GetFinancialReview(ctx, a.organizationID, reviewID)
	if err != nil {
		return OwnerApprovalResult{}, fmt.Errorf("load financial review %d: %w", reviewID, err)
	}
	audit.ProposalCanonicalHash, audit.FinancialReviewCanonicalHash = proposal.CanonicalHash, review.CanonicalHash
	grant, err := newOwnerApprovalGrant(proposal, review, owner.RoleID)
	if err != nil {
		return OwnerApprovalResult{}, err
	}

	// 4. The same ApprovalService every approval goes through: every rule
	// (tuple binding, verdict, current revision and review, executable and
	// feasible budget, separation of duties, hashes, idempotency) is its own.
	toolCallID := OwnerApprovalToolCallID(proposalID, reviewID)
	approval, reused, err := a.approvals.ApproveForExecution(ctx, ApproveParams{
		OrganizationID: a.organizationID, RevisionID: owner.OrganizationRevisionID,
		ProposalID: proposalID, FinancialReviewID: reviewID, ApprovedByRoleID: owner.RoleID, ToolCallID: toolCallID,
	}, grant)
	if err != nil {
		return OwnerApprovalResult{}, err
	}
	result = OwnerApprovalResult{
		ApprovalID: approval.ID, ActorRoleID: owner.RoleID, ActorAuthorityClass: owner.AuthorityClass,
		OrganizationRevisionID: owner.OrganizationRevisionID,
		ProposalID:             approval.ProposalID, ProposalCanonicalHash: approval.ProposalCanonicalHash,
		FinancialReviewID: approval.FinancialReviewID, FinancialReviewCanonicalHash: approval.FinancialReviewCanonicalHash,
		ExecutionBudget: approval.ExecutionBudget, ApprovalCanonicalHash: approval.CanonicalHash,
		ToolCallID: toolCallID, Reused: reused,
	}
	audit.ApprovalID = approval.ID
	return result, nil
}
