// Package postgres implements the durable research agenda repositories on
// the canonical pgx/pool conventions used across the organization. It
// satisfies the V1 repository interfaces and adds durable claims and
// proposal persistence.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Mireuz13/explorarte-organization/internal/search"
)

// Store is the Postgres-backed research agenda store.
type Store struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

// New validates the pool and returns the store.
func New(pool *pgxpool.Pool) (*Store, error) {
	if pool == nil {
		return nil, errors.New("research store requires an initialized pool")
	}
	return &Store{pool: pool, now: func() time.Time { return time.Now().UTC() }}, nil
}

// WithClock overrides the clock (tests).
func (s *Store) WithClock(now func() time.Time) *Store {
	s.now = now
	return s
}

// ErrNotFound is the canonical not-found sentinel.
var ErrNotFound = errors.New("research: not found")

func mapNotFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// =============================================================================
// KnowledgeNeed
// =============================================================================

// SaveNeed upserts a KnowledgeNeed.
func (s *Store) SaveNeed(ctx context.Context, need search.KnowledgeNeed) error {
	if err := need.Validate(); err != nil {
		return err
	}
	now := s.now()
	_, err := s.pool.Exec(ctx, `
		INSERT INTO research_knowledge_needs
			(id, organization_id, department_id, question, description,
			 importance, confidence, status, source, last_satisfied_at, expires_at,
			 created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,
			COALESCE(NULLIF($12::timestamptz, '0001-01-01T00:00:00Z'::timestamptz), $13), $13)
		ON CONFLICT (id) DO UPDATE SET
			question = EXCLUDED.question,
			description = EXCLUDED.description,
			importance = EXCLUDED.importance,
			confidence = EXCLUDED.confidence,
			status = EXCLUDED.status,
			last_satisfied_at = EXCLUDED.last_satisfied_at,
			expires_at = EXCLUDED.expires_at,
			updated_at = EXCLUDED.updated_at`,
		need.ID, need.OrganizationID, need.DepartmentID, need.Question, need.Description,
		need.Importance, need.Confidence, string(need.Status), string(need.Source),
		need.LastSatisfiedAt, need.ExpiresAt, need.CreatedAt, now,
	)
	return err
}

const needColumns = `id, organization_id, department_id, question, description,
	importance, confidence, status, source, last_satisfied_at, expires_at, created_at, updated_at`

func scanNeed(row pgx.Row) (search.KnowledgeNeed, error) {
	var need search.KnowledgeNeed
	var status, source string
	err := row.Scan(&need.ID, &need.OrganizationID, &need.DepartmentID, &need.Question,
		&need.Description, &need.Importance, &need.Confidence, &status, &source,
		&need.LastSatisfiedAt, &need.ExpiresAt, &need.CreatedAt, &need.UpdatedAt)
	if err != nil {
		return search.KnowledgeNeed{}, mapNotFound(err)
	}
	need.Status = search.NeedStatus(status)
	need.Source = search.KnowledgeNeedSource(source)
	return need, nil
}

// GetNeed reads one need.
func (s *Store) GetNeed(ctx context.Context, id string) (search.KnowledgeNeed, error) {
	return scanNeed(s.pool.QueryRow(ctx,
		`SELECT `+needColumns+` FROM research_knowledge_needs WHERE id = $1`, id))
}

// ListNeeds lists needs by department (empty = all).
func (s *Store) ListNeeds(ctx context.Context, departmentID string) ([]search.KnowledgeNeed, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+needColumns+` FROM research_knowledge_needs
		WHERE ($1 = '' OR department_id = $1)
		ORDER BY created_at`, departmentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []search.KnowledgeNeed{}
	for rows.Next() {
		need, err := scanNeed(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, need)
	}
	return out, rows.Err()
}

// =============================================================================
// ResearchTopic
// =============================================================================

func intentsToJSON(intents []search.SearchIntent) []byte {
	if len(intents) == 0 {
		return []byte("[]")
	}
	raw, _ := json.Marshal(intents)
	return raw
}

func intentsFromJSON(raw []byte) []search.SearchIntent {
	var out []search.SearchIntent
	_ = json.Unmarshal(raw, &out)
	return out
}

// SaveTopic upserts a topic. Duplicate prevention (department + normalized
// title) is enforced by the caller (MemoryAgenda/proposal policy); the store
// keeps identity stable per ID. Claims are NEVER overwritten by SaveTopic.
func (s *Store) SaveTopic(ctx context.Context, topic search.ResearchTopic) error {
	if err := topic.Validate(); err != nil {
		return err
	}
	now := s.now()
	_, err := s.pool.Exec(ctx, `
		INSERT INTO research_topics
			(id, organization_id, department_id, parent_knowledge_need_id,
			 title, description, priority, research_class, status,
			 allowed_intents, cadence_seconds, novelty_window_seconds,
			 last_checked_at, next_check_at, created_by, reason, mission_id,
			 created_at, updated_at)
		VALUES ($1,$2,$3,NULLIF($4,''),$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,NULLIF($17,''),$18,$18)
		ON CONFLICT (id) DO UPDATE SET
			title = EXCLUDED.title,
			description = EXCLUDED.description,
			priority = EXCLUDED.priority,
			status = EXCLUDED.status,
			allowed_intents = EXCLUDED.allowed_intents,
			cadence_seconds = EXCLUDED.cadence_seconds,
			last_checked_at = EXCLUDED.last_checked_at,
			next_check_at = EXCLUDED.next_check_at,
			updated_at = EXCLUDED.updated_at`,
		topic.ID, topic.OrganizationID, topic.DepartmentID, topic.ParentKnowledgeNeedID,
		topic.Title, topic.Description, topic.Priority, string(topic.ResearchClass),
		string(topic.Status), intentsToJSON(topic.AllowedIntents),
		int64(topic.Cadence/time.Second), int64(topic.NoveltyWindow/time.Second),
		topic.LastCheckedAt, topic.NextCheckAt, topic.CreatedBy, topic.Reason,
		topic.MissionID, now,
	)
	return err
}

const topicColumns = `id, organization_id, department_id,
	COALESCE(parent_knowledge_need_id, ''),
	title, description, priority, research_class, status,
	allowed_intents, cadence_seconds, novelty_window_seconds,
	last_checked_at, next_check_at, created_by, reason,
	COALESCE(mission_id, ''),
	COALESCE(claim_owner, ''), claim_expires_at,
	created_at, updated_at`

func scanTopic(row pgx.Row) (search.ResearchTopic, error) {
	var topic search.ResearchTopic
	var status, class, claimOwner string
	var intentsJSON []byte
	var cadence, novelty int64
	var claimExpires *time.Time
	err := row.Scan(&topic.ID, &topic.OrganizationID, &topic.DepartmentID,
		&topic.ParentKnowledgeNeedID, &topic.Title, &topic.Description,
		&topic.Priority, &class, &status, &intentsJSON, &cadence, &novelty,
		&topic.LastCheckedAt, &topic.NextCheckAt, &topic.CreatedBy, &topic.Reason,
		&topic.MissionID, &claimOwner, &claimExpires,
		&topic.CreatedAt, &topic.UpdatedAt)
	if err != nil {
		return search.ResearchTopic{}, mapNotFound(err)
	}
	topic.Status = search.TopicStatus(status)
	topic.ResearchClass = search.ResearchClass(class)
	topic.AllowedIntents = intentsFromJSON(intentsJSON)
	topic.Cadence = time.Duration(cadence) * time.Second
	topic.NoveltyWindow = time.Duration(novelty) * time.Second
	_ = claimOwner
	_ = claimExpires // claim state is exercised via TryClaimTopic
	return topic, nil
}

// GetTopic reads one topic.
func (s *Store) GetTopic(ctx context.Context, id string) (search.ResearchTopic, error) {
	return scanTopic(s.pool.QueryRow(ctx,
		`SELECT `+topicColumns+` FROM research_topics WHERE id = $1`, id))
}

// ListTopics lists topics by department (empty = all).
func (s *Store) ListTopics(ctx context.Context, departmentID string) ([]search.ResearchTopic, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+topicColumns+` FROM research_topics
		WHERE ($1 = '' OR department_id = $1)
		ORDER BY created_at`, departmentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []search.ResearchTopic{}
	for rows.Next() {
		topic, err := scanTopic(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, topic)
	}
	return out, rows.Err()
}

// ClaimLost is returned by TryClaimTopic when another live claim holds the
// topic.
var ErrClaimLost = errors.New("research: topic claim lost")

// TryClaimTopic atomically claims a due topic:
//
//	UPDATE ... SET claim_owner, claim_expires_at
//	WHERE id = $1
//	  AND status = 'active'
//	  AND (claim_expires_at IS NULL OR claim_expires_at <= now())
//	RETURNING ...
//
// Exactly one concurrent caller wins; an expired claim is recoverable; a
// live claim cannot be stolen.
func (s *Store) TryClaimTopic(ctx context.Context, topicID, owner string, ttl time.Duration) (search.ResearchTopic, error) {
	if topicID == "" || owner == "" || ttl <= 0 {
		return search.ResearchTopic{}, ErrClaimLost
	}
	now := s.now()
	row := s.pool.QueryRow(ctx, `
		UPDATE research_topics
		SET claim_owner = $2::text, claim_expires_at = $3::timestamptz, updated_at = $3::timestamptz
		WHERE id = $1
		  AND status = 'active'
		  AND (claim_expires_at IS NULL OR claim_expires_at <= $4)
		RETURNING `+topicColumns, topicID, owner, now.Add(ttl), now)
	topic, err := scanTopic(row)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return search.ResearchTopic{}, ErrClaimLost
		}
		return search.ResearchTopic{}, err
	}
	return topic, nil
}

// TryClaimAt is TryClaimTopic with an explicit clock, for tests that verify
// claim expiry recovery deterministically.
func (s *Store) TryClaimAt(ctx context.Context, topicID, owner string, ttl time.Duration, now time.Time) (bool, error) {
	row := s.pool.QueryRow(ctx, `
		UPDATE research_topics
		SET claim_owner = $2::text, claim_expires_at = $3::timestamptz, updated_at = $3::timestamptz
		WHERE id = $1
		  AND status = 'active'
		  AND (claim_expires_at IS NULL OR claim_expires_at <= $4)
		RETURNING id`, topicID, owner, now.Add(ttl), now)
	var id string
	err := row.Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// ReleaseClaim drops the caller's claim (idempotent per owner).
func (s *Store) ReleaseClaim(ctx context.Context, topicID, owner string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE research_topics
		SET claim_owner = NULL, claim_expires_at = NULL, updated_at = $3
		WHERE id = $1 AND claim_owner = $2`,
		topicID, owner, s.now())
	return err
}

// AdvanceTopic stamps a completed check and schedules the next one from the
// topic's own cadence, clearing the claim in the same atomic statement.
func (s *Store) AdvanceTopic(ctx context.Context, topicID string, cadence time.Duration, now time.Time) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE research_topics
		SET last_checked_at = $2::timestamptz,
		    next_check_at = $2::timestamptz + make_interval(secs => $3),
		    claim_owner = NULL,
		    claim_expires_at = NULL,
		    updated_at = $2
		WHERE id = $1`,
		topicID, now, int64(cadence/time.Second))
	return err
}

// =============================================================================
// ResearchCycle
// =============================================================================

// SaveCycle upserts a cycle (twice: started, then completed).
func (s *Store) SaveCycle(ctx context.Context, cycle search.ResearchCycle) error {
	providersJSON, _ := json.Marshal(cycle.ProvidersUsed)
	var errorClass *string
	if cycle.ErrorClass != "" {
		errorClass = &cycle.ErrorClass
	}
	var missionID *string
	if cycle.MissionID != "" {
		missionID = &cycle.MissionID
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO research_cycles
			(id, organization_id, topic_id, department_id, mission_id,
			 trigger, outcome, started_at, completed_at,
			 queries_attempted, providers_used, result_count,
			 new_evidence_count, duplicate_count, findings_created,
			 rag_candidates_created, error_class)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
		ON CONFLICT (id) DO UPDATE SET
			outcome = EXCLUDED.outcome,
			completed_at = EXCLUDED.completed_at,
			queries_attempted = EXCLUDED.queries_attempted,
			providers_used = EXCLUDED.providers_used,
			result_count = EXCLUDED.result_count,
			new_evidence_count = EXCLUDED.new_evidence_count,
			duplicate_count = EXCLUDED.duplicate_count,
			findings_created = EXCLUDED.findings_created,
			rag_candidates_created = EXCLUDED.rag_candidates_created,
			error_class = EXCLUDED.error_class`,
		cycle.ID, cycle.OrganizationID, cycle.TopicID, cycle.DepartmentID, missionID,
		string(cycle.Trigger), string(cycle.Outcome), cycle.StartedAt, cycle.CompletedAt,
		cycle.QueriesAttempted, providersJSON, cycle.ResultCount,
		cycle.NewEvidenceCount, cycle.DuplicateCount, cycle.FindingsCreated,
		cycle.RAGCandidatesCreated, errorClass,
	)
	return err
}

// ListCycles returns a topic's cycles, oldest first.
func (s *Store) ListCycles(ctx context.Context, topicID string) ([]search.ResearchCycle, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, organization_id, topic_id, department_id,
			COALESCE(mission_id, ''), trigger, outcome, started_at, completed_at,
			queries_attempted, providers_used, result_count,
			new_evidence_count, duplicate_count, findings_created,
			rag_candidates_created, COALESCE(error_class, '')
		FROM research_cycles WHERE topic_id = $1 ORDER BY started_at`, topicID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []search.ResearchCycle{}
	for rows.Next() {
		var cycle search.ResearchCycle
		var trigger, outcome string
		var providersJSON []byte
		if err := rows.Scan(&cycle.ID, &cycle.OrganizationID, &cycle.TopicID,
			&cycle.DepartmentID, &cycle.MissionID, &trigger, &outcome,
			&cycle.StartedAt, &cycle.CompletedAt, &cycle.QueriesAttempted,
			&providersJSON, &cycle.ResultCount, &cycle.NewEvidenceCount,
			&cycle.DuplicateCount, &cycle.FindingsCreated,
			&cycle.RAGCandidatesCreated, &cycle.ErrorClass); err != nil {
			return nil, err
		}
		cycle.Trigger = search.ResearchTrigger(trigger)
		cycle.Outcome = search.CycleOutcome(outcome)
		_ = json.Unmarshal(providersJSON, &cycle.ProvidersUsed)
		out = append(out, cycle)
	}
	return out, rows.Err()
}

// UnfinishedCycles lists cycles without completion — restart semantics:
// unfinished cycles are identifiable and are marked failed, never pretended
// to have completed.
func (s *Store) UnfinishedCycles(ctx context.Context, olderThan time.Duration) ([]search.ResearchCycle, error) {
	cutoff := s.now().Add(-olderThan)
	rows, err := s.pool.Query(ctx, `
		SELECT id, organization_id, topic_id, department_id,
			COALESCE(mission_id, ''), trigger, outcome, started_at, completed_at,
			queries_attempted, providers_used, result_count,
			new_evidence_count, duplicate_count, findings_created,
			rag_candidates_created, COALESCE(error_class, '')
		FROM research_cycles
		WHERE completed_at IS NULL AND started_at < $1`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []search.ResearchCycle{}
	for rows.Next() {
		var cycle search.ResearchCycle
		var trigger, outcome string
		var providersJSON []byte
		if err := rows.Scan(&cycle.ID, &cycle.OrganizationID, &cycle.TopicID,
			&cycle.DepartmentID, &cycle.MissionID, &trigger, &outcome,
			&cycle.StartedAt, &cycle.CompletedAt, &cycle.QueriesAttempted,
			&providersJSON, &cycle.ResultCount, &cycle.NewEvidenceCount,
			&cycle.DuplicateCount, &cycle.FindingsCreated,
			&cycle.RAGCandidatesCreated, &cycle.ErrorClass); err != nil {
			return nil, err
		}
		cycle.Trigger = search.ResearchTrigger(trigger)
		cycle.Outcome = search.CycleOutcome(outcome)
		_ = json.Unmarshal(providersJSON, &cycle.ProvidersUsed)
		out = append(out, cycle)
	}
	return out, rows.Err()
}

// MarkCycleFailed closes an unfinished cycle after a restart with an
// explicit interrupted outcome. It NEVER fabricates completion.
func (s *Store) MarkCycleFailed(ctx context.Context, cycleID, errorClass string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE research_cycles
		SET outcome = 'failed', error_class = $2, completed_at = $3
		WHERE id = $1 AND completed_at IS NULL`,
		cycleID, errorClass, s.now())
	return err
}

// =============================================================================
// ResearchFinding
// =============================================================================

func evidenceToJSON(refs []search.EvidenceRef) []byte {
	if len(refs) == 0 {
		return []byte("[]")
	}
	raw, _ := json.Marshal(refs)
	return raw
}

func evidenceFromJSON(raw []byte) []search.EvidenceRef {
	var out []search.EvidenceRef
	_ = json.Unmarshal(raw, &out)
	return out
}

// SaveFinding inserts a finding (idempotent per ID).
func (s *Store) SaveFinding(ctx context.Context, finding search.ResearchFinding) error {
	if err := finding.Validate(); err != nil {
		return err
	}
	var missionID *string
	if finding.MissionID != "" {
		missionID = &finding.MissionID
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO research_findings
			(id, organization_id, topic_id, department_id, research_cycle_id,
			 title, summary, importance, novelty, confidence, classification,
			 evidence, mission_id, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,
			COALESCE(NULLIF($14::timestamptz, '0001-01-01T00:00:00Z'::timestamptz), $15))
		ON CONFLICT (id) DO NOTHING`,
		finding.ID, finding.OrganizationID, finding.TopicID, finding.DepartmentID,
		finding.ResearchCycleID, finding.Title, finding.Summary,
		finding.Importance, finding.Novelty, finding.Confidence,
		string(finding.Classification), evidenceToJSON(finding.EvidenceRefs),
		missionID, finding.CreatedAt, s.now(),
	)
	return err
}

const findingColumns = `id, organization_id, topic_id, department_id,
	research_cycle_id, title, summary, importance, novelty, confidence,
	classification, evidence, COALESCE(mission_id, ''), created_at`

func scanFinding(row pgx.Row) (search.ResearchFinding, error) {
	var finding search.ResearchFinding
	var classification string
	var evidenceJSON []byte
	err := row.Scan(&finding.ID, &finding.OrganizationID, &finding.TopicID,
		&finding.DepartmentID, &finding.ResearchCycleID, &finding.Title,
		&finding.Summary, &finding.Importance, &finding.Novelty,
		&finding.Confidence, &classification, &evidenceJSON,
		&finding.MissionID, &finding.CreatedAt)
	if err != nil {
		return search.ResearchFinding{}, mapNotFound(err)
	}
	finding.Classification = search.FindingClassification(classification)
	finding.EvidenceRefs = evidenceFromJSON(evidenceJSON)
	return finding, nil
}

// ListFindings implements the durable Department Intelligence feed with the
// V1 filter contract, backed by the department/time indexes.
func (s *Store) ListFindings(ctx context.Context, filter search.FindingFilter) ([]search.ResearchFinding, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+findingColumns+` FROM research_findings
		WHERE ($1 = '' OR department_id = $1)
		  AND ($2 = '' OR topic_id = $2)
		  AND ($3 = FALSE OR classification IN ('important', 'critical'))
		  AND ($4::timestamptz IS NULL OR created_at >= $4)
		  AND ($5::timestamptz IS NULL OR created_at <= $5)
		ORDER BY created_at DESC
		LIMIT $6`,
		filter.DepartmentID, filter.TopicID, filter.MinImportance,
		nilIfZero(filter.Since), nilIfZero(filter.Until),
		nonZeroLimit(filter.Limit),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []search.ResearchFinding{}
	for rows.Next() {
		finding, err := scanFinding(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, finding)
	}
	return out, rows.Err()
}

func nilIfZero(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func nonZeroLimit(limit int) int {
	if limit <= 0 {
		return 500
	}
	return limit
}

// =============================================================================
// ResearchTopicProposal
// =============================================================================

// SaveProposal persists a proposal with its host decision. Pending proposals
// are durable across restarts.
func (s *Store) SaveProposal(ctx context.Context, proposal search.ResearchTopicProposal) error {
	var decidedAt *time.Time
	if proposal.ReviewedAt != nil {
		decidedAt = proposal.ReviewedAt
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO research_topic_proposals
			(id, organization_id, department_id, parent_topic_id, parent_need_id,
			 title, description, proposed_class, proposed_priority,
			 proposed_cadence_seconds, allowed_intents, rationale,
			 origin_cycle_id, decision, decision_reason, proposed_by,
			 created_at, decided_at)
		VALUES ($1,$2,$3,NULLIF($4,''),NULLIF($5,''),$6,$7,$8,$9,$10,$11,$12,
			NULLIF($13,''),$14,$15,$16,$17,$18)
		ON CONFLICT (id) DO UPDATE SET
			decision = EXCLUDED.decision,
			decision_reason = EXCLUDED.decision_reason,
			decided_at = EXCLUDED.decided_at`,
		proposal.ID, proposal.OrganizationID, proposal.DepartmentID,
		proposal.ParentTopicID, proposal.ParentKnowledgeNeedID,
		proposal.Title, proposal.Description, string(proposal.ResearchClass),
		proposal.Priority, int64(proposal.Cadence/time.Second),
		intentsToJSON(proposal.AllowedIntents), proposal.Reason,
		proposal.OriginCycleID, string(proposal.Decision),
		string(proposal.RejectReason), proposal.ReviewedBy,
		proposal.CreatedAt, decidedAt,
	)
	return err
}

// ListPendingProposals returns undecided proposals for review.
func (s *Store) ListPendingProposals(ctx context.Context, departmentID string) ([]search.ResearchTopicProposal, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, organization_id, department_id,
			COALESCE(parent_topic_id, ''), COALESCE(parent_need_id, ''),
			title, description, proposed_class, proposed_priority,
			proposed_cadence_seconds, allowed_intents, rationale,
			COALESCE(origin_cycle_id, ''), decision, decision_reason,
			proposed_by, created_at, decided_at
		FROM research_topic_proposals
		WHERE decision = 'pending_review'
		  AND ($1 = '' OR department_id = $1)
		ORDER BY created_at`, departmentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []search.ResearchTopicProposal{}
	for rows.Next() {
		var proposal search.ResearchTopicProposal
		var class, decision string
		var intentsJSON []byte
		var cadence int64
		if err := rows.Scan(&proposal.ID, &proposal.OrganizationID,
			&proposal.DepartmentID, &proposal.ParentTopicID,
			&proposal.ParentKnowledgeNeedID, &proposal.Title,
			&proposal.Description, &class, &proposal.Priority, &cadence,
			&intentsJSON, &proposal.Reason, &proposal.OriginCycleID,
			&decision, &proposal.RejectReason, &proposal.ReviewedBy,
			&proposal.CreatedAt, &proposal.ReviewedAt); err != nil {
			return nil, err
		}
		proposal.ResearchClass = search.ResearchClass(class)
		proposal.Decision = search.ProposalDecision(decision)
		proposal.AllowedIntents = intentsFromJSON(intentsJSON)
		proposal.Cadence = time.Duration(cadence) * time.Second
		out = append(out, proposal)
	}
	return out, rows.Err()
}

// =============================================================================
// QueryHistory over persisted cycles
// =============================================================================

// RecentQueries derives recent query usage from persisted cycle/finding
// evidence — no separate table. In V1 the queries themselves live in the
// query generation events; to keep restart diversity without a new table,
// the store records query digests via SaveQueryRecord.
func (s *Store) RecentQueries(ctx context.Context, topicID string, since time.Time) ([]string, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT query FROM research_query_records
		WHERE topic_id = $1 AND used_at >= $2
		ORDER BY used_at DESC LIMIT 100`, topicID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var query string
		if err := rows.Scan(&query); err != nil {
			return nil, err
		}
		out = append(out, query)
	}
	return out, rows.Err()
}

// RecordQuery stores a used query for diversity enforcement. The auxiliary
// research_query_records table is part of migration 000001; query history
// survives restarts, which is what prevents the "same query every 10
// minutes forever" loop.
func (s *Store) RecordQuery(ctx context.Context, topicID, query string) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO research_query_records (topic_id, query) VALUES ($1, $2)`,
		topicID, query)
	return err
}

// Compile-time interface proofs.
var (
	_ search.NeedRepository    = (*Store)(nil)
	_ search.TopicRepository   = (*Store)(nil)
	_ search.CycleRepository   = (*Store)(nil)
	_ search.FindingRepository = (*Store)(nil)
	_ search.QueryHistory      = (*Store)(nil)
)

// Ensure fmt stays used if code shifts.
var _ = fmt.Sprintf

// =============================================================================
// Durable scheduler boundaries
// =============================================================================

// TryClaim adapts the atomic SQL claim to the scheduler's claim manager.
func (s *Store) TryClaim(ctx context.Context, topicID, owner string, ttl time.Duration) (bool, error) {
	_, err := s.TryClaimTopic(ctx, topicID, owner, ttl)
	if errors.Is(err, ErrClaimLost) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// Release adapts claim release.
func (s *Store) Release(ctx context.Context, topicID, owner string) error {
	return s.ReleaseClaim(ctx, topicID, owner)
}

// EvidenceSeen answers the novelty question against durable findings: it
// loads the topic's evidence refs since the cutoff and compares canonical
// keys computed with the same rules the scheduler uses.
func (s *Store) EvidenceSeen(ctx context.Context, topicID, key string, since time.Time) (bool, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT evidence FROM research_findings
		WHERE topic_id = $1 AND created_at >= $2`, topicID, since)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return false, err
		}
		for _, ref := range evidenceFromJSON(raw) {
			if search.EvidenceRefKey(ref) == key {
				return true, nil
			}
		}
	}
	return false, rows.Err()
}

// RecycleUnfinishedCycles implements restart semantics: cycles left open by
// a dead process are marked failed (never pretended complete) and their
// topic claims released so the work is recoverable on the next tick.
func (s *Store) RecycleUnfinishedCycles(ctx context.Context, olderThan time.Duration) (int, error) {
	stale, err := s.UnfinishedCycles(ctx, olderThan)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, cycle := range stale {
		if err := s.MarkCycleFailed(ctx, cycle.ID, "interrupted_by_restart"); err != nil {
			return count, err
		}
		if err := s.ReleaseClaim(ctx, cycle.TopicID, "scheduler"); err == nil {
			count++
		}
	}
	return count, nil
}

// Compile-time proofs for the durable scheduler boundaries.
var (
	_ search.TopicClaimManager = (*Store)(nil)
	_ search.EvidenceIndex     = (*Store)(nil)
)
