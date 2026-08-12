package agentmessaging_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/agentmessaging"
	"github.com/Mireuz13/explorarte-organization/internal/agentmessaging/topologyfixture"
)

// The V1 topology contract, expressed as executable behavior rather than as
// a claim in a manifest. This is the ORG-04 remedy: securityaudit's catalog
// asserted `topology_check` for the agent_messages channel while the
// enforcement point was a bare TODO, and no test could tell the difference
// because every assertion inspected the catalog's own string literals.
// These cases inspect the real ValidateEdge decision instead, so deleting or
// weakening enforcement fails the build no matter what the catalog says
// about itself.

// TestTopologyV1EdgeContract is the ALLOW/DENY matrix required by the
// security review before agent messaging may be described as hardened.
func TestTopologyV1EdgeContract(t *testing.T) {
	cases := []struct {
		name      string
		sender    string
		recipient string
		allow     bool
	}{
		// The four permitted V1 edges.
		{"worker to own department leader", topologyfixture.RoleEngineeringA, topologyfixture.RoleEngineeringLead, true},
		{"department leader to own worker", topologyfixture.RoleEngineeringLead, topologyfixture.RoleEngineeringA, true},
		{"department leader to CEO", topologyfixture.RoleEngineeringLead, topologyfixture.RoleCEO, true},
		{"CEO to department leader", topologyfixture.RoleCEO, topologyfixture.RoleEngineeringLead, true},

		// Denied edges -- the cases that make the control worth having.
		{"CEO to arbitrary worker", topologyfixture.RoleCEO, topologyfixture.RoleEngineeringA, false},
		{"worker to peer worker in same unit", topologyfixture.RoleEngineeringA, topologyfixture.RoleEngineeringB, false},
		{"engineering leader to finance worker", topologyfixture.RoleEngineeringLead, topologyfixture.RoleFinanceWorker, false},
		{"engineering leader to finance leader", topologyfixture.RoleEngineeringLead, topologyfixture.RoleFinanceLead, false},
		{"worker to foreign department leader", topologyfixture.RoleEngineeringA, topologyfixture.RoleFinanceLead, false},
		{"worker directly to CEO", topologyfixture.RoleEngineeringA, topologyfixture.RoleCEO, false},
		{"cross-department worker to worker", topologyfixture.RoleEngineeringA, topologyfixture.RoleFinanceWorker, false},
		{"self message", topologyfixture.RoleEngineeringA, topologyfixture.RoleEngineeringA, false},
		{"unknown recipient role", topologyfixture.RoleEngineeringLead, "ingenieria/ghost", false},

		// The human owner is deliberately not a participant on this bus.
		// Human authority (stop this, reprioritise, approve, CEO do X) enters
		// through an explicit control/governance interface; letting
		// empresa/human send agent messages would merge governance with
		// operational agent communication, and would be the fifth edge that
		// V1 exists to refuse. These stay DENY on purpose -- if the decision
		// is ever revisited, this is the test to change first, consciously.
		{"owner to CEO", topologyfixture.RoleOwner, topologyfixture.RoleCEO, false},
		{"owner to department leader", topologyfixture.RoleOwner, topologyfixture.RoleEngineeringLead, false},
		{"owner to worker", topologyfixture.RoleOwner, topologyfixture.RoleEngineeringA, false},
		{"CEO to owner", topologyfixture.RoleCEO, topologyfixture.RoleOwner, false},

		// A disabled role is not a participant on this bus. Enabled=false is
		// the live "this role must not act right now" switch that the rest of
		// the system already honours; before this rule, a decommissioned agent
		// could still send and receive here.
		{"disabled worker to own leader", topologyfixture.RoleEngineeringDisabled, topologyfixture.RoleEngineeringLead, false},
		{"leader to disabled worker", topologyfixture.RoleEngineeringLead, topologyfixture.RoleEngineeringDisabled, false},
		{"CEO to disabled worker", topologyfixture.RoleCEO, topologyfixture.RoleEngineeringDisabled, false},
		{"department leader to owner", topologyfixture.RoleEngineeringLead, topologyfixture.RoleOwner, false},
	}

	validator := agentmessaging.NewTopologyValidator(topologyfixture.NewReader(), topologyfixture.OrganizationID)
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			err := validator.ValidateEdge(context.Background(), testCase.sender, testCase.recipient)
			if testCase.allow {
				if err != nil {
					t.Fatalf("edge %s -> %s must be permitted, got: %v", testCase.sender, testCase.recipient, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("edge %s -> %s must be denied, got allow", testCase.sender, testCase.recipient)
			}
			if !errors.Is(err, agentmessaging.ErrTopologyViolation) {
				t.Fatalf("edge %s -> %s denied with the wrong error class: %v", testCase.sender, testCase.recipient, err)
			}
		})
	}
}

// TestTopologyDeniesUnresolvableSender covers the zero-trust precondition:
// a sender the registry cannot resolve is never given the benefit of the
// doubt, because SenderRoleID is caller-supplied data.
func TestTopologyDeniesUnresolvableSender(t *testing.T) {
	validator := agentmessaging.NewTopologyValidator(topologyfixture.NewReader(), topologyfixture.OrganizationID)
	if err := validator.ValidateEdge(context.Background(), "ingenieria/impostor", topologyfixture.RoleEngineeringLead); err == nil {
		t.Fatal("an unresolvable sender role must be denied, not trusted")
	}
}

// TestTopologyDeniesForeignOrganization proves the organization boundary
// holds even when both role identifiers are well formed.
func TestTopologyDeniesForeignOrganization(t *testing.T) {
	validator := agentmessaging.NewTopologyValidator(topologyfixture.NewReader(), "otra-empresa")
	if err := validator.ValidateEdge(context.Background(), topologyfixture.RoleEngineeringA, topologyfixture.RoleEngineeringLead); err == nil {
		t.Fatal("an edge evaluated against a foreign organization must be denied")
	}
}

// TestTopologyDeniesDegenerateRoles guards the inputs a forged or truncated
// command would produce.
func TestTopologyDeniesDegenerateRoles(t *testing.T) {
	validator := agentmessaging.NewTopologyValidator(topologyfixture.NewReader(), topologyfixture.OrganizationID)
	for _, pair := range [][2]string{
		{"", topologyfixture.RoleEngineeringLead},
		{topologyfixture.RoleEngineeringA, ""},
		{"", ""},
		{"   ", topologyfixture.RoleEngineeringLead},
		{topologyfixture.RoleEngineeringA, "   "},
	} {
		if err := validator.ValidateEdge(context.Background(), pair[0], pair[1]); err == nil {
			t.Fatalf("degenerate edge (%q -> %q) must be denied", pair[0], pair[1])
		}
	}
}
