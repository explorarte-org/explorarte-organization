package executive

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// rejectingContexts fails Build with a wrapped contextengine.ErrRejected the
// first time it sees the targeted purpose, and otherwise delegates -- the
// same decorator shape as recordingContexts in repository_grounding_test.go,
// but injecting a fault instead of only observing.
type rejectingContexts struct {
	inner            ContextCoordinator
	targetPurpose    ExecutionPurpose
	injectedOnce     bool
	rejectionMessage string
}

func (r *rejectingContexts) Build(ctx context.Context, request ContextRequest) (ContextSnapshot, error) {
	if !r.injectedOnce && request.ExecutionPurpose == string(r.targetPurpose) {
		r.injectedOnce = true
		return ContextSnapshot{}, fmt.Errorf("%w: %s", ErrContextSourceRejected, r.rejectionMessage)
	}
	return r.inner.Build(ctx, request)
}

// G1-005: a role present and executable in the canonical/DB role catalog
// but whose PERFIL.md (or any other context source its snapshot depends
// on) is missing from the physical file tree must never crash task
// dispatch with a raw filesystem error. ContextCoordinator.Build (the real
// implementation lives in runtimeadapter.Context.Build) translates that
// into ErrContextSourceRejected; the orchestrator must then close the
// attempt and block the root cleanly with a host-classified reason,
// resumable once the missing profile lands -- never propagate the raw
// error, never touch organization_revision_id.
func TestMissingContextSourceBlocksCleanlyInsteadOfCrashing(t *testing.T) {
	fixture := newWiringFixture(t, "freeze", fullSupply(), []EvidenceRequirementProposal{
		{Subject: "MaxDesignRounds", Relations: []string{"definition", "application"}},
	})
	const missingFileMessage = "resolve symlinks: lstat /opt/explorarte/context-source/recursos_agenticos/evaluador_agentes: no such file or directory"
	injector := &rejectingContexts{
		inner:            fixture.orchestrator.contexts,
		targetPurpose:    PurposeDepartmentWorker,
		rejectionMessage: missingFileMessage,
	}
	fixture.orchestrator.contexts = injector

	revisionBefore := fixture.rootRecord(t).OrganizationRevisionID

	run, err := fixture.driveUntilStopped(t, 24)
	if !errors.Is(err, ErrContextSourceRejected) {
		t.Fatalf("the run did not stop on the missing context source: run=%+v err=%v", run, err)
	}
	if run.State != StateBlocked || run.ReasonCode != ReasonContextSourceMissing {
		t.Fatalf("run=%+v, want blocked with %s", run, ReasonContextSourceMissing)
	}
	if !injector.injectedOnce {
		t.Fatal("the fault was never exercised -- test is not proving anything")
	}
	if _, ok := fixture.commandFor(PurposeDepartmentWorker); ok {
		t.Fatal("a worker invocation was created despite the missing context source")
	}

	task, ok := designWorkerTask(t, fixture)
	if !ok {
		t.Fatal("no department worker task exists")
	}
	if task.Status != "failed" {
		t.Fatalf("worker task status=%q, want failed with its attempt closed", task.Status)
	}
	if task.ReasonCode != "host_context_source_missing" {
		t.Fatalf("attempt closed as %q, want host_context_source_missing", task.ReasonCode)
	}
	if len(task.Attempts) != 1 {
		t.Fatalf("attempts=%d, want exactly the one the host opened and closed", len(task.Attempts))
	}
	if task.ActiveLease != nil {
		t.Fatal("the lease outlived the attempt it was issued to")
	}
	if invocations := fixture.harness.models.invocationCount(task.ID, task.Attempts[0].ID); invocations != 0 {
		t.Fatalf("%d model calls exist for an attempt that was closed before any call reached a provider", invocations)
	}

	// The permanent revision-drift guard (orchestrator.go's
	// organization_revision_drift check) must never be tripped by this
	// path: it compares OrganizationRevisionID, which this fix never
	// writes to.
	if after := fixture.rootRecord(t).OrganizationRevisionID; after != revisionBefore {
		t.Fatalf("organization_revision_id changed from %d to %d -- this must never happen as a side effect of a context-source rejection", revisionBefore, after)
	}

	// Resuming a context-source-blocked run must not walk into the same
	// wall a second time on a source that is now available -- but here the
	// fault is one-shot and already consumed, so a resume attempt must not
	// re-inject it or otherwise misbehave; it should simply remain blocked
	// (ErrRunBlocked) until an operator explicitly unblocks the root, exactly
	// like every other host-blocked reason in this file.
	if _, resumeErr := fixture.orchestrator.Resume(context.Background(), fixture.root); !errors.Is(resumeErr, ErrRunBlocked) {
		t.Fatalf("a context-source-blocked run did not stay blocked on resume: %v", resumeErr)
	}
}
