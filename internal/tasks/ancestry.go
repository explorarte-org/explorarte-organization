package tasks

import (
	"errors"
	"fmt"
	"sort"
)

// A task's parent is the task named by its causation ("task:<id>"). Work
// spawned beneath a parent is only meaningful while the parent's scope is still
// live: once an Executive root (or any execution scope) is blocked, cancelled or
// dead, its remaining descendants must not start. The Executive itself stops
// driving such a run, but a descendant left READY stays claimable by anything
// that reaches the queue -- production root 829 was left with two ready and one
// pending descendant under a blocked root.
//
// Two things are deliberately NOT in this rule:
//
//   - A COMPLETED ancestor never blocks. Planning tasks complete precisely so
//     that the tasks they derived can run.
//   - A CEO chat turn does not scope the work it requested. A turn that fails
//     while formatting its final answer (a provider truncation, a persistence
//     glitch) has already durably requested a financial review, and that review
//     must still run. Production task 837 is exactly that: a completed Finance
//     review beneath a failed chat turn.

// TaskClassCEOChatTurn is the class of a CEO chat turn task (the same value as
// internal/ceochat's TaskClass; a test in that package pins the equality).
const TaskClassCEOChatTurn = "executive.ceo_chat_turn"

// ErrAncestorBlocked means a claim was refused because an ancestor's state
// withdraws the work beneath it. It carries no side effect: the task is left
// exactly as it was.
var ErrAncestorBlocked = errors.New("task has an ancestor whose state blocks descendant execution")

// BlocksDescendantClaim reports whether an ancestor in this status withdraws
// the work beneath it.
func (s Status) BlocksDescendantClaim() bool {
	switch s {
	case StatusBlocked, StatusCancelled, StatusRejected, StatusFailed, StatusDeadLetter:
		return true
	default:
		return false
	}
}

// BlockingAncestorStatuses lists every status for which BlocksDescendantClaim
// holds, sorted, so SQL and Go evaluate the one definition.
func BlockingAncestorStatuses() []string {
	var out []string
	for status := range allStatuses {
		if status.BlocksDescendantClaim() {
			out = append(out, string(status))
		}
	}
	sort.Strings(out)
	return out
}

// TaskClassScopesDescendants reports whether tasks of this class own the
// lifetime of the tasks beneath them.
func TaskClassScopesDescendants(taskClass string) bool {
	return taskClass != TaskClassCEOChatTurn
}

// NonScopingTaskClasses lists the classes for which TaskClassScopesDescendants
// is false.
func NonScopingTaskClasses() []string { return []string{TaskClassCEOChatTurn} }

// AncestorBlock names the ancestor that refused a claim.
type AncestorBlock struct {
	TaskID    int64
	Status    Status
	TaskClass string
}

// Blocks reports whether an ancestor with this state withdraws its descendants.
func (a AncestorBlock) Blocks() bool {
	return a.Status.BlocksDescendantClaim() && TaskClassScopesDescendants(a.TaskClass)
}

func (a AncestorBlock) Error() string {
	return fmt.Sprintf("%v: ancestor task %d (%s) is %s", ErrAncestorBlocked, a.TaskID, a.TaskClass, a.Status)
}

func (a AncestorBlock) Unwrap() error { return ErrAncestorBlocked }
