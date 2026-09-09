package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	search "github.com/Mireuz13/explorarte-organization/internal/search"
)

// pgLLMInvoker is a deterministic ModelInvoker for the E2E path: it returns
// the strict query JSON the real OpenRouter model would. Everything else in
// the path (persistence, claims, outbox, novelty) is REAL Postgres.
type pgLLMInvoker struct {
	response string
}

func (f pgLLMInvoker) Invoke(_ context.Context, _ search.ModelInvocationRequest) (search.ModelInvocationResult, error) {
	return search.ModelInvocationResult{
		Content:           []byte(f.response),
		FinishReason:      "stop",
		OutputTokens:      42,
		InputTokens:       128,
		ProviderRequestID: "e2e-probe",
	}, nil
}

// TestPostgres_EndToEnd_ProductionResearchPath runs the FULL production path
// against real Postgres:
//
//	migrations (real) → durable store (real) → scheduler over durable store
//	→ LLM QueryGenerator (model-runtime boundary, scripted response)
//	→ SearchExecutor boundary (scripted router) → novelty over durable
//	findings → finding persisted → outbox events persisted → claim
//	released → NextCheckAt advanced.
//
// The only scripted pieces are the two external networks that cannot be
// contacted from tests (LLM provider, search providers) — both sit behind
// the boundaries whose real implementations are exercised by the opt-in
// smoke (credentials required).
func TestPostgres_EndToEnd_ProductionResearchPath(t *testing.T) {
	pool := requireTestDatabase(t)
	store, err := New(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	outbox := NewOutboxSink(pool, nil)

	// Seed the durable agenda exactly like production boot does.
	now := time.Now().UTC().Truncate(time.Millisecond)
	topic := search.ResearchTopic{
		ID: "e2e-topic-1", OrganizationID: "explorarte", DepartmentID: "finanzas",
		Title:         "AI provider pricing changes",
		Description:   "Detect provider pricing and free-tier changes.",
		ResearchClass: search.ResearchWatch, Status: search.TopicStatusActive,
		AllowedIntents: []search.SearchIntent{search.IntentWebGeneral, search.IntentNews},
		Cadence:        30 * time.Minute, NextCheckAt: now.Add(-time.Minute),
		NoveltyWindow: 24 * time.Hour, Priority: 0.7, CreatedBy: "e2e",
	}
	if err := store.SaveTopic(ctx, topic); err != nil {
		t.Fatal(err)
	}

	// Real LLM boundary (scripted response), real history from the store.
	generator, err := search.NewLLMQueryGenerator(
		pgLLMInvoker{response: `{"queries":[{"query":"openrouter gemini pricing change november","intent":"news"}]}`},
		"openrouter", "google/gemma-4-31b-it:free",
		[]search.SearchIntent{search.IntentWebGeneral, search.IntentNews})
	if err != nil {
		t.Fatal(err)
	}
	generator.History = store

	// Scripted SearchExecutor boundary: returns one novel + one duplicate
	// (of a previously persisted finding) evidence item.
	priorURL := "https://news-a.example/pricing-changed"
	if err := store.SaveCycle(ctx, search.ResearchCycle{
		ID: "e2e-prior-cycle", TopicID: topic.ID, DepartmentID: topic.DepartmentID,
		Trigger: search.TriggerScheduler, Outcome: search.CycleOutcomeNoNewInfo,
		StartedAt: now.Add(-time.Hour), CompletedAt: ptrTime(now.Add(-59 * time.Minute)),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveFinding(ctx, search.ResearchFinding{
		ID: "e2e-prior", TopicID: topic.ID, DepartmentID: topic.DepartmentID,
		ResearchCycleID: "e2e-prior-cycle", Title: "prior", Classification: search.FindingInformative,
		EvidenceRefs: []search.EvidenceRef{{Provider: "brave_web", URL: priorURL}},
		CreatedAt:    now.Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	router := &pgFakeRouter{results: []search.SearchResult{
		{Provider: "brave_web", URL: priorURL, Title: "Already seen pricing news", SourceType: "news"},
		{Provider: "brave_web", URL: "https://news-b.example/free-tier-cut", Title: "Free tier cut announced", SourceType: "news"},
	}}

	cfg := search.DefaultSchedulerConfig()
	cfg.MaxTopicsPerTick = 1
	cfg.MaxQueriesPerTopic = 2
	cfg.MaxSearchRequestsPerTick = 2
	scheduler, err := search.NewAutonomousResearchScheduler(search.SchedulerDeps{
		Search:   router,
		Agenda:   store,
		Claims:   store,
		Evidence: store,
		Queries:  generator,
		Events:   outbox,
		RAG:      pgRAGRecorder{store: store},
		Clock:    func() time.Time { return now.Add(time.Minute) },
		Config:   cfg,
	}, cfg)
	if err != nil {
		t.Fatal(err)
	}

	result, err := scheduler.Tick(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.TopicsRun != 1 {
		t.Fatalf("expected 1 topic run, got %d", result.TopicsRun)
	}
	cycle := result.Cycles[0]

	// Cycle durable + closed with a real outcome.
	cycles, err := store.ListCycles(ctx, topic.ID)
	if err != nil {
		t.Fatal(err)
	}
	var persisted *search.ResearchCycle
	for i := range cycles {
		if cycles[i].ID == cycle.ID {
			persisted = &cycles[i]
		}
	}
	if persisted == nil {
		t.Fatal("cycle must be persisted in Postgres")
	}
	if persisted.CompletedAt == nil {
		t.Fatal("cycle must be completed")
	}
	// One novel evidence (news-b) + one duplicate (news-a).
	if persisted.NewEvidenceCount != 1 || persisted.DuplicateCount != 1 {
		t.Fatalf("novelty accounting wrong: new=%d dup=%d", persisted.NewEvidenceCount, persisted.DuplicateCount)
	}
	if persisted.QueriesAttempted != 1 {
		t.Fatalf("LLM generator must drive queries: %d", persisted.QueriesAttempted)
	}

	// Finding durable with evidence refs.
	findings, err := store.ListFindings(ctx, search.FindingFilter{TopicID: topic.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 2 { // prior + new
		t.Fatalf("expected prior + new finding, got %d", len(findings))
	}
	// 1 novel ref -> informative (V2 policy), not durable.
	if findings[0].Classification != search.FindingInformative {
		t.Fatalf("1 novel ref must be informative, got %s", findings[0].Classification)
	}

	// Claim released and NextCheckAt advanced by cadence.
	after, err := store.GetTopic(ctx, topic.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !after.NextCheckAt.Truncate(time.Microsecond).After(now) {
		t.Fatalf("NextCheckAt must advance: %v", after.NextCheckAt)
	}

	// Organizational events reached the durable outbox.
	var outboxCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM research_outbox_events WHERE aggregate_id = $1`, cycle.ID).Scan(&outboxCount); err != nil {
		t.Fatal(err)
	}
	if outboxCount == 0 {
		t.Fatal("research events must reach the durable outbox")
	}

	// Restart path: a NEW store instance sees the same durable state.
	fresh, err := New(pool)
	if err != nil {
		t.Fatal(err)
	}
	again, err := fresh.GetTopic(ctx, topic.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !again.NextCheckAt.Truncate(time.Microsecond).Equal(after.NextCheckAt.Truncate(time.Microsecond)) {
		t.Fatal("durable state must survive a fresh store instance")
	}
}

func ptrTime(t time.Time) *time.Time { return &t }

// pgRAGRecorder is the submit-only RAG governance stub: it records the
// submission in the durable findings ledger (classification already gates
// which findings reach it). Promotion has no representation here at all.
type pgRAGRecorder struct{ store *Store }

func (r pgRAGRecorder) SubmitRAGCandidate(ctx context.Context, finding search.ResearchFinding) error {
	return r.store.SaveFinding(ctx, finding)
}

// pgFakeRouter is the scripted SearchExecutor for the E2E path.
type pgFakeRouter struct {
	results []search.SearchResult
}

func (f *pgFakeRouter) Search(_ context.Context, req search.SearchRequest) (search.SearchResponse, error) {
	if req.Query == "" {
		return search.SearchResponse{}, errors.New("empty query rejected by boundary")
	}
	return search.SearchResponse{
		Results:       f.results,
		ProviderRoute: []search.ProviderID{"brave_web"},
		CompletedAt:   time.Now().UTC(),
	}, nil
}
