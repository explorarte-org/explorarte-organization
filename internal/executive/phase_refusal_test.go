package executive

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

// A refused mission must stop the run, not be retried forever.
//
// This is the case the deployment hit: the design froze, the implementation
// plan came back with a real diff, and the task engine refused the mission
// because its title did not fit. The refusal returned as an ordinary error,
// so the root stayed executable and the worker resumed it about nine thousand
// six hundred times over eight hours without recording anything anywhere.
//
// The distinguishing property is not that the failure is serious. It is that
// the next attempt submits the identical policy and the identical plan, so
// there is nothing to come back to.
func TestARefusedMissionBlocksTheRoot(t *testing.T) {
	fixture := newMissionFixture(t, smokePath, false)
	fixture.provisioner.err = fmt.Errorf("%w: %w: title must contain 1 to 240 bytes",
		ErrMissionRejected, tasks.ErrInvalidInput)
	fixture.drive(t)

	root := fixture.rootRecord(t)
	if root.Status != "blocked" {
		t.Fatalf("a refused mission must block the run, got status=%q", root.Status)
	}
	if root.ReasonCode != ReasonMissionRejected {
		t.Fatalf("reason=%q want %q", root.ReasonCode, ReasonMissionRejected)
	}
	// The block must say what was wrong. "mission_rejected" on its own sends
	// whoever reads it back to the code.
	if !strings.Contains(root.Reason, "title must contain") {
		t.Fatalf("the block must carry the engine's own words: %q", root.Reason)
	}
	if status := requirementStatus(root, MissionRequirementKey); status == "satisfied" {
		t.Fatal("a refused mission must not satisfy the requirement it failed to provision")
	}
}

// An unavailable provisioner is worth coming back for, and must NOT block:
// blocking a campaign over a database hiccup is the opposite failure and no
// better than looping over a refusal.
//
// The error is expected to escape Resume rather than be swallowed -- that is
// how the worker learns to try again -- so this drives the run by hand
// instead of through the fixture helper, which treats any escaping error as
// fatal.
func TestAnUnavailableProvisionerLeavesTheRunResumable(t *testing.T) {
	fixture := newMissionFixture(t, smokePath, false)
	transient := errors.New("dial tcp: connection refused")
	fixture.provisioner.err = transient

	var escaped error
	for i := 0; i < 24; i++ {
		run, err := fixture.orchestrator.Resume(context.Background(), fixture.root)
		if err != nil && !errors.Is(err, ErrRunBlocked) {
			escaped = err
			break
		}
		if run.State.Terminal() || run.State == StateBlocked {
			break
		}
	}
	if !errors.Is(escaped, transient) {
		t.Fatalf("a transient failure must escape Resume so the worker retries, got %v", escaped)
	}
	root := fixture.rootRecord(t)
	if root.ReasonCode == ReasonMissionRejected {
		t.Fatal("a transient failure must not be recorded as a refusal")
	}
	if root.Status == "blocked" {
		t.Fatalf("a transient failure must leave the run resumable, got blocked: %s", root.Reason)
	}
}
