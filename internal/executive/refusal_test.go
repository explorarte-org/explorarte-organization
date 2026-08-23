package executive

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

// A refusal is not a retry.
//
// The task engine refused a mission because the request was malformed -- the
// title did not fit. That error came back as an ordinary one, so the root
// stayed executable and the worker resumed it about nine thousand six hundred
// times over eight hours, silently. The campaign's design had already frozen;
// it looked idle and was in fact failing continuously.
//
// What makes it a refusal is that the next attempt submits the identical
// policy and the identical plan. There is nothing for it to come back to.
func TestARejectedMissionBlocksTheRootInsteadOfBeingRetried(t *testing.T) {
	engineRefusal := fmt.Errorf("%w: title must contain 1 to 240 bytes", tasks.ErrInvalidInput)
	classified := fmt.Errorf("%w: %w", ErrMissionRejected, engineRefusal)

	if !errors.Is(classified, ErrMissionRejected) {
		t.Fatal("the port must mark a malformed request as a refusal")
	}
	// The underlying cause survives, because an operator reading
	// "mission_rejected" still has to learn what was wrong with it.
	if !errors.Is(classified, tasks.ErrInvalidInput) {
		t.Fatal("classification must not swallow the reason")
	}
	if !strings.Contains(classified.Error(), "title must contain") {
		t.Fatalf("the refusal must carry what the engine said: %q", classified.Error())
	}
}

// An unavailable dependency is worth coming back for. Only a malformed
// request is not, and conflating them would turn a database hiccup into a
// permanently blocked campaign.
func TestAnOrdinaryFailureIsNotARefusal(t *testing.T) {
	for _, err := range []error{
		errors.New("dial tcp: connection refused"),
		fmt.Errorf("provision engineering mission: %w", errors.New("context deadline exceeded")),
	} {
		if errors.Is(err, ErrMissionRejected) {
			t.Fatalf("a transient failure must stay retryable: %v", err)
		}
	}
}

// Everything the worker skips must be something that clears on its own. The
// case it got wrong was neither: a deterministic refusal, not on this list
// and not blocked either, so it fell through to a branch that did nothing and
// said nothing.
func TestOnlySelfResolvingFailuresAreSkipped(t *testing.T) {
	for _, err := range []error{
		ErrDispatchAssignmentRequired, ErrModelOutcomeAmbiguous,
		ErrIndeterminateToolExecution, ErrCompletionInconclusive,
		ErrRunBlocked, ErrLeaseLost, ErrExecutionAuthorityUnavailable,
		ErrExecutionPrincipalUnavailable, ErrPriorExecutionUnresolved,
		ErrExecutionInterrupted,
	} {
		if !resolvesItself(fmt.Errorf("wrapped: %w", err)) {
			t.Errorf("%v must be recognised through a wrap", err)
		}
	}
	for _, err := range []error{
		fmt.Errorf("%w: title must contain 1 to 240 bytes", ErrMissionRejected),
		fmt.Errorf("%w: %w", ErrMissionRejected, tasks.ErrInvalidInput),
		errors.New("dial tcp: connection refused"),
		errors.New("provision engineering mission: something new nobody has classified"),
	} {
		if resolvesItself(err) {
			t.Errorf("%v does not clear on its own and must not be skipped silently", err)
		}
	}
}

// The option must actually install the observer: an observer nothing calls is
// the same silence with extra code.
func TestTheFailureObserverIsInstalled(t *testing.T) {
	called := false
	worker := &Worker{}
	WithFailureObserver(func(int64, error) { called = true })(worker)
	if worker.observe == nil {
		t.Fatal("the option must install the observer")
	}
	worker.observe(1, errors.New("x"))
	if !called {
		t.Fatal("the installed observer must be the one provided")
	}
	// A nil observer must not replace a real one.
	WithFailureObserver(nil)(worker)
	if worker.observe == nil {
		t.Fatal("a nil observer must not clear an installed one")
	}
}
