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
// This is deliberately a narrow, auditable rule rather than a parse. The point
// is not to be clever about Go; it is that the host can say WHY it believes an
// excerpt is a definition, and anyone can check the answer by reading the
// line. Where the rule cannot tell, it says application, and a requirement for
// a definition then goes unsupplied -- which is the honest outcome, because
// the host really does not know one was supplied.
func declarationLine(symbol string) *regexp.Regexp {
	quoted := regexp.QuoteMeta(symbol)
	return regexp.MustCompile(`(?m)^[ \t]*(?:(?:func|type|var|const)[ \t]+(?:\([^)]*\)[ \t]*)?)?` + quoted + `\b`)
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
