package search

type SufficiencyInput struct {
	Results      []SearchResult
	Intent       SearchIntent
	RequireMulti bool
}

type SufficiencyOutput struct {
	Sufficient bool
	Reason     string
}

type SufficiencyEvaluator interface {
	Evaluate(in SufficiencyInput) SufficiencyOutput
}

type DefaultSufficiencyEvaluator struct{}

func NewDefaultSufficiencyEvaluator() *DefaultSufficiencyEvaluator {
	return &DefaultSufficiencyEvaluator{}
}

func (e *DefaultSufficiencyEvaluator) Evaluate(in SufficiencyInput) SufficiencyOutput {
	// Determine minimum results threshold based on intent
	minResults := 3
	switch in.Intent {
	case IntentNews:
		minResults = 5
	case IntentBookGeneral, IntentBookAcademicOA, IntentBookPublicDomain:
		minResults = 2
	case IntentAcademic, IntentBiomedical, IntentPreprint:
		minResults = 3
	}

	if len(in.Results) < minResults {
		return SufficiencyOutput{Sufficient: false, Reason: "insufficient results"}
	}

	if in.RequireMulti {
		// Check diversity based on SourceType
		sources := make(map[string]struct{})
		for _, r := range in.Results {
			if r.SourceType != "" {
				sources[r.SourceType] = struct{}{}
			}
		}
		if len(sources) < 2 {
			return SufficiencyOutput{Sufficient: false, Reason: "require independent sources but less than 2 source types"}
		}
	}

	return SufficiencyOutput{Sufficient: true, Reason: ""}
}
