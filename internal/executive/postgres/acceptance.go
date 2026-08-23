// Package postgres persists the Executive's own durable records.
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Mireuz13/explorarte-organization/internal/executive"
)

type AcceptanceStore struct{ pool *pgxpool.Pool }

func NewAcceptanceStore(pool *pgxpool.Pool) (*AcceptanceStore, error) {
	if pool == nil {
		return nil, errors.New("executive acceptance store requires a PostgreSQL pool")
	}
	return &AcceptanceStore{pool: pool}, nil
}

// RecordAcceptance writes the owner's phase assignment once.
//
// ON CONFLICT DO NOTHING rather than an upsert: a resumed submit must find
// what the first one stored, not overwrite it. The owner's statement about
// when a criterion becomes checkable is made at submission and does not get
// revised by a retry that happens to carry different text.
func (s *AcceptanceStore) RecordAcceptance(ctx context.Context, rootTaskID int64, criteria []executive.AcceptanceCriterion) error {
	if len(criteria) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for ordinal, criterion := range criteria {
		batch.Queue(`
INSERT INTO executive_goal_acceptance (root_task_id, ordinal, phase, criterion)
VALUES ($1, $2, $3, $4)
ON CONFLICT (root_task_id, ordinal) DO NOTHING`,
			rootTaskID, ordinal, string(criterion.Phase), criterion.Text)
	}
	results := s.pool.SendBatch(ctx, batch)
	defer func() { _ = results.Close() }()
	for range criteria {
		if _, err := results.Exec(); err != nil {
			return fmt.Errorf("record acceptance for root %d: %w", rootTaskID, err)
		}
	}
	return nil
}

func (s *AcceptanceStore) Acceptance(ctx context.Context, rootTaskID int64) ([]executive.AcceptanceCriterion, error) {
	rows, err := s.pool.Query(ctx, `
SELECT phase, criterion
FROM executive_goal_acceptance
WHERE root_task_id = $1
ORDER BY ordinal`, rootTaskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []executive.AcceptanceCriterion
	for rows.Next() {
		var criterion executive.AcceptanceCriterion
		var phase string
		if err := rows.Scan(&phase, &criterion.Text); err != nil {
			return nil, err
		}
		criterion.Phase = executive.AcceptancePhase(phase)
		out = append(out, criterion)
	}
	return out, rows.Err()
}
