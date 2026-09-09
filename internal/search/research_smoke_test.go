package search

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestSmoke_AutonomousResearchCycle is an OPT-IN external smoke: it runs ONE
// small cycle against the real configured SearchRouter providers and the
// real research model. It never runs under plain `go test ./...` — it
// requires AUTONOMOUS_RESEARCH_SMOKE=1 AND real provider credentials in the
// environment (the webconfig layer enables providers per env/secret files).
// One query, one cycle, no RAG promotion, no loops.
func TestSmoke_AutonomousResearchCycle(t *testing.T) {
	if os.Getenv("AUTONOMOUS_RESEARCH_SMOKE") != "1" {
		t.Skip("AUTONOMOUS_RESEARCH_SMOKE != 1; skipping external smoke")
	}
	// Real SearchRouter with env-configured providers.
	lookup := func(key string) (string, bool) {
		v := os.Getenv(key)
		return v, v != ""
	}
	set, err := NewWebProviders(lookup, nil)
	if err != nil {
		t.Fatalf("provider bootstrap: %v", err)
	}
	registry := NewRegistry()
	registered, err := set.RegisterInto(registry)
	if err != nil {
		t.Fatalf("provider registration: %v", err)
	}
	if registered == 0 {
		t.Skip("no providers configured; smoke requires at least one real provider")
	}
	router, err := NewRouter(RouterConfig{})
	if err != nil {
		t.Fatalf("router bootstrap: %v", err)
	}
	router.registry = registry

	agenda := NewMemoryAgenda()
	base := time.Now().UTC().Truncate(time.Minute)
	cfg := DefaultSchedulerConfig()
	cfg.MaxTopicsPerTick = 1
	cfg.MaxQueriesPerTopic = 1
	cfg.MaxSearchRequestsPerTick = 1
	scheduler, err := NewAutonomousResearchScheduler(SchedulerDeps{
		Search:   router,
		Agenda:   agenda,
		Claims:   MemoryClaimManager{Agenda: agenda},
		Evidence: MemoryEvidenceIndex{Agenda: agenda},
		Clock:    func() time.Time { return base },
		Config:   cfg,
	}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	topic := ResearchTopic{
		ID: "smoke-topic-1", OrganizationID: "explorarte", DepartmentID: "investigacion",
		Title:         "OpenRouter free model availability",
		ResearchClass: ResearchWatch, Status: TopicStatusActive,
		AllowedIntents: []SearchIntent{IntentWebGeneral},
		Cadence:        time.Hour, NextCheckAt: base,
		NoveltyWindow: 24 * time.Hour, Priority: 0.5, CreatedBy: "smoke",
	}
	if err := agenda.SaveTopic(context.Background(), topic); err != nil {
		t.Fatal(err)
	}
	result, err := scheduler.Tick(context.Background())
	if err != nil {
		t.Fatalf("smoke tick failed: %v", err)
	}
	if result.TopicsRun != 1 {
		t.Fatalf("smoke must run exactly one topic, ran %d", result.TopicsRun)
	}
	cycle := result.Cycles[0]
	if cycle.Outcome != CycleOutcomeCompleted && cycle.Outcome != CycleOutcomeNoNewInfo &&
		cycle.Outcome != CycleOutcomePostponed {
		t.Fatalf("smoke cycle ended in unexpected outcome %q", cycle.Outcome)
	}
	t.Logf("smoke cycle: outcome=%s queries=%d results=%d new_evidence=%d",
		cycle.Outcome, cycle.QueriesAttempted, cycle.ResultCount, cycle.NewEvidenceCount)
	// No RAG submission in smoke: the RAG submitter is intentionally nil.
	cycles, _ := agenda.ListCycles(context.Background(), topic.ID)
	if len(cycles) != 1 {
		t.Fatal("cycle must be persisted")
	}
}
