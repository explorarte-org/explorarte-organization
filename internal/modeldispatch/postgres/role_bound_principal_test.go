//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/modeldispatch"
	dispatchpostgres "github.com/Mireuz13/explorarte-organization/internal/modeldispatch/postgres"
	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
)

// setupRoleBoundTest opens a real Postgres 17, migrates to tip, resets the
// schema, and syncs the canonical registry so dispatch_actor_role_id values
// like "empresa/ceo" satisfy model_execution_principals' FK to
// organization_roles.
func setupRoleBoundTest(t *testing.T) (context.Context, *platformpostgres.Store, *dispatchpostgres.Store) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	t.Cleanup(cancel)
	platform := openDispatchStore(t, ctx)
	t.Cleanup(platform.Close)
	runner, err := platformmigrations.New(platform.Pool(), rootmigrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = runner.Up(ctx); err != nil {
		t.Fatal(err)
	}
	resetDispatchSchema(t, ctx, platform)
	syncDispatchCanonical(t, ctx, platform)
	store, err := dispatchpostgres.New(platform)
	if err != nil {
		t.Fatal(err)
	}
	return ctx, platform, store
}

func registerRoleBound(t *testing.T, ctx context.Context, store *dispatchpostgres.Store, org, role, suffix string) modeldispatch.ExecutionPrincipal {
	t.Helper()
	command := modeldispatch.RegisterPrincipalCommand{
		OrganizationID: org, PrincipalKey: modeldispatch.RoleBoundPrincipalKeyPrefix + role + "-" + suffix,
		DispatchActorRoleID: role, PrincipalKind: modeldispatch.PrincipalLocalProcess,
		IdempotencyKey: "role-bound-test:" + org + ":" + role + ":" + suffix,
	}
	hash, err := modeldispatch.PrincipalRequestHash(command.OrganizationID, command.PrincipalKey, command.DispatchActorRoleID, command.PrincipalKind, "empresa/human")
	if err != nil {
		t.Fatal(err)
	}
	result, err := store.RegisterPrincipal(ctx, modeldispatch.PreparedRegisterPrincipal{Command: command, RequestHash: hash, RegisteredByRoleID: "empresa/human"})
	if err != nil {
		t.Fatal(err)
	}
	return result.Principal
}

func registerTechnical(t *testing.T, ctx context.Context, store *dispatchpostgres.Store, org, role, suffix string) modeldispatch.ExecutionPrincipal {
	t.Helper()
	command := modeldispatch.RegisterPrincipalCommand{
		OrganizationID: org, PrincipalKey: "technical/" + role + "-" + suffix,
		DispatchActorRoleID: role, PrincipalKind: modeldispatch.PrincipalLocalProcess,
		IdempotencyKey: "technical-test:" + org + ":" + role + ":" + suffix,
	}
	hash, err := modeldispatch.PrincipalRequestHash(command.OrganizationID, command.PrincipalKey, command.DispatchActorRoleID, command.PrincipalKind, "empresa/human")
	if err != nil {
		t.Fatal(err)
	}
	result, err := store.RegisterPrincipal(ctx, modeldispatch.PreparedRegisterPrincipal{Command: command, RequestHash: hash, RegisteredByRoleID: "empresa/human"})
	if err != nil {
		t.Fatal(err)
	}
	return result.Principal
}

// seedSecondOrganization inserts a minimal, real, FK-satisfying second
// organization (its own registry revision, one organizational unit, and
// the empresa/ceo role) so cross-org tests exercise a genuinely different
// organization rather than a synthetic non-existent one that would just
// be rejected by the organization_id foreign key before the property under
// test is even reached.
func seedSecondOrganization(t *testing.T, ctx context.Context, store *platformpostgres.Store, orgID string) {
	t.Helper()
	var revisionID int64
	if err := store.Pool().QueryRow(ctx, `
INSERT INTO organization_registry_revisions(canonical_hash,status,schema_versions,document_hashes,counts,diff,applied_at)
VALUES ($1,'applied','{}','{}','{}','{}',NOW())
RETURNING id`, hashFor(orgID)).Scan(&revisionID); err != nil {
		t.Fatalf("seed registry revision for %s: %v", orgID, err)
	}
	tx, err := store.Pool().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SET CONSTRAINTS ALL DEFERRED`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO organizations(id,display_name,owner_role_id,ceo_role_id,runtime_target,state_store_target,cells_are_external,schema_version,document_status,current_revision_id,created_at,updated_at)
VALUES ($1,$1,'empresa/human','empresa/ceo','single_modular_go_binary','postgresql',true,'0.1.0','branch_0_candidate',$2,NOW(),NOW())`, orgID, revisionID); err != nil {
		t.Fatalf("seed organization %s: %v", orgID, err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO organizational_units(organization_id,id,display_name,kind,operational,leaderless,leader_role_id,canonical_status,source_revision_id,created_at,updated_at)
VALUES ($1,'empresa','Empresa','executive_layer',false,true,NULL,'branch_0_candidate',$2,NOW(),NOW())`, orgID, revisionID); err != nil {
		t.Fatalf("seed unit for %s: %v", orgID, err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO organization_roles(organization_id,id,unit_id,role_slug,display_name,runtime_kind,authority_class,canonical_leader,legacy_frontmatter_leader,source_status,enabled,executable,source_hash,source_revision_id,created_at,updated_at)
VALUES
 ($1,'empresa/human','empresa','human','Owner','executive','executive_leadership',false,false,'imported_source',true,true,$3,$2,NOW(),NOW()),
 ($1,'empresa/ceo','empresa','ceo','CEO','executive','executive_leadership',false,false,'imported_source',true,true,$3,$2,NOW(),NOW())`, orgID, revisionID, hashFor(orgID)); err != nil {
		t.Fatalf("seed roles for %s: %v", orgID, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit seed for %s: %v", orgID, err)
	}
}

func hashFor(seed string) string {
	const hex = "0123456789abcdef"
	out := make([]byte, 64)
	for i := range out {
		out[i] = hex[(int(seed[i%len(seed)])+i)%16]
	}
	return string(out)
}

// A. role-bound principal only -> resolver returns it correctly.
func TestResolveActiveForRoleReturnsTheRoleBoundPrincipal(t *testing.T) {
	ctx, _, store := setupRoleBoundTest(t)
	roleBound := registerRoleBound(t, ctx, store, dispatchIntegrationOrganization, "empresa/ceo", "a")
	resolved, err := store.ResolveActiveForRole(ctx, dispatchIntegrationOrganization, "empresa/ceo")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if resolved.ID != roleBound.ID || resolved.PrincipalKey != roleBound.PrincipalKey {
		t.Fatalf("resolved=%+v want=%+v", resolved, roleBound)
	}
}

// B. role-bound + technical principal sharing a role -> resolver returns the
// role-bound one, never the technical one, regardless of insertion order.
func TestResolveActiveForRoleNeverReturnsTechnicalPrincipal(t *testing.T) {
	ctx, _, store := setupRoleBoundTest(t)
	registerTechnical(t, ctx, store, dispatchIntegrationOrganization, "empresa/ceo", "before")
	roleBound := registerRoleBound(t, ctx, store, dispatchIntegrationOrganization, "empresa/ceo", "b")
	registerTechnical(t, ctx, store, dispatchIntegrationOrganization, "empresa/ceo", "after")

	resolved, err := store.ResolveActiveForRole(ctx, dispatchIntegrationOrganization, "empresa/ceo")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if resolved.ID != roleBound.ID {
		t.Fatalf("resolved technical or wrong principal: got id=%d key=%q, want role-bound id=%d", resolved.ID, resolved.PrincipalKey, roleBound.ID)
	}
}

// C. technical principal only, no role-bound registered -> ResolveActiveForRole
// does not accept it as a substitute; must report not-found.
func TestResolveActiveForRoleRejectsTechnicalOnlyAsNotFound(t *testing.T) {
	ctx, _, store := setupRoleBoundTest(t)
	registerTechnical(t, ctx, store, dispatchIntegrationOrganization, "empresa/ceo", "only")

	_, err := store.ResolveActiveForRole(ctx, dispatchIntegrationOrganization, "empresa/ceo")
	if !errors.Is(err, modeldispatch.ErrNotFound) {
		t.Fatalf("err=%v want ErrNotFound (a technical principal must never satisfy a role-bound lookup)", err)
	}
}

// D. role-bound principal registered for a different organization must not
// be visible when resolving for this organization.
func TestResolveActiveForRoleDoesNotLeakAcrossOrganizations(t *testing.T) {
	ctx, platform, store := setupRoleBoundTest(t)
	registerRoleBound(t, ctx, store, dispatchIntegrationOrganization, "empresa/ceo", "explorarte")

	otherOrg := "otra-empresa-rb-test"
	seedSecondOrganization(t, ctx, platform, otherOrg)
	otherRoleBound := registerRoleBound(t, ctx, store, otherOrg, "empresa/ceo", "otra")

	resolved, err := store.ResolveActiveForRole(ctx, dispatchIntegrationOrganization, "empresa/ceo")
	if err != nil {
		t.Fatalf("resolve for %s: %v", dispatchIntegrationOrganization, err)
	}
	if resolved.OrganizationID != dispatchIntegrationOrganization {
		t.Fatalf("resolved principal from wrong org: %+v", resolved)
	}
	if resolved.ID == otherRoleBound.ID {
		t.Fatalf("cross-org leak: resolved the other organization's principal")
	}
}

// E. disabled role-bound principal is not active; resolver must report
// not-found, not silently return the disabled row.
func TestResolveActiveForRoleTreatsDisabledAsNotFound(t *testing.T) {
	ctx, _, store := setupRoleBoundTest(t)
	roleBound := registerRoleBound(t, ctx, store, dispatchIntegrationOrganization, "empresa/ceo", "e")
	if _, err := store.DisablePrincipal(ctx, roleBound.ID, "empresa/human", "test_disable"); err != nil {
		t.Fatalf("disable: %v", err)
	}

	_, err := store.ResolveActiveForRole(ctx, dispatchIntegrationOrganization, "empresa/ceo")
	if !errors.Is(err, modeldispatch.ErrNotFound) {
		t.Fatalf("err=%v want ErrNotFound after disabling the only role-bound principal", err)
	}
}

// F. concurrent first-use provisioning for the same organization+role
// converges on exactly one active role-bound principal, never a
// duplicate/conflicting one, via RegisterPrincipal's idempotent
// ON CONFLICT(organization_id,idempotency_key) DO NOTHING + reuse path.
func TestConcurrentRoleBoundProvisioningConvergesOnOne(t *testing.T) {
	ctx, _, store := setupRoleBoundTest(t)
	const concurrency = 8
	var wg sync.WaitGroup
	results := make([]modeldispatch.ExecutionPrincipal, concurrency)
	errs := make([]error, concurrency)
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			command := modeldispatch.RegisterPrincipalCommand{
				OrganizationID: dispatchIntegrationOrganization, PrincipalKey: modeldispatch.RoleBoundPrincipalKeyPrefix + "empresa/ceo",
				DispatchActorRoleID: "empresa/ceo", PrincipalKind: modeldispatch.PrincipalLocalProcess,
				IdempotencyKey: "role-bound-concurrency:" + dispatchIntegrationOrganization + ":empresa/ceo",
			}
			hash, hashErr := modeldispatch.PrincipalRequestHash(command.OrganizationID, command.PrincipalKey, command.DispatchActorRoleID, command.PrincipalKind, "executive/orchestrator")
			if hashErr != nil {
				errs[i] = hashErr
				return
			}
			result, registerErr := store.RegisterPrincipal(ctx, modeldispatch.PreparedRegisterPrincipal{Command: command, RequestHash: hash, RegisteredByRoleID: "executive/orchestrator"})
			results[i] = result.Principal
			errs[i] = registerErr
		}(i)
	}
	wg.Wait()

	firstID := int64(0)
	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent register %d: %v", i, err)
		}
		if firstID == 0 {
			firstID = results[i].ID
		} else if results[i].ID != firstID {
			t.Fatalf("concurrent registrations converged on different principal IDs: %d vs %d", firstID, results[i].ID)
		}
	}

	resolved, err := store.ResolveActiveForRole(ctx, dispatchIntegrationOrganization, "empresa/ceo")
	if err != nil {
		t.Fatalf("resolve after concurrent provisioning: %v", err)
	}
	if resolved.ID != firstID {
		t.Fatalf("resolved id=%d want %d", resolved.ID, firstID)
	}
}
