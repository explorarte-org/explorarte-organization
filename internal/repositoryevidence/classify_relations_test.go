package repositoryevidence

import "testing"

// An excerpt that physically contains a declaration AND a use demonstrates
// both roles. Collapsing it to one hid every co-located application from the
// probe and the preflight, no matter what discovery reported.
func TestExcerptRelationsReportsEveryRoleAnExcerptContains(t *testing.T) {
	cases := []struct {
		name        string
		content     string
		symbol      string
		definition  bool
		application bool
	}{
		{"declaration and use together", "type Limits struct {\n\tMaxDesignRounds int\n}\n\nfunc check(l Limits) bool {\n\treturn l.MaxDesignRounds > 0\n}\n", "MaxDesignRounds", true, true},
		{"declaration only", "type Limits struct {\n\tMaxDesignRounds int\n}\n", "MaxDesignRounds", true, false},
		{"use only", "return round >= limits.MaxDesignRounds\n", "MaxDesignRounds", false, true},
		{"composite literal key is not a definition", "return Limits{\n\tMaxDesignRounds: 2,\n}\n", "MaxDesignRounds", false, true},
		{"twenty uses stay one application role", "use(MaxDesignRounds)\nuse(MaxDesignRounds)\nuse(MaxDesignRounds)\n", "MaxDesignRounds", false, true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			relations := ExcerptRelations(test.content, test.symbol)
			if relations[RelationDefinition] != test.definition {
				t.Errorf("definition=%v, want %v (%v)", relations[RelationDefinition], test.definition, relations)
			}
			if relations[RelationApplication] != test.application {
				t.Errorf("application=%v, want %v (%v)", relations[RelationApplication], test.application, relations)
			}
			if len(relations) == 0 {
				t.Errorf("the excerpt mentions the symbol but no role was reported")
			}
		})
	}
}

func TestExcerptRelationsOfUnmentionedSymbolIsEmpty(t *testing.T) {
	if relations := ExcerptRelations("func unrelated() {}", "MaxDesignRounds"); len(relations) != 0 {
		t.Fatalf("an unmentioned symbol cannot demonstrate any role: %v", relations)
	}
}

// ExcerptRelations is a strict refinement of ClassifyExcerpt: same mentions,
// and definition iff any defining line exists. The monovalued API stays for
// its existing consumers; this pins that the two can never disagree.
func TestClassifyExcerptAgreesWithExcerptRelations(t *testing.T) {
	worlds := []string{
		"type Limits struct {\n\tMaxDesignRounds int\n}\n\nfunc check(l Limits) bool {\n\treturn l.MaxDesignRounds > 0\n}\n",
		"type Limits struct {\n\tMaxDesignRounds int\n}\n",
		"return round >= limits.MaxDesignRounds\n",
		"return Limits{\n\tMaxDesignRounds: 2,\n}\n",
		"func unrelated() {}\n",
	}
	for _, content := range worlds {
		relation, mentions := ClassifyExcerpt(content, "MaxDesignRounds")
		relations := ExcerptRelations(content, "MaxDesignRounds")
		if mentions != (len(relations) > 0) {
			t.Fatalf("mentions=%v disagrees with relations %v for %q", mentions, relations, content)
		}
		if (relation == RelationDefinition) != relations[RelationDefinition] {
			t.Fatalf("definition disagreement: ClassifyExcerpt=%q, relations=%v for %q", relation, relations, content)
		}
	}
}
