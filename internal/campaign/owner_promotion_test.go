package campaign_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/campaign"
	"github.com/Mireuz13/explorarte-organization/internal/executive"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

// The owner promotion path is a thin adapter over the REAL PromotionService.
// These tests run the real service (real gates, real key derivation) against the
// in-memory store and a recording Executive submitter, so what is proven here is
// the adapter's behavior and that every promotion rule is still the service's.

var _ campaign.PromotionExecutor = (*campaign.PromotionService)(nil)

type fixedOwner struct {
	identity campaign.OwnerIdentity
	err      error
}

func (f fixedOwner) ResolveOwner(context.Context, string) (campaign.OwnerIdentity, error) {
	return f.identity, f.err
}

func canonicalOwner() campaign.OwnerIdentity {
	return campaign.OwnerIdentity{RoleID: executive.OwnerRoleID, AuthorityClass: "owner", RuntimeKind: "human", OrganizationRevisionID: 7}
}

func ownerGrants() fakeAuthorizer {
	return fakeAuthorizer{grants: map[string]bool{executive.OwnerRoleID + ":" + campaign.CapabilityPromotionExecute: true}}
}

type ownerPromotionRig struct {
	store     *memCampaignStore
	submitter *fakeSubmitter
	svc       *campaign.PromotionService
	promoter  *campaign.OwnerPromoter
	approval  campaign.CampaignOwnerApproval
	audits    []campaign.OwnerPromotionAudit
}

func newOwnerPromotionRig(t *testing.T, owner fixedOwner, auth fakeAuthorizer, requirements campaign.ExecutionRequirementsProvider, budget campaign.BudgetRecommendation, mutateProposal ...func(*campaign.CanonicalPayload)) *ownerPromotionRig {
	t.Helper()
	store, submitter, _, proposal, _, _ := setupPromotionFixture(t, mutateProposal...)
	submitter.rootsByKey = make(map[string]fakeRoot)
	_, approval := seedApproved(t, store, proposal, budget, "owner-cli")
	rig := &ownerPromotionRig{store: store, submitter: submitter, approval: approval}
	rig.svc = campaign.NewPromotionService(store, submitter, auth, requirements)
	promoter, err := campaign.NewOwnerPromoter("org-1", store, rig.svc, owner, auth, func(a campaign.OwnerPromotionAudit) { rig.audits = append(rig.audits, a) })
	if err != nil {
		t.Fatal(err)
	}
	rig.promoter = promoter
	return rig
}

func (r *ownerPromotionRig) lastAudit(t *testing.T) campaign.OwnerPromotionAudit {
	t.Helper()
	if len(r.audits) == 0 {
		t.Fatal("no audit event was emitted")
	}
	return r.audits[len(r.audits)-1]
}

// 1. An approved campaign, an authorized canonical owner: it promotes, with the
// historical idempotency identity AND the separate trusted-root causation, and
// the audit trail carries the actor, promotion, root and identities.
func TestOwnerPromotionPromotesAnApprovedCampaign(t *testing.T) {
	rig := newOwnerPromotionRig(t, fixedOwner{identity: canonicalOwner()}, ownerGrants(), fixed(floor()), feasibleBudget())

	result, err := rig.promoter.Promote(context.Background(), rig.approval.ID)
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}
	if rig.submitter.submitCalls != 1 || len(rig.submitter.rootsByKey) != 1 {
		t.Fatalf("submit calls %d, roots %d; want 1, 1", rig.submitter.submitCalls, len(rig.submitter.rootsByKey))
	}
	wantKey := fmt.Sprintf("campaign-promotion:%d:%.16s", rig.approval.ID, rig.approval.CanonicalHash)
	wantCausationKey := fmt.Sprintf("campaign-promotion-%d-%.16s", rig.approval.ID, rig.approval.CanonicalHash)
	if got := rig.submitter.lastRequest; got.IdempotencyKey != wantKey || got.TrustedRootCausationKey != wantCausationKey {
		t.Fatalf("submitted identities = (%q, %q), want (%q, %q)", got.IdempotencyKey, got.TrustedRootCausationKey, wantKey, wantCausationKey)
	}
	want, _ := campaign.ToAgentBudgetLimits(rig.approval.ExecutionBudget)
	if got := rig.submitter.lastRequest.Budget; got == nil || *got != want {
		t.Fatalf("submitted budget %+v, want the approved %+v exactly", got, want)
	}
	if result.ActorRoleID != executive.OwnerRoleID || result.ApprovalID != rig.approval.ID || result.Reused ||
		result.ExecutiveRootTaskID == 0 || result.PromotionID == 0 || result.ExecutiveSubmitIdempotencyKey != wantKey ||
		result.TrustedRootCausation != "owner:"+wantCausationKey || result.PromotionIdempotencyKey != "campaign-promotion:org-1:"+fmt.Sprint(rig.approval.ID) {
		t.Fatalf("result = %+v", result)
	}
	// The durable record names the canonical owner and this path's origin, and
	// claims no conversation turn that did not exist.
	stored, err := rig.store.GetPromotionByApprovalID(context.Background(), "org-1", rig.approval.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.PromotedByRoleID != executive.OwnerRoleID || stored.ToolCallID != fmt.Sprintf("owner-cli:campaign-promote:%d", rig.approval.ID) ||
		stored.ConversationID != 0 || stored.MessageID != 0 || stored.TurnTaskID != 0 {
		t.Fatalf("stored promotion = %+v", stored)
	}
	a := rig.lastAudit(t)
	if len(rig.audits) != 1 || a.Outcome != "promoted" || a.ErrorClass != "" || a.ActorRoleID != executive.OwnerRoleID || a.ActorAuthorityClass != "owner" ||
		a.PromotionID != result.PromotionID || a.ExecutiveRootTaskID != result.ExecutiveRootTaskID || a.IdempotencyKey != wantKey || a.TrustedRootCausation != "owner:"+wantCausationKey {
		t.Fatalf("audit = %+v", a)
	}
}

// 2. Authority is derived and enforced, never assumed: a resolved actor without
// the capability is denied, before anything is submitted.
func TestOwnerPromotionDeniesAnActorWithoutTheCapability(t *testing.T) {
	ceo := campaign.OwnerIdentity{RoleID: executive.CEORoleID, AuthorityClass: "executive", RuntimeKind: "executive_agent", OrganizationRevisionID: 7}
	auth := fakeAuthorizer{denies: map[string]bool{executive.CEORoleID + ":" + campaign.CapabilityPromotionExecute: true}}
	rig := newOwnerPromotionRig(t, fixedOwner{identity: ceo}, auth, fixed(floor()), feasibleBudget())

	_, err := rig.promoter.Promote(context.Background(), rig.approval.ID)
	if !errors.Is(err, campaign.ErrUnauthorized) {
		t.Fatalf("want ErrUnauthorized, got: %v", err)
	}
	assertNothingLaunched(t, rig)
	if a := rig.lastAudit(t); a.Outcome != "failed" || a.ErrorClass != "unauthorized" || a.ActorRoleID != executive.CEORoleID {
		t.Fatalf("audit = %+v", a)
	}
}

// A promoter can never be built without an authorizer: a nil one must not mean
// "skip the check".
func TestOwnerPromoterRequiresEveryDependency(t *testing.T) {
	store := newMemCampaignStore()
	svc := campaign.NewPromotionService(store, &fakeSubmitter{}, ownerGrants(), fixed(floor()))
	ok := fixedOwner{identity: canonicalOwner()}
	for name, build := range map[string]func() (*campaign.OwnerPromoter, error){
		"no authorizer": func() (*campaign.OwnerPromoter, error) {
			return campaign.NewOwnerPromoter("org-1", store, svc, ok, nil, nil)
		},
		"no owner resolver": func() (*campaign.OwnerPromoter, error) {
			return campaign.NewOwnerPromoter("org-1", store, svc, nil, ownerGrants(), nil)
		},
		"no promotion service": func() (*campaign.OwnerPromoter, error) {
			return campaign.NewOwnerPromoter("org-1", store, nil, ok, ownerGrants(), nil)
		},
		"no approvals": func() (*campaign.OwnerPromoter, error) {
			return campaign.NewOwnerPromoter("org-1", nil, svc, ok, ownerGrants(), nil)
		},
		"no organization": func() (*campaign.OwnerPromoter, error) {
			return campaign.NewOwnerPromoter(" ", store, svc, ok, ownerGrants(), nil)
		},
	} {
		if promoter, err := build(); err == nil || promoter != nil {
			t.Errorf("%s must be refused", name)
		}
	}
}

func assertNothingLaunched(t *testing.T, rig *ownerPromotionRig) {
	t.Helper()
	if rig.submitter.submitCalls != 0 || len(rig.submitter.rootsByKey) != 0 {
		t.Fatalf("Executive.Submit calls %d, roots %d; want 0, 0 (no root on a pre-submit failure)", rig.submitter.submitCalls, len(rig.submitter.rootsByKey))
	}
	if len(rig.store.promotions) != 0 {
		t.Fatalf("promotion rows = %d, want 0", len(rig.store.promotions))
	}
}

// 3. A campaign that is not approved for execution is never promoted.
func TestOwnerPromotionRefusesAnApprovalThatIsNotApproved(t *testing.T) {
	rig := newOwnerPromotionRig(t, fixedOwner{identity: canonicalOwner()}, ownerGrants(), fixed(floor()), feasibleBudget())
	revoked := rig.store.approvals[rig.approval.ID]
	revoked.Status = "revoked"
	rig.store.approvals[rig.approval.ID] = revoked

	_, err := rig.promoter.Promote(context.Background(), rig.approval.ID)
	if !errors.Is(err, campaign.ErrApprovalNotApproved) {
		t.Fatalf("want ErrApprovalNotApproved, got: %v", err)
	}
	assertNothingLaunched(t, rig)
	if a := rig.lastAudit(t); a.ErrorClass != "approval_not_approved" {
		t.Fatalf("audit = %+v", a)
	}
}

// 4. The execution-feasibility gate still runs: an approved budget the canonical
// execution can no longer fit is refused, and never raised.
func TestOwnerPromotionRunsTheFeasibilityGate(t *testing.T) {
	rig := newOwnerPromotionRig(t, fixedOwner{identity: canonicalOwner()}, ownerGrants(), fixed(floor()), productionBudget())

	_, err := rig.promoter.Promote(context.Background(), rig.approval.ID)
	if !errors.Is(err, campaign.ErrInfeasibleExecutionBudget) {
		t.Fatalf("want ErrInfeasibleExecutionBudget, got: %v", err)
	}
	assertNothingLaunched(t, rig)
	after, _ := rig.store.GetOwnerApproval(context.Background(), "org-1", rig.approval.ID)
	if after.ExecutionBudget != productionBudget() {
		t.Fatalf("the approved budget changed to %+v; it must never be raised", after.ExecutionBudget)
	}
	if a := rig.lastAudit(t); a.ErrorClass != "infeasible_execution_budget" {
		t.Fatalf("audit = %+v", a)
	}
}

// 5. Unavailable requirements fail explicitly, with the existing error.
func TestOwnerPromotionFailsExplicitlyWithoutExecutionRequirements(t *testing.T) {
	for name, provider := range map[string]campaign.ExecutionRequirementsProvider{
		"no provider":     nil,
		"provider errors": errProvider{errors.New("no priced route")},
	} {
		rig := newOwnerPromotionRig(t, fixedOwner{identity: canonicalOwner()}, ownerGrants(), provider, feasibleBudget())
		if _, err := rig.promoter.Promote(context.Background(), rig.approval.ID); !errors.Is(err, campaign.ErrExecutionRequirementsUnavailable) {
			t.Fatalf("%s: want ErrExecutionRequirementsUnavailable, got: %v", name, err)
		}
		assertNothingLaunched(t, rig)
		if a := rig.lastAudit(t); a.ErrorClass != "execution_requirements_unavailable" {
			t.Fatalf("%s: audit = %+v", name, a)
		}
	}
}

// 6. Re-running converges on the same promotion and root; nothing is submitted twice.
func TestOwnerPromotionRepeatConvergesWithoutASecondRoot(t *testing.T) {
	rig := newOwnerPromotionRig(t, fixedOwner{identity: canonicalOwner()}, ownerGrants(), fixed(floor()), feasibleBudget())
	first, err := rig.promoter.Promote(context.Background(), rig.approval.ID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := rig.promoter.Promote(context.Background(), rig.approval.ID)
	if err != nil {
		t.Fatalf("repeat: %v", err)
	}
	if !second.Reused || second.PromotionID != first.PromotionID || second.ExecutiveRootTaskID != first.ExecutiveRootTaskID {
		t.Fatalf("repeat = %+v, want the same promotion %d / root %d, reused", second, first.PromotionID, first.ExecutiveRootTaskID)
	}
	if rig.submitter.submitCalls != 1 || len(rig.submitter.rootsByKey) != 1 || len(rig.store.promotions) != 1 {
		t.Fatalf("submit calls %d, roots %d, promotions %d; want 1, 1, 1", rig.submitter.submitCalls, len(rig.submitter.rootsByKey), len(rig.store.promotions))
	}
	if a := rig.lastAudit(t); a.Outcome != "reused" {
		t.Fatalf("audit outcome = %q, want reused", a.Outcome)
	}
}

// 7. Failures before Executive.Submit leave no root: identity, approval lookup, input.
func TestOwnerPromotionPreSubmitFailuresLeaveNoRoot(t *testing.T) {
	t.Run("owner identity unavailable", func(t *testing.T) {
		rig := newOwnerPromotionRig(t, fixedOwner{err: fmt.Errorf("%w: two owners", campaign.ErrOwnerIdentityUnavailable)}, ownerGrants(), fixed(floor()), feasibleBudget())
		if _, err := rig.promoter.Promote(context.Background(), rig.approval.ID); !errors.Is(err, campaign.ErrOwnerIdentityUnavailable) {
			t.Fatalf("got: %v", err)
		}
		assertNothingLaunched(t, rig)
		if a := rig.lastAudit(t); a.ErrorClass != "owner_identity_unavailable" {
			t.Fatalf("audit = %+v", a)
		}
	})
	t.Run("unknown approval", func(t *testing.T) {
		rig := newOwnerPromotionRig(t, fixedOwner{identity: canonicalOwner()}, ownerGrants(), fixed(floor()), feasibleBudget())
		if _, err := rig.promoter.Promote(context.Background(), 987654); !errors.Is(err, campaign.ErrApprovalNotFound) {
			t.Fatalf("an unknown approval must be ErrApprovalNotFound, got: %v", err)
		}
		assertNothingLaunched(t, rig)
	})
	t.Run("non-positive approval", func(t *testing.T) {
		rig := newOwnerPromotionRig(t, fixedOwner{identity: canonicalOwner()}, ownerGrants(), fixed(floor()), feasibleBudget())
		if _, err := rig.promoter.Promote(context.Background(), 0); !errors.Is(err, campaign.ErrInvalidInput) {
			t.Fatalf("got: %v", err)
		}
		assertNothingLaunched(t, rig)
	})
}

// 8. ONE PromotionService path. A promotion made the way the CEO tool makes one
// is found by the owner path, and the reverse; the idempotency key is shared, so
// neither path can create a second root.
func TestOwnerPromotionAndTheCEOToolShareOnePromotionPath(t *testing.T) {
	ceoToolCall := func(rig *ownerPromotionRig) (campaign.PromotionResult, error) {
		// Exactly the parameters ceochat's promoteHandler passes.
		return rig.svc.PromoteToExecutive(context.Background(), campaign.PromoteToExecutiveParams{
			OrganizationID: "org-1", OrganizationRevisionID: 7, OwnerApprovalID: rig.approval.ID,
			PromotedByRoleID: executive.OwnerRoleID, ConversationID: 1, MessageID: 1, TurnTaskID: 1,
			ToolCallID: "call-from-the-ceo-tool", IdempotencyKey: fmt.Sprintf("campaign-promotion:%s:%d", "org-1", rig.approval.ID),
		})
	}
	t.Run("tool first, owner path second", func(t *testing.T) {
		rig := newOwnerPromotionRig(t, fixedOwner{identity: canonicalOwner()}, ownerGrants(), fixed(floor()), feasibleBudget())
		viaTool, err := ceoToolCall(rig)
		if err != nil {
			t.Fatal(err)
		}
		viaOwner, err := rig.promoter.Promote(context.Background(), rig.approval.ID)
		if err != nil || !viaOwner.Reused || viaOwner.ExecutiveRootTaskID != viaTool.ExecutiveRootTaskID {
			t.Fatalf("owner path = %+v, %v; want the tool's root %d reused", viaOwner, err, viaTool.ExecutiveRootTaskID)
		}
		if len(rig.submitter.rootsByKey) != 1 {
			t.Fatalf("roots = %d, want 1", len(rig.submitter.rootsByKey))
		}
	})
	t.Run("owner path first, tool second", func(t *testing.T) {
		rig := newOwnerPromotionRig(t, fixedOwner{identity: canonicalOwner()}, ownerGrants(), fixed(floor()), feasibleBudget())
		viaOwner, err := rig.promoter.Promote(context.Background(), rig.approval.ID)
		if err != nil {
			t.Fatal(err)
		}
		viaTool, err := ceoToolCall(rig)
		if err != nil || !viaTool.Reused || viaTool.ExecutiveRootTaskID != viaOwner.ExecutiveRootTaskID {
			t.Fatalf("tool path = %+v, %v; want the owner path's root %d reused", viaTool, err, viaOwner.ExecutiveRootTaskID)
		}
		if len(rig.submitter.rootsByKey) != 1 {
			t.Fatalf("roots = %d, want 1", len(rig.submitter.rootsByKey))
		}
	})
	t.Run("both submit the identical request shape", func(t *testing.T) {
		a := newOwnerPromotionRig(t, fixedOwner{identity: canonicalOwner()}, ownerGrants(), fixed(floor()), feasibleBudget())
		if _, err := ceoToolCall(a); err != nil {
			t.Fatal(err)
		}
		b := newOwnerPromotionRig(t, fixedOwner{identity: canonicalOwner()}, ownerGrants(), fixed(floor()), feasibleBudget())
		if _, err := b.promoter.Promote(context.Background(), b.approval.ID); err != nil {
			t.Fatal(err)
		}
		ra, rb := a.submitter.lastRequest, b.submitter.lastRequest
		if ra.ActorRoleID != rb.ActorRoleID || *ra.Budget != *rb.Budget || ra.Goal.Goal != rb.Goal.Goal || len(ra.Goal.AcceptanceCriteria) != len(rb.Goal.AcceptanceCriteria) {
			t.Fatalf("the two paths submitted different requests:\n tool  %+v\n owner %+v", ra, rb)
		}
	})
}

// The canonical owner is derived from the registry and refuses anything
// ambiguous: exactly one enabled human owner role, and it must be the role
// Executive accepts as the owner of a goal.
type fakeOwnerRegistry struct {
	registry.Reader
	revision *registry.Revision
	roles    []registry.Role
	err      error
}

func (f fakeOwnerRegistry) GetCurrentRevision(context.Context, string) (*registry.Revision, error) {
	return f.revision, f.err
}
func (f fakeOwnerRegistry) ListRoles(context.Context, string, registry.RoleFilter) ([]registry.Role, error) {
	return f.roles, f.err
}

func TestRegistryOwnerResolverDerivesTheOnlyCanonicalOwner(t *testing.T) {
	human := registry.Role{ID: executive.OwnerRoleID, AuthorityClass: "owner", RuntimeKind: "human", Enabled: true}
	ceo := registry.Role{ID: executive.CEORoleID, AuthorityClass: "executive", RuntimeKind: "executive_agent", Enabled: true}
	rev := &registry.Revision{ID: 5}
	ctx := context.Background()

	got, err := campaign.RegistryOwnerResolver{Registry: fakeOwnerRegistry{revision: rev, roles: []registry.Role{ceo, human}}}.ResolveOwner(ctx, "org-1")
	if err != nil || got.RoleID != executive.OwnerRoleID || got.AuthorityClass != "owner" || got.OrganizationRevisionID != 5 {
		t.Fatalf("owner = %+v, %v", got, err)
	}

	otherOwner := human
	otherOwner.ID = "empresa/otro"
	notHuman := human
	notHuman.RuntimeKind = "executive_agent"
	retiredNow := human
	stamp := registryTime()
	retiredNow.RetiredAt = &stamp
	disabled := human
	disabled.Enabled = false
	for name, reg := range map[string]fakeOwnerRegistry{
		"no owner":                 {revision: rev, roles: []registry.Role{ceo}},
		"two owners":               {revision: rev, roles: []registry.Role{human, otherOwner}},
		"owner is not executive's": {revision: rev, roles: []registry.Role{otherOwner}},
		"owner is not human":       {revision: rev, roles: []registry.Role{notHuman}},
		"owner retired":            {revision: rev, roles: []registry.Role{retiredNow}},
		"owner disabled":           {revision: rev, roles: []registry.Role{disabled}},
		"no current revision":      {revision: nil, roles: []registry.Role{human}},
		"registry error":           {revision: rev, err: errors.New("db down")},
	} {
		if _, err := (campaign.RegistryOwnerResolver{Registry: reg}).ResolveOwner(ctx, "org-1"); !errors.Is(err, campaign.ErrOwnerIdentityUnavailable) {
			t.Errorf("%s: want ErrOwnerIdentityUnavailable, got: %v", name, err)
		}
	}
	if _, err := (campaign.RegistryOwnerResolver{}).ResolveOwner(ctx, "org-1"); !errors.Is(err, campaign.ErrOwnerIdentityUnavailable) {
		t.Errorf("nil registry: got %v", err)
	}
}

func TestOwnerPromotionErrorClasses(t *testing.T) {
	for err, want := range map[error]string{
		nil: "", campaign.ErrOwnerIdentityUnavailable: "owner_identity_unavailable", campaign.ErrUnauthorized: "unauthorized",
		campaign.ErrApprovalNotFound: "approval_not_found", campaign.ErrApprovalNotApproved: "approval_not_approved",
		campaign.ErrInfeasibleExecutionBudget: "infeasible_execution_budget", campaign.ErrInvalidExecutionBudget: "invalid_execution_budget",
		campaign.ErrExecutionRequirementsUnavailable: "execution_requirements_unavailable", tasks.ErrIdempotencyConflict: "idempotency_conflict",
		campaign.ErrInvalidInput: "invalid_input", errors.New("boom"): "internal",
	} {
		wrapped := err
		if err != nil {
			wrapped = fmt.Errorf("executive submit: %w", err)
		}
		if got := campaign.OwnerPromotionErrorClass(wrapped); got != want {
			t.Errorf("class(%v) = %q, want %q", err, got, want)
		}
	}
}
