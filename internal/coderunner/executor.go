package coderunner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const defaultOperationTimeout = 5 * time.Minute

type Result struct {
	Type          OperationType
	Success       bool
	ExitCode      int
	Output        string
	BytesProduced int64
	OutputDigest  string
	Truncated     bool
}

type Executor struct {
	Workspace string
	// MaxOutput bounds the size of file content ExecuteOperation will return
	// verbatim for READ_FILE. It does not bound process-output capture,
	// which always uses HeadBudget/TailBudget regardless of MaxOutput.
	MaxOutput int
	// HeadBudget/TailBudget bound how much of a process's stdout+stderr are
	// retained (defaults: 128 KiB head, 64 KiB tail). The digest always
	// covers the complete stream.
	HeadBudget int
	TailBudget int
	// OperationTimeout bounds a single external command. It is trusted
	// runtime configuration, never something a task's Plan can raise or
	// lower. Zero means defaultOperationTimeout.
	OperationTimeout time.Duration
	// PlanOutputBudget caps the aggregate real bytes of process output a
	// single Execute call may produce across all its operations, regardless
	// of how much of that is actually retained. Zero disables the check.
	PlanOutputBudget int64

	budget *outputBudget
}

func (e *Executor) SetWorkspace(path string) { e.Workspace = path }

func (e *Executor) path(rel string) (string, error) {
	real, err := realPath(e.Workspace, rel)
	if err != nil {
		return "", err
	}
	root, rootErr := filepath.EvalSymlinks(e.Workspace)
	if rootErr != nil {
		return "", rootErr
	}
	if withinGitMetadata(root, real) {
		return "", fmt.Errorf("path resolves into .git metadata")
	}
	return real, nil
}

func (e *Executor) opTimeout() time.Duration {
	if e.OperationTimeout <= 0 {
		return defaultOperationTimeout
	}
	return e.OperationTimeout
}

func (e *Executor) headTail() (int, int) {
	h, t := e.HeadBudget, e.TailBudget
	if h <= 0 {
		h = defaultHeadBudget
	}
	if t <= 0 {
		t = defaultTailBudget
	}
	return h, t
}

// capture must only be called through the same *Executor Execute
// initialized: budget is deliberately a field mutated via pointer receiver,
// not a local/copied value, so every operation in one Execute call shares
// the identical *outputBudget instance and their real bytes produced add up
// correctly. All Executor methods use pointer receivers for exactly this
// reason -- a value receiver anywhere in this chain would silently make
// budget-sharing operate on a throwaway copy instead of e itself.
//
// cancel must be the CancelFunc for the context this specific operation is
// about to run under. capture binds it onto e.budget so that if the
// aggregate budget is crossed by a write belonging to *this* operation, the
// operation is interrupted immediately -- not merely flagged for Execute to
// notice once this operation happens to finish on its own.
func (e *Executor) capture(cancel context.CancelFunc) *boundedOutput {
	if e.budget != nil {
		e.budget.bindCancel(cancel)
	}
	h, t := e.headTail()
	return newBoundedOutput(h, t, e.budget)
}

// budgetExceeded reports whether the plan-level aggregate output budget
// (trusted runtime configuration, never task-supplied) has been exceeded by
// operations executed so far in this Execute call.
func (e *Executor) budgetExceeded() bool {
	return e.budget != nil && e.budget.exceeded()
}

func (e *Executor) run(ctx context.Context, typ OperationType, name string, args ...string) (Result, error) {
	runCtx, cancel := context.WithTimeout(ctx, e.opTimeout())
	defer cancel()
	capture := e.capture(cancel)
	code, runErr := runSupervised(runCtx, e.Workspace, "", capture, name, args...)
	return toResult(typ, code, runErr, capture, nil)
}

// successOverride lets a caller redefine what counts as "success" for exit
// codes that carry meaning beyond zero/non-zero (e.g. ripgrep's exit code 1
// for "no matches", which is not a failure). nil means the default
// zero-exit-code rule.
type successOverride func(exitCode int, runErr error) bool

func toResult(typ OperationType, exitCode int, runErr error, capture *boundedOutput, override successOverride) (Result, error) {
	if errors.Is(runErr, ErrIndeterminateExecution) {
		return Result{}, fmt.Errorf("%s: %w", typ, ErrIndeterminateExecution)
	}
	br := capture.Result()
	success := runErr == nil
	if override != nil {
		success = override(exitCode, runErr)
	}
	return Result{
		Type:          typ,
		Success:       success,
		ExitCode:      exitCode,
		Output:        br.String(),
		BytesProduced: br.TotalBytes,
		OutputDigest:  br.DigestSHA256,
		Truncated:     br.Truncated,
	}, nil
}

// Execute runs plan sequentially against a single, shared aggregate output
// budget for this call only: budget is reset here on every call so it can
// never leak across separate plans/tasks that happen to reuse the same
// *Executor instance (orgctl constructs one Executor for the whole worker
// process lifetime, not one per task). Execute is not safe to call
// concurrently on the same *Executor -- the worker never does, since a
// single worker processes one claimed task at a time.
func (e *Executor) Execute(ctx context.Context, plan Plan) ([]Result, error) {
	if e.PlanOutputBudget > 0 {
		e.budget = newOutputBudget(e.PlanOutputBudget)
	} else {
		e.budget = nil
	}
	results := make([]Result, 0, len(plan.Operations))
	for _, op := range plan.Operations {
		r, err := e.ExecuteOperation(ctx, op)
		if err != nil {
			return results, err
		}
		results = append(results, r)
		// Checked before the generic failure branch: a budget-triggered
		// mid-operation cancellation also leaves r.Success false, and the
		// more specific "plan output budget exceeded" is what actually
		// happened, not a generic operation failure.
		if e.budgetExceeded() {
			return results, fmt.Errorf("plan output budget exceeded")
		}
		if !r.Success {
			return results, operationFailure(r)
		}
	}
	return results, nil
}

func (e *Executor) ExecuteOperation(ctx context.Context, op Operation) (Result, error) {
	if err := opValidate(op); err != nil {
		return Result{}, err
	}
	if op.Path != "" && structurallyDenied(op.Path, op.Type.Mutates()) {
		return Result{}, fmt.Errorf("path %q is structurally denied", op.Path)
	}
	switch op.Type {
	case ReadFile:
		p, err := e.path(op.Path)
		if err != nil {
			return Result{}, err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return Result{}, err
		}
		if len(b) > e.limit() {
			return Result{}, fmt.Errorf("output limit")
		}
		return Result{Type: op.Type, Success: true, Output: string(b), BytesProduced: int64(len(b))}, nil
	case Gofmt:
		p, err := e.path(op.Path)
		if err != nil {
			return Result{}, err
		}
		return e.run(ctx, op.Type, "go", "fmt", p)
	case GoBuild:
		pkgs, err := packages(op.Packages)
		if err != nil {
			return Result{}, err
		}
		return e.run(ctx, op.Type, "go", append([]string{"build"}, pkgs...)...)
	case GoVet:
		pkgs, err := packages(op.Packages)
		if err != nil {
			return Result{}, err
		}
		return e.run(ctx, op.Type, "go", append([]string{"vet"}, pkgs...)...)
	case GoTest:
		pkgs, err := packages(op.Packages)
		if err != nil {
			return Result{}, err
		}
		args := append([]string{"test"}, pkgs...)
		if op.Race {
			args = append(args, "-race")
		}
		return e.run(ctx, op.Type, "go", args...)
	case GitStatus:
		return e.run(ctx, op.Type, "git", "status", "--short")
	case GitDiff:
		return e.run(ctx, op.Type, "git", "diff", "--no-ext-diff", "--")
	case Search:
		p, err := e.path(op.Path)
		if err != nil {
			return Result{}, err
		}
		runCtx, cancel := context.WithTimeout(ctx, e.opTimeout())
		capture := e.capture(cancel)
		code, runErr := runSupervised(runCtx, e.Workspace, "", capture, "rg", "--fixed-strings", "--line-number", "--max-count", "100", "--glob", "!/.git", op.Pattern, p)
		cancel()
		// rg's exit code 1 means "ran fine, no matches" -- not a failure.
		return toResult(op.Type, code, runErr, capture, func(exitCode int, runErr error) bool {
			return runErr == nil || exitCode == 1
		})
	case ApplyPatch:
		return e.applyPatch(ctx, op)
	default:
		return Result{}, fmt.Errorf("unsupported operation")
	}
}

// applyPatch enforces confinement on every path the diff touches, then
// requires `git apply --check` to succeed before the real, mutating
// `git apply` is ever run. A patch rejected by either step never reaches
// the workspace and is reported as a deterministic failure, not retried as
// an infrastructure error. git's own path protections are never the sole
// guard: validatePatchPaths runs first and independently.
func (e *Executor) applyPatch(ctx context.Context, op Operation) (Result, error) {
	if err := validatePatchPaths(op.Patch); err != nil {
		return Result{Type: op.Type, Success: false, Output: err.Error()}, nil
	}
	checkCtx, checkCancel := context.WithTimeout(ctx, e.opTimeout())
	checkCapture := e.capture(checkCancel)
	_, checkErr := runSupervised(checkCtx, e.Workspace, op.Patch, checkCapture, "git", "apply", "--check", "--whitespace=nowarn", "-")
	checkCancel()
	if errors.Is(checkErr, ErrIndeterminateExecution) {
		return Result{}, fmt.Errorf("%s: %w", op.Type, ErrIndeterminateExecution)
	}
	if checkErr != nil {
		br := checkCapture.Result()
		return Result{Type: op.Type, Success: false, Output: "git apply --check failed: " + br.String()}, nil
	}
	runCtx, cancel := context.WithTimeout(ctx, e.opTimeout())
	defer cancel()
	capture := e.capture(cancel)
	code, runErr := runSupervised(runCtx, e.Workspace, op.Patch, capture, "git", "apply", "--whitespace=nowarn", "-")
	return toResult(op.Type, code, runErr, capture, nil)
}

func (e *Executor) limit() int {
	if e.MaxOutput <= 0 {
		return 1 << 20
	}
	return e.MaxOutput
}

func packages(p []string) ([]string, error) {
	if len(p) == 0 {
		return []string{"./..."}, nil
	}
	for _, v := range p {
		if err := validatePackage(v); err != nil {
			return nil, err
		}
	}
	return p, nil
}

// validatePackage accepts Go's own package-pattern syntax, including the
// "..." wildcard (e.g. "./...", "./cmd/...") which is the ordinary way to
// mean "this directory and everything under it" -- CodeRunner's own default
// package list already resolves to exactly that. It rejects actual parent
// traversal by checking path segments exactly equal to ".." rather than a
// raw substring match, since a naive `strings.Contains(v, "..")` also
// matches the harmless "..." wildcard and would make `go test ./...` -- the
// single most common Go invocation -- unusable.
func validatePackage(v string) error {
	if v == "" || !strings.HasPrefix(v, "./") || strings.ContainsAny(v, " \t\n;") {
		return fmt.Errorf("unsafe package path")
	}
	for _, seg := range strings.Split(v, "/") {
		if seg == ".." {
			return fmt.Errorf("unsafe package path")
		}
	}
	return nil
}

func opValidate(op Operation) error {
	switch op.Type {
	case ReadFile, Gofmt:
		if op.Path == "" {
			return fmt.Errorf("path required")
		}
	case Search:
		if op.Path == "" || op.Pattern == "" {
			return fmt.Errorf("search path/pattern required")
		}
	case ApplyPatch:
		if strings.TrimSpace(op.Patch) == "" {
			return fmt.Errorf("patch required")
		}
	}
	return nil
}

// failureExcerptBytes bounds what a failed operation contributes to the
// recorded reason.
const failureExcerptBytes = 1500

// failureMarkers are how Go's own tools announce what went wrong: go test
// prefixes a failed package with FAIL and a failed assertion with --- FAIL,
// go build and go vet prefix a package's diagnostics with #, and the
// compiler's own lines carry error:.
//
// Matching on them is content-aware, which is a cost worth naming. It is
// bounded by falling back to the tail whenever no marker is present, so a
// command whose failures look like nothing here is reported exactly as it was
// before.
var failureMarkers = []string{"FAIL", "--- FAIL", "# ", "error:", "panic:"}

// operationFailure reports a failed operation with what the command actually
// said.
//
// The type alone was all that got recorded, so a gate failure was
// unfalsifiable from outside: "operation GO_TEST failed" is consistent with a
// broken test, an unwritable cache, a missing toolchain and an out-of-space
// device, and distinguishes none of them. Diagnosing one meant reconstructing
// the workspace by hand and guessing at the difference, which is how an hour
// went into discovering that the tests passed in eighty-eight seconds.
//
// The Result carrying exit code and output was already in hand at this exact
// line. It was simply dropped.
func operationFailure(r Result) error {
	excerpt := failureExcerpt(r.Output)
	if excerpt == "" {
		// A command that failed silently is itself the finding, and saying
		// so beats an empty quotation that reads like missing data.
		return fmt.Errorf("operation %s failed with exit code %d and produced no output", r.Type, r.ExitCode)
	}
	return fmt.Errorf("operation %s failed with exit code %d: %s", r.Type, r.ExitCode, excerpt)
}

// failureExcerpt keeps the part of the output that says what went wrong.
//
// Keeping the tail alone was the first attempt, on the reasoning that a tool
// puts its summary at the end. That is true of a compiler and of a single
// package. It is false of go test ./..., which prints one line per package as
// each finishes and has no final summary -- so the tail is the alphabetical
// remainder of the packages that PASSED, and the one line naming the failure
// sits in the middle and was cut. A real gate failure was recorded that way:
// exit code 1, followed by fourteen hundred bytes of "ok".
//
// A failure is a block, not a line. "# package" is followed by the compiler
// diagnostics that say what is actually wrong, and "--- FAIL" is followed by
// the assertion that actually failed; keeping only the marker throws away the
// half that carries the information. So a block runs from its marker until
// the next package-status line, and every block is kept.
//
// Falling back to the tail when nothing is marked is what keeps this from
// being a heuristic that can lose the output: a command whose failures look
// like nothing recognised is reported exactly as it was before.
func failureExcerpt(output string) string {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return ""
	}
	var kept []string
	inBlock := false
	for _, line := range strings.Split(trimmed, "\n") {
		switch {
		case isFailureLine(line):
			inBlock = true
		case endsFailureBlock(line):
			inBlock = false
		}
		if inBlock {
			kept = append(kept, line)
		}
	}
	if len(kept) == 0 {
		return boundedTail(trimmed)
	}
	// Kept from the END, because when a run produces more failures than
	// fit, the later ones are the ones no earlier excerpt has shown.
	return boundedTail(strings.Join(kept, "\n"))
}

func isFailureLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	for _, marker := range failureMarkers {
		if strings.HasPrefix(trimmed, marker) {
			return true
		}
	}
	return false
}

// endsFailureBlock reports a line that announces a package's result, which is
// where one failure's details stop and the next package's story starts.
func endsFailureBlock(line string) bool {
	return strings.HasPrefix(line, "ok  \t") || strings.HasPrefix(line, "ok\t") ||
		strings.HasPrefix(line, "?   \t") || strings.HasPrefix(line, "?\t")
}

func boundedTail(text string) string {
	if len(text) <= failureExcerptBytes {
		return text
	}
	return "..." + text[len(text)-failureExcerptBytes:]
}
