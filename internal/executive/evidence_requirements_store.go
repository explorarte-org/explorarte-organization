package executive

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
)

// EvidenceRequirementsReference is where a round's obligations live.
//
// They are durable for the same reason the design pin is: after a campaign is
// accepted, the source of truth must be recorded state and not the owner's
// original JSON. A resume that re-read the submission would be re-interpreting
// the owner every time it restarted, and two interpretations of one goal is
// one too many.
//
// The round is part of the reference so obligations accumulate per round
// instead of overwriting each other, and so adopting the same adjudication
// twice writes the same fact once.
const EvidenceRequirementsReference = "evidence-requirements://"

func evidenceRequirementsReference(rootID int64, round int) string {
	return EvidenceRequirementsReference + strconv.FormatInt(rootID, 10) + "/round/" + strconv.Itoa(round)
}

// recordEvidenceRequirements persists what a round must ground.
//
// Obligations already in force for a round are never mutated. An adjudicator
// that wants more creates the obligation for the NEXT round: a design cannot
// be judged against a contract that changed after it was written.
func (o *Orchestrator) recordEvidenceRequirements(ctx context.Context, rootID int64, round int, required []EvidenceRequirement) error {
	if len(required) == 0 {
		return nil
	}
	payload, err := json.Marshal(required)
	if err != nil {
		return err
	}
	// The digest covers the FACT, not any identifier: the Tasks Engine
	// accepts only 64 hex characters, and a reference is not one.
	factDigest := sha256.Sum256(append([]byte("evidence_requirements\x00"), payload...))
	return o.tasks.RecordEvidence(ctx, EvidenceCommand{
		TaskID: rootID, Type: "result", Reference: evidenceRequirementsReference(rootID, round),
		Digest: hex.EncodeToString(factDigest[:]), RecordedBy: orchestratorWorkerID,
		Metadata: map[string]any{"evidence_requirements": string(payload), "round": round},
	})
}

// evidenceRequirementsForRound reads back what a round must ground.
//
// Obligations are cumulative: a round inherits everything in force before it,
// because an insufficiency the owner named does not stop mattering because an
// adjudicator later named another one.
//
// They are merged per (subject, source), never across sources. Authority lives
// at the slot: if the owner demanded MaxDesignRounds/definition and a later
// adjudication demanded MaxDesignRounds/application, unioning the relations
// under one source would make the adjudicator's obligation look like the
// owner's -- and "who may change this" is decided by exactly that answer.
func (o *Orchestrator) evidenceRequirementsForRound(ctx context.Context, rootID int64, round int) ([]EvidenceRequirement, error) {
	detail, err := o.tasks.GetTask(ctx, rootID)
	if err != nil {
		return nil, err
	}
	type obligationKey struct {
		subject string
		source  EvidenceRequirementSource
	}
	byOrigin := map[obligationKey]EvidenceRequirement{}
	for candidate := 1; candidate <= round; candidate++ {
		reference := evidenceRequirementsReference(rootID, candidate)
		for _, evidence := range detail.Evidence {
			if evidence.Reference != reference {
				continue
			}
			raw, _ := evidence.Metadata["evidence_requirements"].(string)
			if raw == "" {
				return nil, fmt.Errorf("%w: recorded evidence requirements are empty", ErrContractRejected)
			}
			var stored []EvidenceRequirement
			if err := json.Unmarshal([]byte(raw), &stored); err != nil {
				return nil, fmt.Errorf("%w: recorded evidence requirements are unreadable", ErrContractRejected)
			}
			for _, requirement := range stored {
				key := obligationKey{subject: requirement.Subject, source: requirement.Source}
				byOrigin[key] = mergeRequirement(byOrigin[key], requirement)
			}
		}
	}
	merged := make([]EvidenceRequirement, 0, len(byOrigin))
	for _, requirement := range byOrigin {
		// Canonical order per requirement, and canonical order overall,
		// so the contract a round is judged against is identical however
		// the evidence rows happen to come back. Provenance is carried
		// through unchanged: re-adopting here would stamp every loaded
		// obligation with whatever source the loader chose, which is how
		// an owner's requirement quietly becomes the adjudicator's.
		sort.Strings(requirement.Relations)
		merged = append(merged, requirement)
	}
	sort.SliceStable(merged, func(first, second int) bool {
		if merged[first].Subject != merged[second].Subject {
			return merged[first].Subject < merged[second].Subject
		}
		return merged[first].Source < merged[second].Source
	})
	return merged, nil
}

// mergeRequirement unions the relations one source demanded of one subject.
// It is never called across sources: doing so is what would let an
// adjudicator's obligation inherit the owner's authority.
func mergeRequirement(existing, incoming EvidenceRequirement) EvidenceRequirement {
	if existing.Subject == "" {
		return incoming
	}
	seen := map[string]struct{}{}
	for _, relation := range existing.Relations {
		seen[relation] = struct{}{}
	}
	for _, relation := range incoming.Relations {
		if _, already := seen[relation]; already {
			continue
		}
		existing.Relations = append(existing.Relations, relation)
	}
	return existing
}
