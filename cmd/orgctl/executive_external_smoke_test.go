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

func TestExternalSmokeBudgetedSpecHasFixedFiveDollarCeilings(t *testing.T) {
	spec := externalSmokeBudgetedSpec()
	parsed, ok := parseExternalSmokeArgsFor([]string{
		"--confirm", spec.confirmation,
		"--idempotency-key", "external-smoke-5usd-test-1",
		"--json",
	}, &strings.Builder{}, spec)
	if !ok {
		t.Fatal("rejected the explicitly budgeted smoke shape")
	}
	if parsed.idempotencyKey != "external-smoke-5usd-test-1" || !parsed.jsonOutput {
		t.Fatalf("parsed=%+v, want fixed budgeted key and JSON output", parsed)
	}
	if spec.maxUSD != 5 || spec.maxCalls != 1 || spec.maxOutputTokens != 1_048_576 || spec.maxCampaignTokens != 5_000_000 {
		t.Fatalf("spec=%+v, want $5, one call, runtime-valid output ceiling, and 5M campaign ceiling", spec)
	}
	if int64(spec.maxOutputTokens) > spec.maxCampaignTokens {
		t.Fatalf("per-call output ceiling exceeds campaign ceiling: spec=%+v", spec)
	}
}

func TestExternalSmokeBudgetedSpecRejectsSmallSmokeConfirmationAndPrefix(t *testing.T) {
	if _, ok := parseExternalSmokeArgsFor([]string{
		"--confirm", externalSmokeConfirmation,
		"--idempotency-key", "external-smoke-test-3",
	}, &strings.Builder{}, externalSmokeBudgetedSpec()); ok {
		t.Fatal("accepted the small-smoke confirmation for the budgeted command")
	}
	if _, ok := parseExternalSmokeArgsFor([]string{
		"--confirm", externalSmokeBudgetedConfirmation,
		"--idempotency-key", "external-smoke-test-4",
	}, &strings.Builder{}, externalSmokeBudgetedSpec()); ok {
		t.Fatal("accepted a non-budgeted idempotency-key prefix")
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
