package workflowruntime

import "context"

// TaskPort is the only state-changing port in the V2 workflow core. Its
// implementation delegates to internal/tasks, which remains the durable source
// of truth. The port deliberately exposes behavior rather than persistence.
type TaskPort interface {
	Initiate(context.Context, WorkRequest, Actor) (Snapshot, bool, error)
	Observe(context.Context, int64) (Snapshot, error)
	StartExecution(context.Context, ExecutionCommand) (Snapshot, error)
	RecordOutcome(context.Context, OutcomeCommand) (Snapshot, error)
	RecordEvidence(context.Context, EvidenceCommand) (Snapshot, error)
	FinalizeCompleted(context.Context, CompleteCommand) (Snapshot, error)
	Block(context.Context, BranchRequest, BranchAction) (Snapshot, error)
}

// CompletionPort independently evaluates obligations. It never owns terminal
// task state; only TaskPort may persist the completed transition.
type CompletionPort interface {
	Verify(context.Context, int64, int64) (CompletionDecision, error)
}

// DecisionPort is a replaceable branching strategy. Its decision can cause a
// task action, but cannot declare the task terminal.
type DecisionPort interface {
	Evaluate(context.Context, BranchRequest) (BranchDecision, error)
}

// CoordinationPort is reserved for authorized cross-role coordination. Same-
// role execution never reaches this port.
type CoordinationPort interface {
	Send(context.Context, CoordinationCommand, Snapshot, Snapshot) (CoordinationRecord, bool, error)
}

// ExecutivePort keeps the legacy owner-goal decomposition available behind the
// V2 seam while its representation is migrated incrementally.
type ExecutivePort interface {
	Start(context.Context, GoalRequest) (ExecutiveStart, error)
}
