package evaluation

import (
	"context"
	"errors"
	"fmt"
)

// Service orchestrates loading traces and running an evaluation suite for
// either a baseline or a candidate, and reduces the results into a
// comparison. It holds no state of its own beyond its ports.
type Service struct {
	traces    TraceSource
	evaluator Evaluator
	clock     Clock
}

func NewService(traces TraceSource, evaluator Evaluator, clock Clock) (*Service, error) {
	if traces == nil {
		return nil, errors.New("evaluation service requires a trace source")
	}
	if evaluator == nil {
		return nil, errors.New("evaluation service requires an evaluator")
	}
	if clock == nil {
		clock = SystemClock{}
	}
	return &Service{traces: traces, evaluator: evaluator, clock: clock}, nil
}

// EvaluateCase loads the trace for one case and scores it for the given
// role, returning a fully validated result that is verified to actually be
// about the requested case, role and trace: an Evaluator is untrusted input,
// and a mismatched result would otherwise corrupt downstream comparisons
// that index by CaseID.
func (s *Service) EvaluateCase(ctx context.Context, suite EvaluationSuite, c EvaluationCase, subjectID, subjectArtifactHash string, role EvaluationRole) (EvaluationResult, error) {
	if err := suite.Validate(); err != nil {
		return EvaluationResult{}, err
	}
	if err := c.Validate(); err != nil {
		return EvaluationResult{}, err
	}
	trace, err := s.traces.LoadTrace(ctx, c.Trace)
	if err != nil {
		return EvaluationResult{}, fmt.Errorf("load evaluation trace for case %s: %w", c.ID, err)
	}
	request := EvaluationRequest{
		Suite:               suite,
		Case:                c,
		Trace:               trace,
		SubjectID:           subjectID,
		SubjectArtifactHash: subjectArtifactHash,
		Role:                role,
	}
	if err := request.Validate(); err != nil {
		return EvaluationResult{}, err
	}
	result, err := s.evaluator.Evaluate(ctx, request)
	if err != nil {
		return EvaluationResult{}, fmt.Errorf("evaluate case %s: %w", c.ID, err)
	}
	if result.CaseID != c.ID || result.Role != role || result.TraceRef != trace.Ref {
		return EvaluationResult{}, fmt.Errorf("%w: evaluator returned a result for case %q/role %q/trace %d, requested case %q/role %q/trace %d",
			ErrInvalidResult, result.CaseID, result.Role, result.TraceRef.RunID, c.ID, role, trace.Ref.RunID)
	}
	if result.EvaluatedAt.IsZero() {
		result.EvaluatedAt = s.clock.Now().UTC()
	}
	if err := result.Validate(); err != nil {
		return EvaluationResult{}, err
	}
	return result, nil
}

// EvaluateSuite runs every case in the suite for the given role, in order,
// stopping at the first error.
func (s *Service) EvaluateSuite(ctx context.Context, suite EvaluationSuite, subjectID, subjectArtifactHash string, role EvaluationRole) ([]EvaluationResult, error) {
	if err := suite.Validate(); err != nil {
		return nil, err
	}
	results := make([]EvaluationResult, 0, len(suite.Cases))
	for _, c := range suite.Cases {
		result, err := s.EvaluateCase(ctx, suite, c, subjectID, subjectArtifactHash, role)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, nil
}

// RunComparison evaluates the suite for both the baseline and the candidate
// and reduces the results to a SuiteComparisonResult.
func (s *Service) RunComparison(ctx context.Context, suite EvaluationSuite, baselineID, baselineArtifactHash, candidateID, candidateArtifactHash string) (SuiteComparisonResult, error) {
	baselineResults, err := s.EvaluateSuite(ctx, suite, baselineID, baselineArtifactHash, RoleBaseline)
	if err != nil {
		return SuiteComparisonResult{}, err
	}
	candidateResults, err := s.EvaluateSuite(ctx, suite, candidateID, candidateArtifactHash, RoleCandidate)
	if err != nil {
		return SuiteComparisonResult{}, err
	}
	return CompareSuite(suite, baselineResults, candidateResults)
}
