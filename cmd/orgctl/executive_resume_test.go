package main

import (
	"strings"
	"testing"
)

func TestExecutiveResumeAcceptsJSONBeforeOrAfterRootTaskID(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "flag before root", args: []string{"--json", "0"}},
		{name: "flag after root", args: []string{"0", "--json"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr strings.Builder
			code := runExecutiveResume(test.args, &stdout, &stderr)

			if code != exitUsage {
				t.Fatalf("code=%d want exitUsage for invalid root id", code)
			}
			if strings.Contains(stderr.String(), "usage: orgctl executive resume") {
				t.Fatalf("resume rejected interspersed flag order at parser: stderr=%q", stderr.String())
			}
			if !strings.Contains(stderr.String(), "ROOT_TASK_ID must be a positive integer") {
				t.Fatalf("resume did not reach root-id validation: stderr=%q", stderr.String())
			}
		})
	}
}
