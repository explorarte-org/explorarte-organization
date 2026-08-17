package contextcompiler

import "testing"

func testProfile(id string) ContextProfile { return ContextProfile{ID: id, Version: "v1"} }

// TestSelectorRegistry_Precedence proves M1.3 section 10's frozen order:
// EXACT > TASK-CLASS > EXECUTION-PURPOSE > CANONICAL fallback.
func TestSelectorRegistry_Precedence(t *testing.T) {
	taskClassProfile := ProfileEntry{Profile: func() ContextProfile { p := testProfile("task-class-profile"); p.TaskClass = "some.class"; return p }()}
	purposeProfile := ProfileEntry{Profile: func() ContextProfile { p := testProfile("purpose-profile"); p.ExecutionPurpose = "department-worker"; return p }()}
	exactSelector := SemanticSelector{TaskClass: "some.class", ExecutionPurpose: "department-worker", ActorRoleID: "unit/role", ActorUnitID: "unit"}
	exactProfile := ProfileEntry{Profile: testProfile("exact-profile")}

	registry, err := BuildSelectorRegistry(
		[]ProfileEntry{taskClassProfile},
		[]ProfileEntry{purposeProfile},
		[]ExactRegistration{{Selector: exactSelector, Entry: exactProfile}},
	)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("exact beats task-class and execution-purpose", func(t *testing.T) {
		result := registry.Select(exactSelector)
		if !result.Matched || result.Kind != SelectionExact || result.Profile.ID != "exact-profile" {
			t.Fatalf("want exact-profile via exact, got %+v", result)
		}
	})
	t.Run("task-class beats execution-purpose", func(t *testing.T) {
		selector := SemanticSelector{TaskClass: "some.class", ExecutionPurpose: "department-worker", ActorRoleID: "other/role", ActorUnitID: "other"}
		result := registry.Select(selector)
		if !result.Matched || result.Kind != SelectionTaskClass || result.Profile.ID != "task-class-profile" {
			t.Fatalf("want task-class-profile via task_class, got %+v", result)
		}
	})
	t.Run("execution-purpose used when task-class does not match", func(t *testing.T) {
		selector := SemanticSelector{TaskClass: "unregistered.class", ExecutionPurpose: "department-worker"}
		result := registry.Select(selector)
		if !result.Matched || result.Kind != SelectionExecutionPurpose || result.Profile.ID != "purpose-profile" {
			t.Fatalf("want purpose-profile via execution_purpose, got %+v", result)
		}
	})
	t.Run("canonical fallback when nothing matches", func(t *testing.T) {
		selector := SemanticSelector{TaskClass: "unregistered.class", ExecutionPurpose: "unregistered-purpose"}
		result := registry.Select(selector)
		if result.Matched || result.Kind != SelectionCanonical {
			t.Fatalf("want unmatched canonical fallback, got %+v", result)
		}
	})
}

// TestSelectorRegistry_ApplicabilityGatesTaskClassAndPurposeTiers is M1.3
// section 11: TaskClass alone (or ExecutionPurpose alone) must not be
// sufficient when a profile restricts applicability by role/unit.
func TestSelectorRegistry_ApplicabilityGatesTaskClassAndPurposeTiers(t *testing.T) {
	restricted := ProfileEntry{
		Profile:                func() ContextProfile { p := testProfile("restricted"); p.TaskClass = "restricted.class"; return p }(),
		ApplicableActorRoleIDs: []string{"investigacion/research_worker_hourly"},
		ApplicableActorUnitIDs: []string{"investigacion"},
	}
	registry, err := BuildSelectorRegistry([]ProfileEntry{restricted}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	applicable := registry.Select(SemanticSelector{TaskClass: "restricted.class", ActorRoleID: "investigacion/research_worker_hourly", ActorUnitID: "investigacion"})
	if !applicable.Matched || applicable.Kind != SelectionTaskClass {
		t.Fatalf("expected applicable role/unit to match, got %+v", applicable)
	}
	wrongRole := registry.Select(SemanticSelector{TaskClass: "restricted.class", ActorRoleID: "marketing/worker", ActorUnitID: "investigacion"})
	if wrongRole.Matched {
		t.Fatalf("a TaskClass match must not bypass role applicability, got %+v", wrongRole)
	}
	wrongUnit := registry.Select(SemanticSelector{TaskClass: "restricted.class", ActorRoleID: "investigacion/research_worker_hourly", ActorUnitID: "marketing"})
	if wrongUnit.Matched {
		t.Fatalf("a TaskClass match must not bypass unit applicability, got %+v", wrongUnit)
	}
}

// TestSelectorRegistry_DuplicateRegistrationRejected is M1.3 section 10:
// duplicate/ambiguous selector registrations must fail construction, not
// silently depend on map iteration order.
func TestSelectorRegistry_DuplicateRegistrationRejected(t *testing.T) {
	dup := func() ContextProfile { p := testProfile("dup"); p.TaskClass = "same.class"; return p }()
	if _, err := BuildSelectorRegistry([]ProfileEntry{{Profile: dup}, {Profile: dup}}, nil, nil); err == nil {
		t.Fatal("expected duplicate task-class registration to be rejected")
	}
	dupPurpose := func() ContextProfile { p := testProfile("dup-purpose"); p.ExecutionPurpose = "department-worker"; return p }()
	if _, err := BuildSelectorRegistry(nil, []ProfileEntry{{Profile: dupPurpose}, {Profile: dupPurpose}}, nil); err == nil {
		t.Fatal("expected duplicate execution-purpose registration to be rejected")
	}
	selector := SemanticSelector{TaskClass: "x", ExecutionPurpose: "y", ActorRoleID: "z", ActorUnitID: "w"}
	if _, err := BuildSelectorRegistry(nil, nil, []ExactRegistration{
		{Selector: selector, Entry: ProfileEntry{Profile: testProfile("a")}},
		{Selector: selector, Entry: ProfileEntry{Profile: testProfile("b")}},
	}); err == nil {
		t.Fatal("expected duplicate exact registration to be rejected")
	}
}

// TestSelectorRegistry_PartialExactSelectorRejected is the independent
// review's required proof: EXACT means the complete four-axis tuple, not
// merely "not entirely empty" -- a registration missing any single axis
// must fail construction.
func TestSelectorRegistry_PartialExactSelectorRejected(t *testing.T) {
	complete := SemanticSelector{TaskClass: "x", ExecutionPurpose: "y", ActorRoleID: "z", ActorUnitID: "w"}
	for name, mutate := range map[string]func(*SemanticSelector){
		"missing_task_class":        func(s *SemanticSelector) { s.TaskClass = "" },
		"missing_execution_purpose": func(s *SemanticSelector) { s.ExecutionPurpose = "" },
		"missing_actor_role_id":     func(s *SemanticSelector) { s.ActorRoleID = "" },
		"missing_actor_unit_id":     func(s *SemanticSelector) { s.ActorUnitID = "" },
	} {
		t.Run(name, func(t *testing.T) {
			partial := complete
			mutate(&partial)
			if _, err := BuildSelectorRegistry(nil, nil, []ExactRegistration{{Selector: partial, Entry: ProfileEntry{Profile: testProfile("p")}}}); err == nil {
				t.Fatalf("expected a partial exact selector (%s) to be rejected", name)
			}
		})
	}
	// The fully complete tuple must still be accepted.
	if _, err := BuildSelectorRegistry(nil, nil, []ExactRegistration{{Selector: complete, Entry: ProfileEntry{Profile: testProfile("p")}}}); err != nil {
		t.Fatalf("a complete four-axis selector must be accepted: %v", err)
	}
}

// TestSelectorRegistry_DeterministicIndependentOfRegistrationOrder proves
// Select's outcome never depends on the order profiles were registered in
// (a stand-in proof that it does not depend on Go map iteration order
// either, since BuildSelectorRegistry itself builds maps from these
// slices).
func TestSelectorRegistry_DeterministicIndependentOfRegistrationOrder(t *testing.T) {
	a := func() ContextProfile { p := testProfile("a"); p.TaskClass = "class.a"; return p }()
	b := func() ContextProfile { p := testProfile("b"); p.TaskClass = "class.b"; return p }()
	c := func() ContextProfile { p := testProfile("c"); p.TaskClass = "class.c"; return p }()

	forward, err := BuildSelectorRegistry([]ProfileEntry{{Profile: a}, {Profile: b}, {Profile: c}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	backward, err := BuildSelectorRegistry([]ProfileEntry{{Profile: c}, {Profile: b}, {Profile: a}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, class := range []string{"class.a", "class.b", "class.c"} {
		selector := SemanticSelector{TaskClass: class}
		f, bk := forward.Select(selector), backward.Select(selector)
		if f.Profile.ID != bk.Profile.ID || f.Kind != bk.Kind {
			t.Fatalf("selection for %s diverged by registration order: %+v vs %+v", class, f, bk)
		}
	}
}

// TestDefaultSelectorRegistry_ActorRoleIDAloneCannotActivateResearch is
// M1.3 section 1/18.D: the ActorRoleID-only proxy (formerly TaskClassOf)
// is removed from productive selection -- a role being
// investigacion/research_worker_hourly is no longer, by itself, enough to
// select research.corpus_curate.
func TestDefaultSelectorRegistry_ActorRoleIDAloneCannotActivateResearch(t *testing.T) {
	selector := SemanticSelector{ActorRoleID: researchWorkerHourlyRoleID, ActorUnitID: researchUnitID}
	result := defaultSelectorRegistry.Select(selector)
	if result.Matched {
		t.Fatalf("ActorRoleID (and ActorUnitID) alone, with no TaskClass, must not activate a profile: %+v", result)
	}
	withTaskClass := defaultSelectorRegistry.Select(SemanticSelector{TaskClass: ResearchCorpusCurateV1TaskClass, ActorRoleID: researchWorkerHourlyRoleID, ActorUnitID: researchUnitID})
	if !withTaskClass.Matched {
		t.Fatalf("the correct full selector must still match: %+v", withTaskClass)
	}
}
