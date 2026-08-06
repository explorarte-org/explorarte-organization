package evaluation

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func mustTrace(runID int64, payload []byte) (TraceRef, EvaluationTrace) {
	ref := TraceRef{RunID: runID, SchemaVersion: "test.v1", OrganizationID: "org-1"}
	trace := EvaluationTrace{Ref: ref, Payload: payload, LoadedAt: time.Unix(1_700_000_000, 0).UTC()}
	ref.TraceHash = trace.ContentHash()
	trace.Ref = ref
	return ref, trace
}

func mustSuite(t *testing.T, ref TraceRef) EvaluationSuite {
	t.Helper()
	suite := EvaluationSuite{
		ID:            "suite-1",
		Name:          "core suite",
		SchemaVersion: "test.v1",
		Cases: []EvaluationCase{
			{ID: "case-1", Trace: ref, Weight: 1, ExpectedOutcome: "stable output"},
		},
	}
	if err := suite.Validate(); err != nil {
		t.Fatalf("suite must be valid: %v", err)
	}
	return suite
}

func TestTraceRefValidate(t *testing.T) {
	valid := TraceRef{RunID: 1, TraceHash: "abc", SchemaVersion: "v1", OrganizationID: "org-1"}
	if err := valid.Validate(); err != nil {
		t.Fatalf("expected valid ref, got %v", err)
	}
	cases := []TraceRef{
		{RunID: 0, TraceHash: "abc", SchemaVersion: "v1", OrganizationID: "org-1"},
		{RunID: 1, TraceHash: "", SchemaVersion: "v1", OrganizationID: "org-1"},
		{RunID: 1, TraceHash: "abc", SchemaVersion: "", OrganizationID: "org-1"},
		{RunID: 1, TraceHash: "abc", SchemaVersion: "v1", OrganizationID: ""},
	}
	for i, c := range cases {
		if err := c.Validate(); !errors.Is(err, ErrInvalidTraceRef) {
			t.Fatalf("case %d: expected ErrInvalidTraceRef, got %v", i, err)
		}
	}
}

func TestEvaluationTraceValidateHashMismatch(t *testing.T) {
	ref, trace := mustTrace(1, []byte("payload"))
	if err := trace.Validate(); err != nil {
		t.Fatalf("expected valid trace, got %v", err)
	}
	tampered := trace
	tampered.Payload = []byte("tampered")
	if err := tampered.Validate(); !errors.Is(err, ErrTraceHashMismatch) {
		t.Fatalf("expected ErrTraceHashMismatch, got %v", err)
	}
	_ = ref
}

func TestEvaluationSuiteValidateDuplicateCase(t *testing.T) {
	ref, _ := mustTrace(1, []byte("payload"))
	suite := EvaluationSuite{
		ID: "s", Name: "n", SchemaVersion: "v1",
		Cases: []EvaluationCase{
			{ID: "dup", Trace: ref, Weight: 1, ExpectedOutcome: "x"},
			{ID: "dup", Trace: ref, Weight: 1, ExpectedOutcome: "y"},
		},
	}
	if err := suite.Validate(); !errors.Is(err, ErrDuplicateCase) {
		t.Fatalf("expected ErrDuplicateCase, got %v", err)
	}
}

func TestEvaluationSuiteValidateEmpty(t *testing.T) {
	suite := EvaluationSuite{ID: "s", Name: "n", SchemaVersion: "v1"}
	if err := suite.Validate(); !errors.Is(err, ErrEmptySuite) {
		t.Fatalf("expected ErrEmptySuite, got %v", err)
	}
}

func TestEvaluationRequestValidateCaseNotInSuite(t *testing.T) {
	ref, trace := mustTrace(1, []byte("payload"))
	suite := mustSuite(t, ref)
	otherRef, _ := mustTrace(2, []byte("other"))
	foreign := EvaluationCase{ID: "not-a-member", Trace: otherRef, Weight: 1, ExpectedOutcome: "x"}
	req := EvaluationRequest{
		Suite: suite, Case: foreign, Trace: trace,
		CandidateID: "c1", CandidateArtifactHash: "h1", Role: RoleCandidate,
	}
	if err := req.Validate(); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("expected ErrInvalidRequest, got %v", err)
	}
}

func TestEvaluationRequestValidateTraceMismatch(t *testing.T) {
	ref, _ := mustTrace(1, []byte("payload"))
	suite := mustSuite(t, ref)
	_, wrongTrace := mustTrace(2, []byte("different"))
	req := EvaluationRequest{
		Suite: suite, Case: suite.Cases[0], Trace: wrongTrace,
		CandidateID: "c1", CandidateArtifactHash: "h1", Role: RoleCandidate,
	}
	if err := req.Validate(); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("expected ErrInvalidRequest, got %v", err)
	}
}

func TestCompareResultsRoleAndCaseMismatch(t *testing.T) {
	ref, _ := mustTrace(1, []byte("payload"))
	base := EvaluationResult{
		CaseID: "case-1", Role: RoleBaseline, TraceRef: ref,
		Metrics: []Metric{{Name: "m", Value: 1, Unit: "u"}}, Verdict: VerdictPass, EvaluatedAt: time.Now().UTC(),
	}
	cand := base
	cand.Role = RoleBaseline // wrong role on purpose
	if _, err := CompareResults(base, cand); !errors.Is(err, ErrIncomparableResults) {
		t.Fatalf("expected ErrIncomparableResults for role mismatch, got %v", err)
	}

	cand2 := base
	cand2.Role = RoleCandidate
	cand2.CaseID = "case-2"
	if _, err := CompareResults(base, cand2); !errors.Is(err, ErrCaseMismatch) {
		t.Fatalf("expected ErrCaseMismatch, got %v", err)
	}
}

func TestCompareResultsOverallVerdict(t *testing.T) {
	ref, _ := mustTrace(1, []byte("payload"))
	build := func(role EvaluationRole, verdict Verdict, value float64) EvaluationResult {
		return EvaluationResult{
			CaseID: "case-1", Role: role, TraceRef: ref,
			Metrics: []Metric{{Name: "score", Value: value, Unit: "pt"}}, Verdict: verdict, EvaluatedAt: time.Now().UTC(),
		}
	}
	table := []struct {
		name        string
		baseVerdict Verdict
		candVerdict Verdict
		want        Verdict
	}{
		{"both pass", VerdictPass, VerdictPass, VerdictPass},
		{"candidate fails", VerdictPass, VerdictFail, VerdictFail},
		{"baseline fails candidate passes", VerdictFail, VerdictPass, VerdictPass},
		{"baseline inconclusive", VerdictInconclusive, VerdictPass, VerdictInconclusive},
		{"candidate inconclusive", VerdictPass, VerdictInconclusive, VerdictInconclusive},
	}
	for _, tc := range table {
		t.Run(tc.name, func(t *testing.T) {
			base := build(RoleBaseline, tc.baseVerdict, 10)
			cand := build(RoleCandidate, tc.candVerdict, 12)
			result, err := CompareResults(base, cand)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.OverallVerdict != tc.want {
				t.Fatalf("want %s, got %s", tc.want, result.OverallVerdict)
			}
			if len(result.Deltas) != 1 || result.Deltas[0].Delta != 2 {
				t.Fatalf("unexpected deltas: %+v", result.Deltas)
			}
		})
	}
}

func TestCompareSuiteMissingResult(t *testing.T) {
	ref, _ := mustTrace(1, []byte("payload"))
	suite := mustSuite(t, ref)
	if _, err := CompareSuite(suite, nil, nil); !errors.Is(err, ErrIncomparableResults) {
		t.Fatalf("expected ErrIncomparableResults, got %v", err)
	}
}

func TestServiceRunComparison(t *testing.T) {
	ctx := context.Background()
	baselineRef, baselineTrace := mustTrace(1, []byte("short"))
	suite := EvaluationSuite{
		ID: "suite-1", Name: "core", SchemaVersion: "v1",
		Cases: []EvaluationCase{{ID: "case-1", Trace: baselineRef, Weight: 1, ExpectedOutcome: "x"}},
	}

	// The suite's case pins a single TraceRef shared by both roles; the fake
	// evaluator differentiates baseline vs candidate scoring via ScoreFunc
	// instead of via distinct trace content.
	source := NewFakeTraceSource()
	source.Seed(baselineTrace)
	evaluator := NewFakeEvaluator()
	evaluator.SetScoreFunc(func(req EvaluationRequest) (EvaluationResult, error) {
		value := float64(len(req.Trace.Payload))
		if req.Role == RoleCandidate {
			value += 5 // deterministic improvement over baseline
		}
		return EvaluationResult{
			CaseID: req.Case.ID, Role: req.Role, TraceRef: req.Trace.Ref,
			Metrics: []Metric{{Name: "score", Value: value, Unit: "pt"}},
			Verdict: VerdictPass, EvaluatedAt: time.Now().UTC(),
		}, nil
	})

	service, err := NewService(source, evaluator, nil)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	result, err := service.RunComparison(ctx, suite, "baseline-1", "hash-b", "candidate-1", "hash-c")
	if err != nil {
		t.Fatalf("RunComparison: %v", err)
	}
	if result.OverallVerdict != VerdictPass {
		t.Fatalf("expected pass, got %s", result.OverallVerdict)
	}
	if len(result.CaseResults) != 1 || result.CaseResults[0].Deltas[0].Delta != 5 {
		t.Fatalf("unexpected comparison result: %+v", result.CaseResults)
	}
}

func TestServiceConcurrentEvaluateCaseRace(t *testing.T) {
	ctx := context.Background()
	ref, trace := mustTrace(1, []byte("payload"))
	suite := mustSuite(t, ref)
	source := NewFakeTraceSource()
	source.Seed(trace)
	evaluator := NewFakeEvaluator()
	service, err := NewService(source, evaluator, nil)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := service.EvaluateCase(ctx, suite, suite.Cases[0], "c1", "h1", RoleCandidate); err != nil {
				t.Errorf("EvaluateCase: %v", err)
			}
		}()
	}
	wg.Wait()
}
