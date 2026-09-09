package search

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// =============================================================================
// ResearchAgenda
//
// Owns KnowledgeNeeds and ResearchTopics. Responsibilities:
//   - store needs/topics;
//   - select due topics deterministically;
//   - prevent duplicate topics;
//   - update NextCheckAt;
//   - merge related topics;
//   - record topic status.
//
// It NEVER performs searches itself: the SearchRouter is the only search
// boundary. The in-memory implementation is the V1 store; a Postgres
// repository can implement the same interfaces later without touching the
// scheduler.
// =============================================================================

// NeedRepository persists KnowledgeNeeds.
type NeedRepository interface {
	SaveNeed(ctx context.Context, need KnowledgeNeed) error
	GetNeed(ctx context.Context, id string) (KnowledgeNeed, error)
	ListNeeds(ctx context.Context, departmentID string) ([]KnowledgeNeed, error)
}

// TopicRepository persists ResearchTopics.
type TopicRepository interface {
	SaveTopic(ctx context.Context, topic ResearchTopic) error
	GetTopic(ctx context.Context, id string) (ResearchTopic, error)
	ListTopics(ctx context.Context, departmentID string) ([]ResearchTopic, error)
}

// CycleRepository persists ResearchCycles.
type CycleRepository interface {
	SaveCycle(ctx context.Context, cycle ResearchCycle) error
	ListCycles(ctx context.Context, topicID string) ([]ResearchCycle, error)
}

// FindingRepository persists ResearchFindings and powers the department feed.
type FindingRepository interface {
	SaveFinding(ctx context.Context, finding ResearchFinding) error
	ListFindings(ctx context.Context, filter FindingFilter) ([]ResearchFinding, error)
}

// -----------------------------------------------------------------------------
// In-memory implementation
// -----------------------------------------------------------------------------

// MemoryAgenda is a thread-safe in-memory implementation of all four
// repositories. Good enough for V1 tests and single-process deployments.
type MemoryAgenda struct {
	mu sync.RWMutex

	needs    map[string]KnowledgeNeed
	topics   map[string]ResearchTopic
	cycles   map[string]ResearchCycle
	findings map[string]ResearchFinding

	// findingOrder preserves insertion order for stable feeds.
	findingOrder []string

	// claims is the advisory claim registry (topicID -> claim) used to keep
	// concurrent scheduler workers from double-running a topic.
	claims map[string]topicClaim

	// sequence gives deterministic IDs in tests and single-process runs.
	sequence int
}

// NewMemoryAgenda creates an empty in-memory agenda.
func NewMemoryAgenda() *MemoryAgenda {
	return &MemoryAgenda{
		needs:    map[string]KnowledgeNeed{},
		topics:   map[string]ResearchTopic{},
		cycles:   map[string]ResearchCycle{},
		findings: map[string]ResearchFinding{},
		claims:   map[string]topicClaim{},
	}
}

// SaveNeed validates and upserts a KnowledgeNeed.
func (m *MemoryAgenda) SaveNeed(_ context.Context, need KnowledgeNeed) error {
	if err := need.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if need.Status == "" {
		need.Status = NeedStatusActive
	}
	if need.CreatedAt.IsZero() {
		need.CreatedAt = time.Now().UTC()
	}
	need.UpdatedAt = time.Now().UTC()
	m.needs[need.ID] = need
	return nil
}

// GetNeed returns a need by ID.
func (m *MemoryAgenda) GetNeed(_ context.Context, id string) (KnowledgeNeed, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	need, ok := m.needs[id]
	if !ok {
		return KnowledgeNeed{}, fmt.Errorf("%w: knowledge need %q not found", ErrInvalidRequest, id)
	}
	return need, nil
}

// ListNeeds lists needs, optionally filtered by department.
func (m *MemoryAgenda) ListNeeds(_ context.Context, departmentID string) ([]KnowledgeNeed, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]KnowledgeNeed, 0, len(m.needs))
	for _, need := range m.needs {
		if departmentID != "" && need.DepartmentID != departmentID {
			continue
		}
		out = append(out, need)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

// SaveTopic validates and upserts a ResearchTopic. Duplicate prevention is
// keyed on (DepartmentID, normalized Title): saving a second topic with the
// same pair updates the existing one instead of creating noise.
func (m *MemoryAgenda) SaveTopic(_ context.Context, topic ResearchTopic) error {
	if err := topic.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	if topic.Status == "" {
		topic.Status = TopicStatusActive
	}
	if topic.NextCheckAt.IsZero() {
		topic.NextCheckAt = now
	}
	if topic.CreatedAt.IsZero() {
		topic.CreatedAt = now
	}
	topic.UpdatedAt = now

	// Duplicate prevention: same department + same normalized title.
	if topic.ID == "" {
		for _, existing := range m.topics {
			if existing.DepartmentID == topic.DepartmentID &&
				strings.EqualFold(normalizeTopicTitle(existing.Title), normalizeTopicTitle(topic.Title)) {
				topic.ID = existing.ID
				break
			}
		}
	}
	if topic.ID == "" {
		m.sequence++
		topic.ID = fmt.Sprintf("topic-%06d", m.sequence)
	}
	m.topics[topic.ID] = topic
	return nil
}

// GetTopic returns a topic by ID.
func (m *MemoryAgenda) GetTopic(_ context.Context, id string) (ResearchTopic, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	topic, ok := m.topics[id]
	if !ok {
		return ResearchTopic{}, fmt.Errorf("%w: research topic %q not found", ErrInvalidRequest, id)
	}
	return topic, nil
}

// ListTopics lists topics, optionally filtered by department.
func (m *MemoryAgenda) ListTopics(_ context.Context, departmentID string) ([]ResearchTopic, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]ResearchTopic, 0, len(m.topics))
	for _, topic := range m.topics {
		if departmentID != "" && topic.DepartmentID != departmentID {
			continue
		}
		out = append(out, topic)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

// SaveCycle records a cycle.
func (m *MemoryAgenda) SaveCycle(_ context.Context, cycle ResearchCycle) error {
	if strings.TrimSpace(cycle.ID) == "" {
		return fmt.Errorf("%w: cycle id is required", ErrInvalidRequest)
	}
	if strings.TrimSpace(cycle.TopicID) == "" {
		return fmt.Errorf("%w: cycle topic is required", ErrInvalidRequest)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cycles[cycle.ID] = cycle
	return nil
}

// ListCycles returns cycles for a topic, oldest first.
func (m *MemoryAgenda) ListCycles(_ context.Context, topicID string) ([]ResearchCycle, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]ResearchCycle, 0)
	for _, cycle := range m.cycles {
		if cycle.TopicID == topicID {
			out = append(out, cycle)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.Before(out[j].StartedAt) })
	return out, nil
}

// SaveFinding validates and stores a finding.
func (m *MemoryAgenda) SaveFinding(_ context.Context, finding ResearchFinding) error {
	if err := finding.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if finding.CreatedAt.IsZero() {
		finding.CreatedAt = time.Now().UTC()
	}
	if _, exists := m.findings[finding.ID]; !exists {
		m.findingOrder = append(m.findingOrder, finding.ID)
	}
	m.findings[finding.ID] = finding
	return nil
}

// FindingFilter selects findings for the department intelligence feed.
type FindingFilter struct {
	DepartmentID  string
	TopicID       string
	MinImportance bool // only important + critical
	Since         time.Time
	Until         time.Time
	Limit         int
}

// ListFindings implements the Department Intelligence feed.
func (m *MemoryAgenda) ListFindings(_ context.Context, filter FindingFilter) ([]ResearchFinding, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]ResearchFinding, 0)
	// Insertion order = chronological (oldest first), stable.
	for _, id := range m.findingOrder {
		finding := m.findings[id]
		if filter.DepartmentID != "" && finding.DepartmentID != filter.DepartmentID {
			continue
		}
		if filter.TopicID != "" && finding.TopicID != filter.TopicID {
			continue
		}
		if filter.MinImportance &&
			finding.Classification != FindingImportant && finding.Classification != FindingCritical {
			continue
		}
		if !filter.Since.IsZero() && finding.CreatedAt.Before(filter.Since) {
			continue
		}
		if !filter.Until.IsZero() && finding.CreatedAt.After(filter.Until) {
			continue
		}
		out = append(out, finding)
	}
	// Newest first.
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

// normalizeTopicTitle folds whitespace/case so duplicate detection is robust.
func normalizeTopicTitle(title string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(title))), " ")
}

// TryClaimTopic atomically marks a topic as claimed by owner until the TTL
// expires. It is the concurrency guard that keeps two scheduler workers from
// running the same due topic: exactly one caller wins per claim window.
// Claim state is advisory (memory-only in V1); a production Postgres store
// can implement the same semantics with row-level locks.
func (m *MemoryAgenda) TryClaimTopic(topicID, owner string, ttl time.Duration, now time.Time) bool {
	if topicID == "" || owner == "" || ttl <= 0 {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	topic, ok := m.topics[topicID]
	if !ok {
		return false
	}
	if topic.Status != TopicStatusActive && topic.Status != "" {
		return false
	}
	if existing, claimed := m.claims[topicID]; claimed {
		if now.Before(existing.expiresAt) {
			return false // still held
		}
	}
	m.claims[topicID] = topicClaim{owner: owner, expiresAt: now.Add(ttl)}
	return true
}

// ReleaseClaim drops the caller's claim on a topic.
func (m *MemoryAgenda) ReleaseClaim(topicID, owner string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, ok := m.claims[topicID]; ok && existing.owner == owner {
		delete(m.claims, topicID)
	}
}

// topicClaim records who holds a topic and until when.
type topicClaim struct {
	owner     string
	expiresAt time.Time
}

// SeedBootstrapTopics idempotently creates the small initial topic set. It
// never floods the agenda: same (department, normalized title) pairs are
// deduplicated by SaveTopic, so calling this repeatedly is safe. Seeds are
// representative, not exhaustive — departments add needs over time.
func SeedBootstrapTopics(ctx context.Context, agenda *MemoryAgenda, now time.Time) error {
	if agenda == nil {
		return fmt.Errorf("%w: bootstrap requires an agenda", ErrInvalidRequest)
	}
	seeds := []ResearchTopic{
		{
			OrganizationID: "explorarte", DepartmentID: "finanzas",
			Title:         "AI/API provider pricing changes",
			Description:   "Detect pricing changes, free-tier adjustments and cheaper equivalents across AI/API providers.",
			ResearchClass: ResearchWatch, Priority: 0.7,
			AllowedIntents: []SearchIntent{IntentWebGeneral, IntentNews},
			Cadence:        6 * time.Hour, NoveltyWindow: 7 * 24 * time.Hour,
			CreatedBy: "bootstrap", Reason: "cost vigilance",
			Status: TopicStatusActive, NextCheckAt: now,
		},
		{
			OrganizationID: "explorarte", DepartmentID: "infraestructura",
			Title:         "Critical dependency releases and CVEs",
			Description:   "Watch breaking releases and relevant CVEs in the infrastructure dependency set.",
			ResearchClass: ResearchWatch, Priority: 0.8,
			AllowedIntents: []SearchIntent{IntentWebGeneral, IntentNews},
			Cadence:        1 * time.Hour, NoveltyWindow: 7 * 24 * time.Hour,
			CreatedBy: "bootstrap", Reason: "supply-chain risk",
			Status: TopicStatusActive, NextCheckAt: now,
		},
		{
			OrganizationID: "explorarte", DepartmentID: "agentresources",
			Title:         "Agentic systems and self-improvement research",
			Description:   "Track agent harnesses, evaluation, memory, tool-use and orchestration advances.",
			ResearchClass: ResearchTrack, Priority: 0.6,
			AllowedIntents: []SearchIntent{IntentWebGeneral, IntentNews},
			Cadence:        24 * time.Hour, NoveltyWindow: 14 * 24 * time.Hour,
			CreatedBy: "bootstrap", Reason: "capability watch",
			Status: TopicStatusActive, NextCheckAt: now,
		},
		{
			OrganizationID: "explorarte", DepartmentID: "investigacion",
			Title:         "Metacognition, rumination, inner speech and mental regulation",
			Description:   "Deep literature watch: papers and preprints on metacognition and mental regulation.",
			ResearchClass: ResearchDeep, Priority: 0.5,
			AllowedIntents: []SearchIntent{IntentAcademic, IntentPreprint, IntentBookGeneral},
			Cadence:        7 * 24 * time.Hour, NoveltyWindow: 30 * 24 * time.Hour,
			CreatedBy: "bootstrap", Reason: "research program",
			Status: TopicStatusActive, NextCheckAt: now,
		},
	}
	for _, seed := range seeds {
		if err := agenda.SaveTopic(ctx, seed); err != nil {
			return err
		}
	}
	return nil
}
