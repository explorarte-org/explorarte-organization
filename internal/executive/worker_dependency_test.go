package executive

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func proposal(key, dependsOn string) WorkerTaskProposal {
	p := WorkerTaskProposal{
		ClientKey: key, AssignedRoleID: "ingenieria_ia/arquitecto_software",
		TaskClass: TaskClassGeneralWork, Title: key, Instructions: "do " + key,
		AcceptanceCriteria: []string{"done"}, Requirements: []RequirementProposal{}, Priority: 1,
	}
	if dependsOn != "" {
		p.Dependencies = strings.Split(dependsOn, ",")
	}
	return p
}

// materializeFixture drives materializeWorkerTasks against the in-memory task
// store, which honours the real semantics: a task created WITH dependencies is
// born pending, and AddDependency is refused on a running task.
func materializeFixture(t *testing.T, proposals []WorkerTaskProposal) *memoryTasks {
	t.Helper()
	tasksPort := newMemoryTasks()
	orchestrator := testOrchestratorForPorts(t, tasksPort, newFakeModels(), &fakeCompletion{verdict: CompletionPass})
	root := TaskRecord{ID: 1, OrganizationID: "explorarte", CorrelationID: "executive:corr", AssignedRoleID: CEORoleID}
	source := TaskRecord{ID: 2, AssignedRoleID: "ingenieria_ia/orquestador"}
	if err := orchestrator.materializeWorkerTasks(context.Background(), root, source, "ingenieria_ia", proposals, 0, 1); err != nil {
		t.Fatalf("materialize: %v", err)
	}
	return tasksPort
}

func taskByTitle(t *testing.T, store *memoryTasks, title string) TaskRecord {
	t.Helper()
	store.mu.Lock()
	defer store.mu.Unlock()
	for _, task := range store.tasks {
		if task.Title == title {
			return task
		}
	}
	t.Fatalf("task %q was not created", title)
	return TaskRecord{}
}

func depsOf(store *memoryTasks, id int64) []int64 {
	store.mu.Lock()
	defer store.mu.Unlock()
	return append([]int64(nil), store.deps[id]...)
}

// A: the dependency arrives in CreateTaskCommand, the dependent task is born
// pending, and AddDependency is never called.
func TestDependenciesArriveAtCreationNotAfterwards(t *testing.T) {
	store := materializeFixture(t, []WorkerTaskProposal{proposal("a", ""), proposal("b", "a")})

	a := taskByTitle(t, store, "a")
	b := taskByTitle(t, store, "b")
	if a.Status != "ready" {
		t.Fatalf("a status=%q, want ready", a.Status)
	}
	if b.Status != "pending" {
		t.Fatalf("b status=%q, want pending -- a dependent task must not be claimable", b.Status)
	}
	if got := depsOf(store, b.ID); len(got) != 1 || got[0] != a.ID {
		t.Fatalf("b dependencies=%v, want [%d]", got, a.ID)
	}
	if len(store.addDependencyCalls) != 0 {
		t.Fatalf("AddDependency was called %d times; dependencies must be supplied at creation", len(store.addDependencyCalls))
	}
	// The CreateTaskCommand itself carried them.
	for _, command := range store.createCalls {
		if command.Title == "b" && len(command.Dependencies) != 1 {
			t.Fatalf("b was created without its dependencies: %+v", command.Dependencies)
		}
	}
}

// B and F: a chain materializes in topological order even when the plan lists
// it backwards, and the result is deterministic.
func TestChainMaterializesTopologicallyFromReversedInput(t *testing.T) {
	store := materializeFixture(t, []WorkerTaskProposal{
		proposal("c", "b"), proposal("b", "a"), proposal("a", ""),
	})
	a := taskByTitle(t, store, "a")
	b := taskByTitle(t, store, "b")
	c := taskByTitle(t, store, "c")
	if !(a.ID < b.ID && b.ID < c.ID) {
		t.Fatalf("creation order was not topological: a=%d b=%d c=%d", a.ID, b.ID, c.ID)
	}
	if got := depsOf(store, c.ID); len(got) != 1 || got[0] != b.ID {
		t.Fatalf("c dependencies=%v", got)
	}
	if a.Status != "ready" || b.Status != "pending" || c.Status != "pending" {
		t.Fatalf("statuses a=%q b=%q c=%q", a.Status, b.Status, c.Status)
	}
}

// C: fan-out. Both dependents carry the same prerequisite and are pending.
func TestFanOutDependentsAreBornPendingOnTheSamePrerequisite(t *testing.T) {
	store := materializeFixture(t, []WorkerTaskProposal{proposal("a", ""), proposal("b", "a"), proposal("c", "a")})
	a := taskByTitle(t, store, "a")
	for _, key := range []string{"b", "c"} {
		task := taskByTitle(t, store, key)
		if task.Status != "pending" {
			t.Fatalf("%s status=%q", key, task.Status)
		}
		if got := depsOf(store, task.ID); len(got) != 1 || got[0] != a.ID {
			t.Fatalf("%s dependencies=%v", key, got)
		}
	}
}

// D: fan-in. The dependent carries both prerequisite IDs.
func TestFanInCarriesEveryPrerequisite(t *testing.T) {
	store := materializeFixture(t, []WorkerTaskProposal{proposal("a", ""), proposal("b", ""), proposal("c", "a,b")})
	a := taskByTitle(t, store, "a")
	b := taskByTitle(t, store, "b")
	c := taskByTitle(t, store, "c")
	got := depsOf(store, c.ID)
	if len(got) != 2 {
		t.Fatalf("c dependencies=%v, want both prerequisites", got)
	}
	seen := map[int64]bool{got[0]: true, got[1]: true}
	if !seen[a.ID] || !seen[b.ID] {
		t.Fatalf("c dependencies=%v, want %d and %d", got, a.ID, b.ID)
	}
}

// E: independent siblings stay immediately runnable. The fix must not make
// everything pending.
func TestIndependentSiblingsRemainReady(t *testing.T) {
	store := materializeFixture(t, []WorkerTaskProposal{proposal("a", ""), proposal("b", "")})
	for _, key := range []string{"a", "b"} {
		if task := taskByTitle(t, store, key); task.Status != "ready" {
			t.Fatalf("%s status=%q, want ready", key, task.Status)
		}
	}
}

// F: ordering is stable. Among nodes available at the same time the plan's own
// order is preserved, so the same plan always materializes identically.
func TestOrderingIsStableAmongIndependentNodes(t *testing.T) {
	proposals := []WorkerTaskProposal{proposal("first", ""), proposal("second", ""), proposal("third", "first")}
	firstRun := materializeFixture(t, proposals)
	secondRun := materializeFixture(t, proposals)
	order := func(store *memoryTasks) string {
		return taskByTitle(t, store, "first").Title + taskByTitle(t, store, "second").Title
	}
	if taskByTitle(t, firstRun, "first").ID >= taskByTitle(t, firstRun, "second").ID {
		t.Fatal("plan order was not preserved among independent nodes")
	}
	if order(firstRun) != order(secondRun) {
		t.Fatal("materialization is not deterministic across runs")
	}
}

// G: a second materialization of the same plan reuses the same tasks. No
// duplicates, no second set of edges.
func TestRematerializationReusesTasksAndAddsNoEdges(t *testing.T) {
	tasksPort := newMemoryTasks()
	orchestrator := testOrchestratorForPorts(t, tasksPort, newFakeModels(), &fakeCompletion{verdict: CompletionPass})
	root := TaskRecord{ID: 1, OrganizationID: "explorarte", CorrelationID: "executive:corr", AssignedRoleID: CEORoleID}
	source := TaskRecord{ID: 2, AssignedRoleID: "ingenieria_ia/orquestador"}
	proposals := []WorkerTaskProposal{proposal("a", ""), proposal("b", "a")}

	for i := 0; i < 3; i++ {
		if err := orchestrator.materializeWorkerTasks(context.Background(), root, source, "ingenieria_ia", proposals, 0, 1); err != nil {
			t.Fatalf("materialize %d: %v", i, err)
		}
	}
	tasksPort.mu.Lock()
	total := len(tasksPort.tasks)
	tasksPort.mu.Unlock()
	if total != 2 {
		t.Fatalf("tasks=%d, want 2 -- rematerialization duplicated work", total)
	}
	b := taskByTitle(t, tasksPort, "b")
	if got := depsOf(tasksPort, b.ID); len(got) != 1 {
		t.Fatalf("b dependencies=%v, want exactly one edge after three materializations", got)
	}
	if len(tasksPort.addDependencyCalls) != 0 {
		t.Fatalf("AddDependency was called %d times", len(tasksPort.addDependencyCalls))
	}
}

// H: a replan keeps its own idempotency suffix and the same dependency
// semantics, so a replanned graph is distinct from the original but internally
// correct.
func TestReplanKeepsItsOwnKeysAndDependencySemantics(t *testing.T) {
	tasksPort := newMemoryTasks()
	orchestrator := testOrchestratorForPorts(t, tasksPort, newFakeModels(), &fakeCompletion{verdict: CompletionPass})
	root := TaskRecord{ID: 1, OrganizationID: "explorarte", CorrelationID: "executive:corr", AssignedRoleID: CEORoleID}
	source := TaskRecord{ID: 2, AssignedRoleID: "ingenieria_ia/orquestador"}
	proposals := []WorkerTaskProposal{proposal("a", ""), proposal("b", "a")}

	if err := orchestrator.materializeWorkerTasks(context.Background(), root, source, "ingenieria_ia", proposals, 0, 1); err != nil {
		t.Fatal(err)
	}
	if err := orchestrator.materializeWorkerTasks(context.Background(), root, source, "ingenieria_ia", proposals, 1, 1); err != nil {
		t.Fatal(err)
	}
	tasksPort.mu.Lock()
	replanKeys := 0
	pending := 0
	for _, task := range tasksPort.tasks {
		if strings.Contains(task.IdempotencyKey, "-replan:1") {
			replanKeys++
			if task.Status == "pending" {
				pending++
			}
		}
	}
	tasksPort.mu.Unlock()
	if replanKeys != 2 {
		t.Fatalf("replan created %d tasks, want 2", replanKeys)
	}
	if pending != 1 {
		t.Fatalf("replan produced %d pending tasks, want 1 (its dependent)", pending)
	}
}

// I: no dependent worker is claimable before its prerequisite is satisfied.
// Asserted through status rather than through the slice of edges.
func TestDependentWorkerIsNeverClaimableBeforeItsPrerequisite(t *testing.T) {
	store := materializeFixture(t, []WorkerTaskProposal{proposal("design", ""), proposal("review", "design")})
	review := taskByTitle(t, store, "review")
	if review.Status == "ready" || review.Status == "leased" || review.Status == "running" {
		t.Fatalf("the dependent task was claimable at birth: status=%q", review.Status)
	}
	// This is the exact shape that failed in production: a design task and the
	// review of that design, created milliseconds apart.
	design := taskByTitle(t, store, "design")
	if design.Status != "ready" {
		t.Fatalf("the prerequisite was not runnable: %q", design.Status)
	}
}

// A graph that cannot be materialized fails closed, and nothing is created
// half-way.
func TestUnmaterializableGraphsFailClosed(t *testing.T) {
	tasksPort := newMemoryTasks()
	orchestrator := testOrchestratorForPorts(t, tasksPort, newFakeModels(), &fakeCompletion{verdict: CompletionPass})
	root := TaskRecord{ID: 1, OrganizationID: "explorarte", CorrelationID: "executive:corr", AssignedRoleID: CEORoleID}
	source := TaskRecord{ID: 2, AssignedRoleID: "ingenieria_ia/orquestador"}

	cyclic := []WorkerTaskProposal{proposal("a", "b"), proposal("b", "a")}
	if err := orchestrator.materializeWorkerTasks(context.Background(), root, source, "ingenieria_ia", cyclic, 0, 1); !errors.Is(err, ErrDependencyCycle) {
		t.Fatalf("cycle err=%v", err)
	}
	unknown := []WorkerTaskProposal{proposal("a", "nowhere")}
	if err := orchestrator.materializeWorkerTasks(context.Background(), root, source, "ingenieria_ia", unknown, 0, 1); !errors.Is(err, ErrContractRejected) {
		t.Fatalf("unknown dependency err=%v", err)
	}
	tasksPort.mu.Lock()
	defer tasksPort.mu.Unlock()
	if len(tasksPort.tasks) != 0 {
		t.Fatalf("an unmaterializable graph created %d tasks", len(tasksPort.tasks))
	}
}

// J: the pre-existing cycle detector is untouched.
func TestDependencyCycleDetectorStillHolds(t *testing.T) {
	if !dependencyCycle([]WorkerTaskProposal{proposal("a", "b"), proposal("b", "a")}) {
		t.Fatal("cycle detector stopped detecting a cycle")
	}
	if dependencyCycle([]WorkerTaskProposal{proposal("a", ""), proposal("b", "a")}) {
		t.Fatal("cycle detector reports a cycle on an acyclic graph")
	}
}
