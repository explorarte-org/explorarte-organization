package tasksauthority

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/executionharness"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

// The adapter's own tests prove classification, and the Harness tests prove the
// loop reacts correctly to each class. Neither crosses the seam between them,
// which is where a %v would silently reappear and turn every outage back into a
// denial. This test wires the real adapter into the real Runtime.

type seamModel struct{ calls int }

func (m *seamModel) Invoke(context.Context, executionharness.RunIdentity, executionharness.NormalizedModelRequest) (executionharness.ModelResult, error) {
	m.calls++
	return executionharness.ModelResult{FinishReason: executionharness.FinishFinal, FinalOutput: "unreachable", InvocationRef: "inv"}, nil
}

type seamCatalog struct{}

func (seamCatalog) Lookup(context.Context, string) (executionharness.ToolDefinition, bool) {
	return executionharness.ToolDefinition{}, false
}
func (seamCatalog) ValidateArguments(context.Context, executionharness.ToolDefinition, []byte) error {
	return nil
}

type seamTool struct{ calls int }

func (e *seamTool) Execute(context.Context, executionharness.RunIdentity, executionharness.ToolRequest) (executionharness.ToolExecutionResult, error) {
	e.calls++
	return executionharness.ToolExecutionResult{}, nil
}

func seamSpec() executionharness.RunSpec {
	body := "seam fixture"
	sum := sha256.Sum256([]byte(body))
	return executionharness.RunSpec{
		Identity: executionharness.RunIdentity{
			RunID: "seam-run", OrganizationID: "org-1", TaskID: 11, AttemptID: 22,
			RoleID: "role-1", ExecutionPrincipalID: "principal-1", CorrelationID: "corr-1", CausationID: "cause-1",
		},
		LeaseToken: "lease-token",
		Context:    executionharness.InitialContext{ID: "context-1", Version: "v1", Digest: hex.EncodeToString(sum[:]), Content: body},
		Tools:      []executionharness.ToolDefinition{{Name: "lookup_fixture", Description: "d", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		Policy:     executionharness.RunPolicy{MaxTurns: 2, MaxToolCalls: 1, ExecutionProfileID: "standard-v1", ModelPolicyRef: "policy-1", BuildRef: "build-1"},
	}
}

func runSeam(t *testing.T, adapter *Adapter) (executionharness.RunResult, *seamModel, *seamTool) {
	t.Helper()
	model := &seamModel{}
	tool := &seamTool{}
	runtime, err := executionharness.New(adapter, model, seamCatalog{}, tool, executionharness.NewMemoryHistoryStore())
	if err != nil {
		t.Fatal(err)
	}
	return runtime.Execute(context.Background(), seamSpec()), model, tool
}

func TestOutageFromTheRealAdapterReachesTheRuntimeAsUnavailable(t *testing.T) {
	adapter, err := New(
		leaseVerifier{err: fmt.Errorf("%w: PostgreSQL 08006", tasks.ErrDatabaseUnavailable)},
		principalReader{value: Principal{ID: "principal-1", OrganizationID: "org-1", RoleID: "role-1", Active: true}},
	)
	if err != nil {
		t.Fatal(err)
	}

	raw := adapter.AuthorizeExecution(context.Background(), authorityRequest())
	if !errors.Is(raw, executionharness.ErrAuthorityUnavailable) {
		t.Fatalf("the unavailable sentinel did not survive the adapter: %v", raw)
	}
	if !errors.Is(raw, tasks.ErrDatabaseUnavailable) {
		t.Fatalf("the underlying cause did not survive the adapter: %v", raw)
	}

	got, model, tool := runSeam(t, adapter)
	if got.Status != executionharness.StatusAuthorityUnavailable {
		t.Fatalf("runtime status=%q want authority_unavailable: the seam flattened the classification", got.Status)
	}
	if !got.Retryable {
		t.Fatal("the runtime did not mark an authority outage retryable")
	}
	if model.calls != 0 || tool.calls != 0 {
		t.Fatalf("side effects while authority was unreachable: model=%d tool=%d", model.calls, tool.calls)
	}
}

func TestDenialFromTheRealAdapterStaysTerminalAtTheRuntime(t *testing.T) {
	adapter, err := New(
		leaseVerifier{value: tasks.ExecutionLeaseContext{TaskID: 11, AttemptID: 22, OrganizationID: "org-1", AssignedRoleID: "role-1", HolderID: "principal-1"}},
		principalReader{value: Principal{ID: "principal-1", OrganizationID: "org-1", RoleID: "role-1", Active: false}},
	)
	if err != nil {
		t.Fatal(err)
	}

	got, model, tool := runSeam(t, adapter)
	if got.Status != executionharness.StatusAuthorizationDenied {
		t.Fatalf("runtime status=%q want authorization_denied", got.Status)
	}
	if got.Retryable {
		t.Fatal("a disabled principal was reported retryable")
	}
	if model.calls != 0 || tool.calls != 0 {
		t.Fatalf("side effects after a denial: model=%d tool=%d", model.calls, tool.calls)
	}
}
