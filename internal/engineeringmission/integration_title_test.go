//go:build integration

package engineeringmission_test

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/engineeringmission"
)

// The objective a model actually wrote, which stopped a real campaign. The
// design froze, the implementation plan was valid, and the runner never
// started: 248 bytes did not fit a 240-byte task title, and CreateTask
// refused the mission.
//
// The unit tests prove missionTitle bounds a string. This one proves the
// mission path uses it, which is the half that was broken -- the function was
// never the problem, the wiring was.
func TestAMissionSurvivesAnObjectiveTooLongForATaskTitle(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required for this integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	store := openIntegrationStore(t, ctx)
	defer store.Close()
	resetIntegrationSchema(t, ctx, store)
	syncCanonical(t, ctx, store)

	repositoryRoot := filepath.Join(t.TempDir(), "repository")
	baseSHA := initializeMissionRepository(t, repositoryRoot)
	stagingRuntime, taskService, _ := openMissionRuntime(t, ctx, store, repositoryRoot)
	mission := engineeringmission.Service{Tasks: taskService, Promotion: stagingRuntime.Service}

	objective := "Produce the ImplementationPlan for the frozen design: create " +
		"docs/implementation/autonomy-smoke/AUTONOMY_SMOKE_010.md as minimal documentary " +
		"evidence of the autonomous cycle, with no other repository path touched and every " +
		"host gate left to decide."
	if len(objective) <= 240 {
		t.Fatalf("the regression fixture must exceed the title limit, got %d bytes", len(objective))
	}

	policy := engineeringmission.MissionPolicy{
		BaseSHA:            baseSHA,
		Objective:          objective,
		AllowedPaths:       []string{"fixture"},
		AcceptanceCriteria: []string{"fixture tests pass"},
		RequiredGates: []engineeringmission.RequiredGate{
			{Type: engineeringmission.GateBuild, Packages: []string{"./..."}},
		},
	}
	plan := missionPlan(`diff --git a/fixture/value.go b/fixture/value.go
--- a/fixture/value.go
+++ b/fixture/value.go
@@ -1,3 +1,3 @@
 package fixture

-func Value() int { return 1 }
+func Value() int { return 2 }
`, "fixture/value.go")

	task, err := mission.Create(ctx, policy, plan, "explorarte", reviewerRole, "human", "eduardo")
	if err != nil {
		t.Fatalf("a long objective must not stop a mission from being created: %v", err)
	}
	if task.ID == 0 {
		t.Fatal("no mission task was created")
	}
	if n := len(task.Title); n < 1 || n > 240 {
		t.Fatalf("mission title is %d bytes", n)
	}
	if !strings.HasPrefix(task.Title, "Produce the ImplementationPlan") {
		t.Fatalf("the title must still name the mission: %q", task.Title)
	}
	// The full objective is not lost: it stays in the policy, which is what
	// the governance envelope and the evidence digest are built from.
	if _, _, err := policy.MarshalEvidence(); err != nil {
		t.Fatalf("the policy carrying the full objective must still be usable: %v", err)
	}
}
