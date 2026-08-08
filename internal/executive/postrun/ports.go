package postrun

import (
	"context"

	"github.com/Mireuz13/explorarte-organization/internal/completion"
	"github.com/Mireuz13/explorarte-organization/internal/decisiongraphtrace"
	"github.com/Mireuz13/explorarte-organization/internal/memory"
)

// TraceReader resolves a terminal decisiongraph run to the task/attempt it
// belongs to. decisiongraphtrace.Store satisfies this directly.
type TraceReader interface {
	RunSummary(ctx context.Context, runID int64) (decisiongraphtrace.RunSummary, error)
}

// Verifier independently re-derives the completion verdict for a task
// attempt. completion.Service satisfies this directly.
type Verifier interface {
	Verify(ctx context.Context, request completion.VerificationRequest) (completion.VerificationResult, error)
}

// RoleResolver looks up the role a task attempt actually ran as, so a
// proposed candidate is attributed to (and authorized as) that role rather
// than this job's own identity.
type RoleResolver interface {
	AssignedRoleID(ctx context.Context, taskID int64) (string, error)
}

// LessonProposer persists a governed memory candidate. memory.Manager
// satisfies this directly.
type LessonProposer interface {
	Propose(ctx context.Context, request memory.ProposeRequest) (memory.Entry, bool, error)
}

var (
	_ TraceReader    = (*decisiongraphtrace.Store)(nil)
	_ Verifier       = (*completion.Service)(nil)
	_ LessonProposer = (*memory.Manager)(nil)
)
