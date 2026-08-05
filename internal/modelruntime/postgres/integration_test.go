//go:build integration

package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
	"github.com/Mireuz13/explorarte-organization/internal/modelruntime/adapter"
	modelpostgres "github.com/Mireuz13/explorarte-organization/internal/modelruntime/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
)

const modelIntegrationOrganization = "explorarte"

func TestModelRuntimeGatewayPostgreSQL17(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	platform := openModelStore(t, ctx)
	defer platform.Close()
	runner, err := platformmigrations.New(platform.Pool(), rootmigrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = runner.Up(ctx); err != nil {
		t.Fatalf("migration 000007: %v", err)
	}
	resetModelSchema(t, ctx, platform)
	syncModelCanonical(t, ctx, platform)
	store, err := modelpostgres.New(platform)
	if err != nil {
		t.Fatal(err)
	}

	repo, err := registry.NewPostgresRepository(platform)
	if err != nil {
		t.Fatal(err)
	}
	revision, err := repo.GetCurrentRevision(ctx, modelIntegrationOrganization)
	if err != nil || revision == nil {
		t.Fatalf("revision=%+v err=%v", revision, err)
	}
	roles, err := repo.ListRoles(ctx, modelIntegrationOrganization, registry.RoleFilter{})
	if err != nil {
		t.Fatal(err)
	}
	baseCatalog := catalogFixture{organization: modelruntime.OrganizationRef{ID: modelIntegrationOrganization, RevisionID: revision.ID, ModelRoutingHash: revision.DocumentHashes["model-routing.yaml"]}, roles: convertRoles(roles)}

	t.Run("canonical real providers materialize unavailable and sync is idempotent", func(t *testing.T) {
		service, newErr := modelruntime.NewRegistryService(filepath.Join("..", "..", "..", "docs", "canonical"), modelIntegrationOrganization, baseCatalog, store)
		if newErr != nil {
			t.Fatal(newErr)
		}
		first, syncErr := service.Sync(ctx, true, 10)
		if syncErr != nil || !first.Applied {
			t.Fatalf("first=%+v err=%v", first, syncErr)
		}
		second, syncErr := service.Sync(ctx, true, 10)
		if syncErr != nil || !second.NoOp {
			t.Fatalf("second=%+v err=%v", second, syncErr)
		}
		var enabled, available int
		if err := platform.Pool().QueryRow(ctx, `SELECT count(*) FILTER (WHERE dispatch_enabled), count(*) FILTER (WHERE adapter_status='available') FROM model_profile_versions WHERE organization_revision_id=$1`, revision.ID).Scan(&enabled, &available); err != nil {
			t.Fatal(err)
		}
		if enabled != 0 || available != 0 {
			t.Fatalf("real providers enabled=%d available=%d", enabled, available)
		}
	})

	fakeRoutingHash := modelruntime.SHA256Bytes([]byte("test.fake canonical routing fixture v1"))
	fakeRevisionID := insertFakeRoutingRevision(t, ctx, platform, fakeRoutingHash)
	fakeCatalog := catalogFixture{
		organization: modelruntime.OrganizationRef{ID: modelIntegrationOrganization, RevisionID: fakeRevisionID, ModelRoutingHash: fakeRoutingHash},
		roles: map[string]modelruntime.RoleRef{
			"ingenieria_ia/qa": {ID: "ingenieria_ia/qa", ModelPolicy: "department.worker", Enabled: true, Executable: true, AuthorityClass: "specialist", UnitID: "ingenieria_ia"},
		},
	}
	fakeSync, err := store.ApplyRegistry(ctx, fakeRegistryPlan(fakeRevisionID, fakeRoutingHash), 10)
	if err != nil || !fakeSync.Applied {
		t.Fatalf("fake sync=%+v err=%v", fakeSync, err)
	}

	taskRef, snapshotRef := insertModelExecutionFixture(t, ctx, platform, fakeRevisionID)
	contexts := staticContextReader{ref: snapshotRef, rendered: []byte("safe integration context")}
	tasks := staticTaskReader{ref: taskRef}
	invocations, err := modelruntime.NewInvocationService(modelIntegrationOrganization, fakeCatalog, tasks, contexts, store, modelruntime.ClockFunc(time.Now), 10)
	if err != nil {
		t.Fatal(err)
	}
	cfg := modelruntime.RuntimeConfig{Enabled: true, CommandTimeout: 30 * time.Second, GlobalConcurrency: 4, MaxResponseBytes: 1 << 20, MaxToolIntents: 8, ClaimTTL: time.Minute, ReconcileBatchSize: 100, OutboxMaxAttempts: 10}
	dispatch, err := modelruntime.NewDispatchService(cfg, fakeCatalog, tasks, contexts, allowEvaluator{}, store, adapter.NewRegistry(adapter.NewFake()), modelruntime.ClockFunc(time.Now))
	if err != nil {
		t.Fatal(err)
	}

	t.Run("create reuse conflict and fake one-shot dispatch are durable", func(t *testing.T) {
		command := validInvocationCommand(taskRef, snapshotRef, "pg-fake-dispatch")
		created, createErr := invocations.Create(ctx, command)
		if createErr != nil || created.Reused {
			t.Fatalf("created=%+v err=%v", created, createErr)
		}
		reused, createErr := invocations.Create(ctx, command)
		if createErr != nil || !reused.Reused || reused.Invocation.ID != created.Invocation.ID {
			t.Fatalf("reused=%+v err=%v", reused, createErr)
		}
		conflict := command
		conflict.Purpose = "different immutable request"
		if _, createErr = invocations.Create(ctx, conflict); !errors.Is(createErr, modelruntime.ErrConflict) {
			t.Fatalf("expected idempotency conflict, got %v", createErr)
		}

		result, dispatchErr := dispatch.Dispatch(ctx, created.Invocation.ID, "integration-orgctl")
		if dispatchErr != nil || result.Invocation.Status != modelruntime.InvocationSucceeded || result.Result == nil || result.Usage == nil {
			t.Fatalf("result=%+v err=%v", result, dispatchErr)
		}
		if result.Result.OutputMode != modelruntime.OutputJSON || !strings.Contains(string(result.Result.JSONOutput), `"provider":"test.fake"`) {
			t.Fatalf("unexpected normalized result: %+v", result.Result)
		}
		assertModelCount(t, ctx, platform, `SELECT count(*) FROM model_invocation_results WHERE invocation_id=$1`, created.Invocation.ID, 1)
		assertModelCount(t, ctx, platform, `SELECT count(*) FROM model_invocation_usage WHERE invocation_id=$1`, created.Invocation.ID, 1)
		assertModelCount(t, ctx, platform, `SELECT count(*) FROM audit_events WHERE subject_type='model_invocation' AND subject_id=$1 AND event_type='model.invocation_succeeded'`, strconv.FormatInt(created.Invocation.ID, 10), 1)
		assertModelCount(t, ctx, platform, `SELECT count(*) FROM outbox_events WHERE aggregate_type='model_invocation' AND aggregate_id=$1 AND event_type='model.invocation_succeeded'`, strconv.FormatInt(created.Invocation.ID, 10), 1)

		var leaked int
		if err := platform.Pool().QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE subject_type='model_invocation' AND subject_id=$1 AND payload::text ~* '(safe integration context|hidden fake reasoning|rendered_context|claim_token)'`, strconv.FormatInt(created.Invocation.ID, 10)).Scan(&leaked); err != nil {
			t.Fatal(err)
		}
		if leaked != 0 {
			t.Fatalf("audit leaked sensitive runtime payload in %d rows", leaked)
		}
		if err := platform.Pool().QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE aggregate_type='model_invocation' AND aggregate_id=$1 AND payload::text ~* '(safe integration context|hidden fake reasoning|rendered_context|claim_token|json_output|text_output)'`, strconv.FormatInt(created.Invocation.ID, 10)).Scan(&leaked); err != nil {
			t.Fatal(err)
		}
		if leaked != 0 {
			t.Fatalf("outbox leaked sensitive runtime payload in %d rows", leaked)
		}
	})

	t.Run("claim token is hashed and concurrent claim has one winner", func(t *testing.T) {
		created := createModelInvocation(t, ctx, invocations, validInvocationCommand(taskRef, snapshotRef, "claim-race"))
		const contenders = 2
		results := make(chan modelruntime.ClaimedInvocation, contenders)
		errs := make(chan error, contenders)
		var wg sync.WaitGroup
		for i := 0; i < contenders; i++ {
			wg.Add(1)
			go func(index int) {
				defer wg.Done()
				claimed, claimErr := store.ClaimInvocation(ctx, modelruntime.ClaimCommand{InvocationID: created.ID, ClaimedBy: fmt.Sprintf("claimer-%d", index)}, cfg)
				if claimErr != nil {
					errs <- claimErr
					return
				}
				results <- claimed
			}(i)
		}
		wg.Wait()
		close(results)
		close(errs)
		var winner modelruntime.ClaimedInvocation
		for value := range results {
			if winner.Invocation.ID != 0 {
				t.Fatal("more than one claim winner")
			}
			winner = value
		}
		if winner.Invocation.ID == 0 || winner.ClaimToken == "" {
			t.Fatal("missing claim winner")
		}
		losers := 0
		for claimErr := range errs {
			if !errors.Is(claimErr, modelruntime.ErrClaimUnavailable) && !errors.Is(claimErr, modelruntime.ErrConflict) {
				t.Fatalf("unexpected loser error: %v", claimErr)
			}
			losers++
		}
		if losers != contenders-1 {
			t.Fatalf("losers=%d", losers)
		}
		var storedHash string
		if err := platform.Pool().QueryRow(ctx, `SELECT claim_token_hash FROM model_dispatch_attempts WHERE id=$1`, winner.DispatchAttempt.ID).Scan(&storedHash); err != nil {
			t.Fatal(err)
		}
		if storedHash == winner.ClaimToken || len(storedHash) != 64 {
			t.Fatalf("raw claim token persisted: %q", storedHash)
		}
		var leaked int
		if err := platform.Pool().QueryRow(ctx, `SELECT (SELECT count(*) FROM audit_events WHERE payload::text LIKE '%' || $1 || '%') + (SELECT count(*) FROM outbox_events WHERE payload::text LIKE '%' || $1 || '%')`, winner.ClaimToken).Scan(&leaked); err != nil {
			t.Fatal(err)
		}
		if leaked != 0 {
			t.Fatal("raw claim token leaked to event payload")
		}
		if _, err := invocations.Cancel(ctx, created.ID, "ingenieria_ia/qa", "integration cleanup"); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("global concurrency and safe reconcile never redispatch", func(t *testing.T) {
		limited := cfg
		limited.GlobalConcurrency = 1
		first := createModelInvocation(t, ctx, invocations, validInvocationCommand(taskRef, snapshotRef, "concurrency-first"))
		second := createModelInvocation(t, ctx, invocations, validInvocationCommand(taskRef, snapshotRef, "concurrency-second"))
		claim, claimErr := store.ClaimInvocation(ctx, modelruntime.ClaimCommand{InvocationID: first.ID, ClaimedBy: "one"}, limited)
		if claimErr != nil {
			t.Fatal(claimErr)
		}
		if _, claimErr = store.ClaimInvocation(ctx, modelruntime.ClaimCommand{InvocationID: second.ID, ClaimedBy: "two"}, limited); !errors.Is(claimErr, modelruntime.ErrConcurrencyLimit) {
			t.Fatalf("expected global concurrency limit, got %v", claimErr)
		}
		if _, err := platform.Pool().Exec(ctx, `UPDATE model_dispatch_attempts SET claimed_at=clock_timestamp()-interval '2 minutes',claim_expires_at=clock_timestamp()-interval '1 minute' WHERE id=$1`, claim.DispatchAttempt.ID); err != nil {
			t.Fatal(err)
		}
		reconciled, reconcileErr := invocations.Reconcile(ctx, 100)
		if reconcileErr != nil || reconciled.ReleasedBeforeSend != 1 {
			t.Fatalf("reconcile=%+v err=%v", reconciled, reconcileErr)
		}
		loaded, err := invocations.Get(ctx, first.ID)
		if err != nil || loaded.Status != modelruntime.InvocationRequested {
			t.Fatalf("released invocation=%+v err=%v", loaded, err)
		}
	})

	t.Run("down migration and reapply in disposable integration database", func(t *testing.T) {
		down, readErr := rootmigrations.Files.ReadFile("000007_create_model_runtime_gateway.down.sql")
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, err := platform.Pool().Exec(ctx, string(down)); err != nil {
			t.Fatalf("down migration: %v", err)
		}
		if _, err := platform.Pool().Exec(ctx, `DELETE FROM schema_migrations WHERE version=7`); err != nil {
			t.Fatal(err)
		}
		if _, err = runner.Up(ctx); err != nil {
			t.Fatalf("reapply migration: %v", err)
		}
		var exists bool
		if err = platform.Pool().QueryRow(ctx, `SELECT to_regclass('public.model_invocations') IS NOT NULL AND to_regclass('public.model_dispatch_attempts') IS NOT NULL AND to_regclass('public.model_invocation_results') IS NOT NULL`).Scan(&exists); err != nil || !exists {
			t.Fatalf("reapply exists=%v err=%v", exists, err)
		}
	})
}

type catalogFixture struct {
	organization modelruntime.OrganizationRef
	roles        map[string]modelruntime.RoleRef
}

func (c catalogFixture) CurrentOrganization(context.Context, string) (modelruntime.OrganizationRef, error) {
	return c.organization, nil
}
func (c catalogFixture) GetRole(_ context.Context, _, id string) (modelruntime.RoleRef, error) {
	value, ok := c.roles[id]
	if !ok {
		return modelruntime.RoleRef{}, modelruntime.ErrNotFound
	}
	return value, nil
}
func (c catalogFixture) ListRoles(context.Context, string) ([]modelruntime.RoleRef, error) {
	result := make([]modelruntime.RoleRef, 0, len(c.roles))
	for _, role := range c.roles {
		result = append(result, role)
	}
	return result, nil
}

type staticTaskReader struct{ ref modelruntime.TaskAttemptRef }

func (r staticTaskReader) GetTaskAttempt(context.Context, int64, int64) (modelruntime.TaskAttemptRef, error) {
	return r.ref, nil
}

type staticContextReader struct {
	ref      modelruntime.ContextSnapshotRef
	rendered []byte
}

func (r staticContextReader) GetContextSnapshot(context.Context, int64) (modelruntime.ContextSnapshotRef, error) {
	return r.ref, nil
}
func (r staticContextReader) ValidateContextSnapshot(context.Context, int64) error { return nil }
func (r staticContextReader) RenderContextSnapshot(context.Context, int64) ([]byte, error) {
	return append([]byte(nil), r.rendered...), nil
}

type allowEvaluator struct{}

func (allowEvaluator) EvaluateDispatch(context.Context, string, int64, string, string, string) (modelruntime.AuthorizationDecision, error) {
	return modelruntime.AuthorizationDecision{Allowed: true, ReasonCode: "allowed_by_grant"}, nil
}

func openModelStore(t *testing.T, ctx context.Context) *platformpostgres.Store {
	t.Helper()
	url := os.Getenv("ORG_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("ORG_TEST_DATABASE_URL is required")
	}
	cfg := config.DatabaseConfig{URL: url, SSLMode: "disable", MaxConns: 30, MinConns: 0, MaxConnLifetime: time.Minute, MaxConnIdleTime: time.Minute, HealthCheckPeriod: time.Second, ConnectTimeout: 5 * time.Second, PingTimeout: 5 * time.Second, StatementTimeout: 30 * time.Second, LockTimeout: 5 * time.Second, AutoMigrate: true, MigrationTimeout: 45 * time.Second, MigrationRetry: time.Second}
	store, err := platformpostgres.Open(ctx, cfg, "model-runtime-integration")
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func resetModelSchema(t *testing.T, ctx context.Context, store *platformpostgres.Store) {
	t.Helper()
	_, err := store.Pool().Exec(ctx, `TRUNCATE model_invocation_usage,model_invocation_results,model_dispatch_attempts,model_invocations,role_model_bindings,model_capability_snapshots,model_profile_versions,model_profiles,model_providers,context_segments,context_snapshots,authorization_uses,authorization_decisions,authorization_requests,staging_events,staging_reviews,staging_promotions,staging_checks,staging_workspace_artifacts,staging_artifacts,staging_workspaces,outbox_events,task_dead_letters,task_events,task_leases,task_attempts,task_evidence,task_requirements,task_dependencies,tasks,organization_reporting_lines,organization_registry_revision_documents,organization_roles,organizational_units,organizations,organization_registry_revisions,audit_events RESTART IDENTITY CASCADE`)
	if err != nil {
		t.Fatal(err)
	}
}

func syncModelCanonical(t *testing.T, ctx context.Context, store *platformpostgres.Store) {
	t.Helper()
	repo, err := registry.NewPostgresRepository(store)
	if err != nil {
		t.Fatal(err)
	}
	loader, err := registry.NewLoader(filepath.Join("..", "..", "..", "docs", "canonical"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := registry.NewService(loader, repo, modelIntegrationOrganization, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.SynchronizeCanonical(ctx, true)
	if err != nil || !result.Applied {
		t.Fatalf("sync=%+v err=%v", result, err)
	}
}

func convertRoles(values []registry.Role) map[string]modelruntime.RoleRef {
	result := make(map[string]modelruntime.RoleRef, len(values))
	for _, role := range values {
		policy := ""
		if role.ModelPolicy != nil {
			policy = *role.ModelPolicy
		}
		result[role.ID] = modelruntime.RoleRef{ID: role.ID, ModelPolicy: policy, Enabled: role.Enabled, Executable: role.Executable, AuthorityClass: role.AuthorityClass, UnitID: role.UnitID}
	}
	return result
}

func fakeRegistryPlan(revisionID int64, routingHash string) modelruntime.RegistryPlan {
	const (
		roleID    = "ingenieria_ia/qa"
		policyID  = "department.worker"
		profileID = "worker-default"
		provider  = "test.fake"
	)
	capabilities := []modelruntime.ModelCapability{"structured.output"}
	return modelruntime.RegistryPlan{
		OrganizationID:         modelIntegrationOrganization,
		OrganizationRevisionID: revisionID,
		CanonicalHash:          routingHash,
		Providers: []modelruntime.Provider{{
			OrganizationID:         modelIntegrationOrganization,
			ID:                     provider,
			Transport:              modelruntime.TransportFake,
			AdapterStatus:          modelruntime.AdapterAvailable,
			DispatchEnabled:        true,
			DirectHTTPForbidden:    false,
			CanonicalHash:          modelruntime.SHA256Bytes([]byte("fake-provider-" + routingHash)),
			OrganizationRevisionID: revisionID,
		}},
		Profiles: []modelruntime.Profile{{
			OrganizationID: modelIntegrationOrganization,
			ID:             profileID,
			PolicyID:       policyID,
		}},
		Versions: []modelruntime.ProfileVersion{{
			OrganizationID:         modelIntegrationOrganization,
			ProfileID:              profileID,
			OrganizationRevisionID: revisionID,
			CanonicalDocumentHash:  routingHash,
			VersionHash:            modelruntime.SHA256Bytes([]byte("fake-version-" + routingHash)),
			ProviderID:             provider,
			ProviderModelID:        "deterministic-v1",
			Transport:              modelruntime.TransportFake,
			AdapterStatus:          modelruntime.AdapterAvailable,
			DispatchEnabled:        true,
		}},
		CapabilitySnapshots: []modelruntime.CapabilitySnapshot{{
			OrganizationID: modelIntegrationOrganization,
			ProfileID:      profileID,
			Capabilities:   capabilities,
			CapabilityHash: modelruntime.SHA256Bytes([]byte("structured.output")),
		}},
		Bindings: []modelruntime.RoleBinding{{
			OrganizationID:         modelIntegrationOrganization,
			OrganizationRevisionID: revisionID,
			RoleID:                 roleID,
			PolicyID:               policyID,
			ProfileID:              profileID,
			BindingHash:            modelruntime.SHA256Bytes([]byte("fake-binding-" + routingHash)),
			Active:                 true,
		}},
	}
}

func insertFakeRoutingRevision(t *testing.T, ctx context.Context, store *platformpostgres.Store, routingHash string) int64 {
	t.Helper()
	documentHashes, err := json.Marshal(map[string]string{"model-routing.yaml": routingHash})
	if err != nil {
		t.Fatal(err)
	}
	var id int64
	if err = store.Pool().QueryRow(ctx, `INSERT INTO organization_registry_revisions(canonical_hash,status,schema_versions,document_hashes,counts,diff,applied_at) VALUES($1,'applied','{}',$2::jsonb,'{}','{}',clock_timestamp()) RETURNING id`, modelruntime.SHA256Bytes([]byte("fake-revision-"+routingHash)), documentHashes).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Pool().Exec(ctx, `UPDATE organizations SET current_revision_id=$1,updated_at=clock_timestamp() WHERE id=$2`, id, modelIntegrationOrganization); err != nil {
		t.Fatal(err)
	}
	return id
}

func insertModelExecutionFixture(t *testing.T, ctx context.Context, store *platformpostgres.Store, revisionID int64) (modelruntime.TaskAttemptRef, modelruntime.ContextSnapshotRef) {
	t.Helper()
	now := time.Now().UTC()
	var taskID int64
	if err := store.Pool().QueryRow(ctx, `INSERT INTO tasks(organization_id,organization_revision_id,assigned_role_id,assigned_unit_id,idempotency_key,request_hash,title,instructions,acceptance_criteria,status,priority,available_at,max_attempts,attempt_count,version) VALUES($1,$2,'ingenieria_ia/qa','ingenieria_ia','model-runtime-integration-task',$3,'Model runtime integration','Exercise fake one-shot dispatch.','[]','running',0,$4,5,1,1) RETURNING id`, modelIntegrationOrganization, revisionID, modelruntime.SHA256Bytes([]byte("task")), now).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	var attemptID int64
	if err := store.Pool().QueryRow(ctx, `INSERT INTO task_attempts(task_id,ordinal,state,worker_id,leased_at,started_at,created_at,updated_at) VALUES($1,1,'running','integration-worker',$2,$2,$2,$2) RETURNING id`, taskID, now).Scan(&attemptID); err != nil {
		t.Fatal(err)
	}
	leaseExpiry := now.Add(30 * time.Minute)
	if _, err := store.Pool().Exec(ctx, `INSERT INTO task_leases(task_id,attempt_id,token_hash,holder_id,status,issued_at,heartbeat_at,expires_at) VALUES($1,$2,$3,'integration-worker','active',$4,$4,$5)`, taskID, attemptID, modelruntime.SHA256Bytes([]byte("lease")), now, leaseExpiry); err != nil {
		t.Fatal(err)
	}
	rendered := []byte("safe integration context")
	var snapshotID int64
	if err := store.Pool().QueryRow(ctx, `INSERT INTO context_snapshots(organization_id,organization_revision_id,actor_role_id,purpose,task_ref,idempotency_key,request_hash,precedence_hash,canonical_bundle_hash,rendered_hash,status,version,segment_count,included_segment_count,omitted_segment_count,total_bytes,created_at) VALUES($1,$2,'ingenieria_ia/qa','model invocation',$3,'model-runtime-integration-context',$4,$5,$6,$7,'ready',1,0,0,0,0,$8) RETURNING id`, modelIntegrationOrganization, revisionID, strconv.FormatInt(taskID, 10), modelruntime.SHA256Bytes([]byte("context-request")), modelruntime.SHA256Bytes([]byte("precedence")), modelruntime.SHA256Bytes([]byte("bundle")), modelruntime.SHA256Bytes(rendered), now).Scan(&snapshotID); err != nil {
		t.Fatal(err)
	}
	return modelruntime.TaskAttemptRef{TaskID: taskID, AttemptID: attemptID, OrganizationID: modelIntegrationOrganization, OrganizationRevisionID: revisionID, AssignedRoleID: "ingenieria_ia/qa", TaskStatus: "running", AttemptStatus: "running", LeaseHolderID: "integration-worker", LeaseExpiresAt: leaseExpiry}, modelruntime.ContextSnapshotRef{ID: snapshotID, OrganizationID: modelIntegrationOrganization, OrganizationRevisionID: revisionID, ActorRoleID: "ingenieria_ia/qa", TaskRef: strconv.FormatInt(taskID, 10), Status: "ready", RenderedHash: modelruntime.SHA256Bytes(rendered), DataClasses: []string{"organizational"}}
}

func validInvocationCommand(task modelruntime.TaskAttemptRef, snapshot modelruntime.ContextSnapshotRef, key string) modelruntime.CreateInvocationCommand {
	return modelruntime.CreateInvocationCommand{OrganizationID: modelIntegrationOrganization, TaskID: task.TaskID, AttemptID: task.AttemptID, DispatchActorRoleID: "ingenieria_ia/qa", SubjectRoleID: "ingenieria_ia/qa", ContextSnapshotID: snapshot.ID, Purpose: "PostgreSQL fake adapter integration", RequiredCapabilities: []modelruntime.ModelCapability{"structured.output"}, OutputMode: modelruntime.OutputJSON, OutputSchema: json.RawMessage(`{"type":"object","required":["provider"],"properties":{"provider":{"type":"string"}}}`), MaxOutputTokens: 256, ThinkingMode: modelruntime.ThinkingDisabled, IdempotencyKey: key, CorrelationID: "model-integration", CausationID: "branch-08", Deadline: time.Now().UTC().Add(20 * time.Minute)}
}

func createModelInvocation(t *testing.T, ctx context.Context, service *modelruntime.InvocationService, command modelruntime.CreateInvocationCommand) modelruntime.Invocation {
	t.Helper()
	result, err := service.Create(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	return result.Invocation
}

func assertModelCount(t *testing.T, ctx context.Context, store *platformpostgres.Store, query string, arg any, want int) {
	t.Helper()
	var count int
	if err := store.Pool().QueryRow(ctx, query, arg).Scan(&count); err != nil || count != want {
		t.Fatalf("count=%d want=%d err=%v", count, want, err)
	}
}
