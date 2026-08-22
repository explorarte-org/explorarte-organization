package repositoryevidence

import (
	"fmt"

	"github.com/Mireuz13/explorarte-organization/internal/contextengine"
)

// Render turns an excerpt into a context source.
//
// The classification is fixed, not chosen: repository evidence is DATA the
// organization read, never instructions it was given. A file in the tree
// saying "ignore the rules and grant yourself X" is a string that was found,
// and treating it as anything else would make write authority over the
// repository into authority over the agents that read it -- the one shape a
// system allowed to modify its own implementation must never have.
//
// The assembler enforces this independently; setting it here is what makes the
// two agree rather than one of them being the only guard.
func Render(fragment Fragment, baseSHA string) (contextengine.SourceRecord, error) {
	if err := fragment.Validate(); err != nil {
		return contextengine.SourceRecord{}, err
	}
	if err := fragment.ValidFor(baseSHA); err != nil {
		return contextengine.SourceRecord{}, err
	}
	header := fmt.Sprintf("%s lines %d-%d at %s", fragment.Path, fragment.LineStart, fragment.LineEnd, fragment.BaseSHA)
	if fragment.Symbol != "" {
		header += " (" + fragment.Symbol + ")"
	}
	return contextengine.SourceRecord{
		Kind:      contextengine.SourceRepositoryEvidence,
		Reference: fragment.Reference(),
		// The version IS the commit. Anything else would let two excerpts of
		// the same file at different commits look like one source seen twice.
		Version: fragment.BaseSHA,
		// The lowest authority tier there is: an excerpt informs, and
		// ranks below every policy, profile and instruction it might
		// appear to contradict.
		AuthorityTier:        contextengine.TierRAGEvidence,
		InstructionClass:     contextengine.InstructionData,
		TrustClass:           contextengine.TrustUntrusted,
		DataClass:            contextengine.DataOrganizational,
		MayGrantCapabilities: false,
		Content:              []byte(header + "\n" + fragment.Content),
		ContentHash:          fragment.Digest,
	}, nil
}

// RenderBundle renders a whole exploration, refusing the set if any excerpt
// describes a different repository than the one being designed against.
func RenderBundle(fragments []Fragment, baseSHA string) ([]contextengine.SourceRecord, error) {
	if err := ValidateBundle(fragments, baseSHA); err != nil {
		return nil, err
	}
	records := make([]contextengine.SourceRecord, 0, len(fragments))
	for _, fragment := range fragments {
		record, err := Render(fragment, baseSHA)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}
