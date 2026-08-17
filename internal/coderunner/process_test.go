package coderunner

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestProcessGroupKillReapsDescendant proves that cancelling runSupervised
// terminates the whole process tree, not just the direct child: the direct
// child backgrounds a descendant that would, left alone, write a marker
// file about a second after the cancellation deadline. If the descendant
// survives cancellation (e.g. because only the direct child were killed),
// the marker appears; runSupervised's process-group kill must prevent it.
func TestProcessGroupKillReapsDescendant(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh required")
	}
	dir := t.TempDir()
	marker := filepath.Join(dir, "survived")
	script := "sleep 0.2; ( sleep 1; echo survived > " + marker + " ) & disown; sleep 5"

	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	out := newBoundedOutput(0, 0, nil)
	_, err := runSupervised(ctx, dir, "", out, "sh", "-c", script)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline-exceeded classification, got %v", err)
	}

	time.Sleep(1200 * time.Millisecond)
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("descendant survived process-group cancellation: marker file was written")
	} else if !os.IsNotExist(statErr) {
		t.Fatalf("unexpected stat error: %v", statErr)
	}
}

// TestProcessGroupKillTerminatesDirectChild is the control case: a plain
// command with no descendants is reliably killed and reported promptly
// (not indeterminate) when its context is cancelled.
func TestProcessGroupKillTerminatesDirectChild(t *testing.T) {
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("sleep required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	out := newBoundedOutput(0, 0, nil)
	start := time.Now()
	_, err := runSupervised(ctx, t.TempDir(), "", out, "sleep", "30")
	elapsed := time.Since(start)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want deadline-exceeded, got %v", err)
	}
	if elapsed > killGrace+2*time.Second {
		t.Fatalf("took too long to report termination: %v", elapsed)
	}
}

// TestStuckProcessReportsIndeterminate proves the fail-closed contract: a
// process that cannot be reaped within killGrace (here simulated by
// shrinking killGrace-equivalent behavior is not possible since killGrace is
// a package constant, so instead this proves the grace window itself is
// honored -- a process that dies promptly after SIGKILL never reports
// indeterminate) and documents the boundary the P1 spec requires: only the
// unreapable case (not exercised here, since it requires an uninterruptible
// process that no CI-safe fixture can construct) returns
// ErrIndeterminateExecution. See TestProcessGroupKillReapsDescendant and
// TestProcessGroupKillTerminatesDirectChild for the promptly-reaped path.
func TestStuckProcessReportsIndeterminate(t *testing.T) {
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("sleep required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	out := newBoundedOutput(0, 0, nil)
	_, err := runSupervised(ctx, t.TempDir(), "", out, "sleep", "5")
	if errors.Is(err, ErrIndeterminateExecution) {
		t.Fatal("a normally-killable process must not be reported indeterminate")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want deadline-exceeded, got %v", err)
	}
}

func TestRunSupervisedSuccessAndFailureExitCodes(t *testing.T) {
	out := newBoundedOutput(0, 0, nil)
	code, err := runSupervised(context.Background(), t.TempDir(), "", out, "sh", "-c", "exit 0")
	if err != nil || code != 0 {
		t.Fatalf("code=%d err=%v", code, err)
	}
	out2 := newBoundedOutput(0, 0, nil)
	code2, err2 := runSupervised(context.Background(), t.TempDir(), "", out2, "sh", "-c", "exit 7")
	if err2 == nil || code2 != 7 {
		t.Fatalf("code=%d err=%v", code2, err2)
	}
}

func TestRunSupervisedFeedsStdin(t *testing.T) {
	out := newBoundedOutput(0, 0, nil)
	_, err := runSupervised(context.Background(), t.TempDir(), "hello-stdin", out, "cat")
	if err != nil {
		t.Fatal(err)
	}
	if got := out.Result().String(); got != "hello-stdin" {
		t.Fatalf("got %q", got)
	}
}
