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
// V1 Enforces ONLY these edges:
//   - CEO -> department leaders (of any unit in same org)
//   - department leader -> own workers OR CEO
//   - worker -> own department leader ONLY
//   - department leader -> CEO
//
// Owner/control-plane access removed unless explicitly needed.
type TopologyValidator struct {
	reader registry.Reader
	orgID  string
}

func NewTopologyValidator(reader registry.Reader, orgID string) *TopologyValidator {
	return &TopologyValidator{reader: reader, orgID: orgID}
}

// ValidateEdge checks if an edge from senderRole to recipientRole is permitted by V1 topology.
// Returns ErrTopologyViolation if edge not allowed.
func (v *TopologyValidator) ValidateEdge(ctx context.Context, senderRole, recipientRole string) error {
	senderRole = strings.TrimSpace(senderRole)
	recipientRole = strings.TrimSpace(recipientRole)
	if senderRole == "" || recipientRole == "" {
		return fmt.Errorf("sender and recipient roles required")
	}

	ceoRole := "empresa/ceo"

	// Verify both roles exist in our org before proceeding
	senderRoleData, err := v.reader.GetRole(ctx, v.orgID, senderRole)
	if err != nil {
		return fmt.Errorf("role %q does not exist or is inaccessible: %w", senderRole, err)
	}
	if senderRoleData.OrganizationID != "" && senderRoleData.OrganizationID != v.orgID {
		return fmt.Errorf("%w: role %q belongs to org %q, expected %q",
			ErrTopologyViolation, senderRole, senderRoleData.OrganizationID, v.orgID)
	}
	// A disabled role is not a participant. The registry query already
	// excludes retired roles, but Enabled=false is a live, reversible
	// "this role must not act right now" switch, and the rest of the system
	// honours it (contextengine refuses to build a context for a disabled
	// actor). Letting one keep sending and receiving agent messages would
	// leave a decommissioned agent quietly operating on a bus everything
	// else has already shut it out of.
	if !senderRoleData.Enabled {
		return fmt.Errorf("%w: sender role %q is disabled", ErrTopologyViolation, senderRole)
	}

	recipientRoleData, err := v.reader.GetRole(ctx, v.orgID, recipientRole)
	if err != nil {
		// Non-existent recipient → deny (safety: cannot delegate to unknown role)
		return fmt.Errorf("%w: recipient role %q does not exist or is inaccessible",
			ErrTopologyViolation, recipientRole)
	}
	if recipientRoleData.OrganizationID != "" && recipientRoleData.OrganizationID != v.orgID {
		return fmt.Errorf("%w: recipient role %q belongs to org %q, expected %q",
			ErrTopologyViolation, recipientRole, recipientRoleData.OrganizationID, v.orgID)
	}
	if !recipientRoleData.Enabled {
		return fmt.Errorf("%w: recipient role %q is disabled", ErrTopologyViolation, recipientRole)
	}

	// Self-message check
	if senderRole == recipientRole {
		return fmt.Errorf("%w: self-message denied", ErrTopologyViolation)
	}

	// CEO -> department leaders (any unit's canonical_leader in same org)
	if senderRole == ceoRole {
		// Get all department leaders and check if recipient is one of them
		departments, _ := v.reader.ListUnits(ctx, v.orgID)
		for _, dept := range departments {
			leader, err := v.reader.GetLeader(ctx, v.orgID, dept.ID)
			if err != nil {
				continue
			}
			if leader.ID == recipientRole {
				return nil
			}
		}
		return fmt.Errorf("%w: CEO can only send to department leaders", ErrTopologyViolation)
	}

	// Department leader -> own workers OR CEO
	if senderRoleData.CanonicalLeader {
		sentUnitID := senderRoleData.UnitID
		if sentUnitID == "" {
			return fmt.Errorf("%w: department leader %q lacks assigned unit", ErrTopologyViolation, senderRole)
		}

		// Can send to own workers
		if v.senderUnitHasWorker(ctx, sentUnitID, recipientRole) {
			return nil
		}

		// Can send back to CEO
		if recipientRole == ceoRole {
			return nil
		}

		return fmt.Errorf("%w: department leader can only send to their own workers or CEO", ErrTopologyViolation)
	}

	// Worker -> own department leader ONLY (no peer-to-peer!)
	// Find which unit this worker belongs to
	workerUnitID := senderRoleData.UnitID
	if workerUnitID == "" {
		return fmt.Errorf("%w: role %q is neither CEO nor department leader and lacks unit assignment",
			ErrTopologyViolation, senderRole)
	}

	// Get the leader of this worker's unit
	unitLeader, err := v.reader.GetLeader(ctx, v.orgID, workerUnitID)
	if err != nil {
		return fmt.Errorf("%w: could not resolve unit leader for unit %q: %w",
			ErrTopologyViolation, workerUnitID, err)
	}

	// Worker can ONLY send to their own dept leader
	if unitLeader.ID == recipientRole {
		return nil
	}

	return fmt.Errorf("%w: worker can only send to their own department leader", ErrTopologyViolation)
}

// extractUnitFromRole extracts the unit prefix from a role ID (e.g., "ingenieria_ia/orquestador" -> "ingenieria_ia").
func extractUnitFromRole(roleID string) (unitID string, roleSlug string) {
	parts := strings.SplitN(roleID, "/", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return "", roleID
}

// senderUnitHasWorker checks if recipientRole is listed as a worker in sender's unit.
func (v *TopologyValidator) senderUnitHasWorker(ctx context.Context, unitID, recipientRole string) bool {
	if unitID == "" || recipientRole == "" {
		return false
	}

	workers, err := v.reader.ListWorkers(ctx, v.orgID, unitID)
	if err != nil {
		return false
	}

	for _, w := range workers {
		if w.ID == recipientRole {
			return true
		}
	}
	return false
}

var ErrTopologyViolation = errors.New("topology violation: sender->recipient edge not permitted by V1 topology")
