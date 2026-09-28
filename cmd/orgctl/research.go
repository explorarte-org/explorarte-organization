package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata" // the research schedule is America/Santiago; the image may carry no zoneinfo

	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/search"
	searchpostgres "github.com/Mireuz13/explorarte-organization/internal/search/postgres"
)

// The research worker (investigacion/research_worker_hourly): at the top of every local hour it runs
// one tick of the autonomous research scheduler over the durable agenda, through the search router,
// inside a daily spend cap. Findings stay in research_findings, where the CEO reads them
// (research.list_findings); nothing here publishes knowledge.

func runResearch(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printResearchUsage(stderr)
		return exitUsage
	}
	switch args[0] {
	case "seed":
		return runResearchSeed(args[1:], stdout, stderr)
	case "tick":
		return runResearchTick(args[1:], stdout, stderr)
	case "worker":
		if len(args) < 2 || args[1] != "run" {
			printResearchUsage(stderr)
			return exitUsage
		}
		return runResearchWorker(args[2:], stdout, stderr)
	case "status":
		return runResearchStatus(args[1:], stdout, stderr)
	default:
		printResearchUsage(stderr)
		return exitUsage
	}
}

func printResearchUsage(out io.Writer) {
	fmt.Fprintln(out, `usage: orgctl research <command>

  seed [--json]        insert the seed research topics that are missing (never rewrites one)
  tick [--json]        run one budgeted research tick now
  worker run           run a tick at the top of every local hour until stopped
                       (requires AUTONOMOUS_RESEARCH_ENABLED=true)
  status [--json]      today's spend, topics and recent findings

Budget: AUTONOMOUS_RESEARCH_DAILY_BUDGET_USD (default 2), charged
AUTONOMOUS_RESEARCH_COST_PER_QUERY_USD per search request (default 0.01) over the local day of
AUTONOMOUS_RESEARCH_TIMEZONE (default America/Santiago). Providers: ORG_SEARCH_PROVIDER_*.`)
}

// researchRuntime is everything a tick needs, opened once per process.
type researchRuntime struct {
	store  *searchpostgres.Store
	events search.ResearchEventSink
	router *search.Router
	budget search.DailySearchBudget
	worker search.WorkerConfig
	close  func()
}

func openResearchRuntime(ctx context.Context, stderr io.Writer, needRouter bool) (*researchRuntime, int) {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "load configuration: %v\n", err)
		return nil, exitUsage
	}
	lookup := search.LookupEnv(os.LookupEnv)
	workerCfg, err := search.LoadWorkerConfig(lookup)
	if err != nil {
		fmt.Fprintf(stderr, "research configuration: %v\n", err)
		return nil, exitUsage
	}
	budget, err := search.LoadDailySearchBudget(lookup)
	if err != nil {
		fmt.Fprintf(stderr, "research budget: %v\n", err)
		return nil, exitUsage
	}
	platform, runner, code := openDatabase(ctx, cfg, stderr, "research")
	if code != exitOK {
		return nil, code
	}
	status, err := runner.Status(ctx)
	if err != nil || !status.Ready {
		platform.Close()
		fmt.Fprintf(stderr, "database schema not ready (pending %d): %v\n", status.Pending, err)
		return nil, exitDrift
	}
	store, err := searchpostgres.New(platform.Pool())
	if err != nil {
		platform.Close()
		fmt.Fprintf(stderr, "research store: %v\n", err)
		return nil, exitInternal
	}
	rt := &researchRuntime{
		store: store, events: searchpostgres.NewOutboxSink(platform.Pool(), slog.Default()),
		budget: budget, worker: workerCfg, close: platform.Close,
	}
	if !needRouter {
		return rt, exitOK
	}
	registry := search.NewRegistry()
	registered := 0
	web, err := search.NewWebProviders(lookup, nil)
	if err == nil {
		var n int
		n, err = web.RegisterInto(registry)
		registered += n
	}
	if err == nil {
		var academic *search.AcademicProviderSet
		if academic, err = search.NewAcademicProviders(lookup, nil); err == nil {
			var n int
			n, err = academic.RegisterInto(registry)
			registered += n
		}
	}
	if err != nil {
		rt.close()
		fmt.Fprintf(stderr, "search providers: %v\n", err)
		return nil, exitUsage
	}
	if registered == 0 {
		rt.close()
		fmt.Fprintln(stderr, "no search provider is enabled (ORG_SEARCH_PROVIDER_*_ENABLED)")
		return nil, exitUsage
	}
	if rt.router, err = search.NewRouter(search.RouterConfig{Registry: registry}); err != nil {
		rt.close()
		fmt.Fprintf(stderr, "search router: %v\n", err)
		return nil, exitInternal
	}
	return rt, exitOK
}

// researchTickReport is one tick's outcome, including a tick the budget skipped.
type researchTickReport struct {
	At            time.Time `json:"at"`
	Skipped       string    `json:"skipped,omitempty"`
	UsedToday     int       `json:"used_today"`
	MaxToday      int       `json:"max_today"`
	TopicsRun     int       `json:"topics_run"`
	SearchesUsed  int       `json:"searches_used"`
	Outcomes      []string  `json:"outcomes,omitempty"`
	FindingsSaved int       `json:"findings_saved"`
}

func (rt *researchRuntime) tick(ctx context.Context) (researchTickReport, error) {
	now := time.Now().UTC()
	report := researchTickReport{At: now, MaxToday: rt.budget.MaxQueries()}
	used, err := rt.store.QueriesAttemptedSince(ctx, rt.budget.DayStart(now))
	if err != nil {
		return report, fmt.Errorf("read today's research spend: %w", err)
	}
	report.UsedToday = used
	schedCfg := rt.worker.Scheduler
	schedCfg.Enabled = true
	// Claims last one tick; ticks are an hour apart.
	schedCfg.TickInterval = time.Hour
	schedCfg, ok := rt.budget.Bound(schedCfg, used)
	if !ok {
		report.Skipped = "daily_budget_exhausted"
		return report, nil
	}
	scheduler, err := search.NewAutonomousResearchScheduler(search.SchedulerDeps{
		Search: rt.router, Agenda: rt.store, Claims: rt.store, Evidence: rt.store, Events: rt.events, Config: schedCfg,
	}, schedCfg)
	if err != nil {
		return report, err
	}
	result, err := scheduler.Tick(ctx)
	if err != nil {
		return report, err
	}
	report.TopicsRun, report.SearchesUsed = result.TopicsRun, result.SearchesUsed
	for _, cycle := range result.Cycles {
		outcome := fmt.Sprintf("%s: %s (%d queries, %d new)", cycle.TopicID, cycle.Outcome, cycle.QueriesAttempted, cycle.NewEvidenceCount)
		if cycle.ErrorClass != "" {
			outcome += " error=" + cycle.ErrorClass
		}
		report.Outcomes = append(report.Outcomes, outcome)
		report.FindingsSaved += cycle.FindingsCreated
	}
	return report, nil
}

func runResearchSeed(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("research seed", flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return exitUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	rt, code := openResearchRuntime(ctx, stderr, false)
	if code != exitOK {
		return code
	}
	defer rt.close()
	var created, kept []string
	for _, topic := range search.InvestigacionSeedTopics(time.Now().UTC()) {
		if _, err := rt.store.GetTopic(ctx, topic.ID); err == nil {
			kept = append(kept, topic.ID)
			continue
		} else if !errors.Is(err, searchpostgres.ErrNotFound) {
			fmt.Fprintf(stderr, "read topic %s: %v\n", topic.ID, err)
			return exitInternal
		}
		if err := rt.store.SaveTopic(ctx, topic); err != nil {
			fmt.Fprintf(stderr, "save topic %s: %v\n", topic.ID, err)
			return exitInternal
		}
		created = append(created, topic.ID)
	}
	writeValue(stdout, *jsonOutput, map[string]any{"created": created, "already_present": kept})
	return exitOK
}

func runResearchTick(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("research tick", flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return exitUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	rt, code := openResearchRuntime(ctx, stderr, true)
	if code != exitOK {
		return code
	}
	defer rt.close()
	report, err := rt.tick(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "research tick: %v\n", err)
		return exitInternal
	}
	writeValue(stdout, *jsonOutput, report)
	return exitOK
}

func runResearchWorker(args []string, _ io.Writer, stderr io.Writer) int {
	if len(args) != 0 {
		printResearchUsage(stderr)
		return exitUsage
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	rt, code := openResearchRuntime(ctx, stderr, true)
	if code != exitOK {
		return code
	}
	defer rt.close()
	if !rt.worker.Enabled {
		fmt.Fprintln(stderr, "research worker is disabled (AUTONOMOUS_RESEARCH_ENABLED is not true)")
		return exitUsage
	}
	logger := slog.Default()
	// A cycle a dead process left open is failed, never completed, and its claim released.
	if recycled, err := rt.store.RecycleUnfinishedCycles(ctx, 2*time.Hour); err != nil {
		logger.Error("research restart recovery failed", "error", err)
	} else if recycled > 0 {
		logger.Info("research restart recovery", "interrupted_cycles", recycled)
	}
	logger.Info("research worker started", "max_requests_per_day", rt.budget.MaxQueries(), "timezone", rt.budget.Location.String())
	for {
		tickCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
		report, err := rt.tick(tickCtx)
		cancel()
		if err != nil {
			logger.Error("research tick failed", "error", err)
		} else {
			logger.Info("research tick", "skipped", report.Skipped, "topics_run", report.TopicsRun,
				"searches_used", report.SearchesUsed, "used_today", report.UsedToday, "max_today", report.MaxToday,
				"findings", report.FindingsSaved, "outcomes", report.Outcomes)
		}
		next := rt.budget.NextHourlyRun(time.Now())
		select {
		case <-ctx.Done():
			logger.Info("research worker stopped")
			return exitOK
		case <-time.After(time.Until(next)):
		}
	}
}

func runResearchStatus(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("research status", flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return exitUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	rt, code := openResearchRuntime(ctx, stderr, false)
	if code != exitOK {
		return code
	}
	defer rt.close()
	now := time.Now().UTC()
	used, err := rt.store.QueriesAttemptedSince(ctx, rt.budget.DayStart(now))
	if err != nil {
		fmt.Fprintf(stderr, "read today's research spend: %v\n", err)
		return exitInternal
	}
	topics, err := rt.store.ListTopics(ctx, "")
	if err != nil {
		fmt.Fprintf(stderr, "list topics: %v\n", err)
		return exitInternal
	}
	findings, err := rt.store.ListFindings(ctx, search.FindingFilter{Since: now.Add(-7 * 24 * time.Hour), Limit: 20})
	if err != nil {
		fmt.Fprintf(stderr, "list findings: %v\n", err)
		return exitInternal
	}
	type topicRow struct {
		ID, Department, Status string
		NextCheckAt            time.Time
	}
	type findingRow struct {
		ID, Topic, Classification, Summary string
		Evidence                           []string
		CreatedAt                          time.Time
	}
	out := struct {
		UsedToday     int
		MaxToday      int
		SpentTodayUSD float64
		Topics        []topicRow
		Findings      []findingRow
	}{UsedToday: used, MaxToday: rt.budget.MaxQueries(), SpentTodayUSD: float64(used) * rt.budget.CostPerQueryUSD}
	for _, topic := range topics {
		out.Topics = append(out.Topics, topicRow{topic.ID, topic.DepartmentID, string(topic.Status), topic.NextCheckAt})
	}
	for _, finding := range findings {
		row := findingRow{ID: finding.ID, Topic: finding.TopicID, Classification: string(finding.Classification), Summary: finding.Summary, CreatedAt: finding.CreatedAt}
		for _, ref := range finding.EvidenceRefs {
			row.Evidence = append(row.Evidence, ref.Title+" <"+ref.URL+">")
		}
		out.Findings = append(out.Findings, row)
	}
	writeValue(stdout, *jsonOutput, out)
	return exitOK
}
