// Package accessadapter maps Workflow Runtime's authorization port onto the
// existing organization registry, V1 topology, and execution-principal
// identity. It performs no persistence and must run before task creation.
package accessadapter

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/agentmessaging"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
	"github.com/Mireuz13/explorarte-organization/internal/workflowruntime"
)

const ceoRoleID = "empresa/ceo"

type PrincipalIdentity struct {
	ID             string
	OrganizationID string
	RoleID         string
	Active         bool
}

// PrincipalReader is intentionally a Workflow Runtime-owned read contract:
// authorization may inspect an existing identity but can never register,
// disable, assign, or invoke through it. A composition-root adapter may project
// an existing identity store into this type without coupling the runtime to a
// model or provider domain.
type PrincipalReader interface {
	GetPrincipal(context.Context, string) (PrincipalIdentity, error)
}

type Adapter struct {
	registry   registry.Reader
	principals PrincipalReader
}

func New(reader registry.Reader, principals PrincipalReader) (*Adapter, error) {
	if reader == nil || principals == nil {
		return nil, errors.New("workflow access adapter requires registry and principal readers")
	}
	return &Adapter{registry: reader, principals: principals}, nil
}

func (a *Adapter) AuthorizeInitiation(ctx context.Context, actor workflowruntime.Actor, work workflowruntime.WorkRequest) error {
	if work.OrganizationID != actor.OrganizationID || work.RequestedByRoleID != actor.RoleID {
		return denied("work requester is not bound to actor")
	}
	if err := a.authorizePrincipal(ctx, actor); err != nil {
		return err
	}
	actorRole, err := a.activeRole(ctx, actor.OrganizationID, actor.RoleID, true)
	if err != nil {
		return err
	}
	if _, err = a.activeRole(ctx, actor.OrganizationID, work.AssignedRoleID, true); err != nil {
		return err
	}
	if actorRole.ID == work.AssignedRoleID {
		return nil
	}
	if err = agentmessaging.NewTopologyValidator(a.registry, actor.OrganizationID).ValidateEdge(ctx, actor.RoleID, work.AssignedRoleID); err != nil {
		return denied("assignment edge %s -> %s: %v", actor.RoleID, work.AssignedRoleID, err)
	}
	return nil
}

func (a *Adapter) AuthorizeTaskAccess(ctx context.Context, actor workflowruntime.Actor, snapshot workflowruntime.Snapshot, access workflowruntime.TaskAccess) error {
	if snapshot.OrganizationID != actor.OrganizationID {
		return denied("task organization does not match actor")
	}
	if err := a.authorizePrincipal(ctx, actor); err != nil {
		return err
	}
	actorRole, err := a.activeRole(ctx, actor.OrganizationID, actor.RoleID, access == workflowruntime.TaskAccessMutate)
	if err != nil {
		return err
	}
	targetRole, err := a.activeRole(ctx, snapshot.OrganizationID, snapshot.AssignedRoleID, true)
	if err != nil {
		return err
	}

	switch access {
	case workflowruntime.TaskAccessMutate:
		if actor.RoleID != snapshot.AssignedRoleID {
			return denied("only the assigned role may mutate task %d", snapshot.TaskID)
		}
		return nil
	case workflowruntime.TaskAccessObserve:
		// Explicit visibility contract: an assignee may observe its own work;
		// CEO may observe organization descendants; a canonical department
		// leader may observe work assigned inside their own unit. Requester
		// strings on legacy tasks do not grant read authority. Other roles do
		// not gain visibility merely by knowing a task ID.
		if actor.RoleID == snapshot.AssignedRoleID || actor.RoleID == ceoRoleID {
			return nil
		}
		if actorRole.CanonicalLeader && actorRole.UnitID != "" && actorRole.UnitID == targetRole.UnitID {
			return nil
		}
		return denied("role %s may not observe task %d assigned to %s", actor.RoleID, snapshot.TaskID, snapshot.AssignedRoleID)
	default:
		return denied("unknown task access %q", access)
	}
}

func (a *Adapter) authorizePrincipal(ctx context.Context, actor workflowruntime.Actor) error {
	id := strings.TrimSpace(actor.ExecutionPrincipalID)
	if id == "" {
		return denied("execution principal ID is invalid")
	}
	principal, err := a.principals.GetPrincipal(ctx, id)
	if err != nil {
		return denied("execution principal %s is unavailable: %v", id, err)
	}
	if principal.ID != id || principal.OrganizationID != actor.OrganizationID || principal.RoleID != actor.RoleID || !principal.Active {
		return denied("execution principal is inactive or not bound to actor organization and role")
	}
	return nil
}

func (a *Adapter) activeRole(ctx context.Context, organizationID, roleID string, requireExecutable bool) (registry.Role, error) {
	role, err := a.registry.GetRole(ctx, organizationID, roleID)
	if err != nil {
		return registry.Role{}, denied("role %s is unavailable: %v", roleID, err)
	}
	if role.OrganizationID != "" && role.OrganizationID != organizationID {
		return registry.Role{}, denied("role %s belongs to another organization", roleID)
	}
	if !role.Enabled || role.RetiredAt != nil || (requireExecutable && !role.Executable) {
		return registry.Role{}, denied("role %s is disabled, retired, or not executable", roleID)
	}
	return role, nil
}

func denied(format string, values ...any) error {
	return fmt.Errorf("%w: %s", workflowruntime.ErrAuthorizationDenied, fmt.Sprintf(format, values...))
}

var _ workflowruntime.AuthorizationPort = (*Adapter)(nil)
