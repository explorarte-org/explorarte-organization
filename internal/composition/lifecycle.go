package composition

import (
	"fmt"
	"sort"
	"time"
)

// Lifecycle is the durable state of one activation of a component.
type Lifecycle string

const (
	// Inactive: not running, holds nothing, owes nothing.
	Inactive Lifecycle = "inactive"

	// Reloading: coming up. It may already hold bindings on its
	// providers, so it counts as a live holder, but nothing may bind to
	// it yet.
	Reloading Lifecycle = "reloading"

	// Active: running and selectable. The only state a provider may be in
	// when a new consumer binds to it.
	Active Lifecycle = "active"

	// Unloading: leaving. This is the state that makes replacement
	// ordinary instead of a special case. An unloading provider is no
	// longer selectable for NEW consumers, while every consumer that
	// already committed to it keeps its binding until it settles. Leaving
	// and being gone are two different things, and systemctl restart
	// collapses them.
	Unloading Lifecycle = "unloading"

	// Failed: this activation is over and did not succeed. The failure
	// belongs to this episode. Its siblings are unaffected, and what the
	// composition does about it is decided above.
	Failed Lifecycle = "failed"
)

// EpisodeID identifies one activation, not one component.
//
// The distinction is the whole reason bindings can be durable. orgd going
// Active, restarting, and going Active again is two episodes with one
// ComponentID, and a binding left behind by the first must never look like it
// belongs to the second. Component-scoped bindings would silently inherit.
type EpisodeID string

// Episode is one activation of a component, and the unit that holds bindings.
type Episode struct {
	ID          EpisodeID
	ComponentID string
	State       Lifecycle

	// LeaseExpiresAt is how this episode proves it is still there. A live
	// process renews it; a dead one stops, and the lease lapses on its own
	// without anybody having to notice the death.
	LeaseExpiresAt time.Time

	// Adjudicated records that the reconciler has decided what happened to
	// an episode whose lease lapsed. A lapsed lease is a question, not an
	// answer, and this is where the answer is written down.
	Adjudicated bool
}

// live reports whether an episode can still be holding anything.
//
// Note that Unloading counts as live: an episode on its way out still holds
// its bindings, and that is precisely the interval the ordering exists to
// protect.
func (e Episode) live(now time.Time) bool {
	switch e.State {
	case Active, Reloading, Unloading:
		return now.Before(e.LeaseExpiresAt)
	}
	return false
}

// CommittedBinding records that one episode is relying on another for a
// specific key. It names episodes, never components, and it names the key,
// never merely the type.
//
// "worker requires model-runtime" cannot answer who is preventing a
// withdrawal. "consumer episode 91 committed to provider episode 54 for
// runtime.binary.observed_sha" can, and that answer is what turns a stuck
// teardown from a mystery into a name.
type CommittedBinding struct {
	Key      Key
	Consumer EpisodeID
	Provider EpisodeID
}

// World is the durable state a reconciler observes: which episodes exist and
// what they have committed to. It is a value, not a service; every question
// below is answered from it without reaching for anything else.
type World struct {
	episodes map[EpisodeID]Episode
	bindings []CommittedBinding
}

func NewWorld() *World {
	return &World{episodes: make(map[EpisodeID]Episode)}
}

// Start records a new episode of a component, in Reloading.
func (w *World) Start(id EpisodeID, componentID string, leaseExpiresAt time.Time) error {
	if id == "" || componentID == "" {
		return fmt.Errorf("composition: an episode needs an ID and a component")
	}
	if _, exists := w.episodes[id]; exists {
		return fmt.Errorf("composition: episode %q already exists", id)
	}
	w.episodes[id] = Episode{ID: id, ComponentID: componentID, State: Reloading, LeaseExpiresAt: leaseExpiresAt}
	return nil
}

// Heartbeat extends an episode's lease. Only a live episode may renew: an
// episode whose lease already lapsed has to be adjudicated first, because
// letting it renew would erase the very gap that says something went wrong.
func (w *World) Heartbeat(id EpisodeID, until time.Time, now time.Time) error {
	e, ok := w.episodes[id]
	if !ok {
		return fmt.Errorf("composition: no episode %q", id)
	}
	if !e.live(now) {
		return fmt.Errorf("composition: episode %q cannot renew a lapsed lease from state %s; it must be adjudicated", id, e.State)
	}
	e.LeaseExpiresAt = until
	w.episodes[id] = e
	return nil
}

var legalTransitions = map[Lifecycle]map[Lifecycle]bool{
	Inactive:  {Reloading: true},
	Reloading: {Active: true, Failed: true},
	Active:    {Unloading: true, Failed: true},
	Unloading: {Inactive: true, Failed: true},
	Failed:    {Inactive: true},
}

// Transition moves an episode, refusing anything the machine does not allow
// and refusing to finish a teardown somebody is still relying on.
func (w *World) Transition(id EpisodeID, to Lifecycle, now time.Time) error {
	e, ok := w.episodes[id]
	if !ok {
		return fmt.Errorf("composition: no episode %q", id)
	}
	if !legalTransitions[e.State][to] {
		return fmt.Errorf("composition: episode %q may not go %s -> %s", id, e.State, to)
	}
	// An episode whose lease has lapsed is not available for an ordinary
	// transition. Nobody knows yet what became of it, and moving it as if
	// they did is how a decision gets skipped rather than made.
	if lapsable(e.State) && !now.Before(e.LeaseExpiresAt) {
		return fmt.Errorf("composition: episode %q has a lapsed lease and must be adjudicated, not transitioned to %s", id, to)
	}
	if e.State == Unloading && to == Inactive {
		if holders := w.Relied(id, now); len(holders) > 0 {
			return fmt.Errorf("composition: episode %q may not finish unloading, still relied on by %v", id, holders)
		}
	}
	e.State = to
	// An explicit transition to Failed is itself a decision about what
	// happened, so it needs no separate adjudication. The flag exists for
	// the episodes that stopped answering without anybody deciding.
	if to == Failed {
		e.Adjudicated = true
	}
	w.episodes[id] = e
	return nil
}

// lapsable reports whether a state is one in which a lease is being held and
// can therefore run out.
func lapsable(state Lifecycle) bool {
	switch state {
	case Active, Reloading, Unloading:
		return true
	}
	return false
}

// Bind commits a consumer episode to a provider episode for a key.
//
// A provider must be Active to accept a new binding. That single rule is what
// makes Unloading mean something: the moment a provider starts leaving it
// stops being selectable, without anything having to be torn down yet.
func (w *World) Bind(b CommittedBinding, now time.Time) error {
	consumer, ok := w.episodes[b.Consumer]
	if !ok {
		return fmt.Errorf("composition: no consumer episode %q", b.Consumer)
	}
	provider, ok := w.episodes[b.Provider]
	if !ok {
		return fmt.Errorf("composition: no provider episode %q", b.Provider)
	}
	if b.Key == "" {
		return fmt.Errorf("composition: a binding must name a key")
	}
	if b.Consumer == b.Provider {
		return fmt.Errorf("composition: episode %q cannot bind to itself", b.Consumer)
	}
	if provider.State != Active {
		return fmt.Errorf("composition: provider episode %q is %s and is not selectable for new bindings", b.Provider, provider.State)
	}
	if !consumer.live(now) {
		return fmt.Errorf("composition: consumer episode %q is not live and may not commit to anything", b.Consumer)
	}
	for _, existing := range w.bindings {
		if existing == b {
			return nil
		}
		if existing.Consumer == b.Consumer && existing.Key == b.Key {
			return fmt.Errorf("composition: consumer %q is already committed to %q for %q", b.Consumer, existing.Provider, b.Key)
		}
	}
	w.bindings = append(w.bindings, b)
	return nil
}

// Relied names the live episodes still committed to this provider, sorted.
//
// Liveness is the correction that keeps a durable binding from becoming a
// permanent one. Cordis can hold a disposer in memory, so a process dying
// clears what it held. Ours survive the process. Without liveness, a consumer
// that crashes while Active leaves a binding that answers "yes, somebody
// needs it" forever, and the provider it holds can never finish leaving --
// the same shape as a task held open by a coordination that never arrives,
// one layer down.
func (w *World) Relied(provider EpisodeID, now time.Time) []EpisodeID {
	var holders []EpisodeID
	seen := make(map[EpisodeID]bool)
	for _, b := range w.bindings {
		if b.Provider != provider || seen[b.Consumer] {
			continue
		}
		if w.episodes[b.Consumer].live(now) {
			holders = append(holders, b.Consumer)
			seen[b.Consumer] = true
		}
	}
	sort.Slice(holders, func(i, j int) bool { return holders[i] < holders[j] })
	return holders
}

// Lapsed names the episodes whose lease has run out and that nobody has
// adjudicated yet, sorted. These are the questions the reconciler owes an
// answer to before anything downstream of them can move.
func (w *World) Lapsed(now time.Time) []EpisodeID {
	var out []EpisodeID
	for id, e := range w.episodes {
		switch e.State {
		case Active, Reloading, Unloading:
			if !now.Before(e.LeaseExpiresAt) && !e.Adjudicated {
				out = append(out, id)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Adjudicate records what happened to an episode whose lease lapsed.
//
// It is deliberately not "delete the binding on expiry". An expired lease
// says a holder stopped answering; it does not say what became of the work it
// held. Deciding that durably comes first, exactly as it does for an
// invocation whose transport went ambiguous -- and for the same reason: the
// cheap move is to tidy up, and the cheap move throws away the only record of
// what was in flight.
//
// The bindings are kept. A holder that is no longer live already stops
// holding Relied, so the withdrawal it was blocking is free the moment this
// is written down, without any history being erased to achieve it.
func (w *World) Adjudicate(id EpisodeID, outcome Lifecycle, now time.Time) error {
	e, ok := w.episodes[id]
	if !ok {
		return fmt.Errorf("composition: no episode %q", id)
	}
	if now.Before(e.LeaseExpiresAt) {
		return fmt.Errorf("composition: episode %q still holds a valid lease and must not be adjudicated", id)
	}
	if outcome != Failed && outcome != Inactive {
		return fmt.Errorf("composition: a lapsed episode is adjudicated %s or %s, not %s", Failed, Inactive, outcome)
	}
	e.State = outcome
	e.Adjudicated = true
	w.episodes[id] = e
	return nil
}

// Episode returns one episode by ID.
func (w *World) Episode(id EpisodeID) (Episode, bool) {
	e, ok := w.episodes[id]
	return e, ok
}

// Bindings returns every committed binding, including those held by episodes
// that are no longer live. They are evidence of what was committed, not a
// claim about what is still needed; ask Relied for that.
func (w *World) Bindings() []CommittedBinding {
	out := append([]CommittedBinding{}, w.bindings...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Provider != out[j].Provider {
			return out[i].Provider < out[j].Provider
		}
		if out[i].Consumer != out[j].Consumer {
			return out[i].Consumer < out[j].Consumer
		}
		return out[i].Key < out[j].Key
	})
	return out
}
