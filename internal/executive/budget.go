package executive

import "fmt"

type InvocationBudget struct {
	CEOCalls       int `json:"ceo_calls"`
	LeaderCalls    int `json:"leader_calls"`
	WorkerAttempts int `json:"worker_attempts"`
	Replans        int `json:"replans"`
}

func (b InvocationBudget) Total() int { return b.CEOCalls + b.LeaderCalls + b.WorkerAttempts }
func NormalExpectedCalls(departments, attempts int) int {
	if departments < 0 || attempts < 0 {
		return 0
	}
	return 2 + 2*departments + attempts
}
func (b InvocationBudget) Validate(l Limits, departments int) error {
	if b.CEOCalls > 2 {
		return fmt.Errorf("%w: CEO calls %d > 2", ErrBudgetExceeded, b.CEOCalls)
	}
	if b.LeaderCalls > 2*departments+2*l.MaxDepartmentReplans {
		return fmt.Errorf("%w: leader calls", ErrBudgetExceeded)
	}
	if b.Replans > departments*l.MaxDepartmentReplans {
		return fmt.Errorf("%w: replans", ErrBudgetExceeded)
	}
	if b.Total() > l.MaxModelCalls {
		return ErrBudgetExceeded
	}
	return nil
}
