package shadowverifier

import (
	"fmt"
	"sort"
	"strings"
)

// The derivations below are the shadow's independent re-derivation of the
// Phase 1 facts. They are pure functions over a Snapshot (plus the matrix for
// capability_granted): no registry or authorization code is reachable from
// here, and no database access happens inside them.

// RoleExists re-derives role_exists: a role exists when a non-retired
// organization_roles row matches, mirroring registry.GetRole's
// retired_at IS NULL filter (the adapter already excludes retired rows from
// the snapshot).
func RoleExists(snap Snapshot, roleID string) bool {
	_, ok := snap.RoleIndex()[roleID]
	return ok
}

// DepartmentExists re-derives department_exists against
// organizational_units (note the real table name), mirroring
// registry.GetUnit's retired_at IS NULL filter.
func DepartmentExists(snap Snapshot, unitID string) bool {
	_, ok := snap.UnitIndex()[unitID]
	return ok
}

// LeaderOf re-derives leader_of for one unit. Returns the leader role ID and
// found=true when the unit has exactly one non-retired canonical leader, or
// found=false with anomaly="" when it has none (leaderless truth). When the
// unit is unknown, or has more than one canonical leader (the Rama 03
// validation gap this branch's bug hunt confirmed possible), the shadow
// refuses to pick: registry.GetLeader uses QueryRow without ORDER BY and
// would return an arbitrary row, so any comparison there would be against a
// nondeterministic ground truth. anomaly carries the explanation; callers
// record it as a counterexample and mark the comparison uncomparable.
func LeaderOf(snap Snapshot, unitID string) (leaderID string, found bool, anomaly string) {
	unit, ok := snap.UnitIndex()[unitID]
	if !ok {
		return "", false, fmt.Sprintf("unit %q does not exist", unitID)
	}
	var leaders []string
	for _, role := range snap.Roles {
		if role.UnitID == unitID && role.CanonicalLeader {
			leaders = append(leaders, role.ID)
		}
	}
	sort.Strings(leaders)
	switch len(leaders) {
	case 0:
		if !unit.Leaderless {
			return "", false, fmt.Sprintf("unit %q is not leaderless but has no canonical leader", unitID)
		}
		return "", false, ""
	case 1:
		return leaders[0], true, ""
	default:
		return "", false, fmt.Sprintf("unit %q has %d canonical leaders (%s); ground truth is nondeterministic", unitID, len(leaders), strings.Join(leaders, ", "))
	}
}

// reportingEdge reports whether target reports to source in the snapshot's
// current-revision reporting lines.
func reportingEdge(snap Snapshot, roleID, reportsToID string) bool {
	for _, line := range snap.ReportingLines {
		if line.RoleID == roleID && line.ReportsToRoleID == reportsToID {
			return true
		}
	}
	return false
}

// MayDelegate re-derives may_delegate(from, to): from may delegate work to to.
//
// Source of truth: the current-revision reports_to graph — a leader delegates
// to roles that report to them (direct edge to → from). Transitive closure is
// deliberately not included: delegation in this organization is a direct
// management relation, and the canon's task.assign_worker /
// project.delegate_department capabilities are granted per authority class,
// not per chain depth.
//
// Canonical exception: organization.yaml declares the executive observer with
// may_delegate: false, so roles with runtime_kind
// concurrent_read_only_observer never delegate even if an edge existed.
// Nonexistent roles never delegate.
func MayDelegate(snap Snapshot, from, to string) bool {
	roles := snap.RoleIndex()
	fromRole, ok := roles[from]
	if !ok {
		return false
	}
	if _, ok := roles[to]; !ok {
		return false
	}
	if fromRole.RuntimeKind == RuntimeKindReadOnlyObserver {
		return false
	}
	return reportingEdge(snap, to, from)
}

// MayMessage re-derives may_message(from, to): from may address to.
//
// instruction-precedence.yaml was evaluated and rejected as the primary
// source: its authority tiers are content classes (priority 0-6) with no
// canonical mapping from roles or authority classes to tiers, so deriving
// role-to-role messaging from it would require inventing a mapping. The
// sources used instead, all canonical:
//
//  1. A reports_to edge in either direction is a two-way communication
//     channel (leaders instruct reports; reports reply to leaders).
//  2. The executive layer (organizations.owner_role_id and ceo_role_id) may
//     address any role.
//  3. Any role may escalate to the owner — except the executive observer,
//     which organization.yaml declares with may_reply_to_owner: false. That
//     exception is the canonical data point proving escalation is otherwise
//     the default.
//
// Nonexistent roles may not message.
func MayMessage(snap Snapshot, from, to string) bool {
	roles := snap.RoleIndex()
	fromRole, fromOK := roles[from]
	if !fromOK {
		return false
	}
	if _, toOK := roles[to]; !toOK {
		return false
	}
	owner := snap.Organization.OwnerRoleID
	if fromRole.RuntimeKind == RuntimeKindReadOnlyObserver && to == owner {
		return false
	}
	if reportingEdge(snap, from, to) || reportingEdge(snap, to, from) {
		return true
	}
	if from == owner || from == snap.Organization.CEORoleID {
		return true
	}
	if to == owner {
		return true
	}
	return false
}

// ClosureViolationCode mirrors the canonical validator's error codes
// (internal/organization/registry/validation.go's validateReportingGraph),
// re-derived independently so divergence records speak the same language.
const (
	ClosureTargetMissing = "reporting.target_missing"
	ClosureSelfReference = "reporting.self_reference"
	ClosureCycle         = "reporting.cycle"
	ClosureSourceMissing = "reporting.source_missing"
)

// ClosureViolation is one dependency_closed violation found in the live
// reporting graph.
type ClosureViolation struct {
	Code   string
	RoleID string
	Detail string
}

// CheckDependencyClosed re-derives dependency_closed: the reports_to graph of
// the live database state must be closed — every line's source and target
// resolve to a non-retired role, no self-references, no cycles. The canon
// validates exactly this property statically at sync time
// (validateReportingGraph); nothing re-checks it against the live database at
// runtime, which is the gap this fact covers.
func CheckDependencyClosed(snap Snapshot) []ClosureViolation {
	roles := snap.RoleIndex()
	var violations []ClosureViolation
	graph := make(map[string][]string)
	for _, line := range snap.ReportingLines {
		if _, ok := roles[line.RoleID]; !ok {
			violations = append(violations, ClosureViolation{
				Code:   ClosureSourceMissing,
				RoleID: line.RoleID,
				Detail: fmt.Sprintf("reporting line source %q does not resolve to a non-retired role", line.RoleID),
			})
		}
		if _, ok := roles[line.ReportsToRoleID]; !ok {
			violations = append(violations, ClosureViolation{
				Code:   ClosureTargetMissing,
				RoleID: line.RoleID,
				Detail: fmt.Sprintf("role %q reports to unknown role %q", line.RoleID, line.ReportsToRoleID),
			})
		}
		if line.RoleID == line.ReportsToRoleID {
			violations = append(violations, ClosureViolation{
				Code:   ClosureSelfReference,
				RoleID: line.RoleID,
				Detail: fmt.Sprintf("role %q cannot report to itself", line.RoleID),
			})
		}
		graph[line.RoleID] = append(graph[line.RoleID], line.ReportsToRoleID)
	}
	violations = append(violations, detectCycles(graph)...)
	return violations
}

// detectCycles runs the same three-color DFS the canonical validator uses,
// deterministically ordered by node ID.
func detectCycles(graph map[string][]string) []ClosureViolation {
	var violations []ClosureViolation
	state := make(map[string]uint8)
	var visit func(string, []string)
	visit = func(node string, stack []string) {
		if state[node] == 2 {
			return
		}
		if state[node] == 1 {
			cycle := append(append([]string{}, stack...), node)
			violations = append(violations, ClosureViolation{
				Code:   ClosureCycle,
				RoleID: node,
				Detail: "reports_to graph contains a cycle: " + strings.Join(cycle, " -> "),
			})
			return
		}
		state[node] = 1
		nexts := append([]string{}, graph[node]...)
		sort.Strings(nexts)
		for _, next := range nexts {
			visit(next, append(stack, node))
		}
		state[node] = 2
	}
	keys := make([]string, 0, len(graph))
	for node := range graph {
		keys = append(keys, node)
	}
	sort.Strings(keys)
	for _, node := range keys {
		visit(node, nil)
	}
	return violations
}

// CapabilityVerdict is the shadow's re-derived policy decision for one
// (role, capability) pair.
type CapabilityVerdict struct {
	Effect     string // VerdictAllow | VerdictDeny | VerdictApprovalRequired
	ReasonCode string
}

// EvaluateCapability re-derives capability_granted for one (role, capability)
// pair, reimplementing internal/authorization's Authorizer.evaluate policy
// core from the canonical matrix and the shadow snapshot:
//
// unknown capability → role lookup (missing/retired → role_not_found, since
// the snapshot excludes retired rows) → disabled → not executable (owner
// exempt) → unknown authority class → global hard deny → authority hard deny
// → approval required → granted (wildcard or explicit) → default deny.
//
// Organization, revision and matrix-hash checks are run-level preconditions
// in the shadow service, not per-pair branches: the comparator pins them once
// per run, exactly like the real engine pins them per request.
func EvaluateCapability(matrix MatrixIndex, snap Snapshot, roleID, capabilityID string) CapabilityVerdict {
	capability, ok := matrix.Capabilities[capabilityID]
	if !ok {
		return CapabilityVerdict{Effect: VerdictDeny, ReasonCode: ReasonUnknownCapability}
	}
	role, ok := snap.RoleIndex()[roleID]
	if !ok {
		return CapabilityVerdict{Effect: VerdictDeny, ReasonCode: ReasonRoleNotFound}
	}
	if !role.Enabled {
		return CapabilityVerdict{Effect: VerdictDeny, ReasonCode: ReasonRoleDisabled}
	}
	if !role.Executable && role.ID != snap.Organization.OwnerRoleID {
		return CapabilityVerdict{Effect: VerdictDeny, ReasonCode: ReasonRoleNotExecutable}
	}
	authority := strings.TrimSpace(role.AuthorityClass)
	if _, known := matrix.Authorities[authority]; !known {
		return CapabilityVerdict{Effect: VerdictDeny, ReasonCode: ReasonUnknownAuthorityClass}
	}
	if containsString(matrix.HardDenies["*"], capabilityID) || containsString(matrix.HardDenies[authority], capabilityID) {
		return CapabilityVerdict{Effect: VerdictDeny, ReasonCode: ReasonHardDeny}
	}
	if capability.Approval != "" {
		return CapabilityVerdict{Effect: VerdictApprovalRequired, ReasonCode: ReasonApprovalMissing}
	}
	if containsString(matrix.Grants[authority], "*") || containsString(matrix.Grants[authority], capabilityID) {
		return CapabilityVerdict{Effect: VerdictAllow, ReasonCode: ReasonAllowedByGrant}
	}
	return CapabilityVerdict{Effect: VerdictDeny, ReasonCode: ReasonGrantMissing}
}
