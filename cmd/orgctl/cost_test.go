package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/costledger"
	"github.com/Mireuz13/explorarte-organization/internal/modelpricing"
)

func TestCostCommandRequiresSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"cost"}, &stdout, &stderr)
	if code != exitUsage {
		t.Fatalf("exit=%d want=%d stderr=%q", code, exitUsage, stderr.String())
	}
	if !strings.Contains(stderr.String(), "usage: orgctl cost") {
		t.Fatalf("cost usage missing: %q", stderr.String())
	}
}

func TestCostCallsRequiresProvider(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"cost", "calls"}, &stdout, &stderr)
	if code != exitUsage {
		t.Fatalf("exit=%d want=%d stderr=%q", code, exitUsage, stderr.String())
	}
}

func TestCostSummaryRequiresProvider(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"cost", "summary"}, &stdout, &stderr)
	if code != exitUsage {
		t.Fatalf("exit=%d want=%d stderr=%q", code, exitUsage, stderr.String())
	}
}

func TestWriteCostCallsShowsHumanBreakdown(t *testing.T) {
	calls := []costledger.CallBreakdown{{
		InvocationID: 41, TaskID: 7, AttemptID: 9,
		SubjectRoleID:    "investigacion/research_worker_hourly",
		WalletProviderID: "gemini", InvocationProviderID: "gemini", ProviderModelID: "gemini-2.5-flash",
		InvocationStatus: "succeeded", Settlement: costledger.SettlementCommitted,
		InputTokens: 1200, OutputTokens: 300,
		EstimatedUSD: modelpricing.USDFromDollars(0.01), ChargedUSD: modelpricing.USDFromDollars(0.002),
	}}
	var out bytes.Buffer
	writeCostCalls(&out, false, calls)
	for _, want := range []string{"INVOCATION", "41", "7/9", "research_worker_hourly", "gemini/gemini-2.5-flash", "committed", "1200/300", "$0.010000000", "$0.002000000"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output missing %q:\n%s", want, out.String())
		}
	}
}

func TestWriteCostCallsJSONUsesExactNanodollars(t *testing.T) {
	calls := []costledger.CallBreakdown{{InvocationID: 8, EstimatedUSD: 123456789, ChargedUSD: 1234}}
	var out bytes.Buffer
	writeCostCalls(&out, true, calls)
	var decoded []map[string]any
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded) != 1 || decoded[0]["estimated_usd_nanos"] != float64(123456789) || decoded[0]["charged_usd_nanos"] != float64(1234) {
		t.Fatalf("decoded=%+v", decoded)
	}
}
