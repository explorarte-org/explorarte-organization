package main

import (
	"strings"
	"testing"
)

func TestExternalSmokeRequiresExplicitConfirmation(t *testing.T) {
	if _, ok := parseExternalSmokeArgs([]string{
		"--confirm", "wrong", "--idempotency-key", "external-smoke-test",
	}, &strings.Builder{}); ok {
		t.Fatal("accepted an incorrect confirmation token")
	}
}

func TestExternalSmokeAcceptsOnlyBoundedShape(t *testing.T) {
	parsed, ok := parseExternalSmokeArgs([]string{
		"--confirm", externalSmokeConfirmation,
		"--idempotency-key", "external-smoke-test-1",
		"--json",
	}, &strings.Builder{})
	if !ok {
		t.Fatal("rejected the fixed bounded smoke shape")
	}
	if parsed.idempotencyKey != "external-smoke-test-1" || !parsed.jsonOutput {
		t.Fatalf("parsed=%+v, want fixed key and JSON output", parsed)
	}
}

func TestExternalSmokeRejectsConfigurableProviderKnobs(t *testing.T) {
	if _, ok := parseExternalSmokeArgs([]string{
		"--confirm", externalSmokeConfirmation,
		"--idempotency-key", "external-smoke-test-2",
		"--max-output-tokens", "2000",
	}, &strings.Builder{}); ok {
		t.Fatal("accepted a configurable output-token knob")
	}
}
