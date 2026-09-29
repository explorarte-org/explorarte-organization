package runtimeadapter

import (
	"context"
	"strings"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/executive"
)

type prerequisiteTasks struct {
	tasks    map[int64]executive.TaskRecord
	recorded []executive.EvidenceCommand
}

func (p *prerequisiteTasks) GetTask(_ context.Context, id int64) (executive.TaskRecord, error) {
	task := p.tasks[id]
	for _, command := range p.recorded {
		if command.TaskID == id {
			task.Evidence = append(task.Evidence, executive.EvidenceRecord{Reference: command.Reference, Metadata: command.Metadata})
		}
	}
	return task, nil
}

func (p *prerequisiteTasks) RecordEvidence(_ context.Context, command executive.EvidenceCommand) error {
	p.recorded = append(p.recorded, command)
	return nil
}

// Local smoke #42 (root 2018): the servicios quality review depended on the designer's answer but
// ran without it, twice, and the department's replans ran out. A dependent worker is now shown the
// verified results of its prerequisites, once.
func TestADependentWorkerIsShownItsPrerequisitesResults(t *testing.T) {
	body := []byte(`{"schema_version":"worker-result/v1","summary":"the designer's answer, 233 words","evidence_refs":["artifact:abc"]}`)
	tasks := &prerequisiteTasks{tasks: map[int64]executive.TaskRecord{
		2020: {ID: 2020, IdempotencyKey: "executive:2018:leader-plan:servicios", Status: "completed"},
		2024: {ID: 2024, IdempotencyKey: "executive:2018:worker:servicios:answer", AssignedRoleID: "servicios/service_designer", Status: "completed",
			Attempts: []executive.AttemptRecord{{ID: 5, Ordinal: 1, State: "finished"}}},
		2025: {ID: 2025, AssignedRoleID: "servicios/analista_calidad", Status: "running", DependsOn: []int64{2020, 2024}},
	}}
	adapter := EvidenceTasks{
		Models: evidenceModels{
			invocation: executive.InvocationRecord{ID: 77, Status: "succeeded"},
			result:     executive.InvocationResult{InvocationID: 77, JSONOutput: body, ResponseHash: strings.Repeat("a", 64)},
		},
		Completion: passCompletion{}, Limits: executive.DefaultLimits(),
	}
	task, err := adapter.attachPrerequisites(context.Background(), tasks, tasks.tasks[2025])
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks.recorded) != 1 || !strings.HasPrefix(tasks.recorded[0].Reference, "executive-evidence:prerequisites:2025:") {
		t.Fatalf("recorded %+v", tasks.recorded)
	}
	bundle, ok := tasks.recorded[0].Metadata["bundle"].(map[string]any)
	prerequisites, _ := bundle["prerequisites"].([]any)
	if !ok || bundle["schema_version"] != executiveEvidenceSchema || len(prerequisites) != 1 ||
		prerequisites[0].(map[string]any)["summary"] != "the designer's answer, 233 words" {
		t.Fatalf("bundle %+v", bundle)
	}
	// Once: a task that already carries its bundle (a resumed attempt) is not given a second.
	if _, err := adapter.attachPrerequisites(context.Background(), tasks, task); err != nil || len(tasks.recorded) != 1 {
		t.Fatalf("second attach recorded %d bundles, err %v", len(tasks.recorded), err)
	}
}

func TestAWorkerIsNeverShownAnUnfinishedPrerequisite(t *testing.T) {
	tasks := &prerequisiteTasks{tasks: map[int64]executive.TaskRecord{
		1: {ID: 1, IdempotencyKey: "executive:9:worker:d:a", Status: "running"},
		2: {ID: 2, Status: "running", DependsOn: []int64{1}},
	}}
	adapter := EvidenceTasks{Models: evidenceModels{}, Completion: passCompletion{}}
	if _, err := adapter.attachPrerequisites(context.Background(), tasks, tasks.tasks[2]); err == nil || len(tasks.recorded) != 0 {
		t.Fatalf("err %v recorded %d: an unfinished prerequisite must fail, not attach an empty bundle", err, len(tasks.recorded))
	}
}

// Local smoke #46 (root 2057): every worker also depends on its department plan, which is not a
// worker result. A worker whose only prerequisite is its plan gets no bundle and no error.
func TestAWorkersDepartmentPlanIsNotProjectedAsAResult(t *testing.T) {
	tasks := &prerequisiteTasks{tasks: map[int64]executive.TaskRecord{
		2059: {ID: 2059, IdempotencyKey: "executive:2057:leader-plan:servicios", Status: "completed"},
		2063: {ID: 2063, Status: "running", DependsOn: []int64{2059}},
	}}
	adapter := EvidenceTasks{Models: evidenceModels{}, Completion: passCompletion{}}
	if _, err := adapter.attachPrerequisites(context.Background(), tasks, tasks.tasks[2063]); err != nil || len(tasks.recorded) != 0 {
		t.Fatalf("err %v, recorded %d: the plan must be skipped", err, len(tasks.recorded))
	}
}
