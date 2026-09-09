package search

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"
)

// =============================================================================
// Autonomous Research Worker — production lifecycle
//
// Wires the scheduler to a real runtime:
//
//	AUTONOMOUS_RESEARCH_ENABLED=false    (default off; opt-in)
//	AUTONOMOUS_RESEARCH_TICK=10m         (global evaluation tick)
//	AUTONOMOUS_RESEARCH_CLAIM_TTL=15m    (durable claim window)
//	AUTONOMOUS_RESEARCH_MAX_TOPICS_PER_TICK=2
//	AUTONOMOUS_RESEARCH_MAX_QUERIES_PER_TOPIC=3
//	AUTONOMOUS_RESEARCH_MAX_SEARCH_PER_TICK=5
//	AUTONOMOUS_RESEARCH_CLAIM_OWNER=hostname (worker identity)
//
// Research model identity (host-resolved, changeable without recompiling):
//	RESEARCH_MODEL_PROVIDER=openrouter
//	RESEARCH_MODEL_ID=google/gemma-4-31b-it:free
//
// Lifecycle guarantees: Start returns immediately; Stop cancels cleanly
// without orphan goroutines and without starting new cycles; on boot,
// unfinished cycles from a dead process are marked failed (never completed)
// and their claims released, so the work is recoverable.
// =============================================================================

// WorkerConfig is the resolved production configuration.
type WorkerConfig struct {
	Enabled        bool
	TickInterval   time.Duration
	ClaimTTL       time.Duration
	ClaimOwner     string
	Scheduler      SchedulerConfig
	ModelProvider  string
	ModelID        string
	AllowedIntents []SearchIntent
}

// DefaultWorkerConfig returns conservative production defaults.
func DefaultWorkerConfig() WorkerConfig {
	sched := DefaultSchedulerConfig()
	sched.Enabled = true
	return WorkerConfig{
		Enabled:       false,
		TickInterval:  sched.TickInterval,
		ClaimTTL:      15 * time.Minute,
		ClaimOwner:    defaultClaimOwner(),
		Scheduler:     sched,
		ModelProvider: "openrouter",
		// ModelID default is a model VERIFIED to exist on OpenRouter's public
		// catalog with a live ":free" variant (checked against
		// https://openrouter.ai/api/v1/models at integration time). The earlier
		// default minimax/minimax-m3:free does not exist — minimax/m3 is paid-only —
		// and would fail every invocation. TestResearch_ConfiguredDefaultModelIsInVerifiedFreeCatalog
		// guards this regression.
		ModelID: "google/gemma-4-31b-it:free",
		AllowedIntents: []SearchIntent{
			IntentWebGeneral, IntentNews, IntentAcademic, IntentPreprint,
		},
	}
}

// defaultClaimOwner derives a stable per-process worker identity.
func defaultClaimOwner() string {
	host, err := os.Hostname()
	if err != nil || strings.TrimSpace(host) == "" {
		return "research-worker"
	}
	return "research-worker@" + host
}

// LookupEnv mirrors the kernel's environment lookup convention.

// LoadWorkerConfig resolves the worker configuration from the environment.
// Unknown values never fail boot for optional knobs; malformed durations or
// numbers do, following the kernel's config conventions.
func LoadWorkerConfig(lookup LookupEnv) (WorkerConfig, error) {
	cfg := DefaultWorkerConfig()
	if lookup == nil {
		return cfg, nil
	}
	var err error
	if cfg.Enabled, err = envBool(lookup, "AUTONOMOUS_RESEARCH_ENABLED", false); err != nil {
		return cfg, err
	}
	if cfg.TickInterval, err = envDuration(lookup, "AUTONOMOUS_RESEARCH_TICK", cfg.TickInterval); err != nil {
		return cfg, err
	}
	if cfg.ClaimTTL, err = envDuration(lookup, "AUTONOMOUS_RESEARCH_CLAIM_TTL", cfg.ClaimTTL); err != nil {
		return cfg, err
	}
	if raw, ok := lookup("AUTONOMOUS_RESEARCH_CLAIM_OWNER"); ok && strings.TrimSpace(raw) != "" {
		cfg.ClaimOwner = strings.TrimSpace(raw)
	}
	if raw, ok := lookup("RESEARCH_MODEL_PROVIDER"); ok && strings.TrimSpace(raw) != "" {
		cfg.ModelProvider = strings.TrimSpace(raw)
	}
	if raw, ok := lookup("RESEARCH_MODEL_ID"); ok && strings.TrimSpace(raw) != "" {
		cfg.ModelID = strings.TrimSpace(raw)
	}
	// Scheduler budget knobs.
	sched := &cfg.Scheduler
	sched.TickInterval = cfg.TickInterval
	if sched.MaxTopicsPerTick, err = envInt(lookup, "AUTONOMOUS_RESEARCH_MAX_TOPICS_PER_TICK", sched.MaxTopicsPerTick); err != nil {
		return cfg, err
	}
	if sched.MaxQueriesPerTopic, err = envInt(lookup, "AUTONOMOUS_RESEARCH_MAX_QUERIES_PER_TOPIC", sched.MaxQueriesPerTopic); err != nil {
		return cfg, err
	}
	if sched.MaxSearchRequestsPerTick, err = envInt(lookup, "AUTONOMOUS_RESEARCH_MAX_SEARCH_PER_TICK", sched.MaxSearchRequestsPerTick); err != nil {
		return cfg, err
	}
	if err := sched.Validate(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// WorkerDeps wires the worker to its production boundaries.
type WorkerDeps struct {
	Search   SearchExecutor    // SearchRouter (required)
	Agenda   SchedulerAgenda   // Memory or Postgres (required)
	Claims   TopicClaimManager // required for production (durable)
	Evidence EvidenceIndex     // required for production (durable)
	Invoker  ModelInvoker      // optional; nil => deterministic queries
	History  QueryHistory      // optional; LLM diversity
	Events   ResearchEventSink // optional
	RAG      RAGSubmitter      // optional
	Logger   *slog.Logger      // optional
	Clock    func() time.Time  // optional
	// PoolFactory backs FreeModelRouter with the canonical runtime's
	// provider invokers (cloudflare_workers_ai / mistral / openrouter).
	// Required when FREE_MODEL_ROUTER_ENABLED=true.
	PoolFactory InvokerFactory // optional
}

// ResearchWorker owns the autonomous research lifecycle.
type ResearchWorker struct {
	cfg       WorkerConfig
	scheduler *AutonomousResearchScheduler
	logger    *slog.Logger
	clock     func() time.Time
	cancel    context.CancelFunc
	done      chan struct{}
}

// NewResearchWorker validates deps and constructs the worker. It does NOT
// start anything.
func NewResearchWorker(cfg WorkerConfig, deps WorkerDeps) (*ResearchWorker, error) {
	if !cfg.Enabled {
		return nil, fmt.Errorf("%w: autonomous research disabled", ErrInvalidRequest)
	}
	if deps.Search == nil || deps.Agenda == nil {
		return nil, fmt.Errorf("%w: worker requires SearchRouter and agenda", ErrInvalidRequest)
	}
	if deps.Claims == nil || deps.Evidence == nil {
		return nil, fmt.Errorf("%w: production worker requires durable claims and evidence index", ErrInvalidRequest)
	}
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	clock := deps.Clock
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	schedCfg := cfg.Scheduler
	schedCfg.Enabled = true
	scheduler, err := NewAutonomousResearchScheduler(SchedulerDeps{
		Search:   deps.Search,
		Agenda:   deps.Agenda,
		Claims:   deps.Claims,
		Evidence: deps.Evidence,
		Events:   deps.Events,
		RAG:      deps.RAG,
		Clock:    clock,
		Config:   schedCfg,
	}, schedCfg)
	if err != nil {
		return nil, err
	}
	// FREE_MODEL_ROUTER_ENABLED=true swaps the single-model invoker for the
	// capacity-aware pool selector. Scheduler and QueryGenerator are
	// untouched: the router satisfies the same ModelInvoker seam.
	lookup := func(key string) (string, bool) { return os.LookupEnv(key) }
	if enabled, err := envBool(lookup, "FREE_MODEL_ROUTER_ENABLED", false); err == nil && enabled {
		if deps.PoolFactory == nil {
			return nil, fmt.Errorf("%w: FREE_MODEL_ROUTER_ENABLED requires WorkerDeps.PoolFactory", ErrInvalidRequest)
		}
		poolCfg := LoadResearchPoolConfig(lookup)
		poolRouter, err := BuildResearchModelPool(poolCfg, deps.PoolFactory, deps.Events)
		if err != nil {
			return nil, err
		}
		deps.Invoker = poolRouter
		if deps.Logger != nil {
			deps.Logger.Info("research worker using FreeModelRouter", "allow_paid", poolCfg.AllowPaid)
		}
	}
	if deps.Invoker != nil {
		generator, err := NewLLMQueryGenerator(deps.Invoker, cfg.ModelProvider, cfg.ModelID, cfg.AllowedIntents)
		if err != nil {
			return nil, err
		}
		generator.History = deps.History
		scheduler.deps.Queries = generator
	}
	return &ResearchWorker{
		cfg:       cfg,
		scheduler: scheduler,
		logger:    deps.Logger,
		clock:     clock,
		done:      make(chan struct{}),
	}, nil
}

// RecoverOrphans marks unfinished cycles as failed (never completed) so
// restart semantics are honest, and releases their claims.
type CycleRecycler interface {
	RecycleUnfinishedCycles(ctx context.Context, olderThan time.Duration) (int, error)
}

// RecoverOrphans runs the restart recovery pass. It is safe to call on every
// boot before Start.
func (w *ResearchWorker) RecoverOrphans(ctx context.Context, recycler CycleRecycler) error {
	if recycler == nil {
		return nil
	}
	count, err := recycler.RecycleUnfinishedCycles(ctx, 2*w.cfg.ClaimTTL)
	if err != nil {
		return err
	}
	if count > 0 {
		w.logger.Info("research restart recovery", "interrupted_cycles", count)
	}
	return nil
}

// SeedBootstrapIdempotent applies the canonical seed topics once.
func (w *ResearchWorker) SeedBootstrapIdempotent(ctx context.Context, agenda *MemoryAgenda) error {
	return SeedBootstrapTopics(ctx, agenda, w.clock())
}

// Start launches the tick loop in a goroutine. The first tick happens after
// one interval (the boot path runs recovery/seeds explicitly).
func (w *ResearchWorker) Start(ctx context.Context) {
	runCtx, cancel := context.WithCancel(ctx)
	w.cancel = cancel
	go func() {
		defer close(w.done)
		ticker := time.NewTicker(w.cfg.TickInterval)
		defer ticker.Stop()
		for {
			select {
			case <-runCtx.Done():
				w.logger.Info("research worker stopped", "owner", w.cfg.ClaimOwner)
				return
			case <-ticker.C:
				tickCtx, tickCancel := context.WithTimeout(runCtx, w.cfg.TickInterval)
				result, err := w.scheduler.Tick(tickCtx)
				tickCancel()
				if err != nil {
					w.logger.Error("research tick failed", "error", err)
					continue
				}
				w.logger.Info("research tick completed",
					"topics_run", result.TopicsRun, "searches_used", result.SearchesUsed)
			}
		}
	}()
}

// Stop cancels the loop and waits for a clean exit. No new cycles start
// after Stop; an in-flight tick finishes or aborts via its own context.
func (w *ResearchWorker) Stop(timeout time.Duration) {
	if w.cancel == nil {
		return
	}
	w.cancel()
	select {
	case <-w.done:
	case <-time.After(timeout):
		w.logger.Warn("research worker stop timed out", "owner", w.cfg.ClaimOwner)
	}
}

// SmokeFreeModelPool returns the model IDs the smoke/catalog tests validate
// against the live OpenRouter catalog. Only IDs verified to exist with a
// ":free" variant belong here; the earlier list containing
// minimax/minimax-m3:free (paid-only) was corrected after live validation.
func SmokeFreeModelPool() []string {
	return []string{
		"dots-studio/dots-3-note-preview:free",
		"google/gemma-4-26b-a4b-it:free",
		"google/gemma-4-31b-it:free",
		"nvidia/nemotron-3-nano-omni-30b-a3b-reasoning:free",
		"nvidia/nemotron-3-super-120b-a12b:free",
		"nvidia/nemotron-3.5-lightning:free",
		"poolside/laguna-s-2.1:free",
		"thinkingmachines/inkling:free",
	}
}
