// Package modelrouting is the pure candidate-selection layer for Dynamic
// Canonical Model Routing.
//
// It is deliberately neutral: no HTTP, no credentials, no adapter
// construction, no execution of any kind. A Selector's job ends at
// returning WHICH already-canonical candidate should be tried next --
// never at trying it. internal/modelruntime (the kernel) imports this
// package to implement the pool half of RouteResolver; this package
// imports neither internal/modelruntime nor internal/search, so it cannot
// create the import cycle those two packages are kept apart to avoid.
//
// A Selector receives only candidates that RouteResolver already loaded
// from the canonical, materialized routing_candidates table for one
// specific policy -- never an arbitrary list from a caller, an LLM
// response, or request JSON.
package modelrouting

import (
	"errors"
	"sort"
	"strconv"
	"time"
)

// ErrNoCapacity is returned when no candidate is currently eligible. It
// is a capacity signal, not a configuration error: the pool exists and
// is well-formed, every member is just unavailable right now.
var ErrNoCapacity = errors.New("modelrouting: no eligible candidate")

// Candidate is one already-canonical, already-materialized pool member.
// ProfileID/ModelProfileVersionID identify the specific, real, FK-checked
// route this candidate resolves to if selected -- a Selector never
// invents or receives a candidate without them.
type Candidate struct {
	ProviderID            string
	ProviderModelID       string
	Transport             string
	CapacityClass         string
	Priority              int
	ProfileID             string
	ModelProfileVersionID int64
}

func (c Candidate) key() string { return c.ProviderID + "|" + c.ProviderModelID }

// CandidateState is the mutable capacity picture of one candidate,
// supplied by the caller (RouteResolver's capacity-state port) -- this
// package holds none of its own state and mutates nothing; Select is a
// pure function of its arguments.
type CandidateState struct {
	Disabled       bool
	QuotaExhausted bool
	CooldownUntil  *time.Time
}

func (s CandidateState) inCooldown(now time.Time) bool {
	return s.CooldownUntil != nil && now.Before(*s.CooldownUntil)
}

// Requirements narrows what a Selector may choose. AllowPaid=false is
// fail-closed: a "paid" capacity_class candidate is never eligible unless
// this is explicitly true, independent of every other state.
type Requirements struct {
	AllowPaid bool
}

// Decision names the chosen candidate and why, for provenance (Section 10).
type Decision struct {
	Candidate Candidate
	Reason    string
}

// Selector picks one eligible candidate from an already-authorized,
// already-materialized set. It never sees anything the caller did not
// already load from the canonical registry.
type Selector interface {
	Select(candidates []Candidate, state map[string]CandidateState, req Requirements, now time.Time) (Decision, error)
}

// classRank orders consumption preference: consume included/free capacity
// before finite credit, and credit before paid budget. Mirrors
// internal/search's CapacityClass.classRank() ranking exactly -- this
// package does not import internal/search, so the ranking is restated
// here as plain string comparison rather than shared as a type.
func classRank(class string) int {
	switch class {
	case "free_daily":
		return 0
	case "free_model":
		return 1
	case "credit_monthly":
		return 2
	case "paid":
		return 3
	}
	return 99
}

// FreeCapacityV1 is the selector named "free_capacity_v1" in
// docs/canonical/model-routing.yaml (validRoutingSelectors). Deterministic:
// capacity class rank, then declared priority, then provider|model as a
// final stable tie-break. No randomness, no LLM, no wall-clock reads
// beyond the caller-supplied now.
type FreeCapacityV1 struct{}

func (FreeCapacityV1) Select(candidates []Candidate, state map[string]CandidateState, req Requirements, now time.Time) (Decision, error) {
	type ranked struct {
		candidate Candidate
		classRank int
	}
	options := make([]ranked, 0, len(candidates))
	for _, c := range candidates {
		if c.CapacityClass == "paid" && !req.AllowPaid {
			continue
		}
		st := state[c.key()]
		if st.Disabled {
			continue
		}
		if st.inCooldown(now) {
			continue
		}
		if st.QuotaExhausted && (st.inCooldown(now) || st.CooldownUntil == nil) {
			continue
		}
		options = append(options, ranked{candidate: c, classRank: classRank(c.CapacityClass)})
	}
	if len(options) == 0 {
		return Decision{}, ErrNoCapacity
	}
	sort.Slice(options, func(i, j int) bool {
		if options[i].classRank != options[j].classRank {
			return options[i].classRank < options[j].classRank
		}
		if options[i].candidate.Priority != options[j].candidate.Priority {
			return options[i].candidate.Priority < options[j].candidate.Priority
		}
		return options[i].candidate.key() < options[j].candidate.key()
	})
	chosen := options[0].candidate
	return Decision{
		Candidate: chosen,
		Reason:    "free_capacity_v1: class_rank=" + strconv.Itoa(options[0].classRank) + " priority=" + strconv.Itoa(chosen.Priority),
	}, nil
}

// selectors is the closed registry of known selector implementations,
// keyed by the selector_id docs/canonical/model-routing.yaml validates
// against (validRoutingSelectors in internal/modelruntime). Kept here,
// next to the implementations, so the two lists cannot drift silently --
// a new Selector and its id are added in the same place.
//
// Deliberately unexported: what selector_id=free_capacity_v1 MEANS is
// part of the canonical routing identity (it feeds
// RoutingPolicy.CanonicalHash and, once resolved, the persisted
// routing_selector_id provenance column) -- it must not be repointable
// in a running process, including by a test. LookupSelector is the only
// way to read it.
var selectors = map[string]Selector{
	"free_capacity_v1": FreeCapacityV1{},
}

// LookupSelector returns the registered Selector for id and whether one
// exists. An unregistered id is a materialization bug (docs/canonical/
// model-routing.yaml validation should have already rejected it) and must
// fail closed at the caller -- LookupSelector never invents a fallback.
func LookupSelector(id string) (Selector, bool) {
	s, ok := selectors[id]
	return s, ok
}
