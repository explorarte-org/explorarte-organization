package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/decisiongraph"
	decisionpostgres "github.com/Mireuz13/explorarte-organization/internal/decisiongraph/postgres"
)

type decisionBudgetInput struct {
	MaxNodes         int64  `json:"max_nodes"`
	MaxDepth         int64  `json:"max_depth"`
	MaxParallelNodes int64  `json:"max_parallel_nodes"`
	MaxModelCalls    int64  `json:"max_model_calls"`
	MaxInputTokens   int64  `json:"max_input_tokens"`
	MaxOutputTokens  int64  `json:"max_output_tokens"`
	MaxReplans       int64  `json:"max_replans"`
	MaxVerifications int64  `json:"max_verifications"`
	MaxWallTime      string `json:"max_wall_time"`
}

type decisionCreateInput struct {
	TaskID                       int64               `json:"task_id"`
	AttemptID                    int64               `json:"attempt_id"`
	ReasoningPolicySchemaVersion string              `json:"reasoning_policy_schema_version"`
	ReasoningPolicyHash          string              `json:"reasoning_policy_hash"`
	IdempotencyKey               string              `json:"idempotency_key"`
	Budget                       decisionBudgetInput `json:"budget"`
	Deadline                     string              `json:"deadline"`
	CreatedBy                    string              `json:"created_by"`
}

type decisionNodeInput struct {
	ID                   int64                        `json:"id"`
	Type                 decisiongraph.NodeType       `json:"type"`
	BranchState          decisiongraph.BranchState    `json:"branch_state"`
	ExecutionState       decisiongraph.ExecutionState `json:"execution_state"`
	PayloadSchemaVersion string                       `json:"payload_schema_version"`
	PayloadHash          string                       `json:"payload_hash"`
	ContextSnapshotID    *int64                       `json:"context_snapshot_id,omitempty"`
	CreatedBy            string                       `json:"created_by"`
}

type decisionEdgeInput struct {
	FromNodeID int64                  `json:"from_node_id"`
	ToNodeID   int64                  `json:"to_node_id"`
	Type       decisiongraph.EdgeType `json:"type"`
}

type decisionAppendInput struct {
	RunID     int64               `json:"run_id"`
	Nodes     []decisionNodeInput `json:"nodes"`
	Edges     []decisionEdgeInput `json:"edges"`
	Depths    map[int64]int       `json:"depths"`
	CreatedBy string              `json:"created_by"`
}

type decisionClaimInput struct {
	RunID         int64  `json:"run_id"`
	ClaimedBy     string `json:"claimed_by"`
	LeaseDuration string `json:"lease_duration"`
}

type decisionTransitionInput struct {
	RunID        int64                     `json:"run_id"`
	NodeID       int64                     `json:"node_id"`
	ToState      decisiongraph.BranchState `json:"to_state"`
	EvidenceHash string                    `json:"evidence_hash,omitempty"`
	ReasonCode   string                    `json:"reason_code,omitempty"`
	Actor        string                    `json:"actor"`
}

type decisionFinishInput struct {
	ExecutionID       int64                        `json:"execution_id"`
	ClaimToken        string                       `json:"claim_token"`
	FinalState        decisiongraph.ExecutionState `json:"final_state"`
	InputTokens       int64                        `json:"input_tokens"`
	OutputTokens      int64                        `json:"output_tokens"`
	OutcomeHash       string                       `json:"outcome_hash,omitempty"`
	ReasonCode        string                       `json:"reason_code,omitempty"`
	ModelInvocationID *int64                       `json:"model_invocation_id,omitempty"`
	DispatchAttemptID *int64                       `json:"dispatch_attempt_id,omitempty"`
}

type decisionObservationInput struct {
	ExecutionID         int64  `json:"execution_id"`
	SchemaVersion       string `json:"schema_version"`
	ObservationHash     string `json:"observation_hash"`
	SourceKind          string `json:"source_kind"`
	SourceReferenceHash string `json:"source_reference_hash,omitempty"`
}

type decisionVerificationInput struct {
	RunID           int64                           `json:"run_id"`
	NodeID          int64                           `json:"node_id"`
	ExecutionID     *int64                          `json:"execution_id,omitempty"`
	Label           decisiongraph.VerificationLabel `json:"label"`
	VerifierRef     string                          `json:"verifier_ref"`
	VerifierVersion string                          `json:"verifier_version"`
	EvidenceSetHash string                          `json:"evidence_set_hash"`
	ReasonCodes     []string                        `json:"reason_codes"`
}

type decisionTerminalInput struct {
	RunID                   int64                           `json:"run_id"`
	DecisionNodeID          int64                           `json:"decision_node_id"`
	SelectedCandidateNodeID int64                           `json:"selected_candidate_node_id"`
	EvidenceSetHash         string                          `json:"evidence_set_hash"`
	VerificationSetHash     string                          `json:"verification_set_hash"`
	DecisionHash            string                          `json:"decision_hash"`
	VerificationLabel       decisiongraph.VerificationLabel `json:"verification_label"`
	CreatedBy               string                          `json:"created_by"`
}

func runDecision(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printDecisionUsage(stderr)
		return exitUsage
	}
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "load configuration: %v\n", err)
		return exitUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Tasks.CommandTimeout)
	defer cancel()
	store, runner, code := openDatabase(ctx, cfg, stderr, "decision")
	if code != exitOK {
		return code
	}
	defer store.Close()
	status, err := runner.Status(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "migration status: %v\n", err)
		return exitInternal
	}
	if !status.Ready {
		fmt.Fprintf(stderr, "database schema has %d pending migrations\n", status.Pending)
		return exitDrift
	}
	ledger, err := decisionpostgres.New(store, cfg.Tasks.OrganizationID)
	if err != nil {
		fmt.Fprintf(stderr, "create decision graph ledger: %v\n", err)
		return exitInternal
	}
	service, err := decisiongraph.NewService(ledger, decisiongraph.SystemClock{})
	if err != nil {
		fmt.Fprintf(stderr, "create decision graph service: %v\n", err)
		return exitInternal
	}

	switch args[0] {
	case "create":
		var input decisionCreateInput
		jsonOutput, code := parseDecisionFile(args[1:], stderr, &input)
		if code != exitOK {
			return code
		}
		wallTime, err := time.ParseDuration(input.Budget.MaxWallTime)
		if err != nil {
			fmt.Fprintf(stderr, "parse max_wall_time: %v\n", err)
			return exitUsage
		}
		deadline, err := time.Parse(time.RFC3339Nano, input.Deadline)
		if err != nil {
			fmt.Fprintf(stderr, "parse deadline: %v\n", err)
			return exitUsage
		}
		value, err := service.CreateRun(ctx, decisiongraph.CreateRunRequest{
			TaskID: input.TaskID, AttemptID: input.AttemptID,
			ReasoningPolicySchemaVersion: input.ReasoningPolicySchemaVersion,
			ReasoningPolicyHash:          input.ReasoningPolicyHash, IdempotencyKey: input.IdempotencyKey,
			BudgetLimits: decisiongraph.BudgetLimits{
				MaxNodes: input.Budget.MaxNodes, MaxDepth: input.Budget.MaxDepth,
				MaxParallelNodes: input.Budget.MaxParallelNodes, MaxModelCalls: input.Budget.MaxModelCalls,
				MaxInputTokens: input.Budget.MaxInputTokens, MaxOutputTokens: input.Budget.MaxOutputTokens,
				MaxReplans: input.Budget.MaxReplans, MaxVerifications: input.Budget.MaxVerifications,
				MaxWallTime: wallTime,
			},
			Deadline: deadline, CreatedBy: input.CreatedBy,
		})
		if err != nil {
			return decisionCommandError(stderr, err)
		}
		writeValue(stdout, jsonOutput, value)
		return exitOK
	case "append":
		var input decisionAppendInput
		jsonOutput, code := parseDecisionFile(args[1:], stderr, &input)
		if code != exitOK {
			return code
		}
		nodes := make([]decisiongraph.Node, 0, len(input.Nodes))
		for _, item := range input.Nodes {
			nodes = append(nodes, decisiongraph.Node{
				ID: item.ID, Type: item.Type, BranchState: item.BranchState,
				ExecutionState: item.ExecutionState, PayloadSchemaVersion: item.PayloadSchemaVersion,
				PayloadHash: item.PayloadHash, ContextSnapshotID: item.ContextSnapshotID, CreatedBy: item.CreatedBy,
			})
		}
		edges := make([]decisiongraph.Edge, 0, len(input.Edges))
		for _, item := range input.Edges {
			edges = append(edges, decisiongraph.Edge{FromNodeID: item.FromNodeID, ToNodeID: item.ToNodeID, Type: item.Type})
		}
		value, err := service.AppendGraph(ctx, decisiongraph.AppendGraphRequest{
			RunID: input.RunID, Nodes: nodes, Edges: edges, Depths: input.Depths, CreatedBy: input.CreatedBy,
		})
		if err != nil {
			return decisionCommandError(stderr, err)
		}
		writeValue(stdout, jsonOutput, value)
		return exitOK
	case "start":
		jsonOutput, runID, code := parseDecisionRunID(args[1:], stderr)
		if code != exitOK {
			return code
		}
		if err := service.StartRun(ctx, runID); err != nil {
			return decisionCommandError(stderr, err)
		}
		writeValue(stdout, jsonOutput, map[string]any{"run_id": runID, "status": "running"})
		return exitOK
	case "transition":
		var input decisionTransitionInput
		jsonOutput, code := parseDecisionFile(args[1:], stderr, &input)
		if code != exitOK {
			return code
		}
		err := service.TransitionBranch(ctx, decisiongraph.BranchTransitionRequest{
			RunID: input.RunID, NodeID: input.NodeID, ToState: input.ToState,
			EvidenceHash: input.EvidenceHash, ReasonCode: input.ReasonCode, Actor: input.Actor,
		})
		if err != nil {
			return decisionCommandError(stderr, err)
		}
		writeValue(stdout, jsonOutput, map[string]any{"run_id": input.RunID, "node_id": input.NodeID, "branch_state": input.ToState})
		return exitOK
	case "claim":
		var input decisionClaimInput
		jsonOutput, code := parseDecisionFile(args[1:], stderr, &input)
		if code != exitOK {
			return code
		}
		lease, err := time.ParseDuration(input.LeaseDuration)
		if err != nil {
			fmt.Fprintf(stderr, "parse lease_duration: %v\n", err)
			return exitUsage
		}
		value, err := service.ClaimReadyNode(ctx, decisiongraph.ClaimNodeRequest{RunID: input.RunID, ClaimedBy: input.ClaimedBy, LeaseDuration: lease})
		if err != nil {
			return decisionCommandError(stderr, err)
		}
		writeValue(stdout, jsonOutput, value)
		return exitOK
	case "finish":
		var input decisionFinishInput
		jsonOutput, code := parseDecisionFile(args[1:], stderr, &input)
		if code != exitOK {
			return code
		}
		err := service.FinishExecution(ctx, decisiongraph.FinishExecutionRequest{
			ExecutionID: input.ExecutionID, ClaimToken: input.ClaimToken, FinalState: input.FinalState,
			InputTokens: input.InputTokens, OutputTokens: input.OutputTokens,
			OutcomeHash: input.OutcomeHash, ReasonCode: input.ReasonCode,
			ModelInvocationID: input.ModelInvocationID, DispatchAttemptID: input.DispatchAttemptID,
		})
		if err != nil {
			return decisionCommandError(stderr, err)
		}
		writeValue(stdout, jsonOutput, map[string]any{"execution_id": input.ExecutionID, "state": input.FinalState})
		return exitOK
	case "observe":
		var input decisionObservationInput
		jsonOutput, code := parseDecisionFile(args[1:], stderr, &input)
		if code != exitOK {
			return code
		}
		err := service.RecordObservation(ctx, decisiongraph.ObservationRecord{
			ExecutionID: input.ExecutionID, SchemaVersion: input.SchemaVersion,
			ObservationHash: input.ObservationHash, SourceKind: input.SourceKind,
			SourceReferenceHash: input.SourceReferenceHash,
		})
		if err != nil {
			return decisionCommandError(stderr, err)
		}
		writeValue(stdout, jsonOutput, map[string]any{"execution_id": input.ExecutionID, "recorded": true})
		return exitOK
	case "verify":
		var input decisionVerificationInput
		jsonOutput, code := parseDecisionFile(args[1:], stderr, &input)
		if code != exitOK {
			return code
		}
		err := service.RecordVerification(ctx, decisiongraph.VerificationRecord{
			RunID: input.RunID, NodeID: input.NodeID, ExecutionID: input.ExecutionID,
			Label: input.Label, VerifierRef: input.VerifierRef, VerifierVersion: input.VerifierVersion,
			EvidenceSetHash: input.EvidenceSetHash, ReasonCodes: input.ReasonCodes,
		})
		if err != nil {
			return decisionCommandError(stderr, err)
		}
		writeValue(stdout, jsonOutput, map[string]any{"run_id": input.RunID, "node_id": input.NodeID, "label": input.Label})
		return exitOK
	case "decide":
		var input decisionTerminalInput
		jsonOutput, code := parseDecisionFile(args[1:], stderr, &input)
		if code != exitOK {
			return code
		}
		err := service.RecordTerminalDecision(ctx, decisiongraph.TerminalDecisionRequest{
			RunID: input.RunID, DecisionNodeID: input.DecisionNodeID,
			SelectedCandidateNodeID: input.SelectedCandidateNodeID,
			EvidenceSetHash:         input.EvidenceSetHash, VerificationSetHash: input.VerificationSetHash,
			DecisionHash: input.DecisionHash, VerificationLabel: input.VerificationLabel, CreatedBy: input.CreatedBy,
		})
		if err != nil {
			return decisionCommandError(stderr, err)
		}
		writeValue(stdout, jsonOutput, map[string]any{"run_id": input.RunID, "status": "succeeded"})
		return exitOK
	case "recover":
		flags := flag.NewFlagSet("decision recover", flag.ContinueOnError)
		flags.SetOutput(stderr)
		limit := flags.Int("limit", 100, "maximum expired executions")
		jsonOutput := flags.Bool("json", false, "emit JSON")
		if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 {
			return exitUsage
		}
		count, err := service.RecoverExpiredExecutions(ctx, *limit)
		if err != nil {
			return decisionCommandError(stderr, err)
		}
		writeValue(stdout, *jsonOutput, map[string]any{"recovered": count})
		return exitOK
	case "trace":
		jsonOutput, runID, code := parseDecisionRunID(args[1:], stderr)
		if code != exitOK {
			return code
		}
		value, err := service.TraceRef(ctx, runID)
		if err != nil {
			return decisionCommandError(stderr, err)
		}
		writeValue(stdout, jsonOutput, value)
		return exitOK
	default:
		printDecisionUsage(stderr)
		return exitUsage
	}
}

func parseDecisionFile(args []string, stderr io.Writer, destination any) (bool, int) {
	flags := flag.NewFlagSet("decision file command", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("file", "", "strict JSON request file")
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || *path == "" {
		fmt.Fprintln(stderr, "decision command requires --file <path> [--json]")
		return false, exitUsage
	}
	body, err := os.ReadFile(*path)
	if err != nil {
		fmt.Fprintf(stderr, "read decision request: %v\n", err)
		return false, exitUsage
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		fmt.Fprintf(stderr, "decode decision request: %v\n", err)
		return false, exitUsage
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		fmt.Fprintln(stderr, "decode decision request: multiple JSON values")
		return false, exitUsage
	}
	return *jsonOutput, exitOK
}

func parseDecisionRunID(args []string, stderr io.Writer) (bool, int64, int) {
	flags := flag.NewFlagSet("decision run id", flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if err := flags.Parse(args); err != nil || flags.NArg() != 1 {
		return false, 0, exitUsage
	}
	runID, err := strconv.ParseInt(flags.Arg(0), 10, 64)
	if err != nil || runID <= 0 {
		fmt.Fprintln(stderr, "run id must be a positive integer")
		return false, 0, exitUsage
	}
	return *jsonOutput, runID, exitOK
}

func decisionCommandError(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "decision graph operation failed: %v\n", err)
	switch {
	case errors.Is(err, decisiongraph.ErrNotFound):
		return exitInvalid
	case errors.Is(err, decisiongraph.ErrIdempotencyConflict),
		errors.Is(err, decisiongraph.ErrInvalidRun),
		errors.Is(err, decisiongraph.ErrInvalidGraph),
		errors.Is(err, decisiongraph.ErrInvalidTransition),
		errors.Is(err, decisiongraph.ErrInvalidClaim),
		errors.Is(err, decisiongraph.ErrInvalidExecution),
		errors.Is(err, decisiongraph.ErrInvalidVerification),
		errors.Is(err, decisiongraph.ErrInvalidDecision):
		return exitInvalid
	case errors.Is(err, decisiongraph.ErrBudgetExceeded),
		errors.Is(err, decisiongraph.ErrRunNotMutable),
		errors.Is(err, decisiongraph.ErrRunNotActive),
		errors.Is(err, decisiongraph.ErrRunDeadlineExceeded),
		errors.Is(err, decisiongraph.ErrClaimUnavailable),
		errors.Is(err, decisiongraph.ErrStaleClaim):
		return exitDenied
	default:
		return exitInternal
	}
}

func printDecisionUsage(out io.Writer) {
	fmt.Fprintln(out, "usage: orgctl decision <create|append|start|transition|claim|finish|observe|verify|decide|recover|trace> [options]")
}
