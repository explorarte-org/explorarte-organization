package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestCostCommandRequiresSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"cost"}, &stdout, &stderr)
	if code != exitUsage {
		t.Fatalf("exit=%d want=%d stderr=%q", code, exitUsage, stderr.String())
	}
	if !strings.Contains(stderr.String(), "usage: orgctl cost") {
		t.Fatalf("cost usage missing: %q", stderr.String())
	}
}

func TestCostCallsRequiresProvider(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"cost", "calls"}, &stdout, &stderr)
	if code != exitUsage {
		t.Fatalf("exit=%d want=%d stderr=%q", code, exitUsage, stderr.String())
	}
}

func TestCostSummaryRequiresProvider(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"cost", "summary"}, &stdout, &stderr)
	if code != exitUsage {
		t.Fatalf("exit=%d want=%d stderr=%q", code, exitUsage, stderr.String())
	}
}
