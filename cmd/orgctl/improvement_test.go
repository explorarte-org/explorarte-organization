package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/evaluation"
	"github.com/Mireuz13/explorarte-organization/internal/improvement"
)

func TestParseImprovementFileRejectsUnknownFields(t *testing.T) {
	path := writeImprovementInput(t, `{"id":"c1","artifact":{"artifact_id":"a","content_hash":"h","schema_version":"v1"},"lineage":{},"created_by":"owner","unexpected":true}`)
	var input improvementProposeInput
	var stderr bytes.Buffer
	if _, code := parseImprovementFile([]string{"--file", path}, &stderr, &input); code != exitUsage {
		t.Fatalf("code=%d, want %d", code, exitUsage)
	}
	if stderr.Len() == 0 {
		t.Fatal("expected strict decode error")
	}
}

func TestParseImprovementFileRejectsMultipleTopLevelValues(t *testing.T) {
	path := writeImprovementInput(t, `{"id":"c1"} {"id":"c2"}`)
	var input improvementProposeInput
	var stderr bytes.Buffer
	if _, code := parseImprovementFile([]string{"--file", path}, &stderr, &input); code != exitUsage {
		t.Fatalf("code=%d, want %d", code, exitUsage)
	}
}

func TestParseImprovementFileRequiresFileFlag(t *testing.T) {
	var input improvementProposeInput
	var stderr bytes.Buffer
	if _, code := parseImprovementFile([]string{}, &stderr, &input); code != exitUsage {
		t.Fatalf("code=%d, want %d", code, exitUsage)
	}
}

func TestParseImprovementFileAcceptsStrictInput(t *testing.T) {
	path := writeImprovementInput(t, `{"id":"c1","artifact":{"artifact_id":"artifact-1","content_hash":"hash-1","schema_version":"v1"},"lineage":{"parent_candidate_id":"c0","parent_artifact_hash":"hash-0","derived_from":"suite-1"},"created_by":"owner"}`)
	var input improvementProposeInput
	var stderr bytes.Buffer
	jsonOutput, code := parseImprovementFile([]string{"--file", path, "--json"}, &stderr, &input)
	if code != exitOK || !jsonOutput {
		t.Fatalf("code=%d json=%v stderr=%s", code, jsonOutput, stderr.String())
	}
	if input.ID != "c1" || input.Artifact.ContentHash != "hash-1" || input.Lineage.ParentCandidateID != "c0" || input.CreatedBy != "owner" {
		t.Fatalf("input=%+v", input)
	}
}

func TestParseImprovementCandidateIDRejectsEmptyAndExtraValues(t *testing.T) {
	cases := [][]string{
		{},
		{""},
		{"   "},
		{"c1", "c2"},
	}
	for _, args := range cases {
		var stderr bytes.Buffer
		if _, _, code := parseImprovementCandidateID(args, &stderr); code != exitUsage {
			t.Fatalf("args=%v code=%d, want %d", args, code, exitUsage)
		}
	}
}

func TestParseImprovementCandidateIDTrimsWhitespace(t *testing.T) {
	var stderr bytes.Buffer
	jsonOutput, id, code := parseImprovementCandidateID([]string{"--json", " c1 "}, &stderr)
	if code != exitOK || !jsonOutput || id != "c1" {
		t.Fatalf("code=%d json=%v id=%q stderr=%s", code, jsonOutput, id, stderr.String())
	}
}

func TestParseImprovementRunIDRejectsNonPositiveValues(t *testing.T) {
	for _, value := range []string{"0", "-1", "not-a-number"} {
		var stderr bytes.Buffer
		if _, _, code := parseImprovementRunID([]string{value}, &stderr); code != exitUsage {
			t.Fatalf("value=%q code=%d, want %d", value, code, exitUsage)
		}
	}
}

func TestImprovementComparisonInputConvertsToEvaluation(t *testing.T) {
	input := improvementComparisonInput{
		SuiteID: "suite-1", OverallVerdict: "pass", WeightedPassRatio: 1,
		CaseResults: []improvementCaseResultInput{{
			CaseID: "case-1", Weight: 2, BaselineVerdict: "pass", CandidateVerdict: "pass", OverallVerdict: "pass",
			Deltas: []improvementMetricDeltaInput{{Name: "latency", BaselineValue: 2, CandidateValue: 1, Delta: -1, Unit: "s"}},
		}},
	}
	got := input.comparison()
	if err := got.Validate(); err != nil {
		t.Fatalf("converted comparison is invalid: %v", err)
	}
	if got.SuiteID != "suite-1" || got.OverallVerdict != evaluation.VerdictPass || got.WeightedPassRatio != 1 {
		t.Fatalf("comparison=%+v", got)
	}
	if len(got.CaseResults) != 1 || got.CaseResults[0].CaseID != "case-1" || got.CaseResults[0].Weight != 2 {
		t.Fatalf("case results=%+v", got.CaseResults)
	}
	delta := got.CaseResults[0].Deltas[0]
	if delta.Name != "latency" || delta.BaselineValue != 2 || delta.CandidateValue != 1 || delta.Delta != -1 || delta.Unit != "s" {
		t.Fatalf("delta=%+v", delta)
	}
}

func TestCliApprovalGateCapturesRequestAndAppliesOperatorDecision(t *testing.T) {
	gate := &cliApprovalGate{decide: func(request improvement.PromotionRequest) (improvement.PromotionDecision, error) {
		return improvement.PromotionDecision{
			CandidateID: request.CandidateID, Kind: request.Kind,
			Outcome: improvement.PromotionAuthorized, DecidedAt: request.RequestedAt, DecidedBy: "owner",
		}, nil
	}}
	request := improvement.PromotionRequest{CandidateID: "c1", Kind: improvement.PromotionToCanary}
	decision, err := gate.AuthorizePromotion(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !gate.invoked || gate.request.CandidateID != request.CandidateID || gate.request.Kind != request.Kind {
		t.Fatalf("gate did not capture the request: invoked=%v request=%+v", gate.invoked, gate.request)
	}
	if decision.CandidateID != "c1" || decision.Kind != improvement.PromotionToCanary || decision.Outcome != improvement.PromotionAuthorized || decision.DecidedBy != "owner" {
		t.Fatalf("decision=%+v", decision)
	}
}

func TestCliApprovalGateHonorsContextCancellation(t *testing.T) {
	gate := &cliApprovalGate{decide: func(improvement.PromotionRequest) (improvement.PromotionDecision, error) {
		return improvement.PromotionDecision{}, nil
	}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := gate.AuthorizePromotion(ctx, improvement.PromotionRequest{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
	if gate.invoked {
		t.Fatal("gate must not record a decision when the context is already cancelled")
	}
}

func TestNonApprovingGateRejectsEveryRequest(t *testing.T) {
	if _, err := (nonApprovingGate{}).AuthorizePromotion(context.Background(), improvement.PromotionRequest{}); err == nil {
		t.Fatal("non-approving gate must never authorize a promotion")
	}
}

func TestImprovementCommandErrorMapsSentinels(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{improvement.ErrCandidateNotFound, exitInvalid},
		{improvement.ErrInvalidTransition, exitInvalid},
		{evaluation.ErrIncomparableResults, exitInvalid},
		{improvement.ErrPromotionDenied, exitDenied},
		{improvement.ErrRevisionConflict, exitDrift},
		{errors.New("database exploded"), exitInternal},
	}
	for _, test := range cases {
		var stderr bytes.Buffer
		if code := improvementCommandError(&stderr, test.err); code != test.want {
			t.Fatalf("err=%v code=%d, want %d", test.err, code, test.want)
		}
	}
}

func writeImprovementInput(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "improvement.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
