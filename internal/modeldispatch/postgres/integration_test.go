//go:build integration

package postgres_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/modeldispatch"
	dispatchpostgres "github.com/Mireuz13/explorarte-organization/internal/modeldispatch/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/testdbguard"
	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
)

const dispatchIntegrationOrganization = "explorarte"

func TestModelDispatcherAssignmentsPostgreSQL17(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	platform := openDispatchStore(t, ctx)
	defer platform.Close()
	runner, err := platformmigrations.New(platform.Pool(), rootmigrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = runner.Up(ctx); err != nil {
		t.Fatalf("migrations through 000011: %v", err)
	}
	resetDispatchSchema(t, ctx, platform)
	syncDispatchCanonical(t, ctx, platform)
	store, err := dispatchpostgres.New(platform)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := registry.NewPostgresRepository(platform)
	if err != nil {
		t.Fatal(err)
	}
	revision, err := repo.GetCurrentRevision(ctx, dispatchIntegrationOrganization)
	if err != nil || revision == nil {
		t.Fatalf("revision=%+v err=%v", revision, err)
	}

	t.Run("register is idempotent and rejects hash conflicts", func(t *testing.T) {
		command := registerCommandFixture("idempotency-register")
		hash, hashErr := modeldispatch.PrincipalRequestHash(command.OrganizationID, command.PrincipalKey, command.DispatchActorRoleID, command.PrincipalKind, "empresa/human")
		if hashErr != nil {
			t.Fatal(hashErr)
		}
		first, err := store.RegisterPrincipal(ctx, modeldispatch.PreparedRegisterPrincipal{Command: command, RequestHash: hash, RegisteredByRoleID: "empresa/human"})
		if err != nil || first.Reused {
			t.Fatalf("first=%+v err=%v", first, err)
		}
		second, err := store.RegisterPrincipal(ctx, modeldispatch.PreparedRegisterPrincipal{Command: command, RequestHash: hash, RegisteredByRoleID: "empresa/human"})
		if err != nil || !second.Reused || second.Principal.ID != first.Principal.ID {
			t.Fatalf("second=%+v err=%v", second, err)
		}
		conflicting := command
		conflictHash, hashErr := modeldispatch.PrincipalRequestHash(command.OrganizationID, "different-key", command.DispatchActorRoleID, command.PrincipalKind, "empresa/human")
		if hashErr != nil {
			t.Fatal(hashErr)
		}
		if _, err = store.RegisterPrincipal(ctx, modeldispatch.PreparedRegisterPrincipal{Command: conflicting, RequestHash: conflictHash, RegisteredByRoleID: "empresa/human"}); !errors.Is(err, modeldispatch.ErrConflict) {
			t.Fatalf("expected idempotency conflict, got %v", err)
		}
	})

	t.Run("disable is idempotent and blocks future resolution as active", func(t *testing.T) {
		command := registerCommandFixture("idempotency-disable")
		hash, hashErr := modeldispatch.PrincipalRequestHash(command.OrganizationID, command.PrincipalKey, command.DispatchActorRoleID, command.PrincipalKind, "empresa/human")
		if hashErr != nil {
			t.Fatal(hashErr)
		}
		registered, err := store.RegisterPrincipal(ctx, modeldispatch.PreparedRegisterPrincipal{Command: command, RequestHash: hash, RegisteredByRoleID: "empresa/human"})
		if err != nil {
			t.Fatal(err)
		}
		first, err := store.DisablePrincipal(ctx, registered.Principal.ID, "empresa/human", "retired")
		if err != nil || first.Status != modeldispatch.PrincipalDisabled {
			t.Fatalf("first=%+v err=%v", first, err)
		}
		second, err := store.DisablePrincipal(ctx, registered.Principal.ID, "empresa/human", "retired")
		if err != nil || second.Status != modeldispatch.PrincipalDisabled {
			t.Fatalf("disable is not idempotent: %+v err=%v", second, err)
		}
		resolved, err := store.ResolveByKey(ctx, dispatchIntegrationOrganization, command.PrincipalKey)
		if err != nil || resolved.Status != modeldispatch.PrincipalDisabled {
			t.Fatalf("resolved=%+v err=%v", resolved, err)
		}
	})

	t.Run("only one active assignment per task attempt", func(t *testing.T) {
		principal := registerFixturePrincipal(t, ctx, store, "one-active-per-attempt")
		task := insertDispatchTaskFixture(t, ctx, platform, revision.ID, "one-active-per-attempt")
		first := createAssignmentFixture(t, ctx, store, principal, task, revision.ID, "assignment-first")
		if first.Assignment.Status != modeldispatch.AssignmentActive {
			t.Fatalf("first assignment not active: %+v", first)
		}
		_, err := store.CreateAssignment(ctx, prepareAssignmentCommand(principal, task, revision.ID, "assignment-second"))
		if !errors.Is(err, modeldispatch.ErrAssignmentConflict) {
			t.Fatalf("expected one-active-assignment conflict, got %v", err)
		}
		revoked, err := store.RevokeAssignment(ctx, first.Assignment.ID, "empresa/human", "superseded")
		if err != nil || revoked.Status != modeldispatch.AssignmentRevoked {
			t.Fatalf("revoke=%+v err=%v", revoked, err)
		}
		reRevoked, err := store.RevokeAssignment(ctx, first.Assignment.ID, "empresa/human", "superseded")
		if err != nil || reRevoked.Status != modeldispatch.AssignmentRevoked {
			t.Fatalf("revoke is not idempotent: %+v err=%v", reRevoked, err)
		}
		second, err := store.CreateAssignment(ctx, prepareAssignmentCommand(principal, task, revision.ID, "assignment-second"))
		if err != nil || second.Assignment.Status != modeldispatch.AssignmentActive {
			t.Fatalf("second assignment after revoke=%+v err=%v", second, err)
		}
	})

	t.Run("expire moves past-due active assignments and is idempotent", func(t *testing.T) {
		principal := registerFixturePrincipal(t, ctx, store, "expire-fixture")
		task := insertDispatchTaskFixture(t, ctx, platform, revision.ID, "expire-fixture")
		created := createAssignmentFixture(t, ctx, store, principal, task, revision.ID, "assignment-expire")
		// Simulate time having passed beyond valid_until by supplying a future
		// "now" to Expire, rather than mutating valid_from/valid_until directly
		// (which would fight the immutability and ordering CHECK constraints).
		// Expire operates organization-wide, so an earlier subtest's leftover
		// active assignment (e.g. the replacement created after a revoke) may
		// also be swept up here; assert on this fixture's own row via GetAssignment
		// below rather than requiring an exact global count.
		wellPastValidUntil := task.LeaseExpiresAt.Add(time.Hour)
		result, err := store.ExpireAssignments(ctx, dispatchIntegrationOrganization, 100, wellPastValidUntil)
		if err != nil || result.Expired < 1 {
			t.Fatalf("expire=%+v err=%v", result, err)
		}
		again, err := store.ExpireAssignments(ctx, dispatchIntegrationOrganization, 100, wellPastValidUntil)
		if err != nil || again.Expired != 0 {
			t.Fatalf("expire is not idempotent: %+v err=%v", again, err)
		}
		loaded, err := store.GetAssignment(ctx, created.Assignment.ID)
		if err != nil || loaded.Status != modeldispatch.AssignmentExpired {
			t.Fatalf("loaded=%+v err=%v", loaded, err)
		}
	})

	t.Run("quota concurrency never exceeds max_invocations", func(t *testing.T) {
		principal := registerFixturePrincipal(t, ctx, store, "quota-concurrency")
		task := insertDispatchTaskFixture(t, ctx, platform, revision.ID, "quota-concurrency")
		created := createAssignmentFixtureWithQuota(t, ctx, store, principal, task, revision.ID, "assignment-quota", 3)
		var wg sync.WaitGroup
		const attempts = 6
		results := make(chan bool, attempts)
		for i := 0; i < attempts; i++ {
			wg.Add(1)
			go func(index int) {
				defer wg.Done()
				// Mirrors the production locking pattern in
				// internal/modelruntime/postgres/presend.go: lock the row with
				// FOR UPDATE before deciding whether quota remains, inside one
				// transaction, so concurrent consumers serialize on the lock
				// instead of racing on a bare UPDATE's WHERE clause.
				tx, beginErr := platform.Pool().Begin(ctx)
				if beginErr != nil {
					results <- false
					return
				}
				defer func() { _ = tx.Rollback(ctx) }()
				var status string
				var used, max int
				if scanErr := tx.QueryRow(ctx, `SELECT status,used_invocations,max_invocations FROM model_dispatcher_assignments WHERE id=$1 FOR UPDATE`, created.Assignment.ID).Scan(&status, &used, &max); scanErr != nil {
					results <- false
					return
				}
				if status != "active" || used >= max {
					results <- false
					return
				}
				newUsed := used + 1
				newStatus := "active"
				if newUsed >= max {
					newStatus = "exhausted"
				}
				if _, execErr := tx.Exec(ctx, `UPDATE model_dispatcher_assignments SET used_invocations=$2,status=$3,terminal_at=CASE WHEN $3='exhausted' THEN clock_timestamp() ELSE terminal_at END,updated_at=clock_timestamp() WHERE id=$1`, created.Assignment.ID, newUsed, newStatus); execErr != nil {
					results <- false
					return
				}
				if commitErr := tx.Commit(ctx); commitErr != nil {
					results <- false
					return
				}
				results <- true
			}(i)
		}
		wg.Wait()
		close(results)
		won := 0
		for value := range results {
			if value {
				won++
			}
		}
		if won != 3 {
			t.Fatalf("expected exactly 3 successful quota consumptions, got %d", won)
		}
		loaded, err := store.GetAssignment(ctx, created.Assignment.ID)
		if err != nil || loaded.UsedInvocations != 3 || loaded.Status != modeldispatch.AssignmentExhausted {
			t.Fatalf("loaded=%+v err=%v", loaded, err)
		}
	})

	t.Run("resolve active returns principal and assignment together", func(t *testing.T) {
		principal := registerFixturePrincipal(t, ctx, store, "resolve-active")
		task := insertDispatchTaskFixture(t, ctx, platform, revision.ID, "resolve-active")
		created := createAssignmentFixture(t, ctx, store, principal, task, revision.ID, "assignment-resolve")
		resolved, err := store.ResolveActive(ctx, dispatchIntegrationOrganization, task.TaskID, task.AttemptID, "ingenieria_ia/code-runner")
		if err != nil || resolved.Assignment.ID != created.Assignment.ID || resolved.Principal.ID != principal.ID {
			t.Fatalf("resolved=%+v err=%v", resolved, err)
		}
		byID, err := store.GetByID(ctx, dispatchIntegrationOrganization, created.Assignment.ID)
		if err != nil || byID.Assignment.ID != created.Assignment.ID {
			t.Fatalf("byID=%+v err=%v", byID, err)
		}
	})
}

func registerCommandFixture(suffix string) modeldispatch.RegisterPrincipalCommand {
	return modeldispatch.RegisterPrincipalCommand{
		OrganizationID: dispatchIntegrationOrganization, PrincipalKey: "integration/" + suffix,
		DispatchActorRoleID: "ingenieria_ia/code-runner", PrincipalKind: modeldispatch.PrincipalLocalProcess,
		IdempotencyKey: "principal-" + suffix,
	}
}

func registerFixturePrincipal(t *testing.T, ctx context.Context, store *dispatchpostgres.Store, suffix string) modeldispatch.ExecutionPrincipal {
	t.Helper()
	command := registerCommandFixture(suffix)
	hash, err := modeldispatch.PrincipalRequestHash(command.OrganizationID, command.PrincipalKey, command.DispatchActorRoleID, command.PrincipalKind, "empresa/human")
	if err != nil {
		t.Fatal(err)
	}
	registered, err := store.RegisterPrincipal(ctx, modeldispatch.PreparedRegisterPrincipal{Command: command, RequestHash: hash, RegisteredByRoleID: "empresa/human"})
	if err != nil {
		t.Fatal(err)
	}
	return registered.Principal
}

func prepareAssignmentCommand(principal modeldispatch.ExecutionPrincipal, task taskFixtureRef, revisionID int64, idempotencyKey string) modeldispatch.PreparedCreateAssignment {
	return prepareAssignmentCommandWithQuota(principal, task, revisionID, idempotencyKey, 1)
}

func prepareAssignmentCommandWithQuota(principal modeldispatch.ExecutionPrincipal, task taskFixtureRef, revisionID int64, idempotencyKey string, maxInvocations int) modeldispatch.PreparedCreateAssignment {
	validFrom := time.Now().UTC()
	validUntil := task.LeaseExpiresAt
	assignmentHash, _ := modeldispatch.AssignmentScopeHash(dispatchIntegrationOrganization, revisionID, task.TaskID, task.AttemptID, "ingenieria_ia/code-runner", principal.DispatchActorRoleID, principal.ID, maxInvocations, validFrom, validUntil)
	requestHash, _ := modeldispatch.AssignmentRequestHash(dispatchIntegrationOrganization, revisionID, task.TaskID, task.AttemptID, "ingenieria_ia/code-runner", principal.ID, principal.PrincipalKey, principal.DispatchActorRoleID, validFrom, validUntil, maxInvocations, "empresa/human")
	return modeldispatch.PreparedCreateAssignment{
		Command: modeldispatch.CreateAssignmentCommand{
			OrganizationID: dispatchIntegrationOrganization, TaskID: task.TaskID, AttemptID: task.AttemptID,
			SubjectRoleID: "ingenieria_ia/code-runner", ExecutionPrincipalKey: principal.PrincipalKey,
			MaxInvocations: maxInvocations, IdempotencyKey: idempotencyKey,
		},
		Principal: principal, OrganizationRevisionID: revisionID,
		ValidFrom: validFrom, ValidUntil: validUntil,
		AssignmentHash: assignmentHash, RequestHash: requestHash, CreatedByRoleID: "empresa/human",
	}
}

func createAssignmentFixture(t *testing.T, ctx context.Context, store *dispatchpostgres.Store, principal modeldispatch.ExecutionPrincipal, task taskFixtureRef, revisionID int64, idempotencyKey string) modeldispatch.CreateAssignmentResult {
	t.Helper()
	result, err := store.CreateAssignment(ctx, prepareAssignmentCommand(principal, task, revisionID, idempotencyKey))
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func createAssignmentFixtureWithQuota(t *testing.T, ctx context.Context, store *dispatchpostgres.Store, principal modeldispatch.ExecutionPrincipal, task taskFixtureRef, revisionID int64, idempotencyKey string, maxInvocations int) modeldispatch.CreateAssignmentResult {
	t.Helper()
	result, err := store.CreateAssignment(ctx, prepareAssignmentCommandWithQuota(principal, task, revisionID, idempotencyKey, maxInvocations))
	if err != nil {
		t.Fatal(err)
	}
	return result
}

type taskFixtureRef struct {
	TaskID         int64
	AttemptID      int64
	LeaseExpiresAt time.Time
}

func insertDispatchTaskFixture(t *testing.T, ctx context.Context, store *platformpostgres.Store, revisionID int64, suffix string) taskFixtureRef {
	t.Helper()
	now := time.Now().UTC()
	var taskID int64
	if err := store.Pool().QueryRow(ctx, `INSERT INTO tasks(organization_id,organization_revision_id,assigned_role_id,assigned_unit_id,idempotency_key,request_hash,title,instructions,acceptance_criteria,status,priority,available_at,max_attempts,attempt_count,version) VALUES($1,$2,'ingenieria_ia/code-runner','ingenieria_ia',$3,$4,'Model dispatch integration','Exercise dispatcher assignments.','[]','running',0,$5,5,1,1) RETURNING id`, dispatchIntegrationOrganization, revisionID, "dispatch-task-"+suffix, hexFixture("task-"+suffix), now).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	var attemptID int64
	if err := store.Pool().QueryRow(ctx, `INSERT INTO task_attempts(task_id,ordinal,state,worker_id,leased_at,started_at,created_at,updated_at) VALUES($1,1,'running','integration-worker',$2,$2,$2,$2) RETURNING id`, taskID, now).Scan(&attemptID); err != nil {
		t.Fatal(err)
	}
	leaseExpiry := now.Add(30 * time.Minute)
	if _, err := store.Pool().Exec(ctx, `INSERT INTO task_leases(task_id,attempt_id,token_hash,holder_id,status,issued_at,heartbeat_at,expires_at) VALUES($1,$2,$3,'integration-worker','active',$4,$4,$5)`, taskID, attemptID, hexFixture("lease-"+suffix), now, leaseExpiry); err != nil {
		t.Fatal(err)
	}
	return taskFixtureRef{TaskID: taskID, AttemptID: attemptID, LeaseExpiresAt: leaseExpiry}
}

func hexFixture(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:])
}

func openDispatchStore(t *testing.T, ctx context.Context) *platformpostgres.Store {
	t.Helper()
	url := os.Getenv("ORG_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("ORG_TEST_DATABASE_URL is required")
	}
	cfg := config.DatabaseConfig{URL: url, SSLMode: "disable", MaxConns: 30, MinConns: 0, MaxConnLifetime: time.Minute, MaxConnIdleTime: time.Minute, HealthCheckPeriod: time.Second, ConnectTimeout: 5 * time.Second, PingTimeout: 5 * time.Second, StatementTimeout: 30 * time.Second, LockTimeout: 5 * time.Second, AutoMigrate: true, MigrationTimeout: 45 * time.Second, MigrationRetry: time.Second}
	store, err := platformpostgres.Open(ctx, cfg, "model-dispatch-integration")
	if err != nil {
		t.Fatal(err)
	}
	if err := testdbguard.RequireTestDatabase(ctx, os.Getenv("ORG_TEST_DATABASE_URL"), store.Pool()); err != nil {
		store.Close()
		t.Fatalf("refusing to run against unverified database: %v", err)
	}
	return store
}

func resetDispatchSchema(t *testing.T, ctx context.Context, store *platformpostgres.Store) {
	t.Helper()
	if err := testdbguard.RequireDestructive(ctx, os.Getenv("ORG_TEST_DATABASE_URL"), store.Pool()); err != nil {
		t.Fatalf("refusing destructive TRUNCATE: %v", err)
	}
	_, err := store.Pool().Exec(ctx, `TRUNCATE model_dispatcher_assignment_uses,model_dispatcher_assignments,model_execution_principals,model_egress_evaluations,model_invocation_usage,model_invocation_results,model_dispatch_attempts,model_invocations,model_egress_revision_bindings,model_egress_rules,model_egress_policy_versions,role_model_bindings,model_capability_snapshots,model_profile_versions,model_profiles,model_providers,context_segments,context_snapshots,authorization_uses,authorization_decisions,authorization_requests,staging_events,staging_reviews,staging_promotions,staging_checks,staging_workspace_artifacts,staging_artifacts,staging_workspaces,outbox_events,task_dead_letters,task_events,task_leases,task_attempts,task_evidence,task_requirements,task_dependencies,tasks,organization_reporting_lines,organization_registry_revision_documents,organization_roles,organizational_units,organizations,organization_registry_revisions,audit_events RESTART IDENTITY CASCADE`)
	if err != nil {
		t.Fatal(err)
	}
}

func syncDispatchCanonical(t *testing.T, ctx context.Context, store *platformpostgres.Store) {
	t.Helper()
	repo, err := registry.NewPostgresRepository(store)
	if err != nil {
		t.Fatal(err)
	}
	loader, err := registry.NewLoader(filepath.Join("..", "..", "..", "docs", "canonical"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := registry.NewService(loader, repo, dispatchIntegrationOrganization, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.SynchronizeCanonical(ctx, true)
	if err != nil || !result.Applied {
		t.Fatalf("sync=%+v err=%v", result, err)
	}
}
