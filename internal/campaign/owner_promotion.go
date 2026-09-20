package campaign

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/executive"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

// The owner promotion path is a deterministic, non-generative way to promote an
// APPROVED campaign, so that an already-authorized owner action does not depend
// on a conversational model choosing to call a tool.
//
//	LLM may propose promotion.  LLM may request promotion.
//	LLM does NOT gate an already-authorized owner promotion.
//
// It is deliberately NOT a second promotion implementation. OwnerPromoter is a
// thin adapter: it resolves WHO is acting from canonical state, then hands off to
// the same PromotionService the CEO's campaign.promote_to_executive tool uses,
// with the same idempotency key. Every promotion rule -- approval status,
// hashes, latest revision, execution-budget feasibility, the historical
// idempotency identity, the separate trusted-root causation, the durable
// promotion record -- stays in PromotionService and runs unchanged.

// OwnerIdentity is the verified canonical owner.
type OwnerIdentity struct {
	RoleID                 string
	AuthorityClass         string
	RuntimeKind            string
	OrganizationRevisionID int64
}

// OwnerIdentityResolver derives the acting owner from canonical state. It takes
// no caller-supplied role: there is nothing to inject.
type OwnerIdentityResolver interface {
	ResolveOwner(ctx context.Context, organizationID string) (OwnerIdentity, error)
}

// RegistryOwnerResolver resolves the owner from the canonical organization
// registry: the organization's ONE enabled, non-retired human role with owner
// authority, which must be the same role Executive accepts as the owner of a
// goal (executive.OwnerRoleID). Zero, several, or a mismatch is an error.
type RegistryOwnerResolver struct{ Registry registry.Reader }

func (r RegistryOwnerResolver) ResolveOwner(ctx context.Context, organizationID string) (OwnerIdentity, error) {
	if r.Registry == nil {
		return OwnerIdentity{}, fmt.Errorf("%w: no registry reader", ErrOwnerIdentityUnavailable)
	}
	revision, err := r.Registry.GetCurrentRevision(ctx, organizationID)
	if err != nil {
		return OwnerIdentity{}, fmt.Errorf("%w: read current revision: %v", ErrOwnerIdentityUnavailable, err)
	}
	if revision == nil {
		return OwnerIdentity{}, fmt.Errorf("%w: organization %q has no current revision", ErrOwnerIdentityUnavailable, organizationID)
	}
	roles, err := r.Registry.ListRoles(ctx, organizationID, registry.RoleFilter{EnabledOnly: true})
	if err != nil {
		return OwnerIdentity{}, fmt.Errorf("%w: list roles: %v", ErrOwnerIdentityUnavailable, err)
	}
	var owners []registry.Role
	for _, role := range roles {
		if role.Enabled && role.RetiredAt == nil && role.AuthorityClass == "owner" {
			owners = append(owners, role)
		}
	}
	if len(owners) != 1 {
		return OwnerIdentity{}, fmt.Errorf("%w: expected exactly one enabled owner role, found %d", ErrOwnerIdentityUnavailable, len(owners))
	}
	owner := owners[0]
	if owner.ID != executive.OwnerRoleID {
		return OwnerIdentity{}, fmt.Errorf("%w: canonical owner role %q is not the executive owner %q", ErrOwnerIdentityUnavailable, owner.ID, executive.OwnerRoleID)
	}
	if owner.RuntimeKind != "human" {
		return OwnerIdentity{}, fmt.Errorf("%w: owner role %q runtime kind is %q, want human", ErrOwnerIdentityUnavailable, owner.ID, owner.RuntimeKind)
	}
	return OwnerIdentity{RoleID: owner.ID, AuthorityClass: owner.AuthorityClass, RuntimeKind: owner.RuntimeKind, OrganizationRevisionID: revision.ID}, nil
}

// ApprovalReader is the one read OwnerPromoter needs.
type ApprovalReader interface {
	GetOwnerApproval(ctx context.Context, organizationID string, approvalID int64) (CampaignOwnerApproval, error)
}

// PromotionExecutor is implemented by *PromotionService. Wiring the promoter to
// the same instance the CEO tool holds is what makes the two paths one path.
type PromotionExecutor interface {
	PromoteToExecutive(ctx context.Context, params PromoteToExecutiveParams) (PromotionResult, error)
	PromoteToExecutiveWithGrant(ctx context.Context, params PromoteToExecutiveParams, grant ExecutionModeGrant) (PromotionResult, error)
}

// OwnerPromotionResult is what a promotion (new or already durable) reports.
type OwnerPromotionResult struct {
	ApprovalID             int64  `json:"approval_id"`
	ActorRoleID            string `json:"actor_role_id"`
	ActorAuthorityClass    string `json:"actor_authority_class"`
	OrganizationRevisionID int64  `json:"organization_revision_id"`
	PromotionID            int64  `json:"promotion_id"`
	// ExecutionMode is the mode the campaign runs under: the durable
	// promotion's, which for a reused promotion is the one it was made with.
	ExecutionMode          ExecutionMode `json:"execution_mode"`
	ExecutiveRootTaskID    int64         `json:"executive_root_task_id"`
	ExecutiveCorrelationID string        `json:"executive_correlation_id"`
	// ExecutiveSubmitIdempotencyKey is the durable Campaign submission identity
	// recorded on the promotion (campaign-promotion:<approval>:<hash16>).
	ExecutiveSubmitIdempotencyKey string `json:"executive_submit_idempotency_key"`
	// TrustedRootCausation is the causation Campaign submits for this approval
	// ("owner:" + the colon-free trusted-root key).
	TrustedRootCausation    string `json:"trusted_root_causation"`
	PromotionIdempotencyKey string `json:"promotion_idempotency_key"`
	Reused                  bool   `json:"reused"`
}

// OwnerPromotionAudit is emitted exactly once per Promote call, success or not.
type OwnerPromotionAudit struct {
	ApprovalID             int64
	ActorRoleID            string
	ActorAuthorityClass    string
	OrganizationRevisionID int64
	Outcome                string // "promoted", "reused" or "failed"
	ErrorClass             string // empty on success
	Error                  string
	PromotionID            int64
	// ExecutionModeRequested is empty when the caller chose no mode.
	ExecutionModeRequested ExecutionMode
	ExecutionMode          ExecutionMode
	ExecutiveRootTaskID    int64
	IdempotencyKey         string
	TrustedRootCausation   string
	PromotionIdempotency   string
}

// OwnerPromoter is the deterministic owner promotion adapter.
type OwnerPromoter struct {
	organizationID string
	approvals      ApprovalReader
	promotions     PromotionExecutor
	owner          OwnerIdentityResolver
	authorizer     CapabilityAuthorizer
	audit          func(OwnerPromotionAudit)
}

// NewOwnerPromoter wires the adapter. Every dependency is required, including
// the authorizer: the owner's campaign.promotion.execute capability is checked
// here as well as inside PromotionService, and a missing authorizer must never
// mean "skip the check".
func NewOwnerPromoter(organizationID string, approvals ApprovalReader, promotions PromotionExecutor, owner OwnerIdentityResolver, authorizer CapabilityAuthorizer, audit func(OwnerPromotionAudit)) (*OwnerPromoter, error) {
	if strings.TrimSpace(organizationID) == "" {
		return nil, fmt.Errorf("%w: organization id is required", ErrInvalidInput)
	}
	if approvals == nil || promotions == nil || owner == nil || authorizer == nil {
		return nil, errors.New("owner promoter requires approvals, a promotion service, an owner resolver and an authorizer")
	}
	if audit == nil {
		audit = func(OwnerPromotionAudit) {}
	}
	return &OwnerPromoter{organizationID: organizationID, approvals: approvals, promotions: promotions, owner: owner, authorizer: authorizer, audit: audit}, nil
}

// OwnerPromotionErrorClass names a failure for audit and exit-code mapping.
func OwnerPromotionErrorClass(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrOwnerIdentityUnavailable):
		return "owner_identity_unavailable"
	case errors.Is(err, ErrUnauthorized):
		return "unauthorized"
	case errors.Is(err, ErrApprovalNotFound):
		return "approval_not_found"
	case errors.Is(err, ErrApprovalNotApproved):
		return "approval_not_approved"
	case errors.Is(err, ErrInfeasibleExecutionBudget):
		return "infeasible_execution_budget"
	case errors.Is(err, ErrInvalidExecutionBudget):
		return "invalid_execution_budget"
	case errors.Is(err, ErrExecutionRequirementsUnavailable):
		return "execution_requirements_unavailable"
	case errors.Is(err, ErrExecutionModeConflict):
		return "execution_mode_conflict"
	case errors.Is(err, ErrExecutionModeNotAuthorized):
		return "unauthorized"
	case errors.Is(err, tasks.ErrIdempotencyConflict):
		return "idempotency_conflict"
	case errors.Is(err, ErrInvalidInput):
		return "invalid_input"
	default:
		return "internal"
	}
}

// Promote promotes the given approval as the canonical owner.
//
// No execution mode is chosen: a new promotion runs analysis_only and an existing
// one is reported as it is.
func (p *OwnerPromoter) Promote(ctx context.Context, approvalID int64) (OwnerPromotionResult, error) {
	return p.promote(ctx, approvalID, "", false)
}

// PromoteWithMode is Promote with an explicit execution mode chosen by the owner.
// It is the only entry point that can request governed_implementation, and the
// mode reaches PromotionService only as a grant this promoter issues after it has
// resolved the canonical owner and verified the promotion capability. An approval
// that was already promoted under a different mode is refused, never re-promoted.
func (p *OwnerPromoter) PromoteWithMode(ctx context.Context, approvalID int64, mode ExecutionMode) (OwnerPromotionResult, error) {
	return p.promote(ctx, approvalID, mode, true)
}

func (p *OwnerPromoter) promote(ctx context.Context, approvalID int64, requested ExecutionMode, modeChosen bool) (result OwnerPromotionResult, err error) {
	audit := OwnerPromotionAudit{ApprovalID: approvalID}
	if modeChosen {
		audit.ExecutionModeRequested = requested.Normalized()
	}
	defer func() {
		if err != nil {
			audit.Outcome, audit.ErrorClass, audit.Error = "failed", OwnerPromotionErrorClass(err), err.Error()
		} else if result.Reused {
			audit.Outcome = "reused"
		} else {
			audit.Outcome = "promoted"
		}
		p.audit(audit)
	}()
	if approvalID <= 0 {
		return OwnerPromotionResult{}, fmt.Errorf("%w: approval id must be positive", ErrInvalidInput)
	}

	// 1. WHO: derived from canonical state, never supplied by the caller.
	owner, err := p.owner.ResolveOwner(ctx, p.organizationID)
	if err != nil {
		return OwnerPromotionResult{}, err
	}
	audit.ActorRoleID, audit.ActorAuthorityClass, audit.OrganizationRevisionID = owner.RoleID, owner.AuthorityClass, owner.OrganizationRevisionID

	// 2. The resolved actor must already hold the capability. PromotionService
	// checks it again; this pre-check exists so a missing authorizer can never
	// turn into an unchecked promotion.
	if err = p.authorizer.Authorize(ctx, p.organizationID, owner.OrganizationRevisionID, owner.RoleID, CapabilityPromotionExecute); err != nil {
		return OwnerPromotionResult{}, fmt.Errorf("%w: actor %q lacks %s: %v", ErrUnauthorized, owner.RoleID, CapabilityPromotionExecute, err)
	}

	// 3. The approval must exist in this organization. Everything else about it
	// (status, hashes, revision, budget, feasibility) is PromotionService's.
	approval, err := p.approvals.GetOwnerApproval(ctx, p.organizationID, approvalID)
	if err != nil {
		if errors.Is(err, ErrApprovalNotFound) {
			return OwnerPromotionResult{}, err
		}
		return OwnerPromotionResult{}, fmt.Errorf("load owner approval %d: %w", approvalID, err)
	}
	causationKey, causationErr := CampaignPromotionTrustedRootCausationKey(approval.ID, approval.CanonicalHash)
	if causationErr == nil {
		audit.TrustedRootCausation = "owner:" + causationKey
	}

	// 4. The same PromotionService call the CEO tool makes. The promotion
	// idempotency key is the tool's own, so a promotion made by either path is
	// found by the other; the tool-call id names this path's origin. There is no
	// conversation turn behind this action, so none is claimed.
	promotionKey := fmt.Sprintf("campaign-promotion:%s:%d", p.organizationID, approval.ID)
	promoteParams := PromoteToExecutiveParams{
		OrganizationID:         p.organizationID,
		OrganizationRevisionID: owner.OrganizationRevisionID,
		OwnerApprovalID:        approval.ID,
		PromotedByRoleID:       owner.RoleID,
		ToolCallID:             fmt.Sprintf("owner-cli:campaign-promote:%d", approval.ID),
		IdempotencyKey:         promotionKey,
	}
	var promoted PromotionResult
	if modeChosen {
		grant, grantErr := grantExecutionMode(requested, approval.ID, owner.RoleID)
		if grantErr != nil {
			return OwnerPromotionResult{}, grantErr
		}
		promoted, err = p.promotions.PromoteToExecutiveWithGrant(ctx, promoteParams, grant)
	} else {
		promoted, err = p.promotions.PromoteToExecutive(ctx, promoteParams)
	}
	if err != nil {
		return OwnerPromotionResult{}, err
	}
	result = OwnerPromotionResult{
		ApprovalID: approval.ID, ActorRoleID: owner.RoleID, ActorAuthorityClass: owner.AuthorityClass,
		OrganizationRevisionID: owner.OrganizationRevisionID,
		PromotionID:            promoted.Promotion.ID, ExecutionMode: promoted.Promotion.ExecutionMode.Normalized(),
		ExecutiveRootTaskID:           promoted.ExecutiveRootTaskID,
		ExecutiveCorrelationID:        promoted.ExecutiveCorrelationID,
		ExecutiveSubmitIdempotencyKey: promoted.Promotion.ExecutiveSubmitIdempotencyKey,
		TrustedRootCausation:          audit.TrustedRootCausation, PromotionIdempotencyKey: promotionKey, Reused: promoted.Reused,
	}
	audit.PromotionID, audit.ExecutiveRootTaskID, audit.ExecutionMode = result.PromotionID, result.ExecutiveRootTaskID, result.ExecutionMode
	audit.IdempotencyKey, audit.PromotionIdempotency = result.ExecutiveSubmitIdempotencyKey, promotionKey
	return result, nil
}
