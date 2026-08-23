package composition

import (
	"strings"
	"testing"
	"time"
)

// bringUp starts and activates every component of the baseline, in order, so
// a test can begin from a converged world.
func bringUp(t *testing.T, g *Graph, w *World, when time.Time) {
	t.Helper()
	for _, id := range g.Order() {
		ep := EpisodeID(id + "-1")
		if err := w.Start(ep, id, when.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		if err := w.Transition(ep, Active, when); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAConvergedWorldHasNothingToDo(t *testing.T) {
	g, err := Baseline()
	if err != nil {
		t.Fatal(err)
	}
	w := NewWorld()
	bringUp(t, g, w, t0)
	if step, ok := Next(g, w, healthy(), t0); ok {
		t.Fatalf("a world that agrees with itself owes no transition, got %v", step)
	}
}

// The reconciler resolves what it does not know before it acts on it, and it
// does exactly one thing per turn.
func TestUnknownsAreResolvedBeforeAnythingElse(t *testing.T) {
	g, err := Baseline()
	if err != nil {
		t.Fatal(err)
	}
	w := NewWorld()
	bringUp(t, g, w, t0)

	// Two things are wrong at once: egress fell behind, and one episode
	// stopped answering. The lapsed episode has to be settled first.
	obs := healthy()
	obs[KeyEgressBoundRevision] = "18"
	late := t0.Add(2 * time.Minute)
	for _, id := range g.Order() {
		if id == "runtime-orgd" {
			continue
		}
		if err := w.Heartbeat(EpisodeID(id+"-1"), late.Add(time.Minute), t0.Add(30*time.Second)); err != nil {
			t.Fatal(err)
		}
	}

	step, ok := Next(g, w, obs, late)
	if !ok || step.Kind != StepAdjudicate || step.Episode != "runtime-orgd-1" {
		t.Fatalf("the unanswered question comes first, got %v", step)
	}
	if !strings.Contains(step.Reason, "lease lapsed") {
		t.Fatalf("the step must say why it was chosen: %q", step.Reason)
	}
	if err := Apply(w, step, late); err != nil {
		t.Fatal(err)
	}

	// Only now does the admission failure become the next move.
	step, ok = Next(g, w, obs, late)
	if !ok || step.Kind != StepLeave || step.Episode != "assignment-controller-1" {
		t.Fatalf("the component that lost admission must withdraw next, got %v", step)
	}
	if !strings.Contains(step.Reason, "canonical.egress.binding=18") {
		t.Fatalf("the reason must carry the values that caused it: %q", step.Reason)
	}
}

// Leave, then unload, then reload. Never the new episode beside the old one.
func TestReplacementLeavesBeforeItActivates(t *testing.T) {
	g, err := Baseline()
	if err != nil {
		t.Fatal(err)
	}
	w := NewWorld()
	bringUp(t, g, w, t0)

	// A new build is promoted, so a second episode of orgd is prepared.
	if err := w.Start("runtime-orgd-2", "runtime-orgd", t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	err = w.Transition("runtime-orgd-2", Active, t0)
	if err == nil || !strings.Contains(err.Error(), "runtime-orgd-1") {
		t.Fatalf("two activations of one component are two answers to one fact: %v", err)
	}

	// The old episode leaves. It is no longer selectable, but it is not
	// gone: it keeps serving whoever already committed to it.
	if err := w.Transition("runtime-orgd-1", Unloading, t0); err != nil {
		t.Fatal(err)
	}
	if err := w.Transition("runtime-orgd-2", Active, t0); err != nil {
		t.Fatalf("with the incumbent out of selection the replacement may come up: %v", err)
	}
}

func TestTeardownWaitsForTheLastLiveHolderAndThenProceeds(t *testing.T) {
	g, err := Baseline()
	if err != nil {
		t.Fatal(err)
	}
	w := NewWorld()
	bringUp(t, g, w, t0)
	if err := w.Bind(CommittedBinding{
		Key: KeyRuntimeObservedSHA, Consumer: "composition-controller-1", Provider: "runtime-orgd-1",
	}, t0); err != nil {
		t.Fatal(err)
	}
	if err := w.Transition("runtime-orgd-1", Unloading, t0); err != nil {
		t.Fatal(err)
	}

	// Everyone keeps their lease alive except the holder.
	renew := func(at time.Time, skip EpisodeID) {
		for _, id := range g.Order() {
			ep := EpisodeID(id + "-1")
			if ep == skip {
				continue
			}
			_ = w.Heartbeat(ep, at.Add(time.Minute), t0.Add(30*time.Second))
		}
	}

	// While the holder lives, the reconciler will not finish the teardown.
	renew(t0.Add(40*time.Second), "")
	step, ok := Next(g, w, healthy(), t0.Add(40*time.Second))
	if ok && step.Kind == StepUnload && step.Episode == "runtime-orgd-1" {
		t.Fatal("a teardown must not finish while a live consumer still holds it")
	}

	// The holder stops answering. Its lapse is adjudicated first, and only
	// then does the teardown it was blocking become available.
	late := t0.Add(2 * time.Minute)
	renew(late, "composition-controller-1")
	step, ok = Next(g, w, healthy(), late)
	if !ok || step.Kind != StepAdjudicate || step.Episode != "composition-controller-1" {
		t.Fatalf("the dead holder is adjudicated before its grip is released, got %v", step)
	}
	if err := Apply(w, step, late); err != nil {
		t.Fatal(err)
	}
	step, ok = Next(g, w, healthy(), late)
	if !ok || step.Kind != StepUnload || step.Episode != "runtime-orgd-1" {
		t.Fatalf("with nothing live holding it the teardown proceeds, got %v", step)
	}
	if err := Apply(w, step, late); err != nil {
		t.Fatal(err)
	}
	if e, _ := w.Episode("runtime-orgd-1"); e.State != Inactive {
		t.Fatalf("the episode should be inactive, is %s", e.State)
	}
	// The binding survives the whole sequence. It is the record of what
	// was committed, not a claim about what is still needed.
	if got := w.Bindings(); len(got) != 1 {
		t.Fatalf("the binding must remain as evidence: %v", got)
	}
}

func TestAComponentWaitsForItsProvidersToBeActive(t *testing.T) {
	g, err := Baseline()
	if err != nil {
		t.Fatal(err)
	}
	w := NewWorld()
	// Only orgd is prepared. Everything it requires is still down.
	if err := w.Start("runtime-orgd-1", "runtime-orgd", t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	step, ok := Next(g, w, healthy(), t0)
	if ok && step.Episode == "runtime-orgd-1" {
		t.Fatalf("orgd must not come up before the facts it requires are being provided: %v", step)
	}
}

func TestTheChosenStepIsDeterministic(t *testing.T) {
	obs := healthy()
	obs[KeyEgressBoundRevision] = "18"
	var first Step
	for i := 0; i < 25; i++ {
		g, err := Baseline()
		if err != nil {
			t.Fatal(err)
		}
		w := NewWorld()
		bringUp(t, g, w, t0)
		step, ok := Next(g, w, obs, t0)
		if !ok {
			t.Fatal("expected a step")
		}
		if i == 0 {
			first = step
			continue
		}
		if step != first {
			t.Fatalf("step selection must not depend on map iteration: %v vs %v", step, first)
		}
	}
}
