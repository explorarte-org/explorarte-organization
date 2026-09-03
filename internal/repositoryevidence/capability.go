package repositoryevidence

import (
	"context"
	"sort"
	"strings"
)

// maxProbeReads bounds how many excerpts one subject's probe may read.
//
// rankHits already puts a declaration first and an application second when
// both exist, so a handful of reads is enough to know what the pinned world
// can answer for. The probe is a feasibility question, not an inventory.
const maxProbeReads = 4

// CoveragePlan is the outcome of joint admission: which slots the pinned
// world can deliver TOGETHER under one shared budget, and which it cannot.
type CoveragePlan struct {
	// Covered lists every demanded slot the dry-run delivered.
	Covered []EvidenceSlot
	// Undelivered lists the slots that exist in no deliverable arrangement:
	// either the subject is absent from the pin, or admitting the set as a
	// whole would need more capacity than one snapshot has.
	Undelivered []EvidenceSlot
	// Fragments is every excerpt the dry-run actually gathered to deliver
	// Covered -- the same real, host-read content GatherWithCoverage used to
	// answer the admission question, not a second, separate read. A caller
	// that wants to durably prove a covered slot (DURABLE-EVIDENCE-PROOF-
	// CONTRACT) finds its grounding fragment here, from the exact dry-run
	// that established admission, rather than re-reading the tree.
	Fragments []Fragment
}

// PlanSlots is joint admission (checkpoint D): it answers not "can each
// subject be grounded on its own" but "can THIS SET of slots be delivered
// together, by the same selection algorithm, under the SAME Limits the real
// snapshot will run with".
//
// It is literally a dry-run of delivery -- GatherWithCoverage over one shared
// explorer with one shared budget -- so admission and delivery are the same
// code path and cannot disagree. The per-subject probe this replaces gave
// every subject its own full DefaultLimits; R15 proved the gap: four
// adjudicated subjects passed four independent probes, then round 2's real
// snapshot starved driveDesignFreeze/application under the single shared
// budget, and the preflight killed the worker before any model call.
//
// An error returns only when the SENSOR could not answer -- search failed, an
// excerpt could not be read. "Cannot fit" is not an error; it is the honest
// admission verdict, reported through Undelivered for the caller to turn into
// a correctable contract rejection.
func PlanSlots(ctx context.Context, repositoryID, baseSHA string, source Source, limits Limits, window int, slots []EvidenceSlot) (CoveragePlan, error) {
	subjects := make([]string, 0, len(slots))
	seen := map[string]struct{}{}
	for _, slot := range slots {
		subject := strings.TrimSpace(slot.Subject)
		if subject == "" {
			continue
		}
		if _, already := seen[subject]; already {
			continue
		}
		seen[subject] = struct{}{}
		subjects = append(subjects, subject)
	}
	if len(subjects) == 0 || len(slots) == 0 {
		return CoveragePlan{Covered: []EvidenceSlot{}, Undelivered: []EvidenceSlot{}}, nil
	}
	explorer, err := NewExplorer(repositoryID, baseSHA, source, limits)
	if err != nil {
		return CoveragePlan{}, err
	}
	selection := Selection{
		Terms:         subjects,
		RequiredTerms: subjects,
		Slots:         slots,
		Window:        window,
	}
	fragments, uncovered, err := GatherWithCoverage(ctx, explorer, selection)
	if err != nil {
		return CoveragePlan{}, err
	}
	coveredSet := map[EvidenceSlot]bool{}
	for _, slot := range slots {
		coveredSet[slot] = true
	}
	for _, slot := range uncovered {
		delete(coveredSet, slot)
	}
	plan := CoveragePlan{Covered: []EvidenceSlot{}, Undelivered: []EvidenceSlot{}, Fragments: fragments}
	for _, slot := range slots {
		if coveredSet[slot] {
			plan.Covered = append(plan.Covered, slot)
			continue
		}
		plan.Undelivered = append(plan.Undelivered, slot)
	}
	sort.Slice(plan.Undelivered, func(first, second int) bool {
		if plan.Undelivered[first].Subject != plan.Undelivered[second].Subject {
			return plan.Undelivered[first].Subject < plan.Undelivered[second].Subject
		}
		return plan.Undelivered[first].Relation < plan.Undelivered[second].Relation
	})
	return plan, nil
}

// ProbeSubjectSupply answers which of the demanded relations the PINNED world
// can mechanically fill for one subject, using the same classifier the
// preflight will later trust.
//
// This is the authority that keeps an adjudicator from binding a round to
// obligations no snapshot could ever satisfy (AUTONOMY-SMOKE-017-R10): a slot
// is supplyable when some excerpt of the frozen tree classifies as the demanded
// relation -- nothing else counts, and nothing else is asked.
//
// The explorer is built per subject from the delivered baseSHA, so answers are
// about exactly the commit under test and never about HEAD. An error returns
// only when the SENSOR could not answer -- search failed, a candidate excerpt
// could not be read; "not found" is an answer, reported through the returned
// map as false. Callers must treat an error as infrastructure, never as a
// supply verdict.
func ProbeSubjectSupply(ctx context.Context, repositoryID, baseSHA string, source Source, limits Limits, subject string, relations []string, window int) (map[string]bool, error) {
	subject = strings.TrimSpace(subject)
	wanted := make(map[string]bool, len(relations))
	for _, relation := range relations {
		if strings.TrimSpace(relation) != "" {
			wanted[relation] = false
		}
	}
	if subject == "" || len(wanted) == 0 {
		return wanted, nil
	}
	explorer, err := NewExplorer(repositoryID, baseSHA, source, limits)
	if err != nil {
		return nil, err
	}
	matches, err := explorer.Search(ctx, subject)
	if err != nil {
		return nil, err
	}
	read := 0
	for _, match := range matches {
		if read >= maxProbeReads {
			break
		}
		fragment, readErr := explorer.ReadAround(ctx, match, window)
		if readErr != nil {
			// A candidate that cannot be READ is not a candidate that says
			// "no". Skipping it here would turn an observer's silence into a
			// verdict against the obligation -- exactly the misattribution
			// the supply split exists to prevent. Report the outage and let
			// the caller classify it as infrastructure.
			return nil, readErr
		}
		read++
		// One excerpt can prove several roles at once when it physically
		// contains a declaration and a use closer than one window apart.
		// Asking the monovalued classifier here would let the definition
		// answer shadow the application the same fragment demonstrates.
		for relation := range ExcerptRelations(fragment.Content, subject) {
			if _, demanded := wanted[relation]; demanded && !wanted[relation] {
				wanted[relation] = true
			}
		}
		allFound := true
		for _, found := range wanted {
			if !found {
				allFound = false
				break
			}
		}
		if allFound {
			break
		}
	}
	return wanted, nil
}
