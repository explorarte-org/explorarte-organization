package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestBudgetCommandRequiresSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"budget"}, &stdout, &stderr)
	if code != exitUsage {
		t.Fatalf("exit=%d want=%d stderr=%q", code, exitUsage, stderr.String())
	}
	if !strings.Contains(stderr.String(), "usage: orgctl budget") {
		t.Fatalf("budget usage missing: %q", stderr.String())
	}
}

func TestBudgetUnknownSubcommandFailsBeforeDatabaseAccess(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"budget", "dream"}, &stdout, &stderr)
	if code != exitUsage {
		t.Fatalf("exit=%d want=%d stderr=%q", code, exitUsage, stderr.String())
	}
}

func TestBudgetStatusRequiresTaskFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"budget", "status"}, &stdout, &stderr)
	if code != exitUsage {
		t.Fatalf("exit=%d want=%d stderr=%q", code, exitUsage, stderr.String())
	}
	if !strings.Contains(stderr.String(), "usage: orgctl budget status") {
		t.Fatalf("budget status usage missing: %q", stderr.String())
	}
}

func TestBudgetSetPriceRequiresProviderModelInputOutput(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"budget", "set-price", "--provider", "deepseek"}, &stdout, &stderr)
	if code != exitUsage {
		t.Fatalf("exit=%d want=%d stderr=%q", code, exitUsage, stderr.String())
	}
}

func TestBudgetSetBalanceRequiresProviderAndUSD(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"budget", "set-balance", "--provider", "deepseek"}, &stdout, &stderr)
	if code != exitUsage {
		t.Fatalf("exit=%d want=%d stderr=%q", code, exitUsage, stderr.String())
	}
}
