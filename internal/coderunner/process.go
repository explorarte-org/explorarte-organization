package coderunner

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"
)

// ErrIndeterminateExecution marks a run whose side effects on the staging
// workspace could not be proven complete. It is returned whenever a process
// group had to be killed (operation timeout, heartbeat/lease loss, parent
// shutdown) and the wait for that kill to actually reap the tree did not
// complete inside killGrace. Callers MUST treat this as fail-closed: never
// success, never an ordinary retryable failure, and the workspace it touched
// must never be sealed as a candidate.
var ErrIndeterminateExecution = errors.New("indeterminate_code_execution")

// killGrace bounds how long runSupervised waits for a killed process group
// to be reaped before giving up and reporting ErrIndeterminateExecution. It
// is deliberately small and fixed: a process that ignores SIGKILL for this
// long (almost always an uninterruptible-sleep descendant, e.g. blocked
// disk I/O) cannot be waited out indefinitely without violating the
// "bounded wait" requirement, so the caller must move on with an explicit
// unknown-outcome result instead of blocking the worker forever.
const killGrace = 5 * time.Second

// runSupervised runs name/args under dir in its own process group/session so
// that cancellation can reach the whole descendant tree, not just the direct
// child. stdout/stderr are captured into out (safe for concurrent writes).
// If stdin is non-empty it is fed to the process (used by ApplyPatch, the
// only operation that streams input rather than passing it as an argument).
// runCtx bounds the run: on cancellation runSupervised kills the entire
// process group and waits up to killGrace for it to be reaped before
// returning ErrIndeterminateExecution. It never returns while the process
// group might still be running, except in that explicit fail-closed case.
func runSupervised(runCtx context.Context, dir string, stdin string, out *boundedOutput, name string, args ...string) (exitCode int, err error) {
	c := exec.Command(name, args...)
	c.Dir = dir
	c.Stdout = out
	c.Stderr = out
	if stdin != "" {
		c.Stdin = strings.NewReader(stdin)
	}
	setProcessGroup(c)

	if startErr := c.Start(); startErr != nil {
		return -1, startErr
	}

	done := make(chan error, 1)
	go func() { done <- c.Wait() }()

	select {
	case waitErr := <-done:
		return exitCodeOf(waitErr)
	case <-runCtx.Done():
		_ = killProcessGroup(c)
		select {
		case waitErr := <-done:
			code, _ := exitCodeOf(waitErr)
			return code, runCtx.Err()
		case <-time.After(killGrace):
			return -1, ErrIndeterminateExecution
		}
	}
}

// exitCodeOf turns a Wait() error into (exit code, error). A non-zero exit
// still yields a non-nil error: the caller derives Success solely from
// whether err == nil, exactly as a zero exit code does for every operation
// type, so a failing `go test`/`go vet`/`git apply --check` is never
// mistaken for success.
func exitCodeOf(err error) (int, error) {
	if err == nil {
		return 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), err
	}
	return -1, err
}
