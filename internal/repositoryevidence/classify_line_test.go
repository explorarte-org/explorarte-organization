package repositoryevidence

import "testing"

// LineDeclares borrows ClassifyExcerpt's rule for one line. These pin the
// cases that matter to discovery: a declaration is a field, a binding or a
// keyword introduction; a composite-literal key, a mid-line use, a selector
// or prose in a comment is not.
func TestLineDeclares(t *testing.T) {
	for line, want := range map[string]bool{
		"\tMaxDesignRounds int":                          true,
		"MaxDesignRounds int":                            true,
		"func MaxDesignRounds() {}":                      true,
		"type MaxDesignRounds struct{}":                  true,
		"var MaxDesignRounds = 2":                        true,
		"const MaxDesignRounds = 2":                      true,
		"MaxDesignRounds := 2":                           true,
		"\t\tMaxDesignRounds: 2,":                        false,
		"\tif round >= o.limits.MaxDesignRounds {":       false,
		"return limits.MaxDesignRounds":                  false,
		"// MaxDesignRounds bounds the design loop":      false,
		"{Subject: \"MaxDesignRounds\", Relation: \"\"}": false,
		"": false,
	} {
		if got := LineDeclares(line, "MaxDesignRounds"); got != want {
			t.Errorf("LineDeclares(%q)=%v want %v", line, got, want)
		}
	}
	if LineDeclares("anything MaxDesignRounds", "  ") {
		t.Error("an empty symbol must never declare")
	}
}

// The one-line predicate and the excerpt classifier must agree on where the
// excerpt contains exactly one line: two spellings of one rule would let
// discovery promote what classification would later refuse.
func TestLinePredicateAgreesWithExcerptClassifier(t *testing.T) {
	for _, line := range []string{
		"\tMaxDesignRounds int",
		"\t\tMaxDesignRounds: 2,",
		"\tif round >= o.limits.MaxDesignRounds {",
		"// MaxDesignRounds bounds the design loop",
	} {
		single := line + "\n"
		relation, mentions := ClassifyExcerpt(single, "MaxDesignRounds")
		if !mentions {
			t.Fatalf("%q must at least mention the symbol", line)
		}
		if declares := LineDeclares(line, "MaxDesignRounds"); declares != (relation == RelationDefinition) {
			t.Errorf("disagreement on %q: LineDeclares=%v, ClassifyExcerpt=%q", line, declares, relation)
		}
	}
}
