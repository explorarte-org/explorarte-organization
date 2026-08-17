package coderunner

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Result struct {
	Type     OperationType
	Success  bool
	ExitCode int
	Output   string
}
type Executor struct {
	Workspace string
	MaxOutput int
}

func (e *Executor) SetWorkspace(path string) { e.Workspace = path }

func (e Executor) path(rel string) (string, error) {
	if err := SafePath(rel); err != nil {
		return "", err
	}
	root, err := filepath.EvalSymlinks(e.Workspace)
	if err != nil {
		return "", err
	}
	p := filepath.Join(root, rel)
	real, err := filepath.EvalSymlinks(filepath.Dir(p))
	if err != nil {
		return "", err
	}
	if real != root && !strings.HasPrefix(real, root+string(os.PathSeparator)) {
		return "", fmt.Errorf("workspace escape")
	}
	return p, nil
}
func (e Executor) run(ctx context.Context, typ OperationType, args ...string) Result {
	c := exec.CommandContext(ctx, "go", args...)
	c.Dir = e.Workspace
	out, err := c.CombinedOutput()
	code := 0
	if x, ok := err.(*exec.ExitError); ok {
		code = x.ExitCode()
	} else if err != nil {
		code = -1
	}
	return Result{Type: typ, Success: err == nil, ExitCode: code, Output: truncate(string(out), e.limit())}
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
	}
	return results, nil
}
func (e Executor) ExecuteOperation(ctx context.Context, op Operation) (Result, error) {
	if err := opValidate(op); err != nil {
		return Result{}, err
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
		return Result{Type: op.Type, Success: true, Output: string(b)}, nil
	case Gofmt:
		p, err := e.path(op.Path)
		if err != nil {
			return Result{}, err
		}
		return e.run(ctx, op.Type, "fmt", p), nil
	case GoBuild:
		return e.run(ctx, op.Type, append([]string{"build"}, packages(op.Packages)...)...), nil
	case GoVet:
		return e.run(ctx, op.Type, append([]string{"vet"}, packages(op.Packages)...)...), nil
	case GoTest:
		args := append([]string{"test"}, packages(op.Packages)...)
		if op.Race {
			args = append(args, "-race")
		}
		return e.run(ctx, op.Type, args...), nil
	case GitStatus:
		return e.git(ctx, op.Type, "status", "--short"), nil
	case GitDiff:
		return e.git(ctx, op.Type, "diff", "--no-ext-diff", "--"), nil
	case Search:
		p, err := e.path(op.Path)
		if err != nil {
			return Result{}, err
		}
		c := exec.CommandContext(ctx, "rg", "--fixed-strings", "--line-number", "--max-count", "100", op.Pattern, p)
		c.Dir = e.Workspace
		b, err := c.CombinedOutput()
		code := 0
		if x, ok := err.(*exec.ExitError); ok {
			code = x.ExitCode()
		}
		return Result{Type: op.Type, Success: err == nil || code == 1, ExitCode: code, Output: truncate(string(b), e.limit())}, nil
	case ApplyPatch:
		c := exec.CommandContext(ctx, "git", "apply", "--whitespace=nowarn", "-")
		c.Dir = e.Workspace
		c.Stdin = strings.NewReader(op.Patch)
		b, err := c.CombinedOutput()
		code := 0
		if x, ok := err.(*exec.ExitError); ok {
			code = x.ExitCode()
		}
		return Result{Type: op.Type, Success: err == nil, ExitCode: code, Output: truncate(string(b), e.limit())}, nil
	default:
		return Result{}, fmt.Errorf("unsupported operation")
	}
}
func (e Executor) git(ctx context.Context, t OperationType, args ...string) Result {
	c := exec.CommandContext(ctx, "git", args...)
	c.Dir = e.Workspace
	b, err := c.CombinedOutput()
	code := 0
	if x, ok := err.(*exec.ExitError); ok {
		code = x.ExitCode()
	}
	return Result{Type: t, Success: err == nil, ExitCode: code, Output: truncate(string(b), e.limit())}
}
func (e Executor) limit() int {
	if e.MaxOutput <= 0 {
		return 1 << 20
	}
	return e.MaxOutput
}
func packages(p []string) []string {
	if len(p) == 0 {
		return []string{"./..."}
	}
	for _, v := range p {
		if err := validatePackage(v); err != nil {
			return []string{"__invalid_package__"}
		}
	}
	return p
}

func validatePackage(v string) error {
	if v == "" || !strings.HasPrefix(v, "./") || strings.Contains(v, "..") || strings.ContainsAny(v, " \t\n;") {
		return fmt.Errorf("unsafe package path")
	}
	return nil
}
func truncate(v string, max int) string {
	if len(v) <= max {
		return v
	}
	return v[:max]
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
