package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/skillforge/need"
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
		return nil, errors.New("need postgres: store requires an initialized PostgreSQL pool")
	}
	organizationID = strings.TrimSpace(organizationID)
	if organizationID == "" {
		return nil, errors.New("need postgres: organization scope is required")
	}
	return &Store{pool: store.Pool(), organizationID: organizationID}, nil
}

func (s *Store) CreateNeed(ctx context.Context, n need.ProcedureNeed) (need.ProcedureNeed, error) {
	if err := n.Validate(); err != nil {
		return need.ProcedureNeed{}, err
	}

	episodesJSON, err := json.Marshal(n.EpisodeRefs)
	if err != nil {
		return need.ProcedureNeed{}, fmt.Errorf("encode episode refs: %w", err)
	}
	clustersJSON, err := json.Marshal(n.ClusterRefs)
	if err != nil {
		return need.ProcedureNeed{}, fmt.Errorf("encode cluster refs: %w", err)
	}
	evidenceJSON, err := json.Marshal(n.EvidenceRefs)
	if err != nil {
		return need.ProcedureNeed{}, fmt.Errorf("encode evidence refs: %w", err)
	}
	skillsJSON, err := json.Marshal(n.SuggestedSkillIDs)
	if err != nil {
		return need.ProcedureNeed{}, fmt.Errorf("encode suggested skills: %w", err)
	}
	var acceptanceJSON []byte
	if n.Acceptance != nil {
		acceptanceJSON, err = json.Marshal(n.Acceptance)
		if err != nil {
			return need.ProcedureNeed{}, fmt.Errorf("encode acceptance: %w", err)
		}
	}

	_, err = s.pool.Exec(ctx, `
		INSERT INTO skillforge_procedure_needs (
			id, organization_id, revision, role_id, task_class, execution_profile_id,
			problem_statement, episode_refs, cluster_refs, evidence_refs, suggested_skill_ids,
			status, acceptance, canonical_digest, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16
		)
	`, n.ID, n.OrganizationID, n.Revision, n.RoleID, n.TaskClass, n.ExecutionProfileID,
		n.ProblemStatement, episodesJSON, clustersJSON, evidenceJSON, skillsJSON,
		n.Status, acceptanceJSON, n.CanonicalDigest, n.CreatedAt, n.UpdatedAt)

	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return need.ProcedureNeed{}, need.ErrConflict
		}
		return need.ProcedureNeed{}, fmt.Errorf("insert procedure need: %w", err)
	}

	return n, nil
}

func (s *Store) GetNeed(ctx context.Context, organizationID, needID string) (need.ProcedureNeed, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT
			id, organization_id, revision, role_id, task_class, execution_profile_id,
			problem_statement, episode_refs, cluster_refs, evidence_refs, suggested_skill_ids,
			status, acceptance, canonical_digest, created_at, updated_at
		FROM skillforge_procedure_needs
		WHERE organization_id = $1 AND id = $2
		ORDER BY revision DESC
		LIMIT 1
	`, organizationID, needID)

	var n need.ProcedureNeed
	var episodesJSON, clustersJSON, evidenceJSON, skillsJSON, acceptanceJSON []byte
	var taskClass, profileID *string

	err := row.Scan(
		&n.ID, &n.OrganizationID, &n.Revision, &n.RoleID, &taskClass, &profileID,
		&n.ProblemStatement, &episodesJSON, &clustersJSON, &evidenceJSON, &skillsJSON,
		&n.Status, &acceptanceJSON, &n.CanonicalDigest, &n.CreatedAt, &n.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return need.ProcedureNeed{}, need.ErrNotFound
		}
		return need.ProcedureNeed{}, fmt.Errorf("query procedure need %s: %w", needID, err)
	}

	if taskClass != nil {
		n.TaskClass = *taskClass
	}
	if profileID != nil {
		n.ExecutionProfileID = *profileID
	}
	_ = json.Unmarshal(episodesJSON, &n.EpisodeRefs)
	_ = json.Unmarshal(clustersJSON, &n.ClusterRefs)
	_ = json.Unmarshal(evidenceJSON, &n.EvidenceRefs)
	_ = json.Unmarshal(skillsJSON, &n.SuggestedSkillIDs)
	if len(acceptanceJSON) > 0 {
		var acc need.Acceptance
		if err := json.Unmarshal(acceptanceJSON, &acc); err == nil {
			n.Acceptance = &acc
		}
	}

	return n, nil
}

func (s *Store) ListNeeds(ctx context.Context, organizationID string, status need.ProcedureNeedStatus) ([]need.ProcedureNeed, error) {
	query := `
		SELECT DISTINCT ON (organization_id, id)
			id, organization_id, revision, role_id, task_class, execution_profile_id,
			problem_statement, episode_refs, cluster_refs, evidence_refs, suggested_skill_ids,
			status, acceptance, canonical_digest, created_at, updated_at
		FROM skillforge_procedure_needs
		WHERE organization_id = $1
	`
	args := []any{organizationID}
	if status != "" {
		query += ` AND status = $2`
		args = append(args, status)
	}
	query += ` ORDER BY organization_id, id, revision DESC`

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list procedure needs: %w", err)
	}
	defer rows.Close()

	var out []need.ProcedureNeed
	for rows.Next() {
		var n need.ProcedureNeed
		var episodesJSON, clustersJSON, evidenceJSON, skillsJSON, acceptanceJSON []byte
		var taskClass, profileID *string

		if err := rows.Scan(
			&n.ID, &n.OrganizationID, &n.Revision, &n.RoleID, &taskClass, &profileID,
			&n.ProblemStatement, &episodesJSON, &clustersJSON, &evidenceJSON, &skillsJSON,
			&n.Status, &acceptanceJSON, &n.CanonicalDigest, &n.CreatedAt, &n.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan procedure need: %w", err)
		}

		if taskClass != nil {
			n.TaskClass = *taskClass
		}
		if profileID != nil {
			n.ExecutionProfileID = *profileID
		}
		_ = json.Unmarshal(episodesJSON, &n.EpisodeRefs)
		_ = json.Unmarshal(clustersJSON, &n.ClusterRefs)
		_ = json.Unmarshal(evidenceJSON, &n.EvidenceRefs)
		_ = json.Unmarshal(skillsJSON, &n.SuggestedSkillIDs)
		if len(acceptanceJSON) > 0 {
			var acc need.Acceptance
			if err := json.Unmarshal(acceptanceJSON, &acc); err == nil {
				n.Acceptance = &acc
			}
		}
		out = append(out, n)
	}

	return out, nil
}

func (s *Store) SaveNeed(ctx context.Context, n need.ProcedureNeed, expectedRevision int64) (need.ProcedureNeed, error) {
	current, err := s.GetNeed(ctx, n.OrganizationID, n.ID)
	if err != nil {
		return need.ProcedureNeed{}, err
	}

	if current.Revision != expectedRevision {
		return need.ProcedureNeed{}, fmt.Errorf("%w: expected %d, got %d", need.ErrRevisionMismatch, expectedRevision, current.Revision)
	}

	n.Revision = current.Revision + 1
	n.UpdatedAt = time.Now().UTC()
	if err := n.Validate(); err != nil {
		return need.ProcedureNeed{}, err
	}

	episodesJSON, _ := json.Marshal(n.EpisodeRefs)
	clustersJSON, _ := json.Marshal(n.ClusterRefs)
	evidenceJSON, _ := json.Marshal(n.EvidenceRefs)
	skillsJSON, _ := json.Marshal(n.SuggestedSkillIDs)
	var acceptanceJSON []byte
	if n.Acceptance != nil {
		acceptanceJSON, _ = json.Marshal(n.Acceptance)
	}

	_, err = s.pool.Exec(ctx, `
		INSERT INTO skillforge_procedure_needs (
			id, organization_id, revision, role_id, task_class, execution_profile_id,
			problem_statement, episode_refs, cluster_refs, evidence_refs, suggested_skill_ids,
			status, acceptance, canonical_digest, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16
		)
	`, n.ID, n.OrganizationID, n.Revision, n.RoleID, n.TaskClass, n.ExecutionProfileID,
		n.ProblemStatement, episodesJSON, clustersJSON, evidenceJSON, skillsJSON,
		n.Status, acceptanceJSON, n.CanonicalDigest, n.CreatedAt, n.UpdatedAt)

	if err != nil {
		return need.ProcedureNeed{}, fmt.Errorf("save procedure need update: %w", err)
	}

	return n, nil
}
