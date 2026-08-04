package tasks

import (
	"context"
	"time"
)

type Catalog interface {
	CurrentRevision(context.Context, string) (RevisionRef, error)
	GetRole(context.Context, string, string) (RoleRef, error)
}

type TaskReader interface {
	GetTask(context.Context, int64) (TaskDetail, error)
	ListTasks(context.Context, TaskFilter) ([]Task, error)
	ListEvents(context.Context, int64, int) ([]Event, error)
	ListAttempts(context.Context, int64) ([]Attempt, error)
	ListDeadLetters(context.Context, int) ([]DeadLetter, error)
	GetDeadLetter(context.Context, int64) (DeadLetter, error)
}

type TaskService interface {
	CreateTask(context.Context, CreateRequest, string, string) (Task, bool, error)
	AddDependency(context.Context, AddDependencyCommand) error
	AddRequirement(context.Context, AddRequirementCommand) (Requirement, error)
	RecordEvidence(context.Context, RecordEvidenceCommand) (Evidence, error)
	FinalizeTask(context.Context, FinalizeCommand) (Task, error)
	BlockTask(context.Context, BlockCommand) (Task, error)
	UnblockTask(context.Context, UnblockCommand) (Task, error)
	CancelTask(context.Context, CancelCommand) (Task, error)
}

type TaskQueue interface {
	ClaimTasks(context.Context, ClaimRequest) ([]ClaimedTask, error)
	StartAttempt(context.Context, LeaseCommand) (Task, error)
	Heartbeat(context.Context, LeaseCommand) (Lease, error)
	RecordAttemptResult(context.Context, RecordAttemptResultCommand) (Task, error)
}

type TaskReconciler interface {
	Reconcile(context.Context, int) (ReconcileResult, error)
}

type OutboxStore interface {
	ClaimOutbox(context.Context, OutboxClaimRequest) ([]ClaimedOutboxEvent, error)
	AckOutbox(context.Context, OutboxDisposition) error
	NackOutbox(context.Context, OutboxDisposition) error
	RecoverOutbox(context.Context, int) (int, error)
	OutboxStats(context.Context) (OutboxStats, error)
}

type Persistence interface {
	TaskReader
	Create(context.Context, PreparedCreate) (Task, bool, error)
	AddDependency(context.Context, AddDependencyCommand, int) error
	AddRequirement(context.Context, AddRequirementCommand, int) (Requirement, error)
	RecordEvidence(context.Context, RecordEvidenceCommand, int) (Evidence, error)
	Finalize(context.Context, FinalizeCommand, int) (Task, error)
	Block(context.Context, BlockCommand, int) (Task, error)
	Unblock(context.Context, UnblockCommand, AssigneeCheck, int) (Task, error)
	Cancel(context.Context, CancelCommand, int) (Task, error)
	Claim(context.Context, ClaimRequest, AssigneeValidator, int) ([]ClaimedTask, error)
	StartAttempt(context.Context, LeaseCommand, int) (Task, error)
	Heartbeat(context.Context, LeaseCommand, time.Duration) (Lease, error)
	RecordAttemptResult(context.Context, RecordAttemptResultCommand, RetryPolicy, int) (Task, error)
	Reconcile(context.Context, int, AssigneeValidator, RetryPolicy, int) (ReconcileResult, error)
	OutboxStore
}
