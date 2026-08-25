package repositoryevidence

import (
	"context"
	"fmt"

	"github.com/Mireuz13/explorarte-organization/internal/contextengine"
)

// Provider answers a context build with excerpts of the repository at the
// commit the request names.
//
// It never chooses the commit. The request carries one already pinned, and a
// provider free to pick its own could hand a design a repository nobody
// decided to reason about -- which is the failure the pin exists to prevent,
// arriving from the other side.
type Provider struct {
	Repository string
	// BaseSHA is the world this provider is currently answering about, set
	// per build. ValidateVersion needs it: a staleness gate that only
	// checked for a non-empty version was a gate that opened for every
	// world, which is the opposite of what reuse protection is for.
	BaseSHA string
	Source  Source
	Limits  Limits
	// Window is how many lines around a match are read.
	Window int
}

func NewProvider(repository string, source Source, limits Limits, window int) (*Provider, error) {
	if repository == "" || source == nil {
		return nil, fmt.Errorf("%w: a provider needs a repository and a source", ErrInvalidFragment)
	}
	if err := limits.Validate(); err != nil {
		return nil, err
	}
	if window < 1 {
		window = 24
	}
	return &Provider{Repository: repository, Source: source, Limits: limits, Window: window}, nil
}

// ListRepositoryEvidence gathers what the request's own text points at.
//
// An empty result is returned as an ERROR rather than as an empty list. The
// caller asked for a repository to be observed; handing back nothing would be
// indistinguishable from a repository with nothing in it, and the execution
// would proceed to reason about code it never saw. The context engine turns
// this into a refusal, which is the loud failure blindness needs.
func (p *Provider) ListRepositoryEvidence(ctx context.Context, request contextengine.BuildRequest) ([]contextengine.SourceRecord, error) {
	if request.RepositoryBaseSHA == "" {
		return nil, nil
	}
	p.BaseSHA = request.RepositoryBaseSHA
	explorer, err := NewExplorer(p.Repository, request.RepositoryBaseSHA, p.Source, p.Limits)
	if err != nil {
		return nil, err
	}
	selection := SelectionForRequirements(request.RepositoryQuery, request.RepositorySubjects, p.Window)
	// The normative slots ride into selection: PASS 0 satisfies each
	// (subject, relation) before any incidental exploration spends budget.
	for _, slot := range request.RepositorySlots {
		selection.Slots = append(selection.Slots, EvidenceSlot{Subject: slot.Subject, Relation: slot.Relation})
	}
	fragments, err := Gather(ctx, explorer, selection)
	if err != nil {
		return nil, err
	}
	if len(fragments) == 0 {
		return nil, fmt.Errorf("%w: nothing in %s matched what this execution is about", ErrNoEvidenceFound, request.RepositoryBaseSHA)
	}
	return RenderBundle(fragments, request.RepositoryBaseSHA)
}

// ValidateVersion refuses a stored source that no longer describes the commit
// the execution is about.
//
// Snapshots are reused, and a reused snapshot whose evidence cites another
// commit is not slightly out of date -- it describes a different repository.
func (p *Provider) ValidateVersion(_ context.Context, _ string, source contextengine.SourceRecord) error {
	if source.Kind != contextengine.SourceRepositoryEvidence {
		return nil
	}
	if source.Version == "" {
		return fmt.Errorf("%w: repository evidence with no commit", ErrStaleEvidence)
	}
	// Equality, not presence. A reused snapshot whose evidence cites another
	// commit is not slightly out of date -- it describes a different
	// repository, and validating it would let snapshot reuse carry a design
	// into a world nobody decided on.
	if p.BaseSHA != "" && source.Version != p.BaseSHA {
		return fmt.Errorf("%w: reused evidence cites %s but this execution is about %s",
			ErrStaleEvidence, source.Version, p.BaseSHA)
	}
	return nil
}

var _ contextengine.RepositoryEvidenceProvider = (*Provider)(nil)
