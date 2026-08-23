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

// EvidenceRequirementSource records WHY an obligation exists, because the
// answer determines who may change it.
//
// The selector is deliberately absent from this list. If retrieval could
// create requirements, the sensor would decide which facts it is obliged to
// sense: in AUTONOMY-SMOKE-017-R5 the file declaring both limits never reached
// the worker, so a retrieval-derived contract would simply have stopped
// requiring a definition and reported success. Repository blindness would
// have become undetectable by construction.
type EvidenceRequirementSource string

const (
	// The owner's acceptance of the design states what must be grounded.
	EvidenceFromOwnerAcceptance EvidenceRequirementSource = "owner_acceptance"
	// A revise verdict discovered an insufficiency the goal had not stated.
	EvidenceFromAdjudication EvidenceRequirementSource = "adjudication"
	// The host imposes it as policy, independent of any model.
	EvidenceFromHostPolicy EvidenceRequirementSource = "host_policy"
)

// EvidenceRequirementProposal is what a MODEL may ask for. It is deliberately
// a different type from EvidenceRequirement: a proposal carries no authority
// and no provenance, and only the host can turn one into an obligation.
type EvidenceRequirementProposal struct {
	Subject   string   `json:"subject"`
	Relations []string `json:"relations"`
}

// validateEvidenceRequirementProposals rejects malformed proposals before they
// can become obligations. The host is the only place this can happen: a model
// that could bind itself to a vocabulary of its own choosing would be writing
// the exam as well as sitting it.
func validateEvidenceRequirementProposals(proposals []EvidenceRequirementProposal, limits Limits) error {
	if len(proposals) > limits.MaxArrayItems {
		return fmt.Errorf("%w: evidence_requirements", ErrContractRejected)
	}
	seen := map[string]struct{}{}
	for index, proposal := range proposals {
		subject := strings.TrimSpace(proposal.Subject)
		if err := validateRequiredString(subject, limits.MaxStringBytes, "evidence_requirements.subject"); err != nil {
			return err
		}
		if _, duplicate := seen[subject]; duplicate {
			return fmt.Errorf("%w: evidence_requirements names %s twice", ErrContractRejected, subject)
		}
		seen[subject] = struct{}{}
		if len(proposal.Relations) == 0 {
			return fmt.Errorf("%w: evidence_requirements[%d] demands no relation", ErrContractRejected, index)
		}
		relations := map[string]struct{}{}
		for _, relation := range proposal.Relations {
			if !validEvidenceRelation(relation) {
				return fmt.Errorf("%w: evidence_requirements[%d].relations", ErrContractRejected, index)
			}
			if _, repeated := relations[relation]; repeated {
				return fmt.Errorf("%w: evidence_requirements[%d] repeats %s", ErrContractRejected, index, relation)
			}
			relations[relation] = struct{}{}
		}
	}
	return nil
}

// AdoptEvidenceRequirements turns proposals into obligations, stamping where
// each came from. Canonical order makes the resulting contract reproducible
// and comparable across rounds.
func AdoptEvidenceRequirements(proposals []EvidenceRequirementProposal, source EvidenceRequirementSource) []EvidenceRequirement {
	adopted := make([]EvidenceRequirement, 0, len(proposals))
	for _, proposal := range proposals {
		relations := append([]string(nil), proposal.Relations...)
		sort.Strings(relations)
		adopted = append(adopted, EvidenceRequirement{
			Subject: strings.TrimSpace(proposal.Subject), Relations: relations, Source: source,
		})
	}
	sort.SliceStable(adopted, func(first, second int) bool {
		return adopted[first].Subject < adopted[second].Subject
	})
	return adopted
}

// EvidenceSlot is one obligation: a subject and the role a citation must play
// for it. Supply is tracked per SLOT, not per subject.
//
// Asking only "is there anything for MaxDesignRounds?" passes as soon as one
// application site is in the snapshot, and then a design that cannot produce a
// definition -- because none was supplied -- is judged as if it had failed to
// write one down. That is the partial-supply version of exactly the
// misattribution this whole mechanism exists to prevent, and it is the shape
// AUTONOMY-SMOKE-017-R5 actually had: applications of both limits were in
// context, the declarations were not.
type EvidenceSlot struct {
	Subject  string
	Relation string
}

// ValidateEvidenceSupply asks, BEFORE a worker runs, whether the host holds
// material that could satisfy what it is about to demand.
//
// Discovering insufficiency after the call wastes an invocation and, worse,
// arrives at the moment the artifact is being judged -- which is where the
// temptation to blame the artifact lives. Asked first, the question has only
// one honest answer available.
func ValidateEvidenceSupply(required []EvidenceRequirement, available map[EvidenceSlot][]string) error {
	var unsupplied []string
	for _, requirement := range required {
		for _, relation := range requirement.Relations {
			slot := EvidenceSlot{Subject: requirement.Subject, Relation: relation}
			if len(available[slot]) == 0 {
				unsupplied = append(unsupplied, requirement.Subject+"/"+relation)
			}
		}
	}
	if len(unsupplied) == 0 {
		return nil
	}
	sort.Strings(unsupplied)
	return fmt.Errorf("%w: no citation was found for %s", ErrEvidenceInsufficient, strings.Join(unsupplied, ", "))
}

// EvidenceRequirement is one slot the host demands before a design is worth
// adjudicating: a subject, and the relations that must be cited for it.
type EvidenceRequirement struct {
	Subject   string
	Relations []string
	Source    EvidenceRequirementSource
}

// ValidateEvidenceStructure checks form, not correctness. It answers "can this
// artifact even be judged?" so that a round of adjudication is never spent on
// an artifact that structurally cannot answer the question.
//
// available is what the host actually put in front of the worker, indexed by
// SLOT: which citations could play which role. When a slot has no citations at
// all it was unfillable, and the failure is the host's -- reported as
// ErrEvidenceInsufficient rather than blamed on the design.
func ValidateEvidenceStructure(result WorkerResult, required []EvidenceRequirement, available map[EvidenceSlot][]string) error {
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
		for _, relation := range requirement.Relations {
			supplied := available[EvidenceSlot{Subject: requirement.Subject, Relation: relation}]
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
