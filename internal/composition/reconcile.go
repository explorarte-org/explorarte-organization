package composition

import (
	"fmt"
	"sort"
	"time"
)

// StepKind is one lifecycle move the reconciler is prepared to make.
type StepKind string

const (
	// StepAdjudicate decides what happened to an episode that stopped
	// answering. It always outranks everything else: acting on a world
	// with unanswered questions in it is acting on a guess.
	StepAdjudicate StepKind = "adjudicate"

	// StepLeave takes an Active episode out of selection because it no
	// longer has the right to be there.
	StepLeave StepKind = "leave"

	// StepUnload finishes a teardown that nothing live is holding.
	StepUnload StepKind = "unload"

	// StepActivate promotes a Reloading episode that has earned it.
	StepActivate StepKind = "activate"
)

// Step is a single lifecycle move, with the reason it was chosen.
//
// The reason is not decoration. A reconciler that can only say what it did is
// unusable when it does something surprising, and the surprising cases are
// the only ones anybody reads the log for.
type Step struct {
	Kind        StepKind
	Episode     EpisodeID
	ComponentID string
	Reason      string
}

func (s Step) String() string {
	return fmt.Sprintf("%s %s (%s): %s", s.Kind, s.Episode, s.ComponentID, s.Reason)
}

// Next returns the single next safe move, or false when the composition has
// nothing left to do.
//
// One move, not a plan. A batch assumes the process survives to the end of
// it, and the whole reason this state is durable is that it does not. Every
// step is chosen from the world as it is now, so a crash halfway through
// costs exactly one step and the next observation starts over from what is
// actually there.
//
// The order is the argument:
//
//	adjudicate  resolve unknowns before acting on them
//	leave       withdraw what has lost the right to be Active
//	unload      finish teardowns nothing is holding
//	activate    only then bring something up
//
// Leaving outranks activating so a replacement can never have the old and
// new episode Active at once. Unloading outranks activating so the room is
// made before it is filled.
func Next(g *Graph, w *World, obs Observation, now time.Time) (Step, bool) {
	if lapsed := w.Lapsed(now); len(lapsed) > 0 {
		id := lapsed[0]
		e, _ := w.Episode(id)
		return Step{
			Kind: StepAdjudicate, Episode: id, ComponentID: e.ComponentID,
			Reason: fmt.Sprintf("lease lapsed at %s while %s", e.LeaseExpiresAt.UTC().Format(time.RFC3339), e.State),
		}, true
	}

	for _, componentID := range g.order {
		admissionErr := g.Admit(componentID, obs)
		for _, id := range w.episodesOf(componentID) {
			e, _ := w.Episode(id)
			if e.State == Active && admissionErr != nil {
				return Step{
					Kind: StepLeave, Episode: id, ComponentID: componentID,
					Reason: admissionErr.Error(),
				}, true
			}
		}
	}

	for _, componentID := range g.order {
		for _, id := range w.episodesOf(componentID) {
			e, _ := w.Episode(id)
			if e.State != Unloading {
				continue
			}
			if holders := w.Relied(id, now); len(holders) == 0 {
				return Step{
					Kind: StepUnload, Episode: id, ComponentID: componentID,
					Reason: "nothing live relies on it",
				}, true
			}
		}
	}

	for _, componentID := range g.order {
		if g.Admit(componentID, obs) != nil {
			continue
		}
		if w.activeEpisodeOf(componentID) != "" {
			continue
		}
		for _, id := range w.episodesOf(componentID) {
			e, _ := w.Episode(id)
			if e.State != Reloading || !e.live(now) {
				continue
			}
			if blocker := w.unreadyProvider(g, componentID, now); blocker != "" {
				continue
			}
			return Step{
				Kind: StepActivate, Episode: id, ComponentID: componentID,
				Reason: "admitted and every required key has an active provider",
			}, true
		}
	}

	return Step{}, false
}

// Apply performs one step against the world.
func Apply(w *World, step Step, now time.Time) error {
	switch step.Kind {
	case StepAdjudicate:
		return w.Adjudicate(step.Episode, Failed, now)
	case StepLeave:
		return w.Transition(step.Episode, Unloading, now)
	case StepUnload:
		return w.Transition(step.Episode, Inactive, now)
	case StepActivate:
		return w.Transition(step.Episode, Active, now)
	}
	return fmt.Errorf("composition: unknown step %q", step.Kind)
}

// episodesOf returns this component's episodes, sorted, so that step
// selection never depends on map iteration.
func (w *World) episodesOf(componentID string) []EpisodeID {
	var out []EpisodeID
	for id, e := range w.episodes {
		if e.ComponentID == componentID {
			out = append(out, id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// activeEpisodeOf returns the component's Active episode, if it has one.
func (w *World) activeEpisodeOf(componentID string) EpisodeID {
	for _, id := range w.episodesOf(componentID) {
		if e, _ := w.Episode(id); e.State == Active {
			return id
		}
	}
	return ""
}

// unreadyProvider names a required key whose provider has no Active episode,
// or "" when every requirement is being met right now.
func (w *World) unreadyProvider(g *Graph, componentID string, now time.Time) Key {
	c := g.components[componentID]
	for _, k := range sortedKeySlice(c.Requires) {
		ready := false
		for _, provider := range g.providers[k] {
			if w.activeEpisodeOf(provider) != "" {
				ready = true
				break
			}
		}
		if !ready {
			return k
		}
	}
	return ""
}
