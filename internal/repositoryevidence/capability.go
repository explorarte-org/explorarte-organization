package repositoryevidence

import (
	"context"
	"strings"
)

// maxProbeReads bounds how many excerpts one subject's probe may read.
//
// rankHits already puts a declaration first and an application second when
// both exist, so a handful of reads is enough to know what the pinned world
// can answer for. The probe is a feasibility question, not an inventory.
const maxProbeReads = 4

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
		relation, mentions := ClassifyExcerpt(fragment.Content, subject)
		if !mentions {
			continue
		}
		if _, demanded := wanted[relation]; demanded && !wanted[relation] {
			wanted[relation] = true
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
