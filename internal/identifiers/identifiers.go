// Package identifiers extracts exact numeric identifiers/codes from text —
// the third retrieval channel (alongside FTS and vector search) that
// closes a real gap neither of the other two can: embeddings can plausibly
// treat "20" and "2000" as semantically close (both "a number in an error
// context"), and PostgreSQL's 'simple' text search tokenizer attaches a
// leading hyphen to a trailing number ("error-20" tokenizes to the lexeme
// '-20', not '20'), so a document written as "error-20" silently never
// matches a lexical search for "error 20" or "20" alone.
//
// ExtractDigitRuns must produce byte-for-byte the same output as the SQL
// function extract_digit_runs (migration 000029) for the same input — both
// implement "every maximal run of ASCII digits", nothing fancier. Query-time
// extraction happens here in Go; content-time extraction happens as a
// PostgreSQL generated column so it can never drift out of sync with the
// content it indexes. If either implementation's regex ever needs to
// change, the other must change with it in the same commit.
package identifiers

import "regexp"

var digitRun = regexp.MustCompile(`\d+`)

// ExtractDigitRuns returns every maximal run of ASCII digits in text, in
// order of appearance, including duplicates — callers that need a set for
// an overlap query (e.g. PostgreSQL's && operator against a TEXT[] column)
// should dedupe if they care about that, since duplicates never change an
// overlap test's result.
func ExtractDigitRuns(text string) []string {
	matches := digitRun.FindAllString(text, -1)
	if matches == nil {
		return []string{}
	}
	return matches
}
