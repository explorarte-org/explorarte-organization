package executive

import (
	"fmt"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/designfreeze"
)

// A design worker's summary IS the design: the reviewers and the adjudicator read the
// candidate built from the summaries and nothing else the worker produced. On root 1265
// (2026-09-24) the round-2 worker's acceptance criteria said "State that the proposed edit adds
// exactly one new case to the table of TestExtractDigitRunsCoreCases ..." and its summary said
// "the test table". Every other required value was there. The reviewer demanded the literal
// name, the adjudicator sent the design back, and the design ran out of rounds -- a whole review
// and adjudication round spent to recover a name the host could see was missing the moment the
// worker answered.
//
// The check is deterministic and narrow. For a worker under a pending design freeze, every
// identifier its own acceptance criteria name must appear, exactly, in its summary. A summary
// that omits one is refused as an attempt failure with the missing names in the feedback, so the
// worker corrects it on the next attempt of the same round -- one cheap call instead of a round.
//
// What counts as an identifier is deliberately narrow, because a false positive blocks a worker
// that did nothing wrong: a mixed-case name with at least two humps (TestExtractDigitRunsCoreCases,
// MaxDesignRounds, ErrContractRejected). Plain words, acronyms, ALL_CAPS constants, paths, numbers,
// commit ids and product names with a single hump (GitHub, PostgreSQL) never qualify. Quoted
// strings and numeric literals are NOT checked here.
const (
	minIdentifierLength = 8
	maxIdentifierLength = 120
	minIdentifierHumps  = 2
	maxOmittedReported  = 8
)

// identifiersNamedBy returns the identifiers the texts name, in order of first appearance,
// without duplicates.
func identifiersNamedBy(texts []string) []string {
	var found []string
	seen := map[string]bool{}
	for _, text := range texts {
		start := -1
		flush := func(end int) {
			if start < 0 {
				return
			}
			run := text[start:end]
			start = -1
			if isMixedCaseIdentifier(run) && !seen[run] {
				seen[run] = true
				found = append(found, run)
			}
		}
		for index := 0; index < len(text); index++ {
			character := text[index]
			word := character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
				character >= '0' && character <= '9' || character == '_'
			if word {
				if start < 0 {
					start = index
				}
				continue
			}
			flush(index)
		}
		flush(len(text))
	}
	return found
}

func isMixedCaseIdentifier(run string) bool {
	if len(run) < minIdentifierLength || len(run) > maxIdentifierLength {
		return false
	}
	first := run[0]
	if !(first >= 'a' && first <= 'z' || first >= 'A' && first <= 'Z') {
		return false
	}
	hasLower := false
	humps := 0
	for index := 0; index < len(run); index++ {
		character := run[index]
		if character >= 'a' && character <= 'z' {
			hasLower = true
		}
		if index > 0 && character >= 'A' && character <= 'Z' {
			previous := run[index-1]
			if previous >= 'a' && previous <= 'z' || previous >= '0' && previous <= '9' || previous == '_' {
				humps++
			}
		}
	}
	return hasLower && humps >= minIdentifierHumps
}

// omittedIdentifiers returns the identifiers the criteria name that the summary does not contain.
func omittedIdentifiers(summary string, criteria []string) []string {
	var omitted []string
	for _, identifier := range identifiersNamedBy(criteria) {
		if !strings.Contains(summary, identifier) {
			omitted = append(omitted, identifier)
		}
	}
	return omitted
}

// designFreezePending reports whether the run is governed by a design freeze that has not been
// satisfied yet -- the only phase in which a worker's summary is the design under review.
func designFreezePending(root TaskRecord) bool {
	requirement, found := findRequirementByKey(root.Requirements, designfreeze.RequirementKey)
	return found && requirement.Status != "satisfied"
}

// verifyDesignNamesTheCriteriaIdentifiers is the attempt-time gate: a design worker's summary
// that leaves out an identifier its own acceptance criteria name is a contract rejection, and
// its message is what the next attempt reads.
func verifyDesignNamesTheCriteriaIdentifiers(root, task TaskRecord, summary string) error {
	if !designFreezePending(root) {
		return nil
	}
	omitted := omittedIdentifiers(summary, task.AcceptanceCriteria)
	if len(omitted) == 0 {
		return nil
	}
	if len(omitted) > maxOmittedReported {
		omitted = omitted[:maxOmittedReported]
	}
	return fmt.Errorf("%w: your summary omits identifiers your acceptance criteria name: %s. The reviewers read your summary and nothing else; "+
		"state each of them exactly as your acceptance criteria write it",
		ErrContractRejected, strings.Join(omitted, ", "))
}
