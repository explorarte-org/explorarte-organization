package repositoryevidence

import (
	"regexp"
	"strings"
)

// RelationDefinition and RelationApplication are the roles an excerpt can play
// for a symbol, as far as the HOST can establish them without asking a model.
const (
	RelationDefinition  = "definition"
	RelationApplication = "application"
)

// declarationLine matches a line that DECLARES an identifier rather than using
// one: the identifier opens the line, or follows a Go declaration keyword.
//
// A struct field ("MaxDesignRounds    int"), a func, a type, a const or var
// binding all put the name in that position. A use does not:
// "o.limits.MaxDesignRounds" and "round > o.limits.MaxDesignRounds" carry the
// symbol mid-line, behind whatever holds it.
//
// Opening a line is not enough on its own. A composite literal written across
// several lines puts its keys there too:
//
//	return Limits{
//	        MaxDepartmentReplans: 1,
//	}
//
// That is the DEFAULT VALUE of a field, not the field's declaration, and an
// excerpt carrying only the default would otherwise let the host claim it had
// supplied a definition it never saw. A trailing colon separates the two:
// a key is followed by one, a declaration never is -- except ":=", which
// really does introduce a name.
//
// This is deliberately a narrow, auditable rule rather than a parse. The point
// is not to be clever about Go; it is that the host can say WHY it believes an
// excerpt is a definition, and anyone can check the answer by reading the
// line. Where the rule cannot tell, it says application, and a requirement for
// a definition then goes unsupplied -- which is the honest outcome, because
// the host really does not know one was supplied.
func declarationLine(symbol string) *regexp.Regexp {
	quoted := regexp.QuoteMeta(symbol)
	// (?:[^:=]|$) after the name rejects a composite-literal key while
	// keeping a short variable declaration: "MaxDepartmentReplans: 1" is a
	// value being set, "MaxDesignRounds := 2" is a name being introduced.
	return regexp.MustCompile(`(?m)^[ \t]*(?:(?:func|type|var|const)[ \t]+(?:\([^)]*\)[ \t]*)?)?` +
		quoted + `\b[ \t]*(?::=|[^:\n]|$)`)
}

// ClassifyExcerpt reports what role an excerpt can play for a symbol, and
// whether it mentions the symbol at all.
func ClassifyExcerpt(content, symbol string) (relation string, mentions bool) {
	symbol = strings.TrimSpace(symbol)
	if symbol == "" || !strings.Contains(content, symbol) {
		return "", false
	}
	if declarationLine(symbol).MatchString(content) {
		return RelationDefinition, true
	}
	return RelationApplication, true
}

// ExcerptRelations reports EVERY role an excerpt can honestly play for a
// symbol. An excerpt that physically contains both a declaration and a use
// demonstrates both roles; collapsing it to one is how a co-located
// application stayed invisible even after discovery stopped discarding it.
// The keys are exactly RelationDefinition and RelationApplication, an entry
// is present only when true, and an empty map means the symbol is not
// mentioned at all -- so len(relations) > 0 corresponds to ClassifyExcerpt's
// mentions, and relations[RelationDefinition] to its returning definition.
//
// This is the HOST AUTHORITY half of the role question -- what does the read
// content actually contain -- while rankHits answers only where discovery
// suggests looking. No model, no ranking confidence: every answer traces to a
// line anyone can read at the pinned commit.
func ExcerptRelations(content, symbol string) map[string]bool {
	relations := map[string]bool{}
	symbol = strings.TrimSpace(symbol)
	if symbol == "" {
		return relations
	}
	for _, line := range strings.Split(content, "\n") {
		if !strings.Contains(line, symbol) {
			continue
		}
		if declarationLine(symbol).MatchString(line) {
			relations[RelationDefinition] = true
			continue
		}
		relations[RelationApplication] = true
	}
	return relations
}

// LineDeclares reports whether ONE line of source declares symbol rather
// than using it, under the same auditable rule ClassifyExcerpt applies to a
// whole excerpt. It exists for discovery: git grep already returns the text
// of every hit, and a search that knows which hit introduces the name can
// put that hit first instead of spending its budget on fixtures. It grants
// nothing -- an excerpt still has to earn "definition" from ClassifyExcerpt
// after it is actually read.
func LineDeclares(line, symbol string) bool {
	symbol = strings.TrimSpace(symbol)
	if symbol == "" || !strings.Contains(line, symbol) {
		return false
	}
	return declarationLine(symbol).MatchString(line)
}
