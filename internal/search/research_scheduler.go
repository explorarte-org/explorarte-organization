package search

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// =============================================================================
// Autonomous Research Scheduler V1
//
// Deterministic, testable, LLM-free selection:
//
//	every TickInterval
//	  → find topics due (NextCheckAt <= now)
//	  → score deterministically
//	  → run at most MaxTopicsPerTick cycles within the search budget
//	  → update NextCheckAt per each topic's own cadence
//
// The tick NEVER means "search every topic every 10 minutes". The global tick
// only bounds HOW OFTEN due topics are evaluated; each topic's own cadence
// and class govern whether it is actually due.
//
// All external research passes through the SearchRouter boundary
// (SearchExecutor). The researcher never touches provider adapters.
// =============================================================================

// SearchExecutor is the ONLY search boundary the researcher may use. *Router
// satisfies it; tests inject fakes.
type SearchExecutor interface {
	Search(ctx context.Context, req SearchRequest) (SearchResponse, error)
}

// Compile-time proof that *Router is a valid search boundary.
var _ SearchExecutor = (*Router)(nil)

// QueryGenerator produces concrete queries for a topic. The production
// implementation is backed by the OpenRouter Free researcher LLM; tests use
// deterministic fakes. The generator NEVER sees credentials, never picks
// providers, and never bypasses the SearchRouter.
type QueryGenerator interface {
	GenerateQueries(ctx context.Context, topic ResearchTopic, maxQueries int) ([]string, error)
}

// DeterministicQueryGenerator is the always-available V1 generator: it
// derives queries from the topic title (plus the date-derived freshness
// suffix for WATCH topics) without any LLM. Used when no LLM is wired and in
// tests that need full determinism.
type DeterministicQueryGenerator struct{}

// GenerateQueries implements QueryGenerator deterministically.
func (DeterministicQueryGenerator) GenerateQueries(_ context.Context, topic ResearchTopic, maxQueries int) ([]string, error) {
	if strings.TrimSpace(topic.Title) == "" {
		return nil, fmt.Errorf("%w: topic title is required to generate queries", ErrInvalidRequest)
	}
	if maxQueries < 1 {
		maxQueries = 1
	}
	base := strings.TrimSpace(topic.Title)
	queries := []string{base}
	if topic.ResearchClass == ResearchWatch && maxQueries > 1 {
		// WATCH topics diversify with a recency-oriented phrasing.
		queries = append(queries, base+" latest news this week")
	}
	if maxQueries > 2 && topic.Description != "" {
		// A third query explores the description angle.
		queries = append(queries, topic.Description)
	}
	if len(queries) > maxQueries {
		queries = queries[:maxQueries]
	}
	return queries, nil
}

// ResearchEventSink receives safe observability events. Implementations must
// never log secrets; the scheduler only passes structural facts.
type ResearchEventSink interface {
	ResearchEvent(event string, fields map[string]any)
}

// RAGSubmitter submits durable candidates into the existing governance flow.
// The Researcher may SUBMIT only; promotion to Knowledge RAG is governance's
// exclusive power and has no code path here.
type RAGSubmitter interface {
	SubmitRAGCandidate(ctx context.Context, finding ResearchFinding) error
}

// SchedulerDeps wires the scheduler to its boundaries.
type SchedulerDeps struct {
	Search   SearchExecutor    // SearchRouter (required)
	Agenda   SchedulerAgenda   // required; MemoryAgenda or Postgres Store
	Claims   TopicClaimManager // optional; default memory claims when Agenda is *MemoryAgenda
	Evidence EvidenceIndex     // optional; default memory index when Agenda is *MemoryAgenda
	Queries  QueryGenerator    // optional; defaults to DeterministicQueryGenerator
	Findings *FindingPolicy    // optional; defaults to DefaultFindingPolicyConfig
	Events   ResearchEventSink // optional
	RAG      RAGSubmitter      // optional; required only if durable findings occur
	Clock    func() time.Time  // optional; defaults to time.Now
	Config   SchedulerConfig
}

// backoffForErrorClass maps a cycle error class to a bounded postponement
// delay. Quota exhaustion cools down much longer than transient failures;
// nothing here retries immediately and nothing here falls back to paid
// providers.
func backoffForErrorClass(errorClass string) time.Duration {
	switch {
	case strings.Contains(errorClass, "quota"):
		return 6 * time.Hour
	case strings.Contains(errorClass, "rate_limit"):
		return 20 * time.Minute
	case strings.Contains(errorClass, "llm"):
		return 30 * time.Minute
	default:
		return 10 * time.Minute
	}
}

// AutonomousResearchScheduler evaluates due topics every tick and runs
// bounded research cycles.
type AutonomousResearchScheduler struct {
	deps SchedulerDeps
	cfg  SchedulerConfig
}

// NewAutonomousResearchScheduler validates deps/config and returns a scheduler.
func NewAutonomousResearchScheduler(deps SchedulerDeps, cfg SchedulerConfig) (*AutonomousResearchScheduler, error) {
	if deps.Search == nil {
		return nil, fmt.Errorf("%w: scheduler requires a SearchExecutor", ErrInvalidRequest)
	}
	if deps.Agenda == nil {
		return nil, fmt.Errorf("%w: scheduler requires an agenda store", ErrInvalidRequest)
	}
	if cfg.TickInterval == 0 {
		cfg = DefaultSchedulerConfig()
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if memory, ok := deps.Agenda.(*MemoryAgenda); ok {
		if deps.Claims == nil {
			deps.Claims = MemoryClaimManager{Agenda: memory, TTL: cfg.TickInterval}
		}
		if deps.Evidence == nil {
			deps.Evidence = MemoryEvidenceIndex{Agenda: memory}
		}
	}
	if deps.Queries == nil {
		deps.Queries = DeterministicQueryGenerator{}
	}
	if deps.Findings == nil {
		policy, err := NewFindingPolicy(DefaultFindingPolicyConfig())
		if err != nil {
			return nil, err
		}
		deps.Findings = policy
	}
	if deps.Clock == nil {
		deps.Clock = func() time.Time { return time.Now().UTC() }
	}
	return &AutonomousResearchScheduler{deps: deps, cfg: cfg}, nil
}

// TickScore is the deterministic priority of a due topic for one tick.
type TickScore struct {
	Topic ResearchTopic
	Score float64
	Stale time.Duration
}

// scoreTopic computes:
//
//	score = base_priority + staleness_weight - recent_cycle_penalty
//
// staleness_weight grows linearly with how far past NextCheckAt the topic is
// (capped at 1.0 of extra weight per full cadence overdue). recent_cycle_penalty
// damps topics that ran very recently. Deterministic, explicable, no LLM.
func (s *AutonomousResearchScheduler) scoreTopic(topic ResearchTopic, now time.Time, recentCycles int) TickScore {
	stale := now.Sub(topic.NextCheckAt)
	if stale < 0 {
		stale = 0
	}
	stalenessWeight := 0.0
	if topic.Cadence > 0 {
		stalenessWeight = stale.Seconds() / topic.Cadence.Seconds()
		if stalenessWeight > 1.0 {
			stalenessWeight = 1.0
		}
	}
	recentPenalty := 0.0
	if recentCycles > 0 {
		recentPenalty = s.cfg.RecentCyclePenalty * float64(recentCycles)
	}
	score := topic.Priority + stalenessWeight - recentPenalty
	return TickScore{Topic: topic, Score: score, Stale: stale}
}

// SelectDueTopics returns due topics sorted by deterministic score, honoring
// MaxTopicsPerTick. Only ACTIVE topics are ever due (see ResearchTopic.Due).
func (s *AutonomousResearchScheduler) SelectDueTopics(ctx context.Context, now time.Time) ([]TickScore, error) {
	topics, err := s.deps.Agenda.ListTopics(ctx, "")
	if err != nil {
		return nil, err
	}
	scored := make([]TickScore, 0)
	for _, topic := range topics {
		if !topic.Due(now) {
			continue
		}
		recentCycles := s.countRecentCycles(topic.ID, now)
		scored = append(scored, s.scoreTopic(topic, now, recentCycles))
	}
	// Highest score first; ties broken by ID for full determinism.
	sort.Slice(scored, func(i, j int) bool {
		if scored[i].Score != scored[j].Score {
			return scored[i].Score > scored[j].Score
		}
		return scored[i].Topic.ID < scored[j].Topic.ID
	})
	if len(scored) > s.cfg.MaxTopicsPerTick {
		scored = scored[:s.cfg.MaxTopicsPerTick]
	}
	return scored, nil
}

// countRecentCycles counts cycles started within one cadence window of now.
func (s *AutonomousResearchScheduler) countRecentCycles(topicID string, now time.Time) int {
	cycles, err := s.deps.Agenda.ListCycles(context.Background(), topicID)
	if err != nil {
		return 0
	}
	count := 0
	for _, cycle := range cycles {
		if now.Sub(cycle.StartedAt) < s.recentWindow(topicID) {
			count++
		}
	}
	return count
}

// recentWindow bounds "recent" as one tick interval.
func (s *AutonomousResearchScheduler) recentWindow(_ string) time.Duration {
	return s.cfg.TickInterval
}

// TickResult summarizes one scheduler tick.
type TickResult struct {
	TickStartedAt time.Time
	TopicsRun     int
	Cycles        []ResearchCycle
	SearchesUsed  int
}

// Tick runs one scheduler evaluation: select due topics, run bounded cycles,
// update NextCheckAt. It never panics on individual cycle failure — a failed
// cycle is recorded and the topic postponed safely.
func (s *AutonomousResearchScheduler) Tick(ctx context.Context) (TickResult, error) {
	now := s.deps.Clock().UTC()
	result := TickResult{TickStartedAt: now}
	s.emit("research.tick.started", map[string]any{"at": now.Format(time.RFC3339)})

	due, err := s.SelectDueTopics(ctx, now)
	if err != nil {
		s.emit("research.tick.completed", map[string]any{"error_class": "select_failed"})
		return result, err
	}

	searchBudget := s.cfg.MaxSearchRequestsPerTick
	for _, candidate := range due {
		if ctx.Err() != nil {
			break
		}
		if result.TopicsRun >= s.cfg.MaxTopicsPerTick {
			break
		}
		if searchBudget <= 0 {
			// Budget exhausted: leave remaining topics due for the next tick.
			break
		}
		// Claim before running: with multiple scheduler instances the claim
		// window keeps two workers from executing the same due topic. A
		// worker that loses the race simply leaves the topic for its owner.
		claimed, err := s.deps.Claims.TryClaim(ctx, candidate.Topic.ID, "scheduler", s.cfg.TickInterval)
		if err != nil || !claimed {
			continue
		}
		cycle := s.runCycle(ctx, candidate.Topic, now, &searchBudget)
		_ = s.deps.Claims.Release(ctx, candidate.Topic.ID, "scheduler")
		result.Cycles = append(result.Cycles, cycle)
		result.TopicsRun++
	}

	result.SearchesUsed = s.cfg.MaxSearchRequestsPerTick - searchBudget
	s.emit("research.tick.completed", map[string]any{
		"topics_run":    result.TopicsRun,
		"searches_used": result.SearchesUsed,
	})
	return result, nil
}

// runCycle executes one research cycle for a topic: generate queries →
// SearchRouter → novelty → findings → events. Loop protection caps queries;
// any LLM/search-quota failure postpones the topic instead of retrying.
func (s *AutonomousResearchScheduler) runCycle(ctx context.Context, topic ResearchTopic, now time.Time, searchBudget *int) ResearchCycle {
	cycle := ResearchCycle{
		ID:             fmt.Sprintf("cycle-%s-%d", topic.ID, now.UnixNano()),
		OrganizationID: topic.OrganizationID,
		TopicID:        topic.ID,
		DepartmentID:   topic.DepartmentID,
		Trigger:        TriggerScheduler,
		StartedAt:      now,
		MissionID:      topic.MissionID, // optional metadata only; never required
	}
	s.emit("research.topic.claimed", map[string]any{"topic_id": topic.ID, "department": topic.DepartmentID})
	s.emit("research.cycle.started", map[string]any{"cycle_id": cycle.ID, "topic_id": topic.ID})
	// Durable stores enforce cycle->finding FK: the cycle row must exist
	// BEFORE any finding references it. Persisting at start also gives
	// unfinished-cycle recovery something real to detect: a crash here
	// leaves an open cycle, which restart marks failed (never completed).
	_ = s.deps.Agenda.SaveCycle(ctx, cycle)

	maxQueries := s.cfg.MaxQueriesPerTopic
	if maxQueries > *searchBudget {
		maxQueries = *searchBudget
	}

	queries, qerr := s.deps.Queries.GenerateQueries(ctx, topic, maxQueries)
	if qerr != nil {
		return s.finishCyclePostponed(cycle, topic, "query_generation_failed", "research.cycle.failed")
	}
	if len(queries) > s.cfg.MaxQueriesPerTopic {
		queries = queries[:s.cfg.MaxQueriesPerTopic]
	}
	if len(queries) > *searchBudget {
		queries = queries[:*searchBudget]
	}

	// LLM turn accounting: query generation is one turn. Loop protection
	// aborts the cycle if the generator somehow asks for more turns than
	// the budget allows.
	llmTurns := 1

	totalResults := 0
	newEvidence := 0
	duplicates := 0
	var providersUsed []string
	var novelRefs []EvidenceRef
	var allResults []SearchResult
	seenEvidence := map[string]bool{}

	for i, query := range queries {
		if ctx.Err() != nil {
			return s.finishCyclePostponed(cycle, topic, "context_canceled", "research.cycle.failed")
		}
		if llmTurns > s.cfg.MaxLLMTurnsPerCycle {
			return s.finishCycle(cycle, topic, CycleOutcomeAbortedLoop, "loop_limit", nil)
		}

		intent := topic.AllowedIntents[0]
		req := SearchRequest{
			Query:      query,
			Intent:     intent,
			RoleID:     string(RoleResearch),
			MissionID:  topic.MissionID,
			MaxResults: 5,
		}
		resp, err := s.deps.Search.Search(ctx, req)
		*searchBudget--
		cycle.QueriesAttempted++
		s.emit("research.query.generated", map[string]any{
			"cycle_id": cycle.ID, "query_index": i, "intent": string(intent),
		})
		if err != nil {
			// Rate limit / quota / upstream failure: the SearchRouter already
			// tried its route. Postpone — never hammer the same provider.
			return s.finishCyclePostponed(cycle, topic, "search_provider_failed", "research.cycle.failed")
		}
		s.emit("research.search.completed", map[string]any{
			"cycle_id": cycle.ID, "result_count": len(resp.Results),
		})
		totalResults += len(resp.Results)
		providersUsed = append(providersUsed, resp.ProviderRoute.Strings()...)

		for _, ref := range evidenceRefs(resp.Results) {
			key := evidenceKey(ref)
			if seenEvidence[key] {
				duplicates++
				continue
			}
			seenEvidence[key] = true
			cutoff := now
			if topic.NoveltyWindow > 0 {
				cutoff = now.Add(-topic.NoveltyWindow)
			}
			seen, err := s.deps.Evidence.EvidenceSeen(ctx, topic.ID, evidenceKey(ref), cutoff)
			if err != nil {
				continue
			}
			if seen {
				duplicates++
				continue
			}
			newEvidence++
			novelRefs = append(novelRefs, ref)
			s.emit("research.novelty.detected", map[string]any{
				"cycle_id": cycle.ID, "provider": ref.Provider,
			})
		}
		// Collect the raw results for provenance-aware evidence scoring.
		allResults = append(allResults, resp.Results...)
	}

	cycle.ProvidersUsed = dedupeStrings(providersUsed)
	cycle.ResultCount = totalResults
	cycle.NewEvidenceCount = newEvidence
	cycle.DuplicateCount = duplicates

	if newEvidence == 0 {
		return s.finishCycle(cycle, topic, CycleOutcomeNoNewInfo, "", func() {})
	}

	// V2 finding policy: durable_candidate requires novelty PLUS quality
	// PLUS independence PLUS durable claim shape — never raw result count.
	// Three aggregator URLs of the same story are informative, not durable.
	decision := s.deps.Findings.Evaluate(novelRefs, allResults)
	classification := decision.Classification
	finding := ResearchFinding{
		ID:              fmt.Sprintf("finding-%s", cycle.ID),
		OrganizationID:  topic.OrganizationID,
		TopicID:         topic.ID,
		DepartmentID:    topic.DepartmentID,
		ResearchCycleID: cycle.ID,
		Title:           topic.Title,
		Summary:         fmt.Sprintf("%d new evidence item(s) for topic %q", newEvidence, topic.Title),
		Importance:      topic.Priority,
		Novelty:         1.0,
		Confidence:      0.8,
		Classification:  classification,
		EvidenceRefs:    novelRefs,
		MissionID:       topic.MissionID,
		CreatedAt:       s.deps.Clock().UTC(),
	}
	if err := s.deps.Agenda.SaveFinding(ctx, finding); err != nil {
		return s.finishCyclePostponed(cycle, topic, "finding_persist_failed", "research.cycle.failed")
	}
	cycle.FindingsCreated = 1
	s.emit("research.finding.created", map[string]any{
		"cycle_id": cycle.ID, "classification": string(classification),
		"durable_eligible": decision.DurableEligible,
	})

	// Important/critical emit priority events; durable candidates may enter
	// the existing RAG governance (SUBMIT only — never promote).
	if classification == FindingImportant || classification == FindingCritical {
		priority := "important"
		if classification == FindingCritical {
			priority = "critical"
		}
		s.emit("research.finding.alert", map[string]any{
			"cycle_id": cycle.ID, "priority": priority, "department": topic.DepartmentID,
		})
	}
	if classification == FindingDurableCandidate && s.deps.RAG != nil {
		if err := s.deps.RAG.SubmitRAGCandidate(ctx, finding); err == nil {
			cycle.RAGCandidatesCreated = 1
			s.emit("research.rag_candidate.submitted", map[string]any{"cycle_id": cycle.ID})
		}
	}

	return s.finishCycle(cycle, topic, CycleOutcomeCompleted, "", nil)
}

// finishCyclePostponed closes a failed cycle safely: the topic is postponed
// (NextCheckAt pushed into the future), the cycle recorded, and no retry
// loop is started.
func (s *AutonomousResearchScheduler) finishCyclePostponed(cycle ResearchCycle, topic ResearchTopic, errorClass, event string) ResearchCycle {
	cycle.Outcome = CycleOutcomePostponed
	cycle.ErrorClass = errorClass
	s.recordCycleAndSchedule(cycle, topic, event, backoffForErrorClass(errorClass))
	return cycle
}

// finishCycle closes a successful (or aborted) cycle and reschedules the
// topic per its own cadence.
func (s *AutonomousResearchScheduler) finishCycle(cycle ResearchCycle, topic ResearchTopic, outcome CycleOutcome, errorClass string, extra func()) ResearchCycle {
	cycle.Outcome = outcome
	cycle.ErrorClass = errorClass
	if extra != nil {
		extra()
	}
	s.recordCycleAndSchedule(cycle, topic, "", topic.Cadence)
	return cycle
}

// recordCycleAndSchedule persists the cycle, stamps completion, updates the
// topic's LastCheckedAt/NextCheckAt from ITS OWN cadence, and emits closure
// events.
func (s *AutonomousResearchScheduler) recordCycleAndSchedule(cycle ResearchCycle, topic ResearchTopic, failureEvent string, delay time.Duration) {
	now := s.deps.Clock().UTC()
	cycle.CompletedAt = &now
	_ = s.deps.Agenda.SaveCycle(context.Background(), cycle)

	if t, err := s.deps.Agenda.GetTopic(context.Background(), topic.ID); err == nil {
		t.LastCheckedAt = &now
		if delay < time.Minute {
			delay = topic.Cadence
		}
		t.NextCheckAt = now.Add(delay)
		_ = s.deps.Agenda.SaveTopic(context.Background(), t)
		s.emit("research.topic.postponed", map[string]any{
			"topic_id": topic.ID, "next_check_at": t.NextCheckAt.Format(time.RFC3339),
		})
	}

	if failureEvent != "" {
		s.emit(failureEvent, map[string]any{"cycle_id": cycle.ID, "error_class": cycle.ErrorClass})
		return
	}
	s.emit("research.cycle.completed", map[string]any{
		"cycle_id": cycle.ID, "outcome": string(cycle.Outcome),
		"new_evidence": cycle.NewEvidenceCount, "duplicates": cycle.DuplicateCount,
	})
}

// evidenceSeen reports whether an evidence key was recorded for the topic
// since the cutoff. Implements the durable EvidenceIndex contract for the
// in-memory store.
func (m *MemoryAgenda) evidenceSeen(topicID, key string, since time.Time) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, id := range m.findingOrder {
		finding := m.findings[id]
		if finding.TopicID != topicID {
			continue
		}
		if finding.CreatedAt.Before(since) {
			continue
		}
		for _, seen := range finding.EvidenceRefs {
			if evidenceKey(seen) == key {
				return true, nil
			}
		}
	}
	return false, nil
}

// evidenceRefs extracts safe provenance pointers from router results.
func evidenceRefs(results []SearchResult) []EvidenceRef {
	refs := make([]EvidenceRef, 0, len(results))
	for _, r := range results {
		ref := EvidenceRef{
			Provider: r.Provider,
			URL:      canonicalURLOf(r),
			DOI:      r.DOI,
			ArxivID:  r.ArxivID,
			ISBN:     r.ISBN,
			Title:    r.Title,
			FoundAt:  time.Now().UTC(),
		}
		if ref.URL == "" {
			ref.URL = r.URL
		}
		refs = append(refs, ref)
	}
	return refs
}

// canonicalURLOf prefers the canonical URL when present.
func canonicalURLOf(r SearchResult) string {
	if r.CanonicalURL != "" {
		return r.CanonicalURL
	}
	return r.URL
}

// EvidenceRefKey builds the novelty comparison key for one evidence ref:
// structured IDs dominate (DOI > arXiv > ISBN), canonical URL otherwise.
// Exported so durable stores can compute the same key without duplicating
// the canonicalization rules.
func EvidenceRefKey(ref EvidenceRef) string {
	switch {
	case ref.DOI != "":
		return "doi:" + strings.ToLower(ref.DOI)
	case ref.ArxivID != "":
		return "arxiv:" + strings.ToLower(ref.ArxivID)
	case ref.ISBN != "":
		return "isbn:" + strings.ToLower(ref.ISBN)
	default:
		return "url:" + strings.ToLower(ref.URL)
	}
}

// evidenceKey is the internal alias for EvidenceRefKey.
func evidenceKey(ref EvidenceRef) string { return EvidenceRefKey(ref) }

// dedupeStrings preserves order while removing duplicates.
func dedupeStrings(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// emit is a nil-safe event sink call.
func (s *AutonomousResearchScheduler) emit(event string, fields map[string]any) {
	if s.deps.Events == nil {
		return
	}
	s.deps.Events.ResearchEvent(event, fields)
}
