package tasksauthority

import (
	"context"
	"fmt"
	"strconv"

	"github.com/Mireuz13/explorarte-organization/internal/modeldispatch"
)

// CanonicalPrincipalReader adapts the durable model-dispatch principal store
// to the Harness authority port. The Harness identity carries the principal
// reference as a string because it is part of a provider-independent run
// contract; the canonical store remains the owner of the numeric identity and
// active/disabled state.
type CanonicalPrincipalReader struct {
	principals interface {
		GetPrincipal(context.Context, int64) (modeldispatch.ExecutionPrincipal, error)
	}
}

func NewCanonicalPrincipalReader(principals interface {
	GetPrincipal(context.Context, int64) (modeldispatch.ExecutionPrincipal, error)
}) (*CanonicalPrincipalReader, error) {
	if principals == nil {
		return nil, fmt.Errorf("canonical execution principal reader requires a principal store")
	}
	return &CanonicalPrincipalReader{principals: principals}, nil
}

func (r *CanonicalPrincipalReader) ResolveExecutionPrincipal(ctx context.Context, organizationID, principalID string) (Principal, error) {
	id, err := strconv.ParseInt(principalID, 10, 64)
	if err != nil || id <= 0 {
		return Principal{}, fmt.Errorf("invalid canonical execution principal ID")
	}
	value, err := r.principals.GetPrincipal(ctx, id)
	if err != nil {
		return Principal{}, err
	}
	return Principal{
		ID:             strconv.FormatInt(value.ID, 10),
		OrganizationID: value.OrganizationID,
		RoleID:         value.DispatchActorRoleID,
		Active:         value.Status == modeldispatch.PrincipalActive,
	}, nil
}

var _ PrincipalReader = (*CanonicalPrincipalReader)(nil)
