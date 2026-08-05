package postgres

import (
	"errors"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/authorization"
)

func TestTransitionUpdateErrorPreservesOperationalFailures(t *testing.T) {
	operational := errors.New("postgres read failure")
	if got := transitionUpdateError(operational); !errors.Is(got, operational) {
		t.Fatalf("operational error = %v, want original failure", got)
	}
	if got := transitionUpdateError(authorization.ErrRequestNotFound); !errors.Is(got, authorization.ErrInvalidTransition) {
		t.Fatalf("no-row error = %v, want invalid transition", got)
	}
}
