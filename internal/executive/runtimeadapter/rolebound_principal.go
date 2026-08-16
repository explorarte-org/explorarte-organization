package runtimeadapter

import (
	"context"
	"errors"
	"fmt"

	"github.com/Mireuz13/explorarte-organization/internal/modeldispatch"
)

// RoleBoundPrincipalStore is the narrow slice of the dispatch principal store
// this resolver needs. It is deliberately not the whole store: nothing here
// disables, lists, or looks up principals by key.
type RoleBoundPrincipalStore interface {
	ResolveActiveForRole(context.Context, string, string) (modeldispatch.ExecutionPrincipal, error)
	RegisterPrincipal(context.Context, modeldispatch.PreparedRegisterPrincipal) (modeldispatch.RegisterPrincipalResult, error)
}

// RoleBoundPrincipalResolver returns the single active execution principal
// bound to a role, registering one on first use if none exists yet.
//
// It is a mechanism, not a policy. It knows nothing about messages,
// delegations, tasks, leases, or the Execution Harness -- every consumer that
// needs a role-bound identity gets the same rows through the same derivation,
// so two subsystems can never end up disagreeing about which principal a role
// has.
//
// Registration is idempotent by construction: the idempotency key and the
// principal key are both deterministic functions of (organizationID, roleID),
// so a concurrent caller racing this one either creates the row or observes
// the same one just created, never a duplicate -- the same idempotency
// contract every other RegisterPrincipal caller in this codebase relies on.
// Migration 000048's unique index on (organization_id, dispatch_actor_role_id)
// where status='active' is the database-enforced half of that guarantee.
//
// roleID must be an already-persisted, already-registry-validated
// AssignedRoleID, never caller/model/task-text input: this resolver does not
// re-validate role executability, because task creation already established it.
type RoleBoundPrincipalResolver struct {
	Principals     RoleBoundPrincipalStore
	OrganizationID string
}

func NewRoleBoundPrincipalResolver(principals RoleBoundPrincipalStore, organizationID string) (RoleBoundPrincipalResolver, error) {
	if principals == nil {
		return RoleBoundPrincipalResolver{}, errors.New("role-bound principal resolver requires a principal store")
	}
	if organizationID == "" {
		return RoleBoundPrincipalResolver{}, errors.New("role-bound principal resolver requires an organization")
	}
	return RoleBoundPrincipalResolver{Principals: principals, OrganizationID: organizationID}, nil
}

func (r RoleBoundPrincipalResolver) Resolve(ctx context.Context, roleID string) (modeldispatch.ExecutionPrincipal, error) {
	principal, err := r.Principals.ResolveActiveForRole(ctx, r.OrganizationID, roleID)
	if err == nil {
		return principal, nil
	}
	if !errors.Is(err, modeldispatch.ErrNotFound) {
		return modeldispatch.ExecutionPrincipal{}, fmt.Errorf("%w: %v", ErrNoActivePrincipal, err)
	}

	principalKey := modeldispatch.RoleBoundPrincipalKeyPrefix + roleID
	command := modeldispatch.RegisterPrincipalCommand{
		OrganizationID: r.OrganizationID, PrincipalKey: principalKey,
		DispatchActorRoleID: roleID, PrincipalKind: modeldispatch.PrincipalLocalProcess,
		IdempotencyKey: "role-bound-principal:" + r.OrganizationID + ":" + roleID,
	}
	requestHash, hashErr := modeldispatch.PrincipalRequestHash(command.OrganizationID, command.PrincipalKey, command.DispatchActorRoleID, command.PrincipalKind, roleBoundProvisionerRoleID)
	if hashErr != nil {
		return modeldispatch.ExecutionPrincipal{}, fmt.Errorf("compute role-bound principal request hash: %w", hashErr)
	}
	result, registerErr := r.Principals.RegisterPrincipal(ctx, modeldispatch.PreparedRegisterPrincipal{
		Command: command, RequestHash: requestHash, RegisteredByRoleID: roleBoundProvisionerRoleID,
	})
	if registerErr != nil {
		return modeldispatch.ExecutionPrincipal{}, fmt.Errorf("provision role-bound execution principal for %q: %w", roleID, registerErr)
	}
	return result.Principal, nil
}
