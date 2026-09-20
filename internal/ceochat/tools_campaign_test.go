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
	"github.com/Mireuz13/explorarte-organization/internal/executive"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

type fakeCampaignStore struct {
	mu               sync.Mutex
	proposals        map[string]campaign.CampaignProposal // org:key -> proposal
	byID             map[int64]campaign.CampaignProposal
	reviewRequests   map[int64]campaign.CampaignFinancialReviewRequest
	financialReviews map[int64]campaign.CampaignFinancialReview
	approvals        map[int64]campaign.CampaignOwnerApproval
	approvalsByKey   map[string]campaign.CampaignOwnerApproval
	approvalsByTuple map[string]campaign.CampaignOwnerApproval
	promotions       map[int64]campaign.CampaignPromotion
	promotionsByKey  map[string]campaign.CampaignPromotion
	promotionsByAppr map[int64]campaign.CampaignPromotion
	nextID           int64
}

func newFakeCampaignStore() *fakeCampaignStore {
	return &fakeCampaignStore{
		proposals:        make(map[string]campaign.CampaignProposal),
		byID:             make(map[int64]campaign.CampaignProposal),
		reviewRequests:   make(map[int64]campaign.CampaignFinancialReviewRequest),
		financialReviews: make(map[int64]campaign.CampaignFinancialReview),
		approvals:        make(map[int64]campaign.CampaignOwnerApproval),
		approvalsByKey:   make(map[string]campaign.CampaignOwnerApproval),
		approvalsByTuple: make(map[string]campaign.CampaignOwnerApproval),
		promotions:       make(map[int64]campaign.CampaignPromotion),
		promotionsByKey:  make(map[string]campaign.CampaignPromotion),
		promotionsByAppr: make(map[int64]campaign.CampaignPromotion),
		nextID:           1,
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

	rootID := s.nextID
	p := campaign.CampaignProposal{
		RevisionNumber:          1,
		RootProposalID:          &rootID,
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

func (s *fakeCampaignStore) CreateReviewRequest(ctx context.Context, cmd campaign.CreateReviewRequestCommand) (campaign.CampaignFinancialReviewRequest, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	req := campaign.CampaignFinancialReviewRequest{
		ID:                          s.nextID,
		OrganizationID:              cmd.OrganizationID,
		ProposalID:                  cmd.ProposalID,
		ProposalCanonicalHash:       cmd.ProposalCanonicalHash,
		RequestedByRoleID:           cmd.RequestedByRoleID,
		RequestedFromConversationID: cmd.RequestedFromConversationID,
		RequestedFromMessageID:      cmd.RequestedFromMessageID,
		RequestedFromTaskID:         cmd.RequestedFromTaskID,
		ReviewerRoleID:              cmd.ReviewerRoleID,
		ReviewTaskID:                cmd.ReviewTaskID,
		Status:                      campaign.ReviewRequestStatusPending,
		IdempotencyKey:              cmd.IdempotencyKey,
		CreatedAt:                   time.Now(),
		UpdatedAt:                   time.Now(),
	}
	s.nextID++
	s.reviewRequests[req.ID] = req
	return req, false, nil
}

func (s *fakeCampaignStore) GetReviewRequest(ctx context.Context, organizationID string, id int64) (campaign.CampaignFinancialReviewRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	r, found := s.reviewRequests[id]
	if !found || r.OrganizationID != organizationID {
		return campaign.CampaignFinancialReviewRequest{}, campaign.ErrReviewRequestNotFound
	}
	return r, nil
}

func (s *fakeCampaignStore) GetLatestReviewRequestForProposal(ctx context.Context, organizationID string, proposalID int64) (campaign.CampaignFinancialReviewRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var latest campaign.CampaignFinancialReviewRequest
	found := false
	for _, r := range s.reviewRequests {
		if r.OrganizationID == organizationID && r.ProposalID == proposalID {
			if !found || r.ID > latest.ID {
				latest = r
				found = true
			}
		}
	}
	if !found {
		return campaign.CampaignFinancialReviewRequest{}, campaign.ErrReviewRequestNotFound
	}
	return latest, nil
}

func (s *fakeCampaignStore) GetReviewRequestByTaskID(ctx context.Context, organizationID string, taskID int64) (campaign.CampaignFinancialReviewRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var latest campaign.CampaignFinancialReviewRequest
	found := false
	for _, r := range s.reviewRequests {
		if r.OrganizationID == organizationID && r.ReviewTaskID == taskID {
			if !found || r.ID > latest.ID {
				latest = r
				found = true
			}
		}
	}
	if !found {
		return campaign.CampaignFinancialReviewRequest{}, campaign.ErrReviewRequestNotFound
	}
	return latest, nil
}

func (s *fakeCampaignStore) RecordFinancialReview(ctx context.Context, cmd campaign.RecordFinancialReviewCommand) (campaign.CampaignFinancialReview, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if cmd.ReviewerRoleID == "empresa/ceo" || cmd.ReviewerRoleID == "empresa/human" {
		return campaign.CampaignFinancialReview{}, false, campaign.ErrSeparationOfDutiesViolation
	}

	rev := campaign.CampaignFinancialReview{
		ID:                    s.nextID,
		OrganizationID:        cmd.OrganizationID,
		ReviewRequestID:       cmd.ReviewRequestID,
		ProposalID:            cmd.ProposalID,
		ProposalCanonicalHash: cmd.ProposalCanonicalHash,
		ReviewerRoleID:        cmd.ReviewerRoleID,
		ReviewTaskID:          cmd.ReviewTaskID,
		ReviewAttemptID:       cmd.ReviewAttemptID,
		Verdict:               cmd.Verdict,
		RecommendedBudget:     cmd.RecommendedBudget,
		EstimatedCost:         cmd.EstimatedCost,
		Assumptions:           cmd.Assumptions,
		Risks:                 cmd.Risks,
		RequiredCorrections:   cmd.RequiredCorrections,
		MissingInformation:    cmd.MissingInformation,
		Summary:               cmd.Summary,
		CanonicalHash:         cmd.CanonicalHash,
		CreatedAt:             time.Now(),
	}
	s.nextID++
	s.financialReviews[rev.ID] = rev
	if req, ok := s.reviewRequests[cmd.ReviewRequestID]; ok {
		req.Status = campaign.ReviewRequestStatusCompleted
		s.reviewRequests[cmd.ReviewRequestID] = req
	}
	return rev, false, nil
}

func (s *fakeCampaignStore) GetFinancialReview(ctx context.Context, organizationID string, id int64) (campaign.CampaignFinancialReview, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	r, found := s.financialReviews[id]
	if !found || r.OrganizationID != organizationID {
		return campaign.CampaignFinancialReview{}, campaign.ErrFinancialReviewNotFound
	}
	return r, nil
}

func (s *fakeCampaignStore) GetFinancialReviewByRequestID(ctx context.Context, organizationID string, requestID int64) (campaign.CampaignFinancialReview, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, r := range s.financialReviews {
		if r.OrganizationID == organizationID && r.ReviewRequestID == requestID {
			return r, nil
		}
	}
	return campaign.CampaignFinancialReview{}, campaign.ErrFinancialReviewNotFound
}

func (s *fakeCampaignStore) GetLatestFinancialReviewForProposal(ctx context.Context, organizationID string, proposalID int64) (campaign.CampaignFinancialReview, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var latest campaign.CampaignFinancialReview
	found := false
	for _, r := range s.financialReviews {
		if r.OrganizationID == organizationID && r.ProposalID == proposalID {
			if !found || r.ID > latest.ID {
				latest = r
				found = true
			}
		}
	}
	if !found {
		return campaign.CampaignFinancialReview{}, campaign.ErrFinancialReviewNotFound
	}
	return latest, nil
}

func (s *fakeCampaignStore) CreateRevision(ctx context.Context, cmd campaign.CreateRevisionCommand) (campaign.CampaignProposal, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	parent, found := s.byID[cmd.ParentProposalID]
	if !found || parent.OrganizationID != cmd.OrganizationID {
		return campaign.CampaignProposal{}, false, campaign.ErrProposalNotFound
	}

	rootID := cmd.ParentProposalID
	if parent.RootProposalID != nil {
		rootID = *parent.RootProposalID
	}

	lookupKey := cmd.OrganizationID + ":" + cmd.IdempotencyKey
	if existing, found := s.proposals[lookupKey]; found {
		if existing.CanonicalHash == cmd.CanonicalHash {
			return existing, true, nil
		}
		return campaign.CampaignProposal{}, false, fmt.Errorf("%w: hash mismatch", campaign.ErrIdempotencyConflict)
	}

	maxRev := 0
	for _, p := range s.byID {
		if p.OrganizationID == cmd.OrganizationID && p.RootProposalID != nil && *p.RootProposalID == rootID {
			if p.RevisionNumber > maxRev {
				maxRev = p.RevisionNumber
			}
		}
	}
	if maxRev > parent.RevisionNumber {
		return campaign.CampaignProposal{}, false, campaign.ErrStaleParentRevision
	}

	parentID := cmd.ParentProposalID
	rev := campaign.CampaignProposal{
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
		ParentProposalID:        &parentID,
		RevisionNumber:          parent.RevisionNumber + 1,
		RootProposalID:          &rootID,
		IdempotencyKey:          cmd.IdempotencyKey,
		CanonicalHash:           cmd.CanonicalHash,
		CreatedAt:               time.Now(),
		UpdatedAt:               time.Now(),
	}
	s.nextID++
	s.proposals[lookupKey] = rev
	s.byID[rev.ID] = rev
	return rev, false, nil
}

func (s *fakeCampaignStore) GetLatestRevisionForRoot(ctx context.Context, organizationID string, rootProposalID int64) (campaign.CampaignProposal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var latest campaign.CampaignProposal
	found := false
	for _, p := range s.byID {
		if p.OrganizationID == organizationID && p.RootProposalID != nil && *p.RootProposalID == rootProposalID {
			if !found || p.RevisionNumber > latest.RevisionNumber {
				latest = p
				found = true
			}
		}
	}
	if !found {
		return campaign.CampaignProposal{}, campaign.ErrProposalNotFound
	}
	return latest, nil
}

func (s *fakeCampaignStore) CreateOwnerApproval(ctx context.Context, cmd campaign.CreateOwnerApprovalCommand) (campaign.CampaignOwnerApproval, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	lookupKey := cmd.OrganizationID + ":" + cmd.IdempotencyKey
	if existing, found := s.approvalsByKey[lookupKey]; found {
		if existing.CanonicalHash == cmd.CanonicalHash {
			return existing, true, nil
		}
		return campaign.CampaignOwnerApproval{}, false, campaign.ErrApprovalConflict
	}

	tupleKey := fmt.Sprintf("%s:%s:%s", cmd.OrganizationID, cmd.ProposalCanonicalHash, cmd.FinancialReviewCanonicalHash)
	if existing, found := s.approvalsByTuple[tupleKey]; found {
		return existing, true, nil
	}

	appr := campaign.CampaignOwnerApproval{
		ID:                           s.nextID,
		OrganizationID:               cmd.OrganizationID,
		ProposalID:                   cmd.ProposalID,
		ProposalCanonicalHash:        cmd.ProposalCanonicalHash,
		FinancialReviewID:            cmd.FinancialReviewID,
		FinancialReviewCanonicalHash: cmd.FinancialReviewCanonicalHash,
		ApprovedByRoleID:             cmd.ApprovedByRoleID,
		ConversationID:               cmd.ConversationID,
		MessageID:                    cmd.MessageID,
		TurnTaskID:                   cmd.TurnTaskID,
		ToolCallID:                   cmd.ToolCallID,
		Status:                       campaign.StatusApprovedForExecution,
		ExecutionBudget:              cmd.ExecutionBudget,
		IdempotencyKey:               cmd.IdempotencyKey,
		CanonicalHash:                cmd.CanonicalHash,
		CreatedAt:                    time.Now(),
	}
	s.nextID++
	s.approvals[appr.ID] = appr
	s.approvalsByKey[lookupKey] = appr
	s.approvalsByTuple[tupleKey] = appr
	return appr, false, nil
}

func (s *fakeCampaignStore) GetOwnerApproval(ctx context.Context, organizationID string, approvalID int64) (campaign.CampaignOwnerApproval, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	a, found := s.approvals[approvalID]
	if !found || a.OrganizationID != organizationID {
		return campaign.CampaignOwnerApproval{}, campaign.ErrApprovalNotFound
	}
	return a, nil
}

func (s *fakeCampaignStore) GetOwnerApprovalByProposal(ctx context.Context, organizationID string, proposalID int64) (campaign.CampaignOwnerApproval, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var latest campaign.CampaignOwnerApproval
	found := false
	for _, a := range s.approvals {
		if a.OrganizationID == organizationID && a.ProposalID == proposalID {
			if !found || a.CreatedAt.After(latest.CreatedAt) {
				latest = a
				found = true
			}
		}
	}
	if !found {
		return campaign.CampaignOwnerApproval{}, campaign.ErrApprovalNotFound
	}
	return latest, nil
}

func (s *fakeCampaignStore) CreatePromotion(ctx context.Context, cmd campaign.CreatePromotionCommand) (campaign.CampaignPromotion, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	lookupKey := cmd.OrganizationID + ":" + cmd.IdempotencyKey
	if existing, found := s.promotionsByKey[lookupKey]; found {
		if existing.CanonicalHash == cmd.CanonicalHash {
			return existing, true, nil
		}
		return campaign.CampaignPromotion{}, false, fmt.Errorf("%w: hash mismatch", campaign.ErrIdempotencyConflict)
	}

	if existing, found := s.promotionsByAppr[cmd.OwnerApprovalID]; found {
		if existing.IdempotencyKey == cmd.IdempotencyKey && existing.CanonicalHash == cmd.CanonicalHash {
			return existing, true, nil
		}
		return campaign.CampaignPromotion{}, false, campaign.ErrPromotionAlreadyExists
	}

	prom := campaign.CampaignPromotion{
		ID:                            s.nextID,
		OrganizationID:                cmd.OrganizationID,
		OwnerApprovalID:               cmd.OwnerApprovalID,
		OwnerApprovalCanonicalHash:    cmd.OwnerApprovalCanonicalHash,
		ProposalID:                    cmd.ProposalID,
		ProposalCanonicalHash:         cmd.ProposalCanonicalHash,
		FinancialReviewID:             cmd.FinancialReviewID,
		FinancialReviewCanonicalHash:  cmd.FinancialReviewCanonicalHash,
		ExecutionBudget:               cmd.ExecutionBudget,
		ExecutionMode:                 cmd.ExecutionMode,
		ExecutiveRootTaskID:           cmd.ExecutiveRootTaskID,
		ExecutiveCorrelationID:        cmd.ExecutiveCorrelationID,
		ExecutiveSubmitIdempotencyKey: cmd.ExecutiveSubmitIdempotencyKey,
		Status:                        cmd.Status,
		PromotedByRoleID:              cmd.PromotedByRoleID,
		ConversationID:                cmd.ConversationID,
		MessageID:                     cmd.MessageID,
		TurnTaskID:                    cmd.TurnTaskID,
		ToolCallID:                    cmd.ToolCallID,
		IdempotencyKey:                cmd.IdempotencyKey,
		CanonicalHash:                 cmd.CanonicalHash,
		CreatedAt:                     time.Now(),
	}
	s.nextID++
	s.promotions[prom.ID] = prom
	s.promotionsByKey[lookupKey] = prom
	s.promotionsByAppr[prom.OwnerApprovalID] = prom
	return prom, false, nil
}

func (s *fakeCampaignStore) GetPromotion(ctx context.Context, organizationID string, id int64) (campaign.CampaignPromotion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, found := s.promotions[id]
	if !found || p.OrganizationID != organizationID {
		return campaign.CampaignPromotion{}, campaign.ErrPromotionNotFound
	}
	return p, nil
}

func (s *fakeCampaignStore) GetPromotionByApprovalID(ctx context.Context, organizationID string, approvalID int64) (campaign.CampaignPromotion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, found := s.promotionsByAppr[approvalID]
	if !found || p.OrganizationID != organizationID {
		return campaign.CampaignPromotion{}, campaign.ErrPromotionNotFound
	}
	return p, nil
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

type fakeTaskCoord struct {
	tasks  map[int64]tasks.Task
	nextID int64
}

func newFakeTaskCoord() *fakeTaskCoord {
	return &fakeTaskCoord{tasks: make(map[int64]tasks.Task), nextID: 100}
}

func (f *fakeTaskCoord) CreateTask(ctx context.Context, req tasks.CreateRequest, actorType, actorID string) (tasks.Task, bool, error) {
	f.nextID++
	t := tasks.Task{
		ID:             f.nextID,
		OrganizationID: req.OrganizationID,
		TaskClass:      req.TaskClass,
		AssignedRoleID: req.AssignedRoleID,
		Title:          req.Title,
		Instructions:   req.Instructions,
		Status:         tasks.StatusReady,
	}
	f.tasks[t.ID] = t
	return t, true, nil
}

func (f *fakeTaskCoord) ClaimTaskByID(ctx context.Context, taskID int64, req tasks.ClaimRequest) (tasks.ClaimedTask, error) {
	t := f.tasks[taskID]
	t.Status = tasks.StatusRunning
	f.tasks[taskID] = t
	return tasks.ClaimedTask{Task: t, Attempt: tasks.Attempt{ID: 1}, LeaseToken: "lease-1"}, nil
}

func (f *fakeTaskCoord) StartAttempt(ctx context.Context, cmd tasks.LeaseCommand) (tasks.Task, error) {
	return f.tasks[cmd.TaskID], nil
}

func (f *fakeTaskCoord) RecordAttemptResult(ctx context.Context, cmd tasks.RecordAttemptResultCommand) (tasks.Task, error) {
	t := f.tasks[cmd.TaskID]
	t.Status = tasks.StatusAwaitingVerification
	f.tasks[cmd.TaskID] = t
	return t, nil
}

func (f *fakeTaskCoord) FinalizeTask(ctx context.Context, cmd tasks.FinalizeCommand) (tasks.Task, error) {
	t := f.tasks[cmd.TaskID]
	t.Status = tasks.StatusCompleted
	f.tasks[cmd.TaskID] = t
	return t, nil
}

func (f *fakeTaskCoord) GetTask(ctx context.Context, taskID int64) (tasks.Task, error) {
	t, ok := f.tasks[taskID]
	if !ok {
		return tasks.Task{}, errors.New("task not found")
	}
	return t, nil
}

func TestCampaignFinancialReviewTools(t *testing.T) {
	store := newFakeCampaignStore()
	taskCoord := newFakeTaskCoord()
	auth := fakeAuthorizer{
		allowed: map[string]bool{
			"empresa/ceo:campaign.proposal.create":                               true,
			"empresa/ceo:campaign.proposal.read":                                 true,
			"empresa/ceo:campaign.financial_review.request":                      true,
			"empresa/ceo:campaign.financial_review.read":                         true,
			"negocio/administrador_financiero:campaign.financial_review.perform": true,
		},
	}

	finSvc, err := campaign.NewFinanceService(campaign.FinanceServiceConfig{
		OrganizationID: "org-test",
		Requirements:   permissiveExecutionRequirements(),
		Store:          store,
		Tasks:          taskCoord,
		Authorizer:     auth,
	})
	if err != nil {
		t.Fatalf("NewFinanceService: %v", err)
	}

	reg := NewToolRegistry()
	if err := RegisterCampaignTools(reg, "org-test", store, auth, WithFinanceService(finSvc)); err != nil {
		t.Fatalf("RegisterCampaignTools failed: %v", err)
	}

	executor := RegistryToolExecutor{Registry: reg}
	identity := executionharness.RunIdentity{RoleID: CEORoleID}

	// Create a proposal first
	prop, _, err := store.CreateProposal(context.Background(), campaign.CreateProposalCommand{
		OrganizationID:  "org-test",
		CreatedByRoleID: "empresa/ceo",
		CanonicalHash:   "hash-prop-1",
		Title:           "Proposal For Review",
		Goal:            "Review goal",
	})
	if err != nil {
		t.Fatalf("CreateProposal failed: %v", err)
	}

	turnCtx := TurnContext{
		OrganizationID: "org-test",
		ConversationID: 10,
		TaskID:         20,
		AttemptID:      1,
		ActorRoleID:    CEORoleID,
		OwnerMessageID: 100,
	}
	ctx := WithTurnContext(context.Background(), turnCtx)

	// RequestReview's own lineage validation requires a real, durable
	// parent task at turnCtx.TaskID -- seed one matching this turn
	// context's own actor/correlation exactly (RequestedByRoleID must
	// equal turnCtx.ActorRoleID, the same value the real tool handler
	// passes as RequestReviewParams.RequestedByRoleID).
	parentRequestedBy, parentCorrelation := CEORoleID, "corr:tools-campaign-test"
	taskCoord.tasks[turnCtx.TaskID] = tasks.Task{
		ID: turnCtx.TaskID, OrganizationID: turnCtx.OrganizationID,
		RequestedByRoleID: &parentRequestedBy, AssignedRoleID: CEORoleID,
		CorrelationID: &parentCorrelation,
	}

	// 1. Request financial review
	req := executionharness.ToolRequest{
		ToolName:   "campaign.request_financial_review",
		ToolCallID: "call_req_rev_1",
		Arguments:  json.RawMessage(fmt.Sprintf(`{"proposal_id": %d}`, prop.ID)),
	}

	res, err := executor.Execute(ctx, identity, req)
	if err != nil {
		t.Fatalf("campaign.request_financial_review failed: %v", err)
	}

	var reqProj RequestFinancialReviewResultProjection
	if err := json.Unmarshal(res.Content, &reqProj); err != nil {
		t.Fatalf("unmarshal request_financial_review result: %v", err)
	}
	if reqProj.ProposalID != prop.ID || reqProj.ReviewerRoleID != "negocio/administrador_financiero" {
		t.Fatalf("unexpected review request projection: %+v", reqProj)
	}
	if reqProj.Status != "pending" {
		t.Errorf("expected pending status, got %q", reqProj.Status)
	}

	// 2. Mock completed review in store
	_, _, err = store.RecordFinancialReview(context.Background(), campaign.RecordFinancialReviewCommand{
		OrganizationID:        "org-test",
		ReviewRequestID:       reqProj.ReviewRequestID,
		ProposalID:            prop.ID,
		ProposalCanonicalHash: prop.CanonicalHash,
		ReviewerRoleID:        "negocio/administrador_financiero",
		ReviewTaskID:          reqProj.ReviewTaskID,
		ReviewAttemptID:       1,
		Verdict:               campaign.VerdictRecommended,
		Summary:               "Financially sound within limits",
		CanonicalHash:         "hash-rev-1",
	})
	if err != nil {
		t.Fatalf("RecordFinancialReview failed: %v", err)
	}

	// 3. Read back financial review with campaign.get_financial_review
	getReq := executionharness.ToolRequest{
		ToolName:   "campaign.get_financial_review",
		ToolCallID: "call_get_rev_1",
		Arguments:  json.RawMessage(fmt.Sprintf(`{"proposal_id": %d}`, prop.ID)),
	}

	getRes, err := executor.Execute(ctx, identity, getReq)
	if err != nil {
		t.Fatalf("campaign.get_financial_review failed: %v", err)
	}

	var revProj FinancialReviewResultProjection
	if err := json.Unmarshal(getRes.Content, &revProj); err != nil {
		t.Fatalf("unmarshal get_financial_review result: %v", err)
	}
	if revProj.Verdict != string(campaign.VerdictRecommended) || revProj.ProposalID != prop.ID {
		t.Errorf("unexpected financial review projection: %+v", revProj)
	}
}

func TestCampaignReviseProposalAndOwnerApprovalTools(t *testing.T) {
	ctx := context.Background()
	store := newFakeCampaignStore()
	auth := fakeAuthorizer{
		allowed: map[string]bool{
			"owner:campaign.proposal.create":             true,
			"owner:campaign.proposal.read":               true,
			"owner:campaign.proposal.revise":             true,
			"owner:campaign.owner_approval.create":       true,
			"owner:campaign.owner_approval.read":         true,
			"empresa/ceo:campaign.proposal.revise":       true,
			"empresa/ceo:campaign.owner_approval.read":   true,
			"empresa/ceo:campaign.financial_review.read": true,
		},
	}
	reg := NewToolRegistry()
	if err := RegisterCampaignTools(reg, "org-test", store, auth); err != nil {
		t.Fatalf("RegisterCampaignTools failed: %v", err)
	}
	executor := RegistryToolExecutor{Registry: reg}

	turnCtx := TurnContext{
		OrganizationID:         "org-test",
		OrganizationRevisionID: 1,
		ConversationID:         100,
		OwnerRoleID:            "owner",
		OwnerMessageID:         200,
		TaskID:                 300,
		AttemptID:              1,
		ActorRoleID:            "empresa/ceo",
	}
	turnCtxBg := WithTurnContext(ctx, turnCtx)
	identity := executionharness.RunIdentity{
		OrganizationID: "org-test",
		RoleID:         CEORoleID,
		TaskID:         300,
		AttemptID:      1,
	}

	// 1. Initial proposal
	p1Hash, _ := campaign.ComputeCanonicalHash(campaign.CanonicalPayload{
		Title: "Initial Title",
		Goal:  "Initial Goal",
	})
	p1, _, err := store.CreateProposal(ctx, campaign.CreateProposalCommand{
		OrganizationID: "org-test",
		Title:          "Initial Title",
		Goal:           "Initial Goal",
		IdempotencyKey: "k-p1",
		CanonicalHash:  p1Hash,
	})
	if err != nil {
		t.Fatalf("create initial proposal: %v", err)
	}

	// 2. Revise proposal using campaign.revise_proposal tool
	revisePayload := json.RawMessage(fmt.Sprintf(`{
		"proposal_id": %d,
		"title": "Revised Title",
		"goal": "Revised Goal",
		"acceptance_criteria": ["Criteria 1", "Criteria 2"]
	}`, p1.ID))

	resRev, err := executor.Execute(turnCtxBg, identity, executionharness.ToolRequest{
		ToolName:   "campaign.revise_proposal",
		ToolCallID: "call_rev_1",
		Arguments:  revisePayload,
	})
	if err != nil {
		t.Fatalf("campaign.revise_proposal failed: %v", err)
	}

	var revProj ReviseProposalResultProjection
	if err := json.Unmarshal(resRev.Content, &revProj); err != nil {
		t.Fatalf("unmarshal revision result: %v", err)
	}
	if revProj.ParentProposalID != p1.ID || revProj.RevisionNumber != 2 {
		t.Fatalf("unexpected revision projection: %+v", revProj)
	}

	// 3. Record recommended review for v2
	r2Hash, _ := campaign.ComputeReviewCanonicalHash(campaign.ReviewCanonicalPayload{
		ProposalID:            revProj.ProposalID,
		ProposalCanonicalHash: revProj.CanonicalHash,
		ReviewerRoleID:        "empresa/finanzas",
		Verdict:               campaign.VerdictRecommended,
	})
	recBudget := &campaign.BudgetRecommendation{
		MaxUSD:        12000,
		MaxTokens:     300000,
		MaxModelCalls: 120,
		MaxWallTimeMS: 86400000,
		MaxDepth:      7,
		MaxRetries:    5,
		MaxSubagents:  4,
	}
	rev2, _, err := store.RecordFinancialReview(ctx, campaign.RecordFinancialReviewCommand{
		OrganizationID:        "org-test",
		ReviewRequestID:       555,
		ProposalID:            revProj.ProposalID,
		ProposalCanonicalHash: revProj.CanonicalHash,
		ReviewerRoleID:        "empresa/finanzas",
		Verdict:               campaign.VerdictRecommended,
		RecommendedBudget:     recBudget,
		CanonicalHash:         r2Hash,
	})
	if err != nil {
		t.Fatalf("record review v2: %v", err)
	}

	// 4. The CEO can PREPARE an approval, never make one: the tool returns the
	// exact command for the owner and writes nothing.
	prepPayload := json.RawMessage(fmt.Sprintf(`{
		"proposal_id": %d,
		"financial_review_id": %d
	}`, revProj.ProposalID, rev2.ID))
	resPrep, err := executor.Execute(turnCtxBg, identity, executionharness.ToolRequest{
		ToolName:   "campaign.prepare_owner_approval",
		ToolCallID: "call_prep_1",
		Arguments:  prepPayload,
	})
	if err != nil {
		t.Fatalf("campaign.prepare_owner_approval failed: %v", err)
	}
	var prepProj PrepareOwnerApprovalProjection
	if err := json.Unmarshal(resPrep.Content, &prepProj); err != nil {
		t.Fatalf("unmarshal prepare result: %v", err)
	}
	wantCommand := fmt.Sprintf("orgctl campaign approve --proposal %d --review %d", revProj.ProposalID, rev2.ID)
	if !prepProj.OwnerActionRequired || prepProj.Command != wantCommand || prepProj.RecommendedBudget.MaxUSD != recBudget.MaxUSD {
		t.Fatalf("unexpected prepare projection: %+v (want command %q)", prepProj, wantCommand)
	}
	if _, err := store.GetOwnerApprovalByProposal(ctx, "org-test", revProj.ProposalID); err == nil {
		t.Fatal("campaign.prepare_owner_approval created an approval; only the owner can")
	}

	// The approval itself is the owner's act.
	result := approveAsOwner(t, store, auth, revProj.ProposalID, rev2.ID)
	apprProj := OwnerApprovalResultProjection{ApprovalID: result.ApprovalID, ProposalID: result.ProposalID, FinancialReviewID: result.FinancialReviewID,
		ApprovedByRoleID: result.ActorRoleID, ExecutionBudget: result.ExecutionBudget}
	if apprProj.ProposalID != revProj.ProposalID || apprProj.FinancialReviewID != rev2.ID {
		t.Fatalf("unexpected approval projection: %+v", apprProj)
	}
	if apprProj.ApprovedByRoleID != "owner" {
		t.Fatalf("expected approved by owner, got %s", apprProj.ApprovedByRoleID)
	}
	if apprProj.ExecutionBudget.MaxUSD != recBudget.MaxUSD {
		t.Fatalf("budget mismatch: got %v want %v", apprProj.ExecutionBudget.MaxUSD, recBudget.MaxUSD)
	}

	// 5. Read approval with campaign.get_owner_approval
	getApprPayload := json.RawMessage(fmt.Sprintf(`{"approval_id": %d}`, apprProj.ApprovalID))
	resGetAppr, err := executor.Execute(turnCtxBg, identity, executionharness.ToolRequest{
		ToolName:   "campaign.get_owner_approval",
		ToolCallID: "call_get_appr_1",
		Arguments:  getApprPayload,
	})
	if err != nil {
		t.Fatalf("campaign.get_owner_approval failed: %v", err)
	}
	var getApprProj OwnerApprovalResultProjection
	if err := json.Unmarshal(resGetAppr.Content, &getApprProj); err != nil {
		t.Fatalf("unmarshal get approval result: %v", err)
	}
	if getApprProj.ApprovalID != apprProj.ApprovalID {
		t.Fatalf("expected approval ID %d, got %d", apprProj.ApprovalID, getApprProj.ApprovalID)
	}
}

type fakeSubmitter struct {
	mu           sync.Mutex
	submitCalls  int
	resumeCalls  int
	lastRequest  executive.SubmitRequest
	returnRun    executive.Run
	returnReused bool
	returnErr    error
}

func (f *fakeSubmitter) Submit(_ context.Context, req executive.SubmitRequest) (executive.Run, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.submitCalls++
	f.lastRequest = req
	if f.returnErr != nil {
		return executive.Run{}, false, f.returnErr
	}
	run := f.returnRun
	if run.RootTaskID == 0 {
		run = executive.Run{
			RootTaskID:    777,
			CorrelationID: "executive:ceochat-test-corr",
			State:         executive.StateAccepted,
		}
	}
	return run, f.returnReused, nil
}

func TestCampaignPromoteToExecutiveTools(t *testing.T) {
	ctx := context.Background()
	store := newFakeCampaignStore()
	submitter := &fakeSubmitter{}
	auth := fakeAuthorizer{
		allowed: map[string]bool{
			"owner:campaign.proposal.create":       true,
			"owner:campaign.proposal.read":         true,
			"owner:campaign.owner_approval.create": true,
			"owner:campaign.owner_approval.read":   true,
			"owner:campaign.promotion.execute":     true,
			"owner:campaign.promotion.read":        true,
			"empresa/ceo:campaign.promotion.read":  true,
		},
	}
	promSvc := campaign.NewPromotionService(store, submitter, auth, permissiveExecutionRequirements())
	reg := NewToolRegistry()
	if err := RegisterCampaignTools(reg, "org-test", store, auth, WithPromotionService(promSvc)); err != nil {
		t.Fatalf("RegisterCampaignTools failed: %v", err)
	}
	executor := RegistryToolExecutor{Registry: reg}

	turnCtx := TurnContext{
		OrganizationID:         "org-test",
		OrganizationRevisionID: 1,
		ConversationID:         100,
		OwnerRoleID:            "owner",
		OwnerMessageID:         200,
		TaskID:                 300,
		AttemptID:              1,
		ActorRoleID:            "empresa/ceo",
	}
	turnCtxBg := WithTurnContext(ctx, turnCtx)
	identity := executionharness.RunIdentity{
		OrganizationID: "org-test",
		RoleID:         CEORoleID,
		TaskID:         300,
		AttemptID:      1,
	}

	// 1. Initial proposal
	pPayload := campaign.CanonicalPayload{
		Title:              "Summer Promotion Campaign",
		Goal:               "Scale user acquisition by 20%",
		AcceptanceCriteria: []string{"CAC < $50", "ROI > 1.5"},
	}
	pHash, _ := campaign.ComputeCanonicalHash(pPayload)
	p, _, err := store.CreateProposal(ctx, campaign.CreateProposalCommand{
		OrganizationID:     "org-test",
		Title:              pPayload.Title,
		Goal:               pPayload.Goal,
		AcceptanceCriteria: pPayload.AcceptanceCriteria,
		CanonicalHash:      pHash,
		IdempotencyKey:     "prop-promo-1",
		CreatedByRoleID:    "empresa/ceo",
	})
	if err != nil {
		t.Fatalf("create proposal: %v", err)
	}

	// 2. Financial review (recommended)
	recBudget := campaign.BudgetRecommendation{
		MaxUSD:        5000.0,
		MaxTokens:     100000,
		MaxModelCalls: 50,
		MaxWallTimeMS: 300000,
		MaxDepth:      5,
		MaxRetries:    3,
		MaxSubagents:  4,
	}
	rHash, _ := campaign.ComputeReviewCanonicalHash(campaign.ReviewCanonicalPayload{
		ProposalID:            p.ID,
		ProposalCanonicalHash: pHash,
		ReviewerRoleID:        "empresa/finanzas",
		Verdict:               campaign.VerdictRecommended,
		RecommendedBudget:     &recBudget,
	})
	rev, _, err := store.RecordFinancialReview(ctx, campaign.RecordFinancialReviewCommand{
		OrganizationID:        "org-test",
		ReviewRequestID:       101,
		ProposalID:            p.ID,
		ProposalCanonicalHash: pHash,
		ReviewerRoleID:        "empresa/finanzas",
		Verdict:               campaign.VerdictRecommended,
		RecommendedBudget:     &recBudget,
		CanonicalHash:         rHash,
	})
	if err != nil {
		t.Fatalf("record financial review: %v", err)
	}

	// 3. Owner approval
	apprHash, _ := campaign.ComputeApprovalCanonicalHash(campaign.ApprovalCanonicalPayload{
		OrganizationID:               "org-test",
		ProposalID:                   p.ID,
		ProposalCanonicalHash:        pHash,
		FinancialReviewID:            rev.ID,
		FinancialReviewCanonicalHash: rHash,
		ApprovedByRoleID:             "owner",
		ExecutionBudget:              recBudget,
	})
	appr, _, err := store.CreateOwnerApproval(ctx, campaign.CreateOwnerApprovalCommand{
		OrganizationID:               "org-test",
		ProposalID:                   p.ID,
		ProposalCanonicalHash:        pHash,
		FinancialReviewID:            rev.ID,
		FinancialReviewCanonicalHash: rHash,
		ApprovedByRoleID:             "owner",
		ConversationID:               100,
		MessageID:                    200,
		TurnTaskID:                   300,
		ToolCallID:                   "call_appr_1",
		ExecutionBudget:              recBudget,
		CanonicalHash:                apprHash,
		IdempotencyKey:               "appr-promo-1",
	})
	if err != nil {
		t.Fatalf("create owner approval: %v", err)
	}

	// 4. Promote with unauthorized role context -> must fail
	unauthTurnCtx := WithTurnContext(ctx, TurnContext{
		OrganizationID:         "org-test",
		OrganizationRevisionID: 1,
		ConversationID:         100,
		OwnerRoleID:            "unauthorized_role",
		OwnerMessageID:         200,
		TaskID:                 300,
		AttemptID:              1,
		ActorRoleID:            "empresa/ceo",
	})
	promotePayload := json.RawMessage(fmt.Sprintf(`{"owner_approval_id": %d}`, appr.ID))
	_, err = executor.Execute(unauthTurnCtx, identity, executionharness.ToolRequest{
		ToolName:   "campaign.promote_to_executive",
		ToolCallID: "call_promo_unauth",
		Arguments:  promotePayload,
	})
	if err == nil {
		t.Fatalf("expected error for unauthorized role, got nil")
	}

	// 5. Promote with non-existent approval ID -> must fail
	badApprPayload := json.RawMessage(`{"owner_approval_id": 99999}`)
	_, err = executor.Execute(turnCtxBg, identity, executionharness.ToolRequest{
		ToolName:   "campaign.promote_to_executive",
		ToolCallID: "call_promo_notfound",
		Arguments:  badApprPayload,
	})
	if err == nil {
		t.Fatalf("expected error for nonexistent approval, got nil")
	}

	// 6. Valid promotion
	resPromo, err := executor.Execute(turnCtxBg, identity, executionharness.ToolRequest{
		ToolName:   "campaign.promote_to_executive",
		ToolCallID: "call_promo_valid",
		Arguments:  promotePayload,
	})
	if err != nil {
		t.Fatalf("valid promote_to_executive failed: %v", err)
	}

	var promoProj PromoteResultProjection
	if err := json.Unmarshal(resPromo.Content, &promoProj); err != nil {
		t.Fatalf("unmarshal promotion projection: %v", err)
	}
	if promoProj.PromotionID == 0 {
		t.Errorf("expected non-zero promotion ID, got 0")
	}
	if promoProj.Status != "submitted" {
		t.Errorf("status = %q, want submitted", promoProj.Status)
	}
	if promoProj.ExecutiveRootTaskID != 777 {
		t.Errorf("root task ID = %d, want 777", promoProj.ExecutiveRootTaskID)
	}
	if promoProj.ExecutiveCorrelationID != "executive:ceochat-test-corr" {
		t.Errorf("correlation ID = %q, want executive:ceochat-test-corr", promoProj.ExecutiveCorrelationID)
	}
	if promoProj.Reused {
		t.Errorf("expected reused=false on first promotion, got true")
	}

	// Verify submitter was invoked exactly once with pinned budget
	if submitter.submitCalls != 1 {
		t.Errorf("submit calls = %d, want 1", submitter.submitCalls)
	}
	if submitter.resumeCalls != 0 {
		t.Errorf("resume calls = %d, want 0", submitter.resumeCalls)
	}
	if submitter.lastRequest.Budget.MaxTokens != recBudget.MaxTokens {
		t.Errorf("budget tokens = %d, want %d", submitter.lastRequest.Budget.MaxTokens, recBudget.MaxTokens)
	}
	if submitter.lastRequest.Budget.MaxModelCalls != int64(recBudget.MaxModelCalls) {
		t.Errorf("budget model calls = %d, want %d", submitter.lastRequest.Budget.MaxModelCalls, recBudget.MaxModelCalls)
	}
	if submitter.lastRequest.ActorRoleID != executive.OwnerRoleID {
		t.Errorf("actor role = %q, want %q", submitter.lastRequest.ActorRoleID, executive.OwnerRoleID)
	}

	// 7. Idempotent promotion replay with same approval
	resPromoRetry, err := executor.Execute(turnCtxBg, identity, executionharness.ToolRequest{
		ToolName:   "campaign.promote_to_executive",
		ToolCallID: "call_promo_retry",
		Arguments:  promotePayload,
	})
	if err != nil {
		t.Fatalf("retry promote_to_executive failed: %v", err)
	}
	var promoRetryProj PromoteResultProjection
	if err := json.Unmarshal(resPromoRetry.Content, &promoRetryProj); err != nil {
		t.Fatalf("unmarshal retry promotion projection: %v", err)
	}
	if promoRetryProj.PromotionID != promoProj.PromotionID {
		t.Errorf("retry promotion ID = %d, want %d", promoRetryProj.PromotionID, promoProj.PromotionID)
	}
	if promoRetryProj.ExecutiveRootTaskID != 777 {
		t.Errorf("retry root task ID = %d, want 777", promoRetryProj.ExecutiveRootTaskID)
	}
	if !promoRetryProj.Reused {
		t.Errorf("expected reused=true on replay, got false")
	}

	// 8. Read promotion using campaign.get_promotion by promotion_id
	getPromoPayload := json.RawMessage(fmt.Sprintf(`{"promotion_id": %d}`, promoProj.PromotionID))
	resGetPromo, err := executor.Execute(turnCtxBg, identity, executionharness.ToolRequest{
		ToolName:   "campaign.get_promotion",
		ToolCallID: "call_get_promo_1",
		Arguments:  getPromoPayload,
	})
	if err != nil {
		t.Fatalf("campaign.get_promotion by promotion_id failed: %v", err)
	}
	var getPromoProj PromotionResultProjection
	if err := json.Unmarshal(resGetPromo.Content, &getPromoProj); err != nil {
		t.Fatalf("unmarshal get promotion: %v", err)
	}
	if getPromoProj.PromotionID != promoProj.PromotionID {
		t.Errorf("get promotion ID = %d, want %d", getPromoProj.PromotionID, promoProj.PromotionID)
	}
	if getPromoProj.ExecutiveRootTaskID != 777 {
		t.Errorf("get root task ID = %d, want 777", getPromoProj.ExecutiveRootTaskID)
	}

	// 9. Read promotion using campaign.get_promotion by owner_approval_id
	getPromoApprPayload := json.RawMessage(fmt.Sprintf(`{"owner_approval_id": %d}`, appr.ID))
	resGetPromoAppr, err := executor.Execute(turnCtxBg, identity, executionharness.ToolRequest{
		ToolName:   "campaign.get_promotion",
		ToolCallID: "call_get_promo_2",
		Arguments:  getPromoApprPayload,
	})
	if err != nil {
		t.Fatalf("campaign.get_promotion by owner_approval_id failed: %v", err)
	}
	var getPromoApprProj PromotionResultProjection
	if err := json.Unmarshal(resGetPromoAppr.Content, &getPromoApprProj); err != nil {
		t.Fatalf("unmarshal get promotion by approval: %v", err)
	}
	if getPromoApprProj.PromotionID != promoProj.PromotionID {
		t.Errorf("get promotion by appr ID = %d, want %d", getPromoApprProj.PromotionID, promoProj.PromotionID)
	}
}
