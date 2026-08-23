package repositoryevidence

import "testing"

// AUTONOMY-SMOKE-017-R5 asked a design to cite where two limits are DEFINED
// and where they are APPLIED. It cited a test fixture as the definition,
// because the file where both are declared was never in its context.
//
// The reading was alphabetical, so the two identifiers the goal named sorted
// ninth and tenth behind eight incidental capitalised words, and the file
// budget was gone before either could claim it. Five of those eight came from
// the host's own egress rule, appended to the same instructions the query is
// derived from.

const r5Goal = `AUTONOMY-SMOKE-017: diagnosticar cómo internal/executive gobierna por
separado las revisiones de diseño y los replans departamentales. Hacer el
cambio mínimo que convierta esa separación en una decisión host explícita para
ambos loops. ALCANCE PERMITIDO: código productivo de internal/executive.
PROHIBIDO: cambiar los valores por defecto de MaxDesignRounds o
MaxDepartmentReplans. Investigate the repository. Document separate authorized
repository references. Produce a short inventory.`

// What the goal names outranks what its prose happens to contain.
func TestNamedSymbolsAreSearchedBeforeIncidentalWords(t *testing.T) {
	selection := SelectionFromText(r5Goal, 24)
	position := map[string]int{}
	for index, term := range selection.Terms {
		position[term] = index
	}
	for _, symbol := range []string{"MaxDesignRounds", "MaxDepartmentReplans"} {
		rank, found := position[symbol]
		if !found {
			t.Fatalf("%s was not searched for at all", symbol)
		}
		for _, prose := range []string{"ALCANCE", "PERMITIDO", "PROHIBIDO", "Investigate", "Document", "Produce"} {
			if proseRank, alsoFound := position[prose]; alsoFound && proseRank < rank {
				t.Errorf("%q is searched before %s (%d before %d): incidental prose spends the budget first",
					prose, symbol, proseRank, rank)
			}
		}
	}
}

// The ordering must still be a function of the goal alone, or a design stops
// being reproducible from what was asked.
func TestTheSameGoalAlwaysProducesTheSameReading(t *testing.T) {
	first := SelectionFromText(r5Goal, 24)
	second := SelectionFromText(r5Goal, 24)
	if len(first.Terms) != len(second.Terms) {
		t.Fatalf("term count is not stable: %d vs %d", len(first.Terms), len(second.Terms))
	}
	for index := range first.Terms {
		if first.Terms[index] != second.Terms[index] {
			t.Fatalf("reading is not reproducible at %d: %q vs %q", index, first.Terms[index], second.Terms[index])
		}
	}
}

// Ranking must not silently drop searches: prose still gets looked at, after
// the identifiers, if there is budget left.
func TestProseTermsAreDemotedNotDiscarded(t *testing.T) {
	selection := SelectionFromText(r5Goal, 24)
	present := map[string]bool{}
	for _, term := range selection.Terms {
		present[term] = true
	}
	for _, prose := range []string{"ALCANCE", "Investigate"} {
		if !present[prose] {
			t.Fatalf("%q was discarded rather than demoted", prose)
		}
	}
}

func TestWhatCountsAsAnIdentifier(t *testing.T) {
	for term, want := range map[string]bool{
		"MaxDesignRounds": true, "MaxDepartmentReplans": true, "TestSomething": true,
		"ValidateSourceMetadata": true,
		"ALCANCE":                false, "PROHIBIDO": false, "EVIDENCE": false,
		"Investigate": false, "Document": false, "Encoding": false,
	} {
		if got := looksLikeIdentifier(term); got != want {
			t.Errorf("looksLikeIdentifier(%q)=%v want %v", term, got, want)
		}
	}
}
