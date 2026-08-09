package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestAgentsCommandRequiresSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"agents"}, &stdout, &stderr)
	if code != exitUsage {
		t.Fatalf("exit=%d want=%d stderr=%q", code, exitUsage, stderr.String())
	}
	if !strings.Contains(stderr.String(), "usage: orgctl agents") {
		t.Fatalf("agents usage missing: %q", stderr.String())
	}
}

func TestAgentsTreeRequiresTaskFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"agents", "tree"}, &stdout, &stderr)
	if code != exitUsage {
		t.Fatalf("exit=%d want=%d stderr=%q", code, exitUsage, stderr.String())
	}
}

func TestAgentsStatusRequiresTaskFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"agents", "status"}, &stdout, &stderr)
	if code != exitUsage {
		t.Fatalf("exit=%d want=%d stderr=%q", code, exitUsage, stderr.String())
	}
}

func TestAgentsUnknownSubcommandFailsBeforeDatabaseAccess(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"agents", "dream"}, &stdout, &stderr)
	if code != exitUsage {
		t.Fatalf("exit=%d want=%d stderr=%q", code, exitUsage, stderr.String())
	}
}
