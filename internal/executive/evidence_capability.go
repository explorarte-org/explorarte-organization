package executive

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/repositoryevidence"
)

// ErrEvidenceSensorUnavailable reports that the repository observer could not
// answer a capability question -- git broken, path unreadable, source error.
// It is deliberately NOT ErrContractRejected: an obligation cannot be blamed
// for being unprovable when the thing that would prove it never answered.
// AUTONOMY-SMOKE-017-R10's rule holds here too -- whose failure is it?
var ErrEvidenceSensorUnavailable = errors.New("repository sensor could not answer")

// jointAdmissionLimits is the capacity one snapshot's joint admission prices
// against: the same DefaultLimits the real context build will run with. A
// variable only so tests can shrink the world's budget and watch the union
// fail without building a repository big enough to starve the defaults.
var jointAdmissionLimits = repositoryevidence.DefaultLimits

// probeAdjudicationRequirements verifies the NEXT ROUND'S FULL CONTRACT
// against the PINNED tree before any of it becomes durable.
//
// R9 fixed what relations may be demanded; R10 exposed the subject axis:
// "DesignBaseSHA" and "InvocationBudget.Validate" are concepts and composite
// shapes that exist nowhere in the frozen tree as literals. Probing here, at
// the adjudicator's own contract boundary, turns that late dead end into a
// measured rejection the adjudicator can correct on its next attempt.
//
// The durable obligation set remains cumulative (checkpoint D), while a
// durable proof store makes its raw transport incremental. The boundary is a
// design round, never an individual subject:
//
//	inForce = obligations already recorded through the current round
//	novel   = proposals minus slots already in force (withoutSlotsAlreadyInForce)
//	PlanSlots(unproven(inForce)); mint every covered current slot
//	PlanSlots(novel); admit it but leave it raw for the next worker
//
// Both joint plans must have an empty Undelivered list. The next worker is
// still judged against inForce+novel, but receives exact durable refs for the
// former and raw excerpts for the latter. Without a proof store, the legacy
// full cumulative PlanSlots check remains unchanged.
//
// probeAdjudicationRequirements is JOINT admission (checkpoint D): it asks
// not "can each proposed slot be grounded on its own" but "can this SET be
// delivered together, by the same selection algorithm, under the SAME Limits
// the real snapshot will run with". PlanSlots is a dry-run of delivery, so an
// accepted adjudication is a strong host promise: every worker execution of
// the adopted round will receive each demanded slot, either as raw repository
// evidence or as an exact proof-backed ref. R15 proved why per-subject probes
// were not enough -- four subjects passed four independent probes with four
// full budgets, then one shared snapshot budget starved
// driveDesignFreeze/application and the preflight killed the worker. V7
// proved the other half: already-settled slots must not consume that shared
// raw budget forever.
//
// The rejection names every undelivered subject/relation pair and reaches the
// retry through the durable result_summary transport, so Luna can thin her
// demands or ground her proposal through existing symbols instead.
//
// The probe reads the delivered baseSHA explicitly (never HEAD) through the
// same Source the context builder uses. A sensor failure is reported as
// ErrEvidenceSensorUnavailable so it is never recorded as the adjudicator's rejection;
// "cannot fit together" is not a sensor failure, it IS the verdict.
func (o *Orchestrator) probeAdjudicationRequirements(ctx context.Context, root TaskRecord, proposals []EvidenceRequirementProposal) error {
	if len(proposals) == 0 {
		return nil
	}
	if o.repositorySource == nil {
		return fmt.Errorf("%w: adjudication proposed %d evidence requirements but no repository sensor is wired", ErrEvidenceSensorUnavailable, len(proposals))
	}
	baseSHA, err := o.frozenDesignBaseSHA(ctx, root)
	if err != nil {
		return err
	}
	// The durable contract is cumulative, but its transport cost is not. With
	// durable proofs wired, obligations that have already governed a worker
	// round are proven and minted first; genuinely novel proposals are then
	// admitted against a fresh snapshot budget. They are deliberately NOT
	// minted yet: the next worker still needs their raw excerpts in order to
	// do the newly requested design work. On the following adjudication those
	// slots are inForce, are minted, and become cheap carry-forward facts.
	all, err := o.tasks.ListByCorrelation(ctx, root.CorrelationID)
	if err != nil {
		return err
	}
	currentRound := o.activeDesignRound(ctx, all, root.ID)
	inForce, err := o.evidenceRequirementsForRound(ctx, root.ID, currentRound)
	if err != nil {
		return err
	}
	novel := withoutSlotsAlreadyInForce(proposals, inForce)
	adoptedNovel := AdoptEvidenceRequirements(novel, EvidenceFromAdjudication)

	// DURABLE-EVIDENCE-PROOF-CONTRACT: a slot already durably proven at this
	// exact base_sha costs nothing this round -- its raw evidence does not
	// need to be re-gathered to know the joint set can be delivered. This
	// changes only the ACCOUNTING (which slots must pay MaxRanges/MaxBytes/
	// MaxLines this round); the obligation itself stays exactly as durable
	// and monotonic in `candidate`/`inForce` as it always was.
	proven := map[EvidenceSlot]EvidenceProof{}
	if o.evidenceProofs != nil {
		proven, err = o.evidenceProofs.ValidProofs(ctx, root.ID, baseSHA)
		if err != nil {
			return fmt.Errorf("%w: read durable evidence proofs: %v", ErrEvidenceSensorUnavailable, err)
		}
	}
	limits := jointAdmissionLimits()
	if o.evidenceProofs == nil {
		// Compatibility for deployments without the durable store: preserve
		// the original cumulative joint-admission promise exactly.
		candidate := append(append([]EvidenceRequirement(nil), inForce...), adoptedNovel...)
		plan, planErr := o.planUnprovenSlots(ctx, baseSHA, limits, candidate, proven)
		if planErr != nil {
			return planErr
		}
		if len(plan.Undelivered) > 0 {
			return newCapacityConflict(baseSHA, limits, inForce, plan)
		}
		return nil
	}

	// Checkpoint 1: settle the round that actually ran. Mint only these
	// covered slots, so the next round can carry them by durable reference.
	currentPlan, planErr := o.planUnprovenSlots(ctx, baseSHA, limits, inForce, proven)
	if planErr != nil {
		return planErr
	}
	if len(currentPlan.Undelivered) > 0 {
		return newCapacityConflict(baseSHA, limits, inForce, currentPlan)
	}
	if failed := o.mintProofsForNewlyCovered(ctx, root.ID, baseSHA, currentPlan); failed > 0 {
		// The two-checkpoint scheme assumes every inForce slot was minted,
		// so the next worker carries inForce by durable reference and pays
		// raw transport only for novel. A mint that did not land breaks
		// that assumption: the next worker will need raw excerpts for BOTH
		// sets in ONE snapshot, a combination neither checkpoint measured,
		// and the round's preflight would then report
		// evidence_delivery_violation against a promise that was never
		// actually made. Fall back to the legacy joint plan over the full
		// cumulative set, so admission stays true to what will be delivered.
		// Re-probing slots that did mint is conservative, never unsound.
		candidate := append(append([]EvidenceRequirement(nil), inForce...), adoptedNovel...)
		jointPlan, jointErr := o.planUnprovenSlots(ctx, baseSHA, limits, candidate, proven)
		if jointErr != nil {
			return jointErr
		}
		if len(jointPlan.Undelivered) > 0 {
			return newCapacityConflict(baseSHA, limits, inForce, jointPlan)
		}
		return nil
	}

	// Checkpoint 2: prove that the NEW work fits one real worker snapshot.
	// Do not mint it here; admission proves deliverability, while minting is
	// the receipt that lets a later round avoid transporting it again.
	novelPlan, planErr := o.planUnprovenSlots(ctx, baseSHA, limits, adoptedNovel, proven)
	if planErr != nil {
		return planErr
	}
	if len(novelPlan.Undelivered) > 0 {
		return newCapacityConflict(baseSHA, limits, inForce, novelPlan)
	}
	return nil
}

func (o *Orchestrator) planUnprovenSlots(ctx context.Context, baseSHA string, limits repositoryevidence.Limits, requirements []EvidenceRequirement, proven map[EvidenceSlot]EvidenceProof) (repositoryevidence.CoveragePlan, error) {
	slots := evidenceSlots(requirements)
	probeSlots := make([]repositoryevidence.EvidenceSlot, 0, len(slots))
	for _, slot := range slots {
		if _, alreadyProven := proven[slot]; alreadyProven {
			continue
		}
		probeSlots = append(probeSlots, repositoryevidence.EvidenceSlot{Subject: slot.Subject, Relation: slot.Relation})
	}
	if len(probeSlots) == 0 {
		return repositoryevidence.CoveragePlan{}, nil
	}
	plan, err := repositoryevidence.PlanSlots(ctx, o.repositoryID, baseSHA,
		o.repositorySource, limits, 24, probeSlots)
	if err != nil {
		return repositoryevidence.CoveragePlan{}, fmt.Errorf("%w: joint evidence admission at %s: %v", ErrEvidenceSensorUnavailable, baseSHA, err)
	}
	return plan, nil
}

// mintProofsForNewlyCovered records a durable proof for each slot this
// round's dry-run actually classified as covered, from the exact fragment
// that classification came from -- never a second, independent read, and
// never anything a model supplied.
//
// Best-effort in the sense that a mint failure never fails a round that
// already passed the real admission check -- but NOT silent: it returns how
// many covered slots did not end up minted (store error, or no fragment
// classifying as the slot), so the caller can stop assuming the next round
// carries them by reference and re-measure the joint set it will actually
// have to transport raw.
func (o *Orchestrator) mintProofsForNewlyCovered(ctx context.Context, rootTaskID int64, baseSHA string, plan repositoryevidence.CoveragePlan) (failed int) {
	if o.evidenceProofs == nil {
		return 0
	}
	for _, slot := range plan.Covered {
		fragment, found := fragmentSatisfying(plan.Fragments, slot)
		if !found {
			failed++
			continue
		}
		if err := o.evidenceProofs.MintProof(ctx, EvidenceProof{
			OrganizationID:  o.organizationID,
			RootTaskID:      rootTaskID,
			Subject:         slot.Subject,
			Relation:        slot.Relation,
			BaseSHA:         baseSHA,
			SourceReference: fragment.Reference(),
			ContentDigest:   repositoryevidence.DigestOf(fragment.Content),
		}); err != nil {
			failed++
		}
	}
	return failed
}

// fragmentSatisfying finds the first gathered fragment whose real content
// classifies as slot's relation for slot's subject -- the same
// ExcerptRelations classifier admission itself already trusted, so a minted
// proof can never claim more than the dry-run actually established.
func fragmentSatisfying(fragments []repositoryevidence.Fragment, slot repositoryevidence.EvidenceSlot) (repositoryevidence.Fragment, bool) {
	for _, fragment := range fragments {
		if repositoryevidence.ExcerptRelations(fragment.Content, slot.Subject)[slot.Relation] {
			return fragment, true
		}
	}
	return repositoryevidence.Fragment{}, false
}

// CapacityConflict is the structured signal a plain-text rejection could
// not carry: which counts made the joint set undeliverable, and which
// slots are fixed (already in force, monotonic, cannot be dropped) versus
// which were this round's own novel demand. Its String form is what
// reaches the adjudicator's retry through the durable result_summary
// transport, the same path the old plain-text message used, but now with
// enough structure for a reader (or a future recovery policy) to tell
// "physically impossible given what is already owed" from "this specific
// selection was too greedy."
type CapacityConflict struct {
	BaseSHA string

	AvailableRanges int
	ConsumedRanges  int
	AvailableBytes  int
	ConsumedBytes   int
	AvailableLines  int
	ConsumedLines   int

	// AlreadyInForce is the fixed cost: obligations from earlier rounds,
	// monotonic, never droppable. Distinguishing this from Undelivered is
	// the whole point -- it tells a reader how much of the ceiling was
	// never available to this round's own request in the first place.
	AlreadyInForce []EvidenceSlot
	Undelivered    []repositoryevidence.EvidenceSlot
}

func newCapacityConflict(baseSHA string, limits repositoryevidence.Limits, inForce []EvidenceRequirement, plan repositoryevidence.CoveragePlan) error {
	consumedBytes, consumedLines := 0, 0
	for _, fragment := range plan.Fragments {
		consumedBytes += len(fragment.Content)
		if fragment.LineEnd >= fragment.LineStart {
			consumedLines += fragment.LineEnd - fragment.LineStart + 1
		}
	}
	conflict := CapacityConflict{
		BaseSHA:         baseSHA,
		AvailableRanges: limits.MaxRanges, ConsumedRanges: len(plan.Fragments),
		AvailableBytes: limits.MaxBytes, ConsumedBytes: consumedBytes,
		AvailableLines: limits.MaxLines, ConsumedLines: consumedLines,
		AlreadyInForce: evidenceSlots(inForce),
		Undelivered:    plan.Undelivered,
	}
	sort.Slice(conflict.AlreadyInForce, func(i, j int) bool {
		if conflict.AlreadyInForce[i].Subject != conflict.AlreadyInForce[j].Subject {
			return conflict.AlreadyInForce[i].Subject < conflict.AlreadyInForce[j].Subject
		}
		return conflict.AlreadyInForce[i].Relation < conflict.AlreadyInForce[j].Relation
	})
	sort.Slice(conflict.Undelivered, func(i, j int) bool {
		if conflict.Undelivered[i].Subject != conflict.Undelivered[j].Subject {
			return conflict.Undelivered[i].Subject < conflict.Undelivered[j].Subject
		}
		return conflict.Undelivered[i].Relation < conflict.Undelivered[j].Relation
	})
	return fmt.Errorf("%w: %s", ErrContractRejected, conflict.String())
}

func (c CapacityConflict) String() string {
	impossible := make([]string, 0, len(c.Undelivered))
	for _, slot := range c.Undelivered {
		impossible = append(impossible, slot.Subject+"/"+slot.Relation)
	}
	fixed := make([]string, 0, len(c.AlreadyInForce))
	for _, slot := range c.AlreadyInForce {
		fixed = append(fixed, slot.Subject+"/"+slot.Relation)
	}
	return fmt.Sprintf(
		"CAPACITY_CONFLICT at pin %s: undelivered=[%s] ranges=%d/%d bytes=%d/%d lines=%d/%d already_in_force(fixed, cannot be dropped)=[%s]",
		c.BaseSHA, strings.Join(impossible, ", "),
		c.ConsumedRanges, c.AvailableRanges,
		c.ConsumedBytes, c.AvailableBytes,
		c.ConsumedLines, c.AvailableLines,
		strings.Join(fixed, ", "),
	)
}
