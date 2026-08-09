package decisiongraphfixtures

import (
	"context"
	"fmt"

	"github.com/Mireuz13/explorarte-organization/internal/decisiongraph"
	"github.com/Mireuz13/explorarte-organization/internal/evaluation/fixtures"
)

// DecisionGraphRunner implements fixtures.Runner for RunnerKind
// "decisiongraph", against fixtures activated by Activate in this package
// (see activate.go) — internal/evaluation/fixtures itself never imports
// internal/decisiongraph.
type DecisionGraphRunner struct{}

var _ fixtures.Runner = DecisionGraphRunner{}

func (DecisionGraphRunner) Supports(f fixtures.Fixture) bool {
	return f.RunnerKind == "decisiongraph" && f.Status == fixtures.StatusRunnerReady
}

func (DecisionGraphRunner) Run(ctx context.Context, f fixtures.Fixture, subjectID string) (fixtures.RunOutcome, error) {
	if ctx.Err() != nil {
		return fixtures.RunOutcome{}, ctx.Err()
	}
	scenario, ok := f.Scenario.(*DecisionGraphScenario)
	if !ok {
		return fixtures.RunOutcome{}, fmt.Errorf("fixture %s: scenario is not a *DecisionGraphScenario (was it activated via decisiongraphfixtures.Activate?)", f.ID)
	}
	outcome := fixtures.RunOutcome{
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
