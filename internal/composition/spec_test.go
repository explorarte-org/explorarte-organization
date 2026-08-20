package composition

import (
	"strings"
	"testing"
)

func TestTheBaselineCompositionIsWellFormed(t *testing.T) {
	g, err := Baseline()
	if err != nil {
		t.Fatalf("the baseline composition must validate: %v", err)
	}
	order := g.Order()
	at := func(id string) int {
		for i, got := range order {
			if got == id {
				return i
			}
		}
		t.Fatalf("%q is missing from the bring-up order %v", id, order)
		return -1
	}
	// Egress cannot bind a revision the registry has not published yet,
	// and orgd cannot serve a revision egress is not bound to. This is
	// the seam canonicalsync guards imperatively today.
	if at("canonical-registry") > at("egress-binding") {
		t.Errorf("egress must bind after the registry publishes: %v", order)
	}
	if at("egress-binding") > at("runtime-orgd") {
		t.Errorf("orgd must come up after egress is bound: %v", order)
	}
	if at("database-schema") != 0 {
		t.Errorf("everything depends on the database schema, so it comes up first: %v", order)
	}
}

func TestDispatchIsReportedAsIrreversible(t *testing.T) {
	g, err := Baseline()
	if err != nil {
		t.Fatal(err)
	}
	got := g.IrreversibleEffects()
	if len(got) != 1 || got[0] != "runtime-orgd/dispatch-to-provider" {
		t.Fatalf("a request that reached a provider is the one thing here that cannot be taken back, got %v", got)
	}
}

func TestTwoOwnersOfAnExclusiveKeyIsRejected(t *testing.T) {
	_, err := NewGraph(
		[]KeySpec{{Name: "schema.tip", Composition: Exclusive}},
		[]ComponentSpec{
			{ID: "a", Provides: []Key{"schema.tip"}},
			{ID: "b", Provides: []Key{"schema.tip"}},
		},
	)
	if err == nil {
		t.Fatal("an exclusive key with two owners must not validate")
	}
	if !strings.Contains(err.Error(), "exactly one owner") {
		t.Fatalf("the error must say why: %v", err)
	}
}

func TestACycleIsRejectedAndNamed(t *testing.T) {
	_, err := NewGraph(
		[]KeySpec{
			{Name: "k1", Composition: Exclusive},
			{Name: "k2", Composition: Exclusive},
			{Name: "k3", Composition: Exclusive},
		},
		[]ComponentSpec{
			{ID: "a", Requires: []Key{"k3"}, Provides: []Key{"k1"}},
			{ID: "b", Requires: []Key{"k1"}, Provides: []Key{"k2"}},
			{ID: "c", Requires: []Key{"k2"}, Provides: []Key{"k3"}},
		},
	)
	if err == nil {
		t.Fatal("a cycle must be rejected before anything is brought up, not discovered as a hang")
	}
	for _, want := range []string{"dependency cycle", `"k1"`, `"k2"`, `"k3"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the cycle must be readable; %q missing from %v", want, err)
		}
	}
}

func TestAnUndeclaredCompositionIsRejectedRatherThanAssumed(t *testing.T) {
	_, err := NewGraph(
		[]KeySpec{{Name: "k"}},
		[]ComponentSpec{{ID: "a", Provides: []Key{"k"}}},
	)
	if err == nil {
		t.Fatal("a key that does not say how it composes must not get a default")
	}
	if !strings.Contains(err.Error(), "only its owner can say") {
		t.Fatalf("the error must say why there is no default: %v", err)
	}
}

func TestAnUndeclaredReversibilityIsRejected(t *testing.T) {
	_, err := NewGraph(
		[]KeySpec{{Name: "k", Composition: Exclusive}},
		[]ComponentSpec{{ID: "a", Provides: []Key{"k"}, Effects: []EffectSpec{{Name: "send"}}}},
	)
	if err == nil {
		t.Fatal("an effect that does not say whether it can be undone must not validate")
	}
}

func TestARequirementNobodyProvidesIsRejected(t *testing.T) {
	_, err := NewGraph(
		[]KeySpec{
			{Name: "have", Composition: Exclusive},
			{Name: "want", Composition: Exclusive},
		},
		[]ComponentSpec{{ID: "a", Requires: []Key{"want"}, Provides: []Key{"have"}}},
	)
	if err == nil {
		t.Fatal("waiting forever for a fact nobody states is an invalid composition, not a runtime condition")
	}
	if !strings.Contains(err.Error(), "no component provides") {
		t.Fatalf("the error must name the unsatisfiable key: %v", err)
	}
}

func TestASelfEdgeIsRejected(t *testing.T) {
	_, err := NewGraph(
		[]KeySpec{{Name: "k", Composition: Exclusive}},
		[]ComponentSpec{{ID: "a", Requires: []Key{"k"}, Provides: []Key{"k"}}},
	)
	if err == nil {
		t.Fatal("a component that requires what it provides can never come up")
	}
}

func TestUnknownKeysAreCaughtAtTheEdge(t *testing.T) {
	for _, tc := range []struct {
		name string
		c    ComponentSpec
	}{
		{"requires", ComponentSpec{ID: "a", Requires: []Key{"typo"}}},
		{"provides", ComponentSpec{ID: "a", Provides: []Key{"typo"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewGraph([]KeySpec{{Name: "k", Composition: Exclusive}}, []ComponentSpec{tc.c})
			if err == nil || !strings.Contains(err.Error(), "undeclared key") {
				t.Fatalf("a mistyped key must be an unknown key, not a silent dependency: %v", err)
			}
		})
	}
}

func TestTheOrderIsDeterministic(t *testing.T) {
	first, err := Baseline()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		again, err := Baseline()
		if err != nil {
			t.Fatal(err)
		}
		a, b := first.Order(), again.Order()
		if len(a) != len(b) {
			t.Fatalf("order length changed: %v vs %v", a, b)
		}
		for j := range a {
			if a[j] != b[j] {
				t.Fatalf("bring-up order must not depend on map iteration: %v vs %v", a, b)
			}
		}
	}
}
