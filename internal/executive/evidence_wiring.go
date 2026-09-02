package executive

import (
	"context"
	"sort"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/repositoryevidence"
)

// roundEvidenceRequirements is what THIS execution must ground.
//
// Obligations bind the design work of a round. A coordination task -- a plan,
// a review, an adjudication -- is not the thing being grounded, so it carries
// none: demanding a definition citation from a CEO plan would be asking the
// wrong actor for the wrong artifact.
func (o *Orchestrator) roundEvidenceRequirements(ctx context.Context, root, task TaskRecord, purpose ExecutionPurpose) ([]EvidenceRequirement, error) {
	if purpose != PurposeDepartmentWorker {
		return nil, nil
	}
	if !o.repositoryGroundedCampaign(root) {
		return nil, nil
	}
	return o.evidenceRequirementsForRound(ctx, root.ID, designRoundOf(task.IdempotencyKey))
}

// evidenceSubjects is what retrieval must look for before anything it might
// discover on its own.
func evidenceSubjects(required []EvidenceRequirement) []string {
	seen := map[string]struct{}{}
	subjects := make([]string, 0, len(required))
	for _, requirement := range required {
		subject := strings.TrimSpace(requirement.Subject)
		if subject == "" {
			continue
		}
		if _, already := seen[subject]; already {
			continue
		}
		seen[subject] = struct{}{}
		subjects = append(subjects, subject)
	}
	sort.Strings(subjects)
	return subjects
}

// evidenceSlots is the same obligation list at its full normative resolution:
// one (subject, relation) pair per demanded slot, deduplicated and ordered,
// so the relation survives the journey into selection. evidenceSubjects alone
// is what let R15's round 2 lose driveDesignFreeze/application before any
// excerpt was chosen -- the subject arrived, the relation did not.
func evidenceSlots(required []EvidenceRequirement) []EvidenceSlot {
	seen := map[EvidenceSlot]struct{}{}
	slots := make([]EvidenceSlot, 0, len(required)*2)
	for _, requirement := range required {
		subject := strings.TrimSpace(requirement.Subject)
		if subject == "" {
			continue
		}
		for _, relation := range requirement.Relations {
			slot := EvidenceSlot{Subject: subject, Relation: relation}
			if _, already := seen[slot]; already {
				continue
			}
			seen[slot] = struct{}{}
			slots = append(slots, slot)
		}
	}
	sort.Slice(slots, func(first, second int) bool {
		if slots[first].Subject != slots[second].Subject {
			return slots[first].Subject < slots[second].Subject
		}
		return slots[first].Relation < slots[second].Relation
	})
	return slots
}

// suppliedEvidence reads what the snapshot actually put in front of the worker
// and says which slot each excerpt could fill.
//
// The classification is the host's own and deterministic: an excerpt fills
// every slot whose role it physically contains -- a fragment carrying both a
// declaration and a use supplies definition AND application, because both
// claims are checkable against the same pinned lines. Where the host cannot
// tell, the slot stays unsupplied -- which is the honest answer, because it
// really does not know one was supplied.
func (o *Orchestrator) suppliedEvidence(ctx context.Context, snapshotID int64, required []EvidenceRequirement) (map[EvidenceSlot][]string, error) {
	if len(required) == 0 {
		return nil, nil
	}
	if o.snapshotSources == nil {
		// A campaign with obligations and no way to read back what it
		// showed cannot honestly claim to have supplied anything.
		return map[EvidenceSlot][]string{}, nil
	}
	shown, err := o.snapshotSources.SnapshotSources(ctx, snapshotID)
	if err != nil {
		return nil, err
	}
	available := map[EvidenceSlot][]string{}
	for _, source := range shown {
		if source.Kind != "repository_evidence" || !source.Included || source.Content == "" {
			continue
		}
		for _, requirement := range required {
			for relation := range repositoryevidence.ExcerptRelations(source.Content, requirement.Subject) {
				slot := EvidenceSlot{Subject: requirement.Subject, Relation: relation}
				available[slot] = append(available[slot], source.Reference)
			}
		}
	}
	return available, nil
}

// ReasonEvidenceInsufficient stops a campaign whose host could not supply
// what its own contract demands. It is distinct from every model-facing
// reason on purpose: nothing about it is the design's fault.
const ReasonEvidenceInsufficient = "evidence_insufficient"

// ReasonEvidenceDeliveryViolation is the same stop, one level more specific:
// the failed slots belonged to an ADMITTED adjudication plan, which promised
// delivery under the very limits the snapshot ran with. When that promise
// breaks, the fault is host-side by construction -- never the worker's, and
// never the world's -- and the record says so (checkpoint D).
const ReasonEvidenceDeliveryViolation = "evidence_delivery_violation"

// ReasonContextSourceMissing is the root-level Run.ReasonCode for G1-005:
// a role's context source (most commonly PERFIL.md) was registered and
// executable in the canonical/DB role catalog but missing from the
// physical file tree. See ErrContextSourceRejected.
const ReasonContextSourceMissing = "context_source_missing"

// ReasonModelAuthorityViolation is the root-level Run.ReasonCode for
// AUTH-001's sibling finding: a model named a forbiddenModelKeys field
// or delegated across a department boundary. See
// ErrModelAuthorityViolation. Stays blocked through Resume like every
// other reason a human, not a fresh attempt, must clear.
const ReasonModelAuthorityViolation = "model_authority_violation"

// evidenceFailureReason classifies a supply-preflight failure by WHOSE
// promise broke. Owner-goal obligations were never admitted against capacity,
// so their failure stays the honest "the world cannot supply this";
// adjudication-sourced obligations passed joint admission, so their failure
// means the host did not deliver what it accepted.
func evidenceFailureReason(required []EvidenceRequirement) string {
	for _, requirement := range required {
		if requirement.Source == EvidenceFromAdjudication {
			return ReasonEvidenceDeliveryViolation
		}
	}
	return ReasonEvidenceInsufficient
}
