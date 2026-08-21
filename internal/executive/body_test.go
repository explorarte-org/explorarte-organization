package executive

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type bodyModels struct {
	results map[int64]InvocationResult
	err     error
	asked   []int64
}

func (f *bodyModels) GetResult(_ context.Context, id int64) (InvocationResult, error) {
	f.asked = append(f.asked, id)
	if f.err != nil {
		return InvocationResult{}, f.err
	}
	result, ok := f.results[id]
	if !ok {
		return InvocationResult{}, errors.New("no such invocation")
	}
	return result, nil
}

// The rest of the port is unused by candidateBody and must stay that way:
// the body is resolved from the artifact's own invocation ids, never by
// re-discovering work through the task graph.
func (f *bodyModels) GetInvocation(context.Context, int64) (InvocationRecord, error) {
	panic("candidateBody must not look up invocations")
}

func (f *bodyModels) ProviderFailureRetryable(context.Context, int64) (bool, error) {
	panic("candidateBody must not classify failures")
}

func (f *bodyModels) FindTaskAttemptInvocations(context.Context, int64, int64) ([]InvocationRecord, error) {
	panic("candidateBody must not search the task graph for deliverables")
}

func bodyOrchestrator(results map[int64]InvocationResult) (*Orchestrator, *bodyModels) {
	models := &bodyModels{results: results}
	return &Orchestrator{models: models, limits: Limits{MaxStringBytes: 4096}}, models
}

func twoUnitArtifact() designArtifact {
	return designArtifact{RootTaskID: 1, Round: 1, Units: []designUnitRef{
		{UnitID: "ingenieria_ia", TaskID: 10, InvocationID: 100, ResultHash: "aaa"},
		{UnitID: "investigacion", TaskID: 11, InvocationID: 101, ResultHash: "bbb"},
	}}
}

// The reviewer must be able to read the design. Reporting only identities and
// digests made the review structurally impossible: a reviewer that cannot
// read the thing can only say it could not verify anything, which is what it
// said, every time, and a verdict of revise followed forever.
func TestTheReviewerSeesTheDesignItIsAskedToJudge(t *testing.T) {
	o, models := bodyOrchestrator(map[int64]InvocationResult{
		100: {InvocationID: 100, TextOutput: "AUTONOMY_SMOKE_008.md will record the governed cycle.", ResponseHash: "aaa"},
		101: {InvocationID: 101, TextOutput: "No repository files change during design.", ResponseHash: "bbb"},
	})
	body, err := o.candidateBody(context.Background(), twoUnitArtifact())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"AUTONOMY_SMOKE_008.md will record the governed cycle.",
		"No repository files change during design.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the body must carry the deliverable text; missing %q", want)
		}
	}
	// And it stays bound to what the artifact says is under review.
	for _, want := range []string{"task:10 model-invocation:100 result:aaa", "task:11 model-invocation:101 result:bbb"} {
		if !strings.Contains(body, want) {
			t.Errorf("each section must carry its provenance; missing %q", want)
		}
	}
	// Nothing beyond the named invocations is read.
	if len(models.asked) != 2 || models.asked[0] != 100 || models.asked[1] != 101 {
		t.Fatalf("only the artifact's own invocations may be read, got %v", models.asked)
	}
}

// Readability must not be bought with provenance. If the body does not hash
// to what the artifact recorded, "here is the design" and "here is its
// digest" have become two independent claims.
func TestABodyThatDoesNotMatchItsRecordedHashIsRefused(t *testing.T) {
	o, _ := bodyOrchestrator(map[int64]InvocationResult{
		100: {InvocationID: 100, TextOutput: "something else entirely", ResponseHash: "not-aaa"},
		101: {InvocationID: 101, TextOutput: "fine", ResponseHash: "bbb"},
	})
	_, err := o.candidateBody(context.Background(), twoUnitArtifact())
	if err == nil {
		t.Fatal("a deliverable that drifted from its recorded digest must not be shown as the design")
	}
	if !errors.Is(err, ErrContractRejected) {
		t.Fatalf("drift is a contract failure, got %v", err)
	}
	for _, want := range []string{"not-aaa", "aaa"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must carry both digests; missing %q", want)
		}
	}
}

func TestAnEmptyDeliverableIsRefusedRatherThanShownAsADesign(t *testing.T) {
	o, _ := bodyOrchestrator(map[int64]InvocationResult{
		100: {InvocationID: 100, TextOutput: "   ", ResponseHash: "aaa"},
		101: {InvocationID: 101, TextOutput: "fine", ResponseHash: "bbb"},
	})
	if _, err := o.candidateBody(context.Background(), twoUnitArtifact()); err == nil {
		t.Fatal("an empty deliverable is not a design")
	}
}

// A deliverable that is JSON rather than prose is still the design.
func TestAJSONDeliverableIsUsedWhenThereIsNoText(t *testing.T) {
	o, _ := bodyOrchestrator(map[int64]InvocationResult{
		100: {InvocationID: 100, JSONOutput: []byte(`{"design":"markdown note"}`), ResponseHash: "aaa"},
		101: {InvocationID: 101, TextOutput: "fine", ResponseHash: "bbb"},
	})
	body, err := o.candidateBody(context.Background(), twoUnitArtifact())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, `"design":"markdown note"`) {
		t.Fatalf("the JSON deliverable must appear: %s", body)
	}
}

func TestTheBodyIsBoundedByTheHostLimits(t *testing.T) {
	o, _ := bodyOrchestrator(map[int64]InvocationResult{
		100: {InvocationID: 100, TextOutput: strings.Repeat("x", 9000), ResponseHash: "aaa"},
		101: {InvocationID: 101, TextOutput: "fine", ResponseHash: "bbb"},
	})
	body, err := o.candidateBody(context.Background(), twoUnitArtifact())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(body, "x") > 4096 {
		t.Fatalf("the deliverable must be truncated to the host limit, got %d", strings.Count(body, "x"))
	}
}
