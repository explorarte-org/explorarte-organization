package consolidation_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/executive/sleep"
	"github.com/Mireuz13/explorarte-organization/internal/memoryos/consolidation"
	"github.com/Mireuz13/explorarte-organization/internal/memoryos/episode"
	"github.com/Mireuz13/explorarte-organization/internal/rag"
)

type fakeSleepExperienceReader struct {
	values []sleep.Experience
}

func (f *fakeSleepExperienceReader) ListEligible(_ context.Context, _ string, _, _ time.Time, _ int) ([]sleep.Experience, error) {
	return append([]sleep.Experience(nil), f.values...), nil
}

type fakeSleepCandidateProposer struct {
	requests []rag.ProposeRequest
}

func (f *fakeSleepCandidateProposer) Propose(_ context.Context, req rag.ProposeRequest) (rag.KnowledgeVersion, bool, error) {
	f.requests = append(f.requests, req)
	return rag.KnowledgeVersion{ID: req.Command.ID, DocumentID: req.Command.DocumentID, Lifecycle: rag.LifecycleCandidate}, false, nil
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// TestSemanticSingleOwnerStructuralSleepDefault enforces that when Sleep is the
// canonical semantic owner (Phase 1 default):
// 1. Sleep produces its semantic candidate for the eligible experiences.
// 2. MemoryOS observes and groups the corresponding episodes.
// 3. MemoryOS emits ZERO semantic candidate proposals (SemanticSkippedNotOwner = true, reason = not_semantic_owner).
// 4. MemoryOS corrective consolidation proceeds normally and produces its corrective candidate.
// 5. Exactly ONE system (Sleep) acts as the active semantic owner for the collection.
func TestSemanticSingleOwnerStructuralSleepDefault(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 5, 2, 0, 0, 0, time.UTC)

	// Build 3 positive runs eligible for Sleep and MemoryOS positive consolidation
	sleepExperiences := []sleep.Experience{
		{
			RunID: 101, TaskID: 1001, AttemptID: 2001, UnitID: "ingenieria_ia", RoleID: "ingenieria_ia/qa",
			ProviderID: "anthropic", ProviderModelID: "claude-3-5-sonnet", VerificationLabel: sleep.VerificationVerified,
			EvidenceDigest: sha256Hex("evidence-101"), ObservedAt: now.Add(-3 * time.Hour),
		},
		{
			RunID: 102, TaskID: 1002, AttemptID: 2002, UnitID: "ingenieria_ia", RoleID: "ingenieria_ia/qa",
			ProviderID: "anthropic", ProviderModelID: "claude-3-5-sonnet", VerificationLabel: sleep.VerificationVerified,
			EvidenceDigest: sha256Hex("evidence-102"), ObservedAt: now.Add(-2 * time.Hour),
		},
		{
			RunID: 103, TaskID: 1003, AttemptID: 2003, UnitID: "ingenieria_ia", RoleID: "ingenieria_ia/qa",
			ProviderID: "anthropic", ProviderModelID: "claude-3-5-sonnet", VerificationLabel: sleep.VerificationVerified,
			EvidenceDigest: sha256Hex("evidence-103"), ObservedAt: now.Add(-1 * time.Hour),
		},
	}

	memoryosEpisodes := []episode.Episode{
		buildPositiveEpisode("run-101", 1001, 2001, 101, episode.BindingModeHomogeneous, now.Add(-3*time.Hour)),
		buildPositiveEpisode("run-102", 1002, 2002, 102, episode.BindingModeHomogeneous, now.Add(-2*time.Hour)),
		buildPositiveEpisode("run-103", 1003, 2003, 103, episode.BindingModeHomogeneous, now.Add(-1*time.Hour)),
		// Add 3 contradicted episodes to verify corrective consolidation runs concurrently unaffected
		buildContradictedEpisode("run-201", 1101, 2101, 201, "schema.contract", "validation", now.Add(-3*time.Hour)),
		buildContradictedEpisode("run-202", 1102, 2102, 202, "schema.contract", "validation", now.Add(-2*time.Hour)),
		buildContradictedEpisode("run-203", 1103, 2103, 203, "schema.contract", "validation", now.Add(-1*time.Hour)),
	}

	// 1. Run Sleep Cycle
	sleepReader := &fakeSleepExperienceReader{values: sleepExperiences}
	sleepProposer := &fakeSleepCandidateProposer{}
	sleepSvc, err := sleep.NewService(sleepReader, sleepProposer, sleep.ClockFunc(func() time.Time { return now }), sleep.DefaultConfig())
	if err != nil {
		t.Fatalf("sleep.NewService: %v", err)
	}
	sleepResult, err := sleepSvc.RunCycle(ctx, "explorarte", 24*time.Hour)
	if err != nil {
		t.Fatalf("sleep.RunCycle: %v", err)
	}
	if sleepResult.CandidatesProposed != 1 {
		t.Fatalf("expected Sleep to propose 1 candidate, got %d", sleepResult.CandidatesProposed)
	}
	if len(sleepProposer.requests) != 1 {
		t.Fatalf("expected Sleep proposer to receive 1 request, got %d", len(sleepProposer.requests))
	}

	// 2. Run MemoryOS Consolidate with default config (owner = sleep)
	memReader := &memoryReader{episodes: memoryosEpisodes}
	memClusterStore := newMemoryClusterStore()
	memSemanticProposer := newMemorySemanticProposer()
	memCorrectiveProposer := newMemoryCorrectiveProposer()

	cfg := consolidation.DefaultConfig()
	cfg.MinSemanticRecurrence = 3
	cfg.MinCorrectiveRecurrence = 3

	if cfg.SemanticOwner != consolidation.SemanticOwnerSleep {
		t.Fatalf("expected DefaultConfig().SemanticOwner == %q, got %q", consolidation.SemanticOwnerSleep, cfg.SemanticOwner)
	}

	memSvc, err := consolidation.NewService(memReader, memClusterStore, memSemanticProposer, memCorrectiveProposer, cfg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	memResult, err := memSvc.Consolidate(ctx, "explorarte", now.Add(-24*time.Hour), now)
	if err != nil {
		t.Fatalf("memSvc.Consolidate: %v", err)
	}

	// Invariants:
	// A. Semantic owner is recorded as sleep
	if memResult.SemanticOwner != consolidation.SemanticOwnerSleep {
		t.Fatalf("expected SemanticOwner %q, got %q", consolidation.SemanticOwnerSleep, memResult.SemanticOwner)
	}
	// B. Semantic groups are observed and counted
	if memResult.SemanticGroups != 1 {
		t.Fatalf("expected 1 semantic group observed, got %d", memResult.SemanticGroups)
	}
	// C. Semantic proposals are blocked because MemoryOS is not the owner
	if !memResult.SemanticSkippedNotOwner {
		t.Fatalf("expected SemanticSkippedNotOwner = true, got false")
	}
	if memResult.SemanticSkipReason != consolidation.SemanticSkipReasonNotOwner {
		t.Fatalf("expected SemanticSkipReason %q, got %q", consolidation.SemanticSkipReasonNotOwner, memResult.SemanticSkipReason)
	}
	if memResult.SemanticCandidates != 0 {
		t.Fatalf("expected 0 semantic candidates emitted by MemoryOS, got %d", memResult.SemanticCandidates)
	}
	if len(memSemanticProposer.proposals) != 0 {
		t.Fatalf("expected 0 calls to MemoryOS semantic proposer, got %d", len(memSemanticProposer.proposals))
	}
	// D. Corrective consolidation is completely unaffected
	if memResult.CorrectiveCandidates != 1 {
		t.Fatalf("expected 1 corrective candidate emitted, got %d", memResult.CorrectiveCandidates)
	}
	if len(memCorrectiveProposer.proposals) != 1 {
		t.Fatalf("expected 1 corrective proposal in memory store, got %d", len(memCorrectiveProposer.proposals))
	}

	// E. Total semantic owners producing candidates = 1 (sleep only)
	totalSemanticProposals := len(sleepProposer.requests) + len(memSemanticProposer.proposals)
	if totalSemanticProposals != 1 {
		t.Fatalf("expected exactly 1 total semantic candidate proposed across the system, got %d", totalSemanticProposals)
	}
}

// TestSemanticSingleOwnerStructuralMemoryOSEnabled tests selecting MemoryOS as
// the semantic owner (e.g. for future Phase 2 cutover or explicit isolation testing):
// MemoryOS semantic emission is permitted and produces candidates.
func TestSemanticSingleOwnerStructuralMemoryOSEnabled(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 5, 2, 0, 0, 0, time.UTC)

	memoryosEpisodes := []episode.Episode{
		buildPositiveEpisode("run-101", 1001, 2001, 101, episode.BindingModeHomogeneous, now.Add(-3*time.Hour)),
		buildPositiveEpisode("run-102", 1002, 2002, 102, episode.BindingModeHomogeneous, now.Add(-2*time.Hour)),
		buildPositiveEpisode("run-103", 1003, 2003, 103, episode.BindingModeHomogeneous, now.Add(-1*time.Hour)),
		buildContradictedEpisode("run-201", 1101, 2101, 201, "schema.contract", "validation", now.Add(-3*time.Hour)),
		buildContradictedEpisode("run-202", 1102, 2102, 202, "schema.contract", "validation", now.Add(-2*time.Hour)),
		buildContradictedEpisode("run-203", 1103, 2103, 203, "schema.contract", "validation", now.Add(-1*time.Hour)),
	}

	memReader := &memoryReader{episodes: memoryosEpisodes}
	memClusterStore := newMemoryClusterStore()
	memSemanticProposer := newMemorySemanticProposer()
	memCorrectiveProposer := newMemoryCorrectiveProposer()

	cfg := consolidation.DefaultConfig()
	cfg.SemanticOwner = consolidation.SemanticOwnerMemoryOS
	cfg.MinSemanticRecurrence = 3
	cfg.MinCorrectiveRecurrence = 3

	memSvc, err := consolidation.NewService(memReader, memClusterStore, memSemanticProposer, memCorrectiveProposer, cfg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	memResult, err := memSvc.Consolidate(ctx, "explorarte", now.Add(-24*time.Hour), now)
	if err != nil {
		t.Fatalf("memSvc.Consolidate: %v", err)
	}

	if memResult.SemanticOwner != consolidation.SemanticOwnerMemoryOS {
		t.Fatalf("expected SemanticOwner %q, got %q", consolidation.SemanticOwnerMemoryOS, memResult.SemanticOwner)
	}
	if memResult.SemanticSkippedNotOwner {
		t.Fatalf("expected SemanticSkippedNotOwner = false, got true")
	}
	if memResult.SemanticSkipReason != "" {
		t.Fatalf("expected empty SemanticSkipReason, got %q", memResult.SemanticSkipReason)
	}
	if memResult.SemanticCandidates != 1 {
		t.Fatalf("expected 1 semantic candidate emitted, got %d", memResult.SemanticCandidates)
	}
	if len(memSemanticProposer.proposals) != 1 {
		t.Fatalf("expected 1 proposal sent to RAG staging, got %d", len(memSemanticProposer.proposals))
	}
	if memResult.CorrectiveCandidates != 1 {
		t.Fatalf("expected 1 corrective candidate emitted, got %d", memResult.CorrectiveCandidates)
	}
}

func TestSemanticOwnerConfigValidation(t *testing.T) {
	cfg := consolidation.DefaultConfig()
	cfg.SemanticOwner = "unsupported_owner"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected invalid semantic owner to fail validation")
	}

	// Verify NewService defaults empty SemanticOwner to Sleep
	reader := &memoryReader{}
	store := newMemoryClusterStore()
	corrective := newMemoryCorrectiveProposer()
	emptyOwnerCfg := consolidation.DefaultConfig()
	emptyOwnerCfg.SemanticOwner = ""
	svc, err := consolidation.NewService(reader, store, nil, corrective, emptyOwnerCfg)
	if err != nil {
		t.Fatalf("NewService with empty owner failed: %v", err)
	}
	res, err := svc.Consolidate(context.Background(), "explorarte", time.Now().Add(-time.Hour), time.Now())
	if err != nil {
		t.Fatalf("Consolidate failed: %v", err)
	}
	if res.SemanticOwner != consolidation.SemanticOwnerSleep {
		t.Fatalf("expected defaulted owner %q, got %q", consolidation.SemanticOwnerSleep, res.SemanticOwner)
	}
}
