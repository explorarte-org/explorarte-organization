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

// supportedEvidenceRequirementRelations is the exact set of relations an
// obligation may name -- by construction the same set the repository sensor
// can actually supply. ClassifyExcerpt answers only definition or application,
// so an obligation naming anything else is not merely hard to fill: no
// snapshot could ever fill it, and a round carrying such an obligation was
// doomed at adoption time. AUTONOMY-SMOKE-017-R9 reached exactly that wall --
// a revise whose adjudicated demands included relations the world cannot
// prove -- and the campaign died in the preflight instead of anywhere honest.
var supportedEvidenceRequirementRelations = []string{EvidenceDefinition, EvidenceApplication}

// validEvidenceRequirementRelation decides what an obligation MAY DEMAND.
// It is deliberately narrower than validEvidenceRelation, which decides what a
// worker artifact may SAY: evidence[] items legitimately use test and context
// to describe citations, but no sensor exists that can classify an excerpt as
// one, so demanding them would mint obligations outside the host's capability
// set. Splitting the vocabularies keeps both statements true.
func validEvidenceRequirementRelation(relation string) bool {
	for _, supported := range supportedEvidenceRequirementRelations {
		if relation == supported {
			return true
		}
	}
	return false
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
		for relationIndex, relation := range proposal.Relations {
			if !validEvidenceRelation(relation) {
				return fmt.Errorf("%w: evidence_requirements[%d].relations[%d]: %q is not an evidence relation", ErrContractRejected, index, relationIndex, relation)
			}
			if !validEvidenceRequirementRelation(relation) {
				return fmt.Errorf("%w: evidence_requirements[%d].relations[%d]: relation %q cannot be required; supported requirement relations: %s",
					ErrContractRejected, index, relationIndex, relation, strings.Join(supportedEvidenceRequirementRelations, ", "))
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

	}
	// One range cannot stand for two roles UNLESS the host's own content
	// classification already agrees it genuinely does. R5's design offered
	// budget.go#L31-L65 as evidence for two different limits and two
	// different roles -- a real clash, because that range never established
	// either fact for the second role. But a small function whose only
	// caller sits a few lines above its own definition puts a real
	// definition and a real application in front of a worker within one
	// narrow excerpt, and a worker citing that same range for both is not
	// laundering, it is accurate (found live, SELF-AUDIT-001, 2026-09-02:
	// validatePackage's definition and its call site in packages() both
	// fall inside internal/coderunner/executor.go#L283-L331). `available`
	// is already the host's own content-aware answer to "does this exact
	// reference genuinely support this relation" -- suppliedEvidence built
	// it via repositoryevidence.ExcerptRelations, the same classifier that
	// decides definition vs application from real content, not from
	// citation shape. Trusting it here instead of a shape-only rule means
	// this check only fires when the reference is NOT corroborated for at
	// least one of the two relations, which is what laundering actually is.
	// This is checked per SUBJECT rather than per requirement, because one
	// subject can now carry obligations from several sources and a clash
	// between them is still a clash.
	for subject, relations := range bySubject {
		if err := relationsAreDistinct(subject, relations, available); err != nil {
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

func relationsAreDistinct(subject string, relations map[string][]string, available map[EvidenceSlot][]string) error {
	ordered := make([]string, 0, len(relations))
	for relation := range relations {
		ordered = append(ordered, relation)
	}
	sort.Strings(ordered)
	seen := map[string]string{}
	for _, relation := range ordered {
		for _, ref := range relations[relation] {
			if other, clash := seen[ref]; clash && other != relation {
				corroboratedForBoth := anyAvailable([]string{ref}, available[EvidenceSlot{Subject: subject, Relation: other}]) &&
					anyAvailable([]string{ref}, available[EvidenceSlot{Subject: subject, Relation: relation}])
				if !corroboratedForBoth {
					return fmt.Errorf("%w: %s cites the same range for %s and %s (%s)",
						ErrContractRejected, subject, other, relation, ref)
				}
				continue
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

// evidenceContractGuidance renders the authoritative evidence obligations as
// execution-time guidance for the worker that will be judged against them.
//
// It exists because a contract can only bind what it was shown to.
// ValidateEvidenceStructure rejects an artifact that leaves a required slot
// unfilled, but until AUTONOMY-SMOKE-017-R8 nothing told the worker those
// slots existed: the instructions asked for prose citations, the schema gave
// evidence[] its shape but not its obligations, and every rejection arrived
// only after the first failure. Three attempts died measuring a contract they
// had never been handed. The requirements list this function renders is THE
// SAME LIST the validator enforces -- one source of truth for both ends, so
// prompt and host cannot drift.
//
// Deliberately NOT claimed: that evidence_refs must equal evidence[].ref. The
// host (refsAreAllStructured) requires only that no evidence_refs entry falls
// outside the structured evidence; stating more would make the prompt stricter
// than the validator and build the next PROMPT_CONTRACT_MISMATCH.
func evidenceContractGuidance(required []EvidenceRequirement) string {
	if len(required) == 0 {
		return ""
	}
	ordered := append([]EvidenceRequirement(nil), required...)
	sort.SliceStable(ordered, func(first, second int) bool {
		return ordered[first].Subject < ordered[second].Subject
	})
	var guidance strings.Builder
	guidance.WriteString("Required structured evidence slots for this result:\n\n")
	for _, requirement := range ordered {
		relations := append([]string(nil), requirement.Relations...)
		sort.Strings(relations)
		for _, relation := range relations {
			fmt.Fprintf(&guidance, "- subject=%q, relation=%q\n", requirement.Subject, relation)
		}
	}
	guidance.WriteString(`
For every required slot:
- emit at least one evidence[] item with exactly that subject and relation;
- its ref must identify repository evidence supplied in this execution;
- do not invent repository refs.

For worker-result/v2:
- evidence[] is the structured authority;
- every ref you put in evidence_refs must also occur in evidence[].ref;
- do not put unstructured additional refs in evidence_refs.`)
	return guidance.String()
}
