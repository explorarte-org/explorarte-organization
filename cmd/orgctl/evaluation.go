package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/decisiongraphfixtures"
	"github.com/Mireuz13/explorarte-organization/internal/evaluation/fixtures"
	evaluationpostgres "github.com/Mireuz13/explorarte-organization/internal/evaluation/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/webevidencefixtures"
)

func printEvaluationUsage(stderr io.Writer) {
	fmt.Fprintln(stderr, `usage: orgctl evaluation <command> [options]
commands:
   seed    --suite r30 [--json]                     validate and list the fixture catalog for a suite
   run     --suite r30 --mode <subject> [--json]     execute every fixture a runner supports, persist the results
   compare <run-a> <run-b> [--json]                  diff two persisted runs by fixture pass/fail
   report  <run-id> [--json]                         print a persisted run and its outcomes`)
}

// evaluationRunners returns every fixtures.Runner this build can execute,
// in a fixed order. Later R30 phases append to this list (a retrieval
// runner for the RAG/memory fixtures, ...) — orgctl evaluation run/seed
// never need to change again when they do, since RunSuite already skips
// whatever a given Runner does not support.
func evaluationRunners() []fixtures.Runner {
	return []fixtures.Runner{decisiongraphfixtures.DecisionGraphRunner{}, webevidencefixtures.WebEvidenceRunner{}}
}

func runEvaluation(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printEvaluationUsage(stderr)
		return exitUsage
	}
	switch args[0] {
	case "seed":
		return evaluationSeed(args[1:], stdout, stderr)
	case "run":
		return evaluationRun(args[1:], stdout, stderr)
	case "compare":
		return evaluationCompare(args[1:], stdout, stderr)
	case "report":
		return evaluationReport(args[1:], stdout, stderr)
	default:
		printEvaluationUsage(stderr)
		return exitUsage
	}
}

// fixturesForSuite returns the suite's fixtures already activated by
// every known Runner-attaching package (today: decisiongraphfixtures,
// webevidencefixtures) — the single place seed/run/report agree on what
// "runner-ready" means.
func fixturesForSuite(suite string) ([]fixtures.Fixture, error) {
	switch suite {
	case "r30":
		catalog := decisiongraphfixtures.Activate(fixtures.CatalogR30())
		catalog = webevidencefixtures.Activate(catalog)
		return catalog, nil
	default:
		return nil, fmt.Errorf("unknown suite %q", suite)
	}
}

type evaluationFixtureSummary struct {
	ID           string `json:"id"`
	Version      int    `json:"version"`
	Title        string `json:"title"`
	Status       string `json:"status"`
	PendingPhase string `json:"pending_phase,omitempty"`
	RunnerKind   string `json:"runner_kind"`
}

func evaluationSeed(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("evaluation seed", flag.ContinueOnError)
	flags.SetOutput(stderr)
	suite := flags.String("suite", "", "suite id (e.g. r30)")
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if *suite == "" || flags.NArg() != 0 {
		printEvaluationUsage(stderr)
		return exitUsage
	}
	catalog, err := fixturesForSuite(*suite)
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return exitInvalid
	}
	summaries := make([]evaluationFixtureSummary, 0, len(catalog))
	runnerReady := 0
	for _, f := range catalog {
		if err := f.Validate(); err != nil {
			fmt.Fprintf(stderr, "invalid fixture %s: %v\n", f.ID, err)
			return exitInvalid
		}
		if f.Status == fixtures.StatusRunnerReady {
			runnerReady++
		}
		summaries = append(summaries, evaluationFixtureSummary{ID: f.ID, Version: f.Version, Title: f.Title, Status: string(f.Status), PendingPhase: f.PendingPhase, RunnerKind: f.RunnerKind})
	}
	if *jsonOutput {
		writeValue(stdout, true, map[string]any{"suite": *suite, "fixture_count": len(catalog), "runner_ready_count": runnerReady, "fixtures": summaries})
		return exitOK
	}
	fmt.Fprintf(stdout, "suite %s: %d fixtures, %d runner-ready\n", *suite, len(catalog), runnerReady)
	for _, s := range summaries {
		fmt.Fprintf(stdout, "  %-45s v%d  %-13s %s\n", s.ID, s.Version, s.Status, s.Title)
	}
	return exitOK
}

func evaluationRun(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("evaluation run", flag.ContinueOnError)
	flags.SetOutput(stderr)
	suite := flags.String("suite", "", "suite id (e.g. r30)")
	mode := flags.String("mode", "", "subject under test (e.g. lexical, gemini-hybrid, bge-m3-hybrid, decisiongraph)")
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if *suite == "" || *mode == "" || flags.NArg() != 0 {
		printEvaluationUsage(stderr)
		return exitUsage
	}
	catalog, err := fixturesForSuite(*suite)
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return exitInvalid
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "load configuration: %v\n", err)
		return exitUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Authorization.CommandTimeout)
	defer cancel()
	store, runner, code := openDatabase(ctx, cfg, stderr, "evaluation-run")
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
	evalStore, err := evaluationpostgres.New(store, cfg.Tasks.OrganizationID)
	if err != nil {
		fmt.Fprintf(stderr, "create evaluation store: %v\n", err)
		return exitInternal
	}

	now := time.Now().UTC()
	runID, err := evalStore.CreateRun(ctx, *suite, *mode, "orgctl/evaluation-run", now)
	if err != nil {
		fmt.Fprintf(stderr, "create evaluation run: %v\n", err)
		return exitInternal
	}

	var allOutcomes []fixtures.RunOutcome
	for _, r := range evaluationRunners() {
		outcomes, runErr := fixtures.RunSuite(ctx, r, catalog, *mode)
		if runErr != nil {
			fmt.Fprintf(stderr, "run suite: %v\n", runErr)
			return exitInternal
		}
		for _, outcome := range outcomes {
			if err := evalStore.RecordOutcome(ctx, runID, outcome, time.Now().UTC()); err != nil {
				fmt.Fprintf(stderr, "record outcome for %s: %v\n", outcome.FixtureID, err)
				return exitInternal
			}
		}
		allOutcomes = append(allOutcomes, outcomes...)
	}

	// expectedReady is how many fixtures the catalog itself claims are
	// runner-ready right now — as opposed to len(catalog), which also
	// counts fixtures honestly marked fixtures.StatusPending for a future
	// R30 phase that has not landed yet (see internal/evaluation/fixtures.
	// Fixture's doc comment). Those are an expected, structural gap, not a
	// coverage failure, and this run is never held responsible for them.
	// executed < expectedReady, by contrast, means a fixture the catalog
	// claims is ready today was NOT actually run — e.g. its RunnerKind has
	// no matching Runner wired into evaluationRunners(), or a Runner
	// regressed Supports() — exactly the silent-partial-coverage failure
	// mode this check exists to catch, indistinguishable from a clean
	// pending gap unless counted separately like this.
	expectedReady := 0
	for _, f := range catalog {
		if f.Status == fixtures.StatusRunnerReady {
			expectedReady++
		}
	}
	executed := len(allOutcomes)
	skipped := len(catalog) - executed
	coverageComplete := executed == expectedReady

	// CompleteRun's own doc comment already promises "an incomplete canary
	// run must never be reported as if it were a full comparison" — this
	// is that promise actually enforced: completed_at stays NULL whenever
	// coverage fell short, so evaluationCompare (and any other consumer of
	// GetRun) can reject it outright instead of trusting a skipped count
	// nobody is required to check.
	if coverageComplete {
		if err := evalStore.CompleteRun(ctx, runID, time.Now().UTC()); err != nil {
			fmt.Fprintf(stderr, "complete evaluation run: %v\n", err)
			return exitInternal
		}
	}

	passed, failed := 0, 0
	for _, outcome := range allOutcomes {
		if outcome.Passed {
			passed++
		} else {
			failed++
		}
	}
	if *jsonOutput {
		writeValue(stdout, true, map[string]any{
			"run_id": runID, "suite": *suite, "mode": *mode, "executed": executed, "passed": passed, "failed": failed,
			"skipped": skipped, "expected_ready": expectedReady, "coverage_complete": coverageComplete,
		})
	} else {
		fmt.Fprintf(stdout, "run %d: suite=%s mode=%s executed=%d passed=%d failed=%d skipped=%d expected_ready=%d coverage_complete=%v\n",
			runID, *suite, *mode, executed, passed, failed, skipped, expectedReady, coverageComplete)
	}
	if !coverageComplete {
		fmt.Fprintf(stderr, "evaluation run %d executed only %d/%d runner-ready fixtures — left incomplete (completed_at unset), never treat this as a full pass\n", runID, executed, expectedReady)
		return exitCompletionInconclusive
	}
	if failed > 0 {
		return exitCompletionFailed
	}
	return exitOK
}

func evaluationReport(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("evaluation report", flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if flags.NArg() != 1 {
		printEvaluationUsage(stderr)
		return exitUsage
	}
	runID, err := strconv.ParseInt(flags.Arg(0), 10, 64)
	if err != nil || runID <= 0 {
		fmt.Fprintf(stderr, "invalid run id %q\n", flags.Arg(0))
		return exitUsage
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "load configuration: %v\n", err)
		return exitUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Authorization.CommandTimeout)
	defer cancel()
	store, _, code := openDatabase(ctx, cfg, stderr, "evaluation-report")
	if code != exitOK {
		return code
	}
	defer store.Close()
	evalStore, err := evaluationpostgres.New(store, cfg.Tasks.OrganizationID)
	if err != nil {
		fmt.Fprintf(stderr, "create evaluation store: %v\n", err)
		return exitInternal
	}
	run, err := evalStore.GetRun(ctx, runID)
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return exitInvalid
	}
	outcomes, err := evalStore.ListOutcomes(ctx, runID)
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return exitInternal
	}
	if *jsonOutput {
		writeValue(stdout, true, map[string]any{"run": run, "outcomes": outcomes})
		return exitOK
	}
	fmt.Fprintf(stdout, "run %d: suite=%s mode=%s started=%s\n", run.ID, run.SuiteID, run.SubjectID, run.StartedAt.Format(time.RFC3339))
	for _, outcome := range outcomes {
		result := "PASS"
		if !outcome.Passed {
			result = "FAIL"
		}
		fmt.Fprintf(stdout, "  [%s] %s\n", result, outcome.FixtureID)
		for _, violated := range outcome.ViolatedInvariants {
			fmt.Fprintf(stdout, "        violated: %s\n", violated)
		}
	}
	return exitOK
}

func evaluationCompare(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("evaluation compare", flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if flags.NArg() != 2 {
		printEvaluationUsage(stderr)
		return exitUsage
	}
	runAID, errA := strconv.ParseInt(flags.Arg(0), 10, 64)
	runBID, errB := strconv.ParseInt(flags.Arg(1), 10, 64)
	if errA != nil || errB != nil || runAID <= 0 || runBID <= 0 {
		fmt.Fprintf(stderr, "invalid run ids %q %q\n", flags.Arg(0), flags.Arg(1))
		return exitUsage
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "load configuration: %v\n", err)
		return exitUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Authorization.CommandTimeout)
	defer cancel()
	store, _, code := openDatabase(ctx, cfg, stderr, "evaluation-compare")
	if code != exitOK {
		return code
	}
	defer store.Close()
	evalStore, err := evaluationpostgres.New(store, cfg.Tasks.OrganizationID)
	if err != nil {
		fmt.Fprintf(stderr, "create evaluation store: %v\n", err)
		return exitInternal
	}

	// A run whose completed_at is unset was left incomplete on purpose (see
	// evaluationRun) — comparing it would silently treat a partial run as
	// if it had full coverage, exactly the "4/14 read as total approval"
	// risk this gate exists to close. Fail closed instead of guessing.
	runA, err := evalStore.GetRun(ctx, runAID)
	if err != nil {
		fmt.Fprintf(stderr, "load run %d: %v\n", runAID, err)
		return exitInvalid
	}
	runB, err := evalStore.GetRun(ctx, runBID)
	if err != nil {
		fmt.Fprintf(stderr, "load run %d: %v\n", runBID, err)
		return exitInvalid
	}
	if runA.CompletedAt == nil {
		fmt.Fprintf(stderr, "run %d is incomplete (left unfinished by evaluation run — see coverage_complete) and cannot be compared\n", runAID)
		return exitCompletionInconclusive
	}
	if runB.CompletedAt == nil {
		fmt.Fprintf(stderr, "run %d is incomplete (left unfinished by evaluation run — see coverage_complete) and cannot be compared\n", runBID)
		return exitCompletionInconclusive
	}

	outcomesA, err := evalStore.ListOutcomes(ctx, runAID)
	if err != nil {
		fmt.Fprintf(stderr, "load run %d: %v\n", runAID, err)
		return exitInvalid
	}
	outcomesB, err := evalStore.ListOutcomes(ctx, runBID)
	if err != nil {
		fmt.Fprintf(stderr, "load run %d: %v\n", runBID, err)
		return exitInvalid
	}

	// Two complete runs can still cover different fixture sets — e.g. one
	// suite was extended with new fixtures between runs, or the two runs
	// used different suite ids. Comparing across a mismatched set would
	// misreport genuinely-missing fixtures as unchanged, so this is
	// rejected outright rather than compared with gaps papered over.
	if len(outcomesA) != len(outcomesB) {
		fmt.Fprintf(stderr, "run %d covers %d fixtures, run %d covers %d — refusing to compare runs with different fixture sets\n", runAID, len(outcomesA), runBID, len(outcomesB))
		return exitInvalid
	}
	fixtureIDsA := make(map[string]bool, len(outcomesA))
	for _, o := range outcomesA {
		fixtureIDsA[o.FixtureID] = true
	}
	for _, o := range outcomesB {
		if !fixtureIDsA[o.FixtureID] {
			fmt.Fprintf(stderr, "run %d and run %d cover different fixture sets (e.g. %s only appears in run %d) — refusing to compare\n", runAID, runBID, o.FixtureID, runBID)
			return exitInvalid
		}
	}

	byFixtureA := make(map[string]fixtures.RunOutcome, len(outcomesA))
	for _, o := range outcomesA {
		byFixtureA[o.FixtureID] = o
	}
	byFixtureB := make(map[string]fixtures.RunOutcome, len(outcomesB))
	for _, o := range outcomesB {
		byFixtureB[o.FixtureID] = o
	}

	type comparisonRow struct {
		FixtureID string `json:"fixture_id"`
		PassedA   *bool  `json:"passed_a"`
		PassedB   *bool  `json:"passed_b"`
		Changed   bool   `json:"changed"`
	}
	seen := make(map[string]struct{}, len(byFixtureA)+len(byFixtureB))
	var rows []comparisonRow
	for id := range byFixtureA {
		seen[id] = struct{}{}
	}
	for id := range byFixtureB {
		seen[id] = struct{}{}
	}
	for id := range seen {
		a, aok := byFixtureA[id]
		b, bok := byFixtureB[id]
		row := comparisonRow{FixtureID: id}
		if aok {
			passed := a.Passed
			row.PassedA = &passed
		}
		if bok {
			passed := b.Passed
			row.PassedB = &passed
		}
		row.Changed = !aok || !bok || a.Passed != b.Passed
		rows = append(rows, row)
	}
	if *jsonOutput {
		writeValue(stdout, true, map[string]any{"run_a": runAID, "run_b": runBID, "comparison": rows})
		return exitOK
	}
	fmt.Fprintf(stdout, "compare run %d vs run %d:\n", runAID, runBID)
	for _, row := range rows {
		marker := " "
		if row.Changed {
			marker = "!"
		}
		fmt.Fprintf(stdout, " %s %-45s a=%v b=%v\n", marker, row.FixtureID, boolPtrString(row.PassedA), boolPtrString(row.PassedB))
	}
	return exitOK
}

func boolPtrString(v *bool) string {
	if v == nil {
		return "n/a"
	}
	if *v {
		return "pass"
	}
	return "fail"
}
