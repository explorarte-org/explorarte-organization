package decisiongraph

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

type fakeLedger struct {
	createCalls   int
	appendCalls   int
	recoveryCalls int
	createErr     error
	terminalErr   error
}

func (f *fakeLedger) CreateRun(_ context.Context, request CreateRunRequest, now time.Time) (Run, error) {
	f.createCalls++
	if f.createErr != nil {
		return Run{}, f.createErr
	}
	return Run{ID: 1, TaskID: request.TaskID, AttemptID: request.AttemptID, Status: RunPlanned, Deadline: request.Deadline, CreatedAt: now}, nil
}
func (f *fakeLedger) AppendGraph(context.Context, AppendGraphRequest, time.Time) (GraphVersion, error) {
	f.appendCalls++
	return GraphVersion{ID: 1, RunID: 1, VersionNumber: 1}, nil
}
func (*fakeLedger) StartRun(context.Context, int64, time.Time) error { return nil }
func (*fakeLedger) TransitionBranch(context.Context, BranchTransitionRequest, time.Time) error {
	return nil
}
func (*fakeLedger) ClaimReadyNode(context.Context, ClaimNodeRequest, time.Time) (NodeClaim, error) {
	return NodeClaim{ExecutionID: 1}, nil
}
func (*fakeLedger) FinishExecution(context.Context, FinishExecutionRequest, time.Time) error {
	return nil
}
func (*fakeLedger) RecordObservation(context.Context, ObservationRecord, time.Time) error {
	return nil
}
func (*fakeLedger) RecordVerification(context.Context, VerificationRecord, time.Time) error {
	return nil
}
func (f *fakeLedger) RecordTerminalDecision(context.Context, TerminalDecisionRequest, time.Time) error {
	return f.terminalErr
}
func (f *fakeLedger) RecoverExpiredExecutions(context.Context, int, time.Time) (int, error) {
	f.recoveryCalls++
	return 2, nil
}
func (*fakeLedger) TraceRef(context.Context, int64) (TraceRef, error) {
	return TraceRef{RunID: 1, TraceHash: testHash, SchemaVersion: "decision-trace/v1"}, nil
}

func validCreateRunRequest(now time.Time) CreateRunRequest {
	return CreateRunRequest{
		TaskID:                       1,
		AttemptID:                    1,
		ReasoningPolicySchemaVersion: "0.1.0",
		ReasoningPolicyHash:          testHash,
		IdempotencyKey:               "run-1",
		BudgetLimits:                 testLimits(),
		Deadline:                     now.Add(time.Hour),
		CreatedBy:                    "planner",
	}
}

func TestServiceRejectsInvalidRunBeforeLedger(t *testing.T) {
	now := time.Unix(1000, 0).UTC()
	ledger := &fakeLedger{}
	service, err := NewService(ledger, fixedClock{now: now})
	if err != nil {
		t.Fatal(err)
	}
	request := validCreateRunRequest(now)
	request.Deadline = now
	if _, err := service.CreateRun(context.Background(), request); !errors.Is(err, ErrInvalidRun) {
		t.Fatalf("expected invalid run, got %v", err)
	}
	if ledger.createCalls != 0 {
		t.Fatalf("ledger called %d times", ledger.createCalls)
	}
}

func TestServiceRejectsTerminalUnknownDecision(t *testing.T) {
	ledger := &fakeLedger{}
	service, err := NewService(ledger, fixedClock{now: time.Unix(1000, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	request := TerminalDecisionRequest{
		RunID: 1, DecisionNodeID: 2, SelectedCandidateNodeID: 3,
		EvidenceSetHash: testHash, VerificationSetHash: testHash, DecisionHash: testHash,
		VerificationLabel: VerificationUnknown, CreatedBy: "verifier",
	}
	if err := service.RecordTerminalDecision(context.Background(), request); !errors.Is(err, ErrInvalidDecision) {
		t.Fatalf("expected invalid decision, got %v", err)
	}
}

func TestServiceRejectsReopenWithoutEvidence(t *testing.T) {
	ledger := &fakeLedger{}
	service, err := NewService(ledger, fixedClock{now: time.Unix(1000, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	err = service.TransitionBranch(context.Background(), BranchTransitionRequest{
		RunID: 1, NodeID: 2, ToState: BranchActive, Actor: "planner",
	})
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("expected invalid evidence-less reopen, got %v", err)
	}
}

func TestServiceBoundsRecoveryBatch(t *testing.T) {
	ledger := &fakeLedger{}
	service, err := NewService(ledger, fixedClock{now: time.Unix(1000, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.RecoverExpiredExecutions(context.Background(), 257); !errors.Is(err, ErrInvalidExecution) {
		t.Fatalf("expected invalid execution, got %v", err)
	}
	if ledger.recoveryCalls != 0 {
		t.Fatalf("recovery called %d times", ledger.recoveryCalls)
	}
	count, err := service.RecoverExpiredExecutions(context.Background(), 10)
	if err != nil || count != 2 {
		t.Fatalf("count=%d err=%v", count, err)
	}
}
