package ceochat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/campaign"
	"github.com/Mireuz13/explorarte-organization/internal/executionharness"
)

type fakeCampaignStore struct {
	mu        sync.Mutex
	proposals map[string]campaign.CampaignProposal // org:key -> proposal
	byID      map[int64]campaign.CampaignProposal
	nextID    int64
}

func newFakeCampaignStore() *fakeCampaignStore {
	return &fakeCampaignStore{
		proposals: make(map[string]campaign.CampaignProposal),
		byID:      make(map[int64]campaign.CampaignProposal),
		nextID:    1,
	}
}

func (s *fakeCampaignStore) CreateProposal(ctx context.Context, cmd campaign.CreateProposalCommand) (campaign.CampaignProposal, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	lookupKey := cmd.OrganizationID + ":" + cmd.IdempotencyKey
	if existing, found := s.proposals[lookupKey]; found {
		if existing.CanonicalHash == cmd.CanonicalHash {
			return existing, true, nil
		}
		return campaign.CampaignProposal{}, false, fmt.Errorf("%w: hash mismatch", campaign.ErrIdempotencyConflict)
	}

	p := campaign.CampaignProposal{
		ID:                      s.nextID,
		OrganizationID:          cmd.OrganizationID,
		ConversationID:          cmd.ConversationID,
		CreatedByRoleID:         cmd.CreatedByRoleID,
		CreatedFromMessageID:    cmd.CreatedFromMessageID,
		TaskID:                  cmd.TaskID,
		AttemptID:               cmd.AttemptID,
		ToolCallID:              cmd.ToolCallID,
		Status:                  campaign.StatusDraft,
		Title:                   cmd.Title,
		Goal:                    cmd.Goal,
		AcceptanceCriteria:      cmd.AcceptanceCriteria,
		Requirements:            cmd.Requirements,
		Budget:                  cmd.Budget,
		Assumptions:             cmd.Assumptions,
		Risks:                   cmd.Risks,
		OpenQuestions:           cmd.OpenQuestions,
		FinancialReviewRequired: true,
		ExecutionStarted:        false,
		IdempotencyKey:          cmd.IdempotencyKey,
		CanonicalHash:           cmd.CanonicalHash,
		CreatedAt:               time.Now(),
		UpdatedAt:               time.Now(),
	}
	s.nextID++
	s.proposals[lookupKey] = p
	s.byID[p.ID] = p
	return p, false, nil
}

func (s *fakeCampaignStore) GetProposal(ctx context.Context, organizationID string, id int64) (campaign.CampaignProposal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, found := s.byID[id]
	if !found || p.OrganizationID != organizationID {
		return campaign.CampaignProposal{}, campaign.ErrProposalNotFound
	}
	return p, nil
}

func (s *fakeCampaignStore) ListProposals(ctx context.Context, organizationID string, limit, offset int) ([]campaign.CampaignProposal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var result []campaign.CampaignProposal
	for _, p := range s.byID {
		if p.OrganizationID == organizationID {
			result = append(result, p)
		}
	}
	return result, nil
}

type fakeAuthorizer struct {
	allowed map[string]bool // roleID:capability -> bool
}

func (a fakeAuthorizer) Authorize(ctx context.Context, organizationID string, revisionID int64, roleID, capability string) error {
	key := roleID + ":" + capability
	if a.allowed[key] {
		return nil
	}
	return fmt.Errorf("role %q not authorized for %q", roleID, capability)
}

func setupCampaignTestRegistry(t *testing.T, store campaign.Store, auth CapabilityAuthorizer) *ToolRegistry {
	reg := NewToolRegistry()
	if err := RegisterCampaignTools(reg, "org-test", store, auth); err != nil {
		t.Fatalf("RegisterCampaignTools failed: %v", err)
	}
	return reg
}

func TestCampaignProposeValidationAndCreation(t *testing.T) {
	store := newFakeCampaignStore()
	auth := fakeAuthorizer{
		allowed: map[string]bool{
			"owner:campaign.proposal.create": true,
			"owner:campaign.proposal.read":   true,
		},
	}
	reg := setupCampaignTestRegistry(t, store, auth)
	executor := RegistryToolExecutor{Registry: reg}

	validPayload := json.RawMessage(`{
		"title": "Summer Growth Campaign",
		"goal": "Acquire 500 verified creators",
		"acceptance_criteria": ["CPA < $15", "Active rate > 10%"],
		"budget": {"currency": "USD", "max_amount": 2500.0, "source": "OWNER_LIMIT"},
		"assumptions": ["Creator portal is stable"]
	}`)

	turnCtx := TurnContext{
		OrganizationID:         "org-test",
		OrganizationRevisionID: 1,
		ConversationID:         100,
		OwnerRoleID:            "owner",
		OwnerMessageID:         200,
		TaskID:                 300,
		AttemptID:              1,
		ActorRoleID:            "owner",
	}

	ctx := WithTurnContext(context.Background(), turnCtx)

	identity := executionharness.RunIdentity{
		OrganizationID: "org-test",
		RoleID:         CEORoleID,
		TaskID:         300,
		AttemptID:      1,
	}

	req := executionharness.ToolRequest{
		ToolName:   "campaign.propose",
		ToolCallID: "call_abc123",
		Arguments:  validPayload,
	}

	// 1. Successful proposal creation
	res, err := executor.Execute(ctx, identity, req)
	if err != nil {
		t.Fatalf("unexpected execute error: %v", err)
	}

	var proj ProposeResultProjection
	if err := json.Unmarshal(res.Content, &proj); err != nil {
		t.Fatalf("failed to unmarshal projection: %v", err)
	}

	if proj.ProposalID != 1 {
		t.Errorf("expected proposal ID 1, got %d", proj.ProposalID)
	}
	if proj.Status != "draft" {
		t.Errorf("expected status draft, got %q", proj.Status)
	}
	if !proj.FinancialReviewRequired {
		t.Errorf("expected financial_review_required = true")
	}
	if proj.ExecutionStarted {
		t.Errorf("expected execution_started = false")
	}

	// 2. Exact Replay (same tool_call_id, same payload) -> returns same proposal
	resReplay, err := executor.Execute(ctx, identity, req)
	if err != nil {
		t.Fatalf("replay failed: %v", err)
	}
	var projReplay ProposeResultProjection
	_ = json.Unmarshal(resReplay.Content, &projReplay)
	if projReplay.ProposalID != 1 {
		t.Errorf("expected replayed proposal ID 1, got %d", projReplay.ProposalID)
	}

	// 3. Conflict on same tool_call_id with different payload
	differentPayload := json.RawMessage(`{
		"title": "Summer Growth Campaign - Revised Different",
		"goal": "Acquire 1000 verified creators",
		"acceptance_criteria": ["CPA < $10"]
	}`)
	reqDifferent := req
	reqDifferent.Arguments = differentPayload

	_, err = executor.Execute(ctx, identity, reqDifferent)
	if !errors.Is(err, campaign.ErrIdempotencyConflict) {
		t.Fatalf("expected ErrIdempotencyConflict on different payload replay, got: %v", err)
	}

	// 4. Unauthorized actor
	turnCtxUnauthorized := turnCtx
	turnCtxUnauthorized.ActorRoleID = "guest_role"
	ctxUnauthorized := WithTurnContext(context.Background(), turnCtxUnauthorized)
	reqNew := req
	reqNew.ToolCallID = "call_xyz999"

	_, err = executor.Execute(ctxUnauthorized, identity, reqNew)
	if !errors.Is(err, ErrUnauthorizedActor) {
		t.Fatalf("expected ErrUnauthorizedActor for unauthorized caller, got: %v", err)
	}

	// 5. Read back with campaign.get_proposal
	getReq := executionharness.ToolRequest{
		ToolName:   "campaign.get_proposal",
		ToolCallID: "call_get1",
		Arguments:  json.RawMessage(`{"proposal_id": 1}`),
	}
	getRes, err := executor.Execute(ctx, identity, getReq)
	if err != nil {
		t.Fatalf("campaign.get_proposal failed: %v", err)
	}
	var fetched campaign.CampaignProposal
	if err := json.Unmarshal(getRes.Content, &fetched); err != nil {
		t.Fatalf("unmarshal get_proposal result: %v", err)
	}
	if fetched.ID != 1 || fetched.Title != "Summer Growth Campaign" {
		t.Errorf("unexpected fetched proposal: %+v", fetched)
	}
}
