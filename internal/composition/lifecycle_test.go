package composition

import (
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)

// active starts an episode and brings it to Active with a one minute lease.
func active(t *testing.T, w *World, id EpisodeID, component string) {
	t.Helper()
	if err := w.Start(id, component, t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := w.Transition(id, Active, t0); err != nil {
		t.Fatal(err)
	}
}

// This is the correction that keeps a durable binding from becoming a
// permanent one. A consumer that crashes while Active leaves its binding
// behind, and without liveness that binding answers "somebody still needs
// this" forever -- the provider it holds could never finish leaving.
func TestADeadConsumerStopsHoldingItsProvider(t *testing.T) {
	w := NewWorld()
	active(t, w, "provider-54", "runtime-orgd")
	active(t, w, "consumer-91", "assignment-controller")
	if err := w.Bind(CommittedBinding{Key: KeyRuntimeObservedSHA, Consumer: "consumer-91", Provider: "provider-54"}, t0); err != nil {
		t.Fatal(err)
	}
	if err := w.Transition("provider-54", Unloading, t0); err != nil {
		t.Fatal(err)
	}

	// While the consumer is alive it holds the provider, by name.
	held := w.Relied("provider-54", t0)
	if len(held) != 1 || held[0] != "consumer-91" {
		t.Fatalf("a live consumer must hold its provider by name, got %v", held)
	}
	err := w.Transition("provider-54", Inactive, t0)
	if err == nil || !strings.Contains(err.Error(), "consumer-91") {
		t.Fatalf("teardown must be refused and must name who is blocking it: %v", err)
	}

	// The consumer's process dies. Nobody reports it; the lease simply
	// stops being renewed. The provider is fine and keeps renewing, which
	// is what isolates the variable: the only thing that changed is that
	// one holder stopped answering.
	dead := t0.Add(2 * time.Minute)
	if err := w.Heartbeat("provider-54", dead.Add(time.Minute), t0.Add(30*time.Second)); err != nil {
		t.Fatal(err)
	}
	if held := w.Relied("provider-54", dead); len(held) != 0 {
		t.Fatalf("a consumer that stopped answering must not keep holding anything, got %v", held)
	}
	if err := w.Transition("provider-54", Inactive, dead); err != nil {
		t.Fatalf("teardown must be free once nothing live relies on it: %v", err)
	}
}

func TestAnUnloadingProviderKeepsItsConsumersButTakesNoNewOnes(t *testing.T) {
	w := NewWorld()
	active(t, w, "old-provider", "runtime-orgd")
	active(t, w, "early-consumer", "assignment-controller")
	if err := w.Bind(CommittedBinding{Key: KeyRuntimeObservedSHA, Consumer: "early-consumer", Provider: "old-provider"}, t0); err != nil {
		t.Fatal(err)
	}
	if err := w.Transition("old-provider", Unloading, t0); err != nil {
		t.Fatal(err)
	}

	// Leaving and being gone are different. The consumer that already
	// committed keeps its binding, and that interval is the whole point.
	if held := w.Relied("old-provider", t0); len(held) != 1 {
		t.Fatalf("an existing consumer keeps its binding through Unloading, got %v", held)
	}
	active(t, w, "late-consumer", "composition-controller")
	err := w.Bind(CommittedBinding{Key: KeyRuntimeObservedSHA, Consumer: "late-consumer", Provider: "old-provider"}, t0)
	if err == nil || !strings.Contains(err.Error(), "not selectable") {
		t.Fatalf("a provider on its way out must not accept new consumers: %v", err)
	}
}

// orgd going Active, restarting, and going Active again is two episodes with
// one ComponentID. A binding left by the first must never look like it
// belongs to the second.
func TestANewEpisodeDoesNotInheritTheOldEpisodesBindings(t *testing.T) {
	w := NewWorld()
	active(t, w, "orgd-1", "runtime-orgd")
	active(t, w, "consumer", "assignment-controller")
	if err := w.Bind(CommittedBinding{Key: KeyRuntimeObservedSHA, Consumer: "consumer", Provider: "orgd-1"}, t0); err != nil {
		t.Fatal(err)
	}

	// Same component, new activation. The incumbent has to leave selection
	// first, which is the only way two episodes of one component ever
	// coexist.
	if err := w.Transition("orgd-1", Unloading, t0); err != nil {
		t.Fatal(err)
	}
	active(t, w, "orgd-2", "runtime-orgd")
	if held := w.Relied("orgd-2", t0); len(held) != 0 {
		t.Fatalf("the new episode inherited a binding it never accepted: %v", held)
	}
	if held := w.Relied("orgd-1", t0); len(held) != 1 {
		t.Fatalf("the old episode still owns its own binding, got %v", held)
	}
}

func TestALapsedLeaseIsAQuestionNotAnAnswer(t *testing.T) {
	w := NewWorld()
	active(t, w, "orgd", "runtime-orgd")
	late := t0.Add(2 * time.Minute)

	if lapsed := w.Lapsed(late); len(lapsed) != 1 || lapsed[0] != "orgd" {
		t.Fatalf("an episode that stopped renewing must be reported as lapsed, got %v", lapsed)
	}
	// It may not quietly come back to life...
	if err := w.Heartbeat("orgd", late.Add(time.Minute), late); err == nil {
		t.Fatal("renewing a lapsed lease would erase the gap that says something went wrong")
	}
	// ...nor be moved as if somebody knew what happened to it.
	err := w.Transition("orgd", Unloading, late)
	if err == nil || !strings.Contains(err.Error(), "must be adjudicated") {
		t.Fatalf("a lapsed episode must be adjudicated, not transitioned: %v", err)
	}
	if err := w.Adjudicate("orgd", Failed, late); err != nil {
		t.Fatal(err)
	}
	if lapsed := w.Lapsed(late); len(lapsed) != 0 {
		t.Fatalf("an adjudicated episode is no longer an open question: %v", lapsed)
	}
}

func TestAdjudicationKeepsTheBindingsAsEvidence(t *testing.T) {
	w := NewWorld()
	active(t, w, "provider", "runtime-orgd")
	active(t, w, "consumer", "assignment-controller")
	if err := w.Bind(CommittedBinding{Key: KeyRuntimeObservedSHA, Consumer: "consumer", Provider: "provider"}, t0); err != nil {
		t.Fatal(err)
	}
	late := t0.Add(2 * time.Minute)
	if err := w.Adjudicate("consumer", Failed, late); err != nil {
		t.Fatal(err)
	}

	// The cheap move is to delete the binding on expiry. The cheap move
	// throws away the only record of what was committed. Keeping it costs
	// nothing, because a holder that is not live already stops holding.
	if got := w.Bindings(); len(got) != 1 {
		t.Fatalf("the binding is evidence of what was committed and must survive: %v", got)
	}
	if held := w.Relied("provider", late); len(held) != 0 {
		t.Fatalf("an adjudicated consumer must not still hold: %v", held)
	}
}

func TestAValidLeaseIsNotAdjudicable(t *testing.T) {
	w := NewWorld()
	active(t, w, "orgd", "runtime-orgd")
	if err := w.Adjudicate("orgd", Failed, t0); err == nil {
		t.Fatal("an episode that is still answering must never be declared dead")
	}
}

func TestHeartbeatKeepsAnEpisodeHolding(t *testing.T) {
	w := NewWorld()
	active(t, w, "provider", "runtime-orgd")
	active(t, w, "consumer", "assignment-controller")
	if err := w.Bind(CommittedBinding{Key: KeyRuntimeObservedSHA, Consumer: "consumer", Provider: "provider"}, t0); err != nil {
		t.Fatal(err)
	}
	half := t0.Add(30 * time.Second)
	if err := w.Heartbeat("consumer", half.Add(time.Minute), half); err != nil {
		t.Fatal(err)
	}
	later := t0.Add(80 * time.Second)
	if held := w.Relied("provider", later); len(held) != 1 {
		t.Fatalf("a consumer that renewed is still there and still holds, got %v", held)
	}
}

func TestIllegalTransitionsAreRefused(t *testing.T) {
	for _, tc := range []struct {
		name string
		from Lifecycle
		to   Lifecycle
	}{
		{"reloading straight to unloading", Reloading, Unloading},
		{"active back to reloading", Active, Reloading},
		{"active straight to inactive", Active, Inactive},
		{"unloading back to active", Unloading, Active},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := NewWorld()
			if err := w.Start("e", "runtime-orgd", t0.Add(time.Minute)); err != nil {
				t.Fatal(err)
			}
			for _, step := range []Lifecycle{Active, Unloading} {
				if e, _ := w.Episode("e"); e.State == tc.from {
					break
				}
				if err := w.Transition("e", step, t0); err != nil {
					t.Fatal(err)
				}
			}
			if e, _ := w.Episode("e"); e.State != tc.from {
				t.Fatalf("setup left the episode in %s, wanted %s", e.State, tc.from)
			}
			if err := w.Transition("e", tc.to, t0); err == nil {
				t.Fatalf("%s -> %s must be refused", tc.from, tc.to)
			}
		})
	}
}

func TestAConsumerCannotHoldTwoProvidersForOneKey(t *testing.T) {
	w := NewWorld()
	// Two different components, so both may be Active at once. What is
	// under test is the consumer's committed view, not the providers.
	active(t, w, "p1", "runtime-orgd")
	active(t, w, "p2", "egress-binding")
	active(t, w, "c", "assignment-controller")
	b := CommittedBinding{Key: KeyRuntimeObservedSHA, Consumer: "c", Provider: "p1"}
	if err := w.Bind(b, t0); err != nil {
		t.Fatal(err)
	}
	if err := w.Bind(b, t0); err != nil {
		t.Fatalf("re-committing the same binding must be idempotent, not an error: %v", err)
	}
	err := w.Bind(CommittedBinding{Key: KeyRuntimeObservedSHA, Consumer: "c", Provider: "p2"}, t0)
	if err == nil || !strings.Contains(err.Error(), "already committed") {
		t.Fatalf("a committed view is one provider per key, not a preference: %v", err)
	}
}
