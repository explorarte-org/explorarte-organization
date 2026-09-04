package main

import (
	"strings"
	"testing"
)

func TestExecutiveSubmitRejectsNonPositiveModelCallCeiling(t *testing.T) {
	for _, value := range []string{"0", "-1"} {
		stderr := &strings.Builder{}
		code := runExecutiveSubmit([]string{
			"--file", "/does/not/exist",
			"--actor-role", "empresa/human",
			"--idempotency-key", "test-submit-invalid-calls-" + value,
			"--max-model-calls", value,
		}, &strings.Builder{}, stderr)
		if code != exitUsage || !strings.Contains(stderr.String(), "invalid campaign budget") {
			t.Fatalf("max-model-calls=%s code=%d stderr=%q", value, code, stderr.String())
		}
	}
}

func TestExecutiveSubmitAcceptsBoundedNoRetryFlagsBeforeOpeningDatabase(t *testing.T) {
	stderr := &strings.Builder{}
	code := runExecutiveSubmit([]string{
		"--file", "/does/not/exist",
		"--actor-role", "empresa/human",
		"--idempotency-key", "test-submit-bounded-no-retry",
		"--max-model-calls", "7",
		"--no-retries",
	}, &strings.Builder{}, stderr)
	if code != exitInvalid || !strings.Contains(stderr.String(), "read executive goal") {
		t.Fatalf("bounded no-retry flags were not accepted before file validation: code=%d stderr=%q", code, stderr.String())
	}
}
