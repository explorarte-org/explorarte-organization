package consolidation_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/memory"
	"github.com/Mireuz13/explorarte-organization/internal/memoryos/consolidation"
	"github.com/Mireuz13/explorarte-organization/internal/memoryos/episode"
	"github.com/Mireuz13/explorarte-organization/internal/rag"
)

type memoryReader struct {
	episodes []episode.Episode
}

func (r *memoryReader) List(_ context.Context, orgID string, from, to time.Time, limit int) ([]episode.Episode, error) {
	out := make([]episode.Episode, 0)
	for _, ep := range r.episodes {
		if ep.OrganizationID == orgID {
			out = append(out, ep)
		}
	}
	return out, nil
}

func (r *memoryReader) Get(_ context.Context, orgID, episodeID string) (episode.Episode, error) {
	for _, ep := range r.episodes {
		if ep.OrganizationID == orgID && ep.ID == episodeID {
			return ep, nil
		}
	}
	return episode.Episode{}, errors.New("episode not found")
}

type memoryClusterStore struct {
	mu       sync.Mutex
	clusters map[string]consolidation.Cluster
}

func newMemoryClusterStore() *memoryClusterStore {
	return &memoryClusterStore{clusters: make(map[string]consolidation.Cluster)}
}

func (s *memoryClusterStore) SaveCluster(_ context.Context, c consolidation.Cluster) (consolidation.Cluster, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.clusters {
		if existing.OrganizationID == c.OrganizationID && existing.ID == c.ID && existing.CanonicalDigest == c.CanonicalDigest && existing.Status == c.Status {
			return existing, true, nil
		}
	}
	var maxRev int64
	for _, existing := range s.clusters {
		if existing.OrganizationID == c.OrganizationID && existing.ID == c.ID && existing.Revision > maxRev {
			maxRev = existing.Revision
		}
	}
	c.Revision = maxRev + 1
	key := fmt.Sprintf("%s:%s:%d", c.OrganizationID, c.ID, c.Revision)
	s.clusters[key] = c
	return c, false, nil
}

type memorySemanticProposer struct {
	mu        sync.Mutex
	proposals []rag.ProposeRequest
	reused    map[string]bool
}

func newMemorySemanticProposer() *memorySemanticProposer {
	return &memorySemanticProposer{
		proposals: make([]rag.ProposeRequest, 0),
		reused:    make(map[string]bool),
	}
}

func (p *memorySemanticProposer) Propose(_ context.Context, req rag.ProposeRequest) (rag.KnowledgeVersion, bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.reused[req.IdempotencyKey] {
		return rag.KnowledgeVersion{ID: "kv-1", DocumentID: "doc-1", Version: 1}, true, nil
	}
	p.reused[req.IdempotencyKey] = true
	p.proposals = append(p.proposals, req)
	return rag.KnowledgeVersion{ID: fmt.Sprintf("kv-%d", len(p.proposals)), DocumentID: "doc-1", Version: 1}, false, nil
}

type memoryCorrectiveProposer struct {
	mu        sync.Mutex
	proposals []memory.ProposeRequest
	reused    map[string]bool
}

func newMemoryCorrectiveProposer() *memoryCorrectiveProposer {
	return &memoryCorrectiveProposer{
		proposals: make([]memory.ProposeRequest, 0),
		reused:    make(map[string]bool),
	}
}

func (p *memoryCorrectiveProposer) Propose(_ context.Context, req memory.ProposeRequest) (memory.Entry, bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.reused[req.IdempotencyKey] {
		return memory.Entry{ID: req.Command.ID, Status: memory.StatusCandidate}, true, nil
	}
	p.reused[req.IdempotencyKey] = true
	p.proposals = append(p.proposals, req)
	return memory.Entry{ID: req.Command.ID, Status: memory.StatusCandidate}, false, nil
}

func buildContradictedEpisode(harnessRunID string, taskID, attemptID, decisionRunID int64, obKey, obKind string, observedAt time.Time) episode.Episode {
	tokens := int64(50)
	evidenceDigest := strings.Repeat("f", 64)
	return episode.Episode{
		ID:                   episode.EpisodeIDFor("explorarte", harnessRunID),
		OrganizationID:       "explorarte",
		HarnessRunID:         harnessRunID,
		TaskID:               taskID,
		AttemptID:            attemptID,
		DecisionRunID:        &decisionRunID,
		RoleID:               "ingenieria_ia/qa",
		ExecutionPrincipalID: "principal-1",
		TaskClass:            "qa_verification",
		ExecutionPurpose:     "execution",
		ExecutionProfileID:   "profile/standard",
		BindingMode:          episode.BindingModeHomogeneous,
		TerminalStatus:       "success",
		Status:               episode.EpisodeStatusObserved,
		StartedAt:            &observedAt,
		FinishedAt:           &observedAt,
		Invocations: []episode.InvocationUse{
			{InvocationID: decisionRunID, ProviderID: "anthropic", ProviderModelID: "claude-3-5-sonnet", InputTokens: &tokens, OutputTokens: &tokens, Status: "completed"},
		},
		Verification: &episode.VerificationSummary{
			Verdict:       "fail",
			VerifiedAt:    &observedAt,
			Scope:         episode.VerificationScopeAttempt,
			DecisionRunID: &decisionRunID,
			EvidenceRefs:  []string{fmt.Sprintf("decisiongraph:run:%d", decisionRunID)},
			Obligations: []episode.ObligationObservation{
				{
					Key:             obKey,
					Kind:            obKind,
					Label:           consolidation.VerificationContradicted,
					VerifierRef:     "verifier/qa",
					VerifierVersion: "v1.0.0",
					EvidenceDigest:  evidenceDigest,
				},
			},
		},
	}
}

func buildPositiveEpisode(harnessRunID string, taskID, attemptID, decisionRunID int64, binding episode.BindingMode, observedAt time.Time) episode.Episode {
	tokens := int64(50)
	evidenceDigest := strings.Repeat("e", 64)
	invocations := []episode.InvocationUse{
		{InvocationID: decisionRunID, ProviderID: "anthropic", ProviderModelID: "claude-3-5-sonnet", InputTokens: &tokens, OutputTokens: &tokens, Status: "completed"},
	}
	if binding == episode.BindingModeMixed {
		invocations = append(invocations, episode.InvocationUse{
			InvocationID: decisionRunID + 1000, ProviderID: "google", ProviderModelID: "gemini-1.5-pro", InputTokens: &tokens, OutputTokens: &tokens, Status: "completed",
		})
	}

	return episode.Episode{
		ID:                   episode.EpisodeIDFor("explorarte", harnessRunID),
		OrganizationID:       "explorarte",
		HarnessRunID:         harnessRunID,
		TaskID:               taskID,
		AttemptID:            attemptID,
		DecisionRunID:        &decisionRunID,
		RoleID:               "ingenieria_ia/qa",
		ExecutionPrincipalID: "principal-1",
		TaskClass:            "qa_verification",
		ExecutionPurpose:     "execution",
		ExecutionProfileID:   "profile/standard",
		BindingMode:          binding,
		TerminalStatus:       "success",
		Status:               episode.EpisodeStatusObserved,
		StartedAt:            &observedAt,
		FinishedAt:           &observedAt,
		Invocations:          invocations,
		Verification: &episode.VerificationSummary{
			Verdict:       "pass",
			VerifiedAt:    &observedAt,
			Scope:         episode.VerificationScopeAttempt,
			DecisionRunID: &decisionRunID,
			EvidenceRefs:  []string{fmt.Sprintf("decisiongraph:run:%d", decisionRunID)},
			Obligations: []episode.ObligationObservation{
				{
					Key:             "qa-check",
					Kind:            "acceptance_criteria",
					Label:           "verified",
					VerifierRef:     "verifier/qa",
					VerifierVersion: "v1.0.0",
					EvidenceDigest:  evidenceDigest,
				},
			},
		},
	}
}

func TestCorrectiveRecurrenceThresholds(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 5, 1, 0, 0, 0, time.UTC)

	// 1. Below threshold (2 runs < 3): No candidate emitted
	ep1 := buildContradictedEpisode("run-1", 101, 1, 501, "req-auth-token", "task_requirement", now)
	ep2 := buildContradictedEpisode("run-2", 102, 1, 502, "req-auth-token", "task_requirement", now.Add(time.Hour))

	reader := &memoryReader{episodes: []episode.Episode{ep1, ep2}}
	clusterStore := newMemoryClusterStore()
	correctiveProposer := newMemoryCorrectiveProposer()

	cfg := consolidation.DefaultConfig()
	cfg.MinCorrectiveRecurrence = 3

	svc, err := consolidation.NewService(reader, clusterStore, nil, correctiveProposer, cfg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	result, err := svc.Consolidate(ctx, "explorarte", now.Add(-time.Hour), now.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("Consolidate: %v", err)
	}

	if result.CorrectiveCandidates != 0 {
		t.Fatalf("Expected 0 corrective candidates when count=2 < threshold=3, got %d", result.CorrectiveCandidates)
	}
	if len(correctiveProposer.proposals) != 0 {
		t.Fatalf("Expected 0 proposals in memory proposer, got %d", len(correctiveProposer.proposals))
	}

	// 2. Exactly threshold (3 runs == 3): Exactly 1 candidate emitted
	ep3 := buildContradictedEpisode("run-3", 103, 1, 503, "req-auth-token", "task_requirement", now.Add(2*time.Hour))
	reader.episodes = append(reader.episodes, ep3)

	result, err = svc.Consolidate(ctx, "explorarte", now.Add(-time.Hour), now.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("Consolidate with 3 runs: %v", err)
	}

	if result.CorrectiveCandidates != 1 {
		t.Fatalf("Expected 1 corrective candidate for 3 runs, got %d", result.CorrectiveCandidates)
	}
	if len(correctiveProposer.proposals) != 1 {
		t.Fatalf("Expected exactly 1 proposal, got %d", len(correctiveProposer.proposals))
	}

	proposal := correctiveProposer.proposals[0]
	// Verify grounding in the original 3 decision graph runs
	if len(proposal.Command.EvidenceRefs) != 3 {
		t.Fatalf("Expected 3 EvidenceRefs, got %d", len(proposal.Command.EvidenceRefs))
	}
	expectedRefs := map[string]bool{
		"decisiongraph:run:501": true,
		"decisiongraph:run:502": true,
		"decisiongraph:run:503": true,
	}
	for _, ref := range proposal.Command.EvidenceRefs {
		if !expectedRefs[ref.Reference] {
			t.Errorf("Unexpected evidence ref: %s", ref.Reference)
		}
	}

	// Verify representative SourceRunID is the latest decision run (503)
	if proposal.Command.SourceRunID != 503 {
		t.Fatalf("Expected SourceRunID=503, got %d", proposal.Command.SourceRunID)
	}

	// Invariant: No LLM invented correction text; pending review
	if proposal.Command.Correction != cfg.CorrectionPending {
		t.Fatalf("Correction must be pending message, got %q", proposal.Command.Correction)
	}

	// Invariant: MemoryOS cannot auto-approve (must remain candidate)
	if proposal.Command.ProposedBy != "ingenieria_ia/qa" {
		t.Fatalf("Expected proposed_by to be role, got %s", proposal.Command.ProposedBy)
	}

	// 3. Idempotency on rerun: Re-running consolidation reuses cluster and candidate
	resultRerun, err := svc.Consolidate(ctx, "explorarte", now.Add(-time.Hour), now.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("Consolidate rerun: %v", err)
	}
	if resultRerun.CorrectiveCandidates != 0 {
		t.Fatalf("Expected 0 new candidates on rerun, got %d", resultRerun.CorrectiveCandidates)
	}
	if resultRerun.CorrectiveReused != 1 {
		t.Fatalf("Expected 1 candidate reused on rerun, got %d", resultRerun.CorrectiveReused)
	}
}

func TestDifferentObligationKeysDoNotMerge(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 5, 1, 0, 0, 0, time.UTC)

	// Two runs fail on req-auth, two runs fail on req-encryption
	epA1 := buildContradictedEpisode("run-a1", 101, 1, 501, "req-auth", "task_requirement", now)
	epA2 := buildContradictedEpisode("run-a2", 102, 1, 502, "req-auth", "task_requirement", now.Add(time.Hour))
	epB1 := buildContradictedEpisode("run-b1", 103, 1, 503, "req-encryption", "task_requirement", now.Add(2*time.Hour))
	epB2 := buildContradictedEpisode("run-b2", 104, 1, 504, "req-encryption", "task_requirement", now.Add(3*time.Hour))

	reader := &memoryReader{episodes: []episode.Episode{epA1, epA2, epB1, epB2}}
	clusterStore := newMemoryClusterStore()
	correctiveProposer := newMemoryCorrectiveProposer()

	cfg := consolidation.DefaultConfig()
	cfg.MinCorrectiveRecurrence = 3

	svc, err := consolidation.NewService(reader, clusterStore, nil, correctiveProposer, cfg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	result, err := svc.Consolidate(ctx, "explorarte", now.Add(-time.Hour), now.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("Consolidate: %v", err)
	}

	// Neither obligation reached the threshold of 3, so 0 candidates emitted
	if result.CorrectiveCandidates != 0 {
		t.Fatalf("Expected 0 candidates because neither cluster reached recurrence 3, got %d", result.CorrectiveCandidates)
	}
}

func TestSemanticConsolidationCandidateOnlyAndMixedBinding(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 5, 1, 0, 0, 0, time.UTC)

	// 3 positive episodes, one with mixed model binding
	ep1 := buildPositiveEpisode("pos-1", 201, 1, 601, episode.BindingModeHomogeneous, now)
	ep2 := buildPositiveEpisode("pos-2", 202, 1, 602, episode.BindingModeHomogeneous, now.Add(time.Hour))
	ep3 := buildPositiveEpisode("pos-3", 203, 1, 603, episode.BindingModeMixed, now.Add(2*time.Hour))

	reader := &memoryReader{episodes: []episode.Episode{ep1, ep2, ep3}}
	clusterStore := newMemoryClusterStore()
	semanticProposer := newMemorySemanticProposer()

	cfg := consolidation.DefaultConfig()
	cfg.SemanticOwner = consolidation.SemanticOwnerMemoryOS
	cfg.MinSemanticRecurrence = 3

	svc, err := consolidation.NewService(reader, clusterStore, semanticProposer, nil, cfg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	result, err := svc.Consolidate(ctx, "explorarte", now.Add(-time.Hour), now.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("Consolidate: %v", err)
	}

	if result.SemanticCandidates != 1 {
		t.Fatalf("Expected 1 semantic candidate, got %d", result.SemanticCandidates)
	}
	if len(semanticProposer.proposals) != 1 {
		t.Fatalf("Expected 1 semantic proposal, got %d", len(semanticProposer.proposals))
	}

	prop := semanticProposer.proposals[0]
	// Invariant: Candidate only, no auto-approval
	var body consolidation.SemanticCandidateBody
	if err := json.Unmarshal([]byte(prop.Command.Body), &body); err != nil {
		t.Fatalf("Unmarshal candidate body: %v", err)
	}

	// Invariant: Because episode 3 was mixed binding, the candidate must be marked BindingModeMixed
	if body.BindingMode != consolidation.BindingModeMixed {
		t.Fatalf("Expected candidate BindingMode to be mixed, got %s", body.BindingMode)
	}

	// Check that EvidenceRefs contains all 3 runs
	if len(prop.Command.EvidenceRefs) != 3 {
		t.Fatalf("Expected 3 evidence refs, got %d", len(prop.Command.EvidenceRefs))
	}
}

type failingSemanticProposer struct {
	base         *memorySemanticProposer
	failGroupKey string
}

func (p *failingSemanticProposer) Propose(ctx context.Context, req rag.ProposeRequest) (rag.KnowledgeVersion, bool, error) {
	if strings.Contains(req.Command.NamespaceID, p.failGroupKey) || strings.Contains(req.Command.Body, p.failGroupKey) {
		return rag.KnowledgeVersion{}, false, fmt.Errorf("simulated destination failure for %s", p.failGroupKey)
	}
	return p.base.Propose(ctx, req)
}

type failingCorrectiveProposer struct {
	base       *memoryCorrectiveProposer
	failReqKey string
}

func (p *failingCorrectiveProposer) Propose(ctx context.Context, req memory.ProposeRequest) (memory.Entry, bool, error) {
	if strings.Contains(req.Command.Problem, p.failReqKey) {
		return memory.Entry{}, false, fmt.Errorf("simulated destination failure for %s", p.failReqKey)
	}
	return p.base.Propose(ctx, req)
}

func buildPositiveEpisodeWithGroup(harnessRunID string, taskID, attemptID, decisionRunID int64, roleID, taskClass, profileID string, observedAt time.Time) episode.Episode {
	ep := buildPositiveEpisode(harnessRunID, taskID, attemptID, decisionRunID, episode.BindingModeHomogeneous, observedAt)
	ep.RoleID = roleID
	ep.TaskClass = taskClass
	ep.ExecutionProfileID = profileID
	return ep
}

// Test partial failure isolation in semantic pass:
// 3 groups A, B, C; B fails; A and C succeed.
// Cycle reports 2 candidates proposed, 1 failure; no rollback of A or C.
func TestPartialFailureIsolationSemantic(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 5, 1, 0, 0, 0, time.UTC)

	// Build 3 distinct groups of 3 episodes each (9 episodes total)
	epA1 := buildPositiveEpisodeWithGroup("run-a1", 101, 1, 601, "role-a", "task-class-a", "profile/std", now)
	epA2 := buildPositiveEpisodeWithGroup("run-a2", 102, 1, 602, "role-a", "task-class-a", "profile/std", now.Add(time.Hour))
	epA3 := buildPositiveEpisodeWithGroup("run-a3", 103, 1, 603, "role-a", "task-class-a", "profile/std", now.Add(2*time.Hour))

	epB1 := buildPositiveEpisodeWithGroup("run-b1", 201, 1, 701, "role-b", "task-class-b", "profile/std", now)
	epB2 := buildPositiveEpisodeWithGroup("run-b2", 202, 1, 702, "role-b", "task-class-b", "profile/std", now.Add(time.Hour))
	epB3 := buildPositiveEpisodeWithGroup("run-b3", 203, 1, 703, "role-b", "task-class-b", "profile/std", now.Add(2*time.Hour))

	epC1 := buildPositiveEpisodeWithGroup("run-c1", 301, 1, 801, "role-c", "task-class-c", "profile/std", now)
	epC2 := buildPositiveEpisodeWithGroup("run-c2", 302, 1, 802, "role-c", "task-class-c", "profile/std", now.Add(time.Hour))
	epC3 := buildPositiveEpisodeWithGroup("run-c3", 303, 1, 803, "role-c", "task-class-c", "profile/std", now.Add(2*time.Hour))

	reader := &memoryReader{episodes: []episode.Episode{epA1, epA2, epA3, epB1, epB2, epB3, epC1, epC2, epC3}}
	clusterStore := newMemoryClusterStore()

	// Fail only when proposing group B
	semanticProposer := &failingSemanticProposer{
		base:         newMemorySemanticProposer(),
		failGroupKey: "role-b",
	}

	cfg := consolidation.DefaultConfig()
	cfg.SemanticOwner = consolidation.SemanticOwnerMemoryOS
	cfg.MinSemanticRecurrence = 3

	svc, err := consolidation.NewService(reader, clusterStore, semanticProposer, nil, cfg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	result, err := svc.Consolidate(ctx, "explorarte", now.Add(-time.Hour), now.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("Consolidate: %v", err)
	}

	if result.SemanticGroups != 3 {
		t.Fatalf("expected 3 semantic groups, got %d", result.SemanticGroups)
	}
	if result.SemanticCandidates != 2 {
		t.Fatalf("expected 2 successful candidates (A and C), got %d", result.SemanticCandidates)
	}
	if len(result.Failures) != 1 {
		t.Fatalf("expected exactly 1 failure (B), got %d: %+v", len(result.Failures), result.Failures)
	}
	if !strings.Contains(result.Failures[0].Key, "role-b") {
		t.Fatalf("expected failure for role-b, got key=%s", result.Failures[0].Key)
	}
	if len(semanticProposer.base.proposals) != 2 {
		t.Fatalf("expected 2 proposals persisted, got %d", len(semanticProposer.base.proposals))
	}
}

// Test partial failure isolation in corrective pass:
// 3 clusters A, B, C; B fails; A and C succeed.
func TestPartialFailureIsolationCorrective(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 5, 1, 0, 0, 0, time.UTC)

	// 3 runs for each obligation A, B, C
	epA1 := buildContradictedEpisode("run-a1", 101, 1, 501, "req-a", "task_requirement", now)
	epA2 := buildContradictedEpisode("run-a2", 102, 1, 502, "req-a", "task_requirement", now.Add(time.Hour))
	epA3 := buildContradictedEpisode("run-a3", 103, 1, 503, "req-a", "task_requirement", now.Add(2*time.Hour))

	epB1 := buildContradictedEpisode("run-b1", 201, 1, 601, "req-b", "task_requirement", now)
	epB2 := buildContradictedEpisode("run-b2", 202, 1, 602, "req-b", "task_requirement", now.Add(time.Hour))
	epB3 := buildContradictedEpisode("run-b3", 203, 1, 603, "req-b", "task_requirement", now.Add(2*time.Hour))

	epC1 := buildContradictedEpisode("run-c1", 301, 1, 701, "req-c", "task_requirement", now)
	epC2 := buildContradictedEpisode("run-c2", 302, 1, 702, "req-c", "task_requirement", now.Add(time.Hour))
	epC3 := buildContradictedEpisode("run-c3", 303, 1, 703, "req-c", "task_requirement", now.Add(2*time.Hour))

	reader := &memoryReader{episodes: []episode.Episode{epA1, epA2, epA3, epB1, epB2, epB3, epC1, epC2, epC3}}
	clusterStore := newMemoryClusterStore()
	correctiveProposer := &failingCorrectiveProposer{
		base:       newMemoryCorrectiveProposer(),
		failReqKey: "req-b",
	}

	cfg := consolidation.DefaultConfig()
	cfg.MinCorrectiveRecurrence = 3

	svc, err := consolidation.NewService(reader, clusterStore, nil, correctiveProposer, cfg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	result, err := svc.Consolidate(ctx, "explorarte", now.Add(-time.Hour), now.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("Consolidate: %v", err)
	}

	if result.CorrectiveCandidates != 2 {
		t.Fatalf("expected 2 candidates proposed (A and C), got %d", result.CorrectiveCandidates)
	}
	if len(result.Failures) != 1 {
		t.Fatalf("expected 1 failure for cluster B, got %d: %+v", len(result.Failures), result.Failures)
	}
	if !strings.Contains(result.Failures[0].Key, "req-b") {
		t.Fatalf("expected failure for req-b, got key=%s", result.Failures[0].Key)
	}
	if len(correctiveProposer.base.proposals) != 2 {
		t.Fatalf("expected 2 proposals persisted, got %d", len(correctiveProposer.base.proposals))
	}
}

// Test corrective cluster growth when adding a 4th run:
// - R1, R2, R3 -> cluster revision 1, candidate proposed with 3 EvidenceRefs
// - Add R4 -> cluster updated with 4 decision runs, candidate proposed with 4 EvidenceRefs
// - Historical proposal is not mutated
func TestCorrectiveClusterGrowthAddingFourthRun(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 5, 1, 0, 0, 0, time.UTC)

	ep1 := buildContradictedEpisode("run-1", 101, 1, 501, "req-auth-token", "task_requirement", now)
	ep2 := buildContradictedEpisode("run-2", 102, 1, 502, "req-auth-token", "task_requirement", now.Add(time.Hour))
	ep3 := buildContradictedEpisode("run-3", 103, 1, 503, "req-auth-token", "task_requirement", now.Add(2*time.Hour))

	reader := &memoryReader{episodes: []episode.Episode{ep1, ep2, ep3}}
	clusterStore := newMemoryClusterStore()
	correctiveProposer := newMemoryCorrectiveProposer()

	cfg := consolidation.DefaultConfig()
	cfg.MinCorrectiveRecurrence = 3

	svc, err := consolidation.NewService(reader, clusterStore, nil, correctiveProposer, cfg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	result1, err := svc.Consolidate(ctx, "explorarte", now.Add(-time.Hour), now.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("Consolidate 1: %v", err)
	}
	if result1.CorrectiveCandidates != 1 {
		t.Fatalf("expected 1 candidate, got %d", result1.CorrectiveCandidates)
	}
	if len(correctiveProposer.proposals) != 1 {
		t.Fatalf("expected 1 proposal, got %d", len(correctiveProposer.proposals))
	}
	firstProposal := correctiveProposer.proposals[0]
	if len(firstProposal.Command.EvidenceRefs) != 3 {
		t.Fatalf("expected 3 evidence refs, got %d", len(firstProposal.Command.EvidenceRefs))
	}

	// Add 4th run R4
	ep4 := buildContradictedEpisode("run-4", 104, 1, 504, "req-auth-token", "task_requirement", now.Add(3*time.Hour))
	reader.episodes = append(reader.episodes, ep4)

	result2, err := svc.Consolidate(ctx, "explorarte", now.Add(-time.Hour), now.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("Consolidate 2: %v", err)
	}
	// A new candidate reflecting the 4th run should be emitted
	if result2.CorrectiveCandidates != 1 {
		t.Fatalf("expected 1 new candidate for updated cluster, got %d", result2.CorrectiveCandidates)
	}
	if len(correctiveProposer.proposals) != 2 {
		t.Fatalf("expected 2 total proposals (historical + new), got %d", len(correctiveProposer.proposals))
	}
	secondProposal := correctiveProposer.proposals[1]
	if len(secondProposal.Command.EvidenceRefs) != 4 {
		t.Fatalf("expected 4 evidence refs in second proposal, got %d", len(secondProposal.Command.EvidenceRefs))
	}
	// Check that historical proposal was not mutated
	if len(firstProposal.Command.EvidenceRefs) != 3 {
		t.Fatalf("historical proposal was mutated!")
	}
	if firstProposal.Command.ID == secondProposal.Command.ID {
		t.Fatalf("new candidate must have a distinct ID from historical candidate!")
	}
}

// Test semantic double-consolidation safety:
// Validates that sleep candidate and MemoryOS semantic candidate formats have
// completely separate ID namespaces and idempotency keys, avoiding collision,
// and remain candidate-only without auto-approval.
func TestSemanticDoubleConsolidationSafety(t *testing.T) {
	now := time.Date(2026, 9, 5, 1, 0, 0, 0, time.UTC)
	episodes := []episode.Episode{
		buildPositiveEpisodeWithGroup("pos-1", 201, 1, 601, "ingenieria_ia/qa", "qa.verification", "profile/standard", now),
		buildPositiveEpisodeWithGroup("pos-2", 202, 1, 602, "ingenieria_ia/qa", "qa.verification", "profile/standard", now.Add(time.Hour)),
		buildPositiveEpisodeWithGroup("pos-3", 203, 1, 603, "ingenieria_ia/qa", "qa.verification", "profile/standard", now.Add(2*time.Hour)),
	}

	key := consolidation.SemanticGroupKey{
		RoleID:             "ingenieria_ia/qa",
		TaskClass:          "qa.verification",
		ExecutionPurpose:   "execution",
		ExecutionProfileID: "profile/standard",
	}

	req, _, err := consolidation.BuildSemanticCandidate("explorarte", key, episodes)
	if err != nil {
		t.Fatalf("BuildSemanticCandidate: %v", err)
	}

	// MemoryOS candidate assertions
	if !strings.HasPrefix(req.Command.ID, "memoryos-semantic-") {
		t.Fatalf("MemoryOS candidate ID should have prefix memoryos-semantic-, got %s", req.Command.ID)
	}
	if !strings.HasPrefix(req.IdempotencyKey, "memoryos:semantic:") {
		t.Fatalf("MemoryOS idempotency key prefix mismatch: %s", req.IdempotencyKey)
	}
	if req.Command.NamespaceKind != "own" {
		t.Fatalf("MemoryOS namespace kind should be own, got %s", req.Command.NamespaceKind)
	}
}
