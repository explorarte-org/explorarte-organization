//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/executionharness"
	harnesspostgres "github.com/Mireuz13/explorarte-organization/internal/executionharness/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/testdbguard"
	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	historyOrganization = "explorarte"
	historyRole         = "ingenieria_ia/qa"
)

type fixture struct {
	ctx       context.Context
	store     *platformpostgres.Store
	history   *harnesspostgres.Store
	taskID    int64
	attemptID int64
	cleanup   func()
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	databaseURL := os.Getenv("ORG_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("ORG_TEST_DATABASE_URL is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	cfg, err := config.LoadFrom(func(key string) (string, bool) {
		values := map[string]string{
			"ORG_ENVIRONMENT":        "test",
			"ORG_DATABASE_URL":       databaseURL,
			"ORG_DATABASE_MAX_CONNS": "16",
			"ORG_DATABASE_MIN_CONNS": "0",
		}
		value, ok := values[key]
		return value, ok
	})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	platformStore, err := platformpostgres.Open(ctx, cfg.Database, "execution-history-integration-test")
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	fail := func(format string, args ...any) {
		platformStore.Close()
		cancel()
		t.Fatalf(format, args...)
	}
	if err = testdbguard.RequireTestDatabase(ctx, databaseURL, platformStore.Pool()); err != nil {
		fail("refusing to run against unverified database: %v", err)
	}
	runner, err := platformmigrations.New(platformStore.Pool(), rootmigrations.Files)
	if err != nil {
		fail("migration runner: %v", err)
	}
	if _, err = runner.Up(ctx); err != nil {
		fail("migrate: %v", err)
	}
	if err = testdbguard.RequireDestructive(ctx, databaseURL, platformStore.Pool()); err != nil {
		fail("refusing destructive TRUNCATE: %v", err)
	}
	if _, err = platformStore.Pool().Exec(ctx, `
		TRUNCATE execution_run_events,outbox_events,task_dead_letters,task_events,task_leases,task_attempts,
		         task_evidence,task_requirements,task_dependencies,tasks,organization_reporting_lines,
		         organization_registry_revision_documents,organization_roles,organizational_units,
		         organizations,organization_registry_revisions,audit_events RESTART IDENTITY CASCADE
	`); err != nil {
		fail("reset schema: %v", err)
	}
	registryRepo, err := registry.NewPostgresRepository(platformStore)
	if err != nil {
		fail("registry repository: %v", err)
	}
	loader, err := registry.NewLoader(filepath.Join("..", "..", "..", "docs", "canonical"))
	if err != nil {
		fail("registry loader: %v", err)
	}
	registryService, err := registry.NewService(loader, registryRepo, historyOrganization, 30*time.Second)
	if err != nil {
		fail("registry service: %v", err)
	}
	if result, syncErr := registryService.SynchronizeCanonical(ctx, true); syncErr != nil || !result.Applied {
		fail("sync registry: result=%+v err=%v", result, syncErr)
	}
	var revisionID, taskID int64
	if err = platformStore.Pool().QueryRow(ctx, `SELECT id FROM organization_registry_revisions ORDER BY id DESC LIMIT 1`).Scan(&revisionID); err != nil {
		fail("read revision: %v", err)
	}
	if err = platformStore.Pool().QueryRow(ctx, `
		INSERT INTO tasks(organization_id,organization_revision_id,assigned_role_id,assigned_unit_id,idempotency_key,request_hash,
		                  title,instructions,acceptance_criteria,status,priority,available_at,max_attempts,attempt_count,version)
		VALUES($1,$2,$3,'ingenieria_ia','execution-history-fixture',repeat('a',64),'History fixture','Persist harness events.','[]','running',0,NOW(),5,1,1)
		RETURNING id`, historyOrganization, revisionID, historyRole).Scan(&taskID); err != nil {
		fail("insert task: %v", err)
	}
	// The ledger binds run history to a real attempt of a real task in this
	// organization, so the fixture has to create one rather than assert an id.
	var attemptID int64
	if err = platformStore.Pool().QueryRow(ctx, `
		INSERT INTO task_attempts(task_id,ordinal,state,worker_id,leased_at,started_at,created_at,updated_at)
		VALUES($1,1,'running','execution-history-fixture',NOW(),NOW(),NOW(),NOW())
		RETURNING id`, taskID).Scan(&attemptID); err != nil {
		fail("insert task attempt: %v", err)
	}
	history, err := harnesspostgres.New(platformStore, historyOrganization)
	if err != nil {
		fail("history store: %v", err)
	}
	return &fixture{ctx: ctx, store: platformStore, history: history, taskID: taskID, attemptID: attemptID,
		cleanup: func() { platformStore.Close(); cancel() }}
}

func (f *fixture) event(runID string, eventType executionharness.EventType) executionharness.Event {
	return executionharness.Event{
		RunID: runID, OrganizationID: historyOrganization, TaskID: f.taskID, AttemptID: f.attemptID,
		Type: eventType, CorrelationID: runID + ":corr", CausationID: runID + ":cause",
	}
}

func TestDurableExecutionHistoryPostgreSQL17(t *testing.T) {
	f := newFixture(t)
	defer f.cleanup()

	t.Run("append assigns the durable ordinal and read returns it", func(t *testing.T) {
		appended, err := f.history.Append(f.ctx, "run-append", 0, f.event("run-append", executionharness.EventRunStarted))
		if err != nil {
			t.Fatal(err)
		}
		if appended.Sequence != 1 {
			t.Fatalf("sequence=%d want 1", appended.Sequence)
		}
		events, err := f.history.Read(f.ctx, "run-append")
		if err != nil {
			t.Fatal(err)
		}
		if len(events) != 1 || events[0].Sequence != 1 || events[0].Type != executionharness.EventRunStarted {
			t.Fatalf("read=%+v", events)
		}
		if events[0].OrganizationID != historyOrganization || events[0].TaskID != f.taskID || events[0].AttemptID != f.attemptID {
			t.Fatalf("execution identity was not preserved: %+v", events[0])
		}
	})

	t.Run("order is the durable ordinal, not insertion timing", func(t *testing.T) {
		types := []executionharness.EventType{
			executionharness.EventRunStarted,
			executionharness.EventModelRequestPrepared,
			executionharness.EventModelResponseRecorded,
			executionharness.EventToolCallRequested,
			executionharness.EventToolResultRecorded,
		}
		for index, eventType := range types {
			event := f.event("run-order", eventType)
			if eventType == executionharness.EventModelResponseRecorded {
				event.ModelResult = &executionharness.ModelResult{FinishReason: executionharness.FinishTools, InvocationRef: "inv-1"}
			}
			if _, err := f.history.Append(f.ctx, "run-order", uint64(index), event); err != nil {
				t.Fatalf("append %d: %v", index, err)
			}
		}
		events, err := f.history.Read(f.ctx, "run-order")
		if err != nil {
			t.Fatal(err)
		}
		if len(events) != len(types) {
			t.Fatalf("read %d events want %d", len(events), len(types))
		}
		for index, event := range events {
			if event.Sequence != uint64(index+1) || event.Type != types[index] {
				t.Fatalf("event[%d]=%+v want sequence %d type %q", index, event, index+1, types[index])
			}
		}
		if events[2].ModelResult == nil || events[2].ModelResult.InvocationRef != "inv-1" {
			t.Fatalf("model result payload lost across the durable boundary: %+v", events[2])
		}
	})

	t.Run("a re-confirmed append conflicts instead of duplicating history", func(t *testing.T) {
		event := f.event("run-idem", executionharness.EventRunStarted)
		if _, err := f.history.Append(f.ctx, "run-idem", 0, event); err != nil {
			t.Fatal(err)
		}
		_, err := f.history.Append(f.ctx, "run-idem", 0, event)
		if !errors.Is(err, executionharness.ErrHistoryConflict) {
			t.Fatalf("replayed append error=%v want ErrHistoryConflict", err)
		}
		events, err := f.history.Read(f.ctx, "run-idem")
		if err != nil {
			t.Fatal(err)
		}
		if len(events) != 1 {
			t.Fatalf("history has %d events after a replayed append, want 1", len(events))
		}
	})

	t.Run("a gap in the ordinal is refused", func(t *testing.T) {
		if _, err := f.history.Append(f.ctx, "run-gap", 4, f.event("run-gap", executionharness.EventRunStarted)); !errors.Is(err, executionharness.ErrHistoryConflict) {
			t.Fatalf("ordinal gap accepted: %v", err)
		}
	})

	t.Run("runs are isolated from each other", func(t *testing.T) {
		if _, err := f.history.Append(f.ctx, "run-a", 0, f.event("run-a", executionharness.EventRunStarted)); err != nil {
			t.Fatal(err)
		}
		if _, err := f.history.Append(f.ctx, "run-b", 0, f.event("run-b", executionharness.EventRunStarted)); err != nil {
			t.Fatal(err)
		}
		a, err := f.history.Read(f.ctx, "run-a")
		if err != nil {
			t.Fatal(err)
		}
		if len(a) != 1 || a[0].RunID != "run-a" {
			t.Fatalf("run-a history leaked: %+v", a)
		}
	})

	t.Run("organizations cannot see or extend each other's history", func(t *testing.T) {
		if _, err := f.history.Append(f.ctx, "run-shared", 0, f.event("run-shared", executionharness.EventRunStarted)); err != nil {
			t.Fatal(err)
		}
		foreign, err := harnesspostgres.New(f.store, "otra-organizacion")
		if err != nil {
			t.Fatal(err)
		}
		events, err := foreign.Read(f.ctx, "run-shared")
		if err != nil {
			t.Fatal(err)
		}
		if len(events) != 0 {
			t.Fatalf("a foreign organization read %d events of run-shared", len(events))
		}
		// An event stamped with another organization must be refused outright,
		// not silently rewritten into this store's scope.
		crossed := f.event("run-shared", executionharness.EventModelRequestPrepared)
		crossed.OrganizationID = "otra-organizacion"
		if _, err = f.history.Append(f.ctx, "run-shared", 1, crossed); !errors.Is(err, executionharness.ErrHistoryCorrupt) {
			t.Fatalf("cross-organization append error=%v want ErrHistoryCorrupt", err)
		}
	})

	t.Run("the ledger cannot bind a run to a foreign task or a foreign attempt", func(t *testing.T) {
		// Identity is enforced as a whole. The two composite foreign keys are
		// asserted by name so it is unambiguous which one refused: a pair that
		// does not exist in tasks(id, organization_id) is exactly how a
		// cross-organization binding would present itself, and an attempt of a
		// different task is checked directly.
		requireConstraint := func(err error, want string) {
			t.Helper()
			if err == nil {
				t.Fatalf("the ledger accepted a row that %s should have refused", want)
			}
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) {
				t.Fatalf("expected a PostgreSQL constraint violation, got %v", err)
			}
			if pgErr.ConstraintName != want {
				t.Fatalf("refused by %q, expected %q", pgErr.ConstraintName, want)
			}
		}

		foreignTask := f.event("run-foreign-task", executionharness.EventRunStarted)
		foreignTask.TaskID = f.taskID + 100000
		_, err := f.history.Append(f.ctx, "run-foreign-task", 0, foreignTask)
		requireConstraint(err, "execution_run_events_task_organization_fk")

		var revisionID, otherTask, otherAttempt int64
		if err = f.store.Pool().QueryRow(f.ctx, `SELECT organization_revision_id FROM tasks WHERE id=$1`, f.taskID).Scan(&revisionID); err != nil {
			t.Fatal(err)
		}
		if err = f.store.Pool().QueryRow(f.ctx, `
			INSERT INTO tasks(organization_id,organization_revision_id,assigned_role_id,assigned_unit_id,idempotency_key,request_hash,
			                  title,instructions,acceptance_criteria,status,priority,available_at,max_attempts,attempt_count,version)
			VALUES($1,$2,$3,'ingenieria_ia','execution-history-fixture-2',repeat('b',64),'Second fixture','Bind check.','[]','running',0,NOW(),5,1,1)
			RETURNING id`, historyOrganization, revisionID, historyRole).Scan(&otherTask); err != nil {
			t.Fatal(err)
		}
		if err = f.store.Pool().QueryRow(f.ctx, `
			INSERT INTO task_attempts(task_id,ordinal,state,worker_id,leased_at,started_at,created_at,updated_at)
			VALUES($1,1,'running','execution-history-fixture-2',NOW(),NOW(),NOW(),NOW())
			RETURNING id`, otherTask).Scan(&otherAttempt); err != nil {
			t.Fatal(err)
		}
		mismatched := f.event("run-foreign-attempt", executionharness.EventRunStarted)
		mismatched.AttemptID = otherAttempt
		_, err = f.history.Append(f.ctx, "run-foreign-attempt", 0, mismatched)
		requireConstraint(err, "execution_run_events_task_attempt_fk")
	})

	t.Run("the schema refuses a terminal status the runtime can never produce", func(t *testing.T) {
		forged := f.event("run-impossible-terminal", executionharness.EventRunFailed)
		forged.TerminalStatus = executionharness.StatusAuthorityUnavailable
		forged.Reason = "forged"
		if _, err := f.history.Append(f.ctx, "run-impossible-terminal", 0, forged); err == nil {
			t.Fatal("the ledger accepted authority_unavailable as a terminal status")
		}
	})

	t.Run("a cancelled context writes nothing", func(t *testing.T) {
		cancelled, cancel := context.WithCancel(f.ctx)
		cancel()
		if _, err := f.history.Append(cancelled, "run-cancelled", 0, f.event("run-cancelled", executionharness.EventRunStarted)); err == nil {
			t.Fatal("append accepted a cancelled context")
		}
		if _, err := f.history.Read(cancelled, "run-cancelled"); err == nil {
			t.Fatal("read accepted a cancelled context")
		}
		events, err := f.history.Read(f.ctx, "run-cancelled")
		if err != nil {
			t.Fatal(err)
		}
		if len(events) != 0 {
			t.Fatalf("cancelled append persisted %d events", len(events))
		}
	})

	t.Run("history is append-only in the schema, not only in the code", func(t *testing.T) {
		if _, err := f.history.Append(f.ctx, "run-immutable", 0, f.event("run-immutable", executionharness.EventRunStarted)); err != nil {
			t.Fatal(err)
		}
		if _, err := f.store.Pool().Exec(f.ctx, `UPDATE execution_run_events SET event_type='tampered' WHERE run_id='run-immutable'`); err == nil {
			t.Fatal("an UPDATE rewrote durable run history")
		}
		if _, err := f.store.Pool().Exec(f.ctx, `DELETE FROM execution_run_events WHERE run_id='run-immutable'`); err == nil {
			t.Fatal("a DELETE removed durable run history")
		}
	})

	t.Run("a terminal history reloads with its terminal event intact", func(t *testing.T) {
		started := f.event("run-terminal", executionharness.EventRunStarted)
		if _, err := f.history.Append(f.ctx, "run-terminal", 0, started); err != nil {
			t.Fatal(err)
		}
		terminal := f.event("run-terminal", executionharness.EventRunCompleted)
		terminal.TerminalStatus = executionharness.StatusCompleted
		terminal.Reason = "final output"
		if _, err := f.history.Append(f.ctx, "run-terminal", 1, terminal); err != nil {
			t.Fatal(err)
		}
		events, err := f.history.Read(f.ctx, "run-terminal")
		if err != nil {
			t.Fatal(err)
		}
		last := events[len(events)-1]
		if last.Type != executionharness.EventRunCompleted || last.TerminalStatus != executionharness.StatusCompleted || last.Reason != "final output" {
			t.Fatalf("terminal event did not survive the durable boundary: %+v", last)
		}
	})

	t.Run("a closed pool reports failure instead of losing an event", func(t *testing.T) {
		closing := newFixture(t)
		closing.store.Close()
		if _, err := closing.history.Append(f.ctx, "run-down", 0, closing.event("run-down", executionharness.EventRunStarted)); err == nil {
			t.Fatal("append succeeded against a closed pool")
		}
		if _, err := closing.history.Read(f.ctx, "run-down"); err == nil {
			t.Fatal("read succeeded against a closed pool")
		}
	})
}
