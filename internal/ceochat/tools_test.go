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
