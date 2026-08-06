package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestParseDecisionFileRejectsUnknownFields(t *testing.T) {
	path := writeDecisionInput(t, `{"run_id":1,"claimed_by":"worker","lease_duration":"1m","unexpected":true}`)
	var input decisionClaimInput
	var stderr bytes.Buffer
	if _, code := parseDecisionFile([]string{"--file", path}, &stderr, &input); code != exitUsage {
		t.Fatalf("code=%d, want %d", code, exitUsage)
	}
	if stderr.Len() == 0 {
		t.Fatal("expected strict decode error")
	}
}

func TestParseDecisionFileRejectsMultipleTopLevelValues(t *testing.T) {
	path := writeDecisionInput(t, `{"run_id":1,"claimed_by":"worker","lease_duration":"1m"} {"run_id":2}`)
	var input decisionClaimInput
	var stderr bytes.Buffer
	if _, code := parseDecisionFile([]string{"--file", path}, &stderr, &input); code != exitUsage {
		t.Fatalf("code=%d, want %d", code, exitUsage)
	}
}

func TestParseDecisionFileAcceptsStrictInput(t *testing.T) {
	path := writeDecisionInput(t, `{"run_id":1,"claimed_by":"worker","lease_duration":"1m"}`)
	var input decisionClaimInput
	var stderr bytes.Buffer
	jsonOutput, code := parseDecisionFile([]string{"--file", path, "--json"}, &stderr, &input)
	if code != exitOK || !jsonOutput {
		t.Fatalf("code=%d json=%v stderr=%s", code, jsonOutput, stderr.String())
	}
	if input.RunID != 1 || input.ClaimedBy != "worker" || input.LeaseDuration != "1m" {
		t.Fatalf("input=%+v", input)
	}
}

func TestParseDecisionRunIDRejectsNonPositiveValues(t *testing.T) {
	for _, value := range []string{"0", "-1", "not-a-number"} {
		var stderr bytes.Buffer
		if _, _, code := parseDecisionRunID([]string{value}, &stderr); code != exitUsage {
			t.Fatalf("value=%q code=%d, want %d", value, code, exitUsage)
		}
	}
}

func writeDecisionInput(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "decision.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
