package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/skillforge"
	"github.com/Mireuz13/explorarte-organization/internal/skillforge/source"
	"github.com/Mireuz13/explorarte-organization/internal/skillregistry"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type RunStore struct {
	pool           *pgxpool.Pool
	organizationID string
}

func NewRunStore(store *platformpostgres.Store, organizationID string) (*RunStore, error) {
	if store == nil || store.Pool() == nil {
		return nil, errors.New("skillforge run postgres: store requires an initialized PostgreSQL pool")
	}
	organizationID = strings.TrimSpace(organizationID)
	if organizationID == "" {
		return nil, errors.New("skillforge run postgres: organization scope is required")
	}
	return &RunStore{pool: store.Pool(), organizationID: organizationID}, nil
}

func (s *RunStore) CreateRun(ctx context.Context, run skillforge.ForgeRun) (skillforge.ForgeRun, error) {
	if run.OrganizationID == "" {
		run.OrganizationID = s.organizationID
	}
	if run.Revision <= 0 {
		run.Revision = 1
	}
	if run.StartedAt.IsZero() {
		run.StartedAt = time.Now().UTC()
	}

	searchJSON, _ := marshalNullable(run.Search)
	authorJSON, _ := marshalNullable(run.Author)
	pubJSON, _ := marshalNullable(run.PublishedSource)
	matJSON, _ := marshalNullable(run.MaterializedSource)
	valJSON, _ := marshalNullable(run.Validation)
	evalJSON, _ := marshalNullable(run.Evaluation)

	var skillVerID *string
	if strings.TrimSpace(run.SkillVersionID) != "" {
		v := strings.TrimSpace(run.SkillVersionID)
		skillVerID = &v
	}

	_, err := s.pool.Exec(ctx, `
		INSERT INTO skillforge_runs (
			id, organization_id, need_id, revision, status, current_step,
			input_digest, skill_version_id, search_record, author_record,
			published_source, materialized_source, validation_result,
			evaluation_result, started_at, finished_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16
		)
	`, run.ID, run.OrganizationID, run.NeedID, run.Revision, string(run.Status), string(run.CurrentStep),
		run.InputDigest, skillVerID, searchJSON, authorJSON, pubJSON, matJSON, valJSON, evalJSON,
		run.StartedAt, run.FinishedAt)

	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return skillforge.ForgeRun{}, skillforge.ErrRunConflict
		}
		return skillforge.ForgeRun{}, fmt.Errorf("insert skillforge run: %w", err)
	}

	return run, nil
}

func (s *RunStore) GetRun(ctx context.Context, organizationID, runID string) (skillforge.ForgeRun, error) {
	if organizationID == "" {
		organizationID = s.organizationID
	}
	row := s.pool.QueryRow(ctx, `
		SELECT
			id, organization_id, need_id, revision, status, current_step,
			input_digest, skill_version_id, search_record, author_record,
			published_source, materialized_source, validation_result,
			evaluation_result, started_at, finished_at
		FROM skillforge_runs
		WHERE organization_id = $1 AND id = $2
		ORDER BY revision DESC
		LIMIT 1
	`, organizationID, runID)

	return scanRun(row)
}

func (s *RunStore) GetLatestRunByNeed(ctx context.Context, organizationID, needID string) (skillforge.ForgeRun, error) {
	if organizationID == "" {
		organizationID = s.organizationID
	}
	row := s.pool.QueryRow(ctx, `
		SELECT
			id, organization_id, need_id, revision, status, current_step,
			input_digest, skill_version_id, search_record, author_record,
			published_source, materialized_source, validation_result,
			evaluation_result, started_at, finished_at
		FROM skillforge_runs
		WHERE organization_id = $1 AND need_id = $2
		ORDER BY started_at DESC, revision DESC
		LIMIT 1
	`, organizationID, needID)

	return scanRun(row)
}

func (s *RunStore) SaveRun(ctx context.Context, run skillforge.ForgeRun) (skillforge.ForgeRun, error) {
	if run.OrganizationID == "" {
		run.OrganizationID = s.organizationID
	}
	searchJSON, _ := marshalNullable(run.Search)
	authorJSON, _ := marshalNullable(run.Author)
	pubJSON, _ := marshalNullable(run.PublishedSource)
	matJSON, _ := marshalNullable(run.MaterializedSource)
	valJSON, _ := marshalNullable(run.Validation)
	evalJSON, _ := marshalNullable(run.Evaluation)

	var skillVerID *string
	if strings.TrimSpace(run.SkillVersionID) != "" {
		v := strings.TrimSpace(run.SkillVersionID)
		skillVerID = &v
	}

	cmd, err := s.pool.Exec(ctx, `
		UPDATE skillforge_runs SET
			status = $3,
			current_step = $4,
			skill_version_id = $5,
			search_record = $6,
			author_record = $7,
			published_source = $8,
			materialized_source = $9,
			validation_result = $10,
			evaluation_result = $11,
			finished_at = $12
		WHERE organization_id = $1 AND id = $2 AND revision = $13
	`, run.OrganizationID, run.ID, string(run.Status), string(run.CurrentStep),
		skillVerID, searchJSON, authorJSON, pubJSON, matJSON, valJSON, evalJSON,
		run.FinishedAt, run.Revision)

	if err != nil {
		return skillforge.ForgeRun{}, fmt.Errorf("update skillforge run: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		return skillforge.ForgeRun{}, skillforge.ErrRunNotFound
	}

	return run, nil
}

func (s *RunStore) RecordEvent(ctx context.Context, ev skillforge.Event) error {
	if ev.OrganizationID == "" {
		ev.OrganizationID = s.organizationID
	}
	if ev.RecordedAt.IsZero() {
		ev.RecordedAt = time.Now().UTC()
	}
	refsJSON, err := json.Marshal(ev.Refs)
	if err != nil || len(refsJSON) == 0 || string(refsJSON) == "null" {
		refsJSON = []byte("{}")
	}

	var digest *string
	if strings.TrimSpace(ev.Digest) != "" {
		d := strings.TrimSpace(ev.Digest)
		digest = &d
	}

	_, err = s.pool.Exec(ctx, `
		INSERT INTO skillforge_events (
			run_id, sequence, organization_id, event_type, refs, digest, recorded_at
		) VALUES (
			$1,
			COALESCE((SELECT MAX(sequence) + 1 FROM skillforge_events WHERE run_id = $1), 1),
			$2, $3, $4, $5, $6
		)
	`, ev.RunID, ev.OrganizationID, ev.EventType, refsJSON, digest, ev.RecordedAt)

	if err != nil {
		return fmt.Errorf("insert skillforge event: %w", err)
	}
	return nil
}

func (s *RunStore) ListEvents(ctx context.Context, organizationID, runID string) ([]skillforge.Event, error) {
	if organizationID == "" {
		organizationID = s.organizationID
	}
	rows, err := s.pool.Query(ctx, `
		SELECT run_id, sequence, organization_id, event_type, refs, digest, recorded_at
		FROM skillforge_events
		WHERE organization_id = $1 AND run_id = $2
		ORDER BY sequence ASC
	`, organizationID, runID)
	if err != nil {
		return nil, fmt.Errorf("query skillforge events: %w", err)
	}
	defer rows.Close()

	var events []skillforge.Event
	for rows.Next() {
		var ev skillforge.Event
		var refsJSON []byte
		var digest *string
		if err := rows.Scan(&ev.RunID, &ev.Sequence, &ev.OrganizationID, &ev.EventType, &refsJSON, &digest, &ev.RecordedAt); err != nil {
			return nil, fmt.Errorf("scan skillforge event: %w", err)
		}
		if digest != nil {
			ev.Digest = *digest
		}
		if len(refsJSON) > 0 {
			_ = json.Unmarshal(refsJSON, &ev.Refs)
		}
		events = append(events, ev)
	}
	return events, nil
}

func scanRun(row pgx.Row) (skillforge.ForgeRun, error) {
	var run skillforge.ForgeRun
	var status, step string
	var skillVerID *string
	var searchJSON, authorJSON, pubJSON, matJSON, valJSON, evalJSON []byte

	err := row.Scan(
		&run.ID, &run.OrganizationID, &run.NeedID, &run.Revision, &status, &step,
		&run.InputDigest, &skillVerID, &searchJSON, &authorJSON,
		&pubJSON, &matJSON, &valJSON, &evalJSON,
		&run.StartedAt, &run.FinishedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return skillforge.ForgeRun{}, skillforge.ErrRunNotFound
		}
		return skillforge.ForgeRun{}, fmt.Errorf("scan skillforge run: %w", err)
	}

	run.Status = skillforge.RunStatus(status)
	run.CurrentStep = skillforge.Step(step)
	if skillVerID != nil {
		run.SkillVersionID = *skillVerID
	}
	if len(searchJSON) > 0 && string(searchJSON) != "null" {
		var s skillforge.SearchRecord
		if err := json.Unmarshal(searchJSON, &s); err == nil {
			run.Search = &s
		}
	}
	if len(authorJSON) > 0 && string(authorJSON) != "null" {
		var a skillforge.AuthorRecord
		if err := json.Unmarshal(authorJSON, &a); err == nil {
			run.Author = &a
		}
	}
	if len(pubJSON) > 0 && string(pubJSON) != "null" {
		var p source.PublishedSource
		if err := json.Unmarshal(pubJSON, &p); err == nil {
			run.PublishedSource = &p
		}
	}
	if len(matJSON) > 0 && string(matJSON) != "null" {
		var m skillregistry.SourceRecord
		if err := json.Unmarshal(matJSON, &m); err == nil {
			run.MaterializedSource = &m
		}
	}
	if len(valJSON) > 0 && string(valJSON) != "null" {
		var v skillforge.ValidationResult
		if err := json.Unmarshal(valJSON, &v); err == nil {
			run.Validation = &v
		}
	}
	if len(evalJSON) > 0 && string(evalJSON) != "null" {
		var e skillforge.EvaluationResult
		if err := json.Unmarshal(evalJSON, &e); err == nil {
			run.Evaluation = &e
		}
	}

	return run, nil
}

func marshalNullable(v any) ([]byte, error) {
	if v == nil {
		return nil, nil
	}
	return json.Marshal(v)
}
