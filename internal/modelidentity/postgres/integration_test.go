//go:build integration

package postgres_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/modeldispatch"
	dispatchpostgres "github.com/Mireuz13/explorarte-organization/internal/modeldispatch/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/modelidentity"
	identitypostgres "github.com/Mireuz13/explorarte-organization/internal/modelidentity/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
)

const identityIntegrationOrganization = "explorarte"

func TestModelExecutionIdentityPostgreSQL17(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	platform := openIdentityStore(t, ctx)
	defer platform.Close()
	runner, err := platformmigrations.New(platform.Pool(), rootmigrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	result, err := runner.Up(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Current != 16 {
		t.Fatalf("current migration=%d want=16", result.Current)
	}
	resetIdentitySchema(t, ctx, platform)
	syncIdentityCanonical(t, ctx, platform)

	identityStore, err := identitypostgres.New(platform)
	if err != nil {
		t.Fatal(err)
	}
	dispatchStore, err := dispatchpostgres.New(platform)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := modelidentity.LoadCanonicalPolicy(filepath.Join("..", "..", "..", "docs", "canonical"))
	if err != nil {
		t.Fatal(err)
	}
	policy, err := identityStore.Apply(ctx, identityIntegrationOrganization, canonical)
	if err != nil || !policy.Applied {
		t.Fatalf("policy=%+v err=%v", policy, err)
	}
	again, err := identityStore.Apply(ctx, identityIntegrationOrganization, canonical)
	if err != nil || !again.NoOp || again.PolicyVersionID != policy.PolicyVersionID {
		t.Fatalf("policy idempotency=%+v err=%v", again, err)
	}
	status, err := identityStore.Status(ctx, identityIntegrationOrganization, canonical)
	if err != nil || !status.Synchronized {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	if _, mutationErr := platform.Pool().Exec(ctx, `UPDATE model_execution_identity_policy_versions SET canonical_hash=$2 WHERE id=$1`, policy.PolicyVersionID, modelidentity.SHA256Bytes([]byte("mutated-policy"))); mutationErr == nil {
		t.Fatal("identity policy immutable scope accepted mutation")
	}

	principalCommand := modeldispatch.RegisterPrincipalCommand{
		OrganizationID: identityIntegrationOrganization, PrincipalKey: "identity/integration",
		DispatchActorRoleID: "ingenieria_ia/code-runner", PrincipalKind: modeldispatch.PrincipalLocalProcess,
		IdempotencyKey: "identity-principal",
	}
	principalHash, err := modeldispatch.PrincipalRequestHash(principalCommand.OrganizationID, principalCommand.PrincipalKey, principalCommand.DispatchActorRoleID, principalCommand.PrincipalKind, "empresa/human")
	if err != nil {
		t.Fatal(err)
	}
	principalResult, err := dispatchStore.RegisterPrincipal(ctx, modeldispatch.PreparedRegisterPrincipal{Command: principalCommand, RequestHash: principalHash, RegisteredByRoleID: "empresa/human"})
	if err != nil {
		t.Fatal(err)
	}

	first := preparedIdentityKey(t, principalResult.Principal.ID, "identity-key-1")
	registered, err := identityStore.RegisterKey(ctx, first)
	if err != nil || registered.Reused || registered.Key.Status != modelidentity.KeyActive || registered.Key.KeyVersion != 1 {
		t.Fatalf("registered=%+v err=%v", registered, err)
	}
	if _, mutationErr := platform.Pool().Exec(ctx, `UPDATE model_execution_identity_keys SET public_key_fingerprint=$2 WHERE id=$1`, registered.Key.ID, modelidentity.SHA256Bytes([]byte("mutated-key"))); mutationErr == nil {
		t.Fatal("identity key immutable scope accepted mutation")
	}
	reused, err := identityStore.RegisterKey(ctx, first)
	if err != nil || !reused.Reused || reused.Key.ID != registered.Key.ID {
		t.Fatalf("reused=%+v err=%v", reused, err)
	}
	conflict := first
	conflict.RequestHash = modelidentity.SHA256Bytes([]byte("different"))
	if _, err = identityStore.RegisterKey(ctx, conflict); !errors.Is(err, modelidentity.ErrKeyConflict) {
		t.Fatalf("expected idempotency conflict, got %v", err)
	}

	second := preparedIdentityKey(t, principalResult.Principal.ID, "identity-key-2")
	rotated, err := identityStore.RotateKey(ctx, second)
	if err != nil || rotated.Key.KeyVersion != 2 || rotated.Key.Status != modelidentity.KeyActive {
		t.Fatalf("rotated=%+v err=%v", rotated, err)
	}
	old, err := identityStore.GetKey(ctx, registered.Key.ID)
	if err != nil || old.Status != modelidentity.KeyRetiring {
		t.Fatalf("old=%+v err=%v", old, err)
	}
	retired, err := identityStore.RetireKey(ctx, old.ID, "empresa/human")
	if err != nil || retired.Status != modelidentity.KeyRetired {
		t.Fatalf("retired=%+v err=%v", retired, err)
	}
	revoked, err := identityStore.RevokeKey(ctx, rotated.Key.ID, "empresa/human", "integration_revoke")
	if err != nil || revoked.Status != modelidentity.KeyRevoked {
		t.Fatalf("revoked=%+v err=%v", revoked, err)
	}
	if _, err = identityStore.ResolveActiveKeyByFingerprint(ctx, identityIntegrationOrganization, principalResult.Principal.ID, rotated.Key.PublicKeyFingerprint); !errors.Is(err, modelidentity.ErrKeyNotFound) {
		t.Fatalf("revoked key resolved active: %v", err)
	}

	var privateKeyColumns int
	if err = platform.Pool().QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema='public' AND table_name LIKE 'model_execution_identity_%' AND column_name IN ('private_key','raw_nonce','signature')`).Scan(&privateKeyColumns); err != nil {
		t.Fatal(err)
	}
	if privateKeyColumns != 0 {
		t.Fatalf("sensitive identity persistence columns=%d", privateKeyColumns)
	}
}

func preparedIdentityKey(t *testing.T, principalID int64, idempotency string) modelidentity.PreparedKey {
	t.Helper()
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	prepared := modelidentity.PreparedKey{
		OrganizationID: identityIntegrationOrganization, ExecutionPrincipalID: principalID,
		PublicKey: publicKey, PublicKeyFingerprint: modelidentity.PublicKeyFingerprint(publicKey),
		SecretRef:      "file://model-execution/integration/" + idempotency,
		IdempotencyKey: idempotency, CreatedByRoleID: "empresa/human",
	}
	prepared.RequestHash, err = modelidentity.KeyRequestHash(prepared)
	if err != nil {
		t.Fatal(err)
	}
	return prepared
}

func openIdentityStore(t *testing.T, ctx context.Context) *platformpostgres.Store {
	t.Helper()
	url := os.Getenv("ORG_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("ORG_TEST_DATABASE_URL is required")
	}
	cfg := config.DatabaseConfig{URL: url, SSLMode: "disable", MaxConns: 20, MinConns: 0, MaxConnLifetime: time.Minute, MaxConnIdleTime: time.Minute, HealthCheckPeriod: time.Second, ConnectTimeout: 5 * time.Second, PingTimeout: 5 * time.Second, StatementTimeout: 30 * time.Second, LockTimeout: 5 * time.Second, AutoMigrate: true, MigrationTimeout: 45 * time.Second, MigrationRetry: time.Second}
	store, err := platformpostgres.Open(ctx, cfg, "model-identity-integration")
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func resetIdentitySchema(t *testing.T, ctx context.Context, store *platformpostgres.Store) {
	t.Helper()
	_, err := store.Pool().Exec(ctx, `TRUNCATE model_provider_outcomes,model_provider_requests,model_execution_identity_assertions,model_execution_identity_challenges,model_execution_identity_keys,model_execution_identity_policy_versions,model_dispatcher_assignment_uses,model_dispatcher_assignments,model_execution_principals,model_egress_evaluations,model_invocation_usage,model_invocation_results,model_dispatch_attempts,model_invocations,model_egress_revision_bindings,model_egress_rules,model_egress_policy_versions,role_model_bindings,model_capability_snapshots,model_profile_versions,model_profiles,model_providers,context_segments,context_snapshots,authorization_uses,authorization_decisions,authorization_requests,staging_events,staging_reviews,staging_promotions,staging_checks,staging_workspace_artifacts,staging_artifacts,staging_workspaces,outbox_events,task_dead_letters,task_events,task_leases,task_attempts,task_evidence,task_requirements,task_dependencies,tasks,organization_reporting_lines,organization_registry_revision_documents,organization_roles,organizational_units,organizations,organization_registry_revisions,audit_events RESTART IDENTITY CASCADE`)
	if err != nil {
		t.Fatal(err)
	}
}

func syncIdentityCanonical(t *testing.T, ctx context.Context, store *platformpostgres.Store) {
	t.Helper()
	repo, err := registry.NewPostgresRepository(store)
	if err != nil {
		t.Fatal(err)
	}
	loader, err := registry.NewLoader(filepath.Join("..", "..", "..", "docs", "canonical"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := registry.NewService(loader, repo, identityIntegrationOrganization, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.SynchronizeCanonical(ctx, true)
	if err != nil || !result.Applied {
		t.Fatalf("sync=%+v err=%v", result, err)
	}
}
