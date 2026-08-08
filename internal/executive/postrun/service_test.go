package postrun

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/authorization"
	"github.com/Mireuz13/explorarte-organization/internal/completion"
	"github.com/Mireuz13/explorarte-organization/internal/decisiongraphtrace"
	"github.com/Mireuz13/explorarte-organization/internal/memory"
)

type fakeTraces struct {
	summary decisiongraphtrace.RunSummary
	err     error
}

func (f fakeTraces) RunSummary(context.Context, int64) (decisiongraphtrace.RunSummary, error) {
	return f.summary, f.err
}

type fakeVerifier struct {
	result completion.VerificationResult
	err    error
	calls  int
}

func (f *fakeVerifier) Verify(_ context.Context, request completion.VerificationRequest) (completion.VerificationResult, error) {
	f.calls++
	if f.err != nil {
		return completion.VerificationResult{}, f.err
	}
	f.result.TaskID, f.result.AttemptID = request.TaskID, request.AttemptID
	return f.result, nil
}

type fakeRoles struct {
	roleID string
	err    error
}

func (f fakeRoles) AssignedRoleID(context.Context, int64) (string, error) { return f.roleID, f.err }

type fakeLessons struct {
	entry  memory.Entry
	reused bool
	err    error
	got    memory.ProposeRequest
}

func (f *fakeLessons) Propose(_ context.Context, request memory.ProposeRequest) (memory.Entry, bool, error) {
	f.got = request
	if f.err != nil {
		return memory.Entry{}, false, f.err
	}
	return f.entry, f.reused, nil
}

func newService(t *testing.T, traces TraceReader, verify Verifier, roles RoleResolver, lessons LessonProposer) *Service {
	t.Helper()
	svc, err := NewService(traces, verify, roles, lessons)
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func TestProcessRunSkipsCleanPass(t *testing.T) {
	verifier := &fakeVerifier{result: completion.VerificationResult{Verdict: completion.VerdictPass}}
	lessons := &fakeLessons{}
	svc := newService(t, fakeTraces{summary: decisiongraphtrace.RunSummary{TaskID: 10, AttemptID: 1, TraceHash: "h"}}, verifier, fakeRoles{roleID: "ingenieria_ia/qa"}, lessons)

	outcome, err := svc.ProcessRun(context.Background(), "explorarte", 5)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Kind != KindSkippedPass {
		t.Fatalf("kind=%s want=%s", outcome.Kind, KindSkippedPass)
	}
	if lessons.got.Command.ID != "" {
		t.Fatalf("propose must not be called on a clean pass, got %+v", lessons.got)
	}
}

func TestProcessRunProposesFromUnresolvedObligations(t *testing.T) {
	verifier := &fakeVerifier{result: completion.VerificationResult{
		Verdict: completion.VerdictFail,
		Obligations: []completion.ObligationResult{
			{Obligation: completion.ObligationChecksPassed, Label: completion.LabelVerified, Detail: "checks ran"},
			{Obligation: completion.ObligationRequirementsSatisfied, Label: completion.LabelContradicted, Detail: "requirement X was not met"},
		},
	}}
	lessons := &fakeLessons{entry: memory.Entry{ID: "postrun-run-5", Status: memory.StatusCandidate}}
	svc := newService(t, fakeTraces{summary: decisiongraphtrace.RunSummary{TaskID: 10, AttemptID: 1, TraceHash: "abc123"}}, verifier, fakeRoles{roleID: "ingenieria_ia/qa"}, lessons)

	outcome, err := svc.ProcessRun(context.Background(), "explorarte", 5)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Kind != KindProposed {
		t.Fatalf("kind=%s want=%s", outcome.Kind, KindProposed)
	}
	if lessons.got.IdempotencyKey != "postrun:decision-run:5" {
		t.Fatalf("idempotency key=%q", lessons.got.IdempotencyKey)
	}
	if lessons.got.Command.ID != "postrun-run-5" {
		t.Fatalf("entry id=%q", lessons.got.Command.ID)
	}
	if lessons.got.Command.RoleID != "ingenieria_ia/qa" || lessons.got.Command.ProposedBy != "ingenieria_ia/qa" {
		t.Fatalf("role/proposed_by=%+v", lessons.got.Command)
	}
	if lessons.got.Command.SourceRunID != 5 {
		t.Fatalf("source_run_id=%d want=5", lessons.got.Command.SourceRunID)
	}
	if len(lessons.got.Command.EvidenceRefs) != 1 || lessons.got.Command.EvidenceRefs[0].Digest != "abc123" {
		t.Fatalf("evidence refs=%+v", lessons.got.Command.EvidenceRefs)
	}
	// The checks_passed obligation was verified, so it must not appear in the
	// problem text; only the contradicted requirement obligation should.
	if !strings.Contains(lessons.got.Command.Problem, "requirement X was not met") {
		t.Fatalf("problem missing real obligation detail: %q", lessons.got.Command.Problem)
	}
	if strings.Contains(lessons.got.Command.Problem, "checks ran") {
		t.Fatalf("problem must not include a verified obligation's detail: %q", lessons.got.Command.Problem)
	}
	if lessons.got.Command.Correction != correctionPending {
		t.Fatalf("correction=%q want the honest placeholder", lessons.got.Command.Correction)
	}
}

func TestProcessRunReusesIdempotently(t *testing.T) {
	verifier := &fakeVerifier{result: completion.VerificationResult{Verdict: completion.VerdictInconclusive}}
	lessons := &fakeLessons{entry: memory.Entry{ID: "postrun-run-5"}, reused: true}
	svc := newService(t, fakeTraces{summary: decisiongraphtrace.RunSummary{TaskID: 10, AttemptID: 1}}, verifier, fakeRoles{roleID: "ingenieria_ia/qa"}, lessons)

	outcome, err := svc.ProcessRun(context.Background(), "explorarte", 5)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Kind != KindReused {
		t.Fatalf("kind=%s want=%s", outcome.Kind, KindReused)
	}
}

func TestProcessRunSkipsWhenRoleCannotPropose(t *testing.T) {
	verifier := &fakeVerifier{result: completion.VerificationResult{Verdict: completion.VerdictFail}}
	lessons := &fakeLessons{err: authorization.ErrCapabilityDenied}
	svc := newService(t, fakeTraces{summary: decisiongraphtrace.RunSummary{TaskID: 10, AttemptID: 1}}, verifier, fakeRoles{roleID: "empresa/ceo"}, lessons)

	outcome, err := svc.ProcessRun(context.Background(), "explorarte", 5)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Kind != KindSkippedRoleNotEligible {
		t.Fatalf("kind=%s want=%s", outcome.Kind, KindSkippedRoleNotEligible)
	}
}

func TestProcessRunPropagatesOtherProposeErrors(t *testing.T) {
	verifier := &fakeVerifier{result: completion.VerificationResult{Verdict: completion.VerdictFail}}
	lessons := &fakeLessons{err: errors.New("boom")}
	svc := newService(t, fakeTraces{summary: decisiongraphtrace.RunSummary{TaskID: 10, AttemptID: 1}}, verifier, fakeRoles{roleID: "ingenieria_ia/qa"}, lessons)

	if _, err := svc.ProcessRun(context.Background(), "explorarte", 5); err == nil {
		t.Fatal("expected an error")
	}
}

func TestProcessRunPropagatesTraceLoadError(t *testing.T) {
	svc := newService(t, fakeTraces{err: decisiongraphtrace.ErrRunNotSucceeded}, &fakeVerifier{}, fakeRoles{}, &fakeLessons{})
	if _, err := svc.ProcessRun(context.Background(), "explorarte", 5); !errors.Is(err, decisiongraphtrace.ErrRunNotSucceeded) {
		t.Fatalf("err=%v want wrapped ErrRunNotSucceeded", err)
	}
}

func TestProcessRunRejectsInvalidInput(t *testing.T) {
	svc := newService(t, fakeTraces{}, &fakeVerifier{}, fakeRoles{}, &fakeLessons{})
	if _, err := svc.ProcessRun(context.Background(), "explorarte", 0); err == nil {
		t.Fatal("expected an error for a non-positive run id")
	}
	if _, err := svc.ProcessRun(context.Background(), "", 5); err == nil {
		t.Fatal("expected an error for an empty organization id")
	}
}
