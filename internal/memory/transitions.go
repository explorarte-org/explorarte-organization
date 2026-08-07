package memory

import "fmt"

var allowedTransitions = map[Status]map[Status]struct{}{
	StatusCandidate: {
		StatusApproved: {},
		StatusRejected: {},
	},
	StatusApproved: {
		StatusDeprecated: {},
	},
	StatusDeprecated: {
		StatusArchived: {},
	},
	StatusRejected: {
		StatusArchived: {},
	},
	StatusArchived: {},
}

func ValidateTransition(from, to Status) error {
	if !from.Valid() || !to.Valid() {
		return fmt.Errorf("%w: %q -> %q", ErrInvalidTransition, from, to)
	}
	if _, ok := allowedTransitions[from][to]; !ok {
		return fmt.Errorf("%w: %q -> %q", ErrInvalidTransition, from, to)
	}
	return nil
}
