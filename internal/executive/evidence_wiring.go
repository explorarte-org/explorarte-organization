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

// suppliedEvidence reads what the snapshot actually put in front of the worker
// and says which slot each excerpt could fill.
//
// The classification is the host's own and deterministic: an excerpt is a
// definition when it declares the symbol, and an application otherwise. Where
// the host cannot tell, the slot stays unsupplied -- which is the honest
// answer, because it really does not know one was supplied.
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
			relation, mentions := repositoryevidence.ClassifyExcerpt(source.Content, requirement.Subject)
			if !mentions {
				continue
			}
			slot := EvidenceSlot{Subject: requirement.Subject, Relation: relation}
			available[slot] = append(available[slot], source.Reference)
		}
	}
	return available, nil
}

// ReasonEvidenceInsufficient stops a campaign whose host could not supply
// what its own contract demands. It is distinct from every model-facing
// reason on purpose: nothing about it is the design's fault.
const ReasonEvidenceInsufficient = "evidence_insufficient"
