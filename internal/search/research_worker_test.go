package search

import (
	"context"
	"errors"
	"testing"
	"time"
)

// =============================================================================
// Worker config
// =============================================================================

func TestWorkerConfig_Defaults(t *testing.T) {
	cfg, err := LoadWorkerConfig(func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Enabled {
		t.Fatal("worker must default to disabled (opt-in)")
	}
	if cfg.TickInterval != 10*time.Minute {
		t.Fatalf("default tick must be 10m, got %v", cfg.TickInterval)
	}
	if cfg.ModelID == "" || cfg.ModelProvider == "" {
		t.Fatal("research model identity must have host defaults")
	}
}

func TestWorkerConfig_EnvOverrides(t *testing.T) {
	env := map[string]string{
		"AUTONOMOUS_RESEARCH_ENABLED":             "true",
		"AUTONOMOUS_RESEARCH_TICK":                "5m",
		"AUTONOMOUS_RESEARCH_MAX_TOPICS_PER_TICK": "3",
		"AUTONOMOUS_RESEARCH_MAX_SEARCH_PER_TICK": "9",
		"RESEARCH_MODEL_ID":                       "google/gemma-4-31b-it:free",
	}
	cfg, err := LoadWorkerConfig(func(key string) (string, bool) {
		v, ok := env[key]
		return v, ok
	})
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Enabled {
		t.Fatal("enabled override failed")
	}
	if cfg.TickInterval != 5*time.Minute {
		t.Fatalf("tick override failed: %v", cfg.TickInterval)
	}
	if cfg.Scheduler.MaxTopicsPerTick != 3 {
		t.Fatalf("topic cap override failed: %d", cfg.Scheduler.MaxTopicsPerTick)
	}
	if cfg.ModelID != "google/gemma-4-31b-it:free" {
		t.Fatalf("model identity override failed: %s", cfg.ModelID)
	}
}

func TestWorkerConfig_MalformedDurationFails(t *testing.T) {
	_, err := LoadWorkerConfig(func(key string) (string, bool) {
		if key == "AUTONOMOUS_RESEARCH_TICK" {
			return "not-a-duration", true
		}
		return "", false
	})
	if err == nil {
		t.Fatal("malformed duration must fail")
	}
}

// =============================================================================
// Worker lifecycle (K. Shutdown)
// =============================================================================

func TestWorkerLifecycle_StartStopClean(t *testing.T) {
	executor := &fakeSearchExecutor{}
	agenda := NewMemoryAgenda()
	base := time.Now().UTC().Truncate(time.Minute)
	clock := &clockStepper{now: base, step: time.Second}
	schedulerMinTickFloor = time.Millisecond // test-speed ticks only
	cfg := DefaultWorkerConfig()
	cfg.Enabled = true
	cfg.TickInterval = 50 * time.Millisecond // test-speed tick
	cfg.Scheduler.TickInterval = cfg.TickInterval
	worker, err := NewResearchWorker(cfg, WorkerDeps{
		Search:   executor,
		Agenda:   agenda,
		Claims:   MemoryClaimManager{Agenda: agenda},
		Evidence: MemoryEvidenceIndex{Agenda: agenda},
		Clock:    clock.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	worker.Start(ctx)
	time.Sleep(180 * time.Millisecond) // a few ticks
	cancel()
	worker.Stop(2 * time.Second)
	// No goroutine leak: Stop returned within timeout (implicit pass).
	// A disabled worker must refuse construction.
	if _, err := NewResearchWorker(DefaultWorkerConfig(), WorkerDeps{}); err == nil {
		t.Fatal("disabled worker must not construct")
	}
}

func TestWorkerLifecycle_NoNewCyclesAfterStop(t *testing.T) {
	executor := &fakeSearchExecutor{}
	agenda := NewMemoryAgenda()
	topic := watchTopic("topic-afterstop")
	if err := agenda.SaveTopic(context.Background(), topic); err != nil {
		t.Fatal(err)
	}
	schedulerMinTickFloor = time.Millisecond // test-speed ticks only
	cfg := DefaultWorkerConfig()
	cfg.Enabled = true
	cfg.TickInterval = 40 * time.Millisecond
	cfg.Scheduler.TickInterval = cfg.TickInterval
	worker, err := NewResearchWorker(cfg, WorkerDeps{
		Search:   executor,
		Agenda:   agenda,
		Claims:   MemoryClaimManager{Agenda: agenda},
		Evidence: MemoryEvidenceIndex{Agenda: agenda},
		Clock:    func() time.Time { return time.Now().Add(-2 * time.Hour) }, // topic due
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	worker.Start(ctx)
	time.Sleep(120 * time.Millisecond)
	cancel()
	worker.Stop(2 * time.Second)
	callsAfterStop := executor.CallCount()
	time.Sleep(120 * time.Millisecond) // would-be ticks
	if executor.CallCount() != callsAfterStop {
		t.Fatalf("no cycles may start after stop: %d -> %d", callsAfterStop, executor.CallCount())
	}
}

// =============================================================================
// Restart recovery semantics (with a fake recycler; DB integration gates
// the real one)
// =============================================================================

type fakeRecycler struct {
	recycled int
	err      error
	calls    int
}

func (f *fakeRecycler) RecycleUnfinishedCycles(_ context.Context, olderThan time.Duration) (int, error) {
	f.calls++
	_ = olderThan
	return f.recycled, f.err
}

func TestWorkerRecovery_MarksOrphansFailed(t *testing.T) {
	agenda := NewMemoryAgenda()
	cfg := DefaultWorkerConfig()
	cfg.Enabled = true
	worker, err := NewResearchWorker(cfg, WorkerDeps{
		Search:   &fakeSearchExecutor{},
		Agenda:   agenda,
		Claims:   MemoryClaimManager{Agenda: agenda},
		Evidence: MemoryEvidenceIndex{Agenda: agenda},
	})
	if err != nil {
		t.Fatal(err)
	}
	recycler := &fakeRecycler{recycled: 2}
	if err := worker.RecoverOrphans(context.Background(), recycler); err != nil {
		t.Fatal(err)
	}
	if recycler.calls != 1 {
		t.Fatal("recovery must run exactly one recycle pass")
	}
	// Recycler failure propagates: boot must not silently ignore it.
	failing := &fakeRecycler{err: errors.New("db down")}
	if err := worker.RecoverOrphans(context.Background(), failing); err == nil {
		t.Fatal("recovery failure must surface")
	}
}
