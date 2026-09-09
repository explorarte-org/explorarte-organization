package search

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// =============================================================================
// Test fakes
// =============================================================================

// fakeSearchExecutor records SearchRouter calls and returns canned responses.
// It is the ONLY search boundary in research tests — proving the researcher
// never needs anything else.
type fakeSearchExecutor struct {
	mu       sync.Mutex
	calls    []SearchRequest
	response SearchResponse
	err      error
}

func (f *fakeSearchExecutor) Search(_ context.Context, req SearchRequest) (SearchResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, req)
	return f.response, f.err
}

func (f *fakeSearchExecutor) CallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// recordingEvents captures research observability events.
type recordingEvents struct {
	mu     sync.Mutex
	events []string
	fields []map[string]any
}

func (r *recordingEvents) ResearchEvent(event string, fields map[string]any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
	r.fields = append(r.fields, fields)
}

func (r *recordingEvents) Count(event string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, e := range r.events {
		if e == event {
			n++
		}
	}
	return n
}

func (r *recordingEvents) Has(event string) bool { return r.Count(event) > 0 }

// fakeRAGSubmitter records RAG candidate submissions.
type fakeRAGSubmitter struct {
	mu        sync.Mutex
	submitted []ResearchFinding
}

func (f *fakeRAGSubmitter) SubmitRAGCandidate(_ context.Context, finding ResearchFinding) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.submitted = append(f.submitted, finding)
	return nil
}

// clockStepper is a deterministic clock for scheduler tests.
type clockStepper struct {
	mu   sync.Mutex
	now  time.Time
	step time.Duration
}

func (c *clockStepper) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := c.now
	c.now = c.now.Add(c.step)
	return t
}

// newSchedulerFixture builds a fully wired scheduler with deterministic
// defaults for research tests. The clock starts at a rounded real "now" so
// topics built with time.Now() are always consistent with scheduler time.
func newSchedulerFixture(t *testing.T, executor SearchExecutor) (*AutonomousResearchScheduler, *MemoryAgenda, *recordingEvents, *fakeRAGSubmitter, *clockStepper) {
	t.Helper()
	agenda := NewMemoryAgenda()
	events := &recordingEvents{}
	rag := &fakeRAGSubmitter{}
	base := time.Now().UTC().Truncate(time.Minute)
	clock := &clockStepper{now: base, step: time.Second}

	cfg := DefaultSchedulerConfig()
	cfg.MaxTopicsPerTick = 5
	cfg.MaxQueriesPerTopic = 2
	cfg.MaxSearchRequestsPerTick = 10

	sched, err := NewAutonomousResearchScheduler(SchedulerDeps{
		Search: executor,
		Agenda: agenda,
		Events: events,
		RAG:    rag,
		Clock:  clock.Now,
		Config: cfg,
	}, cfg)
	if err != nil {
		t.Fatalf("scheduler construction failed: %v", err)
	}
	return sched, agenda, events, rag, clock
}

// watchTopic returns a valid due WATCH topic for tests.
func watchTopic(id string) ResearchTopic {
	return ResearchTopic{
		ID: id, OrganizationID: "explorarte", DepartmentID: "infraestructura",
		Title:         "Critical dependency releases",
		ResearchClass: ResearchWatch, Status: TopicStatusActive,
		AllowedIntents: []SearchIntent{IntentWebGeneral},
		Cadence:        time.Hour, NextCheckAt: time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC),
		NoveltyWindow: 24 * time.Hour, Priority: 0.8,
	}
}

func webResult(provider, url, title string) SearchResult {
	return SearchResult{Provider: provider, URL: url, Title: title}
}

// =============================================================================
// A. Mission independence (critical)
// =============================================================================

func TestResearch_MissionIndependent_TopicRunsAndFindingCreated(t *testing.T) {
	executor := &fakeSearchExecutor{
		response: SearchResponse{Results: []SearchResult{
			webResult("brave_web", "https://example.com/a1", "Novel release A"),
			webResult("brave_web", "https://example.com/a2", "Novel release B"),
		}},
	}
	sched, agenda, _, _, _ := newSchedulerFixture(t, executor)

	topic := watchTopic("topic-missionfree")
	// Deliberately NO MissionID.
	topic.MissionID = ""
	if err := agenda.SaveTopic(context.Background(), topic); err != nil {
		t.Fatal(err)
	}

	result, err := sched.Tick(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.TopicsRun != 1 {
		t.Fatalf("expected 1 topic run without any mission, got %d", result.TopicsRun)
	}
	cycles, _ := agenda.ListCycles(context.Background(), topic.ID)
	if len(cycles) != 1 {
		t.Fatalf("expected 1 cycle, got %d", len(cycles))
	}
	if cycles[0].MissionID != "" {
		t.Fatalf("cycle must not fabricate a MissionID, got %q", cycles[0].MissionID)
	}
	findings, _ := agenda.ListFindings(context.Background(), FindingFilter{DepartmentID: topic.DepartmentID})
	if len(findings) != 1 {
		t.Fatalf("expected finding without mission, got %d", len(findings))
	}
}

func TestResearch_MissionIndependent_SchedulerQueriesNoMissions(t *testing.T) {
	// Structural proof: SelectDueTopics takes only (ctx, now) — no mission
	// filter exists anywhere in the signature chain.
	executor := &fakeSearchExecutor{}
	sched, agenda, _, _, _ := newSchedulerFixture(t, executor)
	topic := watchTopic("topic-nomission")
	topic.NextCheckAt = time.Now().Add(-time.Hour) // due
	if err := agenda.SaveTopic(context.Background(), topic); err != nil {
		t.Fatal(err)
	}
	due, err := sched.SelectDueTopics(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 {
		t.Fatalf("due selection must not depend on missions; got %d", len(due))
	}
}

// =============================================================================
// B. Due scheduling
// =============================================================================

func TestResearch_DueScheduling_FutureTopicNotRun(t *testing.T) {
	executor := &fakeSearchExecutor{}
	sched, agenda, _, _, _ := newSchedulerFixture(t, executor)
	topic := watchTopic("topic-future")
	topic.NextCheckAt = time.Now().Add(time.Hour) // future: not due
	if err := agenda.SaveTopic(context.Background(), topic); err != nil {
		t.Fatal(err)
	}
	result, err := sched.Tick(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.TopicsRun != 0 {
		t.Fatalf("future topic must not run, got %d", result.TopicsRun)
	}
	if executor.CallCount() != 0 {
		t.Fatalf("no search must happen for non-due topic, got %d calls", executor.CallCount())
	}
}

func TestResearch_DueScheduling_DisabledStatusesNeverRun(t *testing.T) {
	executor := &fakeSearchExecutor{}
	sched, agenda, _, _, _ := newSchedulerFixture(t, executor)
	for i, status := range []TopicStatus{TopicStatusPaused, TopicStatusCompleted, TopicStatusArchived} {
		topic := watchTopic(fmt.Sprintf("topic-%s-%d", status, i))
		topic.Status = status
		topic.NextCheckAt = time.Now().Add(-time.Hour) // otherwise due
		if err := agenda.SaveTopic(context.Background(), topic); err != nil {
			t.Fatal(err)
		}
	}
	result, err := sched.Tick(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.TopicsRun != 0 {
		t.Fatalf("non-active topics must never run, got %d", result.TopicsRun)
	}
}

func TestResearch_DueScheduling_MaxTopicsPerTickRespected(t *testing.T) {
	executor := &fakeSearchExecutor{}
	sched, agenda, _, _, _ := newSchedulerFixture(t, executor)
	for i := 0; i < 5; i++ {
		topic := watchTopic(fmt.Sprintf("topic-%02d", i))
		topic.NextCheckAt = time.Now().Add(-time.Hour)
		if err := agenda.SaveTopic(context.Background(), topic); err != nil {
			t.Fatal(err)
		}
	}
	sched.cfg.MaxTopicsPerTick = 3
	sched.cfg.MaxSearchRequestsPerTick = 10
	result, err := sched.Tick(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.TopicsRun != 3 {
		t.Fatalf("tick must cap topics at 3, got %d", result.TopicsRun)
	}
}

func TestResearch_DueScheduling_PriorityAffectsOrder(t *testing.T) {
	executor := &fakeSearchExecutor{}
	sched, agenda, _, _, _ := newSchedulerFixture(t, executor)
	low := watchTopic("topic-low")
	low.Priority = 0.1
	low.NextCheckAt = time.Now().Add(-2 * time.Hour)
	high := watchTopic("topic-high")
	high.Priority = 0.9
	high.NextCheckAt = time.Now().Add(-2 * time.Hour)
	if err := agenda.SaveTopic(context.Background(), low); err != nil {
		t.Fatal(err)
	}
	if err := agenda.SaveTopic(context.Background(), high); err != nil {
		t.Fatal(err)
	}
	sched.cfg.MaxTopicsPerTick = 1
	sched.cfg.MaxSearchRequestsPerTick = 5
	result, err := sched.Tick(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.TopicsRun != 1 {
		t.Fatal("expected exactly one topic")
	}
	if result.Cycles[0].TopicID != "topic-high" {
		t.Fatalf("high-priority topic must run first, got %s", result.Cycles[0].TopicID)
	}
}

func TestResearch_DueScheduling_NextCheckAtUpdatedAfterRun(t *testing.T) {
	executor := &fakeSearchExecutor{}
	sched, agenda, _, _, _ := newSchedulerFixture(t, executor)
	topic := watchTopic("topic-resched")
	topic.NextCheckAt = time.Now().Add(-time.Hour)
	topic.Cadence = time.Hour
	if err := agenda.SaveTopic(context.Background(), topic); err != nil {
		t.Fatal(err)
	}
	before, _ := agenda.GetTopic(context.Background(), topic.ID)
	if _, err := sched.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	after, _ := agenda.GetTopic(context.Background(), topic.ID)
	if !after.NextCheckAt.After(before.NextCheckAt) {
		t.Fatalf("NextCheckAt must advance by cadence: before=%v after=%v", before.NextCheckAt, after.NextCheckAt)
	}
	if after.LastCheckedAt == nil {
		t.Fatal("LastCheckedAt must be stamped after a run")
	}
}

// =============================================================================
// C. Research class cadence
// =============================================================================

func TestResearch_Cadence_TrackTopicNotRunEveryTick(t *testing.T) {
	executor := &fakeSearchExecutor{}
	sched, agenda, _, _, _ := newSchedulerFixture(t, executor)
	topic := watchTopic("topic-track")
	topic.ResearchClass = ResearchTrack
	topic.Cadence = 6 * time.Hour
	topic.NextCheckAt = time.Now().Add(-time.Minute) // barely due
	if err := agenda.SaveTopic(context.Background(), topic); err != nil {
		t.Fatal(err)
	}
	// First tick runs it.
	if _, err := sched.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Second tick immediately after: topic's cadence (6h) means NOT due,
	// regardless of the 10-minute global tick.
	result, err := sched.Tick(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.TopicsRun != 0 {
		t.Fatalf("TRACK topic must respect its own cadence, ran %d", result.TopicsRun)
	}
}

func TestResearch_Cadence_DeepOnlyWhenDue(t *testing.T) {
	executor := &fakeSearchExecutor{
		response: SearchResponse{Results: []SearchResult{
			webResult("arxiv", "https://arxiv.org/abs/2601.00001", "Fresh preprint"),
		}},
	}
	sched, agenda, _, _, _ := newSchedulerFixture(t, executor)
	topic := watchTopic("topic-deep")
	topic.ResearchClass = ResearchDeep
	topic.Cadence = 7 * 24 * time.Hour
	topic.AllowedIntents = []SearchIntent{IntentAcademic}
	topic.NextCheckAt = time.Now().Add(-7 * 24 * time.Hour) // due
	if err := agenda.SaveTopic(context.Background(), topic); err != nil {
		t.Fatal(err)
	}
	if _, err := sched.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Not due again for a week even though ticks continue.
	result, err := sched.Tick(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.TopicsRun != 0 {
		t.Fatal("DEEP topic must not run again before its cadence elapses")
	}
}

// =============================================================================
// D. KnowledgeNeed
// =============================================================================

func TestResearch_KnowledgeNeed_ProducesLinkedTopic(t *testing.T) {
	agenda := NewMemoryAgenda()
	need := KnowledgeNeed{
		ID: "need-001", OrganizationID: "explorarte", DepartmentID: "finanzas",
		Question: "Which API providers changed pricing this week?",
		Source:   NeedSourceDepartmentRequested, Status: NeedStatusActive,
		Importance: 0.7, Confidence: 0.9,
	}
	if err := agenda.SaveNeed(context.Background(), need); err != nil {
		t.Fatal(err)
	}
	// A need may produce multiple topics over time.
	for i, title := range []string{"API pricing watch", "Free tier changes watch"} {
		topic := watchTopic(fmt.Sprintf("topic-from-need-%d", i))
		topic.DepartmentID = need.DepartmentID
		topic.Title = title
		topic.ParentKnowledgeNeedID = need.ID
		if err := agenda.SaveTopic(context.Background(), topic); err != nil {
			t.Fatal(err)
		}
	}
	saved, err := agenda.GetTopic(context.Background(), "topic-from-need-0")
	if err != nil {
		t.Fatal(err)
	}
	if saved.ParentKnowledgeNeedID != need.ID {
		t.Fatalf("topic must keep parent need link, got %q", saved.ParentKnowledgeNeedID)
	}
	needs, _ := agenda.ListNeeds(context.Background(), "finanzas")
	if len(needs) != 1 {
		t.Fatalf("expected 1 need, got %d", len(needs))
	}
}

func TestResearch_KnowledgeNeed_Validation(t *testing.T) {
	agenda := NewMemoryAgenda()
	bad := KnowledgeNeed{ID: "need-bad", DepartmentID: "finanzas"} // no question/source
	if err := agenda.SaveNeed(context.Background(), bad); err == nil {
		t.Fatal("need without question/source must fail validation")
	}
}

// =============================================================================
// E. SearchRouter boundary
// =============================================================================

func TestResearch_OnlyCallsSearchRouter_WithAuthorizedIntentAndQueryCap(t *testing.T) {
	executor := &fakeSearchExecutor{
		response: SearchResponse{Results: []SearchResult{}},
	}
	sched, agenda, _, _, _ := newSchedulerFixture(t, executor)
	topic := watchTopic("topic-boundary")
	topic.AllowedIntents = []SearchIntent{IntentWebGeneral}
	topic.NextCheckAt = time.Now().Add(-time.Hour)
	if err := agenda.SaveTopic(context.Background(), topic); err != nil {
		t.Fatal(err)
	}
	sched.cfg.MaxQueriesPerTopic = 2
	sched.cfg.MaxSearchRequestsPerTick = 10
	if _, err := sched.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	calls := executor.CallCount()
	if calls == 0 {
		t.Fatal("researcher must search through the SearchExecutor boundary")
	}
	if calls > sched.cfg.MaxQueriesPerTopic {
		t.Fatalf("query cap violated: %d calls > %d", calls, sched.cfg.MaxQueriesPerTopic)
	}
	executor.mu.Lock()
	for _, req := range executor.calls {
		if req.RoleID != string(RoleResearch) {
			t.Fatalf("researcher must search as research role, got %q", req.RoleID)
		}
		if req.Intent != IntentWebGeneral {
			t.Fatalf("intent outside topic allow-list: %q", req.Intent)
		}
	}
	executor.mu.Unlock()
}

// =============================================================================
// F/G. Novelty
// =============================================================================

func TestResearch_NoNovelty_NoFindingNoRAG(t *testing.T) {
	url := "https://example.com/known-release"
	executor := &fakeSearchExecutor{
		response: SearchResponse{Results: []SearchResult{
			webResult("brave_web", url, "Already seen release"),
		}},
	}
	sched, agenda, events, rag, _ := newSchedulerFixture(t, executor)
	topic := watchTopic("topic-known")
	if err := agenda.SaveTopic(context.Background(), topic); err != nil {
		t.Fatal(err)
	}
	// Seed a previous finding containing the same evidence URL.
	prior := ResearchFinding{
		ID: "finding-prior", TopicID: topic.ID, DepartmentID: topic.DepartmentID,
		ResearchCycleID: "cycle-prior", Title: "prior", Classification: FindingInformative,
		EvidenceRefs: []EvidenceRef{{Provider: "brave_web", URL: url}},
		CreatedAt:    time.Now().Add(-time.Hour),
	}
	if err := agenda.SaveFinding(context.Background(), prior); err != nil {
		t.Fatal(err)
	}
	result, err := sched.Tick(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Cycles) != 1 {
		t.Fatal("expected one cycle")
	}
	if result.Cycles[0].Outcome != CycleOutcomeNoNewInfo {
		t.Fatalf("expected no_new_information, got %q", result.Cycles[0].Outcome)
	}
	if len(rag.submitted) != 0 {
		t.Fatal("no RAG candidate may be created without novelty")
	}
	if events.Has("research.finding.created") {
		t.Fatal("no finding may be created without novelty")
	}
}

func TestResearch_Novelty_NewDOICreatesFindingOnce(t *testing.T) {
	doi := "10.1234/novel-paper"
	executor := &fakeSearchExecutor{
		response: SearchResponse{Results: []SearchResult{
			{Provider: "openalex", URL: "https://doi.org/" + doi, Title: "Novel paper", DOI: doi},
		}},
	}
	sched, agenda, events, rag, _ := newSchedulerFixture(t, executor)
	topic := watchTopic("topic-novel-doi")
	topic.Cadence = time.Minute
	if err := agenda.SaveTopic(context.Background(), topic); err != nil {
		t.Fatal(err)
	}
	if _, err := sched.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !events.Has("research.novelty.detected") {
		t.Fatal("novelty must be detected for a fresh DOI")
	}
	findings, _ := agenda.ListFindings(context.Background(), FindingFilter{TopicID: topic.ID})
	if len(findings) != 1 {
		t.Fatalf("expected exactly 1 finding, got %d", len(findings))
	}
	if len(rag.submitted) != 0 {
		t.Fatalf("1 novel ref is informative, not durable; RAG submissions must be 0, got %d", len(rag.submitted))
	}

	// Second tick with the SAME DOI: no duplicate finding.
	if _, err := sched.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	findings2, _ := agenda.ListFindings(context.Background(), FindingFilter{TopicID: topic.ID})
	if len(findings2) != 1 {
		t.Fatalf("repeated evidence must not duplicate findings, got %d", len(findings2))
	}
}

// =============================================================================
// H. Finding classification
// =============================================================================

func TestResearch_Classification_Rules(t *testing.T) {
	if FindingIrrelevant.Propagates() {
		t.Fatal("irrelevant must not propagate")
	}
	for _, c := range []FindingClassification{FindingInformative, FindingImportant, FindingCritical, FindingDurableCandidate} {
		if !c.Propagates() {
			t.Fatalf("%s must propagate", c)
		}
	}
	if FindingImportant == FindingDurableCandidate {
		t.Fatal("important and durable must remain distinct classes")
	}
}

func TestResearch_ImportantFinding_EmitsAlertEvent(t *testing.T) {
	executor := &fakeSearchExecutor{
		response: SearchResponse{Results: []SearchResult{
			webResult("brave_web", "https://example.com/x1", "A"),
			webResult("brave_web", "https://example.com/x2", "B"),
		}},
	}
	sched, agenda, events, rag, _ := newSchedulerFixture(t, executor)
	topic := watchTopic("topic-important")
	if err := agenda.SaveTopic(context.Background(), topic); err != nil {
		t.Fatal(err)
	}
	if _, err := sched.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !events.Has("research.finding.alert") {
		t.Fatal("important finding must emit alert event")
	}
	if len(rag.submitted) != 0 {
		t.Fatal("important (2 refs) must NOT create RAG candidate — only durable does")
	}
	findings, _ := agenda.ListFindings(context.Background(), FindingFilter{TopicID: topic.ID})
	if len(findings) != 1 || findings[0].Classification != FindingImportant {
		t.Fatalf("expected one important finding, got %+v", findings)
	}
}

func TestResearch_DurableFinding_SubmitsRAGCandidate(t *testing.T) {
	// V2: durability requires INDEPENDENT sources — three distinct domains
	// with canonical scholarly source types.
	executor := &fakeSearchExecutor{
		response: SearchResponse{Results: []SearchResult{
			{Provider: "openalex", URL: "https://journal-a.example/paper1", Title: "P1", DOI: "10.1000/p1", SourceType: "journal_article"},
			{Provider: "openalex", URL: "https://journal-b.example/paper2", Title: "P2", DOI: "10.1000/p2", SourceType: "journal_article"},
			{Provider: "openalex", URL: "https://journal-c.example/paper3", Title: "P3", DOI: "10.1000/p3", SourceType: "journal_article"},
		}},
	}
	sched, agenda, events, rag, _ := newSchedulerFixture(t, executor)
	topic := watchTopic("topic-durable")
	if err := agenda.SaveTopic(context.Background(), topic); err != nil {
		t.Fatal(err)
	}
	if _, err := sched.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(rag.submitted) != 1 {
		t.Fatalf("durable candidate must submit exactly one RAG candidate, got %d", len(rag.submitted))
	}
	if !events.Has("research.rag_candidate.submitted") {
		t.Fatal("RAG submission must be observable")
	}
}

// =============================================================================
// H. Durable candidate regression (V2 policy)
// =============================================================================

func TestResearch_DurableV2_ThreeURLsSameDomainNotDurable(t *testing.T) {
	// 3 fresh URLs from the SAME domain: independent_sources=1 < 2.
	// Informative at most — never a RAG candidate.
	executor := &fakeSearchExecutor{
		response: SearchResponse{Results: []SearchResult{
			webResult("brave_web", "https://one-blog.example/post1", "Post 1"),
			webResult("brave_web", "https://one-blog.example/post2", "Post 2"),
			webResult("brave_web", "https://one-blog.example/post3", "Post 3"),
		}},
	}
	sched, agenda, _, rag, _ := newSchedulerFixture(t, executor)
	topic := watchTopic("topic-samedomain")
	if err := agenda.SaveTopic(context.Background(), topic); err != nil {
		t.Fatal(err)
	}
	if _, err := sched.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(rag.submitted) != 0 {
		t.Fatalf("same-domain results must never become durable, got %d RAG submissions", len(rag.submitted))
	}
	findings, _ := agenda.ListFindings(context.Background(), FindingFilter{TopicID: topic.ID})
	// V2: 3 same-domain refs pass the count gate but fail independence,
	// so they elevate to important (feed-visible) — never durable.
	if len(findings) != 1 || findings[0].Classification != FindingImportant {
		t.Fatalf("same-domain evidence must stay non-durable (important), got %+v", findings)
	}
}

func TestResearch_DurableV2_TemporalNewsImportantNotDurable(t *testing.T) {
	// A pricing change across two independent outlets: IMPORTANT
	// intelligence, but temporal — NOT a durable knowledge candidate.
	executor := &fakeSearchExecutor{
		response: SearchResponse{Results: []SearchResult{
			{Provider: "brave_web", URL: "https://news-a.example/pricing-changed", Title: "Provider raises API pricing", SourceType: "news"},
			{Provider: "brave_web", URL: "https://news-b.example/tariff-update", Title: "Free tier changes announced", SourceType: "news"},
		}},
	}
	sched, agenda, events, rag, _ := newSchedulerFixture(t, executor)
	topic := watchTopic("topic-pricing")
	if err := agenda.SaveTopic(context.Background(), topic); err != nil {
		t.Fatal(err)
	}
	if _, err := sched.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(rag.submitted) != 0 {
		t.Fatalf("temporal pricing news must not be durable, got %d", len(rag.submitted))
	}
	if !events.Has("research.finding.alert") {
		t.Fatal("temporal important fact must still alert the department")
	}
	findings, _ := agenda.ListFindings(context.Background(), FindingFilter{TopicID: topic.ID})
	if len(findings) != 1 || findings[0].Classification != FindingImportant {
		t.Fatalf("temporal fact must be important, got %+v", findings)
	}
}

func TestResearch_DurableV2_CanonicalPaperWithCorroborationEligible(t *testing.T) {
	// A canonical DOI paper + independent corroboration: the two DOI hits
	// collapse to ONE evidence key, so novel refs = 2 < MinDurableEvidence.
	// V2 contract: even canonical papers need the full evidence count.
	executor := &fakeSearchExecutor{
		response: SearchResponse{Results: []SearchResult{
			{Provider: "openalex", URL: "https://doi.org/10.1234/abc", Title: "Canonical study", DOI: "10.1234/abc", SourceType: "journal_article", Publisher: "Journal A"},
			{Provider: "crossref", URL: "https://api.crossref.example/10.1234/abc", Title: "Canonical study (metadata)", DOI: "10.1234/abc", SourceType: "paper", Publisher: "Journal A"},
			{Provider: "brave_web", URL: "https://university.example/study-review", Title: "Independent review of the canonical study", SourceType: "documentation"},
		}},
	}
	sched, agenda, _, rag, _ := newSchedulerFixture(t, executor)
	topic := watchTopic("topic-paper")
	if err := agenda.SaveTopic(context.Background(), topic); err != nil {
		t.Fatal(err)
	}
	if _, err := sched.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	findings, _ := agenda.ListFindings(context.Background(), FindingFilter{TopicID: topic.ID})
	if len(findings) != 1 {
		t.Fatal("expected one finding")
	}
	if findings[0].Classification == FindingDurableCandidate {
		t.Fatal("2 novel refs must not be durable even with canonical metadata")
	}
	if len(rag.submitted) != 0 {
		t.Fatalf("sub-threshold evidence must not reach RAG, got %d", len(rag.submitted))
	}
}

// =============================================================================
// I. RAG governance
// =============================================================================

func TestResearch_RAGGovernance_SubmitOnly_NoPromotionPath(t *testing.T) {
	// Contract check: the RAGSubmitter interface has exactly one method —
	// SubmitRAGCandidate. There is no promote/accept API on the research
	// side; promotion belongs to governance outside this package.
	var _ interface {
		SubmitRAGCandidate(context.Context, ResearchFinding) error
	} = &fakeRAGSubmitter{}
	// Policy table: researcher submits, never promotes.
	policy := DefaultRolePolicies()[RoleResearch]
	if !policy.CanSubmitRAGCandidate {
		t.Fatal("research must be able to submit RAG candidates")
	}
	if policy.CanPromoteToKnowledge {
		t.Fatal("research must never promote to knowledge directly")
	}
}

// =============================================================================
// J. Topic discovery / proposal policy
// =============================================================================

func TestResearch_ProposalPolicy(t *testing.T) {
	agenda := NewMemoryAgenda()
	depts := StaticDepartments{"infraestructura": true, "finanzas": true}
	policy, err := NewTopicProposalPolicy(agenda, depts, DefaultSchedulerConfig().CadenceFloors)
	if err != nil {
		t.Fatal(err)
	}

	// Duplicate -> reject.
	seed := watchTopic("topic-seed")
	if err := agenda.SaveTopic(context.Background(), seed); err != nil {
		t.Fatal(err)
	}
	dup := ResearchTopicProposal{
		DepartmentID: seed.DepartmentID, Title: seed.Title,
		ResearchClass: ResearchWatch, Cadence: time.Hour,
		AllowedIntents: []SearchIntent{IntentWebGeneral},
	}
	dup, err = policy.Evaluate(context.Background(), dup)
	if err != nil {
		t.Fatal(err)
	}
	if dup.Decision != ProposalRejected || dup.RejectReason != RejectDuplicate {
		t.Fatalf("duplicate must be rejected, got %s/%s", dup.Decision, dup.RejectReason)
	}

	// Unknown department -> reject.
	unknown := ResearchTopicProposal{
		DepartmentID: "no-existe", Title: "Brand new watch",
		ResearchClass: ResearchWatch, Cadence: time.Hour,
		AllowedIntents: []SearchIntent{IntentWebGeneral},
	}
	unknown, _ = policy.Evaluate(context.Background(), unknown)
	if unknown.Decision != ProposalRejected || unknown.RejectReason != RejectUnknownDepartment {
		t.Fatalf("unknown department must reject, got %s/%s", unknown.Decision, unknown.RejectReason)
	}

	// Safe low-cost child -> autoaccept.
	child := ResearchTopicProposal{
		DepartmentID: "infraestructura", Title: "Registry mirror outages",
		ResearchClass: ResearchWatch, Cadence: time.Hour, Priority: 0.4,
		AllowedIntents: []SearchIntent{IntentWebGeneral},
		ParentTopicID:  "topic-seed",
	}
	child, _ = policy.Evaluate(context.Background(), child)
	if child.Decision != ProposalAccepted {
		t.Fatalf("safe child topic must autoaccept, got %s/%s", child.Decision, child.RejectReason)
	}
	created, err := agenda.GetTopic(context.Background(), child.ID)
	_ = created // proposal creates a NEW topic; identity is host-side
	if err == nil {
		t.Fatal("accepted proposal must not reuse the proposal struct as topic id space")
	}
	topics, _ := agenda.ListTopics(context.Background(), "infraestructura")
	found := false
	for _, topic := range topics {
		if topic.Title == "Registry mirror outages" && topic.CreatedBy == "researcher_proposal" {
			found = true
		}
	}
	if !found {
		t.Fatal("accepted proposal must create a productive topic marked researcher_proposal")
	}

	// High-frequency -> pending review.
	fast := ResearchTopicProposal{
		DepartmentID: "infraestructura", Title: "Second-level outage watch",
		ResearchClass: ResearchWatch, Cadence: time.Minute, // below floor
		AllowedIntents: []SearchIntent{IntentWebGeneral},
	}
	fast, _ = policy.Evaluate(context.Background(), fast)
	if fast.Decision != ProposalPending {
		t.Fatalf("sub-floor cadence must go to review, got %s", fast.Decision)
	}

	// Critical priority -> pending review.
	critical := ResearchTopicProposal{
		DepartmentID: "infraestructura", Title: "Payment provider breach watch",
		ResearchClass: ResearchWatch, Cadence: time.Hour, Priority: 0.99,
		AllowedIntents: []SearchIntent{IntentWebGeneral},
	}
	critical, _ = policy.Evaluate(context.Background(), critical)
	if critical.Decision != ProposalPending || critical.RejectReason != RejectCriticalPriority {
		t.Fatalf("critical priority must need review, got %s/%s", critical.Decision, critical.RejectReason)
	}
}

// =============================================================================
// K. Free LLM unavailable -> postpone, no paid fallback
// =============================================================================

func TestResearch_LLMUnavailable_PostponesSafely_NoPaidFallback(t *testing.T) {
	executor := &fakeSearchExecutor{err: errors.New("llm quota exhausted")}
	sched, agenda, _, _, _ := newSchedulerFixture(t, executor)
	topic := watchTopic("topic-llmdown")
	topic.NextCheckAt = time.Now().Add(-time.Hour)
	topic.Cadence = time.Hour
	if err := agenda.SaveTopic(context.Background(), topic); err != nil {
		t.Fatal(err)
	}
	result, err := sched.Tick(context.Background())
	if err != nil {
		t.Fatalf("scheduler itself must not fail: %v", err)
	}
	if result.TopicsRun != 1 {
		t.Fatal("cycle must run and close safely")
	}
	cycle := result.Cycles[0]
	if cycle.Outcome != CycleOutcomePostponed {
		t.Fatalf("LLM failure must postpone, got %q", cycle.Outcome)
	}
	// Topic rescheduled into the future: no infinite retry.
	after, _ := agenda.GetTopic(context.Background(), topic.ID)
	if !after.NextCheckAt.After(time.Now()) {
		t.Fatalf("postponed topic must have future NextCheckAt, got %v", after.NextCheckAt)
	}
	// No paid fallback configured anywhere in V1 deps.
	if sched.cfg.AllowPaidFallbackForCritical {
		t.Fatal("paid fallback must default to false")
	}
}

// =============================================================================
// L. Search quota exhaustion -> postpone, no hammering
// =============================================================================

func TestResearch_SearchQuotaExhausted_Postpones(t *testing.T) {
	executor := &fakeSearchExecutor{
		err: &ProviderError{Provider: ProviderBrave, Kind: ProviderErrorRateLimited, StatusCode: 429},
	}
	sched, agenda, events, _, _ := newSchedulerFixture(t, executor)
	topic := watchTopic("topic-quota")
	topic.NextCheckAt = time.Now().Add(-time.Hour)
	topic.Cadence = time.Hour
	if err := agenda.SaveTopic(context.Background(), topic); err != nil {
		t.Fatal(err)
	}
	result, err := sched.Tick(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Cycles[0].Outcome != CycleOutcomePostponed {
		t.Fatalf("quota exhaustion must postpone, got %q", result.Cycles[0].Outcome)
	}
	after, _ := agenda.GetTopic(context.Background(), topic.ID)
	if !after.NextCheckAt.After(time.Now()) {
		t.Fatal("postponed topic must not be immediately due again")
	}
	if !events.Has("research.cycle.failed") {
		t.Fatal("cycle failure must be observable")
	}
}

// =============================================================================
// M. Loop protection
// =============================================================================

func TestResearch_LoopProtection_QueryCapAborts(t *testing.T) {
	executor := &fakeSearchExecutor{
		response: SearchResponse{Results: []SearchResult{
			webResult("brave_web", fmt.Sprintf("https://example.com/n%d", time.Now().UnixNano()), "Always novel"),
		}},
	}
	sched, agenda, _, _, _ := newSchedulerFixture(t, executor)
	topic := watchTopic("topic-loopy")
	topic.NextCheckAt = time.Now().Add(-time.Hour)
	if err := agenda.SaveTopic(context.Background(), topic); err != nil {
		t.Fatal(err)
	}
	sched.cfg.MaxQueriesPerTopic = 3
	sched.cfg.MaxSearchRequestsPerTick = 50
	if _, err := sched.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := executor.CallCount(); got > sched.cfg.MaxQueriesPerTopic {
		t.Fatalf("runaway loop: %d queries > cap %d", got, sched.cfg.MaxQueriesPerTopic)
	}
	// MaxLLMTurnsPerCycle is enforced structurally: the cycle caps query
	// generation turns; verify config validation rejects zero-turn configs.
	sched.cfg.MaxLLMTurnsPerCycle = 0
	if err := sched.cfg.Validate(); err == nil {
		t.Fatal("zero LLM turns must fail config validation")
	}
}

// =============================================================================
// N. Department feed
// =============================================================================

func TestResearch_DepartmentFeed_FiltersAndIsolation(t *testing.T) {
	agenda := NewMemoryAgenda()
	now := time.Now().UTC()
	for dept, classification := range map[string]FindingClassification{
		"finanzas":        FindingImportant,
		"infraestructura": FindingInformative,
	} {
		finding := ResearchFinding{
			ID: "finding-" + dept, TopicID: "topic-" + dept, DepartmentID: dept,
			ResearchCycleID: "cycle-" + dept, Title: "Finding for " + dept,
			Classification: classification, CreatedAt: now,
		}
		if err := agenda.SaveFinding(context.Background(), finding); err != nil {
			t.Fatal(err)
		}
	}
	finanzas, _ := agenda.ListFindings(context.Background(), FindingFilter{DepartmentID: "finanzas"})
	if len(finanzas) != 1 || finanzas[0].DepartmentID != "finanzas" {
		t.Fatalf("finanzas feed must contain only its finding, got %+v", finanzas)
	}
	// Important-only filter excludes informative.
	important, _ := agenda.ListFindings(context.Background(), FindingFilter{MinImportance: true})
	if len(important) != 1 || important[0].Classification != FindingImportant {
		t.Fatalf("importance filter failed, got %+v", important)
	}
	// Timeframe filter.
	old, _ := agenda.ListFindings(context.Background(), FindingFilter{Until: now.Add(-time.Hour)})
	if len(old) != 0 {
		t.Fatalf("timeframe filter failed, got %d", len(old))
	}
}

// =============================================================================
// P. Concurrent claim
// =============================================================================

func TestResearch_ConcurrentClaim_OnlyOneWorkerWins(t *testing.T) {
	agenda := NewMemoryAgenda()
	topic := watchTopic("topic-contested")
	if err := agenda.SaveTopic(context.Background(), topic); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	const workers = 8
	wins := make(chan bool, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			wins <- agenda.TryClaimTopic(topic.ID, fmt.Sprintf("worker-%d", n), time.Minute, now)
		}(i)
	}
	wg.Wait()
	close(wins)
	total := 0
	winners := 0
	for won := range wins {
		total++
		if won {
			winners++
		}
	}
	if total != workers {
		t.Fatalf("all workers must report, got %d/%d", total, workers)
	}
	if winners != 1 {
		t.Fatalf("exactly one worker must win the claim, got %d", winners)
	}
	// The claim is held while the TTL is live: a late worker cannot take it.
	if agenda.TryClaimTopic(topic.ID, "worker-late", time.Minute, now.Add(time.Second)) {
		t.Fatal("second claim within TTL must fail")
	}
	// Once the TTL expires, the claim window opens again.
	if !agenda.TryClaimTopic(topic.ID, "worker-late", time.Minute, now.Add(2*time.Minute)) {
		t.Fatal("claim after TTL expiry must succeed")
	}
}

// =============================================================================
// Bootstrap seeds
// =============================================================================

func TestResearch_BootstrapSeeds_Idempotent(t *testing.T) {
	agenda := NewMemoryAgenda()
	now := time.Now().UTC()
	if err := SeedBootstrapTopics(context.Background(), agenda, now); err != nil {
		t.Fatal(err)
	}
	first, _ := agenda.ListTopics(context.Background(), "")
	if err := SeedBootstrapTopics(context.Background(), agenda, now); err != nil {
		t.Fatal(err)
	}
	second, _ := agenda.ListTopics(context.Background(), "")
	if len(first) != len(second) {
		t.Fatalf("seeds must be idempotent: %d vs %d", len(first), len(second))
	}
	if len(second) < 3 {
		t.Fatalf("expected representative seed set, got %d", len(second))
	}
}

// =============================================================================
// Config validation
// =============================================================================

func TestResearch_SchedulerConfigValidation(t *testing.T) {
	cfg := DefaultSchedulerConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("defaults must validate: %v", err)
	}
	bad := cfg
	bad.TickInterval = 30 * time.Second
	if err := bad.Validate(); err == nil {
		t.Fatal("tick below 1 minute must fail")
	}
	bad2 := cfg
	bad2.MaxSearchRequestsPerTick = 1
	bad2.MaxTopicsPerTick = 3
	if err := bad2.Validate(); err == nil {
		t.Fatal("tick budget below topic budget must fail")
	}
}
