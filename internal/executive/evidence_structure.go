package executive

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ErrEvidenceInsufficient reports that the HOST did not supply what its own
// requirement needs. It is deliberately distinct from a contract rejection.
//
// AUTONOMY-SMOKE-017-R5 asked a design to cite where two limits are declared
// and never put the file that declares them in front of it: the retrieval
// budget was spent on incidental words before either symbol was searched. The
// design cited a test fixture, and the adjudicator rejected it -- twice.
//
// If a structural validator had existed then, it would have said "definition
// missing for MaxDesignRounds" and blamed the worker for not producing
// something it was never given. That would have turned repository blindness
// into contract blindness: a system that cannot see, confidently reporting
// that its designer cannot write. So the two failures are separate errors, and
// only one of them is the worker's.
var ErrEvidenceInsufficient = errors.New("supplied evidence cannot satisfy the required slots")

// EvidenceRequirement is one slot the host demands before a design is worth
// adjudicating: a subject, and the relations that must be cited for it.
type EvidenceRequirement struct {
	Subject   string
	Relations []string
}

// ValidateEvidenceStructure checks form, not correctness. It answers "can this
// artifact even be judged?" so that a round of adjudication is never spent on
// an artifact that structurally cannot answer the question.
//
// available is what the host actually put in front of the worker, indexed by
// the subject its search was for. When a subject has no citations there at
// all, its slots were unfillable and the failure is the host's, reported as
// ErrEvidenceInsufficient rather than blamed on the design.
func ValidateEvidenceStructure(result WorkerResult, required []EvidenceRequirement, available map[string][]string) error {
	if len(required) == 0 {
		return nil
	}
	if result.SchemaVersion != WorkerResultSchemaVersionV2 {
		return fmt.Errorf("%w: this design must state evidence relations, which %s cannot express",
			ErrContractRejected, result.SchemaVersion)
	}

	bySubject := map[string]map[string][]string{}
	for _, item := range result.Evidence {
		subject := strings.TrimSpace(item.Subject)
		if bySubject[subject] == nil {
			bySubject[subject] = map[string][]string{}
		}
		bySubject[subject][item.Relation] = append(bySubject[subject][item.Relation], strings.TrimSpace(item.Ref))
	}

	var unfilled, unsupplied []string
	for _, requirement := range required {
		relations := bySubject[requirement.Subject]
		supplied := available[requirement.Subject]
		for _, relation := range requirement.Relations {
			refs := relations[relation]
			filled := len(refs) > 0 && anyAvailable(refs, supplied)
			if filled {
				continue
			}
			slot := requirement.Subject + "/" + relation
			// Whose failure is it? If the host put no citation for this
			// subject in front of the worker, the slot was unfillable and
			// the worker is not the one that failed.
			if len(supplied) == 0 {
				unsupplied = append(unsupplied, slot)
				continue
			}
			unfilled = append(unfilled, slot)
		}
		// The whole point of separating the slots is that one range cannot
		// stand for both. R5's design offered budget.go#L31-L65 as evidence
		// for two different limits and for two different roles.
		if err := relationsAreDistinct(requirement, relations); err != nil {
			return err
		}
	}
	// Host insufficiency is reported first and on its own: a design must
	// never be told it failed to cite what it was never shown.
	if len(unsupplied) > 0 {
		sort.Strings(unsupplied)
		return fmt.Errorf("%w: nothing was supplied for %s", ErrEvidenceInsufficient, strings.Join(unsupplied, ", "))
	}
	if len(unfilled) == 0 {
		return nil
	}
	sort.Strings(unfilled)
	return fmt.Errorf("%w: evidence is missing for %s", ErrContractRejected, strings.Join(unfilled, ", "))
}

func relationsAreDistinct(requirement EvidenceRequirement, relations map[string][]string) error {
	seen := map[string]string{}
	for _, relation := range requirement.Relations {
		for _, ref := range relations[relation] {
			if other, clash := seen[ref]; clash && other != relation {
				return fmt.Errorf("%w: %s cites the same range for %s and %s (%s)",
					ErrContractRejected, requirement.Subject, other, relation, ref)
			}
			seen[ref] = relation
		}
	}
	return nil
}

func anyAvailable(refs []string, available []string) bool {
	for _, ref := range refs {
		for _, candidate := range available {
			if ref == candidate {
				return true
			}
		}
	}
	return false
}
