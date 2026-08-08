package decisiongraph

import (
	"context"
	"time"
)

type Ledger interface {
	CreateRun(context.Context, CreateRunRequest, time.Time) (Run, error)
	AppendGraph(context.Context, AppendGraphRequest, time.Time) (GraphVersion, error)
	StartRun(context.Context, int64, time.Time) error
	TransitionBranch(context.Context, BranchTransitionRequest, time.Time) error
	ClaimReadyNode(context.Context, ClaimNodeRequest, time.Time) (NodeClaim, error)
	FinishExecution(context.Context, FinishExecutionRequest, time.Time) error
	RecordObservation(context.Context, ObservationRecord, time.Time) error
	RecordVerification(context.Context, VerificationRecord, time.Time) error
	RecordTerminalDecision(context.Context, TerminalDecisionRequest, time.Time) error
	CloseUnselectedRun(context.Context, CloseUnselectedRunRequest, time.Time) error
	RecoverExpiredExecutions(context.Context, int, time.Time) (int, error)
	TraceRef(context.Context, int64) (TraceRef, error)
}

type Clock interface {
	Now() time.Time
}

type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now().UTC() }
