package executive

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// The host issues a citation for every repository excerpt it puts in front of a
// model, and accepts back only what it issued. Two questions, one set:
//
//	visible_to_model(excerpt)  =>  the model is TOLD the excerpt's exact reference
//	accepted_ref(ref)          =>  ref is covered by a reference issued to this task
//
// The second half has always been enforced: VerifyEvidenceProvenance checks
// every offered reference against genuineRepositoryCitations, the included
// repository_evidence sources of the invocation's own snapshot. The first half
// was missing. An excerpt reaches the model as "<path> lines a-b at <sha>" plus
// its source, inside an escaped untrusted-data wrapper, and its reference
// (repository://<repo>@<sha>/<path>#La-Lb) is stored on the source but never
// rendered. A worker asked to cite "a real repository:// reference shown to
// you" therefore composed one from what it could see -- and was rejected, three
// attempts running, for citing something the host never issued. That is a
// contract with one half missing, not a model that failed to follow it.
//
// issuedRepositoryCitations is the single definition of "issued", shared by the
// validator (genuineRepositoryCitations) and by the guidance that tells the
// model what it may cite, so the two can never describe different sets.

// maxIssuedCitationsListed bounds the guidance. The repository selection is
// already bounded by its own budget; this is a second seatbelt so a change in
// that budget can never turn the contract into an unbounded prompt.
const maxIssuedCitationsListed = 64

// issuedRepositorySource reports whether a snapshot source is a repository
// excerpt the host issued a citation for: real repository evidence about the
// commit the design is about, that survived assembly.
func issuedRepositorySource(source SnapshotSource, baseSHA string) bool {
	return source.Kind == "repository_evidence" && source.Version == baseSHA && source.Included
}

// issuedRepositoryCitations lists, sorted and de-duplicated, the exact
// repository references issued to the invocation whose snapshot is snapshotID.
func issuedRepositoryCitations(ctx context.Context, sources SnapshotSourceReader, snapshotID int64, baseSHA string) ([]string, error) {
	if sources == nil || snapshotID <= 0 || baseSHA == "" {
		return nil, nil
	}
	shown, err := sources.SnapshotSources(ctx, snapshotID)
	if err != nil {
		return nil, err
	}
	seen := map[string]struct{}{}
	var refs []string
	for _, source := range shown {
		if !issuedRepositorySource(source, baseSHA) {
			continue
		}
		if _, _, ok := parseCitationRange(source.Reference); !ok {
			continue
		}
		if _, dup := seen[source.Reference]; dup {
			continue
		}
		seen[source.Reference] = struct{}{}
		refs = append(refs, source.Reference)
	}
	sort.Strings(refs)
	return refs, nil
}

// issuedCitationGuidance tells a model exactly which repository references the
// host issued to this execution. It is host text in the trusted execution
// contract -- not part of the escaped, untrusted excerpt -- so the strings are
// exact, and it is derived from the same set the provenance validator accepts.
//
// With nothing issued it says so, and says that empty evidence is complete:
// the honest answer to "cite what you saw" when nothing citable was shown.
func issuedCitationGuidance(refs []string) string {
	if len(refs) == 0 {
		return `CITABLE REPOSITORY REFERENCES issued by the host to this execution: none.
No repository excerpt was issued a reference, so nothing repository-related can be cited: return an empty evidence list, and describe anything you say about code as unverified.`
	}
	listed := refs
	omitted := 0
	if len(listed) > maxIssuedCitationsListed {
		omitted = len(listed) - maxIssuedCitationsListed
		listed = listed[:maxIssuedCitationsListed]
	}
	var b strings.Builder
	b.WriteString("CITABLE REPOSITORY REFERENCES issued by the host to this execution. These are the ONLY repository references you may cite, and each one names exactly one excerpt shown to you:\n")
	for _, ref := range listed {
		b.WriteString("- " + ref + "\n")
	}
	if omitted > 0 {
		fmt.Fprintf(&b, "(%d further issued references are not listed; cite only what you can read in your excerpts.)\n", omitted)
	}
	b.WriteString(`Copy a reference exactly, character for character, including the commit and the #L range. A cited range fully inside one issued range is also valid. Excerpts are displayed escaped (for example "&#x2F;" stands for "/"); the references above are the authoritative, unescaped form.
Never compose, shorten or reformat a reference, and never cite a file or range that is not issued above. If a claim rests on code you were not shown, do not cite it: state in prose that it is unverified, or ask for more context instead of inventing a reference.`)
	return b.String()
}

// citesRepositoryEvidence reports whether a purpose's output can carry
// repository citations the host verifies (worker-result/v2 and
// department-review/v2 -- the two verifyWorkerEvidenceProvenance and
// verifyOfferedEvidenceProvenance guard).
func citesRepositoryEvidence(purpose ExecutionPurpose) bool {
	return purpose == PurposeDepartmentWorker || purpose == PurposeDepartmentReview
}

// withIssuedCitations appends the issued-citation guidance to a contract for the
// purposes that cite repository evidence. It is a no-op for an execution with no
// repository grounding (baseSHA == ""), so ordinary campaigns keep their exact
// contract.
func withIssuedCitations(contract string, purpose ExecutionPurpose, baseSHA string, refs []string) string {
	if baseSHA == "" || !citesRepositoryEvidence(purpose) {
		return contract
	}
	if contract != "" {
		contract += "\n\n"
	}
	return contract + issuedCitationGuidance(refs)
}
