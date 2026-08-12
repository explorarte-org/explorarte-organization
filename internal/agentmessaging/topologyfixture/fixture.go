// Package topologyfixture provides a deterministic, in-memory organization
// for exercising agent-messaging topology enforcement.
//
// It exists as ordinary (non-test) code, alongside this repository's other
// *fixtures packages, so that more than one package can assert against the
// same organization without duplicating it. Two packages need it: the
// agentmessaging package proves the V1 edge contract directly, and the
// securityaudit package uses it to prove that the `topology_check` its
// catalog advertises for the agent_messages channel is a real, observable
// denial rather than a string in a manifest.
//
// The organization is deliberately small but complete enough to express
// every V1 edge and every interesting violation of one:
//
//	empresa/ceo            CEO
//	ingenieria/lead        leader of unit "ingenieria"
//	ingenieria/worker_a    worker in "ingenieria"
//	ingenieria/worker_b    worker in "ingenieria"
//	finanzas/lead          leader of unit "finanzas"
//	finanzas/worker        worker in "finanzas"
package topologyfixture

import (
	"context"
	"errors"

	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
)

// Organization and role identifiers of the fixture organization.
const (
	OrganizationID = "explorarte"

	RoleCEO = "empresa/ceo"
	// RoleOwner is the human owner. It is modelled precisely so the tests can
	// prove it has NO edge on this bus: human authority reaches the
	// organization through an explicit control/governance interface, not by
	// impersonating another agent in the operational topology.
	RoleOwner           = "empresa/human"
	RoleEngineeringLead = "ingenieria/lead"
	RoleEngineeringA    = "ingenieria/worker_a"
	RoleEngineeringB    = "ingenieria/worker_b"
	RoleFinanceLead     = "finanzas/lead"
	RoleFinanceWorker   = "finanzas/worker"

	// RoleEngineeringDisabled is a worker whose role has been switched off.
	// It exists so the disabled-role rule can be proved rather than assumed.
	RoleEngineeringDisabled = "ingenieria/worker_disabled"

	UnitEngineering = "ingenieria"
	UnitFinance     = "finanzas"
)

// ErrNotModelled is returned by the Reader surface that topology validation
// never consults. Returning an error rather than a zero value keeps an
// accidental dependency on unmodelled data loud instead of silent.
var ErrNotModelled = errors.New("topologyfixture: not modelled by this fixture")

// Reader is an in-memory registry.Reader describing the fixture
// organization. The zero value is not usable; construct one with NewReader.
type Reader struct {
	roles   map[string]registry.Role
	units   []registry.Unit
	leaders map[string]string
	workers map[string][]string
}

// NewReader builds the fixture organization.
func NewReader() *Reader {
	disabled := func(id, unit string) registry.Role {
		return registry.Role{OrganizationID: OrganizationID, ID: id, UnitID: unit, Enabled: false, Executable: false}
	}
	role := func(id, unit string, leader bool) registry.Role {
		return registry.Role{
			OrganizationID:  OrganizationID,
			ID:              id,
			UnitID:          unit,
			CanonicalLeader: leader,
			Enabled:         true,
			Executable:      true,
		}
	}
	return &Reader{
		roles: map[string]registry.Role{
			RoleCEO:                 role(RoleCEO, "empresa", false),
			RoleOwner:               role(RoleOwner, "empresa", false),
			RoleEngineeringLead:     role(RoleEngineeringLead, UnitEngineering, true),
			RoleEngineeringA:        role(RoleEngineeringA, UnitEngineering, false),
			RoleEngineeringB:        role(RoleEngineeringB, UnitEngineering, false),
			RoleEngineeringDisabled: disabled(RoleEngineeringDisabled, UnitEngineering),
			RoleFinanceLead:         role(RoleFinanceLead, UnitFinance, true),
			RoleFinanceWorker:       role(RoleFinanceWorker, UnitFinance, false),
		},
		units: []registry.Unit{
			{OrganizationID: OrganizationID, ID: UnitEngineering, Operational: true},
			{OrganizationID: OrganizationID, ID: UnitFinance, Operational: true},
		},
		leaders: map[string]string{
			UnitEngineering: RoleEngineeringLead,
			UnitFinance:     RoleFinanceLead,
		},
		workers: map[string][]string{
			UnitEngineering: {RoleEngineeringA, RoleEngineeringB, RoleEngineeringDisabled},
			UnitFinance:     {RoleFinanceWorker},
		},
	}
}

func (r *Reader) GetRole(_ context.Context, organizationID, roleID string) (registry.Role, error) {
	if organizationID != OrganizationID {
		return registry.Role{}, errors.New("topologyfixture: unknown organization")
	}
	role, ok := r.roles[roleID]
	if !ok {
		return registry.Role{}, errors.New("topologyfixture: unknown role")
	}
	return role, nil
}

func (r *Reader) ListUnits(_ context.Context, organizationID string) ([]registry.Unit, error) {
	if organizationID != OrganizationID {
		return nil, errors.New("topologyfixture: unknown organization")
	}
	return append([]registry.Unit(nil), r.units...), nil
}

func (r *Reader) GetLeader(_ context.Context, organizationID, unitID string) (registry.Role, error) {
	if organizationID != OrganizationID {
		return registry.Role{}, errors.New("topologyfixture: unknown organization")
	}
	leader, ok := r.leaders[unitID]
	if !ok {
		return registry.Role{}, errors.New("topologyfixture: unit has no leader")
	}
	return r.roles[leader], nil
}

func (r *Reader) ListWorkers(_ context.Context, organizationID, unitID string) ([]registry.Role, error) {
	if organizationID != OrganizationID {
		return nil, errors.New("topologyfixture: unknown organization")
	}
	ids := r.workers[unitID]
	workers := make([]registry.Role, 0, len(ids))
	for _, id := range ids {
		workers = append(workers, r.roles[id])
	}
	return workers, nil
}

func (r *Reader) GetOrganization(context.Context, string) (registry.Organization, error) {
	return registry.Organization{}, ErrNotModelled
}

func (r *Reader) GetUnit(context.Context, string, string) (registry.Unit, error) {
	return registry.Unit{}, ErrNotModelled
}

func (r *Reader) ListRoles(context.Context, string, registry.RoleFilter) ([]registry.Role, error) {
	return nil, ErrNotModelled
}

func (r *Reader) GetCurrentRevision(context.Context, string) (*registry.Revision, error) {
	return nil, ErrNotModelled
}

func (r *Reader) LoadCurrentSnapshot(context.Context, string) (*registry.Snapshot, error) {
	return nil, ErrNotModelled
}

var _ registry.Reader = (*Reader)(nil)
