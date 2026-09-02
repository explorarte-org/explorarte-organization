package executive

import "fmt"

type InvocationBudget struct {
	CEOCalls       int `json:"ceo_calls"`
	LeaderCalls    int `json:"leader_calls"`
	WorkerAttempts int `json:"worker_attempts"`
	Replans        int `json:"replans"`
}

func (b InvocationBudget) Total() int { return b.CEOCalls + b.LeaderCalls + b.WorkerAttempts }

// NormalExpectedCalls is the happy path with no retries and no replans: the
// CEO's phases, two calls per department (plan and review), and the worker
// attempts the plan asked for.
//
// designFreeze must reflect whether THIS scenario is actually governed by
// a design freeze (plan, adjudicate, close -- ceoPhases) or not (plan,
// close -- ceoPhases-1). ORG_STAGING_ENABLED=false means the freeze path
// never runs in production today, so most real campaigns are the
// two-phase case; ceoPhases itself was fixed at 3 for a DIFFERENT
// incident (root 294) that DID need adjudication, then applied here
// unconditionally to every caller -- silently over-counting by one CEO
// call for every campaign that never adjudicates anything, which is
// exactly what TestExecutivePostgreSQL17EndToEndAndRestart's scenario
// ("return a one-area plan without external actions", no design freeze)
// is.
func NormalExpectedCalls(departments, attempts int, designFreeze bool) int {
	if departments < 0 || attempts < 0 {
		return 0
	}
	phases := ceoPhases
	if !designFreeze {
		phases--
	}
	return phases + 2*departments + attempts
}

// The CEO is asked to do three things in one governed campaign: plan the run,
// adjudicate the frozen design, and close the run.
//
// The ceiling was 2, written when a campaign was plan-then-close. Design
// adjudication was added later and the budget was never revisited, so any run
// governed by a design freeze exceeded its CEO budget on the happy path --
// before a single retry. AUTONOMY-SMOKE-001's root 294 died exactly there,
// with the adversarial review already complete and the adjudication
// already under way.
const ceoPhases = 3

// governedTaskAttempts is what every governed task is created with, and
// therefore what the task engine may legitimately spend before giving up.
//
// The budget counts INVOCATIONS but was sized in PHASES, which silently
// assumed no phase is ever retried. That assumption held only while every
// model failure was terminal; once a transient provider failure could send a
// task back for another attempt, a single retry overran a ceiling that had no
// room for one. A guard that forbids the retries the engine is designed to
// perform is not protecting anything -- it is failing the run on its own
// recovery.
const governedTaskAttempts = 3

func (b InvocationBudget) Validate(l Limits, departments int) error {
	// Both ceilings are runaway guards, not accounting: MaxModelCalls below
	// and the durable agent budget are what actually bound spend. These bound
	// SHAPE -- a campaign making far more calls of one kind than its phases
	// can explain is looping, whatever it costs.
	if maxCEO := ceoPhases * governedTaskAttempts; b.CEOCalls > maxCEO {
		return fmt.Errorf("%w: CEO calls %d > %d", ErrBudgetExceeded, b.CEOCalls, maxCEO)
	}
	if maxLeader := (2*departments + 2*l.MaxDepartmentReplans) * governedTaskAttempts; b.LeaderCalls > maxLeader {
		return fmt.Errorf("%w: leader calls %d > %d", ErrBudgetExceeded, b.LeaderCalls, maxLeader)
	}
	if b.Replans > departments*l.MaxDepartmentReplans {
		return fmt.Errorf("%w: replans", ErrBudgetExceeded)
	}
	if b.Total() > l.MaxModelCalls {
		return ErrBudgetExceeded
	}
	return nil
}
