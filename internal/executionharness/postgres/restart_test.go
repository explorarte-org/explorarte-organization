//go:build integration

package postgres_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/executionharness"
	harnesspostgres "github.com/Mireuz13/explorarte-organization/internal/executionharness/postgres"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
)

// outageAuthority allows work until a chosen check, then reports that authority
// could not be consulted. That is how this test reaches a durable, NON-terminal
// history: the run stops with its trajectory committed and nothing terminal
// written, which is exactly the state a crashed process leaves behind.
type outageAuthority struct {
	calls  int
	stopAt int
}

func (a *outageAuthority) AuthorizeExecution(context.Context, executionharness.AuthorityRequest) error {
	a.calls++
	if a.stopAt > 0 && a.calls >= a.stopAt {
		return fmt.Errorf("%w: principal: fixture store unreachable", executionharness.ErrAuthorityUnavailable)
	}
	return nil
}

type scriptedModel struct {
	results  []executionharness.ModelResult
	calls    int
	requests []executionharness.NormalizedModelRequest
}

func (m *scriptedModel) Invoke(_ context.Context, _ executionharness.RunIdentity, request executionharness.NormalizedModelRequest) (executionharness.ModelResult, error) {
	m.calls++
	m.requests = append(m.requests, request)
	if m.calls > len(m.results) {
		return executionharness.ModelResult{}, errors.New("unexpected model call")
	}
	return m.results[m.calls-1], nil
}

type countingCatalog struct {
	definition executionharness.ToolDefinition
}

func (c countingCatalog) Lookup(context.Context, string) (executionharness.ToolDefinition, bool) {
	return c.definition, true
}
func (countingCatalog) ValidateArguments(context.Context, executionharness.ToolDefinition, []byte) error {
	return nil
}

// countingTool records every side effect so a repeated execution is visible.
type countingTool struct{ calls int }

func (e *countingTool) Execute(context.Context, executionharness.RunIdentity, executionharness.ToolRequest) (executionharness.ToolExecutionResult, error) {
	e.calls++
	return executionharness.ToolExecutionResult{Content: json.RawMessage(`{"value":"durable fixture"}`), Provenance: "postgres/restart"}, nil
}

func restartSpec(taskID int64) executionharness.RunSpec {
	body := "durable restart fixture"
	return executionharness.RunSpec{
		Identity: executionharness.RunIdentity{
			RunID: "run-restart-1", OrganizationID: historyOrganization, TaskID: taskID, AttemptID: 1,
			RoleID: historyRole, ExecutionPrincipalID: "41", CorrelationID: "restart:corr", CausationID: "restart:cause",
		},
		LeaseToken: "lease",
		Context:    executionharness.InitialContext{ID: "ctx-1", Version: "v1", Digest: sha256Hex(body), Content: body},
		Tools:      []executionharness.ToolDefinition{{Name: "lookup_fixture", Description: "read deterministic fixture", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		Policy:     executionharness.RunPolicy{MaxTurns: 4, MaxToolCalls: 2, ExecutionProfileID: "standard-v1", ModelPolicyRef: "policy-1", BuildRef: "build-1"},
	}
}

func toolThenFinal() []executionharness.ModelResult {
	return []executionharness.ModelResult{
		{FinishReason: executionharness.FinishTools, InvocationRef: "inv-a-1",
			ToolRequests: []executionharness.ToolRequest{{ToolCallID: "call-1", ToolName: "lookup_fixture", Arguments: json.RawMessage(`{"key":"alpha"}`)}}},
		{FinishReason: executionharness.FinishFinal, FinalOutput: "durable final", InvocationRef: "inv-b-1"},
	}
}

// openInstance builds a completely independent Harness: its own connection
// pool, its own durable store, its own runtime and its own counters. Nothing
// is shared with the previous instance except PostgreSQL itself.
func openInstance(t *testing.T, ctx context.Context, spec executionharness.RunSpec, authority executionharness.ExecutionAuthorityPort, results []executionharness.ModelResult) (*executionharness.Runtime, *scriptedModel, *countingTool, func()) {
	t.Helper()
	databaseURL := os.Getenv("ORG_TEST_DATABASE_URL")
	cfg, err := config.LoadFrom(func(key string) (string, bool) {
		values := map[string]string{
			"ORG_ENVIRONMENT": "test", "ORG_DATABASE_URL": databaseURL,
			"ORG_DATABASE_MAX_CONNS": "8", "ORG_DATABASE_MIN_CONNS": "0",
		}
		value, ok := values[key]
		return value, ok
	})
	if err != nil {
		t.Fatal(err)
	}
	platformStore, err := platformpostgres.Open(ctx, cfg.Database, "harness-restart-instance")
	if err != nil {
		t.Fatal(err)
	}
	history, err := harnesspostgres.New(platformStore, historyOrganization)
	if err != nil {
		platformStore.Close()
		t.Fatal(err)
	}
	model := &scriptedModel{results: results}
	tools := &countingTool{}
	runtime, err := executionharness.New(authority, model, countingCatalog{definition: spec.Tools[0]}, tools, history)
	if err != nil {
		platformStore.Close()
		t.Fatal(err)
	}
	return runtime, model, tools, platformStore.Close
}

func TestHarnessRestartResumesFromDurableHistory(t *testing.T) {
	f := newFixture(t)
	defer f.cleanup()
	spec := restartSpec(f.taskID)

	// Instance A: authority is consulted for turn 1 and for the tool, then
	// becomes unavailable at the turn-2 boundary.
	runtimeA, modelA, toolsA, closeA := openInstance(t, f.ctx, spec, &outageAuthority{stopAt: 3}, toolThenFinal())
	interrupted := runtimeA.Execute(f.ctx, spec)
	if interrupted.Status != executionharness.StatusAuthorityUnavailable || !interrupted.Retryable {
		closeA()
		t.Fatalf("instance A=%+v", interrupted)
	}
	if modelA.calls != 1 || toolsA.calls != 1 {
		closeA()
		t.Fatalf("instance A: model=%d tool=%d, want 1 and 1", modelA.calls, toolsA.calls)
	}
	durableA, err := f.history.Read(f.ctx, spec.Identity.RunID)
	if err != nil {
		closeA()
		t.Fatal(err)
	}
	// Destroy instance A entirely: pool closed, runtime and counters discarded.
	closeA()

	// Instance B: same run identity, nothing in common with A but the database.
	runtimeB, modelB, toolsB, closeB := openInstance(t, f.ctx, spec, &outageAuthority{}, toolThenFinal()[1:])
	defer closeB()
	resumed := runtimeB.Execute(f.ctx, spec)
	if resumed.Status != executionharness.StatusCompleted || resumed.FinalOutput != "durable final" {
		t.Fatalf("instance B=%+v", resumed)
	}
	if modelB.calls != 1 {
		t.Fatalf("instance B made %d model calls, want exactly 1: turn 1 was repeated", modelB.calls)
	}
	if toolsB.calls != 0 {
		t.Fatalf("instance B executed the tool %d times, want 0: the side effect was repeated", toolsB.calls)
	}

	// B must have resumed from A's trajectory, not from an empty one.
	if len(modelB.requests) != 1 {
		t.Fatalf("captured %d requests", len(modelB.requests))
	}
	visible := modelB.requests[0].VisibleHistory
	if len(visible) != 2 || visible[0].Role != "assistant" || visible[1].Role != "tool" || visible[1].ToolCallID != "call-1" {
		t.Fatalf("instance B did not see instance A's turn: %+v", visible)
	}

	durableB, err := f.history.Read(f.ctx, spec.Identity.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if len(durableB) <= len(durableA) {
		t.Fatalf("resume appended nothing: before=%d after=%d", len(durableA), len(durableB))
	}
	for index, event := range durableA {
		if durableB[index].Sequence != event.Sequence || durableB[index].Type != event.Type {
			t.Fatalf("instance A's event %d was rewritten: %+v -> %+v", index, event, durableB[index])
		}
	}
	for index, event := range durableB {
		if event.Sequence != uint64(index+1) {
			t.Fatalf("durable order broke at %d: %+v", index, event)
		}
		if event.OrganizationID != historyOrganization || event.TaskID != f.taskID || event.AttemptID != 1 {
			t.Fatalf("event %d lost its execution identity: %+v", index, event)
		}
	}
	var invocations []string
	for _, event := range durableB {
		if event.Type == executionharness.EventModelResponseRecorded && event.ModelResult != nil {
			invocations = append(invocations, event.ModelResult.InvocationRef)
		}
	}
	if len(invocations) != 2 || invocations[0] != "inv-a-1" || invocations[1] != "inv-b-1" {
		t.Fatalf("durable invocation identity=%v want [inv-a-1 inv-b-1]", invocations)
	}

	// Instance C: the run is already terminal. Resume must reproduce the
	// result and touch neither the provider nor the tool.
	runtimeC, modelC, toolsC, closeC := openInstance(t, f.ctx, spec, &outageAuthority{}, toolThenFinal())
	defer closeC()
	replayed := runtimeC.Execute(f.ctx, spec)
	if replayed.Status != executionharness.StatusCompleted || replayed.FinalOutput != "durable final" {
		t.Fatalf("terminal resume=%+v", replayed)
	}
	if modelC.calls != 0 || toolsC.calls != 0 {
		t.Fatalf("terminal resume had side effects: model=%d tool=%d", modelC.calls, toolsC.calls)
	}
	durableC, err := f.history.Read(f.ctx, spec.Identity.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if len(durableC) != len(durableB) {
		t.Fatalf("terminal resume appended %d events", len(durableC)-len(durableB))
	}
}

// sha256Hex mirrors the Harness context digest rule without exporting it.
func sha256Hex(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}
