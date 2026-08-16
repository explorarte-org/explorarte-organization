// Package postgres is the durable ExecutionHistoryStore. It exists so a
// Harness run survives the process that started it: the in-memory store is
// deterministic and fine for validation, but a restart loses the trajectory,
// and a resumed run that cannot see its own history repeats model turns and
// tool side effects.
//
// The boundary is deliberate. This package depends on the Harness domain, and
// the Harness never depends on pgx: the port stays a two-method contract and
// this implementation is one of its adapters, alongside the memory store.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/executionharness"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store persists one organization's Harness histories. Scoping the store
// rather than the port is what makes cross-organization collision impossible:
// two organizations may use the same run identifier and neither can read,
// extend, or interleave the other's trajectory.
type Store struct {
	pool           *pgxpool.Pool
	organizationID string
}

func New(store *platformpostgres.Store, organizationID string) (*Store, error) {
	if store == nil || store.Pool() == nil {
		return nil, errors.New("durable execution history requires an initialized PostgreSQL store")
	}
	if strings.TrimSpace(organizationID) == "" {
		return nil, errors.New("durable execution history requires an organization scope")
	}
	return &Store{pool: store.Pool(), organizationID: organizationID}, nil
}

const selectRunEvents = `
	SELECT sequence, payload
	FROM execution_run_events
	WHERE organization_id=$1 AND run_id=$2
	ORDER BY sequence ASC`

// Append writes the next event of a run under optimistic sequence control,
// exactly as the in-memory store does: the caller states the sequence it
// believes is current, and a mismatch is a conflict rather than a silent
// overwrite. A retry of an already-confirmed append therefore conflicts
// instead of producing an ambiguous second copy.
func (s *Store) Append(ctx context.Context, runID string, expectedSequence uint64, event executionharness.Event) (executionharness.Event, error) {
	if err := ctx.Err(); err != nil {
		return executionharness.Event{}, err
	}
	if err := s.validate(runID, event); err != nil {
		return executionharness.Event{}, err
	}
	event.Sequence = expectedSequence + 1
	payload, err := json.Marshal(event)
	if err != nil {
		return executionharness.Event{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return executionharness.Event{}, mapError(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var current int64
	if err = tx.QueryRow(ctx, `SELECT COALESCE(MAX(sequence),0) FROM execution_run_events WHERE organization_id=$1 AND run_id=$2`, s.organizationID, runID).Scan(&current); err != nil {
		return executionharness.Event{}, mapError(err)
	}
	if uint64(current) != expectedSequence {
		return executionharness.Event{}, fmt.Errorf("%w: expected %d, actual %d", executionharness.ErrHistoryConflict, expectedSequence, current)
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO execution_run_events(organization_id,run_id,sequence,task_id,attempt_id,event_type,correlation_id,causation_id,terminal_status,payload)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		s.organizationID, runID, int64(event.Sequence), event.TaskID, event.AttemptID,
		string(event.Type), event.CorrelationID, event.CausationID, string(event.TerminalStatus), payload); err != nil {
		return executionharness.Event{}, mapError(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return executionharness.Event{}, mapError(err)
	}
	return event, nil
}

// Read returns the run's events in durable ordinal order. The sequence column
// is authoritative: the payload is decoded first and its sequence overwritten,
// so a payload that somehow disagreed with the ledger cannot reorder history.
func (s *Store) Read(ctx context.Context, runID string) ([]executionharness.Event, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, selectRunEvents, s.organizationID, runID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	result := []executionharness.Event{}
	for rows.Next() {
		var sequence int64
		var payload []byte
		if err = rows.Scan(&sequence, &payload); err != nil {
			return nil, mapError(err)
		}
		var event executionharness.Event
		if err = json.Unmarshal(payload, &event); err != nil {
			return nil, fmt.Errorf("%w: undecodable event payload at sequence %d", executionharness.ErrHistoryCorrupt, sequence)
		}
		event.Sequence = uint64(sequence)
		result = append(result, event)
	}
	if err = rows.Err(); err != nil {
		return nil, mapError(err)
	}
	return result, nil
}

func (s *Store) validate(runID string, event executionharness.Event) error {
	switch {
	case strings.TrimSpace(runID) == "":
		return fmt.Errorf("%w: run identifier is required", executionharness.ErrHistoryCorrupt)
	case event.RunID != runID:
		return fmt.Errorf("%w: invalid append identity", executionharness.ErrHistoryCorrupt)
	case event.Sequence != 0:
		return fmt.Errorf("%w: caller-assigned sequence", executionharness.ErrHistoryCorrupt)
	case event.OrganizationID != s.organizationID:
		return fmt.Errorf("%w: event organization %q does not belong to this history store", executionharness.ErrHistoryCorrupt, event.OrganizationID)
	case event.TaskID <= 0 || event.AttemptID <= 0:
		return fmt.Errorf("%w: event is not bound to a task attempt", executionharness.ErrHistoryCorrupt)
	case event.Type == "":
		return fmt.Errorf("%w: event type is required", executionharness.ErrHistoryCorrupt)
	}
	return nil
}

// mapError keeps a unique-violation on the run ordinal as a sequence conflict:
// it is the concurrent-writer case, not corruption.
func mapError(err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && strings.Contains(pgErr.ConstraintName, "execution_run_events_run_sequence_unique") {
		return fmt.Errorf("%w: concurrent append for the same run ordinal", executionharness.ErrHistoryConflict)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	return err
}

var _ executionharness.ExecutionHistoryStore = (*Store)(nil)
