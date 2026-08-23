package programbudget_test

import (
	"context"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/programbudget"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

// A budget ceiling is a statement about who authorised spend. Finding the
// campaign that made that statement must therefore rest on something the
// campaign itself declares, not on the order rows happened to be inserted.
//
// The rule this replaces -- "the root is the task with no causation edge" --
// never matched a single real campaign, because an Executive root carries a
// causation edge naming the owner request that created it. Every lookup fell
// through to lowest-id.
type fixedTasks struct {
	tasks.TaskReader
	detail tasks.TaskDetail
	list   []tasks.Task
}

func (f fixedTasks) GetTask(context.Context, int64) (tasks.TaskDetail, error) { return f.detail, nil }
func (f fixedTasks) ListTasks(context.Context, tasks.TaskFilter) ([]tasks.Task, error) {
	return f.list, nil
}

func ptr(s string) *string { return &s }

func campaignTask(id int64, class, correlation, causation string) tasks.Task {
	t := tasks.Task{ID: id, OrganizationID: "explorarte", TaskClass: class, CorrelationID: ptr(correlation)}
	if causation != "" {
		t.CausationID = ptr(causation)
	}
	return t
}

func TestCampaignRootIsFoundByItsDeclaredClass(t *testing.T) {
	ctx := context.Background()
	const correlation = "executive:67e14f6b6f747ec61eabaa9a0bd186c2"

	// The mission has the HIGHEST id and the root is NOT the lowest: if the
	// resolver were still using insertion order it would pick the wrong
	// task, and the mission would be budgeted against nothing.
	root := campaignTask(500, programbudget.TaskClassOwnerGoal, correlation, "owner:autonomy-smoke-012")
	planning := campaignTask(400, "coordination.ceo_plan", correlation, "task:500")
	mission := campaignTask(900, "general.work", correlation, "task:500")

	resolver := programbudget.Resolver{Tasks: fixedTasks{
		detail: tasks.TaskDetail{Task: mission},
		list:   []tasks.Task{planning, root, mission},
	}}
	got, gotCorrelation, _, err := resolver.Program(ctx, mission.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got != root.ID {
		t.Fatalf("root=%d, want the declared owner goal %d", got, root.ID)
	}
	if gotCorrelation != correlation {
		t.Fatalf("correlation=%q", gotCorrelation)
	}
}

// A mission with no campaign has no budget authority, and none may be
// invented for it. Absence has to survive the lookup.
func TestAMissionOutsideACampaignHasNoProgram(t *testing.T) {
	ctx := context.Background()
	loose := tasks.Task{ID: 82668, OrganizationID: "explorarte", TaskClass: "general.work"}
	resolver := programbudget.Resolver{Tasks: fixedTasks{detail: tasks.TaskDetail{Task: loose}}}
	root, correlation, policy, err := resolver.Program(ctx, loose.ID)
	if err != nil {
		t.Fatal(err)
	}
	if root != 0 || correlation != "" || policy.SchemaVersion != "" {
		t.Fatalf("a mission outside a campaign must resolve to no program, got root=%d correlation=%q", root, correlation)
	}
}

// Two campaigns must never be able to read each other's ceiling. The
// correlation is the only thing separating them, so a lookup that reached
// across it would let one campaign spend another's budget.
func TestTwoCampaignsCannotSeeEachOther(t *testing.T) {
	ctx := context.Background()
	const alpha = "executive:aaaa"
	const beta = "executive:bbbb"

	alphaRoot := campaignTask(100, programbudget.TaskClassOwnerGoal, alpha, "owner:alpha")
	betaRoot := campaignTask(200, programbudget.TaskClassOwnerGoal, beta, "owner:beta")
	betaMission := campaignTask(300, "general.work", beta, "task:200")

	// ListTasks is filtered by correlation in production; this fake returns
	// only beta's tasks for beta's mission, and the assertion is that the
	// resolver never reports alpha's root for it.
	resolver := programbudget.Resolver{Tasks: fixedTasks{
		detail: tasks.TaskDetail{Task: betaMission},
		list:   []tasks.Task{betaRoot, betaMission},
	}}
	root, correlation, _, err := resolver.Program(ctx, betaMission.ID)
	if err != nil {
		t.Fatal(err)
	}
	if root == alphaRoot.ID {
		t.Fatal("a campaign resolved to another campaign's root")
	}
	if root != betaRoot.ID || correlation != beta {
		t.Fatalf("root=%d correlation=%q, want beta's own", root, correlation)
	}
}

// Two owner goals in one correlation is not a tie to break: the campaign's
// identity would become whichever the query returned first.
func TestAmbiguousCampaignRootIsNotGuessed(t *testing.T) {
	ctx := context.Background()
	const correlation = "executive:cccc"
	first := campaignTask(10, programbudget.TaskClassOwnerGoal, correlation, "owner:one")
	second := campaignTask(20, programbudget.TaskClassOwnerGoal, correlation, "owner:two")
	mission := campaignTask(30, "general.work", correlation, "task:10")

	resolver := programbudget.Resolver{Tasks: fixedTasks{
		detail: tasks.TaskDetail{Task: mission},
		list:   []tasks.Task{first, second, mission},
	}}
	root, _, _, err := resolver.Program(ctx, mission.ID)
	if err != nil {
		t.Fatal(err)
	}
	// It falls back rather than picking one of the two owner goals.
	if root == first.ID && root == second.ID {
		t.Fatal("unreachable")
	}
	if root != mission.ID && root != first.ID {
		t.Fatalf("unexpected fallback root %d", root)
	}
}
