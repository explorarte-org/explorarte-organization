package fixtures

import (
	"context"
	"fmt"

	"github.com/Mireuz13/explorarte-organization/internal/decisiongraph"
)

// DecisionGraphScenario is the Scenario payload for RunnerKind
// "decisiongraph". It exercises internal/decisiongraph's pure, in-memory
// domain layer (Graph, BudgetLimits, DecisionRecord) directly — no
// Postgres dependency — which is exactly what R30's DAG/budget/evidence
// fixtures need to be reproducible without any external state.
type DecisionGraphScenario struct {
	// AcyclicNodes/AcyclicEdges must build a valid *decisiongraph.Graph.
	AcyclicNodes []decisiongraph.Node
	AcyclicEdges []decisiongraph.Edge
	// ExpectedMaxDepth is the longest goal-distance the fixture expects
	// Graph.Depths() to report for AcyclicNodes/AcyclicEdges.
	ExpectedMaxDepth int
	// CycleEdges, appended to AcyclicEdges, must make NewGraph fail with
	// ErrDependencyCycle. Nil skips this check.
	CycleEdges []decisiongraph.Edge

	// TerminalDecision, if set, is checked against the same
	// verified-or-inferred rule internal/decisiongraph's Service enforces
	// (service.go: "terminal selection requires verified or inferred
	// label") — independently re-derived here, not imported, because this
	// evaluation harness exists to catch exactly this class of regression
	// even if the library's own check were ever weakened.
	TerminalDecision           *decisiongraph.DecisionRecord
	TerminalDecisionShouldPass bool

	// Budget, if set, drives a sequence of BudgetLimits.Reserve calls
	// (BudgetDeltas, applied in order against a running BudgetUsage
	// starting at zero) and asserts the call at ExhaustingDeltaIndex (0
	// if unset, i.e. the first delta) is the first to fail with
	// ErrBudgetExceeded, and every prior delta succeeds cleanly.
	Budget               *decisiongraph.BudgetLimits
	BudgetDeltas         []decisiongraph.BudgetUsage
	ExhaustingDeltaIndex int
}

// DecisionGraphRunner implements Runner for RunnerKind "decisiongraph".
type DecisionGraphRunner struct{}

func (DecisionGraphRunner) Supports(f Fixture) bool {
	return f.RunnerKind == "decisiongraph" && f.Status == StatusRunnerReady
}

func (DecisionGraphRunner) Run(ctx context.Context, f Fixture, subjectID string) (RunOutcome, error) {
	if ctx.Err() != nil {
		return RunOutcome{}, ctx.Err()
	}
	scenario, ok := f.Scenario.(*DecisionGraphScenario)
	if !ok {
		return RunOutcome{}, fmt.Errorf("fixture %s: scenario is not a *DecisionGraphScenario", f.ID)
	}
	outcome := RunOutcome{
		FixtureID:        f.ID,
		SubjectID:        subjectID,
		InvariantResults: make(map[string]bool, len(f.HardInvariants)),
		Metrics:          make(map[string]float64),
		EvidenceRefs:     append([]string(nil), f.ExpectedEvidence...),
	}
	allPassed := true
	record := func(invariant string, passed bool) {
		outcome.InvariantResults[invariant] = passed
		if !passed {
			allPassed = false
			outcome.ViolatedInvariants = append(outcome.ViolatedInvariants, invariant)
		}
	}

	graph, err := decisiongraph.NewGraph(scenario.AcyclicNodes, scenario.AcyclicEdges)
	if err != nil {
		record("graph_must_build_from_acyclic_nodes_and_edges", false)
		outcome.Notes = fmt.Sprintf("NewGraph on acyclic fixture data failed: %v", err)
		outcome.Passed = false
		return outcome, nil
	}
	record("graph_must_build_from_acyclic_nodes_and_edges", true)

	if _, maxDepth, depthErr := graph.Depths(); depthErr != nil || maxDepth != scenario.ExpectedMaxDepth {
		record("max_depth_matches_fixture_expectation", false)
		outcome.Notes = fmt.Sprintf("depth mismatch: got=%d want=%d err=%v", maxDepth, scenario.ExpectedMaxDepth, depthErr)
	} else {
		outcome.Metrics["max_depth"] = float64(maxDepth)
		record("max_depth_matches_fixture_expectation", true)
	}

	if scenario.CycleEdges != nil {
		combined := append(append([]decisiongraph.Edge(nil), scenario.AcyclicEdges...), scenario.CycleEdges...)
		_, cycleErr := decisiongraph.NewGraph(scenario.AcyclicNodes, combined)
		record("dependency_cycle_is_rejected", cycleErr == decisiongraph.ErrDependencyCycle)
	}

	if scenario.TerminalDecision != nil {
		structurallyValid := scenario.TerminalDecision.Validate(graph) == nil
		labelIsRealEvidence := scenario.TerminalDecision.Label == decisiongraph.VerificationVerified || scenario.TerminalDecision.Label == decisiongraph.VerificationInferred
		terminalOK := structurallyValid && labelIsRealEvidence
		record("terminal_decision_never_closes_without_real_evidence", terminalOK == scenario.TerminalDecisionShouldPass)
	}

	if scenario.Budget != nil {
		var current decisiongraph.BudgetUsage
		exhaustedAtExpectedIndex := true
		for i, delta := range scenario.BudgetDeltas {
			next, reserveErr := scenario.Budget.Reserve(current, delta)
			if i < scenario.ExhaustingDeltaIndex {
				if reserveErr != nil {
					exhaustedAtExpectedIndex = false
					break
				}
				current = next
				continue
			}
			if i == scenario.ExhaustingDeltaIndex {
				exhaustedAtExpectedIndex = reserveErr != nil
				break
			}
		}
		record("budget_exhausts_at_the_expected_reservation_and_not_before", exhaustedAtExpectedIndex)
	}

	outcome.Passed = allPassed
	return outcome, nil
}
