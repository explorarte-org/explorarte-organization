package runtimeadapter

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/executive"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

// The B2 ambiguity-resolution boundary guard.
//
// The Executive writes ambiguity resolutions as task evidence whose identity
// lives in a namespaced reference plus metadata, riding the generic
// "result" type. Everything inside package executive runs against a fake
// TaskCoordinator that accepts any Type string -- which is exactly how a
// bespoke "ambiguity_resolution" type once passed its own suite while
// tasks.Service.ValidateEvidence would have rejected it at the production
// boundary with "evidence type is invalid" (RequirementType admits only
// artifact/check/approval/condition/result).
//
// These tests drive the REAL adapter -> Service -> ValidateEvidence chain
// over an in-memory persistence, so the exact shape B2 writes is proven
// acceptable where it matters, and the negative control proves the guard can
// actually see a bad shape.

// The literals below ARE the contract: if executive's writer ever drifts from
// this shape, this guard fails and someone must decide whether the boundary
// moved or the writer broke.
const (
	b2ResolutionReference = "ambiguity-resolution://340"
	b2ResolutionDigest    = "b6a3f1e05d9c4728ba0fe61d3c5a2974e8b10c6df24937a58c0d41e92fb6730a"
)

func b2ResolutionEvidenceCommand(evidenceType string) executive.EvidenceCommand {
	return executive.EvidenceCommand{
		TaskID:     900001,
		Type:       evidenceType,
		Reference:  b2ResolutionReference,
		Digest:     b2ResolutionDigest,
		RecordedBy: "executive-orchestrator",
		Metadata: map[string]any{
			"resolution":   "retry_authorized",
			"authority":    "host_policy",
			"policy":       "pure_model_execution_v1",
			"effect_class": "pure_model",
			"reason":       "ambiguous model execution after send started; pure_model_execution_v1 authorizes one retry: invocation=340 attempt=384 task=900001",
		},
		Satisfies: false,
	}
}

// boundaryPersistence is the minimum tasks.Persistence for one path: every
// method the RecordEvidence chain does not touch refuses loudly, so a test
// that reaches storage KNOWS validation passed, and RecordEvidence itself
// records what survived validation.
type boundaryPersistence struct {
	stored []tasks.RecordEvidenceCommand
}

func (p *boundaryPersistence) refuse() error {
	return errors.New("boundaryPersistence: unexpected call")
}

func (p *boundaryPersistence) GetTask(context.Context, int64) (tasks.TaskDetail, error) {
	return tasks.TaskDetail{}, p.refuse()
}
func (p *boundaryPersistence) ListTasks(context.Context, tasks.TaskFilter) ([]tasks.Task, error) {
	return nil, p.refuse()
}
func (p *boundaryPersistence) ListEvents(context.Context, int64, int) ([]tasks.Event, error) {
	return nil, p.refuse()
}
func (p *boundaryPersistence) ListAttempts(context.Context, int64) ([]tasks.Attempt, error) {
	return nil, p.refuse()
}
func (p *boundaryPersistence) ListDeadLetters(context.Context, int) ([]tasks.DeadLetter, error) {
	return nil, p.refuse()
}
func (p *boundaryPersistence) GetDeadLetter(context.Context, int64) (tasks.DeadLetter, error) {
	return tasks.DeadLetter{}, p.refuse()
}
func (p *boundaryPersistence) Create(context.Context, tasks.PreparedCreate) (tasks.Task, bool, error) {
	return tasks.Task{}, false, p.refuse()
}
func (p *boundaryPersistence) AddDependency(context.Context, tasks.AddDependencyCommand, int) error {
	return p.refuse()
}
func (p *boundaryPersistence) AddRequirement(context.Context, tasks.AddRequirementCommand, int) (tasks.Requirement, error) {
	return tasks.Requirement{}, p.refuse()
}
func (p *boundaryPersistence) RecordRequirementVerification(context.Context, tasks.RequirementVerificationCommand, int) (tasks.Evidence, error) {
	return tasks.Evidence{}, p.refuse()
}
func (p *boundaryPersistence) VerifyActiveExecutionLease(context.Context, tasks.VerifyExecutionLeaseCommand) (tasks.ExecutionLeaseContext, error) {
	return tasks.ExecutionLeaseContext{}, p.refuse()
}
func (p *boundaryPersistence) Finalize(context.Context, tasks.FinalizeCommand, int) (tasks.Task, error) {
	return tasks.Task{}, p.refuse()
}
func (p *boundaryPersistence) Block(context.Context, tasks.BlockCommand, int) (tasks.Task, error) {
	return tasks.Task{}, p.refuse()
}
func (p *boundaryPersistence) Unblock(context.Context, tasks.UnblockCommand, tasks.AssigneeCheck, int) (tasks.Task, error) {
	return tasks.Task{}, p.refuse()
}
func (p *boundaryPersistence) ReleaseCoordinationHold(context.Context, tasks.ReleaseCoordinationHoldCommand, tasks.AssigneeCheck, int) (tasks.Task, error) {
	return tasks.Task{}, p.refuse()
}
func (p *boundaryPersistence) Cancel(context.Context, tasks.CancelCommand, int) (tasks.Task, error) {
	return tasks.Task{}, p.refuse()
}
func (p *boundaryPersistence) Claim(context.Context, tasks.ClaimRequest, tasks.AssigneeValidator, int) ([]tasks.ClaimedTask, error) {
	return nil, p.refuse()
}
func (p *boundaryPersistence) StartAttempt(context.Context, tasks.LeaseCommand, int) (tasks.Task, error) {
	return tasks.Task{}, p.refuse()
}
func (p *boundaryPersistence) Heartbeat(context.Context, tasks.LeaseCommand, time.Duration) (tasks.Lease, error) {
	return tasks.Lease{}, p.refuse()
}
func (p *boundaryPersistence) RecordAttemptResult(context.Context, tasks.RecordAttemptResultCommand, tasks.RetryPolicy, int) (tasks.Task, error) {
	return tasks.Task{}, p.refuse()
}
func (p *boundaryPersistence) Reconcile(context.Context, int, tasks.AssigneeValidator, tasks.RetryPolicy, int) (tasks.ReconcileResult, error) {
	return tasks.ReconcileResult{}, p.refuse()
}
func (p *boundaryPersistence) ClaimOutbox(context.Context, tasks.OutboxClaimRequest) ([]tasks.ClaimedOutboxEvent, error) {
	return nil, p.refuse()
}
func (p *boundaryPersistence) AckOutbox(context.Context, tasks.OutboxDisposition) error {
	return p.refuse()
}
func (p *boundaryPersistence) NackOutbox(context.Context, tasks.OutboxDisposition) error {
	return p.refuse()
}
func (p *boundaryPersistence) RecoverOutbox(context.Context, int) (int, error) {
	return 0, p.refuse()
}
func (p *boundaryPersistence) OutboxStats(context.Context) (tasks.OutboxStats, error) {
	return tasks.OutboxStats{}, p.refuse()
}

// RecordEvidence is the one live method: reaching it means ValidateEvidence
// already accepted the command.
func (p *boundaryPersistence) RecordEvidence(_ context.Context, command tasks.RecordEvidenceCommand, _ int) (tasks.Evidence, error) {
	p.stored = append(p.stored, command)
	digest := command.Digest
	return tasks.Evidence{
		ID: int64(len(p.stored)), TaskID: command.TaskID, Type: command.Type,
		Reference: command.Reference, Digest: &digest, RecordedBy: command.RecordedBy,
		Metadata: command.Metadata,
	}, nil
}

type boundaryCatalog struct{}

func (boundaryCatalog) CurrentRevision(context.Context, string) (tasks.RevisionRef, error) {
	return tasks.RevisionRef{ID: 7}, nil
}
func (boundaryCatalog) GetRole(context.Context, string, string) (tasks.RoleRef, error) {
	return tasks.RoleRef{}, errors.New("not used by this guard")
}

func newBoundaryService(t *testing.T) (Tasks, *boundaryPersistence) {
	t.Helper()
	persistence := &boundaryPersistence{}
	service, err := tasks.NewService(persistence, boundaryCatalog{}, tasks.Config{
		OrganizationID: "explorarte", DefaultMaxAttempts: 5,
		DefaultLeaseDuration: time.Minute, MaxLeaseDuration: 15 * time.Minute,
		RetryPolicy:       tasks.RetryPolicy{BaseDelay: time.Second, MaxDelay: time.Minute},
		OutboxMaxAttempts: 3, OutboxClaimDuration: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	return Tasks{Service: service, OrganizationID: "explorarte"}, persistence
}

// THE guard: the exact EvidenceCommand B2 writes crosses the real adapter,
// the real tasks.Service, and the real ValidateEvidence enum -- reaching
// storage as a "result" whose identity is its reference and metadata.
func TestAmbiguityResolutionShapeCrossesTheRealBoundary(t *testing.T) {
	adapter, persistence := newBoundaryService(t)

	if err := adapter.RecordEvidence(context.Background(), b2ResolutionEvidenceCommand("result")); err != nil {
		t.Fatalf("the B2 resolution shape was rejected by the production boundary: %v", err)
	}
	if len(persistence.stored) != 1 {
		t.Fatalf("want exactly one stored evidence row, got %d", len(persistence.stored))
	}
	if persistence.stored[0].Type != tasks.RequirementResult {
		t.Fatalf("stored type = %q, want %q", persistence.stored[0].Type, tasks.RequirementResult)
	}
}

// The negative control: had B2 written its bespoke type, THIS is precisely
// where production would have refused it. The test pins that the boundary's
// rejection still exists, so the positive case above cannot pass vacuously.
func TestABespokeEvidenceTypeIsRejectedByTheRealBoundary(t *testing.T) {
	adapter, persistence := newBoundaryService(t)

	err := adapter.RecordEvidence(context.Background(), b2ResolutionEvidenceCommand("ambiguity_resolution"))
	if err == nil || !strings.Contains(err.Error(), "evidence type is invalid") {
		t.Fatalf("the boundary must refuse unknown evidence types, got %v", err)
	}
	if len(persistence.stored) != 0 {
		t.Fatalf("a rejected command must not reach storage: %+v", persistence.stored)
	}
}
