package composition

import (
	"strings"
	"testing"
	"time"
)

func storedEpisode(id EpisodeID, component string, state Lifecycle) Episode {
	return Episode{ID: id, ComponentID: component, State: state, LeaseExpiresAt: t0.Add(time.Minute)}
}

// A restart must find the world exactly as it left it, including the states a
// running process was in the middle of.
func TestLoadWorldRestoresWhatWasHappening(t *testing.T) {
	w, err := LoadWorld(
		[]Episode{
			storedEpisode("orgd-1", "runtime-orgd", Unloading),
			storedEpisode("orgd-2", "runtime-orgd", Active),
			storedEpisode("controller-1", "composition-controller", Active),
		},
		[]CommittedBinding{{Key: KeyRuntimeObservedSHA, Consumer: "controller-1", Provider: "orgd-1"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	// The mid-replacement shape survives: the outgoing episode is still
	// held by the consumer that committed to it before it started leaving.
	if held := w.Relied("orgd-1", t0); len(held) != 1 || held[0] != "controller-1" {
		t.Fatalf("the grip must survive the restart that observed it: %v", held)
	}
	if err := w.Transition("orgd-1", Inactive, t0); err == nil {
		t.Fatal("a restored teardown is still blocked by its restored holder")
	}
}

func TestLoadWorldRefusesRowsThatCouldNeverHaveBeenProduced(t *testing.T) {
	for _, tc := range []struct {
		name     string
		episodes []Episode
		bindings []CommittedBinding
		want     string
	}{
		{
			name:     "two active episodes of one component",
			episodes: []Episode{storedEpisode("a", "runtime-orgd", Active), storedEpisode("b", "runtime-orgd", Active)},
			want:     "two active episodes",
		},
		{
			name:     "duplicate episode",
			episodes: []Episode{storedEpisode("a", "runtime-orgd", Active), storedEpisode("a", "runtime-orgd", Inactive)},
			want:     "appears twice",
		},
		{
			name:     "unknown state",
			episodes: []Episode{{ID: "a", ComponentID: "runtime-orgd", State: "zombie"}},
			want:     "unknown state",
		},
		{
			name:     "binding to a missing provider",
			episodes: []Episode{storedEpisode("a", "runtime-orgd", Active)},
			bindings: []CommittedBinding{{Key: KeyRuntimeObservedSHA, Consumer: "a", Provider: "ghost"}},
			want:     "unknown provider episode",
		},
		{
			name:     "two bindings for one consumer and key",
			episodes: []Episode{storedEpisode("a", "runtime-orgd", Active), storedEpisode("b", "egress-binding", Active), storedEpisode("c", "canonical-registry", Active)},
			bindings: []CommittedBinding{{Key: KeyRuntimeObservedSHA, Consumer: "a", Provider: "b"}, {Key: KeyRuntimeObservedSHA, Consumer: "a", Provider: "c"}},
			want:     "two bindings for key",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadWorld(tc.episodes, tc.bindings)
			if err == nil {
				t.Fatal("loading a corrupt record would launder it into a legitimate starting point")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q in %v", tc.want, err)
			}
		})
	}
}

// Loading restores; it does not re-run the rules that got there. An episode
// stored as Active must come back Active without having to pass through
// Reloading again.
func TestLoadWorldRestoresRatherThanReplays(t *testing.T) {
	w, err := LoadWorld([]Episode{storedEpisode("a", "runtime-orgd", Active)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	e, ok := w.Episode("a")
	if !ok || e.State != Active {
		t.Fatalf("wanted a restored active episode, got %+v", e)
	}
	if got := w.Episodes(); len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("Episodes must return what was loaded: %v", got)
	}
}
