// Package tasksauthority adapts the existing durable task lease contract to
// the execution Harness without creating a second task or principal identity.
package tasksauthority

import (
	"context"
	"fmt"

	"github.com/Mireuz13/explorarte-organization/internal/executionharness"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

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
		return fmt.Errorf("%w: lease: %v", executionharness.ErrAuthorityDenied, err)
	}
	if lease.TaskID != i.TaskID || lease.AttemptID != i.AttemptID || lease.OrganizationID != i.OrganizationID ||
		lease.AssignedRoleID != i.RoleID || lease.HolderID != i.ExecutionPrincipalID {
		return fmt.Errorf("%w: task/attempt/organization/role/principal binding mismatch", executionharness.ErrAuthorityDenied)
	}
	principal, err := a.principals.ResolveExecutionPrincipal(ctx, i.OrganizationID, i.ExecutionPrincipalID)
	if err != nil {
		return fmt.Errorf("%w: principal: %v", executionharness.ErrAuthorityDenied, err)
	}
	if !principal.Active || principal.ID != i.ExecutionPrincipalID || principal.OrganizationID != i.OrganizationID || principal.RoleID != i.RoleID {
		return fmt.Errorf("%w: principal inactive or binding mismatch", executionharness.ErrAuthorityDenied)
	}
	return nil
}
