package runtimeadapter

import (
	"context"
	"errors"
	"fmt"

	"github.com/Mireuz13/explorarte-organization/internal/modeldispatch"
)

var (
	// ErrRoleBoundPrincipalNotActive means an identity exists for the role but
	// is not usable. It is deliberately distinct from "none exists": the
	// remedy is explicit rotation, never silent re-creation.
	ErrRoleBoundPrincipalNotActive = errors.New("role-bound execution principal is not active")
	// ErrRoleBoundPrincipalMismatch means the store returned a principal that
	// is not the role-bound identity that was asked for.
	ErrRoleBoundPrincipalMismatch = errors.New("resolved principal is not the expected role-bound identity")
)

// roleBoundProvisionerRoleID attributes auto-provisioned principals in
// registered_by_role_id/audit_events. It is not a role FK (that column has
// none), only an audit label identifying the runtime component that
// provisioned the row.
const roleBoundProvisionerRoleID = "executive/orchestrator"

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
// Migration 000048's unique index is the database-enforced half of that
// guarantee. Note its predicate: it is scoped to
// (organization_id, dispatch_actor_role_id) WHERE status='active' AND
// principal_key LIKE 'role-bound/%'. The role-bound restriction is
// deliberate -- technical process principals may legitimately share a role,
// and conflating the two identity semantics is the defect EXEC-PRINCIPAL-001
// was about.
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
		if validateErr := r.validate(principal, roleID, modeldispatch.RoleBoundPrincipalKeyPrefix+roleID); validateErr != nil {
			return modeldispatch.ExecutionPrincipal{}, validateErr
		}
		return principal, nil
	}
	if !errors.Is(err, modeldispatch.ErrNotFound) {
		// A store that could not answer is not a store that answered "none".
		// The typed cause -- modeldispatch.ErrDatabaseUnavailable, a context
		// deadline, a cancellation -- has to stay reachable with errors.Is so
		// the caller can tell an availability failure from a definite absence
		// of identity. Flattening it here would hand the Executive the same
		// error for "this role has no principal" and "PostgreSQL is down".
		return modeldispatch.ExecutionPrincipal{}, fmt.Errorf("resolve active role-bound principal for %q: %w", roleID, err)
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
	if err = r.validate(result.Principal, roleID, principalKey); err != nil {
		return modeldispatch.ExecutionPrincipal{}, err
	}
	return result.Principal, nil
}

// validate refuses to return anything that is not the active identity this
// resolver promises.
//
// The case that matters is a deliberately disabled principal. Disabling it
// makes ResolveActiveForRole report ErrNotFound, which sends this resolver
// down the provisioning path -- and RegisterPrincipal, keyed on the same
// deterministic idempotency key, hands the very same disabled row back with
// Reused=true. Returning it would look like success and then be denied by
// execution authority on every attempt, burning a lease each time.
//
// Re-creating an identity somebody just revoked is not this resolver's call to
// make. It fails closed; deliberate rotation is an explicit operation with its
// own identity, not a side effect of lazy resolution.
func (r RoleBoundPrincipalResolver) validate(principal modeldispatch.ExecutionPrincipal, roleID, principalKey string) error {
	switch {
	case principal.Status != modeldispatch.PrincipalActive:
		return fmt.Errorf("%w: principal %d for role %q is %q", ErrRoleBoundPrincipalNotActive, principal.ID, roleID, principal.Status)
	case principal.OrganizationID != r.OrganizationID:
		return fmt.Errorf("%w: principal %d belongs to organization %q", ErrRoleBoundPrincipalMismatch, principal.ID, principal.OrganizationID)
	case principal.DispatchActorRoleID != roleID:
		return fmt.Errorf("%w: principal %d is bound to role %q, not %q", ErrRoleBoundPrincipalMismatch, principal.ID, principal.DispatchActorRoleID, roleID)
	case principal.PrincipalKey != principalKey:
		return fmt.Errorf("%w: principal %d has key %q, not the role-bound key %q", ErrRoleBoundPrincipalMismatch, principal.ID, principal.PrincipalKey, principalKey)
	}
	return nil
}
