package bootstrap

import (
	"context"
	"errors"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/staging"
)

type fakePromotionRequester struct {
	task, workspace int64
	role            string
	calls           int
	err             error
}

func (f *fakePromotionRequester) RequestPromotion(_ context.Context, taskID, workspaceID int64, actorRole string) (staging.Promotion, error) {
	f.calls++
	f.task, f.workspace, f.role = taskID, workspaceID, actorRole
	return staging.Promotion{}, f.err
}

type fakePromotionOpener struct {
	command staging.RequestPromotionCommand
	calls   int
}

func (f *fakePromotionOpener) RequestPromotion(_ context.Context, command staging.RequestPromotionCommand) (staging.Promotion, error) {
	f.calls++
	f.command = command
	return staging.Promotion{}, nil
}

type fakePromotionLookup struct {
	exists bool
	err    error
}

func (f fakePromotionLookup) WorkspaceHasPromotion(context.Context, int64) (bool, error) {
	return f.exists, f.err
}

// The review is requested as the code-runner role that produced the candidate, which is the role the
// owner's review must differ from; the adapter can do nothing else.
func TestTheReviewIsRequestedAsTheCodeRunner(t *testing.T) {
	fake := &fakePromotionRequester{}
	requester := missionReviewRequester{missions: fake, promotions: &fakePromotionOpener{}, lookup: fakePromotionLookup{}}
	if err := requester.RequestMissionReview(context.Background(), 1489, 13, false); err != nil {
		t.Fatal(err)
	}
	if fake.task != 1489 || fake.workspace != 13 || fake.role != "ingenieria_ia/code-runner" {
		t.Fatalf("requested task=%d workspace=%d role=%q", fake.task, fake.workspace, fake.role)
	}
	fake.err = errors.New("gates unsatisfied")
	if err := requester.RequestMissionReview(context.Background(), 1489, 13, false); !errors.Is(err, fake.err) {
		t.Fatalf("the adapter hid the failure: %v", err)
	}
}

// External audit A2: the request is idempotent per workspace. An existing promotion is the answer;
// recorded gates with no promotion get only the promotion, as the code-runner; pending gates get both.
func TestTheReviewRequestIsIdempotentPerWorkspace(t *testing.T) {
	missions, opener := &fakePromotionRequester{}, &fakePromotionOpener{}
	existing := missionReviewRequester{missions: missions, promotions: opener, lookup: fakePromotionLookup{exists: true}}
	for _, gatesRecorded := range []bool{false, true} {
		if err := existing.RequestMissionReview(context.Background(), 1489, 13, gatesRecorded); err != nil {
			t.Fatal(err)
		}
	}
	if missions.calls != 0 || opener.calls != 0 {
		t.Fatalf("a workspace with a promotion was asked again: check+promotion %d, promotion %d", missions.calls, opener.calls)
	}

	recovering := missionReviewRequester{missions: missions, promotions: opener, lookup: fakePromotionLookup{}}
	if err := recovering.RequestMissionReview(context.Background(), 1489, 13, true); err != nil {
		t.Fatal(err)
	}
	if missions.calls != 0 || opener.calls != 1 || opener.command.WorkspaceID != 13 || opener.command.ActorRoleID != "ingenieria_ia/code-runner" {
		t.Fatalf("recorded gates without a promotion: check+promotion %d, promotion %d %+v; want the promotion only", missions.calls, opener.calls, opener.command)
	}

	lookupErr := errors.New("database unavailable")
	failing := missionReviewRequester{missions: missions, promotions: opener, lookup: fakePromotionLookup{err: lookupErr}}
	if err := failing.RequestMissionReview(context.Background(), 1489, 13, true); !errors.Is(err, lookupErr) || opener.calls != 1 {
		t.Fatalf("an unanswered lookup must not open anything: err=%v promotion calls=%d", err, opener.calls)
	}
}
