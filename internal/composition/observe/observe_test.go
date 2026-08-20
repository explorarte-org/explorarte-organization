package observe

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/composition"
	"github.com/Mireuz13/explorarte-organization/internal/modelegress"
	"github.com/Mireuz13/explorarte-organization/internal/platform/buildinfo"
)

type fakeEgress struct {
	status modelegress.RegistryStatus
	err    error
}

func (f fakeEgress) Status(context.Context) (modelegress.RegistryStatus, error) {
	return f.status, f.err
}

type fakeSchema struct {
	tip int64
	err error
}

func (f fakeSchema) DatabaseSchemaTip(context.Context) (int64, error) { return f.tip, f.err }

type fakeDesired struct {
	sha string
	err error
}

func (f fakeDesired) DesiredSHA(context.Context) (string, error) { return f.sha, f.err }

func healthyObserver() Observer {
	return Observer{
		Egress:  fakeEgress{status: modelegress.RegistryStatus{OrganizationRevisionID: 19, Synchronized: true}},
		Schema:  fakeSchema{tip: 55},
		Desired: fakeDesired{sha: "abc123"},
		Build:   buildinfo.Info{Commit: "abc123", MigrationTip: 55},
	}
}

func TestAHealthyDeploymentObservesEveryKeyAndIsAdmitted(t *testing.T) {
	result := healthyObserver().Observe(context.Background())
	if len(result.Unobserved) != 0 {
		t.Fatalf("nothing should be missing: %v", result.Unobserved)
	}
	g, err := composition.Baseline()
	if err != nil {
		t.Fatal(err)
	}
	_, refused := g.Admissible(result.Observation)
	if len(refused) != 0 {
		t.Fatalf("a healthy deployment must admit every component: %v", refused)
	}
	converged, diverged := g.Converged(result.Observation)
	if !converged {
		t.Fatalf("the fleet is running the promoted build: %v", diverged)
	}
}

// This is the state that has actually taken production down: the registry has
// a current revision and the egress policy is not bound to it. The status
// says the binding is not current but not which revision it is on, so the
// only honest report is that the binding was not observed.
func TestAnUnboundRevisionIsReportedAsUnobservedNotGuessed(t *testing.T) {
	o := healthyObserver()
	o.Egress = fakeEgress{status: modelegress.RegistryStatus{OrganizationRevisionID: 19, Synchronized: false}}
	result := o.Observe(context.Background())

	if got, ok := result.Observation[composition.KeyEgressBoundRevision]; ok {
		t.Fatalf("no number may be invented for a binding nobody read: got %q", got)
	}
	if got := result.Observation[composition.KeyOrganizationRevision]; got != "19" {
		t.Fatalf("the revision itself was readable and must be reported: %q", got)
	}
	reason := result.Unobserved[composition.KeyEgressBoundRevision]
	if !strings.Contains(reason, "not bound") {
		t.Fatalf("the reason must say what is wrong: %q", reason)
	}

	g, err := composition.Baseline()
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Admit("runtime-orgd", result.Observation); err == nil {
		t.Fatal("an unobserved binding must deny admission")
	} else if !strings.Contains(err.Error(), "not observed") {
		t.Fatalf("the refusal must say it was never observed: %v", err)
	}
}

// One unreachable store must not blind the whole pass. The keys that were
// readable still gate what they gate.
func TestAFailedReaderCostsOnlyItsOwnKeys(t *testing.T) {
	o := healthyObserver()
	o.Schema = fakeSchema{err: errors.New("connection refused")}
	result := o.Observe(context.Background())

	if _, ok := result.Observation[composition.KeyDatabaseSchemaTip]; ok {
		t.Fatal("a failed read must not produce a value")
	}
	if !strings.Contains(result.Unobserved[composition.KeyDatabaseSchemaTip], "connection refused") {
		t.Fatalf("the underlying error must survive into the reason: %q", result.Unobserved[composition.KeyDatabaseSchemaTip])
	}
	for _, k := range []composition.Key{
		composition.KeyOrganizationRevision, composition.KeyEgressBoundRevision,
		composition.KeyRuntimeObservedSHA, composition.KeyRuntimeDesiredSHA,
	} {
		if _, ok := result.Observation[k]; !ok {
			t.Errorf("%s was readable and must still have been read", k)
		}
	}
}

func TestAMissingReaderIsAFactNotAnError(t *testing.T) {
	result := Observer{Build: buildinfo.Info{Commit: "abc123", MigrationTip: 55}}.Observe(context.Background())
	// The binary still knows itself.
	if result.Observation[composition.KeyRuntimeObservedSHA] != "abc123" {
		t.Fatal("local knowledge needs no reader")
	}
	if got := len(result.Missing()); got != 4 {
		t.Fatalf("every key needing a reader must be reported missing, got %d: %v", got, result.Missing())
	}
	for _, k := range result.Missing() {
		if !strings.Contains(result.Unobserved[k], "no ") {
			t.Errorf("%s must say a reader is absent: %q", k, result.Unobserved[k])
		}
	}
}

// A binary compiled at 55 against a database at 56 is the deployment drift
// that broke the fleet mid-cutover. It is now a refusal with both numbers in
// it rather than a schema mismatch discovered by a dying service.
func TestADeploymentAheadOfItsBinaryIsRefusedWithBothNumbers(t *testing.T) {
	o := healthyObserver()
	o.Schema = fakeSchema{tip: 56}
	result := o.Observe(context.Background())

	g, err := composition.Baseline()
	if err != nil {
		t.Fatal(err)
	}
	err = g.Admit("runtime-orgd", result.Observation)
	if err == nil {
		t.Fatal("a binary that cannot speak the database's schema must not be admitted")
	}
	for _, want := range []string{"database.schema.tip=56", "runtime.schema.compatibility=[55]"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must carry %q: %v", want, err)
		}
	}
}

func TestDivergenceIsObservedWithoutDenyingAdmission(t *testing.T) {
	o := healthyObserver()
	o.Desired = fakeDesired{sha: "def456"}
	result := o.Observe(context.Background())

	g, err := composition.Baseline()
	if err != nil {
		t.Fatal(err)
	}
	converged, diverged := g.Converged(result.Observation)
	if converged {
		t.Fatal("a promoted build the fleet has not picked up is divergence")
	}
	if len(diverged) != 1 || !strings.Contains(diverged[0], "def456") {
		t.Fatalf("divergence must name the build that is owed: %v", diverged)
	}
	// The fleet running the previous build is still entitled to serve.
	if err := g.Admit("runtime-orgd", result.Observation); err != nil {
		t.Fatalf("owing a transition is not losing the right to run: %v", err)
	}
}
