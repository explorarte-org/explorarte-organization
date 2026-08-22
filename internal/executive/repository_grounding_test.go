package executive

import (
	"context"
	"strings"
	"testing"
)

// recordingContexts captures what each execution was allowed to observe.
type recordingContexts struct {
	inner    ContextCoordinator
	requests []ContextRequest
}

func (r *recordingContexts) Build(ctx context.Context, request ContextRequest) (ContextSnapshot, error) {
	r.requests = append(r.requests, request)
	return r.inner.Build(ctx, request)
}

func (r *recordingContexts) forPurpose(purpose ExecutionPurpose) []ContextRequest {
	var out []ContextRequest
	for _, request := range r.requests {
		if request.ExecutionPurpose == string(purpose) {
			out = append(out, request)
		}
	}
	return out
}

func newGroundingFixture(t *testing.T) (*missionFixture, *recordingContexts) {
	t.Helper()
	fixture := newMissionFixture(t, smokePath, false)
	recorder := &recordingContexts{inner: fixture.orchestrator.contexts}
	fixture.orchestrator.contexts = recorder
	return fixture, recorder
}

// P2: an execution allowed to observe code receives the PINNED commit, and
// the same one every time.
//
// The commit must come from the durable pin, never from resolving the
// promotion target again. Resolving it here would let two rounds of one design
// read two different repositories -- the exact failure the pin exists to
// prevent, reintroduced where it would be least visible.
func TestCodeGroundedExecutionsCarryThePinnedWorld(t *testing.T) {
	fixture, recorder := newGroundingFixture(t)
	fixture.drive(t)

	grounded := 0
	for _, purpose := range []ExecutionPurpose{
		PurposeDepartmentPlan, PurposeDepartmentWorker, PurposeDepartmentReview,
		PurposeDesignAdjudication, PurposeImplementationPlan,
	} {
		for _, request := range recorder.forPurpose(purpose) {
			grounded++
			if request.RepositoryBaseSHA != targetSHA {
				t.Fatalf("%s carried base sha %q, want the pinned %q", purpose, request.RepositoryBaseSHA, targetSHA)
			}
			if request.RepositoryQuery == "" {
				t.Fatalf("%s carried no selection text, so it would observe nothing", purpose)
			}
		}
	}
	if grounded == 0 {
		t.Fatal("no code-grounded execution ran, so this proves nothing")
	}
}

// P6 in transport form: the adversarial reviewer is never given a repository
// to observe.
//
// Not for cost. Its context admits only public and sanitized data, and
// repository evidence is organizational. Reclassifying it so the reviewer
// could see source would widen an egress boundary as a side effect of a
// convenience.
func TestTheAdversarialReviewerIsNeverGivenARepository(t *testing.T) {
	fixture, recorder := newGroundingFixture(t)
	fixture.drive(t)

	for _, purpose := range []ExecutionPurpose{PurposeAdversarialReview, PurposeCEOPlan, PurposeCEOClosure} {
		requests := recorder.forPurpose(purpose)
		if len(requests) == 0 {
			continue
		}
		for _, request := range requests {
			if request.RepositoryBaseSHA != "" {
				t.Fatalf("%s was given repository grounding %q", purpose, request.RepositoryBaseSHA)
			}
			if request.RepositoryQuery != "" {
				t.Fatalf("%s was given a repository query", purpose)
			}
		}
	}
	if len(recorder.forPurpose(PurposeAdversarialReview)) == 0 {
		t.Fatal("the adversarial review never ran, so this proves nothing")
	}
}

// P1 in transport form: a campaign not governed by a design never carries a
// repository, whatever its purposes are.
func TestAnUngovernedCampaignCarriesNoRepository(t *testing.T) {
	fixture, recorder := newGroundingFixture(t)
	root, err := fixture.orchestrator.tasks.GetTask(context.Background(), fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	kept := root.Requirements[:0]
	for _, requirement := range root.Requirements {
		if requirement.Key != "design-freeze" && requirement.Key != MissionRequirementKey {
			kept = append(kept, requirement)
		}
	}
	root.Requirements = kept
	fixture.tasks.tasks[root.ID] = root
	fixture.drive(t)

	if len(recorder.requests) == 0 {
		t.Fatal("no context was built, so this proves nothing")
	}
	for _, request := range recorder.requests {
		if request.RepositoryBaseSHA != "" {
			t.Fatalf("an ungoverned campaign carried a repository into %s", request.ExecutionPurpose)
		}
	}
}

// The selection text has to describe THIS execution, not just the campaign.
// A worker task naming a symbol needs that symbol found, and the goal alone
// would never mention it.
func TestTheSelectionTextCarriesBothTheGoalAndTheTask(t *testing.T) {
	fixture, recorder := newGroundingFixture(t)
	fixture.drive(t)

	for _, request := range recorder.forPurpose(PurposeDepartmentWorker) {
		if !strings.Contains(request.RepositoryQuery, "AUTONOMY-SMOKE") {
			t.Fatalf("the query lost the goal: %q", request.RepositoryQuery)
		}
		if len(strings.TrimSpace(request.RepositoryQuery)) <= len("AUTONOMY-SMOKE") {
			t.Fatalf("the query carries only the goal, so every worker would read the same code: %q", request.RepositoryQuery)
		}
	}
}
