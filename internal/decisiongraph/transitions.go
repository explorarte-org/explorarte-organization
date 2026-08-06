package decisiongraph

import "fmt"

func ValidateBranchTransition(from, to BranchState, reopenedByNewEvidence bool) error {
	if !from.Valid() || !to.Valid() || from == to {
		return fmt.Errorf("%w: branch %q -> %q", ErrInvalidTransition, from, to)
	}
	if from == BranchActive {
		switch to {
		case BranchSelected, BranchRejectedByEvidence, BranchRejectedByPolicy,
			BranchRejectedByCapability, BranchRejectedByDependency, BranchRejectedByBudget,
			BranchSuperseded, BranchInconclusive:
			return nil
		}
	}
	if reopenedByNewEvidence {
		switch from {
		case BranchRejectedByEvidence, BranchRejectedByPolicy, BranchRejectedByCapability,
			BranchRejectedByDependency, BranchRejectedByBudget, BranchInconclusive:
			if to == BranchActive {
				return nil
			}
		}
	}
	return fmt.Errorf("%w: branch %q -> %q", ErrInvalidTransition, from, to)
}

func ValidateExecutionTransition(from, to ExecutionState) error {
	if !from.Valid() || !to.Valid() || from == to {
		return fmt.Errorf("%w: execution %q -> %q", ErrInvalidTransition, from, to)
	}
	allowed := map[ExecutionState]map[ExecutionState]struct{}{
		ExecutionPending: {
			ExecutionReady: {}, ExecutionCancelled: {},
		},
		ExecutionReady: {
			ExecutionRunning: {}, ExecutionCancelled: {},
		},
		ExecutionRunning: {
			ExecutionWaitingVerification: {}, ExecutionSucceeded: {}, ExecutionFailed: {},
			ExecutionCancelled: {}, ExecutionAmbiguous: {},
		},
		ExecutionWaitingVerification: {
			ExecutionSucceeded: {}, ExecutionFailed: {}, ExecutionCancelled: {}, ExecutionAmbiguous: {},
		},
	}
	if _, ok := allowed[from][to]; !ok {
		return fmt.Errorf("%w: execution %q -> %q", ErrInvalidTransition, from, to)
	}
	return nil
}
