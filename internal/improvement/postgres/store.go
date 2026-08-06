// Package postgres is the durable CandidateStore for internal/improvement:
// it persists Candidate aggregates and their promotion-decision audit trail
// (migration 000013), with optimistic concurrency (a per-row revision
// counter) and a database-level state-machine guard mirroring
// internal/improvement/transitions.go, so a direct SQL write can never
// perform an unlisted candidate state transition either.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/improvement"
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
		return nil, errors.New("improvement store requires initialized PostgreSQL")
	}
	organizationID = strings.TrimSpace(organizationID)
	if organizationID == "" {
		return nil, errors.New("improvement store requires an organization id")
	}
	return &Store{pool: store.Pool(), organizationID: organizationID}, nil
}

var _ improvement.CandidateStore = (*Store)(nil)

func (s *Store) ProposeCandidate(ctx context.Context, candidate improvement.Candidate, createdBy string) (int64, error) {
	if err := candidate.Validate(); err != nil {
		return 0, err
	}
	if candidate.State != improvement.StateProposed {
		return 0, fmt.Errorf("%w: a newly persisted candidate must start proposed, got %q", improvement.ErrInvalidCandidate, candidate.State)
	}
	createdBy = strings.TrimSpace(createdBy)
	if createdBy == "" {
		return 0, fmt.Errorf("%w: created_by is required", improvement.ErrInvalidCandidate)
	}

	var revision int64
	if err := s.pool.QueryRow(ctx, `
INSERT INTO improvement_candidates (
    organization_id, candidate_key, artifact_id, artifact_content_hash, artifact_schema_version,
    parent_candidate_key, parent_artifact_hash, derived_from,
    state, proposed_at, updated_at, created_by
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
RETURNING revision`,
		s.organizationID, candidate.ID,
		candidate.Artifact.ArtifactID, candidate.Artifact.ContentHash, candidate.Artifact.SchemaVersion,
		nullableString(candidate.Lineage.ParentCandidateID), nullableString(candidate.Lineage.ParentArtifactHash), nullableString(candidate.Lineage.DerivedFrom),
		string(candidate.State), candidate.ProposedAt, candidate.UpdatedAt, createdBy,
	).Scan(&revision); err != nil {
		if isUniqueViolation(err) {
			return 0, fmt.Errorf("%w: candidate %s already exists", improvement.ErrInvalidCandidate, candidate.ID)
		}
		return 0, fmt.Errorf("improvement/postgres: propose candidate %s: %w", candidate.ID, err)
	}
	return revision, nil
}

func (s *Store) GetCandidate(ctx context.Context, id string) (improvement.Candidate, int64, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return improvement.Candidate{}, 0, fmt.Errorf("%w: candidate id is required", improvement.ErrInvalidCandidate)
	}
	row := s.pool.QueryRow(ctx, `
SELECT artifact_id, artifact_content_hash, artifact_schema_version,
       parent_candidate_key, parent_artifact_hash, derived_from,
       state, proposed_at, updated_at,
       rollback_target_candidate_key, rollback_target_artifact_hash, rollback_from_state,
       revision
FROM improvement_candidates
WHERE organization_id=$1 AND candidate_key=$2`, s.organizationID, id)
	candidate, revision, err := scanCandidate(id, row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return improvement.Candidate{}, 0, fmt.Errorf("%w: candidate %s", improvement.ErrCandidateNotFound, id)
		}
		return improvement.Candidate{}, 0, fmt.Errorf("improvement/postgres: get candidate %s: %w", id, err)
	}
	return candidate, revision, nil
}

func (s *Store) SaveCandidate(ctx context.Context, candidate improvement.Candidate, expectedRevision int64) (int64, error) {
	if err := candidate.Validate(); err != nil {
		return 0, err
	}
	if expectedRevision <= 0 {
		return 0, fmt.Errorf("%w: expected revision must be positive", improvement.ErrRevisionConflict)
	}

	var rollbackKey, rollbackHash, rollbackFrom *string
	if candidate.RollbackTarget != nil {
		rollbackKey = nullableString(candidate.RollbackTarget.CandidateID)
		rollbackHash = nullableString(candidate.RollbackTarget.ArtifactHash)
		fromState := string(candidate.RollbackTarget.FromState)
		rollbackFrom = &fromState
	}

	command, err := s.pool.Exec(ctx, `
UPDATE improvement_candidates
SET state=$3, updated_at=$4,
    rollback_target_candidate_key=$5, rollback_target_artifact_hash=$6, rollback_from_state=$7,
    revision=revision+1
WHERE organization_id=$1 AND candidate_key=$2 AND revision=$8`,
		s.organizationID, candidate.ID, string(candidate.State), candidate.UpdatedAt,
		rollbackKey, rollbackHash, rollbackFrom, expectedRevision,
	)
	if err != nil {
		return 0, fmt.Errorf("improvement/postgres: save candidate %s: %w", candidate.ID, err)
	}
	if command.RowsAffected() != 1 {
		if _, _, getErr := s.GetCandidate(ctx, candidate.ID); errors.Is(getErr, improvement.ErrCandidateNotFound) {
			return 0, getErr
		}
		return 0, fmt.Errorf("%w: candidate %s", improvement.ErrRevisionConflict, candidate.ID)
	}
	return expectedRevision + 1, nil
}

func (s *Store) RecordPromotionDecision(ctx context.Context, request improvement.PromotionRequest, decision improvement.PromotionDecision) error {
	if err := request.Validate(); err != nil {
		return err
	}
	if err := decision.Validate(); err != nil {
		return err
	}
	if request.CandidateID != decision.CandidateID || request.Kind != decision.Kind {
		return fmt.Errorf("%w: decision does not match request for candidate %s", improvement.ErrInvalidPromotionDecision, request.CandidateID)
	}

	var candidateID int64
	if err := s.pool.QueryRow(ctx, `
SELECT id FROM improvement_candidates WHERE organization_id=$1 AND candidate_key=$2`,
		s.organizationID, request.CandidateID,
	).Scan(&candidateID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: candidate %s", improvement.ErrCandidateNotFound, request.CandidateID)
		}
		return fmt.Errorf("improvement/postgres: locate candidate %s: %w", request.CandidateID, err)
	}

	if _, err := s.pool.Exec(ctx, `
INSERT INTO improvement_promotion_decisions (
    organization_id, candidate_id, kind, outcome, reason,
    from_state, to_state, evaluation_suite_id, evaluation_overall_verdict,
    requested_at, requested_by, decided_at, decided_by
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
		s.organizationID, candidateID, string(request.Kind), string(decision.Outcome), nullableString(decision.Reason),
		string(request.FromState), string(request.ToState), request.Evaluation.SuiteID, string(request.Evaluation.OverallVerdict),
		request.RequestedAt, request.RequestedBy, decision.DecidedAt, decision.DecidedBy,
	); err != nil {
		return fmt.Errorf("improvement/postgres: record promotion decision for candidate %s: %w", request.CandidateID, err)
	}
	return nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanCandidate(id string, row rowScanner) (improvement.Candidate, int64, error) {
	var artifactID, artifactHash, artifactSchema string
	var parentKey, parentHash, derivedFrom *string
	var state string
	var proposedAt, updatedAt time.Time
	var rollbackKey, rollbackHash, rollbackFrom *string
	var revision int64
	if err := row.Scan(
		&artifactID, &artifactHash, &artifactSchema,
		&parentKey, &parentHash, &derivedFrom,
		&state, &proposedAt, &updatedAt,
		&rollbackKey, &rollbackHash, &rollbackFrom,
		&revision,
	); err != nil {
		return improvement.Candidate{}, 0, err
	}

	candidate := improvement.Candidate{
		ID: id,
		Artifact: improvement.ArtifactRef{
			ArtifactID: artifactID, ContentHash: artifactHash, SchemaVersion: artifactSchema,
		},
		Lineage: improvement.Lineage{
			ParentCandidateID:  stringOrEmpty(parentKey),
			ParentArtifactHash: stringOrEmpty(parentHash),
			DerivedFrom:        stringOrEmpty(derivedFrom),
		},
		State:      improvement.CandidateState(state),
		ProposedAt: proposedAt.UTC(),
		UpdatedAt:  updatedAt.UTC(),
	}
	if rollbackKey != nil {
		candidate.RollbackTarget = &improvement.RollbackTarget{
			CandidateID:  *rollbackKey,
			ArtifactHash: stringOrEmpty(rollbackHash),
			FromState:    improvement.CandidateState(stringOrEmpty(rollbackFrom)),
		}
	}
	return candidate, revision, nil
}

func stringOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func nullableString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
