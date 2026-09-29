package executive

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"
)

// Smoke #23 (root 1554): the closure refused because the record said one file changed without showing
// the change. The closure is now shown the diff the mission applied, from the mission's own plan.

const root1554Patch = "--- a/internal/identifiers/identifiers_test.go\n+++ b/internal/identifiers/identifiers_test.go\n" +
	"@@ -20,3 +20,4 @@\n \t\t{\"empty input\", \"\", []string{}},\n+\t\t{\"a single digit between letters\", \"x7y\", []string{\"7\"}},\n \t}\n"

func TestRoot1554TheClosureIsShownTheAppliedDiff(t *testing.T) {
	mission := root1428Mission([]any{"internal/identifiers/identifiers_test.go"})
	mission.Instructions = `{"schema_version":"code-runner-execution/v1","operations":[` +
		`{"type":"APPLY_PATCH","patch":` + mustJSONString(root1554Patch) + `},` +
		`{"type":"GOFMT","path":"internal/identifiers/identifiers_test.go"},{"type":"GO_TEST","packages":["./internal/identifiers/..."]},{"type":"GO_TEST"}]}`
	o, root := closureExecutionFixture(t, mission)
	closure, _, err := o.createClosureTask(context.Background(), root, ExecutivePlan{Objective: "x"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(closure.Instructions, `"applied_patch":`) || !strings.Contains(closure.Instructions, `+\t\t{\"a single digit between letters\", \"x7y\", []string{\"7\"}},`) {
		t.Fatalf("the closure is not shown the applied diff:\n%s", closure.Instructions)
	}
	if !strings.Contains(ceoClosureInstructionPrefix, "the lines it changed (applied_patch)") {
		t.Error("the closure prefix does not say what applied_patch is for")
	}
}

func TestTheAppliedDiffIsBoundedAndNeverGuessed(t *testing.T) {
	if got := closureAppliedPatch("not a plan"); got != "" {
		t.Fatalf("an unreadable plan showed %q", got)
	}
	if got := closureAppliedPatch(`{"operations":[{"type":"GO_TEST"}]}`); got != "" {
		t.Fatalf("a plan with no patch showed %q", got)
	}
	long := "+" + strings.Repeat("é", closureAppliedPatchBytes) + "\n"
	got := closureAppliedPatch(`{"operations":[{"type":"APPLY_PATCH","patch":` + mustJSONString(long) + `}]}`)
	if !utf8.ValidString(got) || !strings.Contains(got, "[diff cut by the host: first ") || !strings.Contains(got, " bytes shown]") {
		t.Fatalf("a long diff was not cut visibly on a character boundary: %q", got[len(got)-80:])
	}
	two := closureAppliedPatch(`{"operations":[{"type":"APPLY_PATCH","patch":"A"},{"type":"GOFMT"},{"type":"APPLY_PATCH","patch":"B"}]}`)
	if two != "A\nB" {
		t.Fatalf("two patches shown as %q, want both in order", two)
	}
}
