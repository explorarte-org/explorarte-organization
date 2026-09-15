package ceochat

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/executionharness"
	"github.com/Mireuz13/explorarte-organization/internal/search"
)

func TestToolCatalogKnowsExactlyTheV1Tools(t *testing.T) {
	catalog := NewToolCatalog()
	if _, ok := catalog.Lookup(context.Background(), ToolListTopics); !ok {
		t.Fatal("catalog must know research.list_topics")
	}
	if _, ok := catalog.Lookup(context.Background(), ToolListFindings); !ok {
		t.Fatal("catalog must know research.list_findings")
	}
	for _, unknown := range []string{"shell.exec", "git.push", "engineering.apply", "secrets.read", "research.investigate", "deploy.run"} {
		if _, ok := catalog.Lookup(context.Background(), unknown); ok {
			t.Fatalf("catalog must not know %q in V1", unknown)
		}
	}
}

// Negative test C: invalid tool arguments must be rejected by the catalog
// before any executor is reached.
func TestValidateArgumentsRejectsOutOfBoundLimit(t *testing.T) {
	catalog := NewToolCatalog()
	definition, _ := catalog.Lookup(context.Background(), ToolListFindings)
	err := catalog.ValidateArguments(context.Background(), definition, json.RawMessage(`{"limit":1000000}`))
	if err == nil {
		t.Fatal("want an error for limit=1000000")
	}
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err=%v want ErrInvalidInput", err)
	}
}

func TestValidateArgumentsRejectsUnknownField(t *testing.T) {
	catalog := NewToolCatalog()
	definition, _ := catalog.Lookup(context.Background(), ToolListFindings)
	err := catalog.ValidateArguments(context.Background(), definition, json.RawMessage(`{"limit":5,"unexpected":"x"}`))
	if err == nil {
		t.Fatal("want an error for an unknown argument field")
	}
}

func TestValidateArgumentsAcceptsEmptyObject(t *testing.T) {
	catalog := NewToolCatalog()
	for _, name := range []string{ToolListTopics, ToolListFindings} {
		definition, _ := catalog.Lookup(context.Background(), name)
		if err := catalog.ValidateArguments(context.Background(), definition, json.RawMessage(`{}`)); err != nil {
			t.Fatalf("%s: unexpected error for empty args: %v", name, err)
		}
	}
}

type fakeTopicLister struct {
	topics []search.ResearchTopic
	calls  int
}

func (f *fakeTopicLister) ListTopics(context.Context, string) ([]search.ResearchTopic, error) {
	f.calls++
	return f.topics, nil
}

type fakeFindingLister struct {
	findings  []search.ResearchFinding
	calls     int
	lastLimit int
}

func (f *fakeFindingLister) ListFindings(_ context.Context, filter search.FindingFilter) ([]search.ResearchFinding, error) {
	f.calls++
	f.lastLimit = filter.Limit
	return f.findings, nil
}

func TestToolExecutorListFindingsAppliesDefaultLimit(t *testing.T) {
	findings := &fakeFindingLister{findings: []search.ResearchFinding{{ID: "f1", Summary: "s"}}}
	executor := ToolExecutor{Findings: findings}
	result, err := executor.executeListFindings(context.Background(), []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if findings.lastLimit != defaultFindingsLimit {
		t.Fatalf("limit=%d want %d", findings.lastLimit, defaultFindingsLimit)
	}
	var decoded struct {
		Findings []findingView `json:"findings"`
	}
	if err := json.Unmarshal(result.Content, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Findings) != 1 || decoded.Findings[0].ID != "f1" {
		t.Fatalf("decoded=%+v", decoded)
	}
}

func TestToolExecutorRejectsUnknownToolName(t *testing.T) {
	executor := ToolExecutor{}
	_, err := executor.Execute(context.Background(), executionharness.RunIdentity{},
		executionharness.ToolRequest{ToolCallID: "call-1", ToolName: "shell.exec", Arguments: json.RawMessage(`{}`)})
	if err == nil {
		t.Fatal("want an error for an unrecognized tool name")
	}
}

func TestHostValidationEnforcesTasksBounds(t *testing.T) {
	// tasks.list limit validation (1..50)
	for _, invalidLimit := range []int{0, -1, 51, 100} {
		body, _ := json.Marshal(map[string]any{"limit": invalidLimit})
		if _, err := decodeTasksListArgs(body); err == nil {
			t.Errorf("tasks.list must reject limit=%d, got nil", invalidLimit)
		} else if !errors.Is(err, ErrInvalidInput) {
			t.Errorf("tasks.list limit=%d error=%v want ErrInvalidInput", invalidLimit, err)
		}
	}
	// tasks.list valid limit
	for _, validLimit := range []int{1, 20, 50} {
		body, _ := json.Marshal(map[string]any{"limit": validLimit})
		if _, err := decodeTasksListArgs(body); err != nil {
			t.Errorf("tasks.list must accept limit=%d, got error: %v", validLimit, err)
		}
	}

	// tasks.get task_id validation (> 0)
	for _, invalidID := range []int64{0, -1, -99} {
		body, _ := json.Marshal(map[string]any{"task_id": invalidID})
		if _, err := decodeTaskIDArgs(body); err == nil {
			t.Errorf("tasks.get must reject task_id=%d, got nil", invalidID)
		} else if !errors.Is(err, ErrInvalidInput) {
			t.Errorf("tasks.get task_id=%d error=%v want ErrInvalidInput", invalidID, err)
		}
	}

	// tasks.list_attempts limit validation (1..20) and task_id (> 0)
	for _, invalidLimit := range []int{0, -1, 21, 100} {
		body, _ := json.Marshal(map[string]any{"task_id": 1, "limit": invalidLimit})
		if _, err := decodeTasksListAttemptsArgs(body); err == nil {
			t.Errorf("tasks.list_attempts must reject limit=%d, got nil", invalidLimit)
		} else if !errors.Is(err, ErrInvalidInput) {
			t.Errorf("tasks.list_attempts limit=%d error=%v want ErrInvalidInput", invalidLimit, err)
		}
	}
	for _, invalidID := range []int64{0, -1} {
		body, _ := json.Marshal(map[string]any{"task_id": invalidID, "limit": 10})
		if _, err := decodeTasksListAttemptsArgs(body); err == nil {
			t.Errorf("tasks.list_attempts must reject task_id=%d, got nil", invalidID)
		}
	}
}

func TestHostValidationEnforcesRunsBounds(t *testing.T) {
	// runs.list_recent limit validation (1..30)
	for _, invalidLimit := range []int{0, -1, 31, 100} {
		body, _ := json.Marshal(map[string]any{"limit": invalidLimit})
		if _, err := decodeRunsListRecentArgs(body); err == nil {
			t.Errorf("runs.list_recent must reject limit=%d, got nil", invalidLimit)
		} else if !errors.Is(err, ErrInvalidInput) {
			t.Errorf("runs.list_recent limit=%d error=%v want ErrInvalidInput", invalidLimit, err)
		}
	}
	// runs.list_recent task_id validation (> 0 when provided)
	for _, invalidID := range []int64{0, -1, -50} {
		body, _ := json.Marshal(map[string]any{"task_id": invalidID})
		if _, err := decodeRunsListRecentArgs(body); err == nil {
			t.Errorf("runs.list_recent must reject task_id=%d, got nil", invalidID)
		} else if !errors.Is(err, ErrInvalidInput) {
			t.Errorf("runs.list_recent task_id=%d error=%v want ErrInvalidInput", invalidID, err)
		}
	}
	// runs.list_recent accepts omitted task_id
	if _, err := decodeRunsListRecentArgs([]byte(`{}`)); err != nil {
		t.Errorf("runs.list_recent must accept empty object: %v", err)
	}

	// runs.get run_id validation
	if _, err := decodeRunsGetArgs([]byte(`{"run_id":""}`)); err == nil {
		t.Error("runs.get must reject empty run_id")
	}
	if _, err := decodeRunsGetArgs([]byte(`{"run_id":"   "}`)); err == nil {
		t.Error("runs.get must reject whitespace run_id")
	}
}

func TestHostValidationEnforcesFinanceAndMemoryBounds(t *testing.T) {
	// finance.get_cost_summary task_id validation (> 0 when provided)
	for _, invalidID := range []int64{0, -1, -10} {
		body, _ := json.Marshal(map[string]any{"task_id": invalidID})
		if _, err := decodeFinanceGetCostSummaryArgs(body); err == nil {
			t.Errorf("finance.get_cost_summary must reject task_id=%d, got nil", invalidID)
		} else if !errors.Is(err, ErrInvalidInput) {
			t.Errorf("finance.get_cost_summary task_id=%d error=%v want ErrInvalidInput", invalidID, err)
		}
	}
	// finance.get_cost_summary accepts empty object
	if _, err := decodeFinanceGetCostSummaryArgs([]byte(`{}`)); err != nil {
		t.Errorf("finance.get_cost_summary must accept empty object: %v", err)
	}

	// memory.search query required, task_id (> 0), limit (1..20)
	if _, err := decodeMemorySearchArgs([]byte(`{"query":""}`)); err == nil {
		t.Error("memory.search must reject empty query")
	}
	for _, invalidID := range []int64{0, -1} {
		body, _ := json.Marshal(map[string]any{"query": "valid", "task_id": invalidID})
		if _, err := decodeMemorySearchArgs(body); err == nil {
			t.Errorf("memory.search must reject task_id=%d, got nil", invalidID)
		}
	}
	for _, invalidLimit := range []int{0, -1, 21, 50} {
		body, _ := json.Marshal(map[string]any{"query": "valid", "limit": invalidLimit})
		if _, err := decodeMemorySearchArgs(body); err == nil {
			t.Errorf("memory.search must reject limit=%d, got nil", invalidLimit)
		}
	}
}
