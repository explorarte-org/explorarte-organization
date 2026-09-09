package search

import (
	"testing"
)

func TestSufficiency_ZeroResults_Insufficient(t *testing.T) {
	eval := &DefaultSufficiencyEvaluator{}
	out := eval.Evaluate(SufficiencyInput{
		Results: []SearchResult{},
		Intent:  IntentWebGeneral,
	})
	if out.Sufficient {
		t.Error("zero results should be insufficient")
	}
}

func TestSufficiency_OneResult_Insufficient(t *testing.T) {
	eval := &DefaultSufficiencyEvaluator{}
	out := eval.Evaluate(SufficiencyInput{
		Results: []SearchResult{
			{Title: "Result 1", URL: "http://example.com/1"},
		},
		Intent: IntentWebGeneral,
	})
	if out.Sufficient {
		t.Error("one result should be insufficient")
	}
}

func TestSufficiency_TwoResults_Insufficient(t *testing.T) {
	eval := &DefaultSufficiencyEvaluator{}
	out := eval.Evaluate(SufficiencyInput{
		Results: []SearchResult{
			{Title: "Result 1", URL: "http://example.com/1"},
			{Title: "Result 2", URL: "http://example.com/2"},
		},
		Intent: IntentWebGeneral,
	})
	if out.Sufficient {
		t.Error("two results should be insufficient for web_general")
	}
}

func TestSufficiency_ThreeOrMoreResults_Sufficient(t *testing.T) {
	eval := &DefaultSufficiencyEvaluator{}
	out := eval.Evaluate(SufficiencyInput{
		Results: []SearchResult{
			{Title: "Result 1", URL: "http://example.com/1"},
			{Title: "Result 2", URL: "http://example.com/2"},
			{Title: "Result 3", URL: "http://example.com/3"},
		},
		Intent: IntentWebGeneral,
	})
	if !out.Sufficient {
		t.Error("three results should be sufficient for web_general")
	}
}

func TestSufficiency_RequireIndependentSources_OneDomain_Insufficient(t *testing.T) {
	eval := &DefaultSufficiencyEvaluator{}
	out := eval.Evaluate(SufficiencyInput{
		Results: []SearchResult{
			{Title: "Result 1", URL: "http://example.com/1", SourceType: "web"},
			{Title: "Result 2", URL: "http://example.com/2", SourceType: "web"},
			{Title: "Result 3", URL: "http://example.com/3", SourceType: "web"},
		},
		Intent:       IntentWebGeneral,
		RequireMulti: true,
	})
	if out.Sufficient {
		t.Error("RequireMulti with 1 domain should be insufficient")
	}
}

func TestSufficiency_RequireIndependentSources_TwoDomains_Sufficient(t *testing.T) {
	eval := &DefaultSufficiencyEvaluator{}
	out := eval.Evaluate(SufficiencyInput{
		Results: []SearchResult{
			{Title: "Result 1", URL: "http://example.com/1", SourceType: "academic"},
			{Title: "Result 2", URL: "http://example.com/2", SourceType: "academic"},
			{Title: "Result 3", URL: "http://other.com/3", SourceType: "news"},
		},
		Intent:       IntentWebGeneral,
		RequireMulti: true,
	})
	if !out.Sufficient {
		t.Error("RequireMulti with 2 source types should be sufficient")
	}
}

func TestSufficiency_AcademicIntent_RequiresThreeResults(t *testing.T) {
	eval := &DefaultSufficiencyEvaluator{}
	out := eval.Evaluate(SufficiencyInput{
		Results: []SearchResult{
			{Title: "Result 1", URL: "http://example.com/1"},
			{Title: "Result 2", URL: "http://example.com/2"},
		},
		Intent: IntentAcademic,
	})
	if out.Sufficient {
		t.Error("academic intent should require 3+ results")
	}
}

func TestSufficiency_BookIntent_RequiresTwoResults(t *testing.T) {
	eval := &DefaultSufficiencyEvaluator{}
	out := eval.Evaluate(SufficiencyInput{
		Results: []SearchResult{
			{Title: "Book 1", URL: "http://example.com/book1"},
		},
		Intent: IntentBookGeneral,
	})
	if out.Sufficient {
		t.Error("book_general intent should require 2+ results")
	}

	out = eval.Evaluate(SufficiencyInput{
		Results: []SearchResult{
			{Title: "Book 1", URL: "http://example.com/book1"},
			{Title: "Book 2", URL: "http://example.com/book2"},
		},
		Intent: IntentBookGeneral,
	})
	if !out.Sufficient {
		t.Error("book_general with 2 results should be sufficient")
	}
}

func TestSufficiency_NewsIntent_RequiresFiveResults(t *testing.T) {
	eval := &DefaultSufficiencyEvaluator{}
	out := eval.Evaluate(SufficiencyInput{
		Results: []SearchResult{
			{Title: "News 1", URL: "http://example.com/1"},
			{Title: "News 2", URL: "http://example.com/2"},
			{Title: "News 3", URL: "http://example.com/3"},
			{Title: "News 4", URL: "http://example.com/4"},
		},
		Intent: IntentNews,
	})
	if out.Sufficient {
		t.Error("news intent should require 5+ results")
	}
}
