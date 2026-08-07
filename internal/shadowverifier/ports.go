package shadowverifier

import (
	"context"
	"time"
)

// FactReader loads the shadow's own projection of the live organization
// state. The postgres adapter implements it by reading Rama 03's tables
// (organizations, organizational_units, organization_roles,
// organization_reporting_lines) directly via SQL — never importing
// internal/organization/registry, the same cross-branch pattern
// internal/completion and internal/decisiongraphtrace established.
type FactReader interface {
	LoadSnapshot(ctx context.Context) (Snapshot, error)
}

// TrafficReader returns recorded authorization_requests rows — real traffic
// with inputs and outcome, the replay corpus for
// exit_criteria.parity_with_go_rules_on_recorded_traffic. The adapter reads
// Rama 06's authorization_requests table directly.
type TrafficReader interface {
	RecordedRequests(ctx context.Context, limit int) ([]RecordedRequest, error)
}

// FindingWriter persists runs and findings. It is the only write surface this
// package has, and it writes exclusively to shadowverifier's own tables
// (migration 000014) — never to any other branch's state.
type FindingWriter interface {
	StartRun(ctx context.Context, run RunRecord) (int64, error)
	FinishRun(ctx context.Context, runID int64, summary RunSummary, status string) error
	RecordFindings(ctx context.Context, runID int64, findings []Finding) error
	GetRun(ctx context.Context, runID int64) (RunRecord, RunSummary, error)
	ListRuns(ctx context.Context, limit int) ([]RunRecord, error)
	RunFindings(ctx context.Context, runID int64) ([]Finding, error)
}

// GroundTruth exposes what the real Go engines decide, so the comparator can
// measure the shadow against them. It is implemented at the CLI composition
// root by wiring the actual registry and authorization services — never
// inside internal/shadowverifier, where calling the real engines would make
// the shadow derivation a tautology. The comparison is the harness's job;
// independence is the derivation's job.
type GroundTruth interface {
	// RoleExists mirrors registry.Service.GetRole semantics (retired roles
	// do not exist).
	RoleExists(ctx context.Context, roleID string) (bool, error)
	// DepartmentExists mirrors registry.Service.GetUnit semantics.
	DepartmentExists(ctx context.Context, unitID string) (bool, error)
	// LeaderOf mirrors registry.Service.GetLeader: found=false when the
	// unit has no canonical leader row.
	LeaderOf(ctx context.Context, unitID string) (roleID string, found bool, err error)
	// EvaluateCapability returns the real Authorizer's effect and reason
	// code for one (role, capability) pair at the current revision.
	EvaluateCapability(ctx context.Context, roleID, capabilityID string) (effect, reasonCode string, err error)
	// CanonicalReportingClosed reports whether the canonical loader's static
	// validation found the reports_to graph closed (no dangling references,
	// self-references or cycles), plus the issue codes when it did not.
	CanonicalReportingClosed(ctx context.Context) (closed bool, issues []string, err error)
}

// Clock is injected the same way every other service in this repo injects
// time, so run and finding timestamps are deterministic in tests.
type Clock interface {
	Now() time.Time
}

type ClockFunc func() time.Time

func (f ClockFunc) Now() time.Time { return f() }
