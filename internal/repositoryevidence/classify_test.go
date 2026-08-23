package repositoryevidence

import "testing"

// The host must be able to say WHY it believes an excerpt is a definition, and
// anyone must be able to check by reading the line. Where it cannot tell it
// must say application, so a definition requirement goes unsupplied rather
// than being declared satisfied by a use.
func TestWhatTheHostCanCallADefinition(t *testing.T) {
	cases := []struct {
		name    string
		content string
		symbol  string
		want    string
	}{
		{"struct field declaration", "type Limits struct {\n\tMaxDepartmentReplans   int\n\tMaxDesignRounds    int\n}", "MaxDesignRounds", RelationDefinition},
		{"function declaration", "func replanCapacityRemains(key string, limit int) bool {", "replanCapacityRemains", RelationDefinition},
		{"method declaration", "func (o *Orchestrator) driveDesignFreeze(ctx context.Context) error {", "driveDesignFreeze", RelationDefinition},
		{"const binding", "const declassifyMinimumRun = 48", "declassifyMinimumRun", RelationDefinition},
		{"use behind a selector", "\tif round > o.limits.MaxDesignRounds {\n\t\treturn nil\n\t}", "MaxDesignRounds", RelationApplication},
		{"use inside an expression", "\tmax := (2*departments + 2*l.MaxDepartmentReplans) * attempts", "MaxDepartmentReplans", RelationApplication},
		{"value in a composite literal", "\treturn Limits{MaxDepartmentReplans: 1, MaxDesignRounds: 2}", "MaxDesignRounds", RelationApplication},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			relation, mentions := ClassifyExcerpt(test.content, test.symbol)
			if !mentions {
				t.Fatalf("the excerpt does not mention %s at all", test.symbol)
			}
			if relation != test.want {
				t.Fatalf("classified as %q, want %q", relation, test.want)
			}
		})
	}
}

func TestAnExcerptThatNeverNamesTheSymbolSuppliesNothing(t *testing.T) {
	if _, mentions := ClassifyExcerpt("func unrelated() {}", "MaxDesignRounds"); mentions {
		t.Fatal("an excerpt that never names the symbol was offered as evidence for it")
	}
}
