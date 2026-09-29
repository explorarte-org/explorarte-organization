package coderunner

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// External audit A1, step B: GO_TEST runs in a separate executor, on a copy of the workspace without
// .git, and its result is authenticated so a test cannot report its own success.

func isolatedFixture(t *testing.T, testBody string) (workspace, exchange string) {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go required")
	}
	workspace, exchange = t.TempDir(), t.TempDir()
	for rel, content := range map[string]string{
		"go.mod":               "module fixture\n\ngo 1.25\n",
		".git/HEAD":            "ref: refs/heads/main\n",
		"internal/x/x.go":      "package x\n\nfunc One() int { return 1 }\n",
		"internal/x/x_test.go": "package x\n\nimport (\n\t\"os\"\n\t\"path/filepath\"\n\t\"testing\"\n)\n\nvar _ = os.Stat\nvar _ = filepath.Join\n\n" + testBody,
	} {
		full := filepath.Join(workspace, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return workspace, exchange
}

func runIsolated(t *testing.T, workspace, exchange string, args []string) (Result, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- (IsolatedTestExecutor{Root: exchange}).RunOne(ctx) }()
	result, err := (IsolatedTests{Root: exchange, Grace: 10 * time.Second}).RunGoTest(ctx, workspace, args, time.Minute, 64<<10, 32<<10)
	if executorErr := <-done; executorErr != nil {
		t.Fatalf("executor: %v", executorErr)
	}
	return result, err
}

func TestAnIsolatedTestRunsOnACopyWithoutGit(t *testing.T) {
	workspace, exchange := isolatedFixture(t, `func TestOne(t *testing.T) {
	if One() != 1 {
		t.Fatal("wrong")
	}
	if _, err := os.Stat(filepath.Join("..", "..", ".git")); err == nil {
		t.Fatal("the executor's copy carries .git")
	}
}
`)
	result, err := runIsolated(t, workspace, exchange, []string{"./..."})
	if err != nil || !result.Success || !slices.Equal(result.Command, []string{"go", "test", "./..."}) {
		t.Fatalf("result %+v err %v, want a passing go test ./...", result, err)
	}
	if entries, _ := os.ReadDir(filepath.Join(exchange, isolatedJobsDir)); len(entries) != 0 {
		t.Fatalf("the job was left behind: %v", entries)
	}
}

// A test that writes its own result file cannot report success: it cannot know the nonce.
func TestAnIsolatedTestCannotReportItsOwnSuccess(t *testing.T) {
	workspace, exchange := isolatedFixture(t, `func TestForge(t *testing.T) {
	forged := []byte(`+"`"+`{"exit_code":0,"success":true,"output":"ok","mac":"00"}`+"`"+`)
	_ = os.WriteFile(filepath.Join("..", "..", "..", "result.json"), forged, 0o600)
	t.Fatal("this test fails")
}
`)
	result, err := runIsolated(t, workspace, exchange, []string{"./..."})
	if err == nil && result.Success {
		t.Fatalf("a failing test that forged its result was reported as passing: %+v", result)
	}
	if err != nil && !errors.Is(err, ErrIndeterminateExecution) {
		t.Fatalf("err = %v, want indeterminate or a failed result", err)
	}
}

func TestTheExecutorRefusesArgumentsTheControllerNeverSends(t *testing.T) {
	workspace, exchange := isolatedFixture(t, "func TestOne(t *testing.T) {}\n")
	result, err := runIsolated(t, workspace, exchange, []string{"-exec=/bin/sh", "./..."})
	if err != nil || result.Success || !strings.Contains(result.Output, "refused") {
		t.Fatalf("result %+v err %v, want a refused, failed run", result, err)
	}
}

func TestTheExecutorsTestEnvironmentDisablesTestCaching(t *testing.T) {
	env := isolatedTestEnv([]string{"PATH=/usr/bin", "GOFLAGS=-mod=mod", "ORG_DATABASE_PASSWORD=secret"})
	if !slices.Contains(env, "GOFLAGS=-mod=mod -count=1") || slices.ContainsFunc(env, func(kv string) bool { return strings.HasPrefix(kv, "ORG_DATABASE") }) {
		t.Fatalf("executor environment %v", env)
	}
}
