package evaluation

import (
	"fmt"
	"math"
)

// MetricDelta is the difference between a baseline and a candidate
// measurement of the same named metric.
type MetricDelta struct {
	Name           string
	BaselineValue  float64
	CandidateValue float64
	Delta          float64
	Unit           string
}

// ComparisonResult is the outcome of comparing one case's baseline result
// against its candidate result.
type ComparisonResult struct {
	CaseID           string
	Weight           float64
	BaselineVerdict  Verdict
	CandidateVerdict Verdict
	Deltas           []MetricDelta
	OverallVerdict   Verdict
}

// CompareResults compares a baseline and a candidate EvaluationResult for the
// same case. Both must already be individually valid.
func CompareResults(baseline, candidate EvaluationResult) (ComparisonResult, error) {
	if err := baseline.Validate(); err != nil {
		return ComparisonResult{}, err
	}
	if err := candidate.Validate(); err != nil {
		return ComparisonResult{}, err
	}
	if baseline.Role != RoleBaseline {
		return ComparisonResult{}, fmt.Errorf("%w: expected a baseline result, got role %q", ErrIncomparableResults, baseline.Role)
	}
	if candidate.Role != RoleCandidate {
		return ComparisonResult{}, fmt.Errorf("%w: expected a candidate result, got role %q", ErrIncomparableResults, candidate.Role)
	}
	if baseline.CaseID != candidate.CaseID {
		return ComparisonResult{}, fmt.Errorf("%w: %s vs %s", ErrCaseMismatch, baseline.CaseID, candidate.CaseID)
	}
	if baseline.TraceRef != candidate.TraceRef {
		return ComparisonResult{}, fmt.Errorf("%w: case %s: baseline and candidate reference different traces", ErrIncomparableResults, baseline.CaseID)
	}

	baselineByName := make(map[string]Metric, len(baseline.Metrics))
	for _, m := range baseline.Metrics {
		baselineByName[m.Name] = m
	}
	if len(candidate.Metrics) != len(baseline.Metrics) {
		return ComparisonResult{}, fmt.Errorf("%w: case %s: metric sets differ in size", ErrIncomparableResults, baseline.CaseID)
	}

	deltas := make([]MetricDelta, 0, len(candidate.Metrics))
	for _, cm := range candidate.Metrics {
		bm, ok := baselineByName[cm.Name]
		if !ok {
			return ComparisonResult{}, fmt.Errorf("%w: case %s: candidate metric %s has no baseline counterpart", ErrIncomparableResults, baseline.CaseID, cm.Name)
		}
		if bm.Unit != cm.Unit {
			return ComparisonResult{}, fmt.Errorf("%w: case %s: metric %s unit mismatch (%s vs %s)", ErrIncomparableResults, baseline.CaseID, cm.Name, bm.Unit, cm.Unit)
		}
		delta := cm.Value - bm.Value
		if math.IsNaN(delta) || math.IsInf(delta, 0) {
			return ComparisonResult{}, fmt.Errorf("%w: case %s: metric %s delta is not finite", ErrIncomparableResults, baseline.CaseID, cm.Name)
		}
		deltas = append(deltas, MetricDelta{
			Name:           cm.Name,
			BaselineValue:  bm.Value,
			CandidateValue: cm.Value,
			Delta:          delta,
			Unit:           cm.Unit,
		})
	}

	return ComparisonResult{
		CaseID:           baseline.CaseID,
		BaselineVerdict:  baseline.Verdict,
		CandidateVerdict: candidate.Verdict,
		Deltas:           deltas,
		OverallVerdict:   overallVerdict(baseline.Verdict, candidate.Verdict),
	}, nil
}

// overallVerdict reduces a baseline/candidate verdict pair to a single
// verdict: any inconclusive input is inconclusive, any candidate failure
// fails, otherwise the candidate passes.
func overallVerdict(baseline, candidate Verdict) Verdict {
	switch {
	case baseline == VerdictInconclusive || candidate == VerdictInconclusive:
		return VerdictInconclusive
	case candidate == VerdictFail:
		return VerdictFail
	default:
		return VerdictPass
	}
}

// SuiteComparisonResult aggregates per-case comparisons across a whole suite.
type SuiteComparisonResult struct {
	SuiteID     string
	CaseResults []ComparisonResult
	// OverallVerdict is the safety-gating verdict: a single failing or
	// inconclusive case poisons it, regardless of case weight. It is what
	// improvement.Service.RecordEvaluationVerdict acts on.
	OverallVerdict Verdict
	// WeightedPassRatio is the fraction of total case weight that passed,
	// in [0, 1]. It is informational context for reviewers and an
	// ApprovalGate; it never overrides OverallVerdict.
	WeightedPassRatio float64
}

// Validate rejects a fabricated or empty comparison: callers (in particular
// internal/improvement) must not be able to approve or promote a candidate
// on an evaluation that never actually ran any case.
func (r SuiteComparisonResult) Validate() error {
	if r.SuiteID == "" {
		return fmt.Errorf("%w: suite comparison requires a suite id", ErrInvalidSuite)
	}
	if !r.OverallVerdict.Valid() {
		return fmt.Errorf("%w: suite %s: invalid overall verdict %q", ErrInvalidResult, r.SuiteID, r.OverallVerdict)
	}
	if len(r.CaseResults) == 0 {
		return fmt.Errorf("%w: suite %s has no case results", ErrEmptySuite, r.SuiteID)
	}
	for i, cr := range r.CaseResults {
		if cr.CaseID == "" {
			return fmt.Errorf("%w: suite %s: case result %d requires a case id", ErrInvalidResult, r.SuiteID, i)
		}
		if !cr.BaselineVerdict.Valid() || !cr.CandidateVerdict.Valid() || !cr.OverallVerdict.Valid() {
			return fmt.Errorf("%w: suite %s: case result %d (%s) has an invalid verdict", ErrInvalidResult, r.SuiteID, i, cr.CaseID)
		}
	}
	return nil
}

// CompareSuite compares every case in a suite between baseline and candidate
// result sets. Both slices must cover exactly the same set of case IDs.
func CompareSuite(suite EvaluationSuite, baselineResults, candidateResults []EvaluationResult) (SuiteComparisonResult, error) {
	if err := suite.Validate(); err != nil {
		return SuiteComparisonResult{}, err
	}
	if len(baselineResults) != len(suite.Cases) || len(candidateResults) != len(suite.Cases) {
		return SuiteComparisonResult{}, fmt.Errorf("%w: suite %s: result count does not match case count", ErrIncomparableResults, suite.ID)
	}

	baselineByCase := make(map[string]EvaluationResult, len(baselineResults))
	for _, r := range baselineResults {
		baselineByCase[r.CaseID] = r
	}
	candidateByCase := make(map[string]EvaluationResult, len(candidateResults))
	for _, r := range candidateResults {
		candidateByCase[r.CaseID] = r
	}

	caseResults := make([]ComparisonResult, 0, len(suite.Cases))
	overall := VerdictPass
	var totalWeight, passedWeight float64
	for _, c := range suite.Cases {
		b, ok := baselineByCase[c.ID]
		if !ok {
			return SuiteComparisonResult{}, fmt.Errorf("%w: suite %s: missing baseline result for case %s", ErrIncomparableResults, suite.ID, c.ID)
		}
		cand, ok := candidateByCase[c.ID]
		if !ok {
			return SuiteComparisonResult{}, fmt.Errorf("%w: suite %s: missing candidate result for case %s", ErrIncomparableResults, suite.ID, c.ID)
		}
		if b.TraceRef != c.Trace {
			return SuiteComparisonResult{}, fmt.Errorf("%w: suite %s: baseline result for case %s references a different trace than the suite pins", ErrIncomparableResults, suite.ID, c.ID)
		}
		if cand.TraceRef != c.Trace {
			return SuiteComparisonResult{}, fmt.Errorf("%w: suite %s: candidate result for case %s references a different trace than the suite pins", ErrIncomparableResults, suite.ID, c.ID)
		}
		cr, err := CompareResults(b, cand)
		if err != nil {
			return SuiteComparisonResult{}, err
		}
		cr.Weight = c.Weight
		caseResults = append(caseResults, cr)
		overall = worseVerdict(overall, cr.OverallVerdict)
		totalWeight += c.Weight
		if cr.OverallVerdict == VerdictPass {
			passedWeight += c.Weight
		}
	}

	return SuiteComparisonResult{
		SuiteID:           suite.ID,
		CaseResults:       caseResults,
		OverallVerdict:    overall,
		WeightedPassRatio: passedWeight / totalWeight,
	}, nil
}

// worseVerdict ranks Fail worse than Inconclusive worse than Pass, so a
// single failing or inconclusive case poisons the whole suite verdict.
func worseVerdict(a, b Verdict) Verdict {
	rank := func(v Verdict) int {
		switch v {
		case VerdictFail:
			return 2
		case VerdictInconclusive:
			return 1
		default:
			return 0
		}
	}
	if rank(b) > rank(a) {
		return b
	}
	return a
}
