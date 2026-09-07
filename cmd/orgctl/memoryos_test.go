package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/memoryos/consolidation"
)

func TestMemoryOSUsageOutput(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"memoryos"}, &stdout, &stderr)
	if code != exitUsage {
		t.Fatalf("exit=%d want=%d stdout=%q stderr=%q", code, exitUsage, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "usage: orgctl memoryos <command> [options]") {
		t.Fatalf("memoryos usage missing: %q", stderr.String())
	}
}

func TestMemoryOSConsolidateUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"memoryos", "consolidate", "extra_arg"}, &stdout, &stderr)
	if code != exitUsage {
		t.Fatalf("exit=%d want=%d stdout=%q stderr=%q", code, exitUsage, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "usage: orgctl memoryos consolidate [--window 720h] [--json]") {
		t.Fatalf("memoryos consolidate usage missing: %q", stderr.String())
	}
}

func TestMemoryOSConsolidateDefaultConfigSemanticOwner(t *testing.T) {
	cfg := consolidation.DefaultConfig()
	if cfg.SemanticOwner != consolidation.SemanticOwnerSleep {
		t.Fatalf("expected DefaultConfig().SemanticOwner == %q, got %q", consolidation.SemanticOwnerSleep, cfg.SemanticOwner)
	}
}
