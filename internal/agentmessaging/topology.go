// Package agentmessaging includes topology validation to enforce strict communication patterns.
package agentmessaging

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
)

// TopologyValidator validates message send requests against the canonical organization topology.
// V1 Enforces only these edges:
//   - owner -> any role
//   - ceo -> department leaders, ceo, owner
//   - department leader -> own workers, ceo
type TopologyValidator struct {
	reader registry.Reader
	orgID  string
}

// NewTopologyValidator creates a new topology validator for the given organization.
func NewTopologyValidator(reader registry.Reader, orgID string) *TopologyValidator {
	return &TopologyValidator{reader: reader, orgID: orgID}
}

// ValidateEdge checks if an edge from senderRole to recipientRole is permitted by V1 topology.
func (v *TopologyValidator) ValidateEdge(ctx context.Context, senderRole, recipientRole string) error {
	if strings.TrimSpace(senderRole) == "" || strings.TrimSpace(recipientRole) == "" {
		return fmt.Errorf("sender and recipient roles required")
	}

	ownerRole := "empresa/human"
	ceoRole := "empresa/ceo"

	// Owner can send to anyone
	if senderRole == ownerRole {
		return nil // Owner has universal authority
	}

	// Load organization to get its structure
	org, err := v.reader.GetOrganization(ctx, v.orgID)
	if err != nil {
		return fmt.Errorf("failed to load organization: %w", err)
	}

	// Get current revision for registry lookups
	revision, err := v.reader.GetCurrentRevision(ctx, v.orgID)
	if err != nil {
		return fmt.Errorf("failed to get revision: %w", err)
	}

	// Check CEO edges
	if senderRole == ceoRole {
		// CEO can send to department leaders
		if v.isDepartmentLeader(ctx, senderRole, recipientRole, revision) {
			return nil
		}
		// CEO can send to owner
		if recipientRole == ownerRole {
			return nil
		}
		// CEO can send to self
		if recipientRole == ceoRole {
			return nil
		}
		// Default deny for CEO
		return ErrTopologyViolation
	}

	// Check department leader edges
	isDeptLeader, deptID := v.isDepartmentLeader(ctx, senderRole, "", revision)
	if isDeptLeader {
		// Department leader can send to their own workers
		if v.isWorkerInDepartment(ctx, senderRole, recipientRole, deptID, revision) {
			return nil
		}
		// Department leader can send to CEO
		if recipientRole == ceoRole {
			return nil
		}
		// Default deny for dept leader
		return ErrTopologyViolation
	}

	// Check worker edges
	workerDept := v.getWorkersDepartment(ctx, senderRole, revision)
	if workerDept != "" {
		// Worker can send to their own department leader
		if v.isDeptLeaderOfWorkersDepartment(ctx, recipientRole, workerDept, revision) {
			return nil
		}
		// Workers cannot send peer-to-peer (V1 explicit denial)
		return ErrTopologyViolation
	}

	// Any other role not covered by above rules
	return ErrTopologyViolation
}

// isDepartmentLeader checks if a role is a department leader and optionally returns its department ID.
func (v *TopologyValidator) isDepartmentLeader(ctx context.Context, roleID, expectRecipient string, revision *registry.Revision) (bool, string) {
	if revision == nil {
		return false, ""
	}

	role, err := v.reader.GetRole(ctx, v.orgID, roleID)
	if err != nil {
		return false, ""
	}

	// Department leaders have specific leadership roles
	// Check if this role leads a department unit
	for _, unit := range revision.Units {
		if unit.CanonicalLeader == roleID {
			return true, unit.ID
		}
	}
	return false, ""
}

// isWorkerInDepartment checks if recipientRole is a worker in the sender's department.
func (v *TopologyValidator) isWorkerInDepartment(ctx context.Context, senderRole, recipientRole, deptID string, revision *registry.Revision) bool {
	if revision == nil {
		return false
	}

	// Find unit by ID from available units
	var foundUnit *registry.Unit
	for i := range revision.Units {
		u := &revision.Units[i]
		if u.ID == deptID {
			foundUnit = u
			break
		}
	}
	if foundUnit == nil {
		return false
	}

	// Check if recipient role belongs to this unit
	for _, member := range foundUnit.Members {
		if member.RoleID == recipientRole {
			return true
		}
	}
	return false
}

// getWorkersDepartment gets the department that owns the worker's role.
func (v *TopologyValidator) getWorkersDepartment(ctx context.Context, roleID string, revision *registry.Revision) string {
	if revision == nil {
		return ""
	}

	for _, unit := range revision.Units {
		for _, member := range unit.Members {
			if member.RoleID == roleID {
				return unit.ID
			}
		}
	}
	return ""
}

// isDeptLeaderOfWorkersDepartment checks if recipientRole is the department leader of the sender's department.
func (v *TopologyValidator) isDeptLeaderOfWorkersDepartment(ctx context.Context, recipientRole, workerDept string, revision *registry.Revision) bool {
	if revision == nil {
		return false
	}

	unit, ok := revision.GetUnitByID(workerDept)
	if !ok {
		return false
	}

	return unit.CanonicalLeader == recipientRole
}

// ErrTopologyViolation indicates a message send violates topology constraints.
var ErrTopologyViolation = errors.New("message topology violation: sender->recipient edge not permitted by V1 topology")
