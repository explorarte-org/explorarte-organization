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
	err             error
}

func (f *fakePromotionRequester) RequestPromotion(_ context.Context, taskID, workspaceID int64, actorRole string) (staging.Promotion, error) {
	f.task, f.workspace, f.role = taskID, workspaceID, actorRole
	return staging.Promotion{}, f.err
}

// The review is requested as the code-runner role that produced the candidate, which is the role the
// owner's review must differ from; the adapter can do nothing else.
func TestTheReviewIsRequestedAsTheCodeRunner(t *testing.T) {
	fake := &fakePromotionRequester{}
	if err := (missionReviewRequester{missions: fake}).RequestMissionReview(context.Background(), 1489, 13); err != nil {
		t.Fatal(err)
	}
	if fake.task != 1489 || fake.workspace != 13 || fake.role != "ingenieria_ia/code-runner" {
		t.Fatalf("requested task=%d workspace=%d role=%q", fake.task, fake.workspace, fake.role)
	}
	fake.err = errors.New("gates unsatisfied")
	if err := (missionReviewRequester{missions: fake}).RequestMissionReview(context.Background(), 1489, 13); !errors.Is(err, fake.err) {
		t.Fatalf("the adapter hid the failure: %v", err)
	}
}
