package tasks

import "fmt"

var allowedTransitions = map[Status]map[Status]struct{}{
	StatusPending: {
		StatusReady: {}, StatusBlocked: {}, StatusRejected: {}, StatusCancelled: {},
	},
	StatusReady: {
		StatusLeased: {}, StatusBlocked: {}, StatusRejected: {}, StatusCancelled: {},
	},
	StatusLeased: {
		StatusRunning: {}, StatusRetryWait: {}, StatusBlocked: {}, StatusDeadLetter: {}, StatusCancelled: {},
	},
	StatusRunning: {
		StatusAwaitingVerification: {}, StatusRetryWait: {}, StatusBlocked: {}, StatusFailed: {}, StatusDeadLetter: {}, StatusCancelled: {},
	},
	StatusAwaitingVerification: {
		StatusCompleted: {}, StatusNoAction: {}, StatusFailed: {}, StatusBlocked: {}, StatusRejected: {}, StatusCancelled: {},
	},
	StatusBlocked: {
		StatusPending: {}, StatusReady: {}, StatusRejected: {}, StatusCancelled: {},
	},
	StatusRetryWait: {
		StatusReady: {}, StatusBlocked: {}, StatusDeadLetter: {}, StatusCancelled: {},
	},
}

func CanTransition(from, to Status) bool {
	if !from.Valid() || !to.Valid() || from.Terminal() || from == to {
		return false
	}
	_, ok := allowedTransitions[from][to]
	return ok
}

func ValidateTransition(from, to Status) error {
	if CanTransition(from, to) {
		return nil
	}
	return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, from, to)
}
