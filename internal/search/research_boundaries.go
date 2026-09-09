package search

import (
	"context"
	"time"
)

// =============================================================================
// Durable scheduler boundaries
//
// V1 hard-wired the scheduler to *MemoryAgenda. Production requires the same
// scheduler to run over Postgres. These interfaces express exactly what the
// scheduler needs; both MemoryAgenda and the pgx Store satisfy them.
// =============================================================================

// SchedulerAgenda is the storage surface the scheduler uses. It is a subset
// of the repository interfaces plus evidence/novelty lookup.
type SchedulerAgenda interface {
	ListTopics(ctx context.Context, departmentID string) ([]ResearchTopic, error)
	GetTopic(ctx context.Context, id string) (ResearchTopic, error)
	SaveTopic(ctx context.Context, topic ResearchTopic) error
	ListCycles(ctx context.Context, topicID string) ([]ResearchCycle, error)
	SaveCycle(ctx context.Context, cycle ResearchCycle) error
	SaveFinding(ctx context.Context, finding ResearchFinding) error
}

// TopicClaimManager is the concurrency guard for multi-worker scheduling.
// Exactly one caller wins per claim window; expired claims are recoverable.
type TopicClaimManager interface {
	TryClaim(ctx context.Context, topicID, owner string, ttl time.Duration) (bool, error)
	Release(ctx context.Context, topicID, owner string) error
}

// EvidenceIndex answers the novelty question against durable state:
// has this evidence key been recorded for the topic since `since`?
type EvidenceIndex interface {
	EvidenceSeen(ctx context.Context, topicID, evidenceKey string, since time.Time) (bool, error)
}

// MemoryClaimManager adapts MemoryAgenda's synchronous claim API.
type MemoryClaimManager struct {
	Agenda *MemoryAgenda
	Owner  string
	TTL    time.Duration
}

// TryClaim implements TopicClaimManager.
func (m MemoryClaimManager) TryClaim(_ context.Context, topicID, owner string, ttl time.Duration) (bool, error) {
	return m.Agenda.TryClaimTopic(topicID, owner, ttl, time.Now().UTC()), nil
}

// Release implements TopicClaimManager.
func (m MemoryClaimManager) Release(_ context.Context, topicID, owner string) error {
	m.Agenda.ReleaseClaim(topicID, owner)
	return nil
}

// MemoryEvidenceIndex adapts MemoryAgenda's in-process novelty lookup.
type MemoryEvidenceIndex struct {
	Agenda *MemoryAgenda
}

// EvidenceSeen implements EvidenceIndex.
func (m MemoryEvidenceIndex) EvidenceSeen(_ context.Context, topicID, key string, since time.Time) (bool, error) {
	return m.Agenda.evidenceSeen(topicID, key, since)
}
