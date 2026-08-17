package runtimeadapter

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/Mireuz13/explorarte-organization/internal/executive"
	"github.com/Mireuz13/explorarte-organization/internal/modeldispatch"
)

// RoleBoundPrincipals is the Executive-facing face of RoleBoundPrincipalResolver.
//
// It exists so the Executive depends on one verb and two strings instead of on
// modeldispatch's principal store, and so the three-way failure surface the
// resolver preserves (unavailable / unusable / resolved) survives the crossing
// instead of being flattened into "no principal" -- the exact collapse that
// once turned a database outage into a missing identity.
type RoleBoundPrincipals struct {
	Resolver RoleBoundPrincipalResolver
}

func (a RoleBoundPrincipals) ResolveRoleBoundPrincipal(ctx context.Context, roleID string) (executive.ExecutionPrincipalRef, error) {
	principal, err := a.Resolver.Resolve(ctx, roleID)
	if err != nil {
		return executive.ExecutionPrincipalRef{}, classifyPrincipalFailure(roleID, err)
	}
	if principal.ID <= 0 || principal.DispatchActorRoleID != roleID {
		return executive.ExecutionPrincipalRef{}, fmt.Errorf("%w: resolver returned principal %d bound to %q for role %q",
			executive.ErrExecutionPrincipalUnusable, principal.ID, principal.DispatchActorRoleID, roleID)
	}
	// Decimal, no padding, no prefix: task_leases.holder_id and the Harness's
	// RunIdentity.ExecutionPrincipalID must be the same string as
	// model_execution_principals.id renders to, or authority compares two
	// spellings of the same identity and denies.
	return executive.ExecutionPrincipalRef{ID: strconv.FormatInt(principal.ID, 10), RoleID: principal.DispatchActorRoleID}, nil
}

// classifyPrincipalFailure keeps "could not ask" apart from "asked and the
// answer is unusable". Only the first is retryable; the second must fail
// closed, because silently re-provisioning an identity somebody revoked is
// precisely the behavior EXEC-PRINCIPAL-001 removed.
func classifyPrincipalFailure(roleID string, err error) error {
	switch {
	case errors.Is(err, modeldispatch.ErrDatabaseUnavailable),
		errors.Is(err, context.Canceled),
		errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("%w: role %q: %w", executive.ErrExecutionPrincipalUnavailable, roleID, err)
	default:
		return fmt.Errorf("%w: role %q: %w", executive.ErrExecutionPrincipalUnusable, roleID, err)
	}
}

var _ executive.RoleBoundPrincipalResolver = RoleBoundPrincipals{}
