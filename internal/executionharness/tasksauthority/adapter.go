// Package tasksauthority adapts the existing durable task lease contract to
// the execution Harness without creating a second task or principal identity.
package tasksauthority

import (
	"context"
	"errors"
	"fmt"

	"github.com/Mireuz13/explorarte-organization/internal/executionharness"
	"github.com/Mireuz13/explorarte-organization/internal/modeldispatch"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

// authorityFailure classifies a failure from a dependency of authority.
//
// Only causes that mean "authority could not be consulted" become
// ErrAuthorityUnavailable. Definite answers stay denials, including not-found:
// a principal or lease that does not exist is a real refusal, not an outage.
// Both branches wrap with %w so errors.Is/errors.As still reach the cause.
func authorityFailure(stage string, err error) error {
	if unavailableCause(err) {
		return fmt.Errorf("%w: %s: %w", executionharness.ErrAuthorityUnavailable, stage, err)
	}
	return fmt.Errorf("%w: %s: %w", executionharness.ErrAuthorityDenied, stage, err)
}

func unavailableCause(err error) bool {
	switch {
	case errors.Is(err, tasks.ErrDatabaseUnavailable),
		errors.Is(err, modeldispatch.ErrDatabaseUnavailable),
		errors.Is(err, executionharness.ErrAuthorityUnavailable),
		errors.Is(err, context.Canceled),
		errors.Is(err, context.DeadlineExceeded):
		return true
	}
	return false
}

type Principal struct {
	ID             string
	OrganizationID string
	RoleID         string
	Active         bool
}

type PrincipalReader interface {
	ResolveExecutionPrincipal(context.Context, string, string) (Principal, error)
}

type Adapter struct {
	leases     tasks.ExecutionLeaseVerifier
	principals PrincipalReader
}

func New(leases tasks.ExecutionLeaseVerifier, principals PrincipalReader) (*Adapter, error) {
	if leases == nil || principals == nil {
		return nil, fmt.Errorf("execution authority dependencies are incomplete")
	}
	return &Adapter{leases: leases, principals: principals}, nil
}

func (a *Adapter) AuthorizeExecution(ctx context.Context, request executionharness.AuthorityRequest) error {
	i := request.Identity
	lease, err := a.leases.VerifyActiveExecutionLease(ctx, tasks.VerifyExecutionLeaseCommand{
		TaskID: i.TaskID, AttemptID: i.AttemptID, HolderID: i.ExecutionPrincipalID, LeaseToken: request.LeaseToken,
	})
	if err != nil {
		return authorityFailure("lease", err)
	}
	if lease.TaskID != i.TaskID || lease.AttemptID != i.AttemptID || lease.OrganizationID != i.OrganizationID ||
		lease.AssignedRoleID != i.RoleID || lease.HolderID != i.ExecutionPrincipalID {
		return fmt.Errorf("%w: task/attempt/organization/role/principal binding mismatch", executionharness.ErrAuthorityDenied)
	}
	principal, err := a.principals.ResolveExecutionPrincipal(ctx, i.OrganizationID, i.ExecutionPrincipalID)
	if err != nil {
		return authorityFailure("principal", err)
	}
	if !principal.Active || principal.ID != i.ExecutionPrincipalID || principal.OrganizationID != i.OrganizationID || principal.RoleID != i.RoleID {
		return fmt.Errorf("%w: principal inactive or binding mismatch", executionharness.ErrAuthorityDenied)
	}
	return nil
}
