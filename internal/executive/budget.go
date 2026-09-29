package executive

import "fmt"

type InvocationBudget struct {
	CEOCalls       int `json:"ceo_calls"`
	LeaderCalls    int `json:"leader_calls"`
	WorkerAttempts int `json:"worker_attempts"`
	Replans        int `json:"replans"`
	// DesignRounds is the highest design round the run has planned (1 when it
	// has none). The CEO adjudicates once per round, and every round's
	// department plans, reviews and replans afresh, so the shape bounds below
	// scale with it.
	DesignRounds int `json:"design_rounds,omitempty"`
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
// DesignRoundOf is the design round a task key belongs to (1 when it names none).
func DesignRoundOf(key string) int { return designRoundOf(key) }

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
	//
	// They were sized for one design round. Local smoke #34 (root 1773,
	// 2026-09-27) replanned once in round 1 and once in round 2 -- each within
	// the orchestrator's per-round MaxDepartmentReplans, since a review key
	// carries its round -- and was blocked here with "replans" because the
	// bound counted both against one.
	rounds := max(1, b.DesignRounds)
	if maxCEO := (ceoPhases + rounds - 1) * governedTaskAttempts; b.CEOCalls > maxCEO {
		return fmt.Errorf("%w: CEO calls %d > %d", ErrBudgetExceeded, b.CEOCalls, maxCEO)
	}
	if maxLeader := (2*departments + 2*l.MaxDepartmentReplans) * rounds * governedTaskAttempts; b.LeaderCalls > maxLeader {
		return fmt.Errorf("%w: leader calls %d > %d", ErrBudgetExceeded, b.LeaderCalls, maxLeader)
	}
	if maxReplans := departments * l.MaxDepartmentReplans * rounds; b.Replans > maxReplans {
		return fmt.Errorf("%w: replans %d > %d", ErrBudgetExceeded, b.Replans, maxReplans)
	}
	if b.Total() > l.MaxModelCalls {
		return ErrBudgetExceeded
	}
	return nil
}
