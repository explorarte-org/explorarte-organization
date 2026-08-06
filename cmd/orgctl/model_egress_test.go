package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/modelegress"
)

func TestModelEgressExitCodes(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "invalid policy", err: modelegress.ErrInvalidPolicy, want: exitInvalid},
		{name: "stale", err: modelegress.ErrPolicyStale, want: exitDrift},
		{name: "missing", err: modelegress.ErrPolicyNotFound, want: exitDrift},
		{name: "conflict", err: modelegress.ErrPolicyConflict, want: exitInvalid},
		{name: "evaluation conflict", err: modelegress.ErrEvaluationConflict, want: exitInvalid},
		{name: "operational", err: errors.New("unexpected"), want: exitInternal},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stderr bytes.Buffer
			if got := modelEgressError(&stderr, test.err); got != test.want {
				t.Fatalf("exit=%d want=%d stderr=%q", got, test.want, stderr.String())
			}
		})
	}
}

func TestModelEgressCLIRejectsPolicyAndProviderSelectionFlags(t *testing.T) {
	forbidden := []string{"--provider", "--policy", "--policy-id", "--policy-version", "--transport", "--classifications", "--effect", "--url", "--api-key"}
	for _, flagName := range forbidden {
		t.Run(strings.TrimPrefix(flagName, "--"), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := modelEgress(context.Background(), nil, []string{"validate", flagName, "value"}, &stdout, &stderr)
			if code != exitUsage {
				t.Fatalf("validate accepted %s: exit=%d stdout=%q stderr=%q", flagName, code, stdout.String(), stderr.String())
			}
			stdout.Reset()
			stderr.Reset()
			code = modelEgress(context.Background(), nil, []string{"sync", flagName, "value"}, &stdout, &stderr)
			if code != exitUsage {
				t.Fatalf("sync accepted %s: exit=%d stdout=%q stderr=%q", flagName, code, stdout.String(), stderr.String())
			}
		})
	}
}

func TestModelUsageIncludesEgressCommands(t *testing.T) {
	var output bytes.Buffer
	printModelUsage(&output)
	if !strings.Contains(output.String(), "model egress <validate|diff|sync|status>") {
		t.Fatalf("usage=%q", output.String())
	}
}
