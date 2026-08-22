package executive

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// The boundary, as a table.
//
// Not "no repository information reaches the reviewer" -- that would leave it
// nothing to review. The line is between an abstraction and a reproduction:
// semantic claims and provenance metadata cross, source text does not, and
// neither does any representation of it this host can reverse. Changing the
// container's bytes does not stop it being exfiltration.
//
// What this does NOT prove is non-interference. A model that read
// organizational data and may emit free text always has a steganographic
// channel, and repository evidence is untrusted input that can instruct it to
// use one. These cases are the practical threat model, and the comment on
// DeclassifyCandidate says so rather than claiming a guarantee the mechanism
// does not provide.
const organizationalSource = `func (o *Orchestrator) driveDepartments(ctx context.Context, root TaskRecord) (Run, bool, error) {
	revision, err := o.registry.CurrentRevision(ctx)
	if err != nil {
		return Run{}, false, err
	}
	for _, req := range plan.DepartmentRequests {
		leader, lookupErr := o.registry.GetLeader(ctx, req.UnitID)
		if lookupErr != nil {
			return Run{}, false, lookupErr
		}
	}
}`

func TestTheEgressBoundaryBetweenClaimAndCopy(t *testing.T) {
	evidence := []string{organizationalSource}
	lines := strings.Split(organizationalSource, "\n")

	for _, tc := range []struct {
		name      string
		candidate string
		reject    bool
	}{
		{
			name:      "exact source copy",
			candidate: "The design should split this:\n" + organizationalSource,
			reject:    true,
		},
		{
			name: "copy with whitespace and indentation changed",
			candidate: "Consider: " + strings.ReplaceAll(
				strings.ReplaceAll(organizationalSource, "\t", "        "), "\n", "\n\n"),
			reject: true,
		},
		{
			name:      "partial but substantial span",
			candidate: "The relevant part is: " + strings.Join(lines[1:5], "\n"),
			reject:    true,
		},
		{
			name:      "inside a markdown code fence",
			candidate: "As shown below:\n```go\n" + organizationalSource + "\n```\n",
			reject:    true,
		},
		{
			name: "JSON-escaped",
			candidate: func() string {
				encoded, _ := json.Marshal(organizationalSource)
				return "The current implementation is " + string(encoded)
			}(),
			reject: true,
		},
		{
			name: "base64 of the source",
			candidate: "Reference material: " +
				base64.StdEncoding.EncodeToString([]byte(organizationalSource)),
			reject: true,
		},
		{
			name: "ordinary paraphrase with a verified reference",
			candidate: "driveDepartments mixes planning and coordination: it resolves the " +
				"organization revision and then loops over department requests, so a failure " +
				"in leader lookup aborts the whole phase. See " + realCite + ".",
			reject: false,
		},
		{
			name: "paths, symbols, commits and citations",
			candidate: "Change internal/executive/orchestrator.go, specifically Orchestrator " +
				"and driveDepartments, against " + designSHA + ". Evidence: " + realCite,
			reject: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := DeclassifyCandidate(tc.candidate, evidence)
			if tc.reject && !errors.Is(err, ErrCandidateContaminated) {
				t.Fatalf("this must not reach a reviewer that may not read source, got %v", err)
			}
			if !tc.reject && err != nil {
				t.Fatalf("a claim about the code must be allowed to cross: %v", err)
			}
		})
	}
}

// Egress is not a property of the author. If organizational bytes are about to
// leave, it does not matter which deliverable put them in the candidate -- so
// the scan runs against the union of everything the contributing deliverables
// were shown.
func TestContaminationIsJudgedAgainstEveryDeliverablesEvidence(t *testing.T) {
	// Worker B saw this; worker A wrote it into the candidate.
	seenByAnotherDeliverable := organizationalSource
	candidate := "Worker A says the helper is unused:\n" + seenByAnotherDeliverable

	if err := DeclassifyCandidate(candidate, []string{seenByAnotherDeliverable}); !errors.Is(err, ErrCandidateContaminated) {
		t.Fatalf("source shown to any contributing deliverable must not leave, got %v", err)
	}
}

// A campaign that observed no code has nothing to declassify, and must not be
// blocked by a rule that has no subject.
func TestAnUngroundedCandidateIsUnaffected(t *testing.T) {
	if err := DeclassifyCandidate("A design that mentions no repository at all.", nil); err != nil {
		t.Fatalf("an ungrounded candidate must pass untouched: %v", err)
	}
}
