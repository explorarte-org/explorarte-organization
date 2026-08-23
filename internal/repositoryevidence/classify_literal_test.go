package repositoryevidence

import "testing"

// A composite literal spread over several lines puts its keys at the start of
// a line, exactly where a declaration sits. DefaultLimits in this very
// repository is written that way, so an excerpt carrying only the default
// value of a field would otherwise let the host claim it had supplied the
// field's definition -- a false preflight, which is the one thing the supply
// check must never be.
func TestCompositeLiteralKeyAtLineStartIsNotADefinition(t *testing.T) {
	content := "func DefaultLimits() Limits {\n\treturn Limits{\n\t\tMaxDepartmentReplans: 1,\n\t\tMaxDesignRounds:      2,\n\t}\n}"
	for _, symbol := range []string{"MaxDepartmentReplans", "MaxDesignRounds"} {
		relation, mentions := ClassifyExcerpt(content, symbol)
		if !mentions {
			t.Fatalf("%s is not even mentioned", symbol)
		}
		if relation != RelationApplication {
			t.Fatalf("%s: a default value was classified as %q", symbol, relation)
		}
	}
}

// A short variable declaration really does introduce a name, and the colon it
// carries must not be mistaken for a literal key.
func TestAShortVariableDeclarationIsStillADefinition(t *testing.T) {
	relation, _ := ClassifyExcerpt("\tMaxDesignRounds := 2\n", "MaxDesignRounds")
	if relation != RelationDefinition {
		t.Fatalf("a short variable declaration was classified as %q", relation)
	}
}

// And the real declaration still is one, so the rule did not simply get
// stricter until nothing qualifies.
func TestTheFieldDeclarationIsStillADefinition(t *testing.T) {
	content := "type Limits struct {\n\tMaxDepartmentReplans   int\n\tMaxDesignRounds    int\n}"
	for _, symbol := range []string{"MaxDepartmentReplans", "MaxDesignRounds"} {
		if relation, _ := ClassifyExcerpt(content, symbol); relation != RelationDefinition {
			t.Fatalf("%s: the declaration was classified as %q", symbol, relation)
		}
	}
}
