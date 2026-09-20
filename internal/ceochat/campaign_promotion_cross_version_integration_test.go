//go:build integration

package ceochat_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/campaign"
	"github.com/Mireuz13/explorarte-organization/internal/executive"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

// CAMPAIGN_PROMOTION_TRUSTED_ROOT_CAUSATION_V1 -- cross-version reconciliation
// at the REAL Campaign boundary.
//
// Before the trusted-root fix, PromotionService submitted an Executive root
// whose idempotency key was
//
//	campaign-promotion:<approvalID>:<hash16>
//
// and whose causation was therefore "owner:" + that key -- a colon-bearing
// value the model-dispatch trusted-root syntax rejects. The corrected code keeps
// the SAME idempotency key (Campaign's durable submission identity) and sends a
// separate, colon-free causation "owner:campaign-promotion-<id>-<hash16>".
//
// In the real Task Engine those two requests share UNIQUE (organization_id,
// idempotency_key) but hash differently, so a retry after a crash between
// Executive.Submit and CreatePromotion must fail closed with
// tasks.ErrIdempotencyConflict: no second root, no promotion, and no automatic
// adoption or mutation of the historical malformed root. (An in-memory fake that
// reuses a root purely by idempotency key would hide exactly that.)

// allowAllAuthorizer grants every capability: authorization is not the boundary
// under test, and the real authorizer is exercised by the canonical e2e.
type allowAllAuthorizer struct{}

func (allowAllAuthorizer) Authorize(context.Context, string, int64, string, string) error { return nil }

// promotionRootFixture is a real proposal, recommended review and owner
// approval in PostgreSQL, the real Executive orchestrator over the real Task
// Engine, and a real PromotionService wired to them.
type promotionRootFixture struct {
	*chatFixture
	rf        *ownerApprovalReuseFixture
	approval  campaign.CampaignOwnerApproval
	proposal  campaign.CampaignProposal
	executive *executive.Orchestrator
	service   *campaign.PromotionService
	oldKey    string // historical idempotency key == the corrected code's key
	oldCause  string // historical (malformed) causation
	newCause  string // corrected, trusted-root-safe causation
}

func newPromotionRootFixture(t *testing.T) *promotionRootFixture {
	t.Helper()
	ctx := context.Background()
	f, rf := newOwnerApprovalReuseFixture(t)
	approval, _, err := rf.store.CreateOwnerApproval(ctx, rf.approvalCmd(t, "cross-version-approval-"+t.Name(), "call-cross-version-approval", rf.budget))
	if err != nil {
		t.Fatalf("create owner approval: %v", err)
	}
	proposal, err := rf.store.GetProposal(ctx, chatTestOrganization, rf.proposalID)
	if err != nil {
		t.Fatalf("read proposal: %v", err)
	}
	orchestrator, _ := buildRealExecutiveOrchestrator(t, f.store, chatTestOrganization)
	oldKey, err := campaign.CampaignPromotionSubmitKey(approval.ID, approval.CanonicalHash)
	if err != nil {
		t.Fatal(err)
	}
	newCauseKey, err := campaign.CampaignPromotionTrustedRootCausationKey(approval.ID, approval.CanonicalHash)
	if err != nil {
		t.Fatal(err)
	}
	return &promotionRootFixture{
		chatFixture: f, rf: rf, approval: approval, proposal: proposal, executive: orchestrator,
		service: campaign.NewPromotionService(rf.store, orchestrator, allowAllAuthorizer{}),
		oldKey:  oldKey, oldCause: "owner:" + oldKey, newCause: "owner:" + newCauseKey,
	}
}

// submitRequest is exactly what PromotionService submits for this approval, so a
// root seeded from it differs from the service's own request ONLY in causation.
func (p *promotionRootFixture) submitRequest(t *testing.T, trustedRootCausationKey string) executive.SubmitRequest {
	t.Helper()
	limits, err := campaign.ToAgentBudgetLimits(p.approval.ExecutionBudget)
	if err != nil {
		t.Fatal(err)
	}
	criteria := []executive.AcceptanceCriterion{{Text: campaign.HostGovernanceDesignCriterion, Phase: executive.AcceptanceDesign}}
	for _, text := range p.proposal.AcceptanceCriteria {
		criteria = append(criteria, executive.AcceptanceCriterion{Text: text, Phase: executive.AcceptanceImplementation})
	}
	return executive.SubmitRequest{
		Goal:                    executive.OwnerGoal{Goal: campaign.FormatProposalGoal(p.proposal), AcceptanceCriteria: criteria},
		ActorRoleID:             executive.OwnerRoleID,
		IdempotencyKey:          p.oldKey,
		TrustedRootCausationKey: trustedRootCausationKey,
		Budget:                  &limits,
	}
}

func (p *promotionRootFixture) promote(callID string) (campaign.PromotionResult, error) {
	return p.service.PromoteToExecutive(context.Background(), campaign.PromoteToExecutiveParams{
		OrganizationID: chatTestOrganization, OrganizationRevisionID: 1, OwnerApprovalID: p.approval.ID,
		PromotedByRoleID: "empresa/human", ConversationID: p.rf.conversationID, MessageID: p.rf.messageID,
		TurnTaskID: p.rf.taskID, ToolCallID: callID, IdempotencyKey: "cross-version-promotion-" + callID + "-" + p.oldKey,
	})
}

type rootCounts struct{ roots, historicalCausation, correctedCausation, promotions int }

func (p *promotionRootFixture) counts(t *testing.T) rootCounts {
	t.Helper()
	ctx := context.Background()
	var c rootCounts
	for query, dest := range map[string]*int{
		"SELECT count(*) FROM tasks WHERE organization_id='" + chatTestOrganization + "' AND idempotency_key='" + p.oldKey + "'": &c.roots,
		"SELECT count(*) FROM tasks WHERE task_class='owner.goal' AND causation_id='" + p.oldCause + "'":                         &c.historicalCausation,
		"SELECT count(*) FROM tasks WHERE task_class='owner.goal' AND causation_id='" + p.newCause + "'":                         &c.correctedCausation,
		"SELECT count(*) FROM campaign_promotions WHERE owner_approval_id=" + itoa(p.approval.ID):                                &c.promotions,
	} {
		if err := p.store.Pool().QueryRow(ctx, query).Scan(dest); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	return c
}

// rootFingerprint hashes the entire durable row (status, causation, hashes,
// timestamps...), so "unchanged" means unchanged in every column.
func (p *promotionRootFixture) rootFingerprint(t *testing.T, rootID int64) string {
	t.Helper()
	var fingerprint string
	if err := p.store.Pool().QueryRow(context.Background(), `SELECT md5(t::text) FROM tasks t WHERE id=$1`, rootID).Scan(&fingerprint); err != nil {
		t.Fatalf("fingerprint root %d: %v", rootID, err)
	}
	return fingerprint
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func trimOwnerPrefix(causation string) string { return strings.TrimPrefix(causation, "owner:") }

// THE MANDATORY REGRESSION. A root created by the previous code version exists
// (historical key + historical malformed causation), no CampaignPromotion
// exists (crash after Executive.Submit, before CreatePromotion), and the
// corrected PromotionService retries against the REAL Executive and Task Engine.
func TestPromotionCrossVersionRetryFailsClosedOnRealPostgres(t *testing.T) {
	p := newPromotionRootFixture(t)
	defer p.cleanup()
	ctx := context.Background()

	// Seed the historical root through the REAL orchestrator exactly as the
	// previous version submitted it: the goal, criteria and budget the service
	// itself would send, no TrustedRootCausationKey -> causation "owner:" + key.
	seeded, reused, err := p.executive.Submit(ctx, p.submitRequest(t, ""))
	if err != nil || reused {
		t.Fatalf("seed the historical root: run=%+v reused=%v err=%v", seeded, reused, err)
	}
	var seededCausation string
	if err := p.store.Pool().QueryRow(ctx, `SELECT causation_id FROM tasks WHERE id=$1`, seeded.RootTaskID).Scan(&seededCausation); err != nil {
		t.Fatal(err)
	}
	if seededCausation != p.oldCause {
		t.Fatalf("the seeded root is not the historical shape: causation %q, want %q", seededCausation, p.oldCause)
	}
	before := p.counts(t)
	if before.roots != 1 || before.historicalCausation != 1 || before.correctedCausation != 0 || before.promotions != 0 {
		t.Fatalf("seeded state = %+v, want exactly one historical root and no promotion", before)
	}
	fingerprintBefore := p.rootFingerprint(t, seeded.RootTaskID)

	for attempt := 1; attempt <= 2; attempt++ {
		result, err := p.promote("retry-" + itoa(int64(attempt)))
		if !errors.Is(err, tasks.ErrIdempotencyConflict) {
			t.Fatalf("attempt %d: want tasks.ErrIdempotencyConflict (fail closed at the Task Engine), got err=%v result=%+v", attempt, err, result)
		}
		after := p.counts(t)
		if after != before {
			t.Fatalf("attempt %d: state changed from %+v to %+v -- EXECUTIVE_ROOT_COUNT must stay 1 and CAMPAIGN_PROMOTION_COUNT 0", attempt, before, after)
		}
		if got := p.rootFingerprint(t, seeded.RootTaskID); got != fingerprintBefore {
			t.Fatalf("attempt %d: the historical root was modified (row fingerprint %s -> %s); it must never be adopted or mutated", attempt, fingerprintBefore, got)
		}
	}
	t.Logf("CROSS_VERSION_ERROR_CLASS=tasks.ErrIdempotencyConflict; roots=%d promotions=%d after two retries", before.roots, before.promotions)
}

// Positive control for the same boundary: a root created by the CORRECTED
// version (same key, separate trusted-root causation) that crashed before
// CreatePromotion DOES converge on retry -- same root, exactly one promotion.
// The fail-closed case above is therefore about the causation difference, not
// about retries being broken.
func TestPromotionSameVersionRetryConvergesOnRealPostgres(t *testing.T) {
	p := newPromotionRootFixture(t)
	defer p.cleanup()
	ctx := context.Background()

	seeded, reused, err := p.executive.Submit(ctx, p.submitRequest(t, trimOwnerPrefix(p.newCause)))
	if err != nil || reused {
		t.Fatalf("seed the corrected-version root: run=%+v reused=%v err=%v", seeded, reused, err)
	}
	result, err := p.promote("same-version")
	if err != nil {
		t.Fatalf("a retry after a same-version crash must converge: %v", err)
	}
	if result.ExecutiveRootTaskID != seeded.RootTaskID || !result.Reused {
		t.Fatalf("promotion = %+v, want the existing root %d reused", result, seeded.RootTaskID)
	}
	if got := p.counts(t); got.roots != 1 || got.correctedCausation != 1 || got.historicalCausation != 0 || got.promotions != 1 {
		t.Fatalf("counts = %+v, want one corrected root and one promotion", got)
	}
	replay, err := p.promote("same-version-replay")
	if err != nil || replay.ExecutiveRootTaskID != seeded.RootTaskID {
		t.Fatalf("replay must return the durable promotion: %+v, %v", replay, err)
	}
	if got := p.counts(t); got.roots != 1 || got.promotions != 1 {
		t.Fatalf("counts after replay = %+v", got)
	}
}
