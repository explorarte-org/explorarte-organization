package tasksauthority

import (
	"context"
	"errors"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/executionharness"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

type leaseVerifier struct {
	value tasks.ExecutionLeaseContext
	err   error
}

func (f leaseVerifier) VerifyActiveExecutionLease(context.Context, tasks.VerifyExecutionLeaseCommand) (tasks.ExecutionLeaseContext, error) {
	return f.value, f.err
}

type principalReader struct {
	value Principal
	err   error
}

func (f principalReader) ResolveExecutionPrincipal(context.Context, string, string) (Principal, error) {
	return f.value, f.err
}

func authorityRequest() executionharness.AuthorityRequest {
	return executionharness.AuthorityRequest{
		Identity:   executionharness.RunIdentity{RunID: "run-1", OrganizationID: "org-1", TaskID: 11, AttemptID: 22, RoleID: "role-1", ExecutionPrincipalID: "principal-1", CorrelationID: "corr-1"},
		LeaseToken: "lease-token",
	}
}

func TestAdapterRequiresExactLeaseAndActivePrincipalBindings(t *testing.T) {
	lease := tasks.ExecutionLeaseContext{TaskID: 11, AttemptID: 22, OrganizationID: "org-1", AssignedRoleID: "role-1", HolderID: "principal-1"}
	principal := Principal{ID: "principal-1", OrganizationID: "org-1", RoleID: "role-1", Active: true}

	tests := []struct {
		name      string
		lease     tasks.ExecutionLeaseContext
		principal Principal
		leaseErr  error
		wantErr   bool
	}{
		{name: "valid", lease: lease, principal: principal},
		{name: "lease verifier deny", lease: lease, principal: principal, leaseErr: errors.New("expired"), wantErr: true},
		{name: "attempt mismatch", lease: func() tasks.ExecutionLeaseContext { v := lease; v.AttemptID++; return v }(), principal: principal, wantErr: true},
		{name: "cross organization", lease: func() tasks.ExecutionLeaseContext { v := lease; v.OrganizationID = "other"; return v }(), principal: principal, wantErr: true},
		{name: "role mismatch", lease: func() tasks.ExecutionLeaseContext { v := lease; v.AssignedRoleID = "other"; return v }(), principal: principal, wantErr: true},
		{name: "principal inactive", lease: lease, principal: func() Principal { v := principal; v.Active = false; return v }(), wantErr: true},
		{name: "principal role mismatch", lease: lease, principal: func() Principal { v := principal; v.RoleID = "other"; return v }(), wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			adapter, err := New(leaseVerifier{value: tc.lease, err: tc.leaseErr}, principalReader{value: tc.principal})
			if err != nil {
				t.Fatal(err)
			}
			err = adapter.AuthorizeExecution(context.Background(), authorityRequest())
			if (err != nil) != tc.wantErr {
				t.Fatalf("AuthorizeExecution error=%v wantErr=%v", err, tc.wantErr)
			}
		})
	}
}
