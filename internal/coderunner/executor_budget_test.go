package coderunner

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func initBudgetFixtureRepo(t *testing.T, root string, untrackedFiles int) {
	t.Helper()
	for _, args := range [][]string{{"init"}, {"config", "user.email", "runner@example.invalid"}, {"config", "user.name", "runner"}} {
		c := exec.Command("git", args...)
		c.Dir = root
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	for i := 0; i < untrackedFiles; i++ {
		name := fmt.Sprintf("file-%03d.txt", i)
		if err := os.WriteFile(filepath.Join(root, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// TestExecutorPlanOutputBudgetAggregatesAcrossOperations proves the
// plan-level output budget is a real aggregate over every operation in one
// Execute call, not a per-operation limit reset on each call: several
// GIT_STATUS operations that are each individually comfortably under the
// budget still trip it once their combined real output crosses it, and
// execution stops before running every operation in the plan.
func TestExecutorPlanOutputBudgetAggregatesAcrossOperations(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git required")
	}
	root := t.TempDir()
	initBudgetFixtureRepo(t, root, 30)

	probe := &Executor{Workspace: root}
	probeResults, err := probe.Execute(context.Background(), Plan{Operations: []Operation{{Type: GitStatus}}})
	if err != nil || len(probeResults) != 1 {
		t.Fatalf("probe: results=%+v err=%v", probeResults, err)
	}
	perOpBytes := probeResults[0].BytesProduced
	if perOpBytes == 0 {
		t.Fatal("fixture produced no output to measure against")
	}

	plan := Plan{Operations: []Operation{
		{Type: GitStatus}, {Type: GitStatus}, {Type: GitStatus}, {Type: GitStatus}, {Type: GitStatus},
	}}
	// Each individual operation's real output is perOpBytes, comfortably
	// under this budget on its own; only the running sum across operations
	// exceeds it.
	budget := perOpBytes*3 + perOpBytes/2
	e := &Executor{Workspace: root, PlanOutputBudget: budget}
	results, err := e.Execute(context.Background(), plan)
	if err == nil || !strings.Contains(err.Error(), "plan output budget exceeded") {
		t.Fatalf("expected plan output budget error, got results=%+v err=%v", results, err)
	}
	if len(results) >= len(plan.Operations) {
		t.Fatalf("expected execution to stop before all %d operations ran, got %d", len(plan.Operations), len(results))
	}
	for _, r := range results {
		if r.BytesProduced > budget {
			t.Fatalf("no single operation in this fixture should individually exceed the aggregate budget: %d > %d", r.BytesProduced, budget)
		}
	}
}

// TestExecutorPlanOutputBudgetResetsPerExecuteCall proves the aggregate
// budget is scoped to one Execute call, not to the *Executor's lifetime:
// orgctl constructs a single Executor for the whole worker process and
// reuses it across every claimed task, so budget state left over from a
// previous plan must never make an unrelated later plan fail.
func TestExecutorPlanOutputBudgetResetsPerExecuteCall(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git required")
	}
	root := t.TempDir()
	initBudgetFixtureRepo(t, root, 5)

	probe := &Executor{Workspace: root}
	probeResults, err := probe.Execute(context.Background(), Plan{Operations: []Operation{{Type: GitStatus}}})
	if err != nil || len(probeResults) != 1 {
		t.Fatalf("probe: results=%+v err=%v", probeResults, err)
	}
	perCallBytes := probeResults[0].BytesProduced
	if perCallBytes == 0 {
		t.Fatal("fixture produced no output to measure against")
	}

	// Enough headroom for one call, nowhere near enough for two calls'
	// worth combined if state leaked between them.
	e := &Executor{Workspace: root, PlanOutputBudget: perCallBytes + perCallBytes/2}
	plan := Plan{Operations: []Operation{{Type: GitStatus}}}
	if _, err := e.Execute(context.Background(), plan); err != nil {
		t.Fatalf("first Execute call: %v", err)
	}
	if _, err := e.Execute(context.Background(), plan); err != nil {
		t.Fatalf("second Execute call on the same *Executor must not inherit budget consumed by the first: %v", err)
	}
}
