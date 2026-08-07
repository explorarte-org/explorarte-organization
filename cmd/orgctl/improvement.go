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
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/decisiongraphtrace"
	"github.com/Mireuz13/explorarte-organization/internal/evaluation"
	"github.com/Mireuz13/explorarte-organization/internal/improvement"
	improvementpostgres "github.com/Mireuz13/explorarte-organization/internal/improvement/postgres"
)

type improvementArtifactInput struct {
	ArtifactID    string `json:"artifact_id"`
	ContentHash   string `json:"content_hash"`
	SchemaVersion string `json:"schema_version"`
}

type improvementLineageInput struct {
	ParentCandidateID  string `json:"parent_candidate_id,omitempty"`
	ParentArtifactHash string `json:"parent_artifact_hash,omitempty"`
	DerivedFrom        string `json:"derived_from,omitempty"`
}

type improvementProposeInput struct {
	ID        string                   `json:"id"`
	Artifact  improvementArtifactInput `json:"artifact"`
	Lineage   improvementLineageInput  `json:"lineage"`
	CreatedBy string                   `json:"created_by"`
}

type improvementMetricDeltaInput struct {
	Name           string  `json:"name"`
	BaselineValue  float64 `json:"baseline_value"`
	CandidateValue float64 `json:"candidate_value"`
	Delta          float64 `json:"delta"`
	Unit           string  `json:"unit"`
}

type improvementCaseResultInput struct {
	CaseID           string                        `json:"case_id"`
	Weight           float64                       `json:"weight"`
	BaselineVerdict  string                        `json:"baseline_verdict"`
	CandidateVerdict string                        `json:"candidate_verdict"`
	Deltas           []improvementMetricDeltaInput `json:"deltas"`
	OverallVerdict   string                        `json:"overall_verdict"`
}

type improvementComparisonInput struct {
	SuiteID           string                       `json:"suite_id"`
	CaseResults       []improvementCaseResultInput `json:"case_results"`
	OverallVerdict    string                       `json:"overall_verdict"`
	WeightedPassRatio float64                      `json:"weighted_pass_ratio"`
}

func (c improvementComparisonInput) comparison() evaluation.SuiteComparisonResult {
	caseResults := make([]evaluation.ComparisonResult, 0, len(c.CaseResults))
	for _, item := range c.CaseResults {
		deltas := make([]evaluation.MetricDelta, 0, len(item.Deltas))
		for _, delta := range item.Deltas {
			deltas = append(deltas, evaluation.MetricDelta{
				Name: delta.Name, BaselineValue: delta.BaselineValue, CandidateValue: delta.CandidateValue,
				Delta: delta.Delta, Unit: delta.Unit,
			})
		}
		caseResults = append(caseResults, evaluation.ComparisonResult{
			CaseID: item.CaseID, Weight: item.Weight,
			BaselineVerdict: evaluation.Verdict(item.BaselineVerdict), CandidateVerdict: evaluation.Verdict(item.CandidateVerdict),
			Deltas: deltas, OverallVerdict: evaluation.Verdict(item.OverallVerdict),
		})
	}
	return evaluation.SuiteComparisonResult{
		SuiteID: c.SuiteID, CaseResults: caseResults,
		OverallVerdict: evaluation.Verdict(c.OverallVerdict), WeightedPassRatio: c.WeightedPassRatio,
	}
}

type improvementVerdictInput struct {
	CandidateID string                     `json:"candidate_id"`
	Comparison  improvementComparisonInput `json:"comparison"`
}

type improvementGateInput struct {
	Outcome   string `json:"outcome"`
	Reason    string `json:"reason,omitempty"`
	DecidedBy string `json:"decided_by"`
}

type improvementPromoteInput struct {
	CandidateID string                     `json:"candidate_id"`
	RequestedBy string                     `json:"requested_by"`
	Comparison  improvementComparisonInput `json:"comparison"`
	Gate        improvementGateInput       `json:"gate"`
}

type improvementRollbackInput struct {
	CandidateID        string `json:"candidate_id"`
	TargetCandidateID  string `json:"target_candidate_id"`
	TargetArtifactHash string `json:"target_artifact_hash"`
}

type cliApprovalGate struct {
	decide  func(improvement.PromotionRequest) (improvement.PromotionDecision, error)
	invoked bool
	request improvement.PromotionRequest
}

func (g *cliApprovalGate) AuthorizePromotion(ctx context.Context, request improvement.PromotionRequest) (improvement.PromotionDecision, error) {
	if err := ctx.Err(); err != nil {
		return improvement.PromotionDecision{}, err
	}
	g.invoked = true
	g.request = request
	return g.decide(request)
}

type nonApprovingGate struct{}

func (nonApprovingGate) AuthorizePromotion(context.Context, improvement.PromotionRequest) (improvement.PromotionDecision, error) {
	return improvement.PromotionDecision{}, errors.New("improvement command does not accept gate decisions")
}

func runImprovement(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printImprovementUsage(stderr)
		return exitUsage
	}
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "load configuration: %v\n", err)
		return exitUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Tasks.CommandTimeout)
	defer cancel()
	store, runner, code := openDatabase(ctx, cfg, stderr, "improvement")
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
	candidates, err := improvementpostgres.New(store, cfg.Tasks.OrganizationID)
	if err != nil {
		fmt.Fprintf(stderr, "create improvement store: %v\n", err)
		return exitInternal
	}

	switch args[0] {
	case "propose":
		var input improvementProposeInput
		jsonOutput, code := parseImprovementFile(args[1:], stderr, &input)
		if code != exitOK {
			return code
		}
		service, serviceErr := improvement.NewService(nonApprovingGate{}, improvement.SystemClock{})
		if serviceErr != nil {
			fmt.Fprintf(stderr, "create improvement service: %v\n", serviceErr)
			return exitInternal
		}
		candidate, serviceErr := service.ProposeCandidate(input.ID, improvement.ArtifactRef{
			ArtifactID: input.Artifact.ArtifactID, ContentHash: input.Artifact.ContentHash, SchemaVersion: input.Artifact.SchemaVersion,
		}, improvement.Lineage{
			ParentCandidateID: input.Lineage.ParentCandidateID, ParentArtifactHash: input.Lineage.ParentArtifactHash, DerivedFrom: input.Lineage.DerivedFrom,
		})
		if serviceErr != nil {
			return improvementCommandError(stderr, serviceErr)
		}
		revision, storeErr := candidates.ProposeCandidate(ctx, candidate, input.CreatedBy)
		if storeErr != nil {
			return improvementCommandError(stderr, storeErr)
		}
		writeValue(stdout, jsonOutput, map[string]any{"id": candidate.ID, "state": candidate.State, "revision": revision})
		return exitOK
	case "get":
		jsonOutput, id, code := parseImprovementCandidateID(args[1:], stderr)
		if code != exitOK {
			return code
		}
		candidate, revision, storeErr := candidates.GetCandidate(ctx, id)
		if storeErr != nil {
			return improvementCommandError(stderr, storeErr)
		}
		writeValue(stdout, jsonOutput, map[string]any{"candidate": candidate, "revision": revision})
		return exitOK
	case "validate", "begin-evaluation", "deprecate":
		jsonOutput, id, code := parseImprovementCandidateID(args[1:], stderr)
		if code != exitOK {
			return code
		}
		return runImprovementTransition(ctx, args[0], id, jsonOutput, stdout, stderr, candidates)
	case "rollback":
		var input improvementRollbackInput
		jsonOutput, code := parseImprovementFile(args[1:], stderr, &input)
		if code != exitOK {
			return code
		}
		return runImprovementRollback(ctx, input, jsonOutput, stdout, stderr, candidates)
	case "verdict":
		var input improvementVerdictInput
		jsonOutput, code := parseImprovementFile(args[1:], stderr, &input)
		if code != exitOK {
			return code
		}
		return runImprovementVerdict(ctx, input, jsonOutput, stdout, stderr, candidates)
	case "promote-canary", "promote-active":
		var input improvementPromoteInput
		jsonOutput, code := parseImprovementFile(args[1:], stderr, &input)
		if code != exitOK {
			return code
		}
		return runImprovementPromotion(ctx, args[0], input, jsonOutput, stdout, stderr, candidates)
	case "trace":
		jsonOutput, runID, code := parseImprovementRunID(args[1:], stderr)
		if code != exitOK {
			return code
		}
		traces, traceErr := decisiongraphtrace.New(store, cfg.Tasks.OrganizationID)
		if traceErr != nil {
			fmt.Fprintf(stderr, "create decision graph trace source: %v\n", traceErr)
			return exitInternal
		}
		value, traceErr := traces.TraceRefForRun(ctx, runID)
		if traceErr != nil {
			return improvementCommandError(stderr, traceErr)
		}
		writeValue(stdout, jsonOutput, value)
		return exitOK
	default:
		printImprovementUsage(stderr)
		return exitUsage
	}
}

func runImprovementTransition(ctx context.Context, command, id string, jsonOutput bool, stdout, stderr io.Writer, candidates improvement.CandidateStore) int {
	service, err := improvement.NewService(nonApprovingGate{}, improvement.SystemClock{})
	if err != nil {
		fmt.Fprintf(stderr, "create improvement service: %v\n", err)
		return exitInternal
	}
	candidate, revision, err := candidates.GetCandidate(ctx, id)
	if err != nil {
		return improvementCommandError(stderr, err)
	}
	var updated improvement.Candidate
	switch command {
	case "validate":
		updated, err = service.ValidateCandidate(candidate)
	case "begin-evaluation":
		updated, err = service.BeginEvaluation(candidate)
	case "deprecate":
		updated, err = service.Deprecate(candidate)
	}
	if err != nil {
		return improvementCommandError(stderr, err)
	}
	newRevision, err := candidates.SaveCandidate(ctx, updated, revision)
	if err != nil {
		return improvementCommandError(stderr, err)
	}
	writeValue(stdout, jsonOutput, map[string]any{"id": updated.ID, "state": updated.State, "revision": newRevision})
	return exitOK
}

func runImprovementVerdict(ctx context.Context, input improvementVerdictInput, jsonOutput bool, stdout, stderr io.Writer, candidates improvement.CandidateStore) int {
	service, err := improvement.NewService(nonApprovingGate{}, improvement.SystemClock{})
	if err != nil {
		fmt.Fprintf(stderr, "create improvement service: %v\n", err)
		return exitInternal
	}
	candidate, revision, err := candidates.GetCandidate(ctx, input.CandidateID)
	if err != nil {
		return improvementCommandError(stderr, err)
	}
	updated, err := service.RecordEvaluationVerdict(candidate, input.Comparison.comparison())
	if err != nil {
		return improvementCommandError(stderr, err)
	}
	newRevision, err := candidates.SaveCandidate(ctx, updated, revision)
	if err != nil {
		return improvementCommandError(stderr, err)
	}
	writeValue(stdout, jsonOutput, map[string]any{"id": updated.ID, "state": updated.State, "revision": newRevision})
	return exitOK
}

func runImprovementRollback(ctx context.Context, input improvementRollbackInput, jsonOutput bool, stdout, stderr io.Writer, candidates improvement.CandidateStore) int {
	service, err := improvement.NewService(nonApprovingGate{}, improvement.SystemClock{})
	if err != nil {
		fmt.Fprintf(stderr, "create improvement service: %v\n", err)
		return exitInternal
	}
	candidate, revision, err := candidates.GetCandidate(ctx, input.CandidateID)
	if err != nil {
		return improvementCommandError(stderr, err)
	}
	updated, err := service.RollBack(candidate, improvement.RollbackTarget{
		CandidateID: input.TargetCandidateID, ArtifactHash: input.TargetArtifactHash, FromState: candidate.State,
	})
	if err != nil {
		return improvementCommandError(stderr, err)
	}
	newRevision, err := candidates.SaveCandidate(ctx, updated, revision)
	if err != nil {
		return improvementCommandError(stderr, err)
	}
	writeValue(stdout, jsonOutput, map[string]any{"id": updated.ID, "state": updated.State, "revision": newRevision})
	return exitOK
}

func runImprovementPromotion(ctx context.Context, command string, input improvementPromoteInput, jsonOutput bool, stdout, stderr io.Writer, candidates improvement.CandidateStore) int {
	clock := improvement.SystemClock{}
	gate := &cliApprovalGate{decide: func(request improvement.PromotionRequest) (improvement.PromotionDecision, error) {
		return improvement.PromotionDecision{
			CandidateID: request.CandidateID, Kind: request.Kind,
			Outcome: improvement.PromotionOutcome(input.Gate.Outcome), Reason: input.Gate.Reason,
			DecidedAt: clock.Now(), DecidedBy: input.Gate.DecidedBy,
		}, nil
	}}
	service, err := improvement.NewService(gate, clock)
	if err != nil {
		fmt.Fprintf(stderr, "create improvement service: %v\n", err)
		return exitInternal
	}
	candidate, revision, err := candidates.GetCandidate(ctx, input.CandidateID)
	if err != nil {
		return improvementCommandError(stderr, err)
	}
	comparison := input.Comparison.comparison()
	var updated improvement.Candidate
	var decision improvement.PromotionDecision
	if command == "promote-canary" {
		updated, decision, err = service.PromoteToCanary(ctx, candidate, input.RequestedBy, comparison)
	} else {
		updated, decision, err = service.PromoteToActive(ctx, candidate, input.RequestedBy, comparison)
	}
	if gate.invoked && (err == nil || errors.Is(err, improvement.ErrPromotionDenied)) {
		if recordErr := candidates.RecordPromotionDecision(ctx, gate.request, decision); recordErr != nil {
			fmt.Fprintf(stderr, "record promotion decision: %v\n", recordErr)
			return exitInternal
		}
	}
	if err != nil {
		return improvementCommandError(stderr, err)
	}
	newRevision, err := candidates.SaveCandidate(ctx, updated, revision)
	if err != nil {
		return improvementCommandError(stderr, err)
	}
	writeValue(stdout, jsonOutput, map[string]any{
		"id": updated.ID, "state": updated.State, "revision": newRevision,
		"promotion_outcome": decision.Outcome, "decided_by": decision.DecidedBy,
	})
	return exitOK
}

func parseImprovementFile(args []string, stderr io.Writer, destination any) (bool, int) {
	flags := flag.NewFlagSet("improvement file command", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("file", "", "strict JSON request file")
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || *path == "" {
		fmt.Fprintln(stderr, "improvement command requires --file <path> [--json]")
		return false, exitUsage
	}
	body, err := os.ReadFile(*path)
	if err != nil {
		fmt.Fprintf(stderr, "read improvement request: %v\n", err)
		return false, exitUsage
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		fmt.Fprintf(stderr, "decode improvement request: %v\n", err)
		return false, exitUsage
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		fmt.Fprintln(stderr, "decode improvement request: multiple JSON values")
		return false, exitUsage
	}
	return *jsonOutput, exitOK
}

func parseImprovementCandidateID(args []string, stderr io.Writer) (bool, string, int) {
	flags := flag.NewFlagSet("improvement candidate id", flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if err := flags.Parse(args); err != nil || flags.NArg() != 1 {
		fmt.Fprintln(stderr, "improvement command requires exactly one candidate id")
		return false, "", exitUsage
	}
	id := strings.TrimSpace(flags.Arg(0))
	if id == "" {
		fmt.Fprintln(stderr, "candidate id must not be empty")
		return false, "", exitUsage
	}
	return *jsonOutput, id, exitOK
}

func parseImprovementRunID(args []string, stderr io.Writer) (bool, int64, int) {
	flags := flag.NewFlagSet("improvement run id", flag.ContinueOnError)
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

func improvementCommandError(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "improvement operation failed: %v\n", err)
	switch {
	case errors.Is(err, improvement.ErrCandidateNotFound):
		return exitInvalid
	case errors.Is(err, improvement.ErrInvalidArtifactRef),
		errors.Is(err, improvement.ErrInvalidLineage),
		errors.Is(err, improvement.ErrInvalidCandidate),
		errors.Is(err, improvement.ErrInvalidTransition),
		errors.Is(err, improvement.ErrInvalidRollbackTarget),
		errors.Is(err, improvement.ErrInvalidPromotionRequest),
		errors.Is(err, improvement.ErrInvalidPromotionDecision),
		errors.Is(err, evaluation.ErrInvalidSuite),
		errors.Is(err, evaluation.ErrEmptySuite),
		errors.Is(err, evaluation.ErrDuplicateCase),
		errors.Is(err, evaluation.ErrInvalidCase),
		errors.Is(err, evaluation.ErrInvalidRequest),
		errors.Is(err, evaluation.ErrInvalidResult),
		errors.Is(err, evaluation.ErrCaseMismatch),
		errors.Is(err, evaluation.ErrIncomparableResults),
		errors.Is(err, decisiongraphtrace.ErrInvalidRun),
		errors.Is(err, decisiongraphtrace.ErrRunNotSucceeded),
		errors.Is(err, decisiongraphtrace.ErrOrganizationMismatch):
		return exitInvalid
	case errors.Is(err, improvement.ErrPromotionDenied):
		return exitDenied
	case errors.Is(err, improvement.ErrRevisionConflict):
		return exitDrift
	default:
		return exitInternal
	}
}

func printImprovementUsage(out io.Writer) {
	fmt.Fprintln(out, "usage: orgctl improvement <propose|get|validate|begin-evaluation|verdict|promote-canary|promote-active|deprecate|rollback|trace> [options]")
}
