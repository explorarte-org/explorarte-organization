package shadowverifier

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type fakeFactReader struct {
	snapshot Snapshot
	err      error
}

func (f *fakeFactReader) LoadSnapshot(context.Context) (Snapshot, error) {
	return f.snapshot, f.err
}

type fakeTraffic struct {
	requests []RecordedRequest
	err      error
}

func (f *fakeTraffic) RecordedRequests(context.Context, int) ([]RecordedRequest, error) {
	return f.requests, f.err
}

type fakeWriter struct {
	nextID    int64
	runs      []RunRecord
	summaries map[int64]RunSummary
	statuses  map[int64]string
	findings  map[int64][]Finding
}

func newFakeWriter() *fakeWriter {
	return &fakeWriter{summaries: map[int64]RunSummary{}, statuses: map[int64]string{}, findings: map[int64][]Finding{}}
}

func (f *fakeWriter) StartRun(_ context.Context, run RunRecord) (int64, error) {
	f.nextID++
	run.ID = f.nextID
	f.runs = append(f.runs, run)
	return run.ID, nil
}

func (f *fakeWriter) FinishRun(_ context.Context, runID int64, summary RunSummary, status string) error {
	if _, ok := f.statuses[runID]; !ok {
		if runID > f.nextID {
			return ErrRunNotFound
		}
	}
	f.summaries[runID] = summary
	f.statuses[runID] = status
	return nil
}

func (f *fakeWriter) RecordFindings(_ context.Context, runID int64, findings []Finding) error {
	f.findings[runID] = append(f.findings[runID], findings...)
	return nil
}

func (f *fakeWriter) GetRun(_ context.Context, runID int64) (RunRecord, RunSummary, error) {
	for _, run := range f.runs {
		if run.ID == runID {
			return run, f.summaries[runID], nil
		}
	}
	return RunRecord{}, RunSummary{}, ErrRunNotFound
}

func (f *fakeWriter) ListRuns(context.Context, int) ([]RunRecord, error) { return f.runs, nil }

func (f *fakeWriter) RunFindings(_ context.Context, runID int64) ([]Finding, error) {
	return f.findings[runID], nil
}

// fakeGround answers with the shadow's own derivations over the same
// snapshot by default — a test-only convenience for parity accounting —
// with per-test overrides to inject disagreements.
type fakeGround struct {
	snapshot           Snapshot
	matrix             MatrixIndex
	canonicalClosed    bool
	canonicalIssues    []string
	roleOverride       map[string]bool
	unitOverride       map[string]bool
	leaderOverride     map[string]string
	capabilityOverride map[string]CapabilityVerdict
	err                error
}

func (g *fakeGround) RoleExists(_ context.Context, roleID string) (bool, error) {
	if g.err != nil {
		return false, g.err
	}
	if value, ok := g.roleOverride[roleID]; ok {
		return value, nil
	}
	return RoleExists(g.snapshot, roleID), nil
}

func (g *fakeGround) DepartmentExists(_ context.Context, unitID string) (bool, error) {
	if g.err != nil {
		return false, g.err
	}
	if value, ok := g.unitOverride[unitID]; ok {
		return value, nil
	}
	return DepartmentExists(g.snapshot, unitID), nil
}

func (g *fakeGround) LeaderOf(_ context.Context, unitID string) (string, bool, error) {
	if g.err != nil {
		return "", false, g.err
	}
	if value, ok := g.leaderOverride[unitID]; ok {
		if value == "" {
			return "", false, nil
		}
		return value, true, nil
	}
	leader, found, _ := LeaderOf(g.snapshot, unitID)
	return leader, found, nil
}

func (g *fakeGround) EvaluateCapability(_ context.Context, roleID, capabilityID string) (string, string, error) {
	if g.err != nil {
		return "", "", g.err
	}
	if value, ok := g.capabilityOverride[roleID+"\x00"+capabilityID]; ok {
		return value.Effect, value.ReasonCode, nil
	}
	verdict := EvaluateCapability(g.matrix, g.snapshot, roleID, capabilityID)
	return verdict.Effect, verdict.ReasonCode, nil
}

func (g *fakeGround) CanonicalReportingClosed(context.Context) (bool, []string, error) {
	if g.err != nil {
		return false, nil, g.err
	}
	return g.canonicalClosed, g.canonicalIssues, nil
}

func testServiceParts(t *testing.T) (*Service, *fakeWriter, *fakeGround) {
	t.Helper()
	snap := testSnapshot()
	matrix := testMatrix()
	matrix.Hash = snap.MatrixHash
	ground := &fakeGround{snapshot: snap, matrix: matrix, canonicalClosed: true}
	writer := newFakeWriter()
	service, err := NewService(
		&fakeFactReader{snapshot: snap}, &fakeTraffic{}, writer, ground,
		matrix, LeaderWorkerMapFact{}, "explorarte", 1, 0, 0,
		ClockFunc(func() time.Time { return time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC) }),
	)
	if err != nil {
		t.Fatal(err)
	}
	return service, writer, ground
}

func TestNewServiceValidation(t *testing.T) {
	snap := testSnapshot()
	matrix := testMatrix()
	matrix.Hash = snap.MatrixHash
	reader := &fakeFactReader{snapshot: snap}
	writer := newFakeWriter()
	ground := &fakeGround{snapshot: snap, matrix: matrix}
	if _, err := NewService(nil, nil, nil, nil, matrix, LeaderWorkerMapFact{}, "explorarte", 0, 0, 0, nil); err == nil {
		t.Fatal("nil ports accepted")
	}
	if _, err := NewService(reader, &fakeTraffic{}, writer, ground, matrix, LeaderWorkerMapFact{}, "", 0, 0, 0, nil); err == nil {
		t.Fatal("empty organization accepted")
	}
	if _, err := NewService(reader, &fakeTraffic{}, writer, ground, MatrixIndex{DefaultPolicy: "allow"}, LeaderWorkerMapFact{}, "explorarte", 0, 0, 0, nil); err == nil {
		t.Fatal("non-deny matrix accepted")
	}
	if _, err := NewService(reader, &fakeTraffic{}, writer, ground, matrix, LeaderWorkerMapFact{}, "explorarte", -1, 0, 0, nil); err == nil {
		t.Fatal("negative sample rate accepted")
	}
}

func TestVerifyExhaustiveParityOnConsistentState(t *testing.T) {
	service, writer, _ := testServiceParts(t)
	report, err := service.VerifyExhaustive(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.Divergences() != 0 {
		t.Fatalf("consistent state produced findings: %+v", report.Findings)
	}
	if report.Summary.ChecksTotal == 0 || report.Summary.ChecksParity != report.Summary.ChecksTotal {
		t.Fatalf("summary=%+v", report.Summary)
	}
	if writer.statuses[report.RunID] != "completed" {
		t.Fatalf("run status=%q", writer.statuses[report.RunID])
	}
	if len(writer.findings[report.RunID]) != 0 {
		t.Fatalf("findings persisted: %+v", writer.findings[report.RunID])
	}
}

func TestVerifyRecordsDivergenceWhenGroundDisagrees(t *testing.T) {
	service, writer, ground := testServiceParts(t)
	ground.capabilityOverride = map[string]CapabilityVerdict{
		"ingenieria_ia/code-runner\x00code.commit": {Effect: VerdictDeny, ReasonCode: ReasonGrantMissing},
	}
	report, err := service.VerifyExhaustive(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.ChecksDivergent != 1 {
		t.Fatalf("summary=%+v", report.Summary)
	}
	if len(report.Findings) != 1 {
		t.Fatalf("findings=%+v", report.Findings)
	}
	finding := report.Findings[0]
	if finding.Fact != FactCapabilityGranted || finding.Kind != KindDivergence {
		t.Fatalf("finding=%+v", finding)
	}
	if finding.SubjectRoleID != "ingenieria_ia/code-runner" || finding.CapabilityID != "code.commit" {
		t.Fatalf("finding subject=%+v", finding)
	}
	if finding.ShadowVerdict != VerdictAllow || finding.GroundVerdict != VerdictDeny {
		t.Fatalf("verdicts=%+v", finding)
	}
	if writer.statuses[report.RunID] != "completed" {
		t.Fatalf("run status=%q", writer.statuses[report.RunID])
	}
}

func TestVerifyRecordsReasonDisagreementAsDivergence(t *testing.T) {
	service, _, ground := testServiceParts(t)
	ground.capabilityOverride = map[string]CapabilityVerdict{
		"ingenieria_ia/code-runner\x00code.commit": {Effect: VerdictAllow, ReasonCode: "some_other_reason"},
	}
	report, err := service.VerifyExhaustive(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.ChecksDivergent != 1 || !strings.Contains(report.Findings[0].Detail, "reasons differ") {
		t.Fatalf("report=%+v", report)
	}
}

func TestVerifyRecordsLeaderAnomalyCounterexample(t *testing.T) {
	service, _, ground := testServiceParts(t)
	dup := testSnapshot()
	dup.Roles = append(dup.Roles, RoleFact{ID: "ingenieria_ia/segundo_lider", UnitID: "ingenieria_ia", AuthorityClass: "department_leadership", CanonicalLeader: true, Enabled: true, Executable: true})
	service.facts = &fakeFactReader{snapshot: dup}
	ground.snapshot = dup
	report, err := service.VerifyExhaustive(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var counterexamples []Finding
	for _, finding := range report.Findings {
		if finding.Fact == FactLeaderOf {
			counterexamples = append(counterexamples, finding)
		}
	}
	if len(counterexamples) != 1 || counterexamples[0].Kind != KindCounterexample {
		t.Fatalf("leader findings=%+v", counterexamples)
	}
	if report.Summary.ChecksUncomparable == 0 {
		t.Fatal("anomalous leader comparison must be uncomparable")
	}
}

func TestVerifyUncomparableOnMatrixDrift(t *testing.T) {
	service, _, _ := testServiceParts(t)
	drifted := testSnapshot()
	drifted.MatrixHash = strings.Repeat("b", 64)
	service.facts = &fakeFactReader{snapshot: drifted}
	report, err := service.VerifyExhaustive(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.ChecksDivergent != 0 || report.Summary.ChecksCounterexample != 0 {
		t.Fatalf("drift must not fabricate divergences: %+v", report.Summary)
	}
	if report.Summary.ChecksUncomparable == 0 || len(report.Uncomparable) == 0 {
		t.Fatalf("drift must be recorded as uncomparable: %+v", report.Summary)
	}
	if !strings.Contains(report.Uncomparable[0].Detail, "policy drift") {
		t.Fatalf("detail=%q", report.Uncomparable[0].Detail)
	}
}

func TestVerifyRejectsRetiredOrganization(t *testing.T) {
	service, writer, _ := testServiceParts(t)
	retired := testSnapshot()
	retired.Organization.Retired = true
	service.facts = &fakeFactReader{snapshot: retired}
	if _, err := service.VerifyExhaustive(context.Background()); !errors.Is(err, ErrOrganizationRetired) {
		t.Fatalf("err=%v", err)
	}
	if len(writer.runs) != 0 {
		t.Fatal("retired organization must not start a run")
	}
}

func TestVerifyDelegateCanonCrossCheck(t *testing.T) {
	service, _, _ := testServiceParts(t)
	service.leaderMap = LeaderWorkerMapFact{Departments: []LeaderWorkerDepartment{
		{ID: "ingenieria_ia", Leader: "ingenieria_ia/orquestador", Workers: []string{"ingenieria_ia/code-runner", "ingenieria_ia/fantasma"}},
	}}
	report, err := service.VerifyExhaustive(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var delegateFindings []Finding
	for _, finding := range report.Findings {
		if finding.Fact == FactMayDelegate {
			delegateFindings = append(delegateFindings, finding)
		}
	}
	// ingenieria_ia/fantasma is not a role, so MayDelegate is false: one
	// counterexample for the canon-listed worker with no live edge.
	if len(delegateFindings) != 1 || delegateFindings[0].Kind != KindCounterexample {
		t.Fatalf("delegate findings=%+v", delegateFindings)
	}
	if delegateFindings[0].TargetRoleID != "ingenieria_ia/fantasma" {
		t.Fatalf("finding=%+v", delegateFindings[0])
	}
}

func TestVerifyDelegateEdgeNotInCanonIsCounterexample(t *testing.T) {
	service, _, _ := testServiceParts(t)
	service.leaderMap = LeaderWorkerMapFact{Departments: []LeaderWorkerDepartment{
		{ID: "ingenieria_ia", Leader: "ingenieria_ia/orquestador", Workers: []string{}},
	}}
	report, err := service.VerifyExhaustive(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, finding := range report.Findings {
		if finding.Fact == FactMayDelegate && finding.TargetRoleID == "ingenieria_ia/code-runner" && finding.Kind == KindCounterexample {
			found = true
		}
	}
	if !found {
		t.Fatalf("live edge missing from canon was not recorded: %+v", report.Findings)
	}
}

func TestVerifyObserverWithReportIsCounterexample(t *testing.T) {
	service, _, ground := testServiceParts(t)
	withEdge := testSnapshot()
	withEdge.ReportingLines = append(withEdge.ReportingLines, ReportingLineFact{RoleID: "ingenieria_ia/code-runner", ReportsToRoleID: "empresa/ceo_observer"})
	service.facts = &fakeFactReader{snapshot: withEdge}
	ground.snapshot = withEdge
	report, err := service.VerifyExhaustive(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var observerFindings []Finding
	for _, finding := range report.Findings {
		if finding.Fact == FactMayDelegate && finding.SubjectRoleID == "empresa/ceo_observer" {
			observerFindings = append(observerFindings, finding)
		}
	}
	if len(observerFindings) != 1 || observerFindings[0].Kind != KindCounterexample {
		t.Fatalf("observer findings=%+v", observerFindings)
	}
}

func TestVerifyDependencyClosedLiveViolation(t *testing.T) {
	service, _, ground := testServiceParts(t)
	cyclic := testSnapshot()
	cyclic.ReportingLines = []ReportingLineFact{
		{RoleID: "empresa/ceo", ReportsToRoleID: "ingenieria_ia/orquestador"},
		{RoleID: "ingenieria_ia/orquestador", ReportsToRoleID: "empresa/ceo"},
	}
	service.facts = &fakeFactReader{snapshot: cyclic}
	ground.snapshot = cyclic
	report, err := service.VerifyExhaustive(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var closureFindings []Finding
	for _, finding := range report.Findings {
		if finding.Fact == FactDependencyClosed {
			closureFindings = append(closureFindings, finding)
		}
	}
	// One cycle violation plus the canon/database disagreement (canon says
	// closed, live graph is cyclic).
	if len(closureFindings) != 2 {
		t.Fatalf("closure findings=%+v", closureFindings)
	}
	for _, finding := range closureFindings {
		if finding.Kind != KindCounterexample {
			t.Fatalf("finding=%+v", finding)
		}
	}
}

func TestReplayRecordedParityStaleAndDivergence(t *testing.T) {
	snap := testSnapshot()
	matrix := testMatrix()
	matrix.Hash = snap.MatrixHash
	traffic := &fakeTraffic{requests: []RecordedRequest{
		{ID: 1, RequesterRoleID: "empresa/human", CapabilityID: "organization.activate_skill", OrganizationRevisionID: 7, CapabilityMatrixHash: matrix.Hash, Status: "approved"},
		{ID: 2, RequesterRoleID: "empresa/human", CapabilityID: "organization.activate_skill", OrganizationRevisionID: 6, CapabilityMatrixHash: matrix.Hash, Status: "approved"},
	}}
	ground := &fakeGround{
		snapshot: snap, matrix: matrix, canonicalClosed: true,
		capabilityOverride: map[string]CapabilityVerdict{
			"empresa/human\x00organization.activate_skill": {Effect: VerdictDeny, ReasonCode: ReasonHardDeny},
		},
	}
	writer := newFakeWriter()
	service, err := NewService(&fakeFactReader{snapshot: snap}, traffic, writer, ground, matrix, LeaderWorkerMapFact{}, "explorarte", 1, 0, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	report, err := service.ReplayRecorded(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.ChecksTotal != 2 || report.Summary.ChecksDivergent != 1 || report.Summary.ChecksUncomparable != 1 {
		t.Fatalf("summary=%+v", report.Summary)
	}
	if report.Findings[0].Fact != FactCapabilityGranted || report.Findings[0].Kind != KindDivergence {
		t.Fatalf("findings=%+v", report.Findings)
	}
	if !strings.Contains(report.Uncomparable[0].Detail, "stale") {
		t.Fatalf("stale detail=%q", report.Uncomparable[0].Detail)
	}
	if writer.statuses[report.RunID] != "completed" {
		t.Fatalf("run status=%q", writer.statuses[report.RunID])
	}
}

func TestVerifyAbortsWhenGroundTruthFails(t *testing.T) {
	service, writer, ground := testServiceParts(t)
	ground.err = errors.New("database exploded")
	_, err := service.VerifyExhaustive(context.Background())
	if err == nil || !strings.Contains(err.Error(), "database exploded") {
		t.Fatalf("err=%v", err)
	}
	for _, status := range writer.statuses {
		if status != "failed" {
			t.Fatalf("run status=%q want failed", status)
		}
	}
}

func TestEnforcementConstantMatchesCanon(t *testing.T) {
	if EnforcementObserveAndCompareOnly != "observe_and_compare_only" {
		t.Fatalf("enforcement=%q", EnforcementObserveAndCompareOnly)
	}
}
