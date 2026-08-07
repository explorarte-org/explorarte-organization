package shadowverifier

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Service orchestrates shadow verification runs. It observes and compares
// only (EnforcementObserveAndCompareOnly): it returns reports and writes to
// shadowverifier's own tables, and it never blocks, gates or mutates anything
// in any other branch.
type Service struct {
	facts          FactReader
	traffic        TrafficReader
	writer         FindingWriter
	ground         GroundTruth
	matrix         MatrixIndex
	leaderMap      LeaderWorkerMapFact
	organizationID string
	sampleRate     int
	maxComparisons int
	replayLimit    int
	clock          Clock
}

// NewService validates the composition. sampleRate <= 1 compares every pair;
// sampleRate N compares a deterministic 1-in-N hash sample. maxComparisons
// bounds capability pair comparisons (0 = default 5000). replayLimit bounds
// recorded-traffic replay (0 = default 1000).
func NewService(facts FactReader, traffic TrafficReader, writer FindingWriter, ground GroundTruth, matrix MatrixIndex, leaderMap LeaderWorkerMapFact, organizationID string, sampleRate, maxComparisons, replayLimit int, clock Clock) (*Service, error) {
	if facts == nil || traffic == nil || writer == nil || ground == nil {
		return nil, errors.New("shadowverifier: all ports are required")
	}
	if strings.TrimSpace(organizationID) == "" {
		return nil, errors.New("shadowverifier: organization ID is required")
	}
	if matrix.DefaultPolicy != "deny" {
		return nil, errors.New("shadowverifier: capability matrix must use default deny")
	}
	if sampleRate < 0 || maxComparisons < 0 || replayLimit < 0 {
		return nil, ErrInvalidRequest
	}
	if maxComparisons == 0 {
		maxComparisons = 5000
	}
	if replayLimit == 0 {
		replayLimit = 1000
	}
	if clock == nil {
		clock = ClockFunc(time.Now)
	}
	return &Service{
		facts:          facts,
		traffic:        traffic,
		writer:         writer,
		ground:         ground,
		matrix:         matrix,
		leaderMap:      leaderMap,
		organizationID: strings.TrimSpace(organizationID),
		sampleRate:     sampleRate,
		maxComparisons: maxComparisons,
		replayLimit:    replayLimit,
		clock:          clock,
	}, nil
}

// OrganizationID returns the organization this verifier is scoped to.
func (s *Service) OrganizationID() string { return s.organizationID }

// VerifyExhaustive compares the shadow against the real engines over the
// whole finite fact space of the current organization state: every role,
// every unit, every sampled role×capability pair, plus the closure and
// canon-cross-check derivations.
func (s *Service) VerifyExhaustive(ctx context.Context) (RunReport, error) {
	snap, err := s.loadAndCheckSnapshot(ctx)
	if err != nil {
		return RunReport{}, err
	}
	runID, err := s.writer.StartRun(ctx, RunRecord{
		OrganizationID: s.organizationID,
		Mode:           RunModeExhaustive,
		RevisionID:     snap.RevisionID,
		MatrixHash:     s.matrix.Hash,
		StartedAt:      s.clock.Now(),
		Status:         "running",
	})
	if err != nil {
		return RunReport{}, err
	}
	tally := &tally{clock: s.clock}

	s.checkExistenceFacts(ctx, snap, tally)
	s.checkLeaderFacts(ctx, snap, tally)
	s.checkCapabilityFacts(ctx, snap, tally)
	s.checkDelegateCanonCrossCheck(snap, tally)
	s.checkMessageObserverClause(snap, tally)
	s.checkDependencyClosed(ctx, snap, tally)

	return s.finishRun(ctx, runID, RunModeExhaustive, tally, "completed")
}

// ReplayRecorded re-derives capability_granted for recorded
// authorization_requests traffic and compares it against the real engine at
// the current revision. Requests recorded under an older revision or matrix
// hash are tallied uncomparable (stale), never divergent: comparing against a
// moved policy would measure drift, not parity.
func (s *Service) ReplayRecorded(ctx context.Context) (RunReport, error) {
	snap, err := s.loadAndCheckSnapshot(ctx)
	if err != nil {
		return RunReport{}, err
	}
	runID, err := s.writer.StartRun(ctx, RunRecord{
		OrganizationID: s.organizationID,
		Mode:           RunModeReplay,
		RevisionID:     snap.RevisionID,
		MatrixHash:     s.matrix.Hash,
		StartedAt:      s.clock.Now(),
		Status:         "running",
	})
	if err != nil {
		return RunReport{}, err
	}
	tally := &tally{clock: s.clock}

	cases, err := s.traffic.RecordedRequests(ctx, s.replayLimit)
	if err != nil {
		s.finishRun(ctx, runID, RunModeReplay, tally, "failed")
		return RunReport{}, err
	}
	for _, recorded := range cases {
		result := CheckResult{
			Fact:          FactCapabilityGranted,
			SubjectRoleID: recorded.RequesterRoleID,
			CapabilityID:  recorded.CapabilityID,
		}
		stale := recorded.OrganizationRevisionID != snap.RevisionID || recorded.CapabilityMatrixHash != s.matrix.Hash
		if stale {
			result.Status = StatusUncomparable
			result.ShadowVerdict = VerdictDeny
			result.Detail = fmt.Sprintf("recorded request %d is stale (recorded revision %d / current %d); replay skipped", recorded.ID, recorded.OrganizationRevisionID, snap.RevisionID)
			tally.add(result)
			continue
		}
		shadow := EvaluateCapability(s.matrix, snap, recorded.RequesterRoleID, recorded.CapabilityID)
		result.ShadowVerdict = shadow.Effect
		effect, reason, err := s.ground.EvaluateCapability(ctx, recorded.RequesterRoleID, recorded.CapabilityID)
		if err != nil {
			s.finishRun(ctx, runID, RunModeReplay, tally, "failed")
			return RunReport{}, err
		}
		result.GroundVerdict = effect
		result.Status, result.Detail = compareCapabilityVerdicts(shadow, effect, reason)
		if result.Status == StatusParity && effect != VerdictApprovalRequired {
			result.Detail = fmt.Sprintf("recorded request %d existed but the current engine no longer answers approval_required (status %s)", recorded.ID, recorded.Status)
		}
		tally.add(result)
	}

	return s.finishRun(ctx, runID, RunModeReplay, tally, "completed")
}

func (s *Service) loadAndCheckSnapshot(ctx context.Context) (Snapshot, error) {
	snap, err := s.facts.LoadSnapshot(ctx)
	if err != nil {
		return Snapshot{}, fmt.Errorf("%w: %v", ErrSnapshotUnavailable, err)
	}
	if snap.Organization.ID != s.organizationID {
		return Snapshot{}, fmt.Errorf("%w: snapshot organization %q does not match %q", ErrSnapshotUnavailable, snap.Organization.ID, s.organizationID)
	}
	if snap.Organization.Retired {
		return Snapshot{}, ErrOrganizationRetired
	}
	if snap.RevisionID <= 0 {
		return Snapshot{}, fmt.Errorf("%w: organization has no applied revision", ErrSnapshotUnavailable)
	}
	return snap, nil
}

// checkExistenceFacts compares role_exists and department_exists for every
// current role/unit, plus one well-formed negative probe each: the shadow and
// the real engine must agree that a nonexistent ID does not exist.
func (s *Service) checkExistenceFacts(ctx context.Context, snap Snapshot, t *tally) {
	roleIDs := make([]string, 0, len(snap.Roles))
	for _, role := range snap.Roles {
		roleIDs = append(roleIDs, role.ID)
	}
	sort.Strings(roleIDs)
	for _, roleID := range roleIDs {
		ground, err := s.ground.RoleExists(ctx, roleID)
		if err != nil {
			t.abort(err)
			return
		}
		t.add(compareBool(FactRoleExists, roleID, "", true, ground))
	}
	probeRole := "shadowverifier/nonexistent_probe"
	ground, err := s.ground.RoleExists(ctx, probeRole)
	if err != nil {
		t.abort(err)
		return
	}
	t.add(compareBool(FactRoleExists, probeRole, "", false, ground))

	unitIDs := make([]string, 0, len(snap.Units))
	for _, unit := range snap.Units {
		unitIDs = append(unitIDs, unit.ID)
	}
	sort.Strings(unitIDs)
	for _, unitID := range unitIDs {
		groundUnit, err := s.ground.DepartmentExists(ctx, unitID)
		if err != nil {
			t.abort(err)
			return
		}
		t.add(compareBoolUnit(FactDepartmentExists, unitID, true, groundUnit))
	}
	probeUnit := "shadowverifier_nonexistent_probe"
	groundUnit, err := s.ground.DepartmentExists(ctx, probeUnit)
	if err != nil {
		t.abort(err)
		return
	}
	t.add(compareBoolUnit(FactDepartmentExists, probeUnit, false, groundUnit))
}

// checkLeaderFacts compares leader_of per unit. Units with an anomalous
// leader count (zero for a non-leaderless unit, or more than one canonical
// leader) are recorded as counterexamples and marked uncomparable: the real
// GetLeader uses QueryRow without ORDER BY, so its answer there is
// nondeterministic and comparing against it would be noise, not signal.
func (s *Service) checkLeaderFacts(ctx context.Context, snap Snapshot, t *tally) {
	unitIDs := make([]string, 0, len(snap.Units))
	for _, unit := range snap.Units {
		unitIDs = append(unitIDs, unit.ID)
	}
	sort.Strings(unitIDs)
	for _, unitID := range unitIDs {
		shadowLeader, shadowFound, anomaly := LeaderOf(snap, unitID)
		if anomaly != "" {
			t.add(CheckResult{
				Fact:          FactLeaderOf,
				SubjectUnitID: unitID,
				ShadowVerdict: VerdictFalse,
				Status:        StatusCounterexample,
				Detail:        anomaly,
			})
			t.add(CheckResult{
				Fact:          FactLeaderOf,
				SubjectUnitID: unitID,
				ShadowVerdict: VerdictFalse,
				Status:        StatusUncomparable,
				Detail:        "leader_of comparison skipped: " + anomaly,
			})
			continue
		}
		groundLeader, groundFound, err := s.ground.LeaderOf(ctx, unitID)
		if err != nil {
			t.abort(err)
			return
		}
		result := CheckResult{Fact: FactLeaderOf, SubjectUnitID: unitID, TargetRoleID: shadowLeader}
		switch {
		case shadowFound && groundFound && shadowLeader == groundLeader:
			result.ShadowVerdict, result.GroundVerdict, result.Status = VerdictTrue, VerdictTrue, StatusParity
		case !shadowFound && !groundFound:
			result.ShadowVerdict, result.GroundVerdict, result.Status = VerdictFalse, VerdictFalse, StatusParity
		default:
			result.ShadowVerdict = verdictFromBool(shadowFound)
			result.GroundVerdict = verdictFromBool(groundFound)
			result.Status = StatusDivergence
			result.Detail = fmt.Sprintf("shadow leader=%q found=%t, ground leader=%q found=%t", shadowLeader, shadowFound, groundLeader, groundFound)
		}
		t.add(result)
	}
}

// checkCapabilityFacts compares capability_granted over the role×capability
// product space, subject to the deterministic sample rate and the comparison
// cap. If the matrix file the shadow parsed does not match the hash recorded
// in the current revision, every comparison would measure a moved policy: one
// uncomparable record explains the run instead of fabricating divergences.
func (s *Service) checkCapabilityFacts(ctx context.Context, snap Snapshot, t *tally) {
	if snap.MatrixHash != s.matrix.Hash {
		t.add(CheckResult{
			Fact:          FactCapabilityGranted,
			ShadowVerdict: VerdictDeny,
			Status:        StatusUncomparable,
			Detail: fmt.Sprintf("policy drift: capability-matrix.yaml file hash %s does not match revision %d recorded hash %s; pair comparisons skipped",
				s.matrix.Hash, snap.RevisionID, snap.MatrixHash),
		})
		return
	}
	capabilityIDs := s.matrix.CapabilityIDs()
	roles := append([]RoleFact{}, snap.Roles...)
	sort.Slice(roles, func(i, j int) bool { return roles[i].ID < roles[j].ID })
	compared := 0
	for _, role := range roles {
		for _, capabilityID := range capabilityIDs {
			if compared >= s.maxComparisons {
				return
			}
			if !sampled(s.sampleRate, role.ID, capabilityID) {
				continue
			}
			compared++
			shadow := EvaluateCapability(s.matrix, snap, role.ID, capabilityID)
			effect, reason, err := s.ground.EvaluateCapability(ctx, role.ID, capabilityID)
			if err != nil {
				t.abort(err)
				return
			}
			status, detail := compareCapabilityVerdicts(shadow, effect, reason)
			t.add(CheckResult{
				Fact:          FactCapabilityGranted,
				SubjectRoleID: role.ID,
				CapabilityID:  capabilityID,
				ShadowVerdict: shadow.Effect,
				GroundVerdict: effect,
				Status:        status,
				Detail:        detail,
			})
		}
	}
}

// compareCapabilityVerdicts requires effect and reason code to agree. An
// effect match with a differing reason is still a divergence: Phase 1 exists
// to surface exactly such structural disagreements before this verifier is
// ever trusted with authority.
func compareCapabilityVerdicts(shadow CapabilityVerdict, groundEffect, groundReason string) (CheckStatus, string) {
	if shadow.Effect != groundEffect {
		return StatusDivergence, fmt.Sprintf("shadow %s/%s vs ground %s/%s", shadow.Effect, shadow.ReasonCode, groundEffect, groundReason)
	}
	if shadow.ReasonCode != groundReason {
		return StatusDivergence, fmt.Sprintf("effect %s agrees but reasons differ: shadow %s vs ground %s", shadow.Effect, shadow.ReasonCode, groundReason)
	}
	return StatusParity, ""
}

// checkDelegateCanonCrossCheck compares the shadow's may_delegate derivation
// (live reports_to edges) against leader-worker-map.yaml, the canonical
// delegation statement. Disagreements are counterexamples: there is no Go
// runtime engine deciding may_delegate, so the canon is the only independent
// reference. Observers are checked against organization.yaml's explicit
// may_delegate: false clause.
func (s *Service) checkDelegateCanonCrossCheck(snap Snapshot, t *tally) {
	roles := snap.RoleIndex()
	for _, department := range s.leaderMap.Departments {
		for _, worker := range department.Workers {
			if !MayDelegate(snap, department.Leader, worker) {
				t.add(CheckResult{
					Fact:          FactMayDelegate,
					SubjectRoleID: department.Leader,
					TargetRoleID:  worker,
					ShadowVerdict: VerdictFalse,
					Status:        StatusCounterexample,
					Detail: fmt.Sprintf("leader-worker-map lists %q as worker of %q in department %q but the live reports_to graph has no such edge",
						worker, department.Leader, department.ID),
				})
				continue
			}
			t.add(CheckResult{
				Fact:          FactMayDelegate,
				SubjectRoleID: department.Leader,
				TargetRoleID:  worker,
				ShadowVerdict: VerdictTrue,
				Status:        StatusParity,
			})
		}
	}
	// Every live edge inside a canon-listed department must appear in the
	// canon: an edge the canon does not know about is drift worth recording.
	listed := make(map[string]map[string]struct{})
	leaders := make(map[string]string)
	for _, department := range s.leaderMap.Departments {
		workers := make(map[string]struct{}, len(department.Workers))
		for _, worker := range department.Workers {
			workers[worker] = struct{}{}
		}
		listed[department.ID] = workers
		leaders[department.Leader] = department.ID
	}
	for _, line := range snap.ReportingLines {
		departmentID, leaderIsKnown := leaders[line.ReportsToRoleID]
		if !leaderIsKnown {
			continue
		}
		worker, workerKnown := roles[line.RoleID]
		if !workerKnown || worker.UnitID != departmentID {
			continue
		}
		if _, ok := listed[departmentID][line.RoleID]; !ok {
			t.add(CheckResult{
				Fact:          FactMayDelegate,
				SubjectRoleID: line.ReportsToRoleID,
				TargetRoleID:  line.RoleID,
				ShadowVerdict: VerdictTrue,
				Status:        StatusCounterexample,
				Detail: fmt.Sprintf("live reports_to edge %q -> %q in department %q is not listed in leader-worker-map.yaml",
					line.RoleID, line.ReportsToRoleID, departmentID),
			})
		}
	}
	// organization.yaml: the observer may_delegate: false. Any live edge that
	// would let an observer delegate contradicts the canon.
	for _, line := range snap.ReportingLines {
		target, ok := roles[line.ReportsToRoleID]
		if !ok || target.RuntimeKind != RuntimeKindReadOnlyObserver {
			continue
		}
		t.add(CheckResult{
			Fact:          FactMayDelegate,
			SubjectRoleID: line.ReportsToRoleID,
			TargetRoleID:  line.RoleID,
			ShadowVerdict: VerdictFalse,
			Status:        StatusCounterexample,
			Detail: fmt.Sprintf("observer %q has %q reporting to it, but organization.yaml declares observer may_delegate: false",
				line.ReportsToRoleID, line.RoleID),
		})
	}
}

// checkMessageObserverClause is may_message's only Phase 1 structural check:
// the observer must not be able to message the owner
// (may_reply_to_owner: false). If a live reporting edge connects them, the
// channel rule and the observer clause would conflict; the derivation resolves
// it in favor of the clause, and the edge itself is recorded as a
// counterexample for investigation.
func (s *Service) checkMessageObserverClause(snap Snapshot, t *tally) {
	owner := snap.Organization.OwnerRoleID
	for _, role := range snap.Roles {
		if role.RuntimeKind != RuntimeKindReadOnlyObserver {
			continue
		}
		if reportingEdge(snap, role.ID, owner) || reportingEdge(snap, owner, role.ID) {
			t.add(CheckResult{
				Fact:          FactMayMessage,
				SubjectRoleID: role.ID,
				TargetRoleID:  owner,
				ShadowVerdict: VerdictFalse,
				Status:        StatusCounterexample,
				Detail:        fmt.Sprintf("observer %q has a reporting edge with owner %q despite may_reply_to_owner: false", role.ID, owner),
			})
			continue
		}
		t.add(CheckResult{
			Fact:          FactMayMessage,
			SubjectRoleID: role.ID,
			TargetRoleID:  owner,
			ShadowVerdict: VerdictFalse,
			Status:        StatusParity,
		})
	}
}

// checkDependencyClosed re-derives reports_to closure from the live database
// and compares the property against the canonical loader's static validation.
// Every violation found live is a counterexample (the static check already
// passed at sync time, so a live violation is runtime drift); a disagreement
// between the two sources on the property itself is recorded too.
func (s *Service) checkDependencyClosed(ctx context.Context, snap Snapshot, t *tally) {
	violations := CheckDependencyClosed(snap)
	for _, violation := range violations {
		t.add(CheckResult{
			Fact:          FactDependencyClosed,
			SubjectRoleID: violation.RoleID,
			ShadowVerdict: VerdictViolated,
			Status:        StatusCounterexample,
			Detail:        violation.Code + ": " + violation.Detail,
		})
	}
	shadowClosed := len(violations) == 0
	canonClosed, issues, err := s.ground.CanonicalReportingClosed(ctx)
	if err != nil {
		t.abort(err)
		return
	}
	result := CheckResult{Fact: FactDependencyClosed}
	if shadowClosed {
		result.ShadowVerdict = VerdictClosed
	} else {
		result.ShadowVerdict = VerdictViolated
	}
	if canonClosed {
		result.GroundVerdict = VerdictClosed
	} else {
		result.GroundVerdict = VerdictViolated
	}
	switch {
	case shadowClosed == canonClosed:
		result.Status = StatusParity
	default:
		result.Status = StatusCounterexample
		result.Detail = fmt.Sprintf("canon/static validation and live database disagree on reports_to closure (canon issues: %s)", strings.Join(issues, ", "))
	}
	t.add(result)
}

func (s *Service) finishRun(ctx context.Context, runID int64, mode RunMode, t *tally, status string) (RunReport, error) {
	if t.aborted != nil {
		status = "failed"
	}
	if err := s.writer.FinishRun(ctx, runID, t.summary, status); err != nil {
		return RunReport{}, err
	}
	if len(t.findings) > 0 {
		if err := s.writer.RecordFindings(ctx, runID, t.findings); err != nil {
			return RunReport{}, err
		}
	}
	report := RunReport{RunID: runID, Mode: mode, Summary: t.summary, Findings: t.findings, Uncomparable: t.uncomparable}
	return report, t.aborted
}

// tally accumulates one run's comparison accounting in memory.
type tally struct {
	clock        Clock
	summary      RunSummary
	findings     []Finding
	uncomparable []CheckResult
	aborted      error
}

func (t *tally) add(result CheckResult) {
	if t.aborted != nil {
		return
	}
	t.summary.ChecksTotal++
	switch result.Status {
	case StatusParity:
		t.summary.ChecksParity++
	case StatusDivergence:
		t.summary.ChecksDivergent++
		t.findings = append(t.findings, FindingFrom(result, t.clock.Now()))
	case StatusCounterexample:
		t.summary.ChecksCounterexample++
		t.findings = append(t.findings, FindingFrom(result, t.clock.Now()))
	case StatusUncomparable:
		t.summary.ChecksUncomparable++
		t.uncomparable = append(t.uncomparable, result)
	}
}

func (t *tally) abort(err error) {
	if t.aborted == nil {
		t.aborted = err
	}
}

func compareBool(fact FactID, subjectRole, subjectUnit string, shadow, ground bool) CheckResult {
	result := CheckResult{Fact: fact, SubjectRoleID: subjectRole, SubjectUnitID: subjectUnit}
	result.ShadowVerdict = verdictFromBool(shadow)
	result.GroundVerdict = verdictFromBool(ground)
	if shadow == ground {
		result.Status = StatusParity
		return result
	}
	result.Status = StatusDivergence
	result.Detail = fmt.Sprintf("shadow %t vs ground %t", shadow, ground)
	return result
}

func compareBoolUnit(fact FactID, subjectUnit string, shadow, ground bool) CheckResult {
	return compareBool(fact, "", subjectUnit, shadow, ground)
}

func verdictFromBool(value bool) string {
	if value {
		return VerdictTrue
	}
	return VerdictFalse
}

// sampled decides whether one (role, capability) pair enters the comparison
// under the 1-in-N deterministic sample rate. The hash is over the canonical
// pair encoding, so the sample is stable across runs and processes.
func sampled(sampleRate int, roleID, capabilityID string) bool {
	if sampleRate <= 1 {
		return true
	}
	sum := sha256.Sum256([]byte(roleID + "\x00" + capabilityID))
	return binary.BigEndian.Uint64(sum[:8])%uint64(sampleRate) == 0
}
