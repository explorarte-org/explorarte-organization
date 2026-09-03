package executive

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/coderunner"
	"github.com/Mireuz13/explorarte-organization/internal/designfreeze"
	"github.com/Mireuz13/explorarte-organization/internal/engineeringmission"
)

const targetSHA = "14a0611b8cf670ccd32b1c9ca662261b0fdbd7c9"

type fakeProgramTarget struct {
	sha    string
	err    error
	hits   int
	moveAt int
	moved  string
}

func (f *fakeProgramTarget) ResolveProgramTargetSHA(context.Context) (string, error) {
	f.hits++
	if f.moveAt > 0 && f.hits > f.moveAt {
		return f.moved, f.err
	}
	return f.sha, f.err
}

// calls reports how many times the promotion target was consulted, which is
// what distinguishes a design episode observing ONE world from one that
// re-resolves it and lets the ground move between rounds.
func (f *fakeProgramTarget) calls() int { return f.hits }

// moveAfter makes the repository advance once the design has been pinned,
// modelling somebody else promoting while a campaign is still deciding.
func (f *fakeProgramTarget) moveAfter(hits int, sha string) {
	f.moveAt, f.moved = hits, sha
}

type fakeMissionProvisioner struct {
	mu       sync.Mutex
	commands []MissionProvisionCommand
	nextID   int64
	// byDigest mirrors engineeringmission.Service.Create, whose idempotency
	// key is the normalized policy's own content digest: the same policy
	// always resolves to the same mission.
	byDigest map[string]int64
	err      error
}

func newFakeMissionProvisioner() *fakeMissionProvisioner {
	return &fakeMissionProvisioner{nextID: 900, byDigest: map[string]int64{}}
}

func (f *fakeMissionProvisioner) ProvisionMission(_ context.Context, command MissionProvisionCommand) (MissionRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return MissionRecord{}, f.err
	}
	f.commands = append(f.commands, command)
	_, digest, err := command.Policy.MarshalEvidence()
	if err != nil {
		return MissionRecord{}, err
	}
	if id, exists := f.byDigest[digest]; exists {
		return MissionRecord{TaskID: id}, nil
	}
	f.nextID++
	f.byDigest[digest] = f.nextID
	return MissionRecord{TaskID: f.nextID}, nil
}

func (f *fakeMissionProvisioner) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.byDigest)
}

func (f *fakeMissionProvisioner) last() (MissionProvisionCommand, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.commands) == 0 {
		return MissionProvisionCommand{}, false
	}
	return f.commands[len(f.commands)-1], true
}

func implementationPlanBody(path string) string {
	diff := "--- a/" + path + "\\n+++ b/" + path + "\\n@@ -0,0 +1 @@\\n+evidence\\n"
	return `{"schema_version":"implementation-plan/v1","objective":"Record the autonomous cycle evidence.",` +
		`"changes":[{"path":"` + path + `","intent":"Write the evidence file.","patch":"` + diff + `"}],` +
		`"verification_expectations":["build, vet and test pass"],"dependency_order":[],"evidence_refs":[]}`
}

type missionFixture struct {
	*freezeFixture
	target      *fakeProgramTarget
	provisioner *fakeMissionProvisioner
}

func newMissionFixture(t *testing.T, planPath string, widenScope bool) *missionFixture {
	t.Helper()
	tasksPort := newMemoryTasks()
	models := newFakeModels()
	bodies := freezeBodies()
	bodies[PurposeImplementationPlan] = implementationPlanBody(planPath)
	harness := &scriptedHarness{models: models, tasks: tasksPort, bodies: bodies, adjudicationVerdict: "freeze"}

	leader := RoleRef{ID: "ingenieria_ia/orquestador", UnitID: "ingenieria_ia", Enabled: true, Executable: true, CanonicalLeader: true}
	workerRole := RoleRef{ID: "ingenieria_ia/qa", UnitID: "ingenieria_ia", Enabled: true, Executable: true}
	reviewer := RoleRef{ID: AdversarialReviewerRoleID, UnitID: "investigacion", Enabled: true, Executable: true}
	ceo := RoleRef{ID: CEORoleID, UnitID: "empresa", Enabled: true, Executable: true}
	target := &fakeProgramTarget{sha: targetSHA}
	provisioner := newFakeMissionProvisioner()

	orchestrator, err := NewOrchestrator(Dependencies{Acceptance: newMemoryAcceptance(),
		OrganizationID: "explorarte",
		Registry: fakeRegistry{
			rev:     RevisionRef{ID: 7},
			units:   map[string]UnitRef{"ingenieria_ia": {ID: "ingenieria_ia", Operational: true, LeaderRoleID: leader.ID}},
			roles:   map[string]RoleRef{leader.ID: leader, workerRole.ID: workerRole, reviewer.ID: reviewer, ceo.ID: ceo},
			leaders: map[string]RoleRef{"ingenieria_ia": leader},
		},
		Tasks: tasksPort, Contexts: &fakeContexts{}, Assignments: fakeAssignments{},
		Principals: newFakePrincipals(), Models: models, Harness: harness,
		Budget: &countingBudget{}, Completion: &fakeCompletion{verdict: CompletionPass},
		Decisions: &fakeDecisionRecorder{}, Authorization: allowAuthz{}, Limits: DefaultLimits(),
		Clock: ClockFunc(func() time.Time { return time.Unix(1000, 0) }),
	}, WithMissionProvisioning(target, provisioner),
		WithSnapshotSources(stubSnapshotSources{}))
	if err != nil {
		t.Fatal(err)
	}

	requirements := []RequirementProposal{
		{Key: designfreeze.RequirementKey, Type: "approval", Description: "Design frozen", Required: true},
		{Key: MissionRequirementKey, Type: "approval", Description: "Engineering mission provisioned", Required: true},
	}
	if widenScope {
		requirements = append(requirements, RequirementProposal{
			Key: InternalCodeScopeRequirementKey, Type: "condition",
			Description: "Owner widened the mission to internal code", Required: true,
		})
	}
	run, _, err := orchestrator.Submit(context.Background(), SubmitRequest{
		ActorRoleID: OwnerRoleID, IdempotencyKey: "autonomy-smoke-001",
		Goal: OwnerGoal{
			Goal:               "AUTONOMY-SMOKE-001: record the autonomous cycle evidence.",
			AcceptanceCriteria: []AcceptanceCriterion{{Text: "Exactly one allowed file changes", Phase: AcceptanceDesign}},
			Requirements:       requirements,
		},
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	return &missionFixture{
		freezeFixture: &freezeFixture{orchestrator: orchestrator, tasks: tasksPort, harness: harness, root: run.RootTaskID},
		target:        target, provisioner: provisioner,
	}
}

const smokePath = "docs/implementation/autonomy-smoke/AUTONOMY_SMOKE.md"

// The whole seam, through the real orchestrator: owner goal to a governed
// mission, with no human step in between.
func TestExecutiveProvisionsAGovernedMissionAfterFreeze(t *testing.T) {
	fixture := newMissionFixture(t, smokePath, false)
	fixture.drive(t)

	root := fixture.rootRecord(t)
	if status := requirementStatus(root, designfreeze.RequirementKey); status != "satisfied" {
		t.Fatalf("design-freeze=%q", status)
	}
	if status := requirementStatus(root, MissionRequirementKey); status != "satisfied" {
		t.Fatalf("implementation-mission=%q", status)
	}
	if fixture.provisioner.count() != 1 {
		t.Fatalf("missions provisioned=%d", fixture.provisioner.count())
	}
	command, ok := fixture.provisioner.last()
	if !ok {
		t.Fatal("no mission was provisioned")
	}

	// BaseSHA is the promotion target's exact commit, not a symbolic ref.
	if command.Policy.BaseSHA != targetSHA {
		t.Fatalf("base sha=%q want %q", command.Policy.BaseSHA, targetSHA)
	}
	// AllowedPaths is exactly the declared file.
	if len(command.Policy.AllowedPaths) != 1 || command.Policy.AllowedPaths[0] != smokePath {
		t.Fatalf("allowed paths=%v", command.Policy.AllowedPaths)
	}
	// Gates are the host's, in full.
	if len(command.Policy.RequiredGates) != 4 {
		t.Fatalf("gates=%v", command.Policy.RequiredGates)
	}
	// The plan is a real CodeRunner plan its own worker can parse.
	parsed, err := coderunner.ParsePlan(command.PlanJSON)
	if err != nil {
		t.Fatalf("CodeRunner rejected the provisioned plan: %v", err)
	}
	if parsed.SchemaVersion != coderunner.SchemaVersion {
		t.Fatalf("plan schema=%q", parsed.SchemaVersion)
	}
	tail := parsed.Operations[len(parsed.Operations)-4:]
	if tail[0].Type != coderunner.GoBuild || tail[1].Type != coderunner.GoVet || tail[2].Type != coderunner.GoTest || tail[3].Type != coderunner.Fitness {
		t.Fatalf("gates are not the final operations: %+v", parsed.Operations)
	}
	// The implementation plan ran as the department leader, under its own
	// purpose.
	planCommand, ok := fixture.commandFor(PurposeImplementationPlan)
	if !ok {
		t.Fatal("no implementation plan execution")
	}
	if planCommand.RoleID != "ingenieria_ia/orquestador" {
		t.Fatalf("implementation plan ran as %q", planCommand.RoleID)
	}
	// Order: freeze decided before the plan was even requested.
	purposes := fixture.purposes()
	indexOf := func(target ExecutionPurpose) int {
		for i, purpose := range purposes {
			if purpose == target {
				return i
			}
		}
		return -1
	}
	if indexOf(PurposeDesignAdjudication) > indexOf(PurposeImplementationPlan) {
		t.Fatalf("the implementation plan preceded the adjudication: %v", purposes)
	}
}

// A mission may never precede the decision that authorized it.
func TestMissionRequiresASatisfiedFreeze(t *testing.T) {
	fixture := newMissionFixture(t, smokePath, false)
	fixture.harness.adjudicationVerdict = "revise"
	run := fixture.drive(t)
	// A revise now opens a bounded revision round rather than stopping at
	// once, so the run ends when the rounds run out. What this test guards is
	// unchanged and stronger for it: through every round, and at the end of
	// them, no mission exists.
	if run.State != StateBlocked || run.ReasonCode != ReasonDesignRoundsExhausted {
		t.Fatalf("run=%+v", run)
	}
	if fixture.provisioner.count() != 0 {
		t.Fatal("a mission was provisioned without a freeze")
	}
	if _, planned := fixture.commandFor(PurposeImplementationPlan); planned {
		t.Fatal("an implementation plan ran without a freeze")
	}
}

// Scope is an upper bound the host sets. A documentation run cannot reach code
// however the plan words it.
func TestScopeDefaultsToTheNarrowestAndRefusesWiderPlans(t *testing.T) {
	fixture := newMissionFixture(t, "internal/executive/orchestrator.go", false)
	run := fixture.drive(t)
	if run.State != StateBlocked || run.ReasonCode != ReasonMissionPolicyRejected {
		t.Fatalf("run=%+v", run)
	}
	if fixture.provisioner.count() != 0 {
		t.Fatal("an out-of-scope mission was provisioned")
	}
	if status := requirementStatus(fixture.rootRecord(t), MissionRequirementKey); status == "satisfied" {
		t.Fatal("the mission requirement was satisfied by a refused policy")
	}

	// The owner widening the scope is what makes the same plan permissible.
	widened := newMissionFixture(t, "internal/executive/orchestrator.go", true)
	widened.drive(t)
	if widened.provisioner.count() != 1 {
		t.Fatalf("a widened run did not provision: %d", widened.provisioner.count())
	}
	command, _ := widened.provisioner.last()
	if command.Policy.AllowedPaths[0] != "internal/executive/orchestrator.go" {
		t.Fatalf("allowed=%v", command.Policy.AllowedPaths)
	}
}

// Governance paths stay refused even when the owner widened the scope.
func TestGovernancePathsAreRefusedEvenWhenWidened(t *testing.T) {
	for _, target := range []string{
		"docs/canonical/capability-matrix.yaml",
		"migrations/000055_x.up.sql",
		"scripts/check-model-runtime-fitness.sh",
		"go.mod",
	} {
		fixture := newMissionFixture(t, target, true)
		fixture.drive(t)
		if fixture.provisioner.count() != 0 {
			t.Fatalf("a mission was provisioned for governance path %q", target)
		}
	}
}

// §18: repeated Resume, and a restart, must not duplicate the mission.
func TestMissionProvisioningIsIdempotentAcrossResumeAndRestart(t *testing.T) {
	fixture := newMissionFixture(t, smokePath, false)
	for i := 0; i < 30; i++ {
		run, err := fixture.orchestrator.Resume(context.Background(), fixture.root)
		if err != nil && !errors.Is(err, ErrRunBlocked) {
			t.Fatalf("resume %d: %v", i, err)
		}
		if run.State.Terminal() || run.State == StateBlocked {
			break
		}
	}
	if fixture.provisioner.count() != 1 {
		t.Fatalf("missions=%d after repeated resume", fixture.provisioner.count())
	}
	if got := countPurpose(fixture.purposes(), PurposeImplementationPlan); got != 1 {
		t.Fatalf("implementation plan ran %d times", got)
	}

	// A restart over the same durable store, with a fresh orchestrator and a
	// fresh provisioner that shares only the digest map -- the same condition
	// engineeringmission.Service.Create faces, where idempotency comes from
	// the policy digest rather than from process memory.
	leader := RoleRef{ID: "ingenieria_ia/orquestador", UnitID: "ingenieria_ia", Enabled: true, Executable: true, CanonicalLeader: true}
	workerRole := RoleRef{ID: "ingenieria_ia/qa", UnitID: "ingenieria_ia", Enabled: true, Executable: true}
	reviewer := RoleRef{ID: AdversarialReviewerRoleID, UnitID: "investigacion", Enabled: true, Executable: true}
	ceo := RoleRef{ID: CEORoleID, UnitID: "empresa", Enabled: true, Executable: true}
	restarted, err := NewOrchestrator(Dependencies{Acceptance: newMemoryAcceptance(),
		OrganizationID: "explorarte",
		Registry: fakeRegistry{
			rev:     RevisionRef{ID: 7},
			units:   map[string]UnitRef{"ingenieria_ia": {ID: "ingenieria_ia", Operational: true, LeaderRoleID: leader.ID}},
			roles:   map[string]RoleRef{leader.ID: leader, workerRole.ID: workerRole, reviewer.ID: reviewer, ceo.ID: ceo},
			leaders: map[string]RoleRef{"ingenieria_ia": leader},
		},
		Tasks: fixture.tasks, Contexts: &fakeContexts{}, Assignments: fakeAssignments{},
		Principals: newFakePrincipals(), Models: fixture.harness.models, Harness: fixture.harness,
		Budget: &countingBudget{}, Completion: &fakeCompletion{verdict: CompletionPass},
		Decisions: &fakeDecisionRecorder{}, Authorization: allowAuthz{}, Limits: DefaultLimits(),
		Clock: ClockFunc(func() time.Time { return time.Unix(1000, 0) }),
	}, WithMissionProvisioning(fixture.target, fixture.provisioner),
		WithSnapshotSources(stubSnapshotSources{}))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		run, resumeErr := restarted.Resume(context.Background(), fixture.root)
		if resumeErr != nil && !errors.Is(resumeErr, ErrRunBlocked) {
			t.Fatalf("restarted resume: %v", resumeErr)
		}
		if run.State.Terminal() || run.State == StateBlocked {
			break
		}
	}
	if fixture.provisioner.count() != 1 {
		t.Fatalf("the restart produced %d missions", fixture.provisioner.count())
	}
}

// The same frozen plan always derives the same policy, so the provisioner's
// content-digest idempotency is reachable at all. This is the property that
// makes duplicate missions impossible rather than merely unlikely.
func TestDerivedPolicyDigestIsStableAcrossRuns(t *testing.T) {
	first := newMissionFixture(t, smokePath, false)
	first.drive(t)
	second := newMissionFixture(t, smokePath, false)
	second.drive(t)

	a, ok := first.provisioner.last()
	if !ok {
		t.Fatal("first run provisioned nothing")
	}
	b, ok := second.provisioner.last()
	if !ok {
		t.Fatal("second run provisioned nothing")
	}
	_, digestA, err := a.Policy.MarshalEvidence()
	if err != nil {
		t.Fatal(err)
	}
	_, digestB, err := b.Policy.MarshalEvidence()
	if err != nil {
		t.Fatal(err)
	}
	if digestA != digestB {
		t.Fatalf("policy digest is not stable: %s vs %s", digestA, digestB)
	}
	if string(a.PlanJSON) != string(b.PlanJSON) {
		t.Fatal("generated plan is not stable across runs")
	}
}

// Without provisioning configured, a governed run stops instead of quietly
// finishing at the freeze while claiming it would implement something.
func TestUnconfiguredProvisioningBlocksRatherThanSkips(t *testing.T) {
	fixture := newMissionFixture(t, smokePath, false)
	fixture.orchestrator.missions = nil
	fixture.orchestrator.programTarget = nil
	run := fixture.drive(t)
	if run.State != StateBlocked || run.ReasonCode != ReasonMissionProvisioningUnavailable {
		t.Fatalf("run=%+v", run)
	}
	if _, closed := fixture.commandFor(PurposeCEOClosure); closed {
		t.Fatal("the CEO closed a run that could not provision its mission")
	}
}

// The Executive holds exactly one engineering capability. If this surface ever
// grows a promote or apply, the boundary moved.
func TestMissionProvisionerSurfaceIsCreateOnly(t *testing.T) {
	var provisioner MissionProvisioner = newFakeMissionProvisioner()
	body, err := json.Marshal(MissionProvisionCommand{Policy: engineeringmission.MissionPolicy{}})
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{"promote", "approve", "apply", "review", "merge"} {
		if strings.Contains(strings.ToLower(string(body)), needle) {
			t.Fatalf("the provisioning command exposes %q", needle)
		}
	}
	if _, ok := provisioner.(interface{ ApplyPromotion() }); ok {
		t.Fatal("the provisioner exposes promotion")
	}
}

// The budget that authorises a mission's existence is enforced by
// correlation at reservation time, so the campaign's identity has to be part
// of provisioning itself. Before this, Executive missions were created with no
// correlation at all: they spent outside the ceiling that had approved them,
// and nothing that later tried to recover one had a budget to admit it
// against.
//
// This asserts the seam rather than either side of it: that the campaign root's
// own correlation is what reaches the provisioner, not a value the phase
// reconstructed or a plausible-looking substitute.
func TestAProvisionedMissionBelongsToItsCampaign(t *testing.T) {
	fixture := newMissionFixture(t, smokePath, false)
	fixture.drive(t)

	root := fixture.rootRecord(t)
	command, ok := fixture.provisioner.last()
	if !ok {
		t.Fatal("no mission was provisioned")
	}
	if root.CorrelationID == "" {
		t.Fatal("the fixture root carries no correlation, so this test would prove nothing")
	}
	if command.CorrelationID != root.CorrelationID {
		t.Fatalf("mission correlation=%q, want the campaign's own %q", command.CorrelationID, root.CorrelationID)
	}
	// The mission is caused by the campaign root, which is also what keeps
	// it from ever being mistaken for a campaign root itself.
	if command.CausationID != taskCausation(root.ID) {
		t.Fatalf("mission causation=%q, want %q", command.CausationID, taskCausation(root.ID))
	}
}
