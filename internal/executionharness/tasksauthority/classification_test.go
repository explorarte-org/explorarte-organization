package tasksauthority

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/executionharness"
	"github.com/Mireuz13/explorarte-organization/internal/modeldispatch"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

// The adapter must answer two different questions with two different errors:
// "authority refused" and "authority could not be consulted". Collapsing them
// is what turned a database outage into a fabricated revocation.
func TestAuthorityFailureClassification(t *testing.T) {
	lease := tasks.ExecutionLeaseContext{TaskID: 11, AttemptID: 22, OrganizationID: "org-1", AssignedRoleID: "role-1", HolderID: "principal-1"}
	active := Principal{ID: "principal-1", OrganizationID: "org-1", RoleID: "role-1", Active: true}

	tests := []struct {
		name            string
		leaseErr        error
		principal       Principal
		principalErr    error
		wantUnavailable bool
		wantCause       error
	}{
		{
			name:            "lease store unreachable",
			leaseErr:        fmt.Errorf("%w: PostgreSQL 08006", tasks.ErrDatabaseUnavailable),
			principal:       active,
			wantUnavailable: true,
			wantCause:       tasks.ErrDatabaseUnavailable,
		},
		{
			name:            "principal store unreachable",
			principal:       active,
			principalErr:    fmt.Errorf("%w: connection reset", modeldispatch.ErrDatabaseUnavailable),
			wantUnavailable: true,
			wantCause:       modeldispatch.ErrDatabaseUnavailable,
		},
		{
			name:            "principal lookup deadline exceeded",
			principal:       active,
			principalErr:    fmt.Errorf("resolve principal: %w", context.DeadlineExceeded),
			wantUnavailable: true,
			wantCause:       context.DeadlineExceeded,
		},
		{
			name:            "lease token mismatch is a real refusal",
			leaseErr:        tasks.ErrLeaseMismatch,
			principal:       active,
			wantUnavailable: false,
			wantCause:       tasks.ErrLeaseMismatch,
		},
		{
			name:            "expired lease is a real refusal",
			leaseErr:        tasks.ErrLeaseExpired,
			principal:       active,
			wantUnavailable: false,
			wantCause:       tasks.ErrLeaseExpired,
		},
		{
			name:            "missing principal is a real refusal, not an outage",
			principal:       active,
			principalErr:    modeldispatch.ErrNotFound,
			wantUnavailable: false,
			wantCause:       modeldispatch.ErrNotFound,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			adapter, err := New(leaseVerifier{value: lease, err: tc.leaseErr}, principalReader{value: tc.principal, err: tc.principalErr})
			if err != nil {
				t.Fatal(err)
			}
			got := adapter.AuthorizeExecution(context.Background(), authorityRequest())
			if got == nil {
				t.Fatal("authority accepted a failing dependency")
			}
			if errors.Is(got, executionharness.ErrAuthorityUnavailable) != tc.wantUnavailable {
				t.Fatalf("unavailable=%v want %v: %v", !tc.wantUnavailable, tc.wantUnavailable, got)
			}
			if errors.Is(got, executionharness.ErrAuthorityDenied) == tc.wantUnavailable {
				t.Fatalf("denied classification is inverted: %v", got)
			}
			if !errors.Is(got, tc.wantCause) {
				t.Fatalf("cause %v was lost from %v", tc.wantCause, got)
			}
		})
	}
}

// A principal the store answered for, but that is disabled, is a decision.
// It must stay a denial no matter how the transport behaved.
func TestDisabledPrincipalIsDeniedNotUnavailable(t *testing.T) {
	lease := tasks.ExecutionLeaseContext{TaskID: 11, AttemptID: 22, OrganizationID: "org-1", AssignedRoleID: "role-1", HolderID: "principal-1"}
	disabled := Principal{ID: "principal-1", OrganizationID: "org-1", RoleID: "role-1", Active: false}

	adapter, err := New(leaseVerifier{value: lease}, principalReader{value: disabled})
	if err != nil {
		t.Fatal(err)
	}
	got := adapter.AuthorizeExecution(context.Background(), authorityRequest())
	if !errors.Is(got, executionharness.ErrAuthorityDenied) {
		t.Fatalf("disabled principal was not denied: %v", got)
	}
	if errors.Is(got, executionharness.ErrAuthorityUnavailable) {
		t.Fatalf("disabled principal was reported as an outage: %v", got)
	}
}
