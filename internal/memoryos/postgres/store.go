package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/memoryos/consolidation"
	"github.com/Mireuz13/explorarte-organization/internal/memoryos/episode"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool           *pgxpool.Pool
	organizationID string
}

func New(store *platformpostgres.Store, organizationID string) (*Store, error) {
	if store == nil || store.Pool() == nil {
		return nil, errors.New("memoryos postgres: store requires an initialized PostgreSQL pool")
	}
	organizationID = strings.TrimSpace(organizationID)
	if organizationID == "" {
		return nil, errors.New("memoryos postgres: organization scope is required")
	}
	return &Store{pool: store.Pool(), organizationID: organizationID}, nil
}

func NewWithPool(pool *pgxpool.Pool, organizationID string) (*Store, error) {
	if pool == nil {
		return nil, errors.New("memoryos postgres: store requires an initialized PostgreSQL pool")
	}
	organizationID = strings.TrimSpace(organizationID)
	if organizationID == "" {
		return nil, errors.New("memoryos postgres: organization scope is required")
	}
	return &Store{pool: pool, organizationID: organizationID}, nil
}

func (s *Store) Pool() *pgxpool.Pool {
	return s.pool
}

func (s *Store) OrganizationID() string {
	return s.organizationID
}

// RecordCompletionObservation stores host-verified completion facts.
func (s *Store) RecordCompletionObservation(ctx context.Context, obs episode.CompletionObservation) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(obs.OrganizationID) == "" || obs.TaskID <= 0 || obs.AttemptID <= 0 || strings.TrimSpace(obs.ObservationDigest) == "" {
		return errors.New("memoryos postgres: completion observation identity is incomplete")
	}
	obligationsJSON, err := json.Marshal(obs.Obligations)
	if err != nil {
		return fmt.Errorf("memoryos postgres: marshal obligations: %w", err)
	}

	_, err = s.pool.Exec(ctx, `
		INSERT INTO memoryos_completion_observations (
			organization_id, task_id, attempt_id, observation_digest, verdict, verified_at, obligations
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (organization_id, task_id, attempt_id, observation_digest) DO NOTHING
	`, obs.OrganizationID, obs.TaskID, obs.AttemptID, obs.ObservationDigest, obs.Verdict, obs.VerifiedAt.UTC(), obligationsJSON)
	return mapError(err)
}

// SaveEpisode persists an Episode and its child collections into PostgreSQL.
// It is idempotent: if the episode already exists with the same canonical digest,
// it is returned as reused (reused = true). If it exists with a different digest,
// a new revision is stored.
func (s *Store) SaveEpisode(ctx context.Context, ep episode.Episode) (episode.Episode, bool, error) {
	if err := ctx.Err(); err != nil {
		return episode.Episode{}, false, err
	}
	if ep.OrganizationID != s.organizationID {
		return episode.Episode{}, false, errors.New("memoryos postgres: episode organization is outside store scope")
	}
	if strings.TrimSpace(ep.ID) == "" || strings.TrimSpace(ep.HarnessRunID) == "" || strings.TrimSpace(ep.CanonicalDigest) == "" {
		return episode.Episode{}, false, errors.New("memoryos postgres: episode identity is incomplete")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return episode.Episode{}, false, mapError(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Check if already exists with exact canonical digest
	var existingRevision int64
	err = tx.QueryRow(ctx, `
		SELECT revision
		FROM memoryos_episodes
		WHERE organization_id = $1 AND harness_run_id = $2 AND canonical_digest = $3
		ORDER BY revision DESC LIMIT 1
	`, ep.OrganizationID, ep.HarnessRunID, ep.CanonicalDigest).Scan(&existingRevision)
	if err == nil {
		// Existing exact match found -> reused!
		existing, ok, getErr := s.getEpisodeTx(ctx, tx, ep.OrganizationID, ep.ID, existingRevision)
		if getErr != nil {
			return episode.Episode{}, false, getErr
		}
		if ok {
			_ = tx.Commit(ctx)
			return existing, true, nil
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return episode.Episode{}, false, mapError(err)
	}

	// Get latest revision for this harness run
	var maxRevision int64
	_ = tx.QueryRow(ctx, `
		SELECT COALESCE(MAX(revision), 0)
		FROM memoryos_episodes
		WHERE organization_id = $1 AND harness_run_id = $2
	`, ep.OrganizationID, ep.HarnessRunID).Scan(&maxRevision)
	newRevision := maxRevision + 1

	incompleteReasonsJSON, err := json.Marshal(ep.Observability.IncompleteReasons)
	if err != nil {
		incompleteReasonsJSON = []byte("[]")
	}

	var verifVerdict *string
	var verifScope *string
	var verifTime *time.Time
	var verifDecisionID *int64
	if ep.Verification != nil {
		v := ep.Verification.Verdict
		verifVerdict = &v
		s := ep.Verification.Scope
		if s == "" {
			s = episode.VerificationScopeAttempt
		}
		verifScope = &s
		if ep.Verification.VerifiedAt != nil {
			t := ep.Verification.VerifiedAt.UTC()
			verifTime = &t
		} else {
			now := time.Now().UTC()
			verifTime = &now
		}
		verifDecisionID = ep.Verification.DecisionRunID
	}

	var startedAt, finishedAt *time.Time
	if ep.StartedAt != nil {
		t := ep.StartedAt.UTC()
		startedAt = &t
	}
	if ep.FinishedAt != nil {
		t := ep.FinishedAt.UTC()
		finishedAt = &t
	}

	snapshotID, _ := strconv.ParseInt(ep.Context.SnapshotID, 10, 64)

	_, err = tx.Exec(ctx, `
		INSERT INTO memoryos_episodes (
			id, organization_id, harness_run_id, revision, canonical_digest,
			task_id, attempt_id, decision_run_id, role_id, execution_principal_id,
			task_class, execution_purpose, execution_profile_id,
			context_snapshot_id, context_snapshot_version, context_snapshot_digest,
			context_provider_visible_digest, execution_context_view_id,
			binding_mode, turns_used, tool_calls_used,
			actual_cost_usd_nanos, estimated_cost_usd_nanos,
			terminal_status, status, event_count, source_facts_digest, incomplete_reasons,
			verification_verdict, verification_scope, verification_verified_at, verification_decision_run_id,
			started_at, finished_at
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, $8, $9, $10,
			$11, $12, $13,
			$14, $15, $16,
			$17, $18,
			$19, $20, $21,
			$22, $23,
			$24, $25, $26, $27, $28,
			$29, $30, $31, $32,
			$33, $34
		)
	`,
		ep.ID, ep.OrganizationID, ep.HarnessRunID, newRevision, ep.CanonicalDigest,
		ep.TaskID, ep.AttemptID, ep.DecisionRunID, ep.RoleID, ep.ExecutionPrincipalID,
		ep.TaskClass, ep.ExecutionPurpose, ep.ExecutionProfileID,
		snapshotID, ep.Context.SnapshotVersion, ep.Context.SnapshotDigest,
		ep.Context.ProviderVisibleDigest, ep.Context.ExecutionContextViewID,
		string(ep.BindingMode), ep.TurnsUsed, ep.ToolCallsUsed,
		ep.ActualCostUSDNanos, ep.EstimatedCostUSDNanos,
		ep.TerminalStatus, ep.Status, ep.Observability.EventCount, ep.Observability.SourceFactsDigest, incompleteReasonsJSON,
		verifVerdict, verifScope, verifTime, verifDecisionID,
		startedAt, finishedAt,
	)
	if err != nil {
		return episode.Episode{}, false, mapError(err)
	}

	// Insert Skills
	for i, sk := range ep.Skills {
		_, err = tx.Exec(ctx, `
			INSERT INTO memoryos_episode_skills (
				episode_id, episode_revision, ordinal, skill_id, version, content_hash,
				available, requested, resolved, included
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		`, ep.ID, newRevision, i+1, sk.SkillID, sk.Version, sk.ContentHash,
			sk.Available, sk.Requested, sk.Resolved, sk.Included)
		if err != nil {
			return episode.Episode{}, false, mapError(err)
		}
	}

	// Insert Invocations
	for _, inv := range ep.Invocations {
		var invCreated, invTerm *time.Time
		if !inv.CreatedAt.IsZero() {
			t := inv.CreatedAt.UTC()
			invCreated = &t
		}
		if inv.TerminalAt != nil {
			t := inv.TerminalAt.UTC()
			invTerm = &t
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO memoryos_episode_invocations (
				episode_id, episode_revision, organization_id, invocation_id,
				provider_id, provider_model_id, input_tokens, output_tokens, reasoning_tokens,
				cost_usd_nanos, estimated_usd_nanos, status, created_at, terminal_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		`, ep.ID, newRevision, ep.OrganizationID, inv.InvocationID,
			inv.ProviderID, inv.ProviderModelID, inv.InputTokens, inv.OutputTokens, inv.ReasoningTokens,
			inv.CostUSDNanos, inv.EstimatedUSDNanos, inv.Status, invCreated, invTerm)
		if err != nil {
			return episode.Episode{}, false, mapError(err)
		}
	}

	// Insert Tools
	for i, tl := range ep.Tools {
		_, err = tx.Exec(ctx, `
			INSERT INTO memoryos_episode_tools (
				episode_id, episode_revision, ordinal, tool_call_id, tool_name,
				definition_digest, outcome, latency_ms, provenance
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		`, ep.ID, newRevision, i+1, tl.ToolCallID, tl.ToolName,
			tl.DefinitionDigest, tl.Outcome, tl.LatencyMS, tl.Provenance)
		if err != nil {
			return episode.Episode{}, false, mapError(err)
		}
	}

	// Insert Obligations
	if ep.Verification != nil {
		for i, ob := range ep.Verification.Obligations {
			refs := ob.EvidenceRefs
			if refs == nil {
				refs = []string{}
			}
			evidenceRefsJSON, err := json.Marshal(refs)
			if err != nil {
				evidenceRefsJSON = []byte("[]")
			}
			var evDigest *string
			if ob.EvidenceDigest != "" {
				evDigest = &ob.EvidenceDigest
			}
			_, err = tx.Exec(ctx, `
				INSERT INTO memoryos_episode_obligations (
					episode_id, episode_revision, ordinal, obligation_key, obligation_kind,
					label, verifier_ref, verifier_version, evidence_digest, evidence_refs
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			`, ep.ID, newRevision, i+1, ob.Key, ob.Kind,
				ob.Label, ob.VerifierRef, ob.VerifierVersion, evDigest, evidenceRefsJSON)
			if err != nil {
				return episode.Episode{}, false, mapError(err)
			}
		}
	}

	if err = tx.Commit(ctx); err != nil {
		return episode.Episode{}, false, mapError(err)
	}
	return ep, false, nil
}

func (s *Store) GetEpisode(ctx context.Context, organizationID, episodeID string) (episode.Episode, bool, error) {
	if err := ctx.Err(); err != nil {
		return episode.Episode{}, false, err
	}
	return s.getEpisodeTx(ctx, s.pool, organizationID, episodeID, 0)
}

func (s *Store) Get(ctx context.Context, organizationID, episodeID string) (episode.Episode, error) {
	ep, ok, err := s.GetEpisode(ctx, organizationID, episodeID)
	if err != nil {
		return episode.Episode{}, err
	}
	if !ok {
		return episode.Episode{}, fmt.Errorf("memoryos postgres: episode %s not found", episodeID)
	}
	return ep, nil
}

type queryable interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

func (s *Store) getEpisodeTx(ctx context.Context, q queryable, organizationID, episodeID string, specificRevision int64) (episode.Episode, bool, error) {
	var query string
	var args []any
	if specificRevision > 0 {
		query = `
			SELECT
				id, organization_id, harness_run_id, revision, canonical_digest,
				task_id, attempt_id, decision_run_id, role_id, execution_principal_id,
				task_class, execution_purpose, execution_profile_id,
				context_snapshot_id, context_snapshot_version, context_snapshot_digest,
				context_provider_visible_digest, execution_context_view_id,
				binding_mode, turns_used, tool_calls_used,
				actual_cost_usd_nanos, estimated_cost_usd_nanos,
				terminal_status, status, event_count, source_facts_digest, incomplete_reasons,
				verification_verdict, verification_scope, verification_verified_at, verification_decision_run_id,
				started_at, finished_at
			FROM memoryos_episodes
			WHERE organization_id = $1 AND id = $2 AND revision = $3
		`
		args = []any{organizationID, episodeID, specificRevision}
	} else {
		query = `
			SELECT
				id, organization_id, harness_run_id, revision, canonical_digest,
				task_id, attempt_id, decision_run_id, role_id, execution_principal_id,
				task_class, execution_purpose, execution_profile_id,
				context_snapshot_id, context_snapshot_version, context_snapshot_digest,
				context_provider_visible_digest, execution_context_view_id,
				binding_mode, turns_used, tool_calls_used,
				actual_cost_usd_nanos, estimated_cost_usd_nanos,
				terminal_status, status, event_count, source_facts_digest, incomplete_reasons,
				verification_verdict, verification_scope, verification_verified_at, verification_decision_run_id,
				started_at, finished_at
			FROM memoryos_episodes
			WHERE organization_id = $1 AND id = $2
			ORDER BY revision DESC LIMIT 1
		`
		args = []any{organizationID, episodeID}
	}

	var ep episode.Episode
	var revision int64
	var contextSnapshotID int64
	var bindingModeStr string
	var incompleteReasonsJSON []byte
	var verifVerdict, verifScope *string
	var verifTime *time.Time
	var verifDecisionID *int64

	row := q.QueryRow(ctx, query, args...)
	err := row.Scan(
		&ep.ID, &ep.OrganizationID, &ep.HarnessRunID, &revision, &ep.CanonicalDigest,
		&ep.TaskID, &ep.AttemptID, &ep.DecisionRunID, &ep.RoleID, &ep.ExecutionPrincipalID,
		&ep.TaskClass, &ep.ExecutionPurpose, &ep.ExecutionProfileID,
		&contextSnapshotID, &ep.Context.SnapshotVersion, &ep.Context.SnapshotDigest,
		&ep.Context.ProviderVisibleDigest, &ep.Context.ExecutionContextViewID,
		&bindingModeStr, &ep.TurnsUsed, &ep.ToolCallsUsed,
		&ep.ActualCostUSDNanos, &ep.EstimatedCostUSDNanos,
		&ep.TerminalStatus, &ep.Status, &ep.Observability.EventCount, &ep.Observability.SourceFactsDigest, &incompleteReasonsJSON,
		&verifVerdict, &verifScope, &verifTime, &verifDecisionID,
		&ep.StartedAt, &ep.FinishedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return episode.Episode{}, false, nil
	}
	if err != nil {
		return episode.Episode{}, false, mapError(err)
	}

	ep.BindingMode = episode.BindingMode(bindingModeStr)
	ep.Context.SnapshotID = strconv.FormatInt(contextSnapshotID, 10)
	ep.Context.TaskClass = ep.TaskClass
	ep.Context.ExecutionPurpose = ep.ExecutionPurpose
	_ = json.Unmarshal(incompleteReasonsJSON, &ep.Observability.IncompleteReasons)
	ep.Observability.Incomplete = len(ep.Observability.IncompleteReasons) > 0

	// Read Skills
	skillRows, err := q.Query(ctx, `
		SELECT skill_id, version, content_hash, available, requested, resolved, included
		FROM memoryos_episode_skills
		WHERE episode_id = $1 AND episode_revision = $2
		ORDER BY ordinal ASC
	`, ep.ID, revision)
	if err != nil {
		return episode.Episode{}, false, mapError(err)
	}
	defer skillRows.Close()
	ep.Skills = make([]episode.SkillUse, 0)
	for skillRows.Next() {
		var sk episode.SkillUse
		if err := skillRows.Scan(&sk.SkillID, &sk.Version, &sk.ContentHash, &sk.Available, &sk.Requested, &sk.Resolved, &sk.Included); err != nil {
			return episode.Episode{}, false, mapError(err)
		}
		ep.Skills = append(ep.Skills, sk)
	}

	// Read Invocations
	invRows, err := q.Query(ctx, `
		SELECT invocation_id, provider_id, provider_model_id, input_tokens, output_tokens, reasoning_tokens,
		       cost_usd_nanos, estimated_usd_nanos, status, created_at, terminal_at
		FROM memoryos_episode_invocations
		WHERE episode_id = $1 AND episode_revision = $2
		ORDER BY invocation_id ASC
	`, ep.ID, revision)
	if err != nil {
		return episode.Episode{}, false, mapError(err)
	}
	defer invRows.Close()
	ep.Invocations = make([]episode.InvocationUse, 0)
	for invRows.Next() {
		var inv episode.InvocationUse
		var crAt *time.Time
		if err := invRows.Scan(&inv.InvocationID, &inv.ProviderID, &inv.ProviderModelID,
			&inv.InputTokens, &inv.OutputTokens, &inv.ReasoningTokens,
			&inv.CostUSDNanos, &inv.EstimatedUSDNanos, &inv.Status, &crAt, &inv.TerminalAt); err != nil {
			return episode.Episode{}, false, mapError(err)
		}
		if crAt != nil {
			inv.CreatedAt = *crAt
		}
		ep.Invocations = append(ep.Invocations, inv)
	}

	// Read Tools
	toolRows, err := q.Query(ctx, `
		SELECT tool_call_id, tool_name, definition_digest, outcome, latency_ms, provenance
		FROM memoryos_episode_tools
		WHERE episode_id = $1 AND episode_revision = $2
		ORDER BY ordinal ASC
	`, ep.ID, revision)
	if err != nil {
		return episode.Episode{}, false, mapError(err)
	}
	defer toolRows.Close()
	ep.Tools = make([]episode.ToolUse, 0)
	for toolRows.Next() {
		var tl episode.ToolUse
		if err := toolRows.Scan(&tl.ToolCallID, &tl.ToolName, &tl.DefinitionDigest, &tl.Outcome, &tl.LatencyMS, &tl.Provenance); err != nil {
			return episode.Episode{}, false, mapError(err)
		}
		ep.Tools = append(ep.Tools, tl)
	}

	// Read Obligations
	if verifVerdict != nil {
		obRows, err := q.Query(ctx, `
			SELECT obligation_key, obligation_kind, label, verifier_ref, verifier_version, evidence_digest, evidence_refs
			FROM memoryos_episode_obligations
			WHERE episode_id = $1 AND episode_revision = $2
			ORDER BY ordinal ASC
		`, ep.ID, revision)
		if err != nil {
			return episode.Episode{}, false, mapError(err)
		}
		defer obRows.Close()
		obligations := make([]episode.ObligationObservation, 0)
		for obRows.Next() {
			var ob episode.ObligationObservation
			var evRefsJSON []byte
			var evDigest *string
			if err := obRows.Scan(&ob.Key, &ob.Kind, &ob.Label, &ob.VerifierRef, &ob.VerifierVersion, &evDigest, &evRefsJSON); err != nil {
				return episode.Episode{}, false, mapError(err)
			}
			if evDigest != nil {
				ob.EvidenceDigest = *evDigest
			}
			_ = json.Unmarshal(evRefsJSON, &ob.EvidenceRefs)
			obligations = append(obligations, ob)
		}
		scope := ""
		if verifScope != nil {
			scope = *verifScope
		}
		var evRefs []string
		if verifDecisionID != nil {
			evRefs = []string{fmt.Sprintf("decisiongraph:run:%d", *verifDecisionID)}
		}
		ep.Verification = &episode.VerificationSummary{
			Verdict:       *verifVerdict,
			Scope:         scope,
			VerifiedAt:    verifTime,
			DecisionRunID: verifDecisionID,
			EvidenceRefs:  evRefs,
			Obligations:   obligations,
		}
	}

	return ep, true, nil
}

func (s *Store) ListEpisodes(ctx context.Context, organizationID string, from, to time.Time, limit int) ([]episode.Episode, error) {
	return s.List(ctx, organizationID, from, to, limit)
}

func (s *Store) List(ctx context.Context, organizationID string, from, to time.Time, limit int) ([]episode.Episode, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 10000 {
		limit = 1000
	}
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT ON (organization_id, harness_run_id) id
		FROM memoryos_episodes
		WHERE organization_id = $1
		  AND created_at >= $2 AND created_at <= $3
		ORDER BY organization_id, harness_run_id, revision DESC
		LIMIT $4
	`, organizationID, from.UTC(), to.UTC(), limit)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, mapError(err)
		}
		ids = append(ids, id)
	}

	episodes := make([]episode.Episode, 0, len(ids))
	for _, id := range ids {
		ep, ok, err := s.GetEpisode(ctx, organizationID, id)
		if err != nil {
			return nil, err
		}
		if ok {
			episodes = append(episodes, ep)
		}
	}
	return episodes, nil
}

// SaveCluster records a cluster into memoryos_clusters.
func (s *Store) SaveCluster(ctx context.Context, c consolidation.Cluster) (consolidation.Cluster, bool, error) {
	if err := ctx.Err(); err != nil {
		return consolidation.Cluster{}, false, err
	}
	if c.OrganizationID != s.organizationID {
		return consolidation.Cluster{}, false, errors.New("memoryos postgres: cluster organization mismatch")
	}
	if err := c.Validate(); err != nil {
		return consolidation.Cluster{}, false, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return consolidation.Cluster{}, false, mapError(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Check if already exists with same canonical digest and status
	var existingRevision int64
	err = tx.QueryRow(ctx, `
		SELECT revision
		FROM memoryos_clusters
		WHERE organization_id = $1 AND id = $2 AND canonical_digest = $3 AND status = $4
		ORDER BY revision DESC LIMIT 1
	`, c.OrganizationID, c.ID, c.CanonicalDigest, c.Status).Scan(&existingRevision)
	if err == nil {
		// Reused!
		existing, ok, getErr := s.getClusterTx(ctx, tx, c.OrganizationID, c.ID, existingRevision)
		if getErr != nil {
			return consolidation.Cluster{}, false, getErr
		}
		if ok {
			_ = tx.Commit(ctx)
			return existing, true, nil
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return consolidation.Cluster{}, false, mapError(err)
	}

	var maxRevision int64
	_ = tx.QueryRow(ctx, `
		SELECT COALESCE(MAX(revision), 0)
		FROM memoryos_clusters
		WHERE organization_id = $1 AND id = $2
	`, c.OrganizationID, c.ID).Scan(&maxRevision)
	newRevision := maxRevision + 1

	epIDsJSON, _ := json.Marshal(c.EpisodeIDs)
	runRefsJSON, _ := json.Marshal(c.DecisionRunRefs)

	_, err = tx.Exec(ctx, `
		INSERT INTO memoryos_clusters (
			id, organization_id, revision, canonical_digest, cluster_kind,
			role_id, task_class, execution_profile_id, obligation_key, obligation_kind,
			episode_ids, decision_run_refs, pass_count, fail_count,
			first_observed, last_observed, status
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
	`, c.ID, c.OrganizationID, newRevision, c.CanonicalDigest, c.Kind,
		c.RoleID, c.TaskClass, c.ExecutionProfileID, c.ObligationKey, c.ObligationKind,
		epIDsJSON, runRefsJSON, c.PassCount, c.FailCount,
		c.FirstObserved.UTC(), c.LastObserved.UTC(), c.Status)
	if err != nil {
		return consolidation.Cluster{}, false, mapError(err)
	}

	if err = tx.Commit(ctx); err != nil {
		return consolidation.Cluster{}, false, mapError(err)
	}
	c.Revision = newRevision
	return c, false, nil
}

func (s *Store) GetCluster(ctx context.Context, organizationID, clusterID string) (consolidation.Cluster, bool, error) {
	return s.getClusterTx(ctx, s.pool, organizationID, clusterID, 0)
}

func (s *Store) getClusterTx(ctx context.Context, q queryable, organizationID, clusterID string, specificRevision int64) (consolidation.Cluster, bool, error) {
	var query string
	var args []any
	if specificRevision > 0 {
		query = `
			SELECT id, organization_id, revision, canonical_digest, cluster_kind,
			       role_id, task_class, execution_profile_id, obligation_key, obligation_kind,
			       episode_ids, decision_run_refs, pass_count, fail_count,
			       first_observed, last_observed, status, created_at
			FROM memoryos_clusters
			WHERE organization_id = $1 AND id = $2 AND revision = $3
		`
		args = []any{organizationID, clusterID, specificRevision}
	} else {
		query = `
			SELECT id, organization_id, revision, canonical_digest, cluster_kind,
			       role_id, task_class, execution_profile_id, obligation_key, obligation_kind,
			       episode_ids, decision_run_refs, pass_count, fail_count,
			       first_observed, last_observed, status, created_at
			FROM memoryos_clusters
			WHERE organization_id = $1 AND id = $2
			ORDER BY revision DESC LIMIT 1
		`
		args = []any{organizationID, clusterID}
	}

	var c consolidation.Cluster
	var epIDsJSON, runRefsJSON []byte
	row := q.QueryRow(ctx, query, args...)
	err := row.Scan(
		&c.ID, &c.OrganizationID, &c.Revision, &c.CanonicalDigest, &c.Kind,
		&c.RoleID, &c.TaskClass, &c.ExecutionProfileID, &c.ObligationKey, &c.ObligationKind,
		&epIDsJSON, &runRefsJSON, &c.PassCount, &c.FailCount,
		&c.FirstObserved, &c.LastObserved, &c.Status, &c.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return consolidation.Cluster{}, false, nil
	}
	if err != nil {
		return consolidation.Cluster{}, false, mapError(err)
	}
	_ = json.Unmarshal(epIDsJSON, &c.EpisodeIDs)
	_ = json.Unmarshal(runRefsJSON, &c.DecisionRunRefs)
	return c, true, nil
}

func (s *Store) ListClusters(ctx context.Context, organizationID, kind string, limit int) ([]consolidation.Cluster, error) {
	if limit <= 0 || limit > 10000 {
		limit = 1000
	}
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT ON (organization_id, id) id
		FROM memoryos_clusters
		WHERE organization_id = $1 AND cluster_kind = $2
		ORDER BY organization_id, id, revision DESC
		LIMIT $3
	`, organizationID, kind, limit)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, mapError(err)
		}
		ids = append(ids, id)
	}

	clusters := make([]consolidation.Cluster, 0, len(ids))
	for _, id := range ids {
		c, ok, err := s.GetCluster(ctx, organizationID, id)
		if err != nil {
			return nil, err
		}
		if ok {
			clusters = append(clusters, c)
		}
	}
	return clusters, nil
}

func mapError(err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			return fmt.Errorf("memoryos postgres: conflict: %s", pgErr.Message)
		case "23503":
			return fmt.Errorf("memoryos postgres: foreign key constraint violation: %s", pgErr.Message)
		case "23514":
			return fmt.Errorf("memoryos postgres: check constraint violation: %s", pgErr.Message)
		}
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	return err
}

var (
	_ consolidation.EpisodeReader = (*Store)(nil)
	_ consolidation.ClusterStore  = (*Store)(nil)
)
