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

// probeAdjudicationRequirements verifies every proposed slot against the
// PINNED tree before it can become a durable obligation of the next round.
//
// R9 fixed what relations may be demanded; R10 exposed the subject axis:
// "DesignBaseSHA" and "InvocationBudget.Validate" are concepts and composite
// shapes that exist nowhere in the frozen tree as literals, so no excerpt can
// ever classify as their definition or application. Probing here, at the
// adjudicator's own contract boundary, turns that late dead end into a
// measured rejection the adjudicator can correct on its next attempt.
//
// probeAdjudicationRequirements is JOINT admission (checkpoint D): it asks
// not "can each proposed slot be grounded on its own" but "can this SET be
// delivered together, by the same selection algorithm, under the SAME Limits
// the real snapshot will run with". PlanSlots is a dry-run of delivery, so an
// accepted adjudication is a strong host promise: every worker snapshot of
// the adopted round will contain each demanded slot. R15 proved why per-
// subject probes were not enough -- four subjects passed four independent
// probes with four full budgets, then one shared snapshot budget starved
// driveDesignFreeze/application and the preflight killed the worker.
//
// The rejection names every undelivered subject/relation pair and reaches the
// retry through the durable result_summary transport, so Luna can thin her
// demands or ground her proposal through existing symbols instead.
//
// The probe reads the delivered baseSHA explicitly (never HEAD) through the
// same Source the context builder uses. A sensor failure is reported as
// ErrEvidenceSensorUnavailable so it is never recorded as Luna's rejection;
// "cannot fit together" is not a sensor failure, it IS the verdict.
func (o *Orchestrator) probeAdjudicationRequirements(ctx context.Context, root TaskRecord, proposals []EvidenceRequirementProposal) error {
	if len(proposals) == 0 || o.repositorySource == nil {
		return nil
	}
	baseSHA, err := o.frozenDesignBaseSHA(ctx, root)
	if err != nil {
		return err
	}
	slots := make([]repositoryevidence.EvidenceSlot, 0, len(proposals)*2)
	for _, proposal := range proposals {
		subject := strings.TrimSpace(proposal.Subject)
		if subject == "" {
			continue
		}
		for _, relation := range proposal.Relations {
			slots = append(slots, repositoryevidence.EvidenceSlot{Subject: subject, Relation: relation})
		}
	}
	plan, planErr := repositoryevidence.PlanSlots(ctx, o.repositoryID, baseSHA,
		o.repositorySource, repositoryevidence.DefaultLimits(), 24, slots)
	if planErr != nil {
		return fmt.Errorf("%w: joint evidence admission at %s: %v", ErrEvidenceSensorUnavailable, baseSHA, planErr)
	}
	if len(plan.Undelivered) == 0 {
		return nil
	}
	impossible := make([]string, 0, len(plan.Undelivered))
	for _, slot := range plan.Undelivered {
		impossible = append(impossible, slot.Subject+"/"+slot.Relation)
	}
	sort.Strings(impossible)
	return fmt.Errorf("%w: joint evidence capacity cannot deliver, at pin %s: %s",
		ErrContractRejected, baseSHA, strings.Join(impossible, ", "))
}
