//go:build !windows

package alibabaclaude

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

type cliRunRequest struct {
	Executable     string
	Args           []string
	Env            []string
	Dir            string
	Stdin          []byte
	MaxStdoutBytes int
	MaxStderrBytes int
	Timeout        time.Duration
	KillGrace      time.Duration
}

type boundedBuffer struct {
	mu       sync.Mutex
	limit    int
	body     []byte
	overflow bool
}

func newBoundedBuffer(limit int) *boundedBuffer {
	return &boundedBuffer{limit: limit, body: make([]byte, 0, minInt(limit, 4096))}
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.limit <= 0 {
		b.overflow = true
		return len(p), nil
	}
	remaining := b.limit - len(b.body)
	if remaining > 0 {
		count := len(p)
		if count > remaining {
			count = remaining
		}
		b.body = append(b.body, p[:count]...)
	}
	if len(p) > remaining {
		b.overflow = true
	}
	return len(p), nil
}

func (b *boundedBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.body...)
}

func (b *boundedBuffer) Overflow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.overflow
}

type processWait struct {
	err      error
	exitCode *int
}

func runCLI(ctx context.Context, request cliRunRequest) ([]byte, bool, *int, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, nil, err
	}
	if request.Timeout <= 0 {
		return nil, false, nil, context.DeadlineExceeded
	}
	runCtx, cancel := context.WithTimeout(ctx, request.Timeout)
	defer cancel()

	stdout := newBoundedBuffer(request.MaxStdoutBytes)
	stderr := newBoundedBuffer(request.MaxStderrBytes)
	cmd := exec.Command(request.Executable, request.Args...)
	cmd.Dir = request.Dir
	cmd.Env = append([]string(nil), request.Env...)
	cmd.Stdin = bytes.NewReader(request.Stdin)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, false, nil, err
	}
	started := true
	waitCh := make(chan processWait, 1)
	go func() {
		err := cmd.Wait()
		waitCh <- processWait{err: err, exitCode: processExitCode(cmd, err)}
	}()

	var result processWait
	select {
	case result = <-waitCh:
	case <-runCtx.Done():
		result = stopProcessGroup(cmd, waitCh, request.KillGrace)
		if result.exitCode == nil {
			return nil, started, nil, runCtx.Err()
		}
		return nil, started, result.exitCode, runCtx.Err()
	}
	if stdout.Overflow() {
		return nil, started, result.exitCode, errors.New("Claude Code stdout exceeds configured limit")
	}
	if stderr.Overflow() {
		return nil, started, result.exitCode, errors.New("Claude Code stderr exceeds configured limit")
	}
	if result.err != nil {
		return nil, started, result.exitCode, fmt.Errorf("Claude Code process failed")
	}
	return stdout.Bytes(), started, result.exitCode, nil
}

func stopProcessGroup(cmd *exec.Cmd, waitCh <-chan processWait, grace time.Duration) processWait {
	if cmd == nil || cmd.Process == nil {
		return processWait{}
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case result := <-waitCh:
		return result
	case <-timer.C:
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		return <-waitCh
	}
}

func processExitCode(cmd *exec.Cmd, err error) *int {
	if cmd != nil && cmd.ProcessState != nil {
		code := cmd.ProcessState.ExitCode()
		if code >= 0 {
			return &code
		}
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ProcessState != nil {
		code := exitErr.ProcessState.ExitCode()
		if code >= 0 {
			return &code
		}
	}
	return nil
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
