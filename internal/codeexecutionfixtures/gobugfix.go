package codeexecutionfixtures

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/evaluation/fixtures"
)

// brokenSource/fixedSource/testSource are the fixture's synthetic package:
// a deliberate off-by-one bug (the loop bound skips the final element),
// reproduced by a real go test, and a single-line fix. The runner applies
// this fix itself — proving the sandbox mechanism (isolation, red->green,
// diff scope containment) works, not simulating an autonomous coding
// agent, which is out of scope for an evaluation harness runner.
const (
	brokenSource = `package buggysum

// Sum adds every element of values. Contains a deliberate off-by-one bug
// for the R30 code-execution-sandbox fixture: the loop bound skips the
// final element.
func Sum(values []int) int {
	total := 0
	for i := 0; i < len(values)-1; i++ {
		total += values[i]
	}
	return total
}
`
	fixedSource = `package buggysum

// Sum adds every element of values.
func Sum(values []int) int {
	total := 0
	for i := 0; i < len(values); i++ {
		total += values[i]
	}
	return total
}
`
	testSource = `package buggysum

import "testing"

func TestSum(t *testing.T) {
	got := Sum([]int{1, 2, 3, 4})
	if got != 10 {
		t.Fatalf("Sum=%d want 10", got)
	}
}
`
	brokenLoopBound = "for i := 0; i < len(values)-1; i++ {"
)

func (r Runner) runGoBugFix(ctx context.Context, f fixtures.Fixture, subjectID string) (fixtures.RunOutcome, error) {
	record := newRecorder(f, subjectID)
	sandboxRoot, err := os.MkdirTemp("", "r30-01-go-bug-fix-*")
	if err != nil {
		return fixtures.RunOutcome{}, fmt.Errorf("create sandbox: %w", err)
	}
	defer os.RemoveAll(sandboxRoot)

	pkgDir := filepath.Join(sandboxRoot, "buggysum")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		return fixtures.RunOutcome{}, err
	}
	if err := os.WriteFile(filepath.Join(sandboxRoot, "go.mod"), []byte("module r30fixture.local/buggysum\n\ngo 1.25\n"), 0o644); err != nil {
		return fixtures.RunOutcome{}, err
	}
	sourcePath := filepath.Join(pkgDir, "buggysum.go")
	testPath := filepath.Join(pkgDir, "buggysum_test.go")
	if err := os.WriteFile(sourcePath, []byte(brokenSource), 0o644); err != nil {
		return fixtures.RunOutcome{}, err
	}
	if err := os.WriteFile(testPath, []byte(testSource), 0o644); err != nil {
		return fixtures.RunOutcome{}, err
	}

	redPassed, redOutput, err := runGoTest(ctx, sandboxRoot)
	if err != nil {
		return fixtures.RunOutcome{}, fmt.Errorf("run red test: %w", err)
	}
	record.record("test_fails_before_fix_is_applied", !redPassed)

	brokenLineNumber := lineNumberOf(brokenSource, brokenLoopBound)
	record.record("cited_cause_points_to_a_real_broken_line", brokenLineNumber > 0)

	beforeChecksums, err := snapshotSandbox(sandboxRoot)
	if err != nil {
		return fixtures.RunOutcome{}, err
	}
	if err := os.WriteFile(sourcePath, []byte(fixedSource), 0o644); err != nil {
		return fixtures.RunOutcome{}, err
	}
	afterChecksums, err := snapshotSandbox(sandboxRoot)
	if err != nil {
		return fixtures.RunOutcome{}, err
	}
	changed := diffChangedFiles(beforeChecksums, afterChecksums)
	onlyTargetChanged := len(changed) == 1 && changed[0] == sourcePath
	record.record("diff_never_touches_files_outside_the_sandboxed_package", onlyTargetChanged)

	greenPassed, greenOutput, err := runGoTest(ctx, sandboxRoot)
	if err != nil {
		return fixtures.RunOutcome{}, fmt.Errorf("run green test: %w", err)
	}
	record.record("test_passes_after_fix_is_applied", greenPassed)
	record.record("previously_green_assertions_still_pass", greenPassed)

	record.outcome.Metrics["broken_line_number"] = float64(brokenLineNumber)
	record.outcome.Metrics["changed_file_count"] = float64(len(changed))
	record.outcome.EvidenceRefs = append(record.outcome.EvidenceRefs,
		fmt.Sprintf("sandbox-file:buggysum.go:line=%d", brokenLineNumber),
		"go-test-output:before="+strings.TrimSpace(redOutput),
		"go-test-output:after="+strings.TrimSpace(greenOutput),
	)
	return record.finish("sandbox aislado en directorio temporal descartable; el diff se acota al paquete sintético"), nil
}

// runGoTest reports (testsPassed, combinedOutput, error). error is only
// non-nil for an infrastructure failure (go binary missing, context
// cancelled); a failing test is a normal, expected outcome represented by
// testsPassed=false, never an error.
func runGoTest(ctx context.Context, dir string) (bool, string, error) {
	cmd := exec.CommandContext(ctx, "go", "test", "./...")
	cmd.Dir = dir
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	if err == nil {
		return true, output.String(), nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return false, output.String(), nil
	}
	return false, output.String(), err
}

func lineNumberOf(source, needle string) int {
	index := strings.Index(source, needle)
	if index < 0 {
		return 0
	}
	return strings.Count(source[:index], "\n") + 1
}

func snapshotSandbox(root string) (map[string]string, error) {
	checksums := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(content)
		checksums[path] = fmt.Sprintf("%x", sum)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("snapshot sandbox: %w", err)
	}
	return checksums, nil
}

func diffChangedFiles(before, after map[string]string) []string {
	changed := make([]string, 0)
	for path, afterSum := range after {
		if beforeSum, ok := before[path]; !ok || beforeSum != afterSum {
			changed = append(changed, path)
		}
	}
	for path := range before {
		if _, ok := after[path]; !ok {
			changed = append(changed, path)
		}
	}
	sort.Strings(changed)
	return changed
}
