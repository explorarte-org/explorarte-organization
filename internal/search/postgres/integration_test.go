// Postgres integration tests for the durable research agenda. They follow
// the Organization Kernel's convention: gated on ORG_TEST_DATABASE_URL and
// skipped cleanly when absent, so `go test ./...` stays green on machines
// without a disposable database. Like the kernel's testdbguard, the URL must
// point at a database literally named explorarte_test.
package postgres

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	search "github.com/Mireuz13/explorarte-organization/internal/search"
	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
)

// requireTestDatabase returns a pool or skips the test.
func requireTestDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("ORG_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("ORG_TEST_DATABASE_URL not set; skipping Postgres integration test")
	}
	if !strings.HasSuffix(strings.TrimRight(dsn, "/"), "explorarte_test") &&
		!strings.Contains(dsn, "explorarte_test") {
		t.Skipf("refusing non-disposable database %q; only explorarte_test is permitted", dsn)
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	runner, err := platformmigrations.New(pool, rootmigrations.Files)
	if err != nil {
		t.Fatalf("create migration runner: %v", err)
	}
	if _, err := runner.Up(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	truncate := func() {
		_, _ = pool.Exec(context.Background(),
			`TRUNCATE research_query_records, research_topic_proposals,
			        research_outbox_events, research_findings, research_cycles,
			        research_topics, research_knowledge_needs`)
	}
	truncate() // clean slate at test start: no residue from prior runs
	t.Cleanup(truncate)
	return pool
}

func needFixture(id string) search.KnowledgeNeed {
	return search.KnowledgeNeed{
		ID: id, OrganizationID: "explorarte", DepartmentID: "finanzas",
		Question: "Which providers changed pricing?", Source: search.NeedSourceDepartmentRequested,
		Importance: 0.7, Confidence: 0.9,
	}
}

func topicFixture(id string) search.ResearchTopic {
	return search.ResearchTopic{
		ID: id, OrganizationID: "explorarte", DepartmentID: "finanzas",
		Title:         "AI/API provider pricing changes",
		ResearchClass: search.ResearchWatch, Status: search.TopicStatusActive,
		AllowedIntents: []search.SearchIntent{search.IntentWebGeneral},
		Cadence:        time.Hour, NextCheckAt: time.Now().UTC().Add(-time.Minute),
		NoveltyWindow: 24 * time.Hour, Priority: 0.7, CreatedBy: "test",
	}
}

// A. CRUD
func TestPostgres_NeedAndTopicCRUD(t *testing.T) {
	store, err := New(requireTestDatabase(t))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.SaveNeed(ctx, needFixture("need-pg-1")); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetNeed(ctx, "need-pg-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Question != "Which providers changed pricing?" {
		t.Fatalf("need roundtrip failed: %+v", got)
	}
	if err := store.SaveTopic(ctx, topicFixture("topic-pg-1")); err != nil {
		t.Fatal(err)
	}
	topic, err := store.GetTopic(ctx, "topic-pg-1")
	if err != nil {
		t.Fatal(err)
	}
	if topic.Cadence != time.Hour || len(topic.AllowedIntents) != 1 {
		t.Fatalf("topic roundtrip failed: %+v", topic)
	}
	// Update persists.
	topic.Priority = 0.95
	if err := store.SaveTopic(ctx, topic); err != nil {
		t.Fatal(err)
	}
	updated, _ := store.GetTopic(ctx, "topic-pg-1")
	if updated.Priority != 0.95 {
		t.Fatalf("update lost: %v", updated.Priority)
	}
}

// A. Cycle + finding + proposal persistence
func TestPostgres_CycleFindingProposalPersistence(t *testing.T) {
	store, err := New(requireTestDatabase(t))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.SaveTopic(ctx, topicFixture("topic-pg-cyc")); err != nil {
		t.Fatal(err)
	}
	cycle := search.ResearchCycle{
		ID: "cycle-pg-1", TopicID: "topic-pg-cyc", DepartmentID: "finanzas",
		Trigger: search.TriggerScheduler, Outcome: search.CycleOutcomeCompleted,
		StartedAt: time.Now().UTC(), QueriesAttempted: 2,
		ProvidersUsed: []string{"brave_web"}, ResultCount: 4, NewEvidenceCount: 3,
	}
	if err := store.SaveCycle(ctx, cycle); err != nil {
		t.Fatal(err)
	}
	completed := time.Now().UTC()
	cycle.CompletedAt = &completed
	cycle.FindingsCreated = 1
	if err := store.SaveCycle(ctx, cycle); err != nil {
		t.Fatal(err)
	}
	finding := search.ResearchFinding{
		ID: "finding-pg-1", TopicID: "topic-pg-cyc", DepartmentID: "finanzas",
		ResearchCycleID: "cycle-pg-1", Title: "Pricing shift detected",
		Classification: search.FindingImportant,
		EvidenceRefs: []search.EvidenceRef{
			{Provider: "brave_web", URL: "https://news-a.example/pricing"},
		},
	}
	if err := store.SaveFinding(ctx, finding); err != nil {
		t.Fatal(err)
	}
	// Department feed with filters.
	feed, err := store.ListFindings(ctx, search.FindingFilter{DepartmentID: "finanzas", MinImportance: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(feed) != 1 || feed[0].Classification != search.FindingImportant {
		t.Fatalf("feed filter failed: %+v", feed)
	}
	// Pending proposal survives restarts.
	proposal := search.ResearchTopicProposal{
		ID: "prop-pg-1", DepartmentID: "finanzas", Title: "Free tier watch",
		ResearchClass: search.ResearchWatch, Cadence: time.Hour,
		AllowedIntents: []search.SearchIntent{search.IntentWebGeneral},
		Decision:       search.ProposalPending,
		RejectReason:   search.RejectHighFrequency,
		CreatedAt:      time.Now().UTC(),
	}
	if err := store.SaveProposal(ctx, proposal); err != nil {
		t.Fatal(err)
	}
	pending, err := store.ListPendingProposals(ctx, "finanzas")
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].ID != "prop-pg-1" {
		t.Fatalf("pending proposals must be durable: %+v", pending)
	}
}

// B. Claim concurrency: 8 workers, exactly one winner; expiry recovers.
func TestPostgres_DurableClaims(t *testing.T) {
	store, err := New(requireTestDatabase(t))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.SaveTopic(ctx, topicFixture("topic-pg-claim")); err != nil {
		t.Fatal(err)
	}
	const workers = 8
	var wg sync.WaitGroup
	winners := make(chan bool, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			claimed, err := store.TryClaim(ctx, "topic-pg-claim", "worker-"+string(rune('a'+n)), time.Minute)
			if err != nil {
				winners <- false
				return
			}
			winners <- claimed
		}(i)
	}
	wg.Wait()
	close(winners)
	wins := 0
	for won := range winners {
		if won {
			wins++
		}
	}
	if wins != 1 {
		t.Fatalf("exactly one durable claim must win, got %d", wins)
	}
	// Live claim cannot be stolen.
	if claimed, _ := store.TryClaim(ctx, "topic-pg-claim", "thief", time.Minute); claimed {
		t.Fatal("live claim must not be stealable")
	}
	// Expired claim is recoverable.
	recovered, err := store.TryClaimAt(ctx, "topic-pg-claim", "recovery-worker", time.Minute,
		time.Now().UTC().Add(2*time.Minute))
	if err != nil || !recovered {
		t.Fatalf("expired claim must be recoverable: %v %v", recovered, err)
	}
	// Completion releases/resolves the claim.
	if err := store.AdvanceTopic(ctx, "topic-pg-claim", time.Hour, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	topic, _ := store.GetTopic(ctx, "topic-pg-claim")
	if !topic.NextCheckAt.After(time.Now()) {
		t.Fatalf("advance must schedule next check: %v", topic.NextCheckAt)
	}
}

// C. Restart: state survives a fresh store instance on the same DB.
func TestPostgres_RestartSurvival(t *testing.T) {
	pool := requireTestDatabase(t)
	first, err := New(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := first.SaveTopic(ctx, topicFixture("topic-pg-restart")); err != nil {
		t.Fatal(err)
	}
	next := time.Now().UTC().Add(30 * time.Minute)
	topic, _ := first.GetTopic(ctx, "topic-pg-restart")
	topic.NextCheckAt = next
	if err := first.SaveTopic(ctx, topic); err != nil {
		t.Fatal(err)
	}
	// "Process restart": brand-new store over the same pool.
	second, err := New(pool)
	if err != nil {
		t.Fatal(err)
	}
	survived, err := second.GetTopic(ctx, "topic-pg-restart")
	if err != nil {
		t.Fatal(err)
	}
	// Postgres timestamptz stores microseconds; Go time has nanoseconds.
	if !survived.NextCheckAt.Truncate(time.Microsecond).Equal(next.Truncate(time.Microsecond)) {
		t.Fatalf("NextCheckAt must survive restart: %v vs %v", survived.NextCheckAt, next)
	}
	// Orphan recovery marks unfinished cycles failed, never completed.
	cycle := search.ResearchCycle{
		ID: "cycle-pg-orphan", TopicID: "topic-pg-restart", DepartmentID: "finanzas",
		Trigger: search.TriggerScheduler, StartedAt: time.Now().UTC().Add(-time.Hour),
	}
	if err := second.SaveCycle(ctx, cycle); err != nil {
		t.Fatal(err)
	}
	recycled, err := second.RecycleUnfinishedCycles(ctx, 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if recycled != 1 {
		t.Fatalf("expected 1 recycled orphan, got %d", recycled)
	}
	cycles, _ := second.ListCycles(ctx, "topic-pg-restart")
	if cycles[0].Outcome != search.CycleOutcomeFailed || cycles[0].CompletedAt == nil {
		t.Fatalf("orphan must be explicitly failed with completion time, got %+v", cycles[0])
	}
}
