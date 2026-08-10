package codeexecutionfixtures

import (
	"context"
	"fmt"
	"reflect"

	"github.com/Mireuz13/explorarte-organization/internal/evaluation/fixtures"
	"github.com/Mireuz13/explorarte-organization/internal/evaluationdb"
	"github.com/jackc/pgx/v5/pgxpool"
)

type preexistingRow struct {
	id   int64
	name string
}

var preexistingRows = []preexistingRow{{1, "alpha"}, {2, "beta"}, {3, "gamma"}}

func (r Runner) runPostgresMigration(ctx context.Context, f fixtures.Fixture, subjectID string) (fixtures.RunOutcome, error) {
	if err := evaluationdb.RequireDisposable(ctx, r.Store); err != nil {
		return fixtures.RunOutcome{}, err
	}
	record := newRecorder(f, subjectID)
	pool := r.Store.Pool()
	schema := "eval_pgmigration_" + stableSuffix(subjectID)
	table := schema + ".widgets"

	// Idempotent replay: always start from a clean, uniquely-named schema.
	// Dropped again on exit so a disposable integration database never
	// accumulates fixture schemas across runs.
	if _, err := pool.Exec(ctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE"); err != nil {
		return fixtures.RunOutcome{}, fmt.Errorf("reset fixture schema: %w", err)
	}
	defer func() { _, _ = pool.Exec(context.WithoutCancel(ctx), "DROP SCHEMA IF EXISTS "+schema+" CASCADE") }()
	if _, err := pool.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		return fixtures.RunOutcome{}, fmt.Errorf("create fixture schema: %w", err)
	}
	if _, err := pool.Exec(ctx, "CREATE TABLE "+table+" (id BIGINT PRIMARY KEY, name TEXT NOT NULL)"); err != nil {
		return fixtures.RunOutcome{}, fmt.Errorf("create fixture table: %w", err)
	}
	for _, row := range preexistingRows {
		if _, err := pool.Exec(ctx, "INSERT INTO "+table+" (id, name) VALUES ($1,$2)", row.id, row.name); err != nil {
			return fixtures.RunOutcome{}, fmt.Errorf("seed preexisting row: %w", err)
		}
	}

	// up.sql/down.sql equivalent, applied twice: a nullable column added
	// then dropped — the pattern internal/platform/migrations requires of
	// every real migration in this repo (see migrations/*.up.sql/.down.sql
	// and their down/reapply tests).
	up := "ALTER TABLE " + table + " ADD COLUMN priority INTEGER"
	down := "ALTER TABLE " + table + " DROP COLUMN priority"

	schemaAfterFirstCycle, err := applyMigrationCycle(ctx, pool, schema, table, up, down)
	if err != nil {
		return fixtures.RunOutcome{}, err
	}
	rowsAfterFirst, err := countRows(ctx, pool, table)
	if err != nil {
		return fixtures.RunOutcome{}, err
	}
	record.record("down_sql_never_deletes_preexisting_rows_first_cycle", rowsAfterFirst == len(preexistingRows))

	schemaAfterSecondCycle, err := applyMigrationCycle(ctx, pool, schema, table, up, down)
	if err != nil {
		return fixtures.RunOutcome{}, err
	}
	rowsAfterSecond, err := countRows(ctx, pool, table)
	if err != nil {
		return fixtures.RunOutcome{}, err
	}
	record.record("down_sql_never_deletes_preexisting_rows_second_cycle", rowsAfterSecond == len(preexistingRows))
	record.record("up_down_up_cycle_twice_produces_the_same_schema", reflect.DeepEqual(schemaAfterFirstCycle, schemaAfterSecondCycle))

	valuesIntact, err := valuesMatch(ctx, pool, table)
	if err != nil {
		return fixtures.RunOutcome{}, err
	}
	record.record("preexisting_row_values_are_never_altered", valuesIntact)

	record.outcome.Metrics["rows_after_first_cycle"] = float64(rowsAfterFirst)
	record.outcome.Metrics["rows_after_second_cycle"] = float64(rowsAfterSecond)
	record.outcome.Metrics["schema_column_count"] = float64(len(schemaAfterSecondCycle))
	record.outcome.EvidenceRefs = append(record.outcome.EvidenceRefs,
		"postgres-schema:"+schema, "up-sql:"+up, "down-sql:"+down,
	)
	return record.finish("dos ciclos up/down aplicados sobre un esquema sintético desechable; los datos preexistentes quedaron verificados intactos"), nil
}

// applyMigrationCycle runs up then down against table and returns the
// resulting column list (ordinal order) after down — the schema the table
// is left in once the cycle completes, so two cycles can be compared for
// exact equality.
func applyMigrationCycle(ctx context.Context, pool *pgxpool.Pool, schema, table, up, down string) ([]string, error) {
	if _, err := pool.Exec(ctx, up); err != nil {
		return nil, fmt.Errorf("apply up migration: %w", err)
	}
	if _, err := pool.Exec(ctx, down); err != nil {
		return nil, fmt.Errorf("apply down migration: %w", err)
	}
	rows, err := pool.Query(ctx, `SELECT column_name FROM information_schema.columns WHERE table_schema=$1 AND table_name='widgets' ORDER BY ordinal_position`, schema)
	if err != nil {
		return nil, fmt.Errorf("read resulting schema: %w", err)
	}
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var column string
		if err := rows.Scan(&column); err != nil {
			return nil, err
		}
		columns = append(columns, column)
	}
	return columns, rows.Err()
}

func countRows(ctx context.Context, pool *pgxpool.Pool, table string) (int, error) {
	var count int
	err := pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&count)
	return count, err
}

func valuesMatch(ctx context.Context, pool *pgxpool.Pool, table string) (bool, error) {
	for _, row := range preexistingRows {
		var name string
		if err := pool.QueryRow(ctx, "SELECT name FROM "+table+" WHERE id=$1", row.id).Scan(&name); err != nil {
			return false, err
		}
		if name != row.name {
			return false, nil
		}
	}
	return true, nil
}
