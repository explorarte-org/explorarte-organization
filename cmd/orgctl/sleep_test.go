package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestSleepCommandIsRegisteredAndRequiresRun(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"sleep"}, &stdout, &stderr)
	if code != exitUsage {
		t.Fatalf("exit=%d want=%d stdout=%q stderr=%q", code, exitUsage, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "usage: orgctl sleep run") {
		t.Fatalf("sleep usage missing: %q", stderr.String())
	}
}

func TestSleepUnknownSubcommandFailsBeforeDatabaseAccess(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"sleep", "dream"}, &stdout, &stderr)
	if code != exitUsage {
		t.Fatalf("exit=%d want=%d stdout=%q stderr=%q", code, exitUsage, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "usage: orgctl sleep run") {
		t.Fatalf("sleep usage missing: %q", stderr.String())
	}
}
