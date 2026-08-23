package composition

import (
	"fmt"
	"sort"
)

// LoadWorld rebuilds a World from durable rows.
//
// It is separate from Start and Bind on purpose. Those are the domain's
// mutators and they enforce what may happen next; this restores what already
// happened, including states no single transition could reach in one step.
// What it still refuses is a set of rows that could never have been produced
// at all -- a duplicate episode, a binding whose endpoints do not exist, two
// Active episodes of one component -- because loading such a world would
// launder a corrupt record into a legitimate starting point.
func LoadWorld(episodes []Episode, bindings []CommittedBinding) (*World, error) {
	w := NewWorld()
	activeOf := map[string]EpisodeID{}
	for _, e := range episodes {
		if e.ID == "" || e.ComponentID == "" {
			return nil, fmt.Errorf("composition: a stored episode is missing its ID or component")
		}
		if _, dup := w.episodes[e.ID]; dup {
			return nil, fmt.Errorf("composition: episode %q appears twice", e.ID)
		}
		if !legalState(e.State) {
			return nil, fmt.Errorf("composition: episode %q has unknown state %q", e.ID, e.State)
		}
		if e.State == Active {
			if incumbent, taken := activeOf[e.ComponentID]; taken {
				return nil, fmt.Errorf("composition: %s has two active episodes, %q and %q", e.ComponentID, incumbent, e.ID)
			}
			activeOf[e.ComponentID] = e.ID
		}
		w.episodes[e.ID] = e
	}
	seen := map[string]struct{}{}
	for _, b := range bindings {
		if b.Key == "" {
			return nil, fmt.Errorf("composition: a stored binding names no key")
		}
		if _, ok := w.episodes[b.Consumer]; !ok {
			return nil, fmt.Errorf("composition: binding names unknown consumer episode %q", b.Consumer)
		}
		if _, ok := w.episodes[b.Provider]; !ok {
			return nil, fmt.Errorf("composition: binding names unknown provider episode %q", b.Provider)
		}
		if b.Consumer == b.Provider {
			return nil, fmt.Errorf("composition: episode %q is bound to itself", b.Consumer)
		}
		fingerprint := string(b.Consumer) + "\x00" + string(b.Key)
		if _, dup := seen[fingerprint]; dup {
			return nil, fmt.Errorf("composition: consumer %q has two bindings for key %q", b.Consumer, b.Key)
		}
		seen[fingerprint] = struct{}{}
		w.bindings = append(w.bindings, b)
	}
	sort.Slice(w.bindings, func(i, j int) bool {
		if w.bindings[i].Consumer != w.bindings[j].Consumer {
			return w.bindings[i].Consumer < w.bindings[j].Consumer
		}
		return w.bindings[i].Key < w.bindings[j].Key
	})
	return w, nil
}

func legalState(state Lifecycle) bool {
	switch state {
	case Inactive, Reloading, Active, Unloading, Failed:
		return true
	}
	return false
}

// Episodes returns every episode, sorted by ID, for persistence and reporting.
func (w *World) Episodes() []Episode {
	out := make([]Episode, 0, len(w.episodes))
	for _, e := range w.episodes {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
