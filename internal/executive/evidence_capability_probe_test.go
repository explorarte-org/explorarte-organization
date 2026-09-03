package executive

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/repositoryevidence"
)

// AUTONOMY-SMOKE-017-R10's terminal state, guarded: an adjudication may only
// bind a next round to obligations the pinned world can mechanically supply.
// "DesignBaseSHA" and "InvocationBudget.Validate" were demanded and could
// never be grounded -- the campaign died in round 2's preflight after paying
// for a plan. The probe moves that discovery to the adjudicator's own
// contract boundary, where the measured rejection reaches its retry.

// The R10 pinned tree, reduced to what matters to the probe -- and, since
// checkpoint D made admission CUMULATIVE, also carrying the owner-goal
// symbols a real campaign's contract would already have in force. The two
// sensors (this admission source and the snapshot stubs) describe the same
// world, exactly as production does.
func capabilityWorld() *probeWorldSource {
	return &probeWorldSource{worlds: map[string]map[string]string{targetSHA: {
		"internal/executive/types.go": `package executive

type Limits struct {
	MaxDesignRounds int
	MaxDepartmentReplans int
}

type FrozenDesign struct {
	BaseSHA string
}
`,
		"internal/executive/budget.go": `package executive

func (b InvocationBudget) Validate(l Limits) error {
	if l.MaxDesignRounds < 1 || l.MaxDepartmentReplans < 1 {
		return nil
	}
	return nil
}

func DefaultLimits() Limits {
	return Limits{}
}
`,
		"internal/executive/design_freeze_phase.go": `package executive

func (o *Orchestrator) driveDesignFreeze(ctx context.Context) (bool, error) {
	return false, nil
}
`,
		"internal/executive/orchestrator.go": `package executive

func step(o *Orchestrator, limits Limits) bool {
	done, err := o.driveDesignFreeze(context.Background())
	if err != nil {
		return false
	}
	return done && limits.MaxDesignRounds > 0 && limits.MaxDepartmentReplans >= 0
}
`,
	}}}
}

type probeWorldSource struct {
	worlds map[string]map[string]string
	fail   error
	seen   []string
}

func (s *probeWorldSource) at(sha string) map[string]string { return s.worlds[sha] }

func (s *probeWorldSource) Search(_ context.Context, baseSHA, query string, limit int) ([]repositoryevidence.Match, error) {
	s.seen = append(s.seen, baseSHA)
	if s.fail != nil {
		return nil, s.fail
	}
	var out []repositoryevidence.Match
	for path, body := range s.worlds[baseSHA] {
		for index, line := range strings.Split(body, "\n") {
			if strings.Contains(line, query) {
				out = append(out, repositoryevidence.Match{Path: path, Line: index + 1})
			}
		}
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *probeWorldSource) Lines(_ context.Context, baseSHA, path string) (int, error) {
	body, ok := s.at(baseSHA)[path]
	if !ok {
		return 0, nil
	}
	return len(strings.Split(body, "\n")), nil
}

func (s *probeWorldSource) ReadRange(_ context.Context, baseSHA, path string, start, end int) (string, error) {
	body, ok := s.at(baseSHA)[path]
	if !ok {
		return "", nil
	}
	all := strings.Split(body, "\n")
	if start > len(all) {
		return "", nil
	}
	if end > len(all) {
		end = len(all)
	}
	return strings.Join(all[start-1:end], "\n"), nil
}

func adjudicationTaskOf(t *testing.T, f *wiringFixture) TaskRecord {
	t.Helper()
	all, err := f.tasks.ListByCorrelation(context.Background(), f.rootRecord(t).CorrelationID)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range all {
		if task.TaskClass == TaskClassCoordinationDesignAdjudication {
			return task
		}
	}
	t.Fatal("no design adjudication task exists")
	return TaskRecord{}
}

func hasRoundRequirements(t *testing.T, f *wiringFixture, round int) bool {
	t.Helper()
	root := f.rootRecord(t)
	want := EvidenceRequirementsReference + strconv.FormatInt(root.ID, 10) + "/round/" + strconv.Itoa(round)
	for _, record := range root.Evidence {
		if record.Reference == want {
			return true
		}
	}
	return false
}

// An adjudication proposal is not allowed to become a durable obligation when
// the host has no repository sensor with which to prove that it can supply
// the requested slots. The old optional-sensor behavior adopted it unprobed;
// that was a fail-open admission promise.
func TestAdjudicationProposalFailsClosedWithoutRepositorySensor(t *testing.T) {
	proposal := []EvidenceRequirementProposal{{
		Subject:   "MaxDesignRounds",
		Relations: []string{EvidenceDefinition},
	}}
	err := (&Orchestrator{}).probeAdjudicationRequirements(context.Background(), TaskRecord{}, proposal)
	if !errors.Is(err, ErrEvidenceSensorUnavailable) {
		t.Fatalf("proposal without a repository sensor returned %v, want ErrEvidenceSensorUnavailable", err)
	}
	if !strings.Contains(err.Error(), "proposed 1 evidence requirements") {
		t.Fatalf("sensor failure did not identify the proposal count: %v", err)
	}
}

// driveCapability resumes past every per-attempt failure class the probe can
// produce, until the run blocks or terminates. ErrCompletionFailed carries a
// sensor outage; it is infrastructure, retried, never a verdict.
func driveCapability(t *testing.T, f *wiringFixture, maxPasses int) {
	t.Helper()
	for i := 0; i < maxPasses; i++ {
		_, err := f.orchestrator.Resume(context.Background(), f.root)
		switch {
		case err == nil,
			errors.Is(err, ErrRunBlocked),
			errors.Is(err, ErrEvidenceInsufficient),
			errors.Is(err, ErrModelResultContractRejected),
			errors.Is(err, ErrCompletionFailed):
		default:
			t.Fatalf("resume pass %d: %v", i, err)
		}
		run, stateErr := f.orchestrator.Status(context.Background(), f.root)
		if stateErr != nil {
			t.Fatalf("status after pass %d: %v", i, stateErr)
		}
		if run.State.Terminal() || run.State == StateBlocked {
			return
		}
	}
}

// An impossible demand is rejected AT THE ADJUDICATION, with measured feedback
// naming the subject/relation pair, and never reaches durable state as a round
// obligation -- so no worker can ever be planned against it.
func TestAnAdjudicationCannotBindUnsupplyableSlots(t *testing.T) {
	fixture := newWiringFixture(t, "revise", fullSupply(), []EvidenceRequirementProposal{
		{Subject: "MaxDesignRounds", Relations: []string{"definition", "application"}},
	}, WithRepositoryEvidenceSource("explorarte-organization", capabilityWorld()))
	fixture.harness.bodies[PurposeDepartmentWorker] =
		`{"schema_version":"worker-result/v2","summary":"Grounded.",` +
			`"evidence_refs":["` + wiringDefRef + `","` + wiringAppRef + `"],` +
			`"evidence":[` +
			`{"claim":"declared","subject":"MaxDesignRounds","relation":"definition","ref":"` + wiringDefRef + `"},` +
			`{"claim":"applied","subject":"MaxDesignRounds","relation":"application","ref":"` + wiringAppRef + `"}]}`
	fixture.harness.adjudicationEvidence =
		`[{"subject":"DesignBaseSHA","relations":["definition"]}]`

	driveCapability(t, fixture, 24)

	task := adjudicationTaskOf(t, fixture)
	sawRejection := false
	for _, code := range fixture.tasks.failed {
		if code == "model_result_contract_rejected" {
			sawRejection = true
		}
	}
	if !sawRejection {
		t.Fatal("an unsupplyable demand was never rejected at the adjudication")
	}
	if task.ReasonCode != "model_result_contract_rejected" {
		t.Fatalf("adjudication closed as %q", task.ReasonCode)
	}
	for _, want := range []string{"DesignBaseSHA/definition", "CAPACITY_CONFLICT"} {
		if !strings.Contains(task.Reason, want) {
			t.Fatalf("rejection feedback missing %q, got: %q", want, task.Reason)
		}
	}
	if hasRoundRequirements(t, fixture, 2) {
		t.Fatal("an unsupplyable demand was adopted as a round obligation")
	}
}

// The observer failing is infrastructure, not Luna's verdict: the attempt
// closes under its own code, and no contract rejection is recorded -- blaming
// the artifact for the observer's silence would be the misattribution this
// whole family of guards exists to prevent.
func TestSensorOutageIsNotBlamedOnTheAdjudicator(t *testing.T) {
	source := capabilityWorld()
	source.fail = errors.New("git index locked")
	fixture := newWiringFixture(t, "revise", fullSupply(), []EvidenceRequirementProposal{
		{Subject: "MaxDesignRounds", Relations: []string{"definition", "application"}},
	}, WithRepositoryEvidenceSource("explorarte-organization", source))
	fixture.harness.bodies[PurposeDepartmentWorker] =
		`{"schema_version":"worker-result/v2","summary":"Grounded.",` +
			`"evidence_refs":["` + wiringDefRef + `","` + wiringAppRef + `"],` +
			`"evidence":[` +
			`{"claim":"declared","subject":"MaxDesignRounds","relation":"definition","ref":"` + wiringDefRef + `"},` +
			`{"claim":"applied","subject":"MaxDesignRounds","relation":"application","ref":"` + wiringAppRef + `"}]}`
	fixture.harness.adjudicationEvidence =
		`[{"subject":"driveDesignFreeze","relations":["application"]}]`

	driveCapability(t, fixture, 12)

	all, _ := fixture.tasks.ListByCorrelation(context.Background(), fixture.rootRecord(t).CorrelationID)
	for _, dbg := range all {
		t.Logf("class=%q status=%q reason=%.80s", dbg.TaskClass, dbg.Status, dbg.Reason)
	}
	task := adjudicationTaskOf(t, fixture)
	if task.ReasonCode != "evidence_sensor_unavailable" {
		t.Fatalf("sensor outage recorded as %q, want evidence_sensor_unavailable", task.ReasonCode)
	}
	for _, code := range fixture.tasks.failed {
		if code == "model_result_contract_rejected" {
			t.Fatal("a sensor outage was recorded as the adjudicator's rejection")
		}
	}
}

// A real symbol passes the probe and binds the round it was asked for: the
// productive revise path survives the new gate untouched.
func TestARealSymbolPassesTheProbeAndBindsTheNextRound(t *testing.T) {
	fixture := newWiringFixture(t, "revise", fullSupply(), []EvidenceRequirementProposal{
		{Subject: "MaxDesignRounds", Relations: []string{"definition", "application"}},
	}, WithRepositoryEvidenceSource("explorarte-organization", capabilityWorld()))
	fixture.harness.bodies[PurposeDepartmentWorker] =
		`{"schema_version":"worker-result/v2","summary":"Grounded.",` +
			`"evidence_refs":["` + wiringDefRef + `","` + wiringAppRef + `"],` +
			`"evidence":[` +
			`{"claim":"declared","subject":"MaxDesignRounds","relation":"definition","ref":"` + wiringDefRef + `"},` +
			`{"claim":"applied","subject":"MaxDesignRounds","relation":"application","ref":"` + wiringAppRef + `"}]}`
	fixture.harness.adjudicationEvidence =
		`[{"subject":"driveDesignFreeze","relations":["definition","application"]}]`

	fixture.driveUntilStopped(t, 24)

	if !hasRoundRequirements(t, fixture, 2) {
		t.Fatal("a supplyable obligation was not adopted for round 2")
	}
}
