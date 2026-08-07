// Package shadowverifier implements Phase 1 of
// docs/canonical/reasoning-assurance.yaml (organization_rules_shadow_mode):
// an independent re-derivation of seven organizational facts, compared against
// what the real Go engines already decide, with every disagreement recorded
// durably. Enforcement is observe_and_compare_only — this package never blocks
// anything, never mutates any other branch's state, and never becomes an
// authority: it only observes, compares, and leaves a durable record of any
// disagreement.
//
// Independence requirement: the shadow derivations in this package never call
// internal/organization/registry or internal/authorization code — calling the
// same function would make the comparison a meaningless tautology. The shadow
// re-reads the underlying Postgres tables directly (via its own postgres
// adapter, the same cross-branch pattern internal/completion and
// internal/decisiongraphtrace established) and re-parses capability-matrix.yaml
// with its own types. The comparison harness that obtains the real engines'
// verdicts lives at the CLI composition root, behind the GroundTruth port.
package shadowverifier

import "time"

// Enforcement mirrors reasoning-assurance.yaml Phase 1's enforcement field
// verbatim. It is a load-bearing constant, not documentation: Phase 1 has no
// authority, and any future phase that wants to block must go through the
// canonical document first, not through a code change here.
type Enforcement string

const EnforcementObserveAndCompareOnly Enforcement = "observe_and_compare_only"

// FactID enumerates the seven facts reasoning-assurance.yaml Phase 1 scopes
// exactly (organization_rules_shadow_mode.scope).
type FactID string

const (
	FactRoleExists        FactID = "role_exists"
	FactDepartmentExists  FactID = "department_exists"
	FactLeaderOf          FactID = "leader_of"
	FactMayMessage        FactID = "may_message"
	FactMayDelegate       FactID = "may_delegate"
	FactCapabilityGranted FactID = "capability_granted"
	FactDependencyClosed  FactID = "dependency_closed"
)

func (f FactID) Valid() bool {
	switch f {
	case FactRoleExists, FactDepartmentExists, FactLeaderOf, FactMayMessage, FactMayDelegate, FactCapabilityGranted, FactDependencyClosed:
		return true
	default:
		return false
	}
}

// FindingKind distinguishes the two durable records Phase 1's exit criteria
// require. A divergence is a shadow-vs-real-engine disagreement on a fact the
// real engine decides. A counterexample is a concrete case refuting a property
// the organization state is supposed to satisfy (reporting-graph closure,
// canonical leader counts, canon-to-database agreement) — these facts have no
// Go runtime engine to compare against, so they are reported instead of
// compared (exit_criteria.counterexample_reporting).
type FindingKind string

const (
	KindDivergence     FindingKind = "divergence"
	KindCounterexample FindingKind = "counterexample"
)

func (k FindingKind) Valid() bool {
	switch k {
	case KindDivergence, KindCounterexample:
		return true
	default:
		return false
	}
}

// CheckStatus classifies one comparison instance.
type CheckStatus string

const (
	// StatusParity: shadow and ground truth agree.
	StatusParity CheckStatus = "parity"
	// StatusDivergence: shadow and the real engine disagree on a fact the
	// real engine decides.
	StatusDivergence CheckStatus = "divergence"
	// StatusCounterexample: the shadow found a concrete violation of an
	// invariant (closure, leader count, canon/database agreement).
	StatusCounterexample CheckStatus = "counterexample"
	// StatusUncomparable: the comparison cannot be made soundly (stale
	// recorded traffic, policy drift between file and revision, anomalous
	// state with a nondeterministic ground truth). Absence of proof is not
	// proof of falsity — uncomparable is never collapsed into divergence.
	StatusUncomparable CheckStatus = "uncomparable"
)

// Shadow verdict vocabulary. Existence and relation facts use true/false;
// capability_granted mirrors internal/authorization's effect strings exactly
// (compared as strings, the same "mirror the values without importing the
// package" pattern internal/completion uses for requirement types).
const (
	VerdictTrue  = "true"
	VerdictFalse = "false"

	VerdictAllow            = "allow"
	VerdictDeny             = "deny"
	VerdictApprovalRequired = "approval_required"

	VerdictClosed   = "closed"
	VerdictViolated = "violated"
)

// Shadow reason codes for capability_granted, mirroring
// internal/authorization's ReasonCode string values for the policy-core paths
// the shadow re-derives. Organization/revision/matrix-hash checks are
// run-level preconditions here, not per-pair reasons.
const (
	ReasonAllowedByGrant        = "allowed_by_grant"
	ReasonApprovalMissing       = "approval_missing"
	ReasonHardDeny              = "hard_deny"
	ReasonGrantMissing          = "grant_missing"
	ReasonUnknownCapability     = "unknown_capability"
	ReasonRoleNotFound          = "role_not_found"
	ReasonRoleDisabled          = "role_disabled"
	ReasonRoleNotExecutable     = "role_not_executable"
	ReasonUnknownAuthorityClass = "unknown_authority_class"
)

// RuntimeKindReadOnlyObserver mirrors organization.yaml's executive observer
// (runtime_kind: concurrent_read_only_observer), which the canon explicitly
// restricts: may_reply_to_owner: false, may_delegate: false.
const RuntimeKindReadOnlyObserver = "concurrent_read_only_observer"

// OrganizationFact is the shadow's own read-only projection of the
// organizations row (never registry.Organization).
type OrganizationFact struct {
	ID              string
	OwnerRoleID     string
	CEORoleID       string
	CurrentRevision int64
	Retired         bool
}

// UnitFact projects one organizational_units row. Note the table name:
// organizational_units, not organization_units.
type UnitFact struct {
	ID           string
	Kind         string
	Operational  bool
	Leaderless   bool
	LeaderRoleID string // empty when the column is NULL
	Retired      bool
}

// RoleFact projects one organization_roles row. Retired roles are excluded by
// the adapter query, mirroring registry.GetRole's retired_at IS NULL filter:
// a retired role "does not exist" for fact purposes.
type RoleFact struct {
	ID              string
	UnitID          string
	AuthorityClass  string
	RuntimeKind     string
	CanonicalLeader bool
	Enabled         bool
	Executable      bool
}

// ReportingLineFact projects one organization_reporting_lines row for the
// organization's current revision.
type ReportingLineFact struct {
	RoleID          string
	ReportsToRoleID string
	Relationship    string
}

// Snapshot is the shadow's whole-organization view for one run: one load,
// every derivation afterwards is pure in-memory work.
type Snapshot struct {
	Organization OrganizationFact
	RevisionID   int64
	// MatrixHash is the semantic hash of capability-matrix.yaml recorded in
	// the current revision's document hashes; the run compares it against the
	// hash of the file the shadow parsed to detect policy drift.
	MatrixHash     string
	Units          []UnitFact
	Roles          []RoleFact
	ReportingLines []ReportingLineFact
}

// RoleIndex returns the snapshot's roles keyed by ID for derivations.
func (s Snapshot) RoleIndex() map[string]RoleFact {
	index := make(map[string]RoleFact, len(s.Roles))
	for _, role := range s.Roles {
		index[role.ID] = role
	}
	return index
}

// UnitIndex returns the snapshot's units keyed by ID for derivations.
func (s Snapshot) UnitIndex() map[string]UnitFact {
	index := make(map[string]UnitFact, len(s.Units))
	for _, unit := range s.Units {
		index[unit.ID] = unit
	}
	return index
}

// RecordedRequest is one authorization_requests row — real recorded traffic
// with inputs and outcome, used by replay mode.
type RecordedRequest struct {
	ID                     int64
	RequesterRoleID        string
	CapabilityID           string
	OrganizationRevisionID int64
	CapabilityMatrixHash   string
	Status                 string
}

// RunMode distinguishes how the compared corpus was assembled.
type RunMode string

const (
	// RunModeExhaustive: the whole finite fact space of the current state
	// (every role, every unit, every role×capability pair).
	RunModeExhaustive RunMode = "exhaustive"
	// RunModeReplay: recorded authorization_requests traffic.
	RunModeReplay RunMode = "replay"
)

func (m RunMode) Valid() bool {
	switch m {
	case RunModeExhaustive, RunModeReplay:
		return true
	default:
		return false
	}
}

// RunRecord is the durable header of one verification run.
type RunRecord struct {
	ID             int64
	OrganizationID string
	Mode           RunMode
	RevisionID     int64
	MatrixHash     string
	StartedAt      time.Time
	FinishedAt     time.Time
	Status         string // running | completed | failed
}

// RunSummary tallies one run's comparisons.
type RunSummary struct {
	ChecksTotal          int
	ChecksParity         int
	ChecksDivergent      int
	ChecksCounterexample int
	ChecksUncomparable   int
}

// Finding is one durable divergence or counterexample.
type Finding struct {
	Fact          FactID
	Kind          FindingKind
	SubjectRoleID string
	SubjectUnitID string
	CapabilityID  string
	TargetRoleID  string
	ShadowVerdict string
	GroundVerdict string
	Detail        string
	DetectedAt    time.Time
}

// CheckResult is one in-memory comparison instance. Only divergence and
// counterexample results become durable findings; parity and uncomparable are
// tallied (and returned for observability).
type CheckResult struct {
	Fact          FactID
	SubjectRoleID string
	SubjectUnitID string
	CapabilityID  string
	TargetRoleID  string
	ShadowVerdict string
	GroundVerdict string
	Status        CheckStatus
	Detail        string
}

// FindingFrom converts a divergence/counterexample check result into its
// durable form.
func FindingFrom(result CheckResult, at time.Time) Finding {
	return Finding{
		Fact:          result.Fact,
		Kind:          FindingKind(result.Status),
		SubjectRoleID: result.SubjectRoleID,
		SubjectUnitID: result.SubjectUnitID,
		CapabilityID:  result.CapabilityID,
		TargetRoleID:  result.TargetRoleID,
		ShadowVerdict: result.ShadowVerdict,
		GroundVerdict: result.GroundVerdict,
		Detail:        result.Detail,
		DetectedAt:    at,
	}
}

// RunReport is what one verification run returns to the caller.
type RunReport struct {
	RunID        int64
	Mode         RunMode
	Summary      RunSummary
	Findings     []Finding
	Uncomparable []CheckResult
}

// Divergences reports how many durable disagreements the run found.
func (r RunReport) Divergences() int {
	return r.Summary.ChecksDivergent + r.Summary.ChecksCounterexample
}
