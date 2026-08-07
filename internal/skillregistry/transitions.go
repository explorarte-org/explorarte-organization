package skillregistry

import "fmt"

var lifecycleTransitions = map[Lifecycle]map[Lifecycle]struct{}{
	LifecycleDraft: {
		LifecycleHumanApproved: {},
		LifecycleRetired:       {},
	},
	LifecycleHumanApproved: {
		LifecycleCandidate: {},
		LifecycleRetired:   {},
	},
	LifecycleCandidate: {
		LifecycleActive:  {},
		LifecycleRetired: {},
	},
	LifecycleActive: {
		LifecycleSuspended: {},
		LifecycleRetired:   {},
	},
	LifecycleSuspended: {
		LifecycleActive:  {},
		LifecycleRetired: {},
	},
	LifecycleRetired: {},
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
