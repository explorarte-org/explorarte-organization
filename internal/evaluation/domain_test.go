package evaluation

import (
	"context"
	"errors"
	"math"
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
		SubjectID: "c1", SubjectArtifactHash: "h1", Role: RoleCandidate,
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
		SubjectID: "c1", SubjectArtifactHash: "h1", Role: RoleCandidate,
	}
	if err := req.Validate(); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("expected ErrInvalidRequest, got %v", err)
	}
}

// TestEvaluationRequestValidateCaseFieldMismatch proves a request cannot
// smuggle in a case that shares an ID and Trace with the suite's real case
// but differs in another field (here, ExpectedOutcome): membership requires
// full struct equality, not just a matching ID and trace.
func TestEvaluationRequestValidateCaseFieldMismatch(t *testing.T) {
	ref, trace := mustTrace(1, []byte("payload"))
	suite := mustSuite(t, ref)
	tampered := suite.Cases[0]
	tampered.ExpectedOutcome = "a different expectation than the suite's"
	req := EvaluationRequest{
		Suite: suite, Case: tampered, Trace: trace,
		SubjectID: "c1", SubjectArtifactHash: "h1", Role: RoleCandidate,
	}
	if err := req.Validate(); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("expected ErrInvalidRequest for a tampered case, got %v", err)
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

// TestCompareResultsTraceRefMismatch proves two results that share a CaseID
// but were evaluated against different traces are rejected, even though
// nothing else about them looks wrong.
func TestCompareResultsTraceRefMismatch(t *testing.T) {
	ref1, _ := mustTrace(1, []byte("payload-1"))
	ref2, _ := mustTrace(2, []byte("payload-2"))
	base := EvaluationResult{
		CaseID: "case-1", Role: RoleBaseline, TraceRef: ref1,
		Metrics: []Metric{{Name: "m", Value: 1, Unit: "u"}}, Verdict: VerdictPass, EvaluatedAt: time.Now().UTC(),
	}
	cand := base
	cand.Role = RoleCandidate
	cand.TraceRef = ref2
	if _, err := CompareResults(base, cand); !errors.Is(err, ErrIncomparableResults) {
		t.Fatalf("expected ErrIncomparableResults for trace ref mismatch, got %v", err)
	}
}

// TestCompareResultsNonFiniteDelta proves that two individually finite
// metric values whose subtraction overflows to +/-Inf are rejected instead
// of silently producing a non-finite MetricDelta.
func TestCompareResultsNonFiniteDelta(t *testing.T) {
	ref, _ := mustTrace(1, []byte("payload"))
	base := EvaluationResult{
		CaseID: "case-1", Role: RoleBaseline, TraceRef: ref,
		Metrics: []Metric{{Name: "m", Value: -math.MaxFloat64, Unit: "u"}}, Verdict: VerdictPass, EvaluatedAt: time.Now().UTC(),
	}
	cand := base
	cand.Role = RoleCandidate
	cand.Metrics = []Metric{{Name: "m", Value: math.MaxFloat64, Unit: "u"}}
	if _, err := CompareResults(base, cand); !errors.Is(err, ErrIncomparableResults) {
		t.Fatalf("expected ErrIncomparableResults for a non-finite delta, got %v", err)
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

func TestCompareSuiteWeightedPassRatio(t *testing.T) {
	ref1, _ := mustTrace(1, []byte("payload-1"))
	ref2, _ := mustTrace(2, []byte("payload-2"))
	suite := EvaluationSuite{
		ID: "suite-w", Name: "weighted", SchemaVersion: "v1",
		Cases: []EvaluationCase{
			{ID: "heavy", Trace: ref1, Weight: 3, ExpectedOutcome: "x"},
			{ID: "light", Trace: ref2, Weight: 1, ExpectedOutcome: "y"},
		},
	}
	if err := suite.Validate(); err != nil {
		t.Fatalf("suite must be valid: %v", err)
	}
	build := func(caseID string, role EvaluationRole, ref TraceRef, verdict Verdict) EvaluationResult {
		return EvaluationResult{
			CaseID: caseID, Role: role, TraceRef: ref,
			Metrics: []Metric{{Name: "m", Value: 1, Unit: "u"}}, Verdict: verdict, EvaluatedAt: time.Now().UTC(),
		}
	}
	baseline := []EvaluationResult{
		build("heavy", RoleBaseline, ref1, VerdictPass),
		build("light", RoleBaseline, ref2, VerdictPass),
	}
	candidate := []EvaluationResult{
		build("heavy", RoleCandidate, ref1, VerdictPass),
		build("light", RoleCandidate, ref2, VerdictFail),
	}
	result, err := CompareSuite(suite, baseline, candidate)
	if err != nil {
		t.Fatalf("CompareSuite: %v", err)
	}
	// heavy (weight 3) passes, light (weight 1) fails: 3/4 = 0.75.
	if got, want := result.WeightedPassRatio, 0.75; got != want {
		t.Fatalf("WeightedPassRatio = %v, want %v", got, want)
	}
	// A single failing case still poisons the safety-gating verdict,
	// regardless of how small its weight is.
	if result.OverallVerdict != VerdictFail {
		t.Fatalf("OverallVerdict = %s, want fail despite high weighted pass ratio", result.OverallVerdict)
	}
	for _, cr := range result.CaseResults {
		var wantWeight float64
		switch cr.CaseID {
		case "heavy":
			wantWeight = 3
		case "light":
			wantWeight = 1
		}
		if cr.Weight != wantWeight {
			t.Fatalf("case %s: Weight = %v, want %v", cr.CaseID, cr.Weight, wantWeight)
		}
	}
}

// TestCompareSuiteWrongPinnedTrace proves a result that names the right
// CaseID but was actually evaluated against a trace other than the one the
// suite's case pins is rejected, even though CompareResults alone (which
// only sees the two results, not the suite) couldn't tell.
func TestCompareSuiteWrongPinnedTrace(t *testing.T) {
	ref, _ := mustTrace(1, []byte("payload"))
	suite := mustSuite(t, ref)
	wrongRef, _ := mustTrace(2, []byte("other"))
	build := func(role EvaluationRole, traceRef TraceRef) EvaluationResult {
		return EvaluationResult{
			CaseID: suite.Cases[0].ID, Role: role, TraceRef: traceRef,
			Metrics: []Metric{{Name: "m", Value: 1, Unit: "u"}}, Verdict: VerdictPass, EvaluatedAt: time.Now().UTC(),
		}
	}
	baseline := []EvaluationResult{build(RoleBaseline, wrongRef)}
	candidate := []EvaluationResult{build(RoleCandidate, wrongRef)}
	if _, err := CompareSuite(suite, baseline, candidate); !errors.Is(err, ErrIncomparableResults) {
		t.Fatalf("expected ErrIncomparableResults for a result pinned to the wrong trace, got %v", err)
	}
}

func TestSuiteComparisonResultValidateRejectsEmpty(t *testing.T) {
	empty := SuiteComparisonResult{SuiteID: "suite-1", OverallVerdict: VerdictPass}
	if err := empty.Validate(); err == nil {
		t.Fatal("expected an empty comparison (no case results) to be rejected")
	}
	withCase := empty
	withCase.CaseResults = []ComparisonResult{
		{CaseID: "case-1", BaselineVerdict: VerdictPass, CandidateVerdict: VerdictPass, OverallVerdict: VerdictPass},
	}
	if err := withCase.Validate(); err != nil {
		t.Fatalf("expected a well-formed comparison to be valid, got %v", err)
	}
}

func TestCompareSuiteMissingResult(t *testing.T) {
	ref, _ := mustTrace(1, []byte("payload"))
	suite := mustSuite(t, ref)
	if _, err := CompareSuite(suite, nil, nil); !errors.Is(err, ErrIncomparableResults) {
		t.Fatalf("expected ErrIncomparableResults, got %v", err)
	}
}

// TestServiceEvaluateCaseRejectsMismatchedEvaluatorResult proves EvaluateCase
// does not trust an Evaluator's returned CaseID/Role/TraceRef: a buggy or
// malicious Evaluator returning a result for the wrong case must not slip
// through, since downstream comparisons index results by CaseID.
func TestServiceEvaluateCaseRejectsMismatchedEvaluatorResult(t *testing.T) {
	ctx := context.Background()
	ref, trace := mustTrace(1, []byte("payload"))
	suite := mustSuite(t, ref)
	source := NewFakeTraceSource()
	source.Seed(trace)
	evaluator := NewFakeEvaluator()
	evaluator.SetScoreFunc(func(req EvaluationRequest) (EvaluationResult, error) {
		return EvaluationResult{
			CaseID: "some-other-case", Role: req.Role, TraceRef: req.Trace.Ref,
			Metrics: []Metric{{Name: "m", Value: 1, Unit: "u"}}, Verdict: VerdictPass, EvaluatedAt: time.Now().UTC(),
		}, nil
	})
	service, err := NewService(source, evaluator, nil)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if _, err := service.EvaluateCase(ctx, suite, suite.Cases[0], "c1", "h1", RoleCandidate); !errors.Is(err, ErrInvalidResult) {
		t.Fatalf("expected ErrInvalidResult for a mismatched evaluator response, got %v", err)
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
