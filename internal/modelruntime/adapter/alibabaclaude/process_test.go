//go:build !windows

package alibabaclaude

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRunCLIReportsNotStartedWithoutLaunchingShellFallback(t *testing.T) {
	_, started, exitCode, err := runCLI(context.Background(), cliRunRequest{
		Executable:     filepath.Join(t.TempDir(), "missing-claude"),
		Dir:            t.TempDir(),
		Env:            []string{"PATH=/usr/bin:/bin"},
		Stdin:          []byte("input"),
		MaxStdoutBytes: 4096,
		MaxStderrBytes: 4096,
		Timeout:        time.Second,
		KillGrace:      100 * time.Millisecond,
	})
	if err == nil || started || exitCode != nil {
		t.Fatalf("missing executable: started=%v exit=%v err=%v", started, exitCode, err)
	}
}

func TestRunCLIBoundsStdoutAndDoesNotReturnBodyOnOverflow(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "overflow")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ni=0\nwhile [ $i -lt 5000 ]; do printf x; i=$((i+1)); done\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	body, started, exitCode, err := runCLI(context.Background(), cliRunRequest{
		Executable:     script,
		Dir:            dir,
		Env:            []string{"PATH=/usr/bin:/bin"},
		Stdin:          []byte("input"),
		MaxStdoutBytes: 1024,
		MaxStderrBytes: 4096,
		Timeout:        time.Second,
		KillGrace:      100 * time.Millisecond,
	})
	if err == nil || !started || exitCode == nil || *exitCode != 0 {
		t.Fatalf("overflow: started=%v exit=%v err=%v", started, exitCode, err)
	}
	if body != nil {
		t.Fatalf("overflow returned provider body: %d bytes", len(body))
	}
}

func TestRunCLIBoundsStderrWithoutLeakingItThroughError(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "stderr-overflow")
	secret := "provider-secret-message-that-must-not-leak"
	if err := os.WriteFile(script, []byte("#!/bin/sh\ni=0\nwhile [ $i -lt 5000 ]; do printf '"+secret+"' >&2; i=$((i+1)); done\nexit 23\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	_, started, exitCode, err := runCLI(context.Background(), cliRunRequest{
		Executable:     script,
		Dir:            dir,
		Env:            []string{"PATH=/usr/bin:/bin"},
		Stdin:          []byte("input"),
		MaxStdoutBytes: 4096,
		MaxStderrBytes: 1024,
		Timeout:        time.Second,
		KillGrace:      100 * time.Millisecond,
	})
	if err == nil || !started || exitCode == nil || *exitCode != 23 {
		t.Fatalf("stderr overflow: started=%v exit=%v err=%v", started, exitCode, err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unexpected timeout: %v", err)
	}
	if containsString(err.Error(), secret) {
		t.Fatalf("stderr content leaked through error: %v", err)
	}
}

func containsString(value, needle string) bool {
	if needle == "" || len(value) < len(needle) {
		return false
	}
	for i := 0; i+len(needle) <= len(value); i++ {
		if value[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
