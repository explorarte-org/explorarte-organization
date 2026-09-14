package ceochat

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/executionharness"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

// fakeTaskReader is a minimal, in-memory TaskReader used only by this
// file's registry-level tests: it exercises the tasks.* handlers' own
// pagination/validation/error-propagation logic without any real
// PostgreSQL dependency (that dependency is proven separately, against
// real data, by the integration suite).
type fakeTaskReader struct {
	tasksByOffset []tasks.Task
	listErr       error
	attempts      []tasks.Attempt
	getErr        error
	lastFilter    tasks.TaskFilter
}

func (f *fakeTaskReader) ListTasks(_ context.Context, filter tasks.TaskFilter) ([]tasks.Task, error) {
	f.lastFilter = filter
	if f.listErr != nil {
		return nil, f.listErr
	}
	start := filter.Offset
	if start > len(f.tasksByOffset) {
		start = len(f.tasksByOffset)
	}
	end := start + filter.Limit
	if end > len(f.tasksByOffset) {
		end = len(f.tasksByOffset)
	}
	return f.tasksByOffset[start:end], nil
}

func (f *fakeTaskReader) GetTask(_ context.Context, id int64) (tasks.TaskDetail, error) {
	if f.getErr != nil {
		return tasks.TaskDetail{}, f.getErr
	}
	return tasks.TaskDetail{Task: tasks.Task{ID: id, Title: "t", Status: tasks.StatusReady, AssignedRoleID: CEORoleID}}, nil
}

func (f *fakeTaskReader) ListAttemptsPage(_ context.Context, _ int64, limit, offset int) ([]tasks.Attempt, error) {
	start := offset
	if start > len(f.attempts) {
		start = len(f.attempts)
	}
	end := start + limit
	if end > len(f.attempts) {
		end = len(f.attempts)
	}
	return f.attempts[start:end], nil
}

func newFakeTasks(n int) []tasks.Task {
	out := make([]tasks.Task, n)
	for i := range out {
		out[i] = tasks.Task{ID: int64(i + 1), Title: "task", Status: tasks.StatusReady, AssignedRoleID: CEORoleID, CreatedAt: time.Now()}
	}
	return out
}

// Negative test J: a second Register call for an ID already registered is a
// deterministic, immediate error -- there is no "last one wins".
func TestToolRegistryRejectsDuplicateRegistration(t *testing.T) {
	registry := NewToolRegistry()
	reader := &fakeTaskReader{}
	if err := RegisterTaskTools(registry, reader); err != nil {
		t.Fatal(err)
	}
	if err := RegisterTaskTools(registry, reader); !errors.Is(err, ErrDuplicateToolRegistration) {
		t.Fatalf("err=%v want ErrDuplicateToolRegistration", err)
	}
}

// Negative test K: an oversized model-supplied limit must be REJECTED, not
// silently clamped -- it must never reach the fake reader's filter at all.
func TestTasksListRejectsOversizedLimitBeforeReachingTheReader(t *testing.T) {
	registry := NewToolRegistry()
	reader := &fakeTaskReader{tasksByOffset: newFakeTasks(3)}
	if err := RegisterTaskTools(registry, reader); err != nil {
		t.Fatal(err)
	}
	catalog := RegistryToolCatalog{Registry: registry}
	definition, ok := catalog.Lookup(context.Background(), ToolTasksList)
	if !ok {
		t.Fatal("tasks.list must be registered")
	}
	err := catalog.ValidateArguments(context.Background(), definition, json.RawMessage(`{"limit":1000000}`))
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err=%v want ErrInvalidInput", err)
	}
	if reader.lastFilter.Limit != 0 {
		t.Fatalf("reader must never be called: lastFilter=%+v", reader.lastFilter)
	}
}

// Negative test C: a caller whose identity does not match a tool's
// RequiredRole is denied by the executor's own defense-in-depth check,
// before the canonical handler ever runs. ceochat's own Send always drives
// tools under RoleID=CEORoleID, so this boundary is proven directly against
// RegistryToolExecutor -- the same adapter Send composes into the Harness.
func TestRegistryToolExecutorDeniesWrongActorRole(t *testing.T) {
	registry := NewToolRegistry()
	reader := &fakeTaskReader{tasksByOffset: newFakeTasks(1)}
	if err := RegisterTaskTools(registry, reader); err != nil {
		t.Fatal(err)
	}
	executor := RegistryToolExecutor{Registry: registry}
	_, err := executor.Execute(context.Background(),
		executionharness.RunIdentity{RoleID: "empresa/not-the-ceo"},
		executionharness.ToolRequest{ToolCallID: "call-1", ToolName: ToolTasksList, Arguments: json.RawMessage(`{}`)})
	if !errors.Is(err, ErrUnauthorizedActor) {
		t.Fatalf("err=%v want ErrUnauthorizedActor", err)
	}
	if reader.lastFilter.Limit != 0 {
		t.Fatal("the canonical reader must never be called for a denied actor")
	}
}

// Negative test F: a handler whose canonical-service call returns more
// bytes than its own descriptor's MaxResultBytes allows must be refused by
// the executor -- never truncated into invalid JSON, never handed to the
// Harness oversized.
func TestRegistryToolExecutorRejectsOversizedResult(t *testing.T) {
	registry := NewToolRegistry()
	err := registry.Register(ToolDescriptor{
		ID: "test.oversized", Version: "v1", Description: "d",
		InputSchema: json.RawMessage(`{"type":"object"}`), Access: AccessReadOnly, RequiredRole: CEORoleID,
		Limits: ToolLimits{MaxResultBytes: 8, Timeout: time.Second}, DataClass: DataClassInternal,
	}, func(json.RawMessage) error { return nil },
		func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`{"this-is-definitely-more-than-eight-bytes":true}`), nil
		})
	if err != nil {
		t.Fatal(err)
	}
	executor := RegistryToolExecutor{Registry: registry}
	_, err = executor.Execute(context.Background(),
		executionharness.RunIdentity{RoleID: CEORoleID},
		executionharness.ToolRequest{ToolCallID: "call-1", ToolName: "test.oversized", Arguments: json.RawMessage(`{}`)})
	if !errors.Is(err, ErrToolResultTooLarge) {
		t.Fatalf("err=%v want ErrToolResultTooLarge", err)
	}
}

// Negative test H: a canonical-service error must propagate as an error --
// the executor must never fabricate a successful ToolExecutionResult when
// the handler failed.
func TestRegistryToolExecutorPropagatesServiceErrorWithoutFabricatingSuccess(t *testing.T) {
	registry := NewToolRegistry()
	reader := &fakeTaskReader{getErr: errors.New("canonical service unavailable")}
	if err := RegisterTaskTools(registry, reader); err != nil {
		t.Fatal(err)
	}
	executor := RegistryToolExecutor{Registry: registry}
	result, err := executor.Execute(context.Background(),
		executionharness.RunIdentity{RoleID: CEORoleID},
		executionharness.ToolRequest{ToolCallID: "call-1", ToolName: ToolTasksGet, Arguments: json.RawMessage(`{"task_id":1}`)})
	if err == nil {
		t.Fatal("want an error, got a fabricated success")
	}
	if len(result.Content) != 0 {
		t.Fatalf("result content must be empty on error, got %q", result.Content)
	}
}

// Negative test G: pagination is a host-owned, opaque cursor over the
// canonical service's own Limit/Offset -- page 1 followed by page 2 must
// cover disjoint rows with no duplication, and a tampered/invalid cursor
// must be denied rather than guessed at.
func TestTasksListPaginationCoversDisjointRowsAndRejectsInvalidCursor(t *testing.T) {
	registry := NewToolRegistry()
	reader := &fakeTaskReader{tasksByOffset: newFakeTasks(5)}
	if err := RegisterTaskTools(registry, reader); err != nil {
		t.Fatal(err)
	}
	executor := RegistryToolExecutor{Registry: registry}
	identity := executionharness.RunIdentity{RoleID: CEORoleID}

	page1, err := executor.Execute(context.Background(), identity,
		executionharness.ToolRequest{ToolCallID: "call-1", ToolName: ToolTasksList, Arguments: json.RawMessage(`{"limit":2}`)})
	if err != nil {
		t.Fatal(err)
	}
	var decoded1 struct {
		Tasks      []taskListView `json:"tasks"`
		NextCursor string         `json:"next_cursor"`
	}
	if err = json.Unmarshal(page1.Content, &decoded1); err != nil {
		t.Fatal(err)
	}
	if len(decoded1.Tasks) != 2 || decoded1.NextCursor == "" {
		t.Fatalf("page1=%+v", decoded1)
	}

	page2, err := executor.Execute(context.Background(), identity,
		executionharness.ToolRequest{ToolCallID: "call-2", ToolName: ToolTasksList,
			Arguments: json.RawMessage(`{"limit":2,"cursor":"` + decoded1.NextCursor + `"}`)})
	if err != nil {
		t.Fatal(err)
	}
	var decoded2 struct {
		Tasks []taskListView `json:"tasks"`
	}
	if err = json.Unmarshal(page2.Content, &decoded2); err != nil {
		t.Fatal(err)
	}
	if len(decoded2.Tasks) != 2 {
		t.Fatalf("page2=%+v", decoded2)
	}
	seen := map[int64]bool{}
	for _, task := range append(append([]taskListView{}, decoded1.Tasks...), decoded2.Tasks...) {
		if seen[task.TaskID] {
			t.Fatalf("task %d appeared on both pages", task.TaskID)
		}
		seen[task.TaskID] = true
	}

	catalog := RegistryToolCatalog{Registry: registry}
	definition, _ := catalog.Lookup(context.Background(), ToolTasksList)
	if err = catalog.ValidateArguments(context.Background(), definition, json.RawMessage(`{"cursor":"not-a-real-cursor"}`)); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err=%v want ErrInvalidInput for an invalid cursor", err)
	}
}

// Every registered ToolDescriptor must declare access=read_only -- this is
// this round's central invariant, checked once across the whole production
// registry rather than per family.
func TestEveryRegisteredToolDescriptorIsReadOnly(t *testing.T) {
	registry := NewToolRegistry()
	if err := RegisterTaskTools(registry, &fakeTaskReader{}); err != nil {
		t.Fatal(err)
	}
	for _, definition := range registry.Definitions() {
		descriptor, ok := registry.Lookup(definition.Name)
		if !ok {
			t.Fatalf("definitions must round-trip through Lookup: %q", definition.Name)
		}
		if descriptor.Access != AccessReadOnly {
			t.Fatalf("tool %q has access=%q, want read_only", definition.Name, descriptor.Access)
		}
	}
}

// TestToolDescriptorValidateRequiresMaxResultBytesRegardlessOfMaxRows is a
// regression test: MaxResultBytes must be positive on its own, never waived
// just because MaxRows>0. RegistryToolExecutor.Execute only enforces the
// byte bound when MaxResultBytes itself is positive (see its own doc
// comment) -- a descriptor that validated with MaxRows>0 and
// MaxResultBytes<=0 would run with NO result-size bound at all, silently
// defeating BOUNDED_RESULTS.
func TestToolDescriptorValidateRequiresMaxResultBytesRegardlessOfMaxRows(t *testing.T) {
	registry := NewToolRegistry()
	err := registry.Register(ToolDescriptor{
		ID: "test.unbounded_bytes", Version: "v1", Description: "d",
		InputSchema: json.RawMessage(`{"type":"object"}`), Access: AccessReadOnly, RequiredRole: CEORoleID,
		Limits: ToolLimits{MaxRows: 20, MaxResultBytes: 0, Timeout: time.Second}, DataClass: DataClassInternal,
	}, func(json.RawMessage) error { return nil },
		func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`{}`), nil
		})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err=%v want ErrInvalidInput (MaxRows>0 must not waive the MaxResultBytes>0 requirement)", err)
	}
}
