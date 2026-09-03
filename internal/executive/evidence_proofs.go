package executive

import "context"

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
