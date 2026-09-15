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
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

type fakeCampaignStore struct {
	mu               sync.Mutex
	proposals        map[string]campaign.CampaignProposal // org:key -> proposal
	byID             map[int64]campaign.CampaignProposal
	reviewRequests   map[int64]campaign.CampaignFinancialReviewRequest
	financialReviews map[int64]campaign.CampaignFinancialReview
	nextID           int64
}

func newFakeCampaignStore() *fakeCampaignStore {
	return &fakeCampaignStore{
		proposals:        make(map[string]campaign.CampaignProposal),
		byID:             make(map[int64]campaign.CampaignProposal),
		reviewRequests:   make(map[int64]campaign.CampaignFinancialReviewRequest),
		financialReviews: make(map[int64]campaign.CampaignFinancialReview),
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
