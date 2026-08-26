// Package cibase pins the contract of the CI base-semantics guards: WHICH
// delta each guard audits, and that the workflow actually covers the
// branches this organization works on. The properties below were the exact
// failures of the frozen-base era -- August SHAs re-auditing history on
// every PR, and a push trigger blind to fix/** branches.
package cibase

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const (
	repoRoot       = "../.."
	workflowPath   = ".github/workflows/ci.yml"
	resolverScript = "scripts/resolve-task-base.sh"
	immutabilitySc = "scripts/check-canonical-immutability.sh"
	fitnessScript  = "scripts/check-task-fitness.sh"
)

// rangeAudit recognizes campaign-pinned RANGE audits (BASE..TIP): they
// already scope themselves to their own task's delta, whatever the
// variable casing is.
var rangeAudit = regexp.MustCompile(`git diff --name-only "[^"]*\.\.[^"]*"[^\n]*-- docs/canonical`)

func repoFile(t *testing.T, rel string) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join(repoRoot, rel))
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	data, err := os.ReadFile(repoFile(t, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(data)
}

type workflowTrigger struct {
	On struct {
		Push struct {
			Branches []string `yaml:"branches"`
		} `yaml:"push"`
		PullRequest interface{} `yaml:"pull_request"`
	} `yaml:"on"`
	Jobs map[string]struct {
		Steps []struct {
			Name string   `yaml:"name"`
			Run  string   `yaml:"run"`
			Uses string   `yaml:"uses"`
			ID   string   `yaml:"id"`
			Env  struct{} `yaml:"env"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

// GUARD: the workflow covers every kind of branch this organization works
// on. R10..R16 ran entirely on fix/** branches whose pushes never triggered
// anything, so the only possible signal came from an event that was dying
// at startup.
func TestWorkflowPushTriggersCoverTheWorkingBranches(t *testing.T) {
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(readRepoFile(t, workflowPath)), &node); err != nil {
		t.Fatalf("ci.yml does not parse: %v", err)
	}
	if len(node.Content) == 0 {
		t.Fatal("empty workflow document")
	}
	// Walk the raw mapping so the plain key `on` matches by its SOURCE TEXT:
	// YAML tags it !!bool, but the file says `on:` and every parser that
	// matters reads that text.
	rootMapping := node.Content[0]
	var onNode *yaml.Node
	for i := 0; i+1 < len(rootMapping.Content); i += 2 {
		if rootMapping.Content[i].Value == "on" {
			onNode = rootMapping.Content[i+1]
		}
	}
	if onNode == nil || onNode.Kind != yaml.MappingNode {
		t.Fatal("workflow declares no `on:` trigger mapping")
	}
	var pushNode *yaml.Node
	pullRequestPresent := false
	for i := 0; i+1 < len(onNode.Content); i += 2 {
		switch onNode.Content[i].Value {
		case "push":
			pushNode = onNode.Content[i+1]
		case "pull_request":
			pullRequestPresent = true
		}
	}
	if !pullRequestPresent {
		t.Fatal("pull_request trigger missing")
	}
	if pushNode == nil || pushNode.Kind != yaml.MappingNode {
		t.Fatal("push trigger missing")
	}
	var got []string
	for i := 0; i+1 < len(pushNode.Content); i += 2 {
		if pushNode.Content[i].Value != "branches" {
			continue
		}
		for _, branch := range pushNode.Content[i+1].Content {
			got = append(got, branch.Value)
		}
	}
	want := []string{"main", "feat/**", "fix/**"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("push branches = %v, want %v", got, want)
	}
}

// GUARD: no step may audit the present against a frozen August SHA. Those
// constants turned every post-August PR red by construction and had to be
// hand-moved every few weeks.
func TestWorkflowCarriesNoFrozenBaseShas(t *testing.T) {
	wf := readRepoFile(t, workflowPath)
	for _, frozen := range []string{"3584d9e7", "a199d1ee"} {
		if strings.Contains(wf, frozen) {
			t.Fatalf("workflow still hardcodes frozen base %s", frozen)
		}
	}
}

// GUARD: both jobs resolve the change base through the shared script --
// push-to-main through the event's previous commit first -- and audit
// canonical immutability by delegating to the same script the local
// fitness checks use. Two definitions of the base would drift exactly the
// way the frozen ones did.
func TestBothJobsResolveAndDelegateTheBase(t *testing.T) {
	var wf workflowTrigger
	if err := yaml.Unmarshal([]byte(readRepoFile(t, workflowPath)), &wf); err != nil {
		t.Fatal(err)
	}
	if len(wf.Jobs) == 0 {
		t.Fatal("no jobs in workflow")
	}
	for name, job := range wf.Jobs {
		resolves, delegates := false, false
		for _, step := range job.Steps {
			if step.ID == "change_base" && strings.Contains(step.Run, "github.event.before") &&
				strings.Contains(step.Run, resolverScript) {
				resolves = true
			}
			if strings.Contains(step.Run, immutabilitySc) {
				delegates = true
			}
		}
		if !resolves || !delegates {
			t.Fatalf("job %s: resolves=%v delegatesCanonical=%v", name, resolves, delegates)
		}
	}
}

// GUARD: the fitness script consumes the shared resolver and the shared
// canonical audit instead of carrying its own frozen default and its own
// allow-list copy.
func TestTaskFitnessDelegatesBaseAndCanonicalAudit(t *testing.T) {
	script := readRepoFile(t, fitnessScript)
	if strings.Contains(script, "a199d1ee") {
		t.Fatal("check-task-fitness.sh still defaults to a frozen base")
	}
	if !strings.Contains(script, resolverScript) || !strings.Contains(script, immutabilitySc) {
		t.Fatal("check-task-fitness.sh must delegate to the shared resolver and canonical audit")
	}
	if strings.Contains(script, "unauthorized canonical change") {
		t.Fatal("the allow-list must live only in check-canonical-immutability.sh")
	}
}

func TestGuardScriptsAreExecutable(t *testing.T) {
	for _, rel := range []string{resolverScript, immutabilitySc, fitnessScript} {
		info, err := os.Stat(repoFile(t, rel))
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		if info.Mode()&0111 == 0 {
			t.Fatalf("%s is not executable", rel)
		}
	}
}

// GUARD: the allow-list and the frozen-base disease must not regrow copies.
// Every generic fitness guard that audits docs/canonical either DELEGATES to
// check-canonical-immutability.sh against a resolved base, or is a
// campaign-pinned RANGE audit (BASE..TIP) that already scopes its own task's
// delta. Only the definition itself may carry the refusal wording.
func TestGenericCanonicalAuditsDelegateOrAreRangeScoped(t *testing.T) {
	definition := "check-canonical-immutability.sh"
	matches, err := filepath.Glob(repoFile(t, "scripts/check-*-fitness.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 {
		t.Fatal("no fitness scripts found; the glob broke")
	}
	for _, path := range matches {
		base := filepath.Base(path)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		src := string(data)
		// Reading canonical POLICY files is not auditing canonical CHANGES:
		// only a diff of docs/canonical counts as an audit.
		auditsCanonical := strings.Contains(src, "docs/canonical") &&
			strings.Contains(src, "git diff --name-only")
		if !auditsCanonical {
			continue
		}
		rangePinned := rangeAudit.MatchString(src)
		switch {
		case base == definition:
			continue
		case rangePinned:
			// Campaign-pinned range: already delta-scoped by design.
			continue
		case strings.Contains(src, "unauthorized canonical change"):
			t.Fatalf("%s carries its own canonical allow-list; delegate to %s", base, definition)
		default:
			if !strings.Contains(src, "resolve-task-base.sh") {
				t.Fatalf("%s audits docs/canonical without the shared base resolver", base)
			}
		}
	}
}

// --- behavioural properties, exercised against throwaway repositories ----

func haveGit() bool { _, err := exec.LookPath("git"); return err == nil }

type gitRepo struct {
	dir string
	env []string
}

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", dir}, args...)...)
	command.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		"GIT_AUTHOR_DATE=2026-01-01T00:00:00Z", "GIT_COMMITTER_DATE=2026-01-01T00:00:00Z",
	)
	out, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeCommitAll(t *testing.T, dir, message string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-m", message)
}

// newScenario builds a repository whose main line ALREADY CARRIES a
// canonical file that the allow-list does not authorize -- committed
// historically, exactly like every artifact merged since August.
func newScenario(t *testing.T) (*gitRepo, string) {
	t.Helper()
	if !haveGit() {
		t.Skip("git is required")
	}
	root := t.TempDir()
	work := filepath.Join(root, "work")
	bare := filepath.Join(root, "origin.git")

	gitRun(t, root, "init", "-b", "main", work)
	writeCommitAll(t, work, "pre-canonical state", map[string]string{
		"README.md": "hello\n",
	})
	writeCommitAll(t, work, "canonical baseline", map[string]string{
		"docs/canonical/model-routing.yaml":          "routing: v1\n",
		"docs/canonical/q3-ontology.provenance.yaml": "provenance: merged-long-ago\n",
	})
	gitRun(t, work, "remote", "add", "origin", bare)
	gitRun(t, root, "init", "--bare", "-b", "main", bare)
	gitRun(t, work, "push", "-u", "origin", "main")
	return &gitRepo{dir: work}, bare
}

func (g *gitRepo) run(t *testing.T, script string, env map[string]string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command("bash", append([]string{script}, args...)...)
	cmd.Env = func() []string {
		base := os.Environ()
		for k, v := range env {
			base = append(base, k+"="+v)
		}
		return base
	}()
	cmd.Dir = g.dir
	out, err := cmd.CombinedOutput()
	exitCode := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		exitCode = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("run %s: %v: %s", script, err, out)
	}
	return string(out), exitCode
}

// PROPERTY 1: a canonical file already merged into main is history, not a
// present-task violation. A branch with NO new canonical changes passes,
// even though its baseline carries unauthorized-looking files.
func TestHistoricalCanonicalStateDoesNotFailABranch(t *testing.T) {
	g, _ := newScenario(t)
	gitRun(t, g.dir, "checkout", "-b", "fix/something")
	writeCommitAll(t, g.dir, "non-canonical work", map[string]string{"README.md": "changed\n"})
	out, code := g.run(t, repoFile(t, immutabilitySc), nil)
	if code != 0 {
		t.Fatalf("historical state failed the guard:\n%s", out)
	}
}

// PROPERTY 2: a NEW unauthorized canonical change in the delta fails, and
// the failure names the file.
func TestNewUnauthorizedCanonicalChangeFails(t *testing.T) {
	g, _ := newScenario(t)
	gitRun(t, g.dir, "checkout", "-b", "fix/something")
	writeCommitAll(t, g.dir, "smuggle a canonical change", map[string]string{
		"docs/canonical/evil-new-policy.yaml": "unauthorized: true\n",
	})
	out, code := g.run(t, repoFile(t, immutabilitySc), nil)
	if code == 0 {
		t.Fatalf("an unauthorized new canonical change passed:\n%s", out)
	}
	if !strings.Contains(out, "evil-new-policy.yaml") {
		t.Fatalf("refusal does not name the offending file:\n%s", out)
	}
}

// The allow-list stays scoped to the CURRENT delta: an exception file may
// still change inside a task.
func TestAllowListedExceptionInDeltaPasses(t *testing.T) {
	g, _ := newScenario(t)
	gitRun(t, g.dir, "checkout", "-b", "fix/something")
	writeCommitAll(t, g.dir, "routing update", map[string]string{
		"docs/canonical/model-routing.yaml": "routing: v2\n",
	})
	out, code := g.run(t, repoFile(t, immutabilitySc), nil)
	if code != 0 {
		t.Fatalf("an allow-listed delta change failed:\n%s", out)
	}
}

// PROPERTY 3 (base semantics): on a PR-style run, the audited delta starts
// at the MERGE-BASE with the target branch. Canonical drift that happened
// on the BASE side after the fork belongs to main's history, not to this
// branch's task.
func TestPullRequestDeltaStartsAtTheMergeBase(t *testing.T) {
	g, bare := newScenario(t)
	gitRun(t, g.dir, "checkout", "-b", "fix/something")
	// Main moves AFTER the fork: an authorized-but-historical-looking edit
	// lands on the base line.
	mainWork := filepath.Join(filepath.Dir(g.dir), "mainwork")
	gitRun(t, "", "clone", bare, mainWork)
	writeCommitAll(t, mainWork, "main-side canonical edit", map[string]string{
		"docs/canonical/q3-ontology.provenance.yaml": "provenance: edited-on-main\n",
	})
	gitRun(t, mainWork, "push", "origin", "main")
	// The branch learns about main's advance without rebasing onto it.
	gitRun(t, g.dir, "fetch", "origin")

	out, code := g.run(t, repoFile(t, immutabilitySc), map[string]string{"GITHUB_BASE_REF": "main"})
	if code != 0 {
		t.Fatalf("base-side drift was attributed to the branch:\n%s", out)
	}
}

// An explicitly configured base always wins over every inference.
func TestExplicitBaseCommitTakesPrecedence(t *testing.T) {
	g, _ := newScenario(t)
	gitRun(t, g.dir, "checkout", "-b", "fix/something")
	firstCommit := gitRun(t, g.dir, "rev-parse", "main~1")
	out, code := g.run(t, repoFile(t, immutabilitySc),
		map[string]string{"TASK_ENGINE_BASE_COMMIT": firstCommit})
	if code == 0 {
		t.Fatal("an explicitly configured historical base must reproduce the historical refusal")
	}
	if !strings.Contains(out, "q3-ontology.provenance.yaml") {
		t.Fatalf("explicit-base refusal names the wrong file:\n%s", out)
	}
}

// Running ON the mainline audits the current state against itself: the run
// must not reinterpret all of history as belonging to the present task.
func TestRunningOnMainAuditsNothingButItself(t *testing.T) {
	g, _ := newScenario(t)
	out, code := g.run(t, repoFile(t, resolverScript), nil)
	if code != 0 {
		t.Fatalf("resolver failed: %s", out)
	}
	if got, want := strings.TrimSpace(out), gitRun(t, g.dir, "rev-parse", "HEAD"); got != want {
		t.Fatalf("on main the base is HEAD itself: got %s want %s", got, want)
	}
}
