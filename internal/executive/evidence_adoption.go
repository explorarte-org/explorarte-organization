package executive

import (
	"context"
	"encoding/json"
	"strconv"
)

// adoptAdjudicationRequirements turns the previous round's revise into the
// obligations of the round it opened.
//
// Idempotent by guard, not by hope: the task engine appends evidence rows
// without uniqueness on reference, so a resumed run that re-adopted would
// write the same obligation again on every pass. The round's reference is
// checked first and the decision is recorded at most once; a later resume
// loads it from durable state like any other reader. Nothing already in
// force is touched -- an adjudicator that wants more binds the NEXT round,
// never the contract a design was already written against.
func (o *Orchestrator) adoptAdjudicationRequirements(ctx context.Context, root TaskRecord, all []TaskRecord, round int) error {
	previous := round - 1
	adjudication, ok := findTaskByKey(all, childKey(root.ID, "design-adjudication:round:"+strconv.Itoa(previous)))
	if !ok || adjudication.Status != "completed" {
		return nil
	}
	result, ok := o.resultForCompletedTask(ctx, adjudication)
	if !ok {
		return nil
	}
	proposals, err := adjudicationEvidenceRequirementsOf(result.JSONOutput)
	if err != nil || len(proposals) == 0 {
		return nil
	}
	recorded, err := o.roundHasRecordedRequirements(ctx, root.ID, round)
	if err != nil {
		return err
	}
	if recorded {
		return nil
	}
	inForce, err := o.evidenceRequirementsForRound(ctx, root.ID, previous)
	if err != nil {
		return err
	}
	// A reviewer restating an obligation someone else already imposed must
	// not become a second authority over it. The original stands; the
	// restatement is redundant, not a claim.
	novel := withoutSlotsAlreadyInForce(proposals, inForce)
	if len(novel) == 0 {
		return nil
	}
	return o.recordEvidenceRequirements(ctx, root.ID, round,
		AdoptEvidenceRequirements(novel, EvidenceFromAdjudication))
}

// withoutSlotsAlreadyInForce drops (subject, relation) pairs that are already
// obligations, so no slot can end up attributed to two sources at once.
func withoutSlotsAlreadyInForce(proposals []EvidenceRequirementProposal, inForce []EvidenceRequirement) []EvidenceRequirementProposal {
	held := map[EvidenceSlot]struct{}{}
	for _, requirement := range inForce {
		for _, relation := range requirement.Relations {
			held[EvidenceSlot{Subject: requirement.Subject, Relation: relation}] = struct{}{}
		}
	}
	novel := make([]EvidenceRequirementProposal, 0, len(proposals))
	for _, proposal := range proposals {
		relations := make([]string, 0, len(proposal.Relations))
		for _, relation := range proposal.Relations {
			if _, already := held[EvidenceSlot{Subject: proposal.Subject, Relation: relation}]; already {
				continue
			}
			relations = append(relations, relation)
		}
		if len(relations) == 0 {
			continue
		}
		novel = append(novel, EvidenceRequirementProposal{Subject: proposal.Subject, Relations: relations})
	}
	return novel
}

// adjudicationEvidenceRequirementsOf reads only the proposals, the way
// adjudicationVerdictOf reads only the verdict: the host identity binding a
// full parse requires is not needed to learn what the next round must ground.
func adjudicationEvidenceRequirementsOf(body []byte) ([]EvidenceRequirementProposal, error) {
	var envelope struct {
		Verdict              AdjudicationVerdict           `json:"verdict"`
		EvidenceRequirements []EvidenceRequirementProposal `json:"evidence_requirements"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, err
	}
	if envelope.Verdict != AdjudicationRevise {
		return nil, nil
	}
	return envelope.EvidenceRequirements, nil
}
