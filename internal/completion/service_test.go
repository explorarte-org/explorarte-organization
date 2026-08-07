package completion

import (
	"context"
	"errors"
	"testing"
	"time"
)

func fixedClock(at time.Time) ClockFunc { return func() time.Time { return at } }

func newTestService(t *testing.T, tasks *fakeTasks, artifacts *fakeArtifacts, checks *fakeChecks, approvals *fakeApprovals, branches *fakeBranches) *Service {
	t.Helper()
	s, err := NewService(tasks, artifacts, checks, approvals, branches, fixedClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return s
}

func baseTaskFact() TaskFact {
	return TaskFact{
		TaskID: 1,
		Status: "awaiting_verification",
		Requirements: []RequirementFact{
			{RequirementID: 10, Type: RequirementArtifact, Required: true, Satisfied: true, EvidenceRef: "staging://artifact/1", EvidenceDigest: "aaa"},
			{RequirementID: 11, Type: RequirementCheck, Required: true, Satisfied: true, EvidenceRef: "check-run-1"},
			{RequirementID: 12, Type: RequirementApproval, Required: true, Satisfied: true, EvidenceRef: "approval-9", EvidenceDigest: "digest-x"},
		},
	}
}

func TestNewServiceRequiresAllReaders(t *testing.T) {
	if _, err := NewService(nil, &fakeArtifacts{}, &fakeChecks{}, &fakeApprovals{}, &fakeBranches{}, nil); err == nil {
		t.Fatal("expected error for nil TaskReader")
	}
}

func TestVerifyRejectsInvalidRequest(t *testing.T) {
	s := newTestService(t, &fakeTasks{}, &fakeArtifacts{}, &fakeChecks{}, &fakeApprovals{}, &fakeBranches{})
	if _, err := s.Verify(context.Background(), VerificationRequest{}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("err=%v", err)
	}
}

func TestVerifyPassesWhenEverythingIndependentlyConfirmed(t *testing.T) {
	tasks := &fakeTasks{facts: map[int64]TaskFact{1: baseTaskFact()}}
	artifacts := &fakeArtifacts{digests: map[string]string{"staging://artifact/1": "aaa"}}
	checks := &fakeChecks{passed: map[int64]bool{11: true}}
	approvals := &fakeApprovals{consumed: map[string]string{"approval-9": "digest-x"}}
	branches := &fakeBranches{states: map[string]string{"1:5": "selected"}}
	s := newTestService(t, tasks, artifacts, checks, approvals, branches)

	result, err := s.Verify(context.Background(), VerificationRequest{TaskID: 1, AttemptID: 5})
	if err != nil {
		t.Fatal(err)
	}
	if result.Verdict != VerdictPass {
		t.Fatalf("verdict=%s obligations=%+v", result.Verdict, result.Obligations)
	}
	if len(result.Obligations) != 5 { // requirements_satisfied + artifact + check + approval + branch
		t.Fatalf("expected 5 obligations, got %d: %+v", len(result.Obligations), result.Obligations)
	}
	for _, o := range result.Obligations {
		if o.Label != LabelVerified {
			t.Fatalf("obligation %s not verified: %+v", o.Obligation, o)
		}
	}
}

func TestVerifyFailsWhenRequiredRequirementNotSatisfied(t *testing.T) {
	fact := baseTaskFact()
	fact.Requirements[0].Satisfied = false
	tasks := &fakeTasks{facts: map[int64]TaskFact{1: fact}}
	s := newTestService(t, tasks, &fakeArtifacts{}, &fakeChecks{}, &fakeApprovals{}, &fakeBranches{})

	result, err := s.Verify(context.Background(), VerificationRequest{TaskID: 1, AttemptID: 5})
	if err != nil {
		t.Fatal(err)
	}
	if result.Verdict != VerdictFail {
		t.Fatalf("verdict=%s", result.Verdict)
	}
	found := false
	for _, o := range result.Obligations {
		if o.Obligation == ObligationRequirementsSatisfied {
			found = true
			if o.Label != LabelContradicted {
				t.Fatalf("label=%s", o.Label)
			}
		}
	}
	if !found {
		t.Fatal("missing requirements_satisfied obligation")
	}
}

func TestVerifyFailsOnArtifactDigestMismatch(t *testing.T) {
	tasks := &fakeTasks{facts: map[int64]TaskFact{1: baseTaskFact()}}
	artifacts := &fakeArtifacts{digests: map[string]string{"staging://artifact/1": "different-digest"}}
	checks := &fakeChecks{passed: map[int64]bool{11: true}}
	approvals := &fakeApprovals{consumed: map[string]string{"approval-9": "digest-x"}}
	branches := &fakeBranches{}
	s := newTestService(t, tasks, artifacts, checks, approvals, branches)

	result, err := s.Verify(context.Background(), VerificationRequest{TaskID: 1, AttemptID: 5})
	if err != nil {
		t.Fatal(err)
	}
	if result.Verdict != VerdictFail {
		t.Fatalf("verdict=%s obligations=%+v", result.Verdict, result.Obligations)
	}
}

func TestVerifyFailsWhenArtifactMissingFromStaging(t *testing.T) {
	tasks := &fakeTasks{facts: map[int64]TaskFact{1: baseTaskFact()}}
	artifacts := &fakeArtifacts{digests: map[string]string{}}
	checks := &fakeChecks{passed: map[int64]bool{11: true}}
	approvals := &fakeApprovals{consumed: map[string]string{"approval-9": "digest-x"}}
	s := newTestService(t, tasks, artifacts, checks, approvals, &fakeBranches{})

	result, err := s.Verify(context.Background(), VerificationRequest{TaskID: 1, AttemptID: 5})
	if err != nil {
		t.Fatal(err)
	}
	if result.Verdict != VerdictFail {
		t.Fatalf("verdict=%s", result.Verdict)
	}
}

func TestVerifyFailsWhenCheckDidNotPass(t *testing.T) {
	tasks := &fakeTasks{facts: map[int64]TaskFact{1: baseTaskFact()}}
	artifacts := &fakeArtifacts{digests: map[string]string{"staging://artifact/1": "aaa"}}
	checks := &fakeChecks{passed: map[int64]bool{}} // requirement 11 absent -> false
	approvals := &fakeApprovals{consumed: map[string]string{"approval-9": "digest-x"}}
	s := newTestService(t, tasks, artifacts, checks, approvals, &fakeBranches{})

	result, err := s.Verify(context.Background(), VerificationRequest{TaskID: 1, AttemptID: 5})
	if err != nil {
		t.Fatal(err)
	}
	if result.Verdict != VerdictFail {
		t.Fatalf("verdict=%s", result.Verdict)
	}
}

func TestVerifyFailsWhenApprovalNotConsumedOrDigestMismatches(t *testing.T) {
	tasks := &fakeTasks{facts: map[int64]TaskFact{1: baseTaskFact()}}
	artifacts := &fakeArtifacts{digests: map[string]string{"staging://artifact/1": "aaa"}}
	checks := &fakeChecks{passed: map[int64]bool{11: true}}
	approvals := &fakeApprovals{consumed: map[string]string{"approval-9": "wrong-digest"}}
	s := newTestService(t, tasks, artifacts, checks, approvals, &fakeBranches{})

	result, err := s.Verify(context.Background(), VerificationRequest{TaskID: 1, AttemptID: 5})
	if err != nil {
		t.Fatal(err)
	}
	if result.Verdict != VerdictFail {
		t.Fatalf("verdict=%s", result.Verdict)
	}
}

func TestVerifyFailsWhenSelectedBranchWasLaterRejected(t *testing.T) {
	tasks := &fakeTasks{facts: map[int64]TaskFact{1: baseTaskFact()}}
	artifacts := &fakeArtifacts{digests: map[string]string{"staging://artifact/1": "aaa"}}
	checks := &fakeChecks{passed: map[int64]bool{11: true}}
	approvals := &fakeApprovals{consumed: map[string]string{"approval-9": "digest-x"}}
	branches := &fakeBranches{states: map[string]string{"1:5": "rejected_by_evidence"}}
	s := newTestService(t, tasks, artifacts, checks, approvals, branches)

	result, err := s.Verify(context.Background(), VerificationRequest{TaskID: 1, AttemptID: 5})
	if err != nil {
		t.Fatal(err)
	}
	if result.Verdict != VerdictFail {
		t.Fatalf("verdict=%s obligations=%+v", result.Verdict, result.Obligations)
	}
}

func TestVerifyTreatsMissingDecisionRunAsVacuouslyVerified(t *testing.T) {
	tasks := &fakeTasks{facts: map[int64]TaskFact{1: baseTaskFact()}}
	artifacts := &fakeArtifacts{digests: map[string]string{"staging://artifact/1": "aaa"}}
	checks := &fakeChecks{passed: map[int64]bool{11: true}}
	approvals := &fakeApprovals{consumed: map[string]string{"approval-9": "digest-x"}}
	branches := &fakeBranches{states: map[string]string{}} // no run for task 1 / attempt 5
	s := newTestService(t, tasks, artifacts, checks, approvals, branches)

	result, err := s.Verify(context.Background(), VerificationRequest{TaskID: 1, AttemptID: 5})
	if err != nil {
		t.Fatal(err)
	}
	if result.Verdict != VerdictPass {
		t.Fatalf("verdict=%s obligations=%+v", result.Verdict, result.Obligations)
	}
}

func TestVerifyIsInconclusiveWhenArtifactDigestNeverRecorded(t *testing.T) {
	fact := baseTaskFact()
	fact.Requirements[0].EvidenceDigest = ""
	tasks := &fakeTasks{facts: map[int64]TaskFact{1: fact}}
	artifacts := &fakeArtifacts{digests: map[string]string{"staging://artifact/1": "aaa"}}
	checks := &fakeChecks{passed: map[int64]bool{11: true}}
	approvals := &fakeApprovals{consumed: map[string]string{"approval-9": "digest-x"}}
	s := newTestService(t, tasks, artifacts, checks, approvals, &fakeBranches{})

	result, err := s.Verify(context.Background(), VerificationRequest{TaskID: 1, AttemptID: 5})
	if err != nil {
		t.Fatal(err)
	}
	if result.Verdict != VerdictInconclusive {
		t.Fatalf("verdict=%s obligations=%+v", result.Verdict, result.Obligations)
	}
}

func TestVerifyIgnoresNonRequiredRequirements(t *testing.T) {
	fact := baseTaskFact()
	fact.Requirements = append(fact.Requirements, RequirementFact{RequirementID: 13, Type: RequirementArtifact, Required: false, Satisfied: false, EvidenceRef: "nope", EvidenceDigest: "zzz"})
	tasks := &fakeTasks{facts: map[int64]TaskFact{1: fact}}
	artifacts := &fakeArtifacts{digests: map[string]string{"staging://artifact/1": "aaa"}}
	checks := &fakeChecks{passed: map[int64]bool{11: true}}
	approvals := &fakeApprovals{consumed: map[string]string{"approval-9": "digest-x"}}
	s := newTestService(t, tasks, artifacts, checks, approvals, &fakeBranches{})

	result, err := s.Verify(context.Background(), VerificationRequest{TaskID: 1, AttemptID: 5})
	if err != nil {
		t.Fatal(err)
	}
	if result.Verdict != VerdictPass {
		t.Fatalf("verdict=%s obligations=%+v", result.Verdict, result.Obligations)
	}
	if len(result.Obligations) != 5 {
		t.Fatalf("non-required requirement should not add an obligation, got %d", len(result.Obligations))
	}
}

func TestVerifyPropagatesTaskNotFound(t *testing.T) {
	s := newTestService(t, &fakeTasks{}, &fakeArtifacts{}, &fakeChecks{}, &fakeApprovals{}, &fakeBranches{})
	if _, err := s.Verify(context.Background(), VerificationRequest{TaskID: 999, AttemptID: 1}); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("err=%v", err)
	}
}
