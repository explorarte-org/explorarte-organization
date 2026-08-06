//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/config"
	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	"github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
	"github.com/jackc/pgx/v5"
)

func TestPostgresMigrationsAndUnitOfWork(t *testing.T) {
	databaseURL := os.Getenv("ORG_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("ORG_TEST_DATABASE_URL is required for integration tests")
	}
	cfg, err := config.LoadFrom(func(key string) (string, bool) {
		values := map[string]string{
			"ORG_ENVIRONMENT":        "test",
			"ORG_DATABASE_URL":       databaseURL,
			"ORG_DATABASE_MAX_CONNS": "4",
			"ORG_DATABASE_MIN_CONNS": "0",
		}
		value, ok := values[key]
		return value, ok
	})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store, err := postgres.Open(ctx, cfg.Database, "explorarte-integration-test")
	if err != nil {
		t.Fatalf("open PostgreSQL: %v", err)
	}
	defer store.Close()
	if err := store.Ping(ctx); err != nil {
		t.Fatalf("ping PostgreSQL: %v", err)
	}
	for _, statement := range []string{
		`DROP TABLE IF EXISTS model_egress_evaluations`,
		`DROP TABLE IF EXISTS model_invocation_usage`,
		`DROP TABLE IF EXISTS model_invocation_results`,
		`DROP TABLE IF EXISTS model_dispatch_attempts`,
		`DROP TABLE IF EXISTS model_invocations`,
		`DROP TABLE IF EXISTS role_model_bindings`,
		`DROP TABLE IF EXISTS model_capability_snapshots`,
		`DROP TABLE IF EXISTS model_profile_versions`,
		`DROP TABLE IF EXISTS model_profiles`,
		`DROP TABLE IF EXISTS model_providers`,
		`DROP TABLE IF EXISTS model_egress_revision_bindings`,
		`DROP TABLE IF EXISTS model_egress_rules`,
		`DROP TABLE IF EXISTS model_egress_policy_versions`,
		`DROP FUNCTION IF EXISTS model_egress_revision_belongs_to_organization(TEXT,BIGINT)`,
		`DROP FUNCTION IF EXISTS model_egress_normalized_reason_codes(JSONB)`,
		`DROP FUNCTION IF EXISTS model_egress_normalized_classifications(JSONB)`,
		`DROP FUNCTION IF EXISTS reject_model_egress_immutable_mutation()`,
		`DROP FUNCTION IF EXISTS enforce_model_egress_rule_insert_window()`,
		`DROP FUNCTION IF EXISTS enforce_model_egress_policy_version_immutability()`,
		`DROP TABLE IF EXISTS context_segments`,
		`DROP TABLE IF EXISTS context_snapshots`,
		`DROP FUNCTION IF EXISTS reject_context_segment_mutation()`,
		`DROP TABLE IF EXISTS authorization_uses`,
		`DROP TABLE IF EXISTS authorization_decisions`,
		`DROP TABLE IF EXISTS authorization_requests`,
		`DROP TABLE IF EXISTS staging_events`,
		`DROP TABLE IF EXISTS staging_reviews`,
		`DROP TABLE IF EXISTS staging_promotions`,
		`DROP TABLE IF EXISTS staging_checks`,
		`DROP TABLE IF EXISTS staging_workspace_artifacts`,
		`DROP TABLE IF EXISTS staging_artifacts`,
		`DROP TABLE IF EXISTS staging_workspaces`,
		`DROP TABLE IF EXISTS outbox_events`,
		`DROP TABLE IF EXISTS task_dead_letters`,
		`DROP TABLE IF EXISTS task_events`,
		`DROP TABLE IF EXISTS task_leases`,
		`DROP TABLE IF EXISTS task_attempts`,
		`DROP TABLE IF EXISTS task_evidence`,
		`DROP TABLE IF EXISTS task_requirements`,
		`DROP TABLE IF EXISTS task_dependencies`,
		`DROP TABLE IF EXISTS tasks`,
		`DROP TABLE IF EXISTS organization_reporting_lines`,
		`ALTER TABLE IF EXISTS organizational_units DROP CONSTRAINT IF EXISTS organizational_units_leader_role_fk`,
		`ALTER TABLE IF EXISTS organizations DROP CONSTRAINT IF EXISTS organizations_ceo_role_fk`,
		`ALTER TABLE IF EXISTS organizations DROP CONSTRAINT IF EXISTS organizations_owner_role_fk`,
		`DROP TABLE IF EXISTS organization_roles`,
		`DROP TABLE IF EXISTS organizational_units`,
		`DROP TABLE IF EXISTS organizations`,
		`DROP TABLE IF EXISTS organization_registry_revision_documents`,
		`DROP TABLE IF EXISTS organization_registry_revisions`,
		`DROP TABLE IF EXISTS audit_events`,
		`DROP TABLE IF EXISTS schema_migrations`,
	} {
		if _, err := store.Pool().Exec(ctx, statement); err != nil {
			t.Fatalf("reset integration schema: %v", err)
		}
	}
	runner, err := platformmigrations.New(store.Pool(), rootmigrations.Files)
	if err != nil {
		t.Fatalf("create migration runner: %v", err)
	}
	result, err := runner.Up(ctx)
	if err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	if len(result.Applied) != 9 || result.Current != 9 {
		t.Fatalf("unexpected migration result: %+v", result)
	}
	status, err := runner.Status(ctx)
	if err != nil {
		t.Fatalf("migration status: %v", err)
	}
	if !status.Ready || status.Pending != 0 || status.Applied != 9 {
		t.Fatalf("unexpected migration status: %+v", status)
	}
	if err := store.UnitOfWork().WithinTransaction(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO audit_events (event_type, actor_type, actor_id, payload) VALUES ($1, $2, $3, $4::jsonb)`, "integration.commit", "test", "runner", `{"ok":true}`)
		return err
	}); err != nil {
		t.Fatalf("committed transaction: %v", err)
	}
	rollbackMarker := errors.New("force rollback")
	err = store.UnitOfWork().WithinTransaction(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO audit_events (event_type) VALUES ($1)`, "integration.rollback"); err != nil {
			return err
		}
		return rollbackMarker
	})
	if !errors.Is(err, rollbackMarker) {
		t.Fatalf("rollback transaction error = %v, want marker", err)
	}
	var committed, rolledBack int
	if err := store.Pool().QueryRow(ctx, `SELECT count(*) FILTER (WHERE event_type = 'integration.commit'), count(*) FILTER (WHERE event_type = 'integration.rollback') FROM audit_events`).Scan(&committed, &rolledBack); err != nil {
		t.Fatalf("query audit events: %v", err)
	}
	if committed != 1 || rolledBack != 0 {
		t.Fatalf("transaction counts = committed:%d rolled_back:%d, want 1/0", committed, rolledBack)
	}
	second, err := runner.Up(ctx)
	if err != nil {
		t.Fatalf("idempotent migration run: %v", err)
	}
	if len(second.Applied) != 0 || second.Current != 9 {
		t.Fatalf("second migration result = %+v, want no changes", second)
	}

	loaded, err := platformmigrations.Load(rootmigrations.Files)
	if err != nil {
		t.Fatalf("load migrations for down test: %v", err)
	}
	if len(loaded) != 9 {
		t.Fatalf("loaded migrations = %d, want 9", len(loaded))
	}
	if err := store.UnitOfWork().WithinTransaction(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, loaded[8].DownSQL); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM schema_migrations WHERE version = 9`)
		return err
	}); err != nil {
		t.Fatalf("down migration 000009: %v", err)
	}
	var assignmentTable *string
	if err := store.Pool().QueryRow(ctx, `SELECT to_regclass('public.model_dispatcher_assignments')::text`).Scan(&assignmentTable); err != nil {
		t.Fatalf("check down migration: %v", err)
	}
	if assignmentTable != nil {
		t.Fatalf("model_dispatcher_assignments still exists after down: %v", *assignmentTable)
	}
	var dispatcherColumnCount int
	if err := store.Pool().QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema='public' AND table_name='model_invocations' AND column_name IN ('dispatcher_assignment_id','execution_principal_id')`).Scan(&dispatcherColumnCount); err != nil {
		t.Fatalf("check invocation dispatcher columns after down: %v", err)
	}
	if dispatcherColumnCount != 0 {
		t.Fatalf("invocation dispatcher columns remain after down: %d", dispatcherColumnCount)
	}
	restored, err := runner.Up(ctx)
	if err != nil {
		t.Fatalf("restore migration 000009: %v", err)
	}
	if len(restored.Applied) != 1 || restored.Current != 9 {
		t.Fatalf("restore migration result = %+v, want only 000009", restored)
	}
}
