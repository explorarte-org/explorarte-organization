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
		values := map[string]string{"ORG_ENVIRONMENT": "test", "ORG_DATABASE_URL": databaseURL, "ORG_DATABASE_MAX_CONNS": "4", "ORG_DATABASE_MIN_CONNS": "0"}
		v, ok := values[key]
		return v, ok
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store, err := postgres.Open(ctx, cfg.Database, "explorarte-integration-test")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`DROP TRIGGER IF EXISTS organizational_memory_entries_no_delete ON organizational_memory_entries`, `DROP TRIGGER IF EXISTS organizational_memory_entry_update_guard ON organizational_memory_entries`, `DROP TRIGGER IF EXISTS organizational_memory_entry_insert_guard ON organizational_memory_entries`, `DROP TRIGGER IF EXISTS organizational_memory_version_insert_guard ON organizational_memory_versions`, `DROP TRIGGER IF EXISTS organizational_memory_idempotency_immutable ON organizational_memory_idempotency`, `DROP TRIGGER IF EXISTS organizational_memory_events_immutable ON organizational_memory_state_events`, `DROP TRIGGER IF EXISTS organizational_memory_evidence_immutable ON organizational_memory_evidence_refs`, `DROP TRIGGER IF EXISTS organizational_memory_versions_immutable ON organizational_memory_versions`, `DROP TABLE IF EXISTS organizational_memory_idempotency`, `DROP TABLE IF EXISTS organizational_memory_state_events`, `DROP TABLE IF EXISTS organizational_memory_evidence_refs`, `DROP TABLE IF EXISTS organizational_memory_entries`, `DROP TABLE IF EXISTS organizational_memory_versions`, `DROP FUNCTION IF EXISTS organizational_memory_reject_entry_delete()`, `DROP FUNCTION IF EXISTS organizational_memory_guard_entry_update()`, `DROP FUNCTION IF EXISTS organizational_memory_guard_entry_insert()`, `DROP FUNCTION IF EXISTS organizational_memory_guard_version_insert()`, `DROP FUNCTION IF EXISTS organizational_memory_reject_mutation()`,
		`DROP TABLE IF EXISTS shadow_verifier_divergences`, `DROP TABLE IF EXISTS shadow_verifier_runs`,
		`DROP TRIGGER IF EXISTS decision_budget_events_immutable ON decision_budget_events`, `DROP TRIGGER IF EXISTS decision_records_immutable ON decision_records`, `DROP TRIGGER IF EXISTS decision_verifications_immutable ON decision_verifications`, `DROP TRIGGER IF EXISTS decision_observations_immutable ON decision_observations`, `DROP TRIGGER IF EXISTS decision_graph_edges_immutable ON decision_graph_edges`, `DROP TRIGGER IF EXISTS decision_branch_events_immutable ON decision_branch_events`, `DROP TRIGGER IF EXISTS decision_graph_versions_immutable ON decision_graph_versions`, `DROP TRIGGER IF EXISTS decision_graph_runs_update_guard ON decision_graph_runs`, `DROP TRIGGER IF EXISTS decision_graph_nodes_update_guard ON decision_graph_nodes`, `DROP TRIGGER IF EXISTS decision_graph_edges_cycle_guard ON decision_graph_edges`, `DROP TABLE IF EXISTS decision_budget_events`, `DROP TABLE IF EXISTS decision_records`, `DROP TABLE IF EXISTS decision_verifications`, `DROP TABLE IF EXISTS decision_observations`, `DROP TABLE IF EXISTS decision_node_executions`, `DROP TABLE IF EXISTS decision_graph_budgets`, `DROP TABLE IF EXISTS decision_branch_events`, `DROP TABLE IF EXISTS decision_graph_edges`, `DROP TABLE IF EXISTS decision_graph_nodes`, `DROP TABLE IF EXISTS decision_graph_versions`, `DROP TABLE IF EXISTS decision_graph_runs`, `DROP FUNCTION IF EXISTS decision_graph_immutable_row()`, `DROP FUNCTION IF EXISTS decision_graph_guard_run_update()`, `DROP FUNCTION IF EXISTS decision_graph_guard_node_update()`, `DROP FUNCTION IF EXISTS decision_graph_reject_edge_cycle()`,
		`DROP TABLE IF EXISTS improvement_promotion_decisions`, `DROP TABLE IF EXISTS improvement_candidates`,
		`DROP TABLE IF EXISTS model_provider_outcomes`, `DROP TABLE IF EXISTS model_provider_requests`, `DROP FUNCTION IF EXISTS reject_model_provider_outcome_mutation()`, `DROP FUNCTION IF EXISTS reject_model_provider_request_mutation()`, `ALTER TABLE IF EXISTS model_dispatch_attempts DROP CONSTRAINT IF EXISTS model_dispatch_attempts_identity_assertion_fk`, `ALTER TABLE IF EXISTS model_dispatch_attempts DROP CONSTRAINT IF EXISTS model_dispatch_attempts_identity_key_fk`, `DROP TABLE IF EXISTS model_execution_identity_assertions`, `DROP TABLE IF EXISTS model_execution_identity_challenges`, `DROP TABLE IF EXISTS model_execution_identity_keys`, `DROP TABLE IF EXISTS model_execution_identity_policy_versions`, `DROP TABLE IF EXISTS model_egress_evaluations`, `DROP TABLE IF EXISTS model_invocation_usage`, `DROP TABLE IF EXISTS model_invocation_results`, `DROP TABLE IF EXISTS model_dispatch_attempts`, `DROP TABLE IF EXISTS model_invocations`, `DROP TABLE IF EXISTS role_model_bindings`, `DROP TABLE IF EXISTS model_capability_snapshots`, `DROP TABLE IF EXISTS model_profile_versions`, `DROP TABLE IF EXISTS model_profiles`, `DROP TABLE IF EXISTS model_providers`, `DROP TABLE IF EXISTS model_egress_revision_bindings`, `DROP TABLE IF EXISTS model_egress_rules`, `DROP TABLE IF EXISTS model_egress_policy_versions`, `DROP FUNCTION IF EXISTS model_egress_revision_belongs_to_organization(TEXT,BIGINT)`, `DROP FUNCTION IF EXISTS model_egress_normalized_reason_codes(JSONB)`, `DROP FUNCTION IF EXISTS model_egress_normalized_classifications(JSONB)`, `DROP FUNCTION IF EXISTS reject_model_egress_immutable_mutation()`, `DROP FUNCTION IF EXISTS enforce_model_egress_rule_insert_window()`, `DROP FUNCTION IF EXISTS enforce_model_egress_policy_version_immutability()`, `DROP TABLE IF EXISTS context_segments`, `DROP TABLE IF EXISTS context_snapshots`, `DROP FUNCTION IF EXISTS reject_context_segment_mutation()`, `DROP TABLE IF EXISTS authorization_uses`, `DROP TABLE IF EXISTS authorization_decisions`, `DROP TABLE IF EXISTS authorization_requests`, `DROP TABLE IF EXISTS staging_events`, `DROP TABLE IF EXISTS staging_reviews`, `DROP TABLE IF EXISTS staging_promotions`, `DROP TABLE IF EXISTS staging_checks`, `DROP TABLE IF EXISTS staging_workspace_artifacts`, `DROP TABLE IF EXISTS staging_artifacts`, `DROP TABLE IF EXISTS staging_workspaces`, `DROP TABLE IF EXISTS outbox_events`, `DROP TABLE IF EXISTS task_dead_letters`, `DROP TABLE IF EXISTS task_events`, `DROP TABLE IF EXISTS task_leases`, `DROP TABLE IF EXISTS task_attempts`, `DROP TABLE IF EXISTS task_evidence`, `DROP TABLE IF EXISTS task_requirements`, `DROP TABLE IF EXISTS task_dependencies`, `DROP TABLE IF EXISTS tasks`, `DROP TABLE IF EXISTS organization_reporting_lines`, `ALTER TABLE IF EXISTS organizational_units DROP CONSTRAINT IF EXISTS organizational_units_leader_role_fk`, `ALTER TABLE IF EXISTS organizations DROP CONSTRAINT IF EXISTS organizations_ceo_role_fk`, `ALTER TABLE IF EXISTS organizations DROP CONSTRAINT IF EXISTS organizations_owner_role_fk`, `DROP TABLE IF EXISTS organization_roles`, `DROP TABLE IF EXISTS organizational_units`, `DROP TABLE IF EXISTS organizations`, `DROP TABLE IF EXISTS organization_registry_revision_documents`, `DROP TABLE IF EXISTS organization_registry_revisions`, `DROP TABLE IF EXISTS audit_events`, `DROP TABLE IF EXISTS schema_migrations`,
	} {
		if _, err := store.Pool().Exec(ctx, statement); err != nil {
			t.Fatalf("reset integration schema: %v", err)
		}
	}
	runner, err := platformmigrations.New(store.Pool(), rootmigrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	result, err := runner.Up(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Applied) != 16 || result.Current != 16 {
		t.Fatalf("unexpected migration result: %+v", result)
	}
	status, err := runner.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Ready || status.Pending != 0 || status.Applied != 16 {
		t.Fatalf("status=%+v", status)
	}
	if err := store.UnitOfWork().WithinTransaction(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO audit_events (event_type,actor_type,actor_id,payload) VALUES ($1,$2,$3,$4::jsonb)`, "integration.commit", "test", "runner", `{"ok":true}`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	marker := errors.New("force rollback")
	err = store.UnitOfWork().WithinTransaction(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO audit_events (event_type) VALUES ($1)`, "integration.rollback"); err != nil {
			return err
		}
		return marker
	})
	if !errors.Is(err, marker) {
		t.Fatalf("rollback=%v", err)
	}
	var committed, rolled int
	if err := store.Pool().QueryRow(ctx, `SELECT count(*) FILTER (WHERE event_type='integration.commit'),count(*) FILTER (WHERE event_type='integration.rollback') FROM audit_events`).Scan(&committed, &rolled); err != nil {
		t.Fatal(err)
	}
	if committed != 1 || rolled != 0 {
		t.Fatalf("counts=%d/%d", committed, rolled)
	}
	second, err := runner.Up(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Applied) != 0 || second.Current != 16 {
		t.Fatalf("second=%+v", second)
	}
	loaded, err := platformmigrations.Load(rootmigrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 16 {
		t.Fatalf("loaded=%d want16", len(loaded))
	}
	if err := store.UnitOfWork().WithinTransaction(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, loaded[15].DownSQL); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM schema_migrations WHERE version=16`)
		return err
	}); err != nil {
		t.Fatalf("down migration 000016: %v", err)
	}
	for _, table := range []string{"skill_registry_versions", "skill_registry_skills", "skill_registry_lifecycle_events"} {
		var relation *string
		if err := store.Pool().QueryRow(ctx, `SELECT to_regclass('public.' || $1)::text`, table).Scan(&relation); err != nil {
			t.Fatal(err)
		}
		if relation != nil {
			t.Fatalf("%s still exists", table)
		}
	}
	restored, err := runner.Up(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.Applied) != 1 || restored.Current != 16 {
		t.Fatalf("restore=%+v want 000016", restored)
	}
}
