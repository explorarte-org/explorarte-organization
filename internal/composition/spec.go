package composition

import (
	"fmt"
	"sort"
	"strings"
)

// Key names one fact about the operating world that some component owns and
// others may depend on. A key is not a variable: it is the identity of a
// fact, and two components naming the same key are talking about the same
// fact whether they meant to or not.
type Key string

// Composition says how a key tolerates more than one provider. There is no
// default and there must not be one: a default is a guess about somebody
// else's abstraction, and the whole point of declaring composition is that
// only the owner of the abstraction knows the answer. An undeclared
// composition is rejected rather than assumed.
type Composition string

const (
	// Exclusive: one provider, ever. A second provider is not a merge
	// conflict to resolve at runtime, it is an invalid composition.
	Exclusive Composition = "exclusive"

	// Ordered: several providers may contribute, and precedence carries
	// meaning. Later contributions may have acquired their meaning from
	// the ones before them, so withdrawal out of order is not permitted.
	// This is what an assembled prompt context is, and it is why a
	// segment cannot be removed from the middle of one.
	Ordered Composition = "ordered"

	// Commutative: several providers may contribute and order does not
	// change the result, so a contribution may be withdrawn out of order.
	// Nothing in this package acts on that permission yet -- withdrawal
	// does not exist here at all -- so declaring it today buys ordering
	// freedom that no code spends. It is defined so that the first key
	// that genuinely is commutative can say so instead of lying by
	// omission, not so that keys can be optimistically labelled.
	Commutative Composition = "commutative"
)

func (c Composition) valid() bool {
	switch c {
	case Exclusive, Ordered, Commutative:
		return true
	}
	return false
}

// Reversibility is a property of one concrete effect, not of a component and
// not of the system. This is the distinction that keeps the runtime honest:
// it may only promise to undo the things that can actually be undone.
type Reversibility string

const (
	// Reversible: a real inverse exists and applying it restores the
	// prior state. Creating a worktree. Releasing a wallet reservation
	// while the request is still on this side of the wire.
	Reversible Reversibility = "reversible"

	// Compensatable: what happened cannot be unhappened, but a further
	// action exists that settles the world into an acceptable state. A
	// promoted Git ref answers to a revert commit; the original promotion
	// stays in the history where it belongs.
	Compensatable Reversibility = "compensatable"

	// Irreversible: it happened, and the only honest response is to
	// record it and reconcile. A request that reached a provider. Money
	// spent. Tokens consumed. An email sent. An identifier issued.
	//
	// Naming these is the point of the whole type. A runtime that treats
	// an irreversible effect as merely un-undone will eventually try to
	// take back money it already spent.
	Irreversible Reversibility = "irreversible"
)

func (r Reversibility) valid() bool {
	switch r {
	case Reversible, Compensatable, Irreversible:
		return true
	}
	return false
}

// KeySpec declares a key and how it composes. Keys are declared up front,
// separately from the components that use them, so that a typo in a Requires
// list is an unknown key rather than a silently unsatisfiable dependency.
type KeySpec struct {
	Name        Key
	Composition Composition
}

// EffectSpec names something a component does to the world and says whether
// it can be taken back.
type EffectSpec struct {
	Name          string
	Reversibility Reversibility
}

// ComponentSpec is one participant in the topology. Requires and Provides are
// the only edges that exist: a component may not name another component
// directly, because depending on an identity rather than on a fact is how a
// replacement becomes a special case instead of an ordinary transition.
type ComponentSpec struct {
	ID       string
	Requires []Key
	Provides []Key
	Effects  []EffectSpec
}

// Graph is a validated composition. Holding one is the evidence that the
// checks in NewGraph passed; there is no way to build an invalid Graph and no
// exported field to invalidate one afterwards.
type Graph struct {
	keys       map[Key]KeySpec
	components map[string]ComponentSpec
	providers  map[Key][]string
	order      []string
}

// NewGraph validates a composition and, if it is well formed, returns it
// along with a bring-up order.
//
// Every check here is a gate, not a warning, and they run before anything can
// reach an active state. A cycle discovered while components are already
// running is not a diagnosis, it is a deadlock with extra steps.
func NewGraph(keys []KeySpec, components []ComponentSpec) (*Graph, error) {
	g := &Graph{
		keys:       make(map[Key]KeySpec, len(keys)),
		components: make(map[string]ComponentSpec, len(components)),
		providers:  make(map[Key][]string),
	}

	for _, k := range keys {
		if strings.TrimSpace(string(k.Name)) == "" {
			return nil, fmt.Errorf("composition: a key was declared without a name")
		}
		if _, dup := g.keys[k.Name]; dup {
			return nil, fmt.Errorf("composition: key %q was declared twice", k.Name)
		}
		if !k.Composition.valid() {
			return nil, fmt.Errorf("composition: key %q declares composition %q; a key must say how it composes, and only its owner can say", k.Name, k.Composition)
		}
		g.keys[k.Name] = k
	}

	for _, c := range components {
		if strings.TrimSpace(c.ID) == "" {
			return nil, fmt.Errorf("composition: a component was declared without an ID")
		}
		if _, dup := g.components[c.ID]; dup {
			return nil, fmt.Errorf("composition: component %q was declared twice", c.ID)
		}
		if err := validateEffects(c); err != nil {
			return nil, err
		}
		if err := g.validateEdges(c); err != nil {
			return nil, err
		}
		g.components[c.ID] = c
		for _, k := range c.Provides {
			g.providers[k] = append(g.providers[k], c.ID)
		}
	}

	for k := range g.providers {
		sort.Strings(g.providers[k])
	}

	if err := g.assertSingleOwnership(); err != nil {
		return nil, err
	}
	if err := g.assertSatisfiable(); err != nil {
		return nil, err
	}

	order, err := g.topological()
	if err != nil {
		return nil, err
	}
	g.order = order
	return g, nil
}

func validateEffects(c ComponentSpec) error {
	seen := make(map[string]struct{}, len(c.Effects))
	for _, e := range c.Effects {
		if strings.TrimSpace(e.Name) == "" {
			return fmt.Errorf("composition: component %q declares an effect without a name", c.ID)
		}
		if _, dup := seen[e.Name]; dup {
			return fmt.Errorf("composition: component %q declares effect %q twice", c.ID, e.Name)
		}
		if !e.Reversibility.valid() {
			return fmt.Errorf("composition: component %q declares effect %q with reversibility %q; an effect must say whether it can be undone, compensated, or only recorded", c.ID, e.Name, e.Reversibility)
		}
		seen[e.Name] = struct{}{}
	}
	return nil
}

func (g *Graph) validateEdges(c ComponentSpec) error {
	provides := make(map[Key]struct{}, len(c.Provides))
	for _, k := range c.Provides {
		if _, known := g.keys[k]; !known {
			return fmt.Errorf("composition: component %q provides undeclared key %q", c.ID, k)
		}
		if _, dup := provides[k]; dup {
			return fmt.Errorf("composition: component %q provides key %q twice", c.ID, k)
		}
		provides[k] = struct{}{}
	}
	requires := make(map[Key]struct{}, len(c.Requires))
	for _, k := range c.Requires {
		if _, known := g.keys[k]; !known {
			return fmt.Errorf("composition: component %q requires undeclared key %q", c.ID, k)
		}
		if _, dup := requires[k]; dup {
			return fmt.Errorf("composition: component %q requires key %q twice", c.ID, k)
		}
		if _, self := provides[k]; self {
			return fmt.Errorf("composition: component %q both requires and provides key %q, so it can never be brought up", c.ID, k)
		}
		requires[k] = struct{}{}
	}
	return nil
}

// assertSingleOwnership is the executable form of the rule that one fact has
// one owner. Two components providing the same exclusive key is not a
// conflict the runtime gets to arbitrate at bring-up time -- there is no
// principled way to pick, and picking silently is how a stale provider wins.
func (g *Graph) assertSingleOwnership() error {
	for _, k := range g.sortedKeys() {
		spec := g.keys[k]
		owners := g.providers[k]
		if spec.Composition == Exclusive && len(owners) > 1 {
			return fmt.Errorf("composition: key %q is exclusive but provided by %s; an exclusive key has exactly one owner", k, strings.Join(owners, ", "))
		}
	}
	return nil
}

// assertSatisfiable rejects a requirement that nothing in this composition
// provides. Left alone it would not surface as a validation failure but as a
// component that waits forever for a fact no one was ever going to state.
func (g *Graph) assertSatisfiable() error {
	for _, id := range g.sortedComponents() {
		for _, k := range g.components[id].Requires {
			if len(g.providers[k]) == 0 {
				return fmt.Errorf("composition: component %q requires key %q, which no component provides", id, k)
			}
		}
	}
	return nil
}

const (
	unvisited = 0
	onStack   = 1
	settled   = 2
)

// topological returns a bring-up order, or names the cycle that makes one
// impossible. The edge is provider-before-consumer: a component may only come
// up once every fact it requires is being provided.
func (g *Graph) topological() ([]string, error) {
	state := make(map[string]int, len(g.components))
	var stack []string
	var out []string

	var visit func(id string) error
	visit = func(id string) error {
		switch state[id] {
		case settled:
			return nil
		case onStack:
			return fmt.Errorf("composition: dependency cycle: %s", g.renderCycle(stack, id))
		}
		state[id] = onStack
		stack = append(stack, id)
		for _, k := range sortedKeySlice(g.components[id].Requires) {
			for _, provider := range g.providers[k] {
				if provider == id {
					continue
				}
				if err := visit(provider); err != nil {
					return err
				}
			}
		}
		stack = stack[:len(stack)-1]
		state[id] = settled
		out = append(out, id)
		return nil
	}

	for _, id := range g.sortedComponents() {
		if err := visit(id); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// renderCycle names the loop the way somebody debugging it needs to read it:
// each hop says which component is waiting, on which fact, and who owes it.
func (g *Graph) renderCycle(stack []string, closing string) string {
	start := 0
	for i, id := range stack {
		if id == closing {
			start = i
			break
		}
	}
	loop := append(append([]string{}, stack[start:]...), closing)
	hops := make([]string, 0, len(loop)-1)
	for i := 0; i+1 < len(loop); i++ {
		// The DFS descends from a component to the provider it is
		// waiting on, so the hop reads in that direction: loop[i]
		// requires something loop[i+1] owes it.
		waiter, owed := loop[i], loop[i+1]
		hops = append(hops, fmt.Sprintf("%s requires %q provided by %s", waiter, g.linkingKey(waiter, owed), owed))
	}
	return strings.Join(hops, "; ")
}

func (g *Graph) linkingKey(waiter, owed string) Key {
	for _, k := range sortedKeySlice(g.components[waiter].Requires) {
		for _, provider := range g.providers[k] {
			if provider == owed {
				return k
			}
		}
	}
	return ""
}

// Order returns the validated bring-up order. Teardown is its reverse only
// for a composition whose keys are all exclusive or ordered; nothing here
// performs either.
func (g *Graph) Order() []string {
	return append([]string{}, g.order...)
}

// Providers returns the components that provide a key, sorted.
func (g *Graph) Providers(k Key) []string {
	return append([]string{}, g.providers[k]...)
}

// KeySpecOf returns the declaration for a key.
func (g *Graph) KeySpecOf(k Key) (KeySpec, bool) {
	spec, ok := g.keys[k]
	return spec, ok
}

// IrreversibleEffects lists every effect in the composition that cannot be
// taken back, as component/effect pairs, sorted. This is what a mission has
// to be honest about before it is provisioned: not what it intends to do, but
// what it will not be able to undo if it turns out to be wrong.
func (g *Graph) IrreversibleEffects() []string {
	var out []string
	for _, id := range g.sortedComponents() {
		for _, e := range g.components[id].Effects {
			if e.Reversibility == Irreversible {
				out = append(out, id+"/"+e.Name)
			}
		}
	}
	sort.Strings(out)
	return out
}

func (g *Graph) sortedComponents() []string {
	out := make([]string, 0, len(g.components))
	for id := range g.components {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func (g *Graph) sortedKeys() []Key {
	out := make([]Key, 0, len(g.keys))
	for k := range g.keys {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func sortedKeySlice(in []Key) []Key {
	out := append([]Key{}, in...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
