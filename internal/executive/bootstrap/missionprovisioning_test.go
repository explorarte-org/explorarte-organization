package bootstrap

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/engineeringmission"
	"github.com/Mireuz13/explorarte-organization/internal/executive"
	"github.com/Mireuz13/explorarte-organization/internal/staging"
	"github.com/Mireuz13/explorarte-organization/internal/staging/gitexec"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

const testTargetRef = "refs/heads/v2/program-context-memory-001"

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = dir
	command.Env = append(command.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// realRepository builds a repository whose HEAD and target ref deliberately
// point at different commits, which is the only way to prove the resolver
// reads the ref rather than the checkout.
func realRepository(t *testing.T) (string, staging.RepositoryConfig, *gitexec.Backend, string, string) {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "--initial-branch=main", ".")
	git(t, dir, "commit", "--allow-empty", "-m", "target commit")
	targetSHA := git(t, dir, "rev-parse", "HEAD")
	git(t, dir, "update-ref", testTargetRef, targetSHA)
	// Move HEAD forward. The target ref stays behind.
	git(t, dir, "commit", "--allow-empty", "-m", "head commit")
	headSHA := git(t, dir, "rev-parse", "HEAD")
	if targetSHA == headSHA {
		t.Fatal("fixture did not diverge HEAD from the target ref")
	}
	backend, err := gitexec.New("/usr/bin/git", t.TempDir(), 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	repository := staging.RepositoryConfig{
		ID: "explorarte-organization", Path: dir, Enabled: true,
		AllowedTargetRefs: []string{testTargetRef},
	}
	return dir, repository, backend, targetSHA, headSHA
}

func TestResolverReadsTheGovernedRefNotHead(t *testing.T) {
	dir, repository, backend, targetSHA, headSHA := realRepository(t)
	resolver := programTargetResolver{git: backend, repository: repository, targetRef: testTargetRef}

	got, err := resolver.ResolveProgramTargetSHA(context.Background())
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != targetSHA {
		t.Fatalf("resolved %q, want the target ref %q", got, targetSHA)
	}
	if got == headSHA {
		t.Fatal("the resolver read HEAD instead of the governed ref")
	}

	// A promotion moves the target. The next mission must be based on where it
	// moved to, not on a value cached at startup.
	git(t, dir, "update-ref", testTargetRef, headSHA)
	moved, err := resolver.ResolveProgramTargetSHA(context.Background())
	if err != nil {
		t.Fatalf("resolve after move: %v", err)
	}
	if moved != headSHA {
		t.Fatalf("resolved %q after the target moved, want %q", moved, headSHA)
	}
}

func TestResolverFailsClosedOnUnknownRef(t *testing.T) {
	_, repository, backend, _, _ := realRepository(t)
	resolver := programTargetResolver{git: backend, repository: repository, targetRef: "refs/heads/does-not-exist"}
	if _, err := resolver.ResolveProgramTargetSHA(context.Background()); err == nil {
		t.Fatal("an unknown ref resolved successfully")
	}
}

func TestResolverFailsClosedOnUnknownRepositoryOrDisallowedRef(t *testing.T) {
	_, repository, backend, _, _ := realRepository(t)
	catalog := fakeCatalog{repository: repository}

	if _, err := newProgramTargetResolver(context.Background(), backend, catalog, "some-other-repo", testTargetRef); err == nil {
		t.Fatal("an unknown repository was accepted")
	}
	// A ref the repository does not already allow cannot become a target by
	// being named here.
	if _, err := newProgramTargetResolver(context.Background(), backend, catalog, repository.ID, "refs/heads/main"); err == nil {
		t.Fatal("a disallowed target ref was accepted")
	}
	if _, err := newProgramTargetResolver(context.Background(), nil, catalog, repository.ID, testTargetRef); err == nil {
		t.Fatal("a nil git backend was accepted")
	}
	if _, err := newProgramTargetResolver(context.Background(), backend, catalog, repository.ID, testTargetRef); err != nil {
		t.Fatalf("the allowed combination was refused: %v", err)
	}
}

type fakeCatalog struct{ repository staging.RepositoryConfig }

func (f fakeCatalog) List(context.Context) []staging.RepositoryView { return nil }
func (f fakeCatalog) Get(_ context.Context, id string) (staging.RepositoryConfig, string, error) {
	if id != f.repository.ID {
		return staging.RepositoryConfig{}, "", staging.ErrRepositoryDenied
	}
	return f.repository, "hash", nil
}
func (f fakeCatalog) Validate(context.Context, string) error { return nil }

// recordingCreator stands in for engineeringmission.Service, which needs
// Postgres. Idempotency there is keyed on the normalized policy digest, so the
// adapter is correct exactly when it passes the policy through untouched.
type recordingCreator struct {
	calls    []engineeringmission.MissionPolicy
	origins  []engineeringmission.MissionOrigin
	plans    []string
	byDigest map[string]int64
	nextID   int64
	err      error
}

func (r *recordingCreator) CreateIn(_ context.Context, policy engineeringmission.MissionPolicy, plan string,
	origin engineeringmission.MissionOrigin, _, _ string) (tasks.Task, error) {
	if r.err != nil {
		return tasks.Task{}, r.err
	}
	r.calls = append(r.calls, policy)
	r.origins = append(r.origins, origin)
	r.plans = append(r.plans, plan)
	_, digest, err := policy.MarshalEvidence()
	if err != nil {
		return tasks.Task{}, err
	}
	if r.byDigest == nil {
		r.byDigest = map[string]int64{}
	}
	if id, exists := r.byDigest[digest]; exists {
		return tasks.Task{ID: id}, nil
	}
	r.nextID++
	r.byDigest[digest] = r.nextID
	return tasks.Task{ID: r.nextID}, nil
}

func samplePolicy() engineeringmission.MissionPolicy {
	return engineeringmission.MissionPolicy{
		TaskID:             0,
		BaseSHA:            "d4d19cce47b0f6a1ddda20b248d9b9aa6e1843ec",
		Objective:          "record evidence",
		AllowedPaths:       []string{"docs/implementation/autonomy-smoke/AUTONOMY_SMOKE.md"},
		AcceptanceCriteria: []string{"exactly one file changes"},
		RequiredGates: []engineeringmission.RequiredGate{
			{Type: engineeringmission.GateBuild}, {Type: engineeringmission.GateVet}, {Type: engineeringmission.GateTest},
		},
	}
}

func TestProvisionerCreatesAMissionAndPassesThePolicyThrough(t *testing.T) {
	creator := &recordingCreator{}
	provisioner := missionProvisioner{missions: creator, organizationID: "explorarte"}
	policy := samplePolicy()

	record, err := provisioner.ProvisionMission(context.Background(), executive.MissionProvisionCommand{
		Policy: policy, PlanJSON: []byte(`{"schema_version":"code-runner-execution/v1","operations":[]}`),
		RequestedByRoleID: "empresa/ceo", ActorType: "service", ActorID: "principal-1",
	})
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	if record.TaskID == 0 {
		t.Fatal("no mission task id returned")
	}
	if len(creator.calls) != 1 {
		t.Fatalf("creates=%d", len(creator.calls))
	}
	// The adapter constructs no policy of its own, so it cannot change a
	// BaseSHA, widen AllowedPaths or drop a gate.
	got := creator.calls[0]
	if got.BaseSHA != policy.BaseSHA {
		t.Fatalf("base sha changed: %q -> %q", policy.BaseSHA, got.BaseSHA)
	}
	if strings.Join(got.AllowedPaths, ",") != strings.Join(policy.AllowedPaths, ",") {
		t.Fatalf("allowed paths changed: %v -> %v", policy.AllowedPaths, got.AllowedPaths)
	}
	if len(got.RequiredGates) != len(policy.RequiredGates) {
		t.Fatalf("gates changed: %v -> %v", policy.RequiredGates, got.RequiredGates)
	}
}

func TestProvisioningTheSamePolicyTwiceDoesNotDuplicateTheMission(t *testing.T) {
	creator := &recordingCreator{}
	provisioner := missionProvisioner{missions: creator, organizationID: "explorarte"}
	command := executive.MissionProvisionCommand{
		Policy: samplePolicy(), PlanJSON: []byte(`{"schema_version":"code-runner-execution/v1","operations":[]}`),
		RequestedByRoleID: "empresa/ceo", ActorType: "service", ActorID: "principal-1",
	}
	first, err := provisioner.ProvisionMission(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	second, err := provisioner.ProvisionMission(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	if first.TaskID != second.TaskID {
		t.Fatalf("the same policy produced two missions: %d and %d", first.TaskID, second.TaskID)
	}
	if len(creator.byDigest) != 1 {
		t.Fatalf("distinct missions=%d", len(creator.byDigest))
	}
}

func TestProvisionerSurfacesFailureRatherThanInventingAMission(t *testing.T) {
	creator := &recordingCreator{err: errors.New("task store unavailable")}
	provisioner := missionProvisioner{missions: creator, organizationID: "explorarte"}
	record, err := provisioner.ProvisionMission(context.Background(), executive.MissionProvisionCommand{Policy: samplePolicy()})
	if err == nil {
		t.Fatal("a failed create returned success")
	}
	if record.TaskID != 0 {
		t.Fatalf("a mission id was invented: %d", record.TaskID)
	}
}

// ---------------------------------------------------------------- wiring

func TestMissionProvisioningIsAbsentWhenUnconfigured(t *testing.T) {
	t.Setenv(missionRepositoryEnv, "")
	t.Setenv(missionTargetRefEnv, "")
	cfg := config.Config{}
	cfg.Staging.Enabled = false

	options, err := missionProvisioningOptions(cfg, nil, nil)
	if err != nil {
		t.Fatalf("an unconfigured deployment failed to start: %v", err)
	}
	if len(options) != 0 {
		t.Fatalf("options=%d, want none", len(options))
	}
}

func TestConfiguredProvisioningWithStagingDisabledRefusesToStart(t *testing.T) {
	t.Setenv(missionRepositoryEnv, "explorarte-organization")
	t.Setenv(missionTargetRefEnv, testTargetRef)
	cfg := config.Config{}
	cfg.Staging.Enabled = false

	if _, err := missionProvisioningOptions(cfg, nil, nil); err == nil {
		t.Fatal("mission provisioning was configured against disabled staging and started anyway")
	} else if !strings.Contains(err.Error(), "staging is disabled") {
		t.Fatalf("err=%v", err)
	}
}

func TestPartialProvisioningConfigurationIsInert(t *testing.T) {
	cfg := config.Config{}
	cfg.Staging.Enabled = false
	for name, pair := range map[string][2]string{
		"repository only": {"explorarte-organization", ""},
		"target only":     {"", testTargetRef},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(missionRepositoryEnv, pair[0])
			t.Setenv(missionTargetRefEnv, pair[1])
			options, err := missionProvisioningOptions(cfg, nil, nil)
			if err != nil {
				t.Fatalf("half configuration errored: %v", err)
			}
			if len(options) != 0 {
				t.Fatal("half configuration enabled mission provisioning")
			}
		})
	}
}

// The option this wiring produces is exactly what the Executive needs to stop
// blocking on mission_provisioning_unavailable: a resolver and a provisioner,
// both non-nil, applied to the orchestrator.
func TestWiringProducesTheOrchestratorOption(t *testing.T) {
	_, repository, backend, targetSHA, _ := realRepository(t)
	resolver, err := newProgramTargetResolver(context.Background(), backend, fakeCatalog{repository: repository}, repository.ID, testTargetRef)
	if err != nil {
		t.Fatal(err)
	}
	provisioner := missionProvisioner{missions: &recordingCreator{}, organizationID: "explorarte"}

	var target executive.ProgramTargetResolver = resolver
	var missions executive.MissionProvisioner = provisioner
	if target == nil || missions == nil {
		t.Fatal("the wiring produced a nil port")
	}
	got, err := target.ResolveProgramTargetSHA(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != targetSHA {
		t.Fatalf("wired resolver returned %q, want %q", got, targetSHA)
	}
	if _, err = filepath.Abs(repository.Path); err != nil {
		t.Fatal(err)
	}
}
