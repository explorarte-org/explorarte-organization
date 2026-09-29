package repositoryevidence

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/contextengine"
)

// Local smoke #35 (root 1848): design workers auditing the executive declined to name a defect
// because the declarations it turned on were outside their 48-line windows. The diet is pinned so
// a change to it is deliberate, and the window reads a function body, not just its signature.
func TestTheEvidenceDietSeesAFunctionNotJustItsSignature(t *testing.T) {
	limits := DefaultLimits()
	if limits.MaxFiles != 12 || limits.MaxRanges != 24 || limits.MaxBytes != 192*1024 || limits.MaxSearches != 16 {
		t.Fatalf("DefaultLimits = %+v", limits)
	}
	if 2*DefaultWindow < 80 {
		t.Fatalf("a %d-line excerpt window cannot hold a typical function body", 2*DefaultWindow)
	}
	if limits.MaxLines < 2*WorkerWindow {
		t.Fatalf("MaxLines %d would cut a single %d-line window", limits.MaxLines, 2*WorkerWindow)
	}
}

// By the owner's decision (2026-09-28) the executions that write -- a design worker and the
// implementation plan -- read twice the window a judge reads.
func TestWritersReadTwiceTheWindowJudgesRead(t *testing.T) {
	lines := make([]string, 1000)
	for i := range lines {
		lines[i] = "// filler"
	}
	lines[499] = "func (o *Orchestrator) driveDepartments() {}"
	source := newSource()
	source.lines["internal/executive/orchestrator.go"] = len(lines)
	source.content["internal/executive/orchestrator.go"] = strings.Join(lines, "\n")
	source.found = []Match{{Path: "internal/executive/orchestrator.go", Line: 500}}
	provider, err := NewProvider("explorarte-organization", source, DefaultLimits(), DefaultWindow)
	if err != nil {
		t.Fatal(err)
	}
	span := func(purpose string) int {
		records, err := provider.ListRepositoryEvidence(context.Background(), contextengine.BuildRequest{
			RepositoryBaseSHA: shaA, ExecutionPurpose: purpose,
			RepositoryQuery: "internal/executive/orchestrator.go driveDepartments",
		})
		if err != nil {
			t.Fatal(err)
		}
		widest := 0
		for _, record := range records {
			var start, end int
			if _, scanErr := fmt.Sscanf(record.Reference[strings.LastIndex(record.Reference, "#L")+2:], "%d-L%d", &start, &end); scanErr == nil && end-start+1 > widest {
				widest = end - start + 1
			}
		}
		return widest
	}
	judge, worker, planner := span("design-adjudication"), span("department-worker"), span("implementation-plan")
	if judge != 2*DefaultWindow || worker != 2*WorkerWindow || planner != worker {
		t.Fatalf("excerpt spans: judge %d, worker %d, implementation plan %d; want %d, %d, %d",
			judge, worker, planner, 2*DefaultWindow, 2*WorkerWindow, 2*WorkerWindow)
	}
}
