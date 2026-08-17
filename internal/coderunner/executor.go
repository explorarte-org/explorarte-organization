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

func (e Executor) path(rel string) (string, error) {
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

func (e Executor) opTimeout() time.Duration {
	if e.OperationTimeout <= 0 {
		return defaultOperationTimeout
	}
	return e.OperationTimeout
}

func (e Executor) headTail() (int, int) {
	h, t := e.HeadBudget, e.TailBudget
	if h <= 0 {
		h = defaultHeadBudget
	}
	if t <= 0 {
		t = defaultTailBudget
	}
	return h, t
}

func (e *Executor) capture() *boundedOutput {
	if e.budget == nil && e.PlanOutputBudget > 0 {
		e.budget = newOutputBudget(e.PlanOutputBudget)
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

func (e Executor) run(ctx context.Context, typ OperationType, name string, args ...string) (Result, error) {
	capture := e.capture()
	runCtx, cancel := context.WithTimeout(ctx, e.opTimeout())
	defer cancel()
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

func (e Executor) Execute(ctx context.Context, plan Plan) ([]Result, error) {
	results := make([]Result, 0, len(plan.Operations))
	for _, op := range plan.Operations {
		r, err := e.ExecuteOperation(ctx, op)
		if err != nil {
			return results, err
		}
		results = append(results, r)
		if !r.Success {
			return results, fmt.Errorf("operation %s failed", op.Type)
		}
		if e.budgetExceeded() {
			return results, fmt.Errorf("plan output budget exceeded")
		}
	}
	return results, nil
}

func (e Executor) ExecuteOperation(ctx context.Context, op Operation) (Result, error) {
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
		capture := e.capture()
		runCtx, cancel := context.WithTimeout(ctx, e.opTimeout())
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
func (e Executor) applyPatch(ctx context.Context, op Operation) (Result, error) {
	if err := validatePatchPaths(op.Patch); err != nil {
		return Result{Type: op.Type, Success: false, Output: err.Error()}, nil
	}
	checkCapture := e.capture()
	checkCtx, checkCancel := context.WithTimeout(ctx, e.opTimeout())
	_, checkErr := runSupervised(checkCtx, e.Workspace, op.Patch, checkCapture, "git", "apply", "--check", "--whitespace=nowarn", "-")
	checkCancel()
	if errors.Is(checkErr, ErrIndeterminateExecution) {
		return Result{}, fmt.Errorf("%s: %w", op.Type, ErrIndeterminateExecution)
	}
	if checkErr != nil {
		br := checkCapture.Result()
		return Result{Type: op.Type, Success: false, Output: "git apply --check failed: " + br.String()}, nil
	}
	capture := e.capture()
	runCtx, cancel := context.WithTimeout(ctx, e.opTimeout())
	defer cancel()
	code, runErr := runSupervised(runCtx, e.Workspace, op.Patch, capture, "git", "apply", "--whitespace=nowarn", "-")
	return toResult(op.Type, code, runErr, capture, nil)
}

func (e Executor) limit() int {
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
