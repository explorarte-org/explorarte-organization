package authorization

import "testing"

func TestAuthorizationStateMachine(t *testing.T) {
	allowed := [][2]RequestStatus{{RequestPending, RequestApproved}, {RequestPending, RequestRejected}, {RequestPending, RequestCancelled}, {RequestPending, RequestExpired}, {RequestApproved, RequestConsumed}, {RequestApproved, RequestCancelled}, {RequestApproved, RequestExpired}}
	for _, transition := range allowed {
		if !CanTransition(transition[0], transition[1]) {
			t.Fatalf("expected %s -> %s", transition[0], transition[1])
		}
	}
	for _, transition := range [][2]RequestStatus{{RequestRejected, RequestApproved}, {RequestExpired, RequestApproved}, {RequestCancelled, RequestApproved}, {RequestConsumed, RequestApproved}, {RequestConsumed, RequestConsumed}} {
		if CanTransition(transition[0], transition[1]) {
			t.Fatalf("unexpected %s -> %s", transition[0], transition[1])
		}
	}
}
