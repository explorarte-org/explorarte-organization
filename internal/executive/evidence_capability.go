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
// R9 fixed what relations may be demanded; R10 exposed the remaining axis:
// subjects. "DesignBaseSHA" and "InvocationBudget.Validate" are concepts and
// composite shapes that exist nowhere in the frozen tree as literals, so no
// excerpt can ever classify as their definition or application -- and a round
// carrying them was doomed at adoption, discovered only after planning had
// run. Probing here, at the adjudicator's own contract boundary, turns that
// late dead end into a measured rejection the adjudicator can correct on its
// next attempt: the reason names each impossible subject/relation pair and
// reaches the retry through the durable result_summary transport.
//
// The probe reads the delivered baseSHA explicitly (never HEAD) through the
// same Source the context builder uses, and answers with the same classifier
// the preflight trusts. A sensor failure is reported as
// ErrEvidenceSensorUnavailable so it is never recorded as Luna's rejection.
func (o *Orchestrator) probeAdjudicationRequirements(ctx context.Context, root TaskRecord, proposals []EvidenceRequirementProposal) error {
	if len(proposals) == 0 || o.repositorySource == nil {
		return nil
	}
	baseSHA, err := o.frozenDesignBaseSHA(ctx, root)
	if err != nil {
		return err
	}
	var impossible []string
	for _, proposal := range proposals {
		subject := strings.TrimSpace(proposal.Subject)
		if subject == "" {
			continue
		}
		supplied, probeErr := repositoryevidence.ProbeSubjectSupply(
			ctx, o.repositoryID, baseSHA, o.repositorySource,
			repositoryevidence.DefaultLimits(), subject, proposal.Relations, 24)
		if probeErr != nil {
			return fmt.Errorf("%w: probing %q at %s: %v", ErrEvidenceSensorUnavailable, subject, baseSHA, probeErr)
		}
		for _, relation := range proposal.Relations {
			if !supplied[relation] {
				impossible = append(impossible, subject+"/"+relation)
			}
		}
	}
	if len(impossible) == 0 {
		return nil
	}
	sort.Strings(impossible)
	return fmt.Errorf("%w: adjudicated obligations the pinned repository (%s) cannot supply: %s",
		ErrContractRejected, baseSHA, strings.Join(impossible, ", "))
}
