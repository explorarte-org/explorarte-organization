package executive

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// EvidenceProof is the durable record DURABLE-EVIDENCE-PROOF-CONTRACT
// (docs/reports/DURABLE-EVIDENCE-PROOF-CONTRACT.md) adds to close
// EVIDENCE_CAPACITY_LIVENESS_INCOMPLETENESS
// (docs/reports/CAPACITY-LIVENESS-INVESTIGATION.md): once a (subject,
// relation) has genuinely been shown deliverable against a base_sha, a
// later round's joint-capacity probe should not have to re-spend raw
// evidence budget proving it again.
//
// A proof does not relax obligation monotonicity (an obligation is never
// discharged or removed from what evidenceRequirementsForRound accumulates)
// or provenance strictness (SourceReference/ContentDigest always trace to
// real, host-classified content, never a model's assertion). It only
// changes whether that content must be re-transported every round.
type EvidenceProof struct {
	ID              int64
	OrganizationID  string
	RootTaskID      int64
	Subject         string
	Relation        string
	BaseSHA         string
	SourceReference string
	ContentDigest   string
}

// EvidenceProofStore is the host-only write/read surface for durable
// proofs. There is deliberately no method here a worker's own result could
// drive: MintProof is called from exactly one place
// (probeAdjudicationRequirements, evidence_capability.go), fed only by a
// PlanSlots dry-run's own Fragments -- never by anything a model produced.
type EvidenceProofStore interface {
	// ValidProofs returns every non-invalidated proof for rootTaskID at
	// baseSHA, keyed by the slot it discharges. A caller uses this to
	// exclude already-proven slots from a fresh joint-capacity probe.
	ValidProofs(ctx context.Context, rootTaskID int64, baseSHA string) (map[EvidenceSlot]EvidenceProof, error)
	// MintProof durably records that a slot was proven. It must be called
	// only with a fragment PlanSlots' own dry-run actually classified as
	// satisfying this exact (subject, relation) -- see mintProofsForNewlyCovered
	// in evidence_capability.go for the one call site.
	MintProof(ctx context.Context, proof EvidenceProof) error
	// InvalidateProofs tombstones every valid proof for rootTaskID whose
	// BaseSHA no longer matches currentBaseSHA -- called from the same pass
	// that already applies ReasonWorldChangedSinceFreeze
	// (design_freeze_phase.go), so a proof never outlives the freeze it was
	// minted under.
	InvalidateProofs(ctx context.Context, rootTaskID int64, currentBaseSHA string) error
}

// WithEvidenceProofs wires durable proof persistence into joint-capacity
// admission.
//
// Optional, matching WithRepositoryEvidenceSource's own posture: without it,
// probeAdjudicationRequirements behaves exactly as it did before this
// contract existed -- every round re-probes the full cumulative obligation
// set from scratch. A deployment that has not run this migration, or has
// not wired a store, degrades to the old (correct, just non-liveness-
// recovering) behavior rather than failing closed.
func WithEvidenceProofs(store EvidenceProofStore) OrchestratorOption {
	return func(o *Orchestrator) { o.evidenceProofs = store }
}

// validEvidenceProofs reads the carry-forward capabilities available to one
// execution. An unwired store means the old full-transport behavior; a wired
// store that cannot answer is a sensor outage, never permission to forget a
// cumulative obligation.
func (o *Orchestrator) validEvidenceProofs(ctx context.Context, rootTaskID int64, baseSHA string) (map[EvidenceSlot]EvidenceProof, error) {
	if o.evidenceProofs == nil || rootTaskID == 0 || strings.TrimSpace(baseSHA) == "" {
		return map[EvidenceSlot]EvidenceProof{}, nil
	}
	proofs, err := o.evidenceProofs.ValidProofs(ctx, rootTaskID, baseSHA)
	if err != nil {
		return nil, fmt.Errorf("%w: read durable evidence proofs: %v", ErrEvidenceSensorUnavailable, err)
	}
	return proofs, nil
}

// requirementsWithoutProofs is the raw evidence payload for this execution.
// Requirements remain cumulative elsewhere; only slots with an exact durable
// proof at the frozen base are removed from repository transport.
func requirementsWithoutProofs(required []EvidenceRequirement, proofs map[EvidenceSlot]EvidenceProof) []EvidenceRequirement {
	if len(proofs) == 0 {
		return append([]EvidenceRequirement(nil), required...)
	}
	remaining := make([]EvidenceRequirement, 0, len(required))
	for _, requirement := range required {
		copyRequirement := requirement
		copyRequirement.Relations = nil
		for _, relation := range requirement.Relations {
			if _, proven := proofs[EvidenceSlot{Subject: requirement.Subject, Relation: relation}]; !proven {
				copyRequirement.Relations = append(copyRequirement.Relations, relation)
			}
		}
		if len(copyRequirement.Relations) > 0 {
			remaining = append(remaining, copyRequirement)
		}
	}
	return remaining
}

func addProofBackedSupply(available map[EvidenceSlot][]string, required []EvidenceRequirement, proofs map[EvidenceSlot]EvidenceProof) map[EvidenceSlot][]string {
	if available == nil {
		available = map[EvidenceSlot][]string{}
	}
	for _, slot := range evidenceSlots(required) {
		if proof, ok := proofs[slot]; ok && strings.TrimSpace(proof.SourceReference) != "" {
			available[slot] = append(available[slot], proof.SourceReference)
		}
	}
	return available
}

// proofCarryForwardGuidance gives the worker exactly the host-authored facts
// it may carry without retransmitting old source. It contains references, not
// repository content, and is rendered from the same map used by validation.
func proofCarryForwardGuidance(required []EvidenceRequirement, proofs map[EvidenceSlot]EvidenceProof) string {
	var slots []EvidenceSlot
	for _, slot := range evidenceSlots(required) {
		if proof, ok := proofs[slot]; ok && strings.TrimSpace(proof.SourceReference) != "" {
			slots = append(slots, slot)
		}
	}
	if len(slots) == 0 {
		return ""
	}
	sort.Slice(slots, func(i, j int) bool {
		if slots[i].Subject != slots[j].Subject {
			return slots[i].Subject < slots[j].Subject
		}
		return slots[i].Relation < slots[j].Relation
	})
	var guidance strings.Builder
	guidance.WriteString("Durable repository evidence carried forward from earlier design rounds:\n\n")
	for _, slot := range slots {
		fmt.Fprintf(&guidance, "- subject=%q, relation=%q, ref=%q\n", slot.Subject, slot.Relation, proofs[slot].SourceReference)
	}
	guidance.WriteString(`
These exact refs are host-verified repository evidence at this campaign's frozen commit. Their raw excerpts are intentionally not retransmitted in this execution.
For every carried slot, copy the exact subject, relation and ref into evidence[] so the cumulative contract remains explicit. Do not alter, widen or invent a carried ref.`)
	return guidance.String()
}
