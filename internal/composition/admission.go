package composition

import (
	"fmt"
	"sort"
	"strings"
)

// Observation is what the world currently reports, key by key. A key absent
// from an Observation has not been observed, which is never the same thing as
// observing that it is empty: an unobserved fact denies admission rather than
// passing it.
type Observation map[Key]string

// Predicate is a decidable statement about observed values. The set of
// predicates is deliberately closed rather than an arbitrary func, because a
// refusal has to be renderable: a component that may not activate must be
// able to say which statement failed and on which values, and a closure
// cannot.
type Predicate interface {
	// Keys names every key the predicate reads, so a graph can check at
	// construction that it only reads facts the component depends on.
	Keys() []Key
	// Evaluate returns whether the statement holds, and when it does not,
	// a reason carrying the actual values.
	Evaluate(Observation) (bool, string)
	String() string
}

// Equal states that two keys carry the same value.
//
// This is the shape of the invariant canonicalsync protects: it does not ask
// whether a revision exists and some binding exists, it refuses to call the
// organization executable while the current revision is not the one egress
// policy is bound to. Topology cannot say that. "runtime-orgd requires
// canonical.egress.binding" only says the fact exists, never that it agrees
// with canonical.registry.revision.
func Equal(a, b Key) Predicate { return equalPredicate{a: a, b: b} }

type equalPredicate struct{ a, b Key }

func (p equalPredicate) Keys() []Key { return []Key{p.a, p.b} }

func (p equalPredicate) String() string { return fmt.Sprintf("%s == %s", p.a, p.b) }

func (p equalPredicate) Evaluate(obs Observation) (bool, string) {
	av, aok := obs[p.a]
	bv, bok := obs[p.b]
	if !aok || !bok {
		return false, unobserved(obs, p.a, p.b)
	}
	if av != bv {
		return false, fmt.Sprintf("%s=%s but %s=%s", p.a, av, p.b, bv)
	}
	return true, ""
}

// MemberOf states that one key's value appears in another key's set of
// accepted values, written as a comma-separated list.
//
// The relation this exists for is schema compatibility, and writing it out is
// what makes rolling replacement expressible: an old binary that accepts
// [55] and a new one that accepts [55,56] can both be admitted while the
// database is at 55, and the moment the database moves to 56 the old one
// loses admission and leaves. A single key called "schema.tip" hides that
// relation and can only express agreement or disagreement.
func MemberOf(value, set Key) Predicate { return memberPredicate{value: value, set: set} }

type memberPredicate struct{ value, set Key }

func (p memberPredicate) Keys() []Key { return []Key{p.value, p.set} }

func (p memberPredicate) String() string { return fmt.Sprintf("%s in %s", p.value, p.set) }

func (p memberPredicate) Evaluate(obs Observation) (bool, string) {
	v, vok := obs[p.value]
	s, sok := obs[p.set]
	if !vok || !sok {
		return false, unobserved(obs, p.value, p.set)
	}
	for _, candidate := range strings.Split(s, ",") {
		if strings.TrimSpace(candidate) == v {
			return true, ""
		}
	}
	return false, fmt.Sprintf("%s=%s is not in %s=[%s]", p.value, v, p.set, s)
}

func unobserved(obs Observation, keys ...Key) string {
	var missing []string
	for _, k := range keys {
		if _, ok := obs[k]; !ok {
			missing = append(missing, string(k))
		}
	}
	sort.Strings(missing)
	return fmt.Sprintf("not observed: %s", strings.Join(missing, ", "))
}

// ConvergenceSpec pairs a key stating what the world should be with the key
// reporting what it currently is.
//
// They are separate keys rather than two readings of one key because they
// have different owners, and one fact has one owner. A promotion states the
// desired build; the running fleet reports the observed one. Collapsing them
// into a single key would make "what we want" indistinguishable from "what
// there is", which is precisely the distinction a reconciler exists to act
// on.
type ConvergenceSpec struct {
	Desired  Key
	Observed Key
}

// Admit reports whether a component may hold Active under an observation.
//
// Admission is separate from topology on purpose. A graph can be perfectly
// well formed -- acyclic, singly owned, every requirement satisfied -- while
// the values in it disagree, and that disagreement is not a structural error
// to reject at construction. It is a runtime condition that has to be
// re-evaluated as the world moves, and that a component can lose after having
// held it.
func (g *Graph) Admit(componentID string, obs Observation) error {
	c, known := g.components[componentID]
	if !known {
		return fmt.Errorf("composition: no component %q in this composition", componentID)
	}
	for _, p := range c.Admits {
		if ok, reason := p.Evaluate(obs); !ok {
			return fmt.Errorf("composition: %s may not be active: %s (%s)", componentID, p, reason)
		}
	}
	return nil
}

// Admissible returns the components that may hold Active under an
// observation, in bring-up order, and the reasons the rest may not.
func (g *Graph) Admissible(obs Observation) (admitted []string, refused map[string]string) {
	refused = make(map[string]string)
	for _, id := range g.order {
		if err := g.Admit(id, obs); err != nil {
			refused[id] = err.Error()
			continue
		}
		admitted = append(admitted, id)
	}
	return admitted, refused
}

// Converged reports whether every declared desired/observed pair agrees, and
// names the ones that do not.
//
// Divergence is not a failure. It is the ordinary signal that a transition is
// owed, and the reconciler's whole job is to act on it. What would be a
// failure is not being able to tell: a composition that cannot distinguish
// the build it wants from the build it is running has nothing to converge
// toward.
func (g *Graph) Converged(obs Observation) (bool, []string) {
	var diverged []string
	for _, pair := range g.convergence {
		desired, dok := obs[pair.Desired]
		observed, ook := obs[pair.Observed]
		switch {
		case !dok || !ook:
			diverged = append(diverged, unobserved(obs, pair.Desired, pair.Observed))
		case desired != observed:
			diverged = append(diverged, fmt.Sprintf("%s=%s but %s=%s", pair.Desired, desired, pair.Observed, observed))
		}
	}
	sort.Strings(diverged)
	return len(diverged) == 0, diverged
}
