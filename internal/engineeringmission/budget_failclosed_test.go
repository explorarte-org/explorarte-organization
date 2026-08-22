package engineeringmission

import (
	"context"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/modelpricing"
	"github.com/Mireuz13/explorarte-organization/internal/programbudget"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

type budgetTasks struct {
	tasks.TaskReader
	detail tasks.TaskDetail
	list   []tasks.Task
}

func (b budgetTasks) GetTask(context.Context, int64) (tasks.TaskDetail, error) { return b.detail, nil }
func (b budgetTasks) ListTasks(context.Context, tasks.TaskFilter) ([]tasks.Task, error) {
	return b.list, nil
}

type noSpend struct{}

func (noSpend) ProgramFamilySpend(context.Context, string, string, []string) (modelpricing.USDNanos, error) {
	return 0, nil
}
func (noSpend) TaskFamilySpend(context.Context, int64, string, []string) (modelpricing.USDNanos, error) {
	return 0, nil
}

// Recovery mints new autonomous work, so the absence of a ceiling is a missing
// ceiling, never permission. A mission that belongs to no campaign has no
// authority that budgeted it, and the honest answer is to refuse.
//
// This is the property that keeps the correlation fix from being cosmetic: if
// a mission ever again reaches a dead letter without its campaign, recovery
// declines instead of quietly spending against nothing.
func TestRecoveryIsFailClosedWithoutABudgetAuthority(t *testing.T) {
	ctx := context.Background()
	orphan := tasks.Task{ID: 82668, OrganizationID: "explorarte", TaskClass: "general.work"}
	admission := ProgramBudgetAdmission{
		Programs: programbudget.Resolver{Tasks: budgetTasks{detail: tasks.TaskDetail{Task: orphan}}},
		Spend:    noSpend{},
	}
	verdict, err := admission.Admit(ctx, orphan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if verdict.Admitted {
		t.Fatal("a mission with no campaign must not be admitted: nothing budgeted it")
	}
	if verdict.Reason == "" {
		t.Fatal("a refusal must say why")
	}
}
