package shadowverifier

import (
	"fmt"
	"strings"
	"testing"
)

func testSnapshot() Snapshot {
	return Snapshot{
		Organization: OrganizationFact{ID: "explorarte", OwnerRoleID: "empresa/human", CEORoleID: "empresa/ceo", CurrentRevision: 7},
		RevisionID:   7,
		MatrixHash:   strings.Repeat("a", 64),
		Units: []UnitFact{
			{ID: "ingenieria_ia", Kind: "department", Operational: true},
			{ID: "empresa", Kind: "executive_layer", Leaderless: true},
		},
		Roles: []RoleFact{
			{ID: "empresa/human", UnitID: "empresa", AuthorityClass: "owner", RuntimeKind: "human", Enabled: true},
			{ID: "empresa/ceo", UnitID: "empresa", AuthorityClass: "executive", RuntimeKind: "human", Enabled: true, Executable: true},
			{ID: "empresa/ceo_observer", UnitID: "empresa", AuthorityClass: "executive", RuntimeKind: RuntimeKindReadOnlyObserver, Enabled: true},
			{ID: "ingenieria_ia/orquestador", UnitID: "ingenieria_ia", AuthorityClass: "department_leadership", CanonicalLeader: true, Enabled: true, Executable: true},
			{ID: "ingenieria_ia/code-runner", UnitID: "ingenieria_ia", AuthorityClass: "execution_service", Enabled: true, Executable: true},
		},
		ReportingLines: []ReportingLineFact{
			{RoleID: "empresa/ceo", ReportsToRoleID: "empresa/human", Relationship: "reports_to"},
			{RoleID: "ingenieria_ia/orquestador", ReportsToRoleID: "empresa/ceo", Relationship: "reports_to"},
			{RoleID: "ingenieria_ia/code-runner", ReportsToRoleID: "ingenieria_ia/orquestador", Relationship: "reports_to"},
		},
	}
}

func TestRoleAndDepartmentExistExcludeRetiredAndUnknown(t *testing.T) {
	snap := testSnapshot()
	if !RoleExists(snap, "ingenieria_ia/code-runner") {
		t.Fatal("existing role must exist")
	}
	if RoleExists(snap, "fantasma/rol") {
		t.Fatal("unknown role must not exist")
	}
	if !DepartmentExists(snap, "ingenieria_ia") {
		t.Fatal("existing unit must exist")
	}
	if DepartmentExists(snap, "fantasma") {
		t.Fatal("unknown unit must not exist")
	}
}

func TestLeaderOfSingleLeaderLeaderlessAndAnomalies(t *testing.T) {
	snap := testSnapshot()
	leader, found, anomaly := LeaderOf(snap, "ingenieria_ia")
	if leader != "ingenieria_ia/orquestador" || !found || anomaly != "" {
		t.Fatalf("leader=%q found=%t anomaly=%q", leader, found, anomaly)
	}
	leader, found, anomaly = LeaderOf(snap, "empresa")
	if leader != "" || found || anomaly != "" {
		t.Fatalf("leaderless unit: leader=%q found=%t anomaly=%q", leader, found, anomaly)
	}
	leader, found, anomaly = LeaderOf(snap, "fantasma")
	if found || anomaly == "" {
		t.Fatalf("unknown unit must be anomalous: found=%t anomaly=%q", found, anomaly)
	}

	dup := testSnapshot()
	dup.Roles = append(dup.Roles, RoleFact{ID: "ingenieria_ia/segundo_lider", UnitID: "ingenieria_ia", AuthorityClass: "department_leadership", CanonicalLeader: true, Enabled: true, Executable: true})
	leader, found, anomaly = LeaderOf(dup, "ingenieria_ia")
	if found || !strings.Contains(anomaly, "2 canonical leaders") {
		t.Fatalf("duplicate leaders must be anomalous: leader=%q found=%t anomaly=%q", leader, found, anomaly)
	}

	orphan := testSnapshot()
	orphan.Units[0].Leaderless = false
	orphan.Roles[3].CanonicalLeader = false
	leader, found, anomaly = LeaderOf(orphan, "ingenieria_ia")
	if found || anomaly == "" {
		t.Fatalf("non-leaderless unit without leader must be anomalous: found=%t anomaly=%q", found, anomaly)
	}
}

func TestMayDelegateDirectEdgeOnly(t *testing.T) {
	snap := testSnapshot()
	if !MayDelegate(snap, "ingenieria_ia/orquestador", "ingenieria_ia/code-runner") {
		t.Fatal("leader must delegate to direct report")
	}
	if MayDelegate(snap, "ingenieria_ia/code-runner", "ingenieria_ia/orquestador") {
		t.Fatal("delegation is not upward")
	}
	if MayDelegate(snap, "empresa/ceo", "ingenieria_ia/code-runner") {
		t.Fatal("delegation must not be transitive across the chain")
	}
	if MayDelegate(snap, "fantasma/rol", "ingenieria_ia/code-runner") || MayDelegate(snap, "ingenieria_ia/orquestador", "fantasma/rol") {
		t.Fatal("nonexistent roles must not delegate")
	}
}

func TestMayDelegateObserverException(t *testing.T) {
	snap := testSnapshot()
	snap.ReportingLines = append(snap.ReportingLines, ReportingLineFact{RoleID: "ingenieria_ia/code-runner", ReportsToRoleID: "empresa/ceo_observer"})
	if MayDelegate(snap, "empresa/ceo_observer", "ingenieria_ia/code-runner") {
		t.Fatal("observer may_delegate: false per organization.yaml, even with a live edge")
	}
}

func TestMayMessageChannelsExecutiveAndEscalation(t *testing.T) {
	snap := testSnapshot()
	cases := []struct {
		name     string
		from, to string
		want     bool
	}{
		{"report to leader", "ingenieria_ia/code-runner", "ingenieria_ia/orquestador", true},
		{"leader to report", "ingenieria_ia/orquestador", "ingenieria_ia/code-runner", true},
		{"owner broadcast", "empresa/human", "ingenieria_ia/code-runner", true},
		{"ceo broadcast", "empresa/ceo", "ingenieria_ia/code-runner", true},
		{"escalation to owner", "ingenieria_ia/code-runner", "empresa/human", true},
		{"observer to owner forbidden", "empresa/ceo_observer", "empresa/human", false},
		{"observer to ceo no channel", "empresa/ceo_observer", "empresa/ceo", false},
		{"unrelated peers", "ingenieria_ia/code-runner", "empresa/ceo_observer", false},
		{"nonexistent source", "fantasma/rol", "empresa/human", false},
		{"nonexistent target", "empresa/human", "fantasma/rol", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := MayMessage(snap, tc.from, tc.to); got != tc.want {
				t.Fatalf("MayMessage(%q,%q)=%t want %t", tc.from, tc.to, got, tc.want)
			}
		})
	}
}

func TestMayMessageObserverEdgeToOwnerIsRecordedButDenied(t *testing.T) {
	snap := testSnapshot()
	snap.ReportingLines = append(snap.ReportingLines, ReportingLineFact{RoleID: "empresa/ceo_observer", ReportsToRoleID: "empresa/human"})
	if MayMessage(snap, "empresa/ceo_observer", "empresa/human") {
		t.Fatal("observer clause must win over the reporting channel")
	}
}

func TestCheckDependencyClosedOnCleanGraph(t *testing.T) {
	if violations := CheckDependencyClosed(testSnapshot()); len(violations) != 0 {
		t.Fatalf("clean graph produced violations: %+v", violations)
	}
}

func TestCheckDependencyClosedFindsDanglingSelfAndCycles(t *testing.T) {
	dangling := testSnapshot()
	dangling.ReportingLines = append(dangling.ReportingLines, ReportingLineFact{RoleID: "ingenieria_ia/code-runner", ReportsToRoleID: "fantasma/rol"})
	violations := CheckDependencyClosed(dangling)
	if len(violations) != 1 || violations[0].Code != ClosureTargetMissing {
		t.Fatalf("dangling: %+v", violations)
	}

	self := testSnapshot()
	self.ReportingLines = append(self.ReportingLines, ReportingLineFact{RoleID: "empresa/ceo", ReportsToRoleID: "empresa/ceo"})
	violations = CheckDependencyClosed(self)
	// A self-loop is also a cycle; the canonical validator reports both the
	// same way, so the shadow mirrors the overlap.
	if len(violations) != 2 || violations[0].Code != ClosureSelfReference || violations[1].Code != ClosureCycle {
		t.Fatalf("self reference: %+v", violations)
	}

	cycle := testSnapshot()
	cycle.ReportingLines = []ReportingLineFact{
		{RoleID: "empresa/ceo", ReportsToRoleID: "ingenieria_ia/orquestador"},
		{RoleID: "ingenieria_ia/orquestador", ReportsToRoleID: "ingenieria_ia/code-runner"},
		{RoleID: "ingenieria_ia/code-runner", ReportsToRoleID: "empresa/ceo"},
	}
	violations = CheckDependencyClosed(cycle)
	if len(violations) == 0 || violations[0].Code != ClosureCycle {
		t.Fatalf("cycle: %+v", violations)
	}
	if !strings.Contains(violations[0].Detail, " -> ") {
		t.Fatalf("cycle detail must show the path: %q", violations[0].Detail)
	}
}

func testMatrix() MatrixIndex {
	return MatrixIndex{
		Hash:          strings.Repeat("a", 64),
		DefaultPolicy: "deny",
		Capabilities: map[string]CapabilityFact{
			"code.commit":                 {ID: "code.commit", Risk: "high"},
			"task.execute":                {ID: "task.execute", Risk: "medium"},
			"organization.activate_skill": {ID: "organization.activate_skill", Risk: "high", Approval: "owner"},
			"model.invoke":                {ID: "model.invoke", Risk: "high"},
			"cell.read_clinical_data":     {ID: "cell.read_clinical_data", Risk: "forbidden"},
		},
		Grants: map[string][]string{
			"owner":             {"*"},
			"execution_service": {"code.commit", "model.invoke"},
			"specialist":        {"task.execute"},
		},
		HardDenies: map[string][]string{
			"*":          {"cell.read_clinical_data"},
			"owner":      {"model.invoke"},
			"specialist": {"task.execute"},
		},
		Authorities: map[string]struct{}{"owner": {}, "executive": {}, "execution_service": {}, "specialist": {}},
	}
}

func TestEvaluateCapabilityPolicyCore(t *testing.T) {
	matrix := testMatrix()
	snap := testSnapshot()
	cases := []struct {
		name       string
		role       string
		capability string
		effect     string
		reason     string
	}{
		{"explicit grant", "ingenieria_ia/code-runner", "code.commit", VerdictAllow, ReasonAllowedByGrant},
		{"wildcard owner", "empresa/human", "code.commit", VerdictAllow, ReasonAllowedByGrant},
		{"owner hard deny beats wildcard", "empresa/human", "model.invoke", VerdictDeny, ReasonHardDeny},
		{"global hard deny", "ingenieria_ia/code-runner", "cell.read_clinical_data", VerdictDeny, ReasonHardDeny},
		{"authority hard deny beats grant", "creativo/copywriter", "task.execute", VerdictDeny, ReasonHardDeny},
		{"approval required before grant", "empresa/human", "organization.activate_skill", VerdictApprovalRequired, ReasonApprovalMissing},
		{"unknown capability", "empresa/human", "fantasma.capability", VerdictDeny, ReasonUnknownCapability},
		{"unknown role", "fantasma/rol", "code.commit", VerdictDeny, ReasonRoleNotFound},
		{"default deny", "ingenieria_ia/code-runner", "task.execute", VerdictDeny, ReasonGrantMissing},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			roleSnap := snap
			if tc.role == "creativo/copywriter" {
				roleSnap = testSnapshot()
				roleSnap.Roles = append(roleSnap.Roles, RoleFact{ID: "creativo/copywriter", UnitID: "creativo", AuthorityClass: "specialist", Enabled: true, Executable: true})
			}
			got := EvaluateCapability(matrix, roleSnap, tc.role, tc.capability)
			if got.Effect != tc.effect || got.ReasonCode != tc.reason {
				t.Fatalf("got %s/%s want %s/%s", got.Effect, got.ReasonCode, tc.effect, tc.reason)
			}
		})
	}
}

func TestEvaluateCapabilityRoleStateDenials(t *testing.T) {
	matrix := testMatrix()
	snap := testSnapshot()
	disabled := snap
	disabled.Roles = append(append([]RoleFact{}, snap.Roles...), RoleFact{ID: "ingenieria_ia/deshabilitado", UnitID: "ingenieria_ia", AuthorityClass: "execution_service", Enabled: false, Executable: true})
	if got := EvaluateCapability(matrix, disabled, "ingenieria_ia/deshabilitado", "code.commit"); got.Effect != VerdictDeny || got.ReasonCode != ReasonRoleDisabled {
		t.Fatalf("disabled: %+v", got)
	}
	notExecutable := snap
	notExecutable.Roles = append(append([]RoleFact{}, snap.Roles...), RoleFact{ID: "ingenieria_ia/no_ejecutable", UnitID: "ingenieria_ia", AuthorityClass: "execution_service", Enabled: true, Executable: false})
	if got := EvaluateCapability(matrix, notExecutable, "ingenieria_ia/no_ejecutable", "code.commit"); got.Effect != VerdictDeny || got.ReasonCode != ReasonRoleNotExecutable {
		t.Fatalf("not executable: %+v", got)
	}
	if got := EvaluateCapability(matrix, notExecutable, "empresa/human", "code.commit"); got.Effect != VerdictAllow {
		t.Fatalf("owner is exempt from executable: %+v", got)
	}
	unknownAuthority := snap
	unknownAuthority.Roles = append(append([]RoleFact{}, snap.Roles...), RoleFact{ID: "ingenieria_ia/autoridad_desconocida", UnitID: "ingenieria_ia", AuthorityClass: "fantasma", Enabled: true, Executable: true})
	if got := EvaluateCapability(matrix, unknownAuthority, "ingenieria_ia/autoridad_desconocida", "code.commit"); got.Effect != VerdictDeny || got.ReasonCode != ReasonUnknownAuthorityClass {
		t.Fatalf("unknown authority: %+v", got)
	}
}

func TestSampledIsDeterministicAndBounded(t *testing.T) {
	for i := 0; i < 3; i++ {
		if !sampled(1, "a/b", "cap") || !sampled(0, "a/b", "cap") {
			t.Fatal("sample rate <= 1 must include everything")
		}
	}
	first := sampled(7, "empresa/human", "code.commit")
	for i := 0; i < 10; i++ {
		if sampled(7, "empresa/human", "code.commit") != first {
			t.Fatal("sampling must be deterministic")
		}
	}
	included := 0
	total := 1000
	for i := 0; i < total; i++ {
		role := "rol/" + string(rune('a'+i%26)) + fmt.Sprint(i)
		if sampled(10, role, "code.commit") {
			included++
		}
	}
	if included == 0 || included == total {
		t.Fatalf("1-in-10 sample included %d/%d", included, total)
	}
}
