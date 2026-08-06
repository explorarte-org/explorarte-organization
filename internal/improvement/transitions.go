package improvement

import "fmt"

// candidateTransitions is the default-deny allow-list for the promotion
// state machine. Any (from, to) pair not present here is rejected,
// including proposed -> active, which must never be reachable directly.
var candidateTransitions = map[CandidateState]map[CandidateState]struct{}{
	StateProposed: {
		StateValidated: {},
	},
	StateValidated: {
		StateEvaluating: {},
	},
	StateEvaluating: {
		StateApproved:     {},
		StateRejected:     {},
		StateInconclusive: {},
	},
	StateApproved: {
		StateCanary: {},
	},
	StateCanary: {
		StateActive:     {},
		StateRolledBack: {},
	},
	StateActive: {
		StateDeprecated: {},
		StateRolledBack: {},
	},
}

// ValidateCandidateTransition enforces the default-deny candidate promotion
// state machine.
func ValidateCandidateTransition(from, to CandidateState) error {
	if !from.Valid() || !to.Valid() || from == to {
		return fmt.Errorf("%w: candidate %q -> %q", ErrInvalidTransition, from, to)
	}
	if _, ok := candidateTransitions[from][to]; !ok {
		return fmt.Errorf("%w: candidate %q -> %q", ErrInvalidTransition, from, to)
	}
	return nil
}
