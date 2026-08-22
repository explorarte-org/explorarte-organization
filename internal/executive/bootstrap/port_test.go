package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/engineeringmission"
	"github.com/Mireuz13/explorarte-organization/internal/executive"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

type refusingCreator struct{ err error }

func (c refusingCreator) CreateIn(context.Context, engineeringmission.MissionPolicy, string,
	engineeringmission.MissionOrigin, string, string) (tasks.Task, error) {
	return tasks.Task{}, c.err
}

// The port is where the two vocabularies meet, so it is where the question
// "could coming back later possibly help?" gets answered.
//
// A malformed request cannot be helped by waiting: the next attempt submits
// the identical policy and the identical plan. Returning it unclassified left
// the root executable and the worker resumed it about nine thousand six
// hundred times over eight hours, on a campaign whose design had already
// frozen.
func TestAMalformedMissionRequestIsClassifiedAsARefusal(t *testing.T) {
	engineRefusal := fmt.Errorf("%w: title must contain 1 to 240 bytes", tasks.ErrInvalidInput)
	provisioner := missionProvisioner{missions: refusingCreator{err: engineRefusal}, organizationID: "explorarte"}

	_, err := provisioner.ProvisionMission(context.Background(), executive.MissionProvisionCommand{})
	if err == nil {
		t.Fatal("a refused mission must surface as an error")
	}
	if !errors.Is(err, executive.ErrMissionRejected) {
		t.Fatalf("a malformed request must be classified as a refusal, got %v", err)
	}
	// The cause survives: "mission_rejected" alone does not tell an operator
	// what was wrong with it.
	if !errors.Is(err, tasks.ErrInvalidInput) {
		t.Fatal("classification must not swallow the reason")
	}
}

// An unavailable dependency is worth coming back for. Classifying it as a
// refusal would turn a database hiccup into a permanently blocked campaign,
// which is the opposite failure and no better.
func TestAnUnavailableDependencyStaysRetryable(t *testing.T) {
	for _, cause := range []error{
		errors.New("dial tcp 127.0.0.1:5432: connect: connection refused"),
		context.DeadlineExceeded,
	} {
		provisioner := missionProvisioner{missions: refusingCreator{err: cause}, organizationID: "explorarte"}
		_, err := provisioner.ProvisionMission(context.Background(), executive.MissionProvisionCommand{})
		if err == nil {
			t.Fatal("the failure must still surface")
		}
		if errors.Is(err, executive.ErrMissionRejected) {
			t.Fatalf("a transient failure must not be classified as a refusal: %v", cause)
		}
		if !errors.Is(err, cause) {
			t.Fatalf("the cause must survive: %v", err)
		}
	}
}
