package programbudget

import (
	"context"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

type policyTasks struct {
	details map[int64]tasks.TaskDetail
	list    []tasks.Task
}

func (f policyTasks) GetTask(_ context.Context, id int64) (tasks.TaskDetail, error) {
	return f.details[id], nil
}
func (f policyTasks) ListTasks(context.Context, tasks.TaskFilter) ([]tasks.Task, error) {
	return f.list, nil
}
func (f policyTasks) ListEvents(context.Context, int64, int) ([]tasks.Event, error) { return nil, nil }
func (f policyTasks) ListAttempts(context.Context, int64) ([]tasks.Attempt, error)  { return nil, nil }
func (f policyTasks) ListDeadLetters(context.Context, int) ([]tasks.DeadLetter, error) {
	return nil, nil
}
func (f policyTasks) GetDeadLetter(context.Context, int64) (tasks.DeadLetter, error) {
	return tasks.DeadLetter{}, nil
}

func TestDecodeDollarPolicyAndResolveByCausationRoot(t *testing.T) {
	correlation := "program-1"
	causation := "child"
	root := tasks.Task{ID: 10, OrganizationID: "org", CorrelationID: &correlation}
	child := tasks.Task{ID: 20, OrganizationID: "org", CorrelationID: &correlation, CausationID: &causation}
	policy := map[string]any{"schema_version": SchemaVersion, "program_root_task_id": 10, "families": []any{map[string]any{"key": "deepseek", "provider_ids": []string{"deepseek"}, "model_ids": []string{"deepseek-v4-pro", "deepseek-v4-flash"}, "max_usd": 7}}}
	p, err := Decode(policy)
	if err != nil || p.Validate() != nil || p.Families[0].MaxUSD.USD() != 7 {
		t.Fatalf("decode policy: %+v %v", p, err)
	}
	detail := tasks.TaskDetail{Task: root, Evidence: []tasks.Evidence{{Reference: "program-model-budget://10", Metadata: policy}}}
	r := Resolver{Tasks: policyTasks{details: map[int64]tasks.TaskDetail{10: detail, 20: {Task: child}}, list: []tasks.Task{root, child}}}
	scope, err := r.Resolve(context.Background(), 20, "deepseek", "deepseek-v4-flash")
	if err != nil || scope.ProgramRootTaskID != 10 || scope.Family.Key != "deepseek" {
		t.Fatalf("resolve: %+v %v", scope, err)
	}
}

func TestResolveRejectsUnauthorizedProvider(t *testing.T) {
	c := "p"
	task := tasks.Task{ID: 1, OrganizationID: "org", CorrelationID: &c}
	d := tasks.TaskDetail{Task: task, Evidence: []tasks.Evidence{{Reference: "program-model-budget://1", Metadata: map[string]any{"schema_version": SchemaVersion, "program_root_task_id": 1, "families": []any{map[string]any{"key": "deepseek", "provider_ids": []string{"deepseek"}, "model_ids": []string{"deepseek-v4-pro"}, "max_usd": 7}}}}}}
	r := Resolver{Tasks: policyTasks{details: map[int64]tasks.TaskDetail{1: d}, list: []tasks.Task{task}}}
	if _, err := r.Resolve(context.Background(), 1, "unknown", "model"); err == nil {
		t.Fatal("expected unauthorized route denial")
	}
}
