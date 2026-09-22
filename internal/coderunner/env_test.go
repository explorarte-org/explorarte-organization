package coderunner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// subprocessEnv keeps exactly the allowlisted names, dropping everything else --
// in particular, anything shaped like this codebase's own credentials.
func TestSubprocessEnvKeepsOnlyTheAllowlist(t *testing.T) {
	environ := []string{
		"PATH=/usr/bin:/bin",
		"HOME=/home/coderunner",
		"GOCACHE=/tmp/go-cache",
		"GOMODCACHE=/tmp/go-mod",
		"GOFLAGS=-mod=mod",
		"GOTOOLCHAIN=local",
		"ORG_DATABASE_HOST=postgres",
		"ORG_DATABASE_PASSWORD=super-secret",
		"ORG_DATABASE_USER=explorarte_app",
		"ORG_STAGING_WORKSPACE_ROOT=/var/lib/explorarte/staging/workspaces",
		"ORG_TASK_ORGANIZATION_ID=explorarte",
		"AWS_SECRET_ACCESS_KEY=whatever",
		"HOSTNAME=1bc108465436",
		"PWD=/go",
	}
	got := subprocessEnv(environ)
	want := []string{
		"PATH=/usr/bin:/bin", "HOME=/home/coderunner", "GOCACHE=/tmp/go-cache",
		"GOMODCACHE=/tmp/go-mod", "GOFLAGS=-mod=mod", "GOTOOLCHAIN=local",
	}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("subprocessEnv = %v, want %v", got, want)
	}
	for _, kv := range got {
		if strings.HasPrefix(kv, "ORG_") || strings.Contains(strings.ToUpper(kv), "SECRET") || strings.Contains(strings.ToUpper(kv), "PASSWORD") {
			t.Fatalf("a credential-shaped variable survived filtering: %q", kv)
		}
	}
}

// A name absent from environ never appears in the result -- subprocessEnv reads
// what is there, it does not synthesize an allowlisted variable that was never set.
func TestSubprocessEnvNeverInventsAnAbsentVariable(t *testing.T) {
	got := subprocessEnv([]string{"PATH=/bin", "ORG_DATABASE_HOST=postgres"})
	if len(got) != 1 || got[0] != "PATH=/bin" {
		t.Fatalf("subprocessEnv = %v, want exactly [PATH=/bin]", got)
	}
}

// Malformed entries (no "=") are dropped, not misread as a bare name.
func TestSubprocessEnvIgnoresMalformedEntries(t *testing.T) {
	got := subprocessEnv([]string{"PATH=/bin", "GARBAGE_NO_EQUALS", "HOME"})
	if len(got) != 1 || got[0] != "PATH=/bin" {
		t.Fatalf("subprocessEnv = %v", got)
	}
}

// A value that legitimately contains "=" (GOFLAGS can) is kept whole: only the
// FIRST "=" splits the name from the value.
func TestSubprocessEnvKeepsEmbeddedEqualsInTheValue(t *testing.T) {
	got := subprocessEnv([]string{"GOFLAGS=-ldflags=-X main.version=1"})
	if len(got) != 1 || got[0] != "GOFLAGS=-ldflags=-X main.version=1" {
		t.Fatalf("subprocessEnv = %v", got)
	}
}

// Empty input yields an empty (non-nil-panicking) result.
func TestSubprocessEnvOfEmptyEnvironIsEmpty(t *testing.T) {
	if got := subprocessEnv(nil); len(got) != 0 {
		t.Fatalf("subprocessEnv(nil) = %v", got)
	}
}

// The production root cause, reproduced directly: a credential this PACKAGE's own
// process holds does not reach a command runSupervised runs, even though nothing
// in runSupervised's signature says so -- it is enforced once, for every operation
// type, at the single choke point every operation goes through.
func TestRunSupervisedNeverLeaksTheCallersEnvironmentToTheCommand(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh required")
	}
	t.Setenv("ORG_DATABASE_PASSWORD", "leaked-if-this-test-fails")
	t.Setenv("ORG_DATABASE_HOST", "postgres")

	out := newBoundedOutput(0, 0, nil)
	_, err := runSupervised(context.Background(), t.TempDir(), "", out,
		"sh", "-c", `if [ -n "$ORG_DATABASE_PASSWORD" ] || [ -n "$ORG_DATABASE_HOST" ]; then echo LEAKED; exit 1; fi; echo CLEAN`)
	if err != nil {
		t.Fatalf("command failed: %v, output=%s", err, out.Result().String())
	}
	if got := out.Result().String(); strings.TrimSpace(got) != "CLEAN" {
		t.Fatalf("the code-runner's own credential reached the subprocess: %q", got)
	}
}

// The other side of the same guarantee: a command that NEEDS something on the
// allowlist (here PATH, to find `sh` itself) still gets it. A test that only
// proved secrets are blocked could pass by blocking everything.
func TestRunSupervisedStillProvidesWhatTheAllowlistPermits(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh required")
	}
	t.Setenv("GOCACHE", filepath.Join(t.TempDir(), "gocache"))
	out := newBoundedOutput(0, 0, nil)
	_, err := runSupervised(context.Background(), t.TempDir(), "", out,
		"sh", "-c", `if [ -z "$PATH" ] || [ -z "$GOCACHE" ]; then echo MISSING; exit 1; fi; echo PRESENT`)
	if err != nil {
		t.Fatalf("command failed: %v, output=%s", err, out.Result().String())
	}
	if got := out.Result().String(); strings.TrimSpace(got) != "PRESENT" {
		t.Fatalf("an allowlisted variable did not reach the subprocess: %q", got)
	}
}

// End to end, against the real toolchain (skipped where go/git are not on PATH,
// as CI's unit-test job already has them): the whole plan CodeRunner actually
// executes -- apply, build, vet, test -- succeeds under the explicit environment,
// against a tiny real module, with only the allowlist visible to every step.
func TestExecutorRunsARealPlanUnderTheExplicitEnvironment(t *testing.T) {
	for _, bin := range []string{"go", "git"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s required", bin)
		}
	}
	dir := t.TempDir()
	mustWrite := func(rel, content string) {
		t.Helper()
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("go.mod", "module fixture\n\ngo 1.25\n")
	mustWrite("add.go", "package fixture\n\nfunc Add(a, b int) int { return a + b }\n")
	mustWrite("add_test.go", "package fixture\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(2, 3) != 5 {\n\t\tt.Fatal(\"wrong\")\n\t}\n}\n")
	run := func(t *testing.T, dir, name string, args ...string) {
		t.Helper()
		out := newBoundedOutput(0, 0, nil)
		code, err := runSupervised(context.Background(), dir, "", out, name, args...)
		if err != nil || code != 0 {
			t.Fatalf("%s %v: code=%d err=%v output=%s", name, args, code, err, out.Result().String())
		}
	}
	// A credential the real code-runner process holds for its own purposes, exactly
	// as it does in production -- must not change whether this succeeds, and the
	// commands below never see it.
	t.Setenv("ORG_DATABASE_PASSWORD", "must-not-reach-go-or-git")
	run(t, dir, "go", "build", "./...")
	run(t, dir, "go", "vet", "./...")
	run(t, dir, "go", "test", "-count=1", "./...")
}
