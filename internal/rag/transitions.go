package rag

import "fmt"

var lifecycleTransitions = map[Lifecycle]map[Lifecycle]struct{}{
	LifecycleCandidate: {
		LifecycleApproved: {},
		LifecycleRejected: {},
	},
	LifecycleApproved: {
		LifecycleDeprecated: {},
	},
	LifecycleDeprecated: {
		LifecycleArchived: {},
	},
	LifecycleRejected: {
		LifecycleArchived: {},
	},
	LifecycleArchived: {},
}

func ValidateTransition(from, to Lifecycle) error {
	if !from.Valid() || !to.Valid() {
		return fmt.Errorf("%w: %q -> %q", ErrInvalidTransition, from, to)
	}
	if _, ok := lifecycleTransitions[from][to]; !ok {
		return fmt.Errorf("%w: %q -> %q", ErrInvalidTransition, from, to)
	}
	return nil
}
