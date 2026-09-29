package ceochat

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

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

// A finding reaches the CEO with what it found, bounded: a full page of findings with long titles
// stays inside the tool's result limit.
func TestListFindingsCarriesBoundedEvidence(t *testing.T) {
	long := strings.Repeat("título ", 60)
	refs := make([]search.EvidenceRef, 6)
	for i := range refs {
		refs[i] = search.EvidenceRef{Title: long, URL: "https://doi.org/10.1/" + strings.Repeat("x", 80), DOI: "10.1/x", ArxivID: "2609.01234v1", Snippet: strings.Repeat("resumen ", 80)}
	}
	page := make([]search.ResearchFinding, maxFindingsLimit)
	for i := range page {
		page[i] = search.ResearchFinding{ID: "finding-cycle-investigacion-topic-" + strings.Repeat("9", 40), TopicID: "t", DepartmentID: "investigacion", Summary: strings.Repeat("s", 120), EvidenceRefs: refs}
	}
	executor := ToolExecutor{Findings: &fakeFindingLister{findings: page}}
	result, err := executor.executeListFindings(context.Background(), []byte(`{"limit":20}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Content) > findingsResultBytes {
		t.Fatalf("a full page is %d bytes, over the %d-byte tool bound", len(result.Content), findingsResultBytes)
	}
	var decoded struct {
		Findings []findingView `json:"findings"`
	}
	if err := json.Unmarshal(result.Content, &decoded); err != nil {
		t.Fatal(err)
	}
	evidence := decoded.Findings[0].Evidence
	if len(evidence) != maxEvidencePerFinding || evidence[0].URL != refs[0].URL || evidence[0].DOI != "10.1/x" ||
		!utf8.ValidString(evidence[0].Title) || len(evidence[0].Title) > maxEvidenceTitleBytes+len("…") ||
		!strings.HasPrefix(evidence[0].Snippet, "resumen") || len(evidence[0].Snippet) > maxEvidenceSnippetBytes+len("…") {
		t.Fatalf("evidence %+v", evidence)
	}
}

// The research tools require research.findings.read of the executing role.
func TestResearchToolsRequireTheReadCapability(t *testing.T) {
	findings := &fakeFindingLister{findings: []search.ResearchFinding{{ID: "f1"}}}
	ctx := WithTurnContext(context.Background(), TurnContext{OrganizationID: "org-test", OrganizationRevisionID: 1, ActorRoleID: "owner"})
	identity := executionharness.RunIdentity{OrganizationID: "org-test", RoleID: CEORoleID}
	request := executionharness.ToolRequest{ToolName: ToolListFindings, ToolCallID: "call_1", Arguments: json.RawMessage(`{}`)}
	for name, allowed := range map[string]map[string]bool{
		"granted": {"empresa/ceo:research.findings.read": true},
		"denied":  {"owner:research.findings.read": true},
	} {
		registry := NewToolRegistry()
		if err := RegisterResearchTools(registry, nil, findings, fakeAuthorizer{allowed: allowed}); err != nil {
			t.Fatal(err)
		}
		_, err := RegistryToolExecutor{Registry: registry}.Execute(ctx, identity, request)
		if (name == "granted") != (err == nil) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

// Local smoke #51: the CEO asked for 50 findings against an unstated maximum. The schemas state
// every bound the host enforces.
func TestResearchToolSchemasStateTheirLimits(t *testing.T) {
	for name, raw := range map[string]json.RawMessage{ToolListFindings: listFindingsSchema, ToolListTopics: listTopicsSchema} {
		var schema struct {
			Properties map[string]struct {
				Maximum *int `json:"maximum"`
				Minimum *int `json:"minimum"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		limit := schema.Properties["limit"]
		want := maxFindingsLimit
		if name == ToolListTopics {
			want = maxTopicsLimit
		}
		if limit.Maximum == nil || *limit.Maximum != want || limit.Minimum == nil || *limit.Minimum != 1 {
			t.Errorf("%s limit bounds %+v, want 1..%d", name, limit, want)
		}
	}
}
