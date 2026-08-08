package sleep

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/rag"
)

type fakeExperienceReader struct {
	values []Experience
	from   time.Time
	to     time.Time
	limit  int
}

func (f *fakeExperienceReader) ListEligible(_ context.Context, _ string, from, to time.Time, limit int) ([]Experience, error) {
	f.from, f.to, f.limit = from, to, limit
	return append([]Experience(nil), f.values...), nil
}

type fakeCandidateProposer struct {
	requests []rag.ProposeRequest
	seen     map[string]struct{}
	failWhen func(rag.ProposeRequest) bool
}

func (f *fakeCandidateProposer) Propose(_ context.Context, request rag.ProposeRequest) (rag.KnowledgeVersion, bool, error) {
	if f.failWhen != nil && f.failWhen(request) {
		return rag.KnowledgeVersion{}, false, errors.New("simulated proposer failure")
	}
	if f.seen == nil {
		f.seen = map[string]struct{}{}
	}
	_, reused := f.seen[request.IdempotencyKey]
	f.seen[request.IdempotencyKey] = struct{}{}
	f.requests = append(f.requests, request)
	return rag.KnowledgeVersion{ID: request.Command.ID, DocumentID: request.Command.DocumentID, Lifecycle: rag.LifecycleCandidate}, reused, nil
}

func TestRunCycleProposesRecurringObservedCandidate(t *testing.T) {
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	reader := &fakeExperienceReader{values: []Experience{
		testExperience(1, VerificationVerified, "deepseek", now.Add(-3*time.Hour)),
		testExperience(2, VerificationVerified, "deepseek", now.Add(-2*time.Hour)),
		testExperience(3, VerificationContradicted, "deepseek", now.Add(-time.Hour)),
	}}
	proposer := &fakeCandidateProposer{}
	service, err := NewService(reader, proposer, ClockFunc(func() time.Time { return now }), DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.RunCycle(context.Background(), "explorarte", 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if result.EligibleExperiences != 3 || result.RecurringGroups != 1 || result.MixedContradictionGroups != 1 || result.CandidatesProposed != 1 {
		t.Fatalf("result=%+v", result)
	}
	if len(proposer.requests) != 1 {
		t.Fatalf("proposals=%d want=1", len(proposer.requests))
	}
	request := proposer.requests[0]
	if request.Command.OrganizationID != "explorarte" || request.Command.ProposedBy != ProposerRoleID || request.Command.NamespaceID != "ingenieria_ia" {
		t.Fatalf("request=%+v", request)
	}
	if result.Proposals[0].Confidence != Confidence(3, 8, 2.0/3.0, 1, true) {
		t.Fatalf("confidence=%f", result.Proposals[0].Confidence)
	}
	if reader.limit != DefaultConfig().MaxExperiences || !reader.from.Equal(now.Add(-24*time.Hour)) || !reader.to.Equal(now) {
		t.Fatalf("reader window from=%s to=%s limit=%d", reader.from, reader.to, reader.limit)
	}

	second, err := service.RunCycle(context.Background(), "explorarte", 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if second.CandidatesReused != 1 || second.CandidatesProposed != 0 {
		t.Fatalf("second=%+v", second)
	}
}

func TestRunCycleIsolatesOneCandidateFailureFromOthers(t *testing.T) {
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	groupA := []Experience{
		testExperience(1, VerificationVerified, "deepseek", now.Add(-5*time.Hour)),
		testExperience(2, VerificationVerified, "deepseek", now.Add(-4*time.Hour)),
		testExperience(3, VerificationVerified, "deepseek", now.Add(-3*time.Hour)),
	}
	groupB := []Experience{
		testExperience(11, VerificationVerified, "gemini", now.Add(-2*time.Hour)),
		testExperience(12, VerificationVerified, "gemini", now.Add(-time.Hour)),
		testExperience(13, VerificationVerified, "gemini", now.Add(-30*time.Minute)),
	}
	reader := &fakeExperienceReader{values: append(append([]Experience(nil), groupA...), groupB...)}
	proposer := &fakeCandidateProposer{failWhen: func(request rag.ProposeRequest) bool {
		return strings.Contains(request.Command.Title, "deepseek")
	}}
	service, err := NewService(reader, proposer, ClockFunc(func() time.Time { return now }), DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.RunCycle(context.Background(), "explorarte", 24*time.Hour)
	if err != nil {
		t.Fatalf("RunCycle must isolate a single candidate's failure, not abort the cycle: %v", err)
	}
	if len(result.Failures) != 1 {
		t.Fatalf("failures=%+v want exactly 1", result.Failures)
	}
	if result.Failures[0].Group.ProviderID != "deepseek" {
		t.Fatalf("failure group=%+v want provider=deepseek", result.Failures[0].Group)
	}
	if result.CandidatesProposed != 1 || len(result.Proposals) != 1 {
		t.Fatalf("result=%+v want the gemini group still successfully proposed", result)
	}
	if result.Proposals[0].Group.ProviderID != "gemini" {
		t.Fatalf("surviving proposal group=%+v want provider=gemini", result.Proposals[0].Group)
	}
}

func TestRunCycleSkipsWeakAndInsufficientGroups(t *testing.T) {
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	values := []Experience{
		testExperience(1, VerificationVerified, "weak-provider", now.Add(-5*time.Hour)),
		testExperience(2, VerificationVerified, "weak-provider", now.Add(-4*time.Hour)),
		testExperience(3, VerificationContradicted, "weak-provider", now.Add(-3*time.Hour)),
		testExperience(4, VerificationContradicted, "weak-provider", now.Add(-2*time.Hour)),
		testExperience(5, VerificationUnknown, "weak-provider", now.Add(-time.Hour)), // exactly .40
	}
	insufficientA := testExperience(20, VerificationVerified, "short-provider", now.Add(-30*time.Minute))
	insufficientA.RoleID = "ingenieria_ia/frontend"
	insufficientB := testExperience(21, VerificationVerified, "short-provider", now.Add(-20*time.Minute))
	insufficientB.RoleID = "ingenieria_ia/frontend"
	values = append(values, insufficientA, insufficientB)
	reader := &fakeExperienceReader{values: values}
	proposer := &fakeCandidateProposer{}
	service, err := NewService(reader, proposer, ClockFunc(func() time.Time { return now }), DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.RunCycle(context.Background(), "explorarte", 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if result.SkippedLowPassRate != 1 || result.SkippedInsufficientRuns != 1 || result.CandidatesProposed != 0 || len(proposer.requests) != 0 {
		t.Fatalf("result=%+v requests=%d", result, len(proposer.requests))
	}
}
