package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/questionidentity"
)

func TestRunEmitsRejectedDecisionWithoutOperationalError(t *testing.T) {
	input := `{"assignment_id":"REFORMULATED-Q3-001-EVIDENCE-003","objective":"exactly N mechanisms"}`
	var output bytes.Buffer
	if err := run(strings.NewReader(input), &output); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	var outcome questionidentity.ControllerBindingOutcome
	if err := json.Unmarshal(output.Bytes(), &outcome); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if outcome.Decision.Accepted() || outcome.ProviderCallAllowed {
		t.Fatalf("drift was callable: %#v", outcome)
	}
}

func TestRunRejectsOversizedInputOperationally(t *testing.T) {
	var output bytes.Buffer
	err := run(strings.NewReader(strings.Repeat("x", maxCandidateBytes+1)), &output)
	if err == nil {
		t.Fatal("run() accepted oversized input")
	}
}
