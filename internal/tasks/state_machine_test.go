package tasks

import (
	"errors"
	"testing"
)

func TestStatusTerminalClassification(t *testing.T) {
	tests := []struct {
		status   Status
		terminal bool
	}{
		{StatusPending, false}, {StatusReady, false}, {StatusLeased, false}, {StatusRunning, false},
		{StatusAwaitingVerification, false}, {StatusBlocked, false}, {StatusRetryWait, false},
		{StatusCompleted, true}, {StatusNoAction, true}, {StatusFailed, true}, {StatusDeadLetter, true},
		{StatusRejected, true}, {StatusCancelled, true},
	}
	for _, test := range tests {
		t.Run(string(test.status), func(t *testing.T) {
			if got := test.status.Terminal(); got != test.terminal {
				t.Fatalf("Terminal() = %v, want %v", got, test.terminal)
			}
			if !test.status.Valid() {
				t.Fatal("known status reported invalid")
			}
		})
	}
}

func TestAllowedTransitions(t *testing.T) {
	tests := []struct{ from, to Status }{
		{StatusPending, StatusReady}, {StatusPending, StatusBlocked},
		{StatusReady, StatusLeased}, {StatusReady, StatusCancelled},
		{StatusLeased, StatusRunning}, {StatusLeased, StatusRetryWait},
		{StatusRunning, StatusAwaitingVerification}, {StatusRunning, StatusRetryWait},
		{StatusRunning, StatusFailed}, {StatusRunning, StatusBlocked},
		{StatusAwaitingVerification, StatusCompleted}, {StatusAwaitingVerification, StatusNoAction},
		{StatusBlocked, StatusPending}, {StatusBlocked, StatusReady},
		{StatusRetryWait, StatusReady}, {StatusRetryWait, StatusDeadLetter},
	}
	for _, test := range tests {
		t.Run(string(test.from)+"_to_"+string(test.to), func(t *testing.T) {
			if !CanTransition(test.from, test.to) {
				t.Fatalf("expected transition %s -> %s", test.from, test.to)
			}
			if err := ValidateTransition(test.from, test.to); err != nil {
				t.Fatalf("ValidateTransition() error = %v", err)
			}
		})
	}
}

func TestForbiddenDirectCompletionTransitions(t *testing.T) {
	for _, from := range []Status{StatusPending, StatusReady, StatusLeased, StatusRunning, StatusRetryWait, StatusBlocked} {
		t.Run(string(from), func(t *testing.T) {
			if CanTransition(from, StatusCompleted) {
				t.Fatalf("direct %s -> completed must be forbidden", from)
			}
			if err := ValidateTransition(from, StatusCompleted); !errors.Is(err, ErrInvalidTransition) {
				t.Fatalf("error = %v, want ErrInvalidTransition", err)
			}
		})
	}
}

func TestTerminalStatesCannotReopen(t *testing.T) {
	for _, from := range []Status{StatusCompleted, StatusNoAction, StatusFailed, StatusDeadLetter, StatusRejected, StatusCancelled} {
		t.Run(string(from), func(t *testing.T) {
			for _, to := range []Status{StatusPending, StatusReady, StatusRunning, StatusAwaitingVerification} {
				if CanTransition(from, to) {
					t.Fatalf("terminal transition %s -> %s must be forbidden", from, to)
				}
			}
		})
	}
}
