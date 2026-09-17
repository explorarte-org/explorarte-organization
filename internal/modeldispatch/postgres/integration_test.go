//go:build integration

package postgres_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/authorization"
	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/modeldispatch"
	dispatchpostgres "github.com/Mireuz13/explorarte-organization/internal/modeldispatch/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
	modelruntimepostgres "github.com/Mireuz13/explorarte-organization/internal/modelruntime/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
	taskpostgres "github.com/Mireuz13/explorarte-organization/internal/tasks/postgres"
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

	t.Run("authorized attempt boundary persists exact replay and rejects provenance or binding drift", func(t *testing.T) {
		taskStore, taskErr := taskpostgres.New(platform)
		if taskErr != nil {
			t.Fatal(taskErr)
		}
		reader := dispatchTaskReader{reader: taskStore}
		catalog := dispatchCatalog{reader: repo}
		authorizer, authErr := authorization.New(repo, dispatchIntegrationOrganization, filepath.Join("..", "..", "..", "docs", "canonical"))
		if authErr != nil {
			t.Fatal(authErr)
		}
		principal := registerFixturePrincipal(t, ctx, store, "authorized-attempt")
		insertBindingFixture(t, ctx, platform, revision.ID, revision.CanonicalHash, "empresa/ceo", "authorized-attempt")
		rootID := insertLineageTaskFixture(t, ctx, platform, revision.ID, "authorized-root", "empresa/ceo", "executive:authorized-attempt", "owner:authorized-attempt", "ready")
		attemptTaskID := insertLineageTaskFixture(t, ctx, platform, revision.ID, "authorized-child", "empresa/ceo", "executive:authorized-attempt", "task:"+strconv.FormatInt(rootID, 10), "running")
		attempt := insertRunningAttemptFixture(t, ctx, platform, attemptTaskID, "authorized-attempt")

		assignments, serviceErr := modeldispatch.NewAssignmentService(
			dispatchIntegrationOrganization, authorizer, catalog, reader, store, store,
			modeldispatch.ClockFunc(time.Now), 15*time.Minute, time.Hour,
		)
		if serviceErr != nil {
			t.Fatal(serviceErr)
		}
		provisioner, provisionerErr := modeldispatch.NewAuthorizedAttemptProvisioner(assignments, reader, store, principal.PrincipalKey)
		if provisionerErr != nil {
			t.Fatal(provisionerErr)
		}
		first, ensureErr := provisioner.EnsureAuthorizedAssignmentForRunningAttempt(ctx, attemptTaskID, attempt.AttemptID)
		if ensureErr != nil || first.Reused || first.Assignment.CreatedByRoleID != "empresa/human" {
			t.Fatalf("first=%+v err=%v", first, ensureErr)
		}
		replayed, ensureErr := provisioner.EnsureAuthorizedAssignmentForRunningAttempt(ctx, attemptTaskID, attempt.AttemptID)
		if ensureErr != nil || !replayed.Reused || replayed.Assignment.ID != first.Assignment.ID {
			t.Fatalf("replay=%+v err=%v", replayed, ensureErr)
		}

		if _, updateErr := platform.Pool().Exec(ctx, `UPDATE role_model_bindings SET binding_hash=$1 WHERE organization_id=$2 AND organization_revision_id=$3 AND role_id='empresa/ceo'`, hexFixture("changed-effective-binding"), dispatchIntegrationOrganization, revision.ID); updateErr != nil {
			t.Fatal(updateErr)
		}
		if _, ensureErr = provisioner.EnsureAuthorizedAssignmentForRunningAttempt(ctx, attemptTaskID, attempt.AttemptID); !errors.Is(ensureErr, modeldispatch.ErrConflict) {
			t.Fatalf("expected binding replay conflict, got %v", ensureErr)
		}

		missingBindingTaskID := insertLineageTaskFixture(t, ctx, platform, revision.ID, "missing-binding-child", "ingenieria_ia/qa", "executive:authorized-attempt", "task:"+strconv.FormatInt(rootID, 10), "running")
		missingBindingAttempt := insertRunningAttemptFixture(t, ctx, platform, missingBindingTaskID, "missing-binding")
		if _, ensureErr = provisioner.EnsureAuthorizedAssignmentForRunningAttempt(ctx, missingBindingTaskID, missingBindingAttempt.AttemptID); !errors.Is(ensureErr, modeldispatch.ErrTaskAttemptRejected) {
			t.Fatalf("expected missing binding rejection, got %v", ensureErr)
		}

		brokenTaskID := insertLineageTaskFixture(t, ctx, platform, revision.ID, "broken-ancestry-child", "empresa/ceo", "executive:authorized-attempt", "task:999999999", "running")
		brokenAttempt := insertRunningAttemptFixture(t, ctx, platform, brokenTaskID, "broken-ancestry")
		if _, ensureErr = provisioner.EnsureAuthorizedAssignmentForRunningAttempt(ctx, brokenTaskID, brokenAttempt.AttemptID); !errors.Is(ensureErr, modeldispatch.ErrTaskAttemptRejected) {
			t.Fatalf("expected broken ancestry rejection, got %v", ensureErr)
		}
		var unexpected int
		if countErr := platform.Pool().QueryRow(ctx, `SELECT COUNT(*) FROM model_dispatcher_assignments WHERE task_id=ANY($1::bigint[])`, []int64{missingBindingTaskID, brokenTaskID}).Scan(&unexpected); countErr != nil || unexpected != 0 {
			t.Fatalf("rejected attempts wrote assignments: count=%d err=%v", unexpected, countErr)
		}
	})

	// The four t.Run blocks below cover
	// CEO_CONVERSATIONAL_DISPATCH_ASSIGNMENT_BOUNDARY_V1's own test matrix
	// (A/B/C invalid-policy, D/E policy conflict) against REAL Postgres,
	// through AuthorizedAttemptProvisioner exactly as production wires it
	// -- complementing (not duplicating) the fake-store unit tests in
	// internal/modeldispatch/authorized_attempt_service_test.go (which
	// prove the provisioner's own digest/replay logic in isolation) and
	// the pre-existing "quota concurrency never exceeds max_invocations"
	// test above (which proves N>1 consumption is race-safe at the store
	// layer, independent of who chose N). ingenieria_ia/orquestador is
	// used as the subject role specifically so a fresh role_model_binding
	// can be inserted without depending on execution order relative to
	// the "authorized attempt boundary..." block above, which already
	// bound empresa/ceo at this same revision.
	t.Run("WithMaxInvocations provisions a bounded multi-invocation grant, default and ceiling preserved", func(t *testing.T) {
		taskStore, taskErr := taskpostgres.New(platform)
		if taskErr != nil {
			t.Fatal(taskErr)
		}
		reader := dispatchTaskReader{reader: taskStore}
		catalog := dispatchCatalog{reader: repo}
		authorizer, authErr := authorization.New(repo, dispatchIntegrationOrganization, filepath.Join("..", "..", "..", "docs", "canonical"))
		if authErr != nil {
			t.Fatal(authErr)
		}
		principal := registerFixturePrincipal(t, ctx, store, "quota-policy")
		insertBindingFixture(t, ctx, platform, revision.ID, revision.CanonicalHash, "ingenieria_ia/orquestador", "quota-policy")
		assignments, serviceErr := modeldispatch.NewAssignmentService(
			dispatchIntegrationOrganization, authorizer, catalog, reader, store, store,
			modeldispatch.ClockFunc(time.Now), 15*time.Minute, time.Hour,
		)
		if serviceErr != nil {
			t.Fatal(serviceErr)
		}

		// Invalid policy is rejected AT CONSTRUCTION, never at dispatch time.
		for _, invalid := range []int{0, -1, 65} {
			if _, constructErr := modeldispatch.NewAuthorizedAttemptProvisioner(assignments, reader, store, principal.PrincipalKey, modeldispatch.WithMaxInvocations(invalid)); !errors.Is(constructErr, modeldispatch.ErrInvalidRequest) {
				t.Fatalf("max_invocations=%d: expected construction to be rejected, got %v", invalid, constructErr)
			}
		}

		// A/L: default constructor -- Executive's unchanged shape -- still exactly MaxInvocations=1.
		defaultProvisioner, err := modeldispatch.NewAuthorizedAttemptProvisioner(assignments, reader, store, principal.PrincipalKey)
		if err != nil {
			t.Fatal(err)
		}
		rootID := insertLineageTaskFixture(t, ctx, platform, revision.ID, "quota-policy-root", "ingenieria_ia/orquestador", "executive:quota-policy", "owner:quota-policy", "ready")
		defaultTaskID := insertLineageTaskFixture(t, ctx, platform, revision.ID, "quota-policy-default-child", "ingenieria_ia/orquestador", "executive:quota-policy", "task:"+strconv.FormatInt(rootID, 10), "running")
		defaultAttempt := insertRunningAttemptFixture(t, ctx, platform, defaultTaskID, "quota-policy-default")
		defaultResult, err := defaultProvisioner.EnsureAuthorizedAssignmentForRunningAttempt(ctx, defaultTaskID, defaultAttempt.AttemptID)
		if err != nil || defaultResult.Assignment.MaxInvocations != 1 {
			t.Fatalf("default provisioner max_invocations=%d, want 1 (err=%v)", defaultResult.Assignment.MaxInvocations, err)
		}

		// B: WithMaxInvocations(8) -- ceochat's own MaxTurns-derived ceiling -- on an independent attempt.
		boundedProvisioner, err := modeldispatch.NewAuthorizedAttemptProvisioner(assignments, reader, store, principal.PrincipalKey, modeldispatch.WithMaxInvocations(8))
		if err != nil {
			t.Fatal(err)
		}
		boundedTaskID := insertLineageTaskFixture(t, ctx, platform, revision.ID, "quota-policy-bounded-child", "ingenieria_ia/orquestador", "executive:quota-policy", "task:"+strconv.FormatInt(rootID, 10), "running")
		boundedAttempt := insertRunningAttemptFixture(t, ctx, platform, boundedTaskID, "quota-policy-bounded")
		boundedResult, err := boundedProvisioner.EnsureAuthorizedAssignmentForRunningAttempt(ctx, boundedTaskID, boundedAttempt.AttemptID)
		if err != nil || boundedResult.Assignment.MaxInvocations != 8 {
			t.Fatalf("bounded provisioner max_invocations=%d, want 8 (err=%v)", boundedResult.Assignment.MaxInvocations, err)
		}

		// C: reuse -- the SAME bounded provisioner called again on the SAME attempt resolves the SAME assignment.
		reused, err := boundedProvisioner.EnsureAuthorizedAssignmentForRunningAttempt(ctx, boundedTaskID, boundedAttempt.AttemptID)
		if err != nil || !reused.Reused || reused.Assignment.ID != boundedResult.Assignment.ID {
			t.Fatalf("reuse result=%+v err=%v", reused, err)
		}
	})

	t.Run("differing max_invocations policy on the same attempt fails closed in both directions", func(t *testing.T) {
		taskStore, taskErr := taskpostgres.New(platform)
		if taskErr != nil {
			t.Fatal(taskErr)
		}
		reader := dispatchTaskReader{reader: taskStore}
		catalog := dispatchCatalog{reader: repo}
		authorizer, authErr := authorization.New(repo, dispatchIntegrationOrganization, filepath.Join("..", "..", "..", "docs", "canonical"))
		if authErr != nil {
			t.Fatal(authErr)
		}
		principal := registerFixturePrincipal(t, ctx, store, "quota-conflict")
		insertBindingFixture(t, ctx, platform, revision.ID, revision.CanonicalHash, "ingenieria_ia/qa", "quota-conflict")
		assignments, serviceErr := modeldispatch.NewAssignmentService(
			dispatchIntegrationOrganization, authorizer, catalog, reader, store, store,
			modeldispatch.ClockFunc(time.Now), 15*time.Minute, time.Hour,
		)
		if serviceErr != nil {
			t.Fatal(serviceErr)
		}
		defaultProvisioner, err := modeldispatch.NewAuthorizedAttemptProvisioner(assignments, reader, store, principal.PrincipalKey)
		if err != nil {
			t.Fatal(err)
		}
		boundedProvisioner, err := modeldispatch.NewAuthorizedAttemptProvisioner(assignments, reader, store, principal.PrincipalKey, modeldispatch.WithMaxInvocations(8))
		if err != nil {
			t.Fatal(err)
		}
		rootID := insertLineageTaskFixture(t, ctx, platform, revision.ID, "quota-conflict-root", "ingenieria_ia/qa", "executive:quota-conflict", "owner:quota-conflict", "ready")

		// D: existing max=1 (default), then a max=8 provisioner on the SAME attempt -- must fail closed, never widen.
		forwardTaskID := insertLineageTaskFixture(t, ctx, platform, revision.ID, "quota-conflict-forward", "ingenieria_ia/qa", "executive:quota-conflict", "task:"+strconv.FormatInt(rootID, 10), "running")
		forwardAttempt := insertRunningAttemptFixture(t, ctx, platform, forwardTaskID, "quota-conflict-forward")
		if _, err = defaultProvisioner.EnsureAuthorizedAssignmentForRunningAttempt(ctx, forwardTaskID, forwardAttempt.AttemptID); err != nil {
			t.Fatalf("seed max=1 assignment: %v", err)
		}
		if _, err = boundedProvisioner.EnsureAuthorizedAssignmentForRunningAttempt(ctx, forwardTaskID, forwardAttempt.AttemptID); !errors.Is(err, modeldispatch.ErrConflict) {
			t.Fatalf("expected conflict widening max=1 to max=8, got %v", err)
		}

		// E: reverse -- existing max=8, then a max=1 provisioner on the SAME attempt -- must also fail closed, never narrow.
		reverseTaskID := insertLineageTaskFixture(t, ctx, platform, revision.ID, "quota-conflict-reverse", "ingenieria_ia/qa", "executive:quota-conflict", "task:"+strconv.FormatInt(rootID, 10), "running")
		reverseAttempt := insertRunningAttemptFixture(t, ctx, platform, reverseTaskID, "quota-conflict-reverse")
		if _, err = boundedProvisioner.EnsureAuthorizedAssignmentForRunningAttempt(ctx, reverseTaskID, reverseAttempt.AttemptID); err != nil {
			t.Fatalf("seed max=8 assignment: %v", err)
		}
		if _, err = defaultProvisioner.EnsureAuthorizedAssignmentForRunningAttempt(ctx, reverseTaskID, reverseAttempt.AttemptID); !errors.Is(err, modeldispatch.ErrConflict) {
			t.Fatalf("expected conflict narrowing max=8 to max=1, got %v", err)
		}

		// Exact match, no silent widening or narrowing: precisely one row at 1, one at 8.
		rows, queryErr := platform.Pool().Query(ctx, `SELECT max_invocations FROM model_dispatcher_assignments WHERE task_id=ANY($1::bigint[]) ORDER BY task_id`, []int64{forwardTaskID, reverseTaskID})
		if queryErr != nil {
			t.Fatal(queryErr)
		}
		defer rows.Close()
		var maxes []int
		for rows.Next() {
			var m int
			if scanErr := rows.Scan(&m); scanErr != nil {
				t.Fatal(scanErr)
			}
			maxes = append(maxes, m)
		}
		if len(maxes) != 2 || maxes[0] != 1 || maxes[1] != 8 {
			t.Fatalf("persisted max_invocations=%v, want exactly [1 8] (no silent widening/narrowing)", maxes)
		}
	})

	// CEO_CONVERSATIONAL_DISPATCH_ASSIGNMENT_BOUNDARY_CLOSURE_V1 GAP 1,
	// REQUIRED TESTS B/C/D/E: durable compatibility for a real assignment
	// row seeded with the EXACT identity a pre-quota-policy binary would
	// have written (idempotency key computed by an independently
	// reimplemented legacy formula, never by calling the current
	// production helper -- see legacyAuthorizedAttemptIdempotencyKeyForPostgresTest
	// below and its sibling unit-level pin,
	// TestAuthorizedAttemptLegacyMax1DigestsAreByteIdenticalToThePreQuotaPolicyFormula,
	// in internal/modeldispatch/authorized_attempt_service_test.go).
	t.Run("a durable legacy max=1 assignment replays cleanly after the quota-policy upgrade", func(t *testing.T) {
		taskStore, taskErr := taskpostgres.New(platform)
		if taskErr != nil {
			t.Fatal(taskErr)
		}
		reader := dispatchTaskReader{reader: taskStore}
		catalog := dispatchCatalog{reader: repo}
		authorizer, authErr := authorization.New(repo, dispatchIntegrationOrganization, filepath.Join("..", "..", "..", "docs", "canonical"))
		if authErr != nil {
			t.Fatal(authErr)
		}
		principal := registerFixturePrincipal(t, ctx, store, "legacy-replay")
		// ingenieria_ia/qa already has a role_model_binding from the
		// "differing max_invocations policy..." block above (same
		// revision): model_profiles enforces UNIQUE(organization_id,
		// policy_id), so a role's binding can only be inserted once per
		// test function run, not once per suffix. Reusing it here (rather
		// than picking a fresh role) keeps this block's intent legible --
		// it is specifically about REPLAYING a legacy assignment on this
		// exact role, not about routing setup.
		authority, authorityErr := store.GetRoleRoutingAuthority(ctx, dispatchIntegrationOrganization, revision.ID, "ingenieria_ia/qa")
		if authorityErr != nil {
			t.Fatalf("load routing authority for legacy fixture: %v", authorityErr)
		}
		assignments, serviceErr := modeldispatch.NewAssignmentService(
			dispatchIntegrationOrganization, authorizer, catalog, reader, store, store,
			modeldispatch.ClockFunc(time.Now), 15*time.Minute, time.Hour,
		)
		if serviceErr != nil {
			t.Fatal(serviceErr)
		}
		rootID := insertLineageTaskFixture(t, ctx, platform, revision.ID, "legacy-replay-root", "ingenieria_ia/qa", "executive:legacy-replay", "owner:legacy-replay", "ready")
		legacyTaskID := insertLineageTaskFixture(t, ctx, platform, revision.ID, "legacy-replay-child", "ingenieria_ia/qa", "executive:legacy-replay", "task:"+strconv.FormatInt(rootID, 10), "running")
		legacyAttempt := insertRunningAttemptFixture(t, ctx, platform, legacyTaskID, "legacy-replay")

		// Seed AS THE PRE-CHANGE PROVISIONER WOULD HAVE: MaxInvocations=1
		// (the only value that could ever exist before this quota policy),
		// IdempotencyKey computed by the pinned legacy formula, created
		// directly through AssignmentService.Create -- never through
		// AuthorizedAttemptProvisioner, which is the thing under test here.
		legacyIdemKey := legacyAuthorizedAttemptIdempotencyKeyForPostgresTest(rootID, legacyTaskID, legacyAttempt.AttemptID, dispatchIntegrationOrganization, revision.ID, "ingenieria_ia/qa", principal, authority)
		// Read the lease expiry back through the same TaskAttemptReader
		// AssignmentService.Create itself uses (not the Go-side value
		// insertRunningAttemptFixture returned): TIMESTAMPTZ is
		// microsecond-precision, Go's time.Time is nanosecond-precision, and
		// Create's own "assignment vigency exceeds the active task lease"
		// check compares against whatever THIS read returns -- using the
		// pre-round-trip value here can be a few nanoseconds later than
		// what Postgres actually stored, tripping that check spuriously.
		freshLegacyAttempt, freshErr := reader.GetTaskAttempt(ctx, legacyTaskID, legacyAttempt.AttemptID)
		if freshErr != nil {
			t.Fatal(freshErr)
		}
		validUntil := freshLegacyAttempt.LeaseExpiresAt
		seeded, seedErr := assignments.Create(ctx, "empresa/human", modeldispatch.CreateAssignmentCommand{
			OrganizationID: dispatchIntegrationOrganization, TaskID: legacyTaskID, AttemptID: legacyAttempt.AttemptID,
			SubjectRoleID: "ingenieria_ia/qa", ExecutionPrincipalKey: principal.PrincipalKey,
			MaxInvocations: 1, ValidUntil: &validUntil, IdempotencyKey: legacyIdemKey,
		})
		if seedErr != nil {
			t.Fatalf("seed legacy assignment: %v", seedErr)
		}
		if seeded.Assignment.MaxInvocations != 1 || seeded.Assignment.IdempotencyKey != legacyIdemKey {
			t.Fatalf("legacy seed=%+v, want max=1 idempotency_key=%q", seeded.Assignment, legacyIdemKey)
		}

		// REQUIRED TEST B: the NEW default provisioner (max=1, unconfigured)
		// resolves the SAME durable row, exactly, no mutation, no second row.
		defaultProvisioner, err := modeldispatch.NewAuthorizedAttemptProvisioner(assignments, reader, store, principal.PrincipalKey)
		if err != nil {
			t.Fatal(err)
		}
		replayed, err := defaultProvisioner.EnsureAuthorizedAssignmentForRunningAttempt(ctx, legacyTaskID, legacyAttempt.AttemptID)
		if err != nil || !replayed.Reused || replayed.Assignment.ID != seeded.Assignment.ID || replayed.Assignment.MaxInvocations != 1 {
			t.Fatalf("legacy replay=%+v err=%v, want Reused=true same ID max=1", replayed, err)
		}
		var rowCount int
		if countErr := platform.Pool().QueryRow(ctx, `SELECT COUNT(*) FROM model_dispatcher_assignments WHERE task_id=$1 AND attempt_id=$2`, legacyTaskID, legacyAttempt.AttemptID).Scan(&rowCount); countErr != nil || rowCount != 1 {
			t.Fatalf("row count=%d err=%v, want exactly 1 (no second assignment written)", rowCount, countErr)
		}

		// REQUIRED TEST C: existing legacy max=1, new policy max=8 on the SAME attempt -> CONFLICT, no widening.
		boundedProvisioner, err := modeldispatch.NewAuthorizedAttemptProvisioner(assignments, reader, store, principal.PrincipalKey, modeldispatch.WithMaxInvocations(8))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = boundedProvisioner.EnsureAuthorizedAssignmentForRunningAttempt(ctx, legacyTaskID, legacyAttempt.AttemptID); !errors.Is(err, modeldispatch.ErrConflict) {
			t.Fatalf("expected conflict widening legacy max=1 to max=8, got %v", err)
		}

		// REQUIRED TEST D: existing max=8, new policy max=1 on an INDEPENDENT attempt -> CONFLICT, no narrowing.
		max8TaskID := insertLineageTaskFixture(t, ctx, platform, revision.ID, "legacy-replay-max8", "ingenieria_ia/qa", "executive:legacy-replay", "task:"+strconv.FormatInt(rootID, 10), "running")
		max8Attempt := insertRunningAttemptFixture(t, ctx, platform, max8TaskID, "legacy-replay-max8")
		max8First, err := boundedProvisioner.EnsureAuthorizedAssignmentForRunningAttempt(ctx, max8TaskID, max8Attempt.AttemptID)
		if err != nil || max8First.Assignment.MaxInvocations != 8 {
			t.Fatalf("seed max=8 assignment: result=%+v err=%v", max8First, err)
		}
		if _, err = defaultProvisioner.EnsureAuthorizedAssignmentForRunningAttempt(ctx, max8TaskID, max8Attempt.AttemptID); !errors.Is(err, modeldispatch.ErrConflict) {
			t.Fatalf("expected conflict narrowing max=8 to max=1, got %v", err)
		}

		// REQUIRED TEST E: max=8, same policy max=8 again on the SAME attempt -> exact reuse.
		max8Second, err := boundedProvisioner.EnsureAuthorizedAssignmentForRunningAttempt(ctx, max8TaskID, max8Attempt.AttemptID)
		if err != nil || !max8Second.Reused || max8Second.Assignment.ID != max8First.Assignment.ID {
			t.Fatalf("expected exact max=8 reuse, got result=%+v err=%v", max8Second, err)
		}
	})
}

// legacyAuthorizedAttemptIdempotencyKeyForPostgresTest is an INDEPENDENT
// reimplementation (never calling internal/modeldispatch's own
// authorizedAttemptIdempotencyKey, which is unexported and under test) of
// the formula that shipped before CEO_CONVERSATIONAL_DISPATCH_ASSIGNMENT_BOUNDARY_V1
// -- the exact byte layout a pre-quota-policy binary would have produced for
// MaxInvocations=1, the only value that could ever have existed. It exists
// so "a durable legacy max=1 assignment replays cleanly after the
// quota-policy upgrade" above can seed a row that is genuinely
// representative of pre-upgrade durable state, not merely of whatever the
// current (already-fixed) production formula happens to produce today.
func legacyAuthorizedAttemptIdempotencyKeyForPostgresTest(rootTaskID, taskID, attemptID int64, organizationID string, organizationRevisionID int64, assignedRoleID string, principal modeldispatch.ExecutionPrincipal, authority modeldispatch.RoleRoutingAuthorityRef) string {
	var authorityTail []string
	if authority.Kind == modeldispatch.RoleRoutingPoolPolicy {
		authorityTail = []string{"pool_policy", authority.PolicyID, authority.AuthorityHash}
	} else {
		authorityTail = []string{authority.ProfileID, strconv.FormatInt(authority.ModelProfileVersionID, 10), authority.AuthorityHash}
	}
	fields := append([]string{
		organizationID, strconv.FormatInt(organizationRevisionID, 10),
		strconv.FormatInt(rootTaskID, 10), strconv.FormatInt(taskID, 10), strconv.FormatInt(attemptID, 10),
		assignedRoleID, strconv.FormatInt(principal.ID, 10), principal.PrincipalKey, principal.DispatchActorRoleID,
	}, authorityTail...)
	body := strings.Join(fields, "\x00")
	sum := sha256.Sum256([]byte(body))
	return fmt.Sprintf("authorized-attempt/%d/%d/%s", taskID, attemptID, hex.EncodeToString(sum[:])[:32])
}

func registerCommandFixture(suffix string) modeldispatch.RegisterPrincipalCommand {
	return modeldispatch.RegisterPrincipalCommand{
		OrganizationID: dispatchIntegrationOrganization, PrincipalKey: "integration/" + suffix,
		DispatchActorRoleID: "ingenieria_ia/code-runner", PrincipalKind: modeldispatch.PrincipalLocalProcess,
		IdempotencyKey: "principal-" + suffix,
	}
}

type dispatchCatalog struct{ reader registry.Reader }

func (a dispatchCatalog) CurrentRevision(ctx context.Context, organizationID string) (int64, error) {
	revision, err := a.reader.GetCurrentRevision(ctx, organizationID)
	if err != nil {
		return 0, err
	}
	if revision == nil {
		return 0, registry.ErrNotFound
	}
	return revision.ID, nil
}

func (a dispatchCatalog) GetRole(ctx context.Context, organizationID, roleID string) (modeldispatch.RoleRef, error) {
	role, err := a.reader.GetRole(ctx, organizationID, roleID)
	if err != nil {
		return modeldispatch.RoleRef{}, err
	}
	return modeldispatch.RoleRef{ID: role.ID, Enabled: role.Enabled, Executable: role.Executable, AuthorityClass: role.AuthorityClass}, nil
}

type dispatchTaskReader struct{ reader tasks.TaskReader }

func (a dispatchTaskReader) GetTaskAttempt(ctx context.Context, taskID, attemptID int64) (modeldispatch.TaskAttemptRef, error) {
	detail, err := a.reader.GetTask(ctx, taskID)
	if err != nil {
		return modeldispatch.TaskAttemptRef{}, err
	}
	var attempt *tasks.Attempt
	for i := range detail.Attempts {
		if detail.Attempts[i].ID == attemptID {
			attempt = &detail.Attempts[i]
			break
		}
	}
	if attempt == nil || detail.ActiveLease == nil || detail.ActiveLease.AttemptID != attemptID {
		return modeldispatch.TaskAttemptRef{}, modeldispatch.ErrTaskAttemptRejected
	}
	return modeldispatch.TaskAttemptRef{
		TaskID: detail.Task.ID, AttemptID: attempt.ID, OrganizationID: detail.Task.OrganizationID,
		OrganizationRevisionID: detail.Task.OrganizationRevisionID, AssignedRoleID: detail.Task.AssignedRoleID,
		TaskStatus: string(detail.Task.Status), AttemptStatus: string(attempt.State),
		LeaseHolderID: detail.ActiveLease.HolderID, LeaseExpiresAt: detail.ActiveLease.ExpiresAt,
	}, nil
}

func (a dispatchTaskReader) GetTaskLineage(ctx context.Context, taskID int64) (modeldispatch.TaskLineageRef, error) {
	detail, err := a.reader.GetTask(ctx, taskID)
	if err != nil {
		return modeldispatch.TaskLineageRef{}, err
	}
	requester, correlation, causation := "", "", ""
	if detail.Task.RequestedByRoleID != nil {
		requester = *detail.Task.RequestedByRoleID
	}
	if detail.Task.CorrelationID != nil {
		correlation = *detail.Task.CorrelationID
	}
	if detail.Task.CausationID != nil {
		causation = *detail.Task.CausationID
	}
	return modeldispatch.TaskLineageRef{
		TaskID: detail.Task.ID, OrganizationID: detail.Task.OrganizationID,
		OrganizationRevisionID: detail.Task.OrganizationRevisionID, RequestedByRoleID: requester,
		AssignedRoleID: detail.Task.AssignedRoleID, CorrelationID: correlation, CausationID: causation,
	}, nil
}

// insertBindingFixture materializes a full static profile/version/binding
// chain for roleID, using roleID's OWN organization_roles.model_policy
// (already populated by syncDispatchCanonical from the real canonical
// docs) as the policy_id everywhere -- GetRoleRoutingAuthority requires a
// static binding's own policy_id to agree with the role's model_policy, the
// same way real ApplyRegistry-materialized bindings always do.
func insertBindingFixture(t *testing.T, ctx context.Context, store *platformpostgres.Store, revisionID int64, canonicalHash, roleID, suffix string) {
	t.Helper()
	providerID := "provider-" + suffix
	profileID := "profile-" + suffix
	var policyID string
	if err := store.Pool().QueryRow(ctx, `SELECT model_policy FROM organization_roles WHERE organization_id=$1 AND id=$2`, dispatchIntegrationOrganization, roleID).Scan(&policyID); err != nil {
		t.Fatalf("insertBindingFixture: load %q model_policy: %v", roleID, err)
	}
	if _, err := store.Pool().Exec(ctx, `
INSERT INTO model_providers(organization_id,id,transport,adapter_status,dispatch_enabled,direct_http_forbidden,canonical_hash,organization_revision_id)
VALUES($1,$2,'fake_adapter','available',true,true,$3,$4)`, dispatchIntegrationOrganization, providerID, canonicalHash, revisionID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool().Exec(ctx, `INSERT INTO model_profiles(organization_id,id,policy_id) VALUES($1,$2,$3)`, dispatchIntegrationOrganization, profileID, policyID); err != nil {
		t.Fatal(err)
	}
	var versionID int64
	if err := store.Pool().QueryRow(ctx, `
INSERT INTO model_profile_versions(organization_id,profile_id,version_number,organization_revision_id,canonical_document_hash,version_hash,provider_id,provider_model_id,transport,adapter_status,dispatch_enabled)
VALUES($1,$2,1,$3,$4,$5,$6,'fixture-v1','fake_adapter','available',true) RETURNING id`,
		dispatchIntegrationOrganization, profileID, revisionID, canonicalHash, hexFixture("version-"+suffix), providerID).Scan(&versionID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool().Exec(ctx, `INSERT INTO model_capability_snapshots(organization_id,model_profile_version_id,capabilities,capability_hash) VALUES($1,$2,'[]',$3)`, dispatchIntegrationOrganization, versionID, hexFixture("caps-"+suffix)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool().Exec(ctx, `
INSERT INTO role_model_bindings(organization_id,organization_revision_id,role_id,policy_id,profile_id,model_profile_version_id,binding_hash,active)
VALUES($1,$2,$3,$4,$5,$6,$7,true)`, dispatchIntegrationOrganization, revisionID, roleID, policyID, profileID, versionID, hexFixture("binding-"+suffix)); err != nil {
		t.Fatal(err)
	}
}

func insertLineageTaskFixture(t *testing.T, ctx context.Context, store *platformpostgres.Store, revisionID int64, suffix, assignedRoleID, correlationID, causationID, status string) int64 {
	t.Helper()
	attemptCount := 0
	if status == "running" {
		attemptCount = 1
	}
	assignedUnitID := strings.SplitN(assignedRoleID, "/", 2)[0]
	var taskID int64
	if err := store.Pool().QueryRow(ctx, `
INSERT INTO tasks(organization_id,organization_revision_id,requested_by_role_id,assigned_role_id,assigned_unit_id,idempotency_key,request_hash,title,instructions,acceptance_criteria,status,priority,available_at,max_attempts,attempt_count,version,correlation_id,causation_id)
VALUES($1,$2,'empresa/human',$3,$4,$5,$6,'Authorized dispatch integration','Exercise persisted provenance.','[]',$7,0,$8,5,$9,1,$10,$11) RETURNING id`,
		dispatchIntegrationOrganization, revisionID, assignedRoleID, assignedUnitID, "dispatch-lineage-"+suffix, hexFixture("task-"+suffix), status, time.Now().UTC(), attemptCount, correlationID, causationID).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	return taskID
}

func insertRunningAttemptFixture(t *testing.T, ctx context.Context, store *platformpostgres.Store, taskID int64, suffix string) taskFixtureRef {
	t.Helper()
	now := time.Now().UTC()
	var attemptID int64
	if err := store.Pool().QueryRow(ctx, `INSERT INTO task_attempts(task_id,ordinal,state,worker_id,leased_at,started_at,created_at,updated_at) VALUES($1,1,'running','integration-worker',$2,$2,$2,$2) RETURNING id`, taskID, now).Scan(&attemptID); err != nil {
		t.Fatal(err)
	}
	leaseExpiry := now.Add(30 * time.Minute)
	if _, err := store.Pool().Exec(ctx, `INSERT INTO task_leases(task_id,attempt_id,token_hash,holder_id,status,issued_at,heartbeat_at,expires_at) VALUES($1,$2,$3,'41','active',$4,$4,$5)`, taskID, attemptID, hexFixture("lineage-lease-"+suffix), now, leaseExpiry); err != nil {
		t.Fatal(err)
	}
	return taskFixtureRef{TaskID: taskID, AttemptID: attemptID, LeaseExpiresAt: leaseExpiry}
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

const (
	dispatchAuthorityPoolPolicyID      = "dispatch-authority-test.worker.pool"
	dispatchAuthorityPoolRoleID        = "ingenieria_ia/qa"
	dispatchAuthorityEmptyPoolPolicyID = "dispatch-authority-test.empty.pool"
	dispatchAuthorityEmptyPoolRoleID   = "ingenieria_ia/frontend"
)

const dispatchAuthorityPoolPolicyYAML = `  dispatch-authority-test.worker.pool:
    routing_mode: pool
    selector: free_capacity_v1
    allow_paid: false
    candidates:
      - provider: test.fake
        model: dispatch-authority-a
        transport: fake_adapter
        capacity_class: free_daily
        priority: 10
`

var dispatchAuthorityCanonicalDocumentNames = []string{
	"organization.yaml", "role-catalog.yaml", "leader-worker-map.yaml",
	"model-routing.yaml", "model-egress-policy.yaml", "capability-matrix.yaml",
	"instruction-precedence.yaml", "decisions-required.yaml", "source-manifest.yaml",
}

// buildDispatchAuthorityPoolCanonicalDir copies the real docs/canonical
// documents verbatim, then consistently rewrites BOTH role-catalog.yaml
// (dispatchAuthorityPoolRoleID's own model_policy line, so a real
// org-registry sync from this directory leaves organization_roles.model_policy
// naming the new pool policy -- GetRoleRoutingAuthority reads that column
// directly, no override mechanism exists) and model-routing.yaml (injecting
// the pool policy that role now names). Unlike the retry-failover E2E
// fixture (which could only ever touch ONE of routing/egress at a time,
// because a different validator's hardcoded productive egress allow-rules
// map has no test.fake entry), role-catalog.yaml and model-routing.yaml are
// SUPPOSED to cross-reference each other, and LoadCanonicalRouting's own
// validation is designed to confirm that agreement, not reject it -- so
// both can be modified together here.
func buildDispatchAuthorityPoolCanonicalDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	srcDir := filepath.Join("..", "..", "..", "docs", "canonical")
	for _, name := range dispatchAuthorityCanonicalDocumentNames {
		body, err := os.ReadFile(filepath.Join(srcDir, name))
		if err != nil {
			t.Fatalf("read real canonical document %s: %v", name, err)
		}
		switch name {
		case "role-catalog.yaml":
			text := string(body)
			anchor := "- id: " + dispatchAuthorityPoolRoleID
			idx := strings.Index(text, anchor)
			if idx < 0 {
				t.Fatalf("role %s not found in role-catalog.yaml", dispatchAuthorityPoolRoleID)
			}
			const old = "model_policy: department.worker"
			rel := strings.Index(text[idx:], old)
			if rel < 0 {
				t.Fatalf("model_policy line not found for %s", dispatchAuthorityPoolRoleID)
			}
			abs := idx + rel
			body = []byte(text[:abs] + "model_policy: " + dispatchAuthorityPoolPolicyID + text[abs+len(old):])
		case "model-routing.yaml":
			text := string(body)
			const marker = "routing_invariants:"
			mi := strings.Index(text, marker)
			if mi < 0 {
				t.Fatalf("routing_invariants marker not found in model-routing.yaml")
			}
			body = []byte(text[:mi] + dispatchAuthorityPoolPolicyYAML + text[mi:])
		}
		if err := os.WriteFile(filepath.Join(dir, name), body, 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

// TestRoleRoutingAuthorityPostgreSQL17 exercises Store.GetRoleRoutingAuthority
// directly against real Postgres: the pool happy path through the real
// canonical pipeline (YAML -> LoadCanonicalRouting -> BuildRegistryPlan ->
// ApplyRegistry, no manual routing_policies/routing_candidates rows), and
// every fail-closed edge case A3 requires -- each exercised as its own
// subtest against real rows, not a fake.
func TestRoleRoutingAuthorityPostgreSQL17(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	platform := openDispatchStore(t, ctx)
	defer platform.Close()
	runner, err := platformmigrations.New(platform.Pool(), rootmigrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = runner.Up(ctx); err != nil {
		t.Fatalf("migrations: %v", err)
	}
	resetDispatchSchema(t, ctx, platform)

	tmpDir := buildDispatchAuthorityPoolCanonicalDir(t)

	repo, err := registry.NewPostgresRepository(platform)
	if err != nil {
		t.Fatal(err)
	}
	loader, err := registry.NewLoader(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	regService, err := registry.NewService(loader, repo, dispatchIntegrationOrganization, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	syncResult, err := regService.SynchronizeCanonical(ctx, true)
	if err != nil || !syncResult.Applied {
		t.Fatalf("org registry sync=%+v err=%v", syncResult, err)
	}
	revision, err := repo.GetCurrentRevision(ctx, dispatchIntegrationOrganization)
	if err != nil || revision == nil {
		t.Fatalf("revision=%+v err=%v", revision, err)
	}

	routing, err := modelruntime.LoadCanonicalRouting(tmpDir)
	if err != nil {
		t.Fatalf("LoadCanonicalRouting: %v", err)
	}
	plan, err := modelruntime.BuildRegistryPlan(
		[]modelruntime.RoleRef{{ID: dispatchAuthorityPoolRoleID, ModelPolicy: dispatchAuthorityPoolPolicyID, Enabled: true, Executable: true, UnitID: "ingenieria_ia"}},
		modelruntime.OrganizationRef{ID: dispatchIntegrationOrganization, RevisionID: revision.ID},
		routing,
	)
	if err != nil {
		t.Fatalf("BuildRegistryPlan: %v", err)
	}
	modelStore, err := modelruntimepostgres.New(platform)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = modelStore.ApplyRegistry(ctx, plan, 10); err != nil {
		t.Fatalf("ApplyRegistry: %v", err)
	}

	dispatchStore, err := dispatchpostgres.New(platform)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("pool happy path materialized through the real canonical pipeline", func(t *testing.T) {
		authority, err := dispatchStore.GetRoleRoutingAuthority(ctx, dispatchIntegrationOrganization, revision.ID, dispatchAuthorityPoolRoleID)
		if err != nil {
			t.Fatalf("GetRoleRoutingAuthority: %v", err)
		}
		if authority.Kind != modeldispatch.RoleRoutingPoolPolicy {
			t.Fatalf("kind=%v want pool", authority.Kind)
		}
		if authority.PolicyID != dispatchAuthorityPoolPolicyID {
			t.Fatalf("policy id=%q want %q", authority.PolicyID, dispatchAuthorityPoolPolicyID)
		}
		if authority.ProfileID != "" || authority.ModelProfileVersionID != 0 {
			t.Fatalf("pool authority carries a synthetic profile: %+v", authority)
		}
		var canonicalHash string
		if scanErr := platform.Pool().QueryRow(ctx, `SELECT canonical_hash FROM routing_policies WHERE organization_id=$1 AND organization_revision_id=$2 AND policy_id=$3`, dispatchIntegrationOrganization, revision.ID, dispatchAuthorityPoolPolicyID).Scan(&canonicalHash); scanErr != nil {
			t.Fatal(scanErr)
		}
		if authority.AuthorityHash != canonicalHash {
			t.Fatalf("authority hash=%q want routing_policies.canonical_hash=%q", authority.AuthorityHash, canonicalHash)
		}
	})

	t.Run("revision drift fails closed", func(t *testing.T) {
		if _, err := dispatchStore.GetRoleRoutingAuthority(ctx, dispatchIntegrationOrganization, revision.ID+999, dispatchAuthorityPoolRoleID); err == nil {
			t.Fatal("expected fail-closed rejection for a revision the role was not last synced at")
		}
	})

	t.Run("disabled role fails closed", func(t *testing.T) {
		if _, err := platform.Pool().Exec(ctx, `UPDATE organization_roles SET enabled=false, executable=false WHERE organization_id=$1 AND id=$2`, dispatchIntegrationOrganization, dispatchAuthorityPoolRoleID); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if _, err := platform.Pool().Exec(ctx, `UPDATE organization_roles SET enabled=true, executable=true WHERE organization_id=$1 AND id=$2`, dispatchIntegrationOrganization, dispatchAuthorityPoolRoleID); err != nil {
				t.Fatal(err)
			}
		}()
		if _, err := dispatchStore.GetRoleRoutingAuthority(ctx, dispatchIntegrationOrganization, revision.ID, dispatchAuthorityPoolRoleID); err == nil {
			t.Fatal("expected fail-closed rejection for a disabled role")
		}
	})

	t.Run("non-executable role fails closed", func(t *testing.T) {
		if _, err := platform.Pool().Exec(ctx, `UPDATE organization_roles SET executable=false WHERE organization_id=$1 AND id=$2`, dispatchIntegrationOrganization, dispatchAuthorityPoolRoleID); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if _, err := platform.Pool().Exec(ctx, `UPDATE organization_roles SET executable=true WHERE organization_id=$1 AND id=$2`, dispatchIntegrationOrganization, dispatchAuthorityPoolRoleID); err != nil {
				t.Fatal(err)
			}
		}()
		if _, err := dispatchStore.GetRoleRoutingAuthority(ctx, dispatchIntegrationOrganization, revision.ID, dispatchAuthorityPoolRoleID); err == nil {
			t.Fatal("expected fail-closed rejection for a non-executable role")
		}
	})

	t.Run("missing model_policy fails closed", func(t *testing.T) {
		if _, err := platform.Pool().Exec(ctx, `UPDATE organization_roles SET model_policy=NULL WHERE organization_id=$1 AND id=$2`, dispatchIntegrationOrganization, dispatchAuthorityPoolRoleID); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if _, err := platform.Pool().Exec(ctx, `UPDATE organization_roles SET model_policy=$3 WHERE organization_id=$1 AND id=$2`, dispatchIntegrationOrganization, dispatchAuthorityPoolRoleID, dispatchAuthorityPoolPolicyID); err != nil {
				t.Fatal(err)
			}
		}()
		if _, err := dispatchStore.GetRoleRoutingAuthority(ctx, dispatchIntegrationOrganization, revision.ID, dispatchAuthorityPoolRoleID); err == nil {
			t.Fatal("expected fail-closed rejection for a role with no model_policy")
		}
	})

	t.Run("static and pool simultaneously fails closed", func(t *testing.T) {
		insertBindingFixture(t, ctx, platform, revision.ID, revision.CanonicalHash, dispatchAuthorityPoolRoleID, "authority-conflict")
		defer func() {
			if _, err := platform.Pool().Exec(ctx, `DELETE FROM role_model_bindings WHERE organization_id=$1 AND organization_revision_id=$2 AND role_id=$3`, dispatchIntegrationOrganization, revision.ID, dispatchAuthorityPoolRoleID); err != nil {
				t.Fatal(err)
			}
		}()
		if _, err := dispatchStore.GetRoleRoutingAuthority(ctx, dispatchIntegrationOrganization, revision.ID, dispatchAuthorityPoolRoleID); err == nil {
			t.Fatal("expected fail-closed rejection when both a static binding and a pool policy exist")
		}
	})

	t.Run("static binding whose own policy_id disagrees with model_policy fails closed", func(t *testing.T) {
		// A deliberate scope violation: role_model_bindings.policy_id names a
		// policy other than the one organization_roles.model_policy for this
		// role actually points at -- unlike insertBindingFixture, which
		// always derives a matching policy_id, this constructs the
		// mismatch directly.
		const mismatchedPolicyID = "authority-test.mismatched.policy"
		const mismatchedProfileID = "authority-test-mismatched-profile"
		const mismatchedProviderID = "authority-test-mismatched-provider"
		if _, err := platform.Pool().Exec(ctx, `INSERT INTO model_providers(organization_id,id,transport,adapter_status,dispatch_enabled,direct_http_forbidden,canonical_hash,organization_revision_id) VALUES($1,$2,'fake_adapter','available',true,true,$3,$4)`, dispatchIntegrationOrganization, mismatchedProviderID, revision.CanonicalHash, revision.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := platform.Pool().Exec(ctx, `INSERT INTO model_profiles(organization_id,id,policy_id) VALUES($1,$2,$3)`, dispatchIntegrationOrganization, mismatchedProfileID, mismatchedPolicyID); err != nil {
			t.Fatal(err)
		}
		var versionID int64
		if err := platform.Pool().QueryRow(ctx, `INSERT INTO model_profile_versions(organization_id,profile_id,version_number,organization_revision_id,canonical_document_hash,version_hash,provider_id,provider_model_id,transport,adapter_status,dispatch_enabled) VALUES($1,$2,1,$3,$4,$5,$6,'fixture-v1','fake_adapter','available',true) RETURNING id`, dispatchIntegrationOrganization, mismatchedProfileID, revision.ID, revision.CanonicalHash, hexFixture("authority-mismatch-version"), mismatchedProviderID).Scan(&versionID); err != nil {
			t.Fatal(err)
		}
		if _, err := platform.Pool().Exec(ctx, `INSERT INTO role_model_bindings(organization_id,organization_revision_id,role_id,policy_id,profile_id,model_profile_version_id,binding_hash,active) VALUES($1,$2,$3,$4,$5,$6,$7,true)`, dispatchIntegrationOrganization, revision.ID, dispatchAuthorityPoolRoleID, mismatchedPolicyID, mismatchedProfileID, versionID, hexFixture("authority-mismatch-binding")); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if _, err := platform.Pool().Exec(ctx, `DELETE FROM role_model_bindings WHERE organization_id=$1 AND organization_revision_id=$2 AND role_id=$3`, dispatchIntegrationOrganization, revision.ID, dispatchAuthorityPoolRoleID); err != nil {
				t.Fatal(err)
			}
		}()
		if _, err := dispatchStore.GetRoleRoutingAuthority(ctx, dispatchIntegrationOrganization, revision.ID, dispatchAuthorityPoolRoleID); err == nil {
			t.Fatal("expected fail-closed rejection for a static binding whose own policy_id disagrees with the role's model_policy")
		}
	})

	t.Run("pool policy with zero materialized candidates fails closed", func(t *testing.T) {
		if _, err := platform.Pool().Exec(ctx, `UPDATE organization_roles SET model_policy=$3 WHERE organization_id=$1 AND id=$2`, dispatchIntegrationOrganization, dispatchAuthorityEmptyPoolRoleID, dispatchAuthorityEmptyPoolPolicyID); err != nil {
			t.Fatal(err)
		}
		if _, err := platform.Pool().Exec(ctx, `INSERT INTO routing_policies(organization_id,organization_revision_id,policy_id,routing_mode,selector_id,allow_paid,canonical_hash) VALUES($1,$2,$3,'pool','free_capacity_v1',false,$4)`, dispatchIntegrationOrganization, revision.ID, dispatchAuthorityEmptyPoolPolicyID, hexFixture("authority-empty-pool")); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if _, err := platform.Pool().Exec(ctx, `DELETE FROM routing_policies WHERE organization_id=$1 AND organization_revision_id=$2 AND policy_id=$3`, dispatchIntegrationOrganization, revision.ID, dispatchAuthorityEmptyPoolPolicyID); err != nil {
				t.Fatal(err)
			}
		}()
		if _, err := dispatchStore.GetRoleRoutingAuthority(ctx, dispatchIntegrationOrganization, revision.ID, dispatchAuthorityEmptyPoolRoleID); err == nil {
			t.Fatal("expected fail-closed rejection for a pool policy with zero materialized candidates")
		}
	})
}
