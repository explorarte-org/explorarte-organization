package runtimeadapter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/Mireuz13/explorarte-organization/internal/contextengine/canonical"
	"github.com/Mireuz13/explorarte-organization/internal/decisiongraph"
	"github.com/Mireuz13/explorarte-organization/internal/executive"
)

// reasoningAssuranceLogicalName is the canonical source contextengine already
// loads and hashes for docs/canonical/reasoning-assurance.yaml. Reused here
// instead of hashing the policy file a second time. canonical.Provider.Load
// prefixes every LogicalName with "docs/canonical/".
const reasoningAssuranceLogicalName = "docs/canonical/reasoning-assurance.yaml"

// Provisional decision-graph budget ceilings for fields decisiongraph
// requires that internal/executive has no analogous policy for yet. These
// constants are owned by this adapter, not a canonical policy — nothing
// today justifies promoting them to one.
const (
	decisionGraphMaxNodes         = 32
	decisionGraphMaxDepth         = 8
	decisionGraphMaxParallelNodes = 4
	decisionGraphMaxInputTokens   = 200000
	decisionGraphMaxVerifications = 4
)

const (
	decisionGoalNodeID      int64 = 1
	decisionCandidateNodeID int64 = 2
	decisionDecisionNodeID  int64 = 3
)

// DecisionGraph records the completion verdict for one finished task attempt
// as a durable decisiongraph.Run: one goal node (the task), one
// candidate_action node (the attempt's result), and one decision node (the
// completion verdict). It deliberately does not model a richer reasoning
// decomposition — the orchestrator doesn't produce hypotheses or multiple
// candidates today, so recording one wouldn't be honest evidence.
type DecisionGraph struct {
	Service   *decisiongraph.Service
	Canonical *canonical.Provider
	Limits    executive.Limits
	Clock     executive.Clock
}

func (a DecisionGraph) RecordAttemptDecision(ctx context.Context, record executive.AttemptDecisionRecord) error {
	if a.Service == nil || a.Canonical == nil {
		return errors.New("decision graph adapter is not configured")
	}
	if record.TaskID <= 0 || record.AttemptID <= 0 {
		return fmt.Errorf("record attempt decision: invalid task or attempt id")
	}

	policyVersion, policyHash, err := a.reasoningPolicy(ctx)
	if err != nil {
		return fmt.Errorf("resolve reasoning policy: %w", err)
	}

	now := a.Clock.Now()
	run, err := a.Service.CreateRun(ctx, decisiongraph.CreateRunRequest{
		TaskID:                       record.TaskID,
		AttemptID:                    record.AttemptID,
		ReasoningPolicySchemaVersion: policyVersion,
		ReasoningPolicyHash:          policyHash,
		IdempotencyKey:               fmt.Sprintf("task:%d:attempt:%d", record.TaskID, record.AttemptID),
		BudgetLimits: decisiongraph.BudgetLimits{
			MaxNodes:         decisionGraphMaxNodes,
			MaxDepth:         decisionGraphMaxDepth,
			MaxParallelNodes: decisionGraphMaxParallelNodes,
			MaxModelCalls:    int64(a.Limits.MaxModelCalls),
			MaxInputTokens:   decisionGraphMaxInputTokens,
			MaxOutputTokens:  int64(a.Limits.MaxOutputTokens),
			MaxReplans:       int64(a.Limits.MaxDepartmentReplans),
			MaxVerifications: decisionGraphMaxVerifications,
			MaxWallTime:      a.Limits.InvocationDeadline,
		},
		Deadline:  now.Add(a.Limits.InvocationDeadline),
		CreatedBy: serviceActor,
	})
	if err != nil {
		return fmt.Errorf("create decision graph run: %w", err)
	}

	detailHash := digestString(fmt.Sprintf("task:%d:attempt:%d:verdict:%s:detail:%s", record.TaskID, record.AttemptID, record.Verdict, record.Detail))
	graphVersion, err := a.Service.AppendGraph(ctx, decisiongraph.AppendGraphRequest{
		RunID: run.ID,
		Nodes: []decisiongraph.Node{
			{
				ID: decisionGoalNodeID, Type: decisiongraph.NodeGoal,
				BranchState: decisiongraph.BranchActive, ExecutionState: decisiongraph.ExecutionSucceeded,
				PayloadSchemaVersion: "1", PayloadHash: digestString(fmt.Sprintf("task:%d", record.TaskID)),
				CreatedBy: serviceActor,
			},
			{
				ID: decisionCandidateNodeID, Type: decisiongraph.NodeCandidateAction,
				BranchState: verdictBranchState(record.Verdict), ExecutionState: decisiongraph.ExecutionSucceeded,
				PayloadSchemaVersion: "1", PayloadHash: detailHash,
				CreatedBy: serviceActor,
			},
			{
				ID: decisionDecisionNodeID, Type: decisiongraph.NodeDecision,
				BranchState: decisiongraph.BranchActive, ExecutionState: decisiongraph.ExecutionSucceeded,
				PayloadSchemaVersion: "1", PayloadHash: detailHash,
				CreatedBy: serviceActor,
			},
		},
		Edges: []decisiongraph.Edge{
			{FromNodeID: decisionCandidateNodeID, ToNodeID: decisionGoalNodeID, Type: decisiongraph.EdgeSatisfies},
			{FromNodeID: decisionDecisionNodeID, ToNodeID: decisionCandidateNodeID, Type: decisiongraph.EdgeDependsOn},
		},
		Depths:    map[int64]int{decisionGoalNodeID: 0, decisionCandidateNodeID: 1, decisionDecisionNodeID: 2},
		CreatedBy: serviceActor,
	})
	if err != nil {
		return fmt.Errorf("append decision graph: %w", err)
	}
	// AppendGraph assigns its own durable node ids distinct from the logical
	// ids above (decision_graph_nodes.id is a run-independent global
	// identity column, not scoped per run) — every later call must address
	// nodes by the ids GraphVersion.NodeIDs maps back to.
	candidateNodeID := graphVersion.NodeIDs[decisionCandidateNodeID]
	decisionNodeID := graphVersion.NodeIDs[decisionDecisionNodeID]

	if err := a.Service.StartRun(ctx, run.ID); err != nil {
		return fmt.Errorf("start decision graph run: %w", err)
	}

	label := verificationLabel(record.Verdict)
	if err := a.Service.RecordVerification(ctx, decisiongraph.VerificationRecord{
		RunID: run.ID, NodeID: candidateNodeID, Label: label,
		VerifierRef: "internal/completion", VerifierVersion: "phase2",
		EvidenceSetHash: detailHash, ReasonCodes: []string{},
	}); err != nil {
		return fmt.Errorf("record decision verification: %w", err)
	}

	if label != decisiongraph.VerificationVerified && label != decisiongraph.VerificationInferred {
		// A fail/inconclusive completion verdict has no terminal *selection*
		// to record — decisiongraph.Service.RecordTerminalDecision requires a
		// verified or inferred label. The verification above already made the
		// outcome durable and queryable.
		return nil
	}

	if err := a.Service.RecordTerminalDecision(ctx, decisiongraph.TerminalDecisionRequest{
		RunID: run.ID, DecisionNodeID: decisionNodeID, SelectedCandidateNodeID: candidateNodeID,
		EvidenceSetHash: detailHash, VerificationSetHash: detailHash, DecisionHash: detailHash,
		VerificationLabel: label, CreatedBy: serviceActor,
	}); err != nil {
		return fmt.Errorf("record terminal decision: %w", err)
	}
	return nil
}

// reasoningPolicy resolves the schema version and content hash of
// docs/canonical/reasoning-assurance.yaml from contextengine's canonical
// bundle — the same source contextengine already loads for context
// assembly — instead of hashing the policy file a second time here.
func (a DecisionGraph) reasoningPolicy(ctx context.Context) (version, hash string, err error) {
	bundle, err := a.Canonical.Load(ctx)
	if err != nil {
		return "", "", err
	}
	for _, source := range bundle.Sources {
		if source.LogicalName == reasoningAssuranceLogicalName {
			return source.Version, source.ContentHash, nil
		}
	}
	return "", "", fmt.Errorf("%s not found in canonical bundle", reasoningAssuranceLogicalName)
}

func verdictBranchState(verdict executive.CompletionVerdict) decisiongraph.BranchState {
	switch verdict {
	case executive.CompletionPass:
		return decisiongraph.BranchSelected
	case executive.CompletionFail:
		return decisiongraph.BranchRejectedByEvidence
	default:
		return decisiongraph.BranchInconclusive
	}
}

func verificationLabel(verdict executive.CompletionVerdict) decisiongraph.VerificationLabel {
	switch verdict {
	case executive.CompletionPass:
		return decisiongraph.VerificationVerified
	case executive.CompletionFail:
		return decisiongraph.VerificationContradicted
	default:
		return decisiongraph.VerificationUnknown
	}
}

func digestString(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

var _ executive.DecisionRecorder = DecisionGraph{}
