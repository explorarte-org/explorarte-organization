package completion

import (
	"context"
	"time"
)

// RequirementType mirrors internal/tasks.RequirementType's values exactly
// (artifact, check, approval, condition, result). completion never imports
// internal/tasks — its postgres adapter reads task_requirements/task_evidence via
// SQL directly, the same cross-package pattern internal/decisiongraphtrace already
// established for reading internal/decisiongraph's tables.
type RequirementType string

const (
	RequirementArtifact  RequirementType = "artifact"
	RequirementCheck     RequirementType = "check"
	RequirementApproval  RequirementType = "approval"
	RequirementCondition RequirementType = "condition"
	RequirementResult    RequirementType = "result"
)

// RequirementFact is the read-only projection completion needs of one
// internal/tasks requirement row plus its most recent evidence, if any.
type RequirementFact struct {
	RequirementID  int64
	Type           RequirementType
	Required       bool
	Satisfied      bool
	EvidenceRef    string // Evidence.Reference: artifact path, approval request ID, etc.
	EvidenceDigest string // Evidence.Digest, empty when the requirement type doesn't carry one
}

// TaskFact is the read-only projection of the internal/tasks.Task row completion
// needs to decide whether re-checking is even applicable (e.g. status must already
// be awaiting_verification for a completion claim to make sense).
type TaskFact struct {
	TaskID       int64
	Status       string // mirrors internal/tasks.Status values; compared as a string to avoid importing the package
	Requirements []RequirementFact
}

// TaskReader reads internal/tasks' own record of a task and its requirements —
// this is the "requirements_satisfied" obligation's system of record.
type TaskReader interface {
	TaskFacts(ctx context.Context, taskID int64) (TaskFact, error)
}

// ArtifactChecker independently confirms a staging artifact actually exists and its
// stored content hash matches what the requirement's evidence claims — the
// "artifact_exists" obligation's system of record is internal/staging, not
// whatever internal/tasks was told.
type ArtifactChecker interface {
	// ArtifactDigest returns the real content digest of the artifact at reference,
	// or ErrArtifactNotFound if nothing exists there.
	ArtifactDigest(ctx context.Context, reference string) (digest string, err error)
}

// CheckRunChecker independently confirms the checks a requirement of type "check"
// claims to have passed actually ran and passed. The system of record is
// internal/staging's staging_checks table (Rama 05 records a real check run there,
// keyed by (task_id, requirement_id), independent of and prior to whatever
// task_evidence.status was self-reported as satisfied) — not internal/tasks' own
// evidence log.
type CheckRunChecker interface {
	CheckPassed(ctx context.Context, taskID, requirementID int64) (bool, error)
}

// ApprovalChecker independently confirms an approval requirement's evidence points
// to a real, actually-consumed authorization.ApprovalRequest with a matching
// action digest — the "approval_present" obligation's system of record is
// internal/authorization, not whatever internal/tasks was told.
type ApprovalChecker interface {
	// ApprovalConsumed reports whether the approval request identified by
	// requestRef is in the "consumed" state and its stored action digest equals
	// actionDigest. requestRef is the Evidence.Reference for an approval-type
	// requirement (expected to hold the ApprovalRequest ID).
	ApprovalConsumed(ctx context.Context, requestRef, actionDigest string) (bool, error)
}

// DecisionBranchChecker independently re-confirms that the decision graph
// candidate a task's completion depended on was genuinely selected, rather than
// trusting whatever the caller claims. decisiongraph.DecisionRecord.Validate
// already guarantees the selected candidate node was in BranchSelected state at
// the moment the decision was recorded, and decision_graph_nodes' own guard
// trigger makes BranchSelected effectively terminal (a selected node can never
// transition away — only active can move to selected/rejected_by_*, and only
// rejected_by_*/inconclusive can reopen back to active). So this check is
// deliberately defense-in-depth, not a hedge against a reachable "selected then
// later rejected" case: it re-derives the fact from decisiongraph's own tables at
// completion time instead of trusting a value threaded through unrelated call
// paths, and it does catch a real scenario the schema does allow — a task/attempt
// pair with more than one decision_graph_runs row (e.g. a redone reasoning
// attempt), where an earlier succeeded run's decision must not be the one
// consulted if a later run superseded it.
//
// Most tasks never touch the decision graph at all — decisiongraph.Run carries
// TaskID/AttemptID directly, so "no run exists for this attempt" is resolved by
// the checker itself, not left to the caller to know in advance. found=false means
// exactly that: the obligation "no rejected branch was reused" holds vacuously,
// since no branch was involved in the first place.
type DecisionBranchChecker interface {
	// CurrentBranchStateForAttempt returns the *current* branch state of the
	// selected candidate node for the terminal decision of the run matching
	// (taskID, attemptID). found is false when no such run exists yet, or a run
	// exists but never reached a terminal decision.
	CurrentBranchStateForAttempt(ctx context.Context, taskID, attemptID int64) (state string, found bool, err error)
}

// Clock is injected the same way every other service in this repo injects time,
// so verification timestamps are deterministic in tests.
type Clock interface {
	Now() time.Time
}

type ClockFunc func() time.Time

func (f ClockFunc) Now() time.Time { return f() }
