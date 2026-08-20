package executive

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

type Validator struct {
	registry RegistryResolver
	authz    AuthorizationGate
	limits   Limits
}

func NewValidator(registry RegistryResolver, authz AuthorizationGate, limits Limits) (*Validator, error) {
	if registry == nil || authz == nil {
		return nil, fmt.Errorf("executive validator requires registry and authorization")
	}
	if limits.MaxDepartments <= 0 {
		limits = DefaultLimits()
	}
	return &Validator{registry: registry, authz: authz, limits: limits}, nil
}

func (v *Validator) ValidateExecutivePlan(ctx context.Context, revisionID int64, plan ExecutivePlan) (map[string]RoleRef, error) {
	current, err := v.registry.CurrentRevision(ctx)
	if err != nil {
		return nil, err
	}
	if current.ID != revisionID {
		return nil, fmt.Errorf("%w: organization revision drift", ErrRegistryMismatch)
	}
	if len(plan.DepartmentRequests) == 0 || len(plan.DepartmentRequests) > v.limits.MaxDepartments {
		return nil, ErrPlanTooLarge
	}
	seen := map[string]struct{}{}
	leaders := make(map[string]RoleRef, len(plan.DepartmentRequests))
	for _, req := range plan.DepartmentRequests {
		if _, ok := seen[req.UnitID]; ok {
			return nil, fmt.Errorf("%w: duplicate department %s", ErrContractRejected, req.UnitID)
		}
		seen[req.UnitID] = struct{}{}
		unit, e := v.registry.GetUnit(ctx, req.UnitID)
		if e != nil {
			return nil, fmt.Errorf("%w: department %s: %v", ErrRegistryMismatch, req.UnitID, e)
		}
		if unit.Retired || !unit.Operational || unit.Leaderless || unit.LeaderRoleID == "" {
			return nil, fmt.Errorf("%w: department %s is not an operational led unit", ErrRegistryMismatch, req.UnitID)
		}
		leader, e := v.registry.GetLeader(ctx, req.UnitID)
		if e != nil {
			return nil, fmt.Errorf("%w: leader %s: %v", ErrRegistryMismatch, req.UnitID, e)
		}
		if leader.ID != unit.LeaderRoleID || leader.UnitID != unit.ID || !leader.CanonicalLeader || !roleAssignable(leader) {
			return nil, fmt.Errorf("%w: canonical leader for %s", ErrRoleNotAssignable, req.UnitID)
		}
		decision, e := v.authz.Evaluate(ctx, AuthorizationRequest{OrganizationRevisionID: revisionID, ActorRoleID: CEORoleID, CapabilityID: "project.delegate_department", ResourceType: "organization_unit", ResourceID: req.UnitID, ActionDigest: actionDigest("delegate", req.UnitID, req.Objective, req.Deliverable)})
		if e != nil {
			return nil, e
		}
		if !decision.Allowed {
			return nil, fmt.Errorf("%w: CEO delegation denied for %s: %s", ErrRegistryMismatch, req.UnitID, decision.ReasonCode)
		}
		leaders[req.UnitID] = leader
	}
	return leaders, nil
}

func (v *Validator) ValidateDepartmentPlan(ctx context.Context, revisionID int64, departmentID, leaderRoleID string, plan DepartmentPlan) error {
	if plan.DepartmentID != departmentID {
		return fmt.Errorf("%w: department plan mismatch", ErrRegistryMismatch)
	}
	current, err := v.registry.CurrentRevision(ctx)
	if err != nil {
		return err
	}
	if current.ID != revisionID {
		return fmt.Errorf("%w: organization revision drift", ErrRegistryMismatch)
	}
	leader, err := v.registry.GetLeader(ctx, departmentID)
	if err != nil {
		return err
	}
	if leader.ID != leaderRoleID || !roleAssignable(leader) || !leader.CanonicalLeader {
		return ErrRoleNotAssignable
	}
	keys := map[string]struct{}{}
	for _, t := range plan.Tasks {
		if _, dup := keys[t.ClientKey]; dup {
			return fmt.Errorf("%w: duplicate client_key %s", ErrContractRejected, t.ClientKey)
		}
		keys[t.ClientKey] = struct{}{}
		role, e := v.registry.GetRole(ctx, t.AssignedRoleID)
		if e != nil {
			return fmt.Errorf("%w: role %s: %v", ErrRegistryMismatch, t.AssignedRoleID, e)
		}
		if !roleAssignable(role) {
			return fmt.Errorf("%w: %s", ErrRoleNotAssignable, t.AssignedRoleID)
		}
		if role.UnitID != departmentID {
			return fmt.Errorf("%w: %s -> %s", ErrCrossDepartmentDelegation, departmentID, t.AssignedRoleID)
		}
		if role.ID == OwnerRoleID || role.ID == CEORoleID || role.ID == ObserverRoleID {
			return fmt.Errorf("%w: prohibited worker role %s", ErrCrossDepartmentDelegation, role.ID)
		}
		// An execution service is a deterministic executor, not a cognitive
		// worker. It has no model policy and no model binding, so a worker
		// task assigned to one can never be dispatched: the run reaches the
		// department phase, waits for an authorization nothing will ever
		// issue, and stalls. Rejecting it here fails at plan validation
		// instead of six steps later.
		//
		// Executable=true does NOT mean "can be executed through the
		// Harness". It means the role may participate operationally. This
		// validator previously conflated the two, which is exactly how
		// ingenieria_ia/code-runner -- named "Ejecutor de código", the only
		// role in its department with shell access and the one that can
		// commit -- became a semantically reasonable and structurally
		// impossible choice for "modify this file".
		//
		// Execution services enter through their own governed path
		// (EngineeringMission -> CodeRunner), never through a department
		// plan. The rule is deliberately negative and narrow: no positive
		// allowlist of authority classes, because the invariant we actually
		// found is that an execution service is not a cognitive worker.
		if role.AuthorityClass == "execution_service" {
			return fmt.Errorf("%w: execution service %s cannot be assigned as a cognitive department worker", ErrRoleNotAssignable, role.ID)
		}
		// A cognitive worker task runs a model and produces a validated
		// result. That is the entire lifecycle available to it: its outcome
		// is a durable model result plus evidence references, and it has no
		// mechanism to write a file, run a check, grant an approval or make a
		// condition true. A BLOCKING requirement of any of those types is an
		// obligation nothing in that path can discharge, so the task can
		// never complete -- CompletionGate correctly refuses it, and the run
		// dies after the model has already been paid for.
		//
		// The five requirement types stay valid everywhere else, because the
		// question is not whether a type is legitimate but WHO can satisfy
		// it: artifacts and checks are materialized later by
		// EngineeringMission and CodeRunner, approvals by promotion review,
		// conditions by the gates that own them.
		//
		// Type alone is not enough, and assuming it was is what let the next
		// run fail the same way one level down. The host satisfies a result
		// requirement only when its KEY is one the host itself owns: a leader
		// that invents "document_content_result" produces a well-typed
		// obligation that nothing ever discharges, and the task hangs exactly
		// as it did with a required artifact.
		//
		// The host already attaches its own blocking requirement to every
		// worker task (appendResultRequirement adds model_result), so the
		// only blocking obligation a cognitive worker task needs already
		// exists and is guaranteed to be satisfied by the validated model
		// result. A leader proposing that same key is redundant but harmless;
		// proposing any other blocking requirement is asking for something
		// no part of this path can deliver.
		//
		// Optional requirements of any type are left alone: they do not block
		// completion and can carry real descriptive intent. What must never
		// exist is a blocking one with no satisfier.
		for _, requirement := range t.Requirements {
			if !requirement.Required {
				continue
			}
			if requirement.Type == "result" && requirement.Key == hostOwnedResultRequirementKey {
				continue
			}
			return fmt.Errorf("%w: blocking requirement %q of type %q on cognitive worker task %q; the host owns the only blocking requirement (%q) and nothing else can satisfy one",
				ErrRequirementUnsatisfiable, requirement.Key, requirement.Type, t.ClientKey, hostOwnedResultRequirementKey)
		}
		decision, e := v.authz.Evaluate(ctx, AuthorizationRequest{OrganizationRevisionID: revisionID, ActorRoleID: leaderRoleID, CapabilityID: "task.assign_worker", ResourceType: "role", ResourceID: role.ID, ActionDigest: actionDigest("assign-worker", departmentID, t.ClientKey, role.ID, t.Instructions)})
		if e != nil {
			return e
		}
		if !decision.Allowed {
			return fmt.Errorf("%w: worker assignment denied: %s", ErrRoleNotAssignable, decision.ReasonCode)
		}
	}
	for _, t := range plan.Tasks {
		for _, dep := range t.Dependencies {
			if _, ok := keys[dep]; !ok {
				return fmt.Errorf("%w: missing dependency %s", ErrContractRejected, dep)
			}
		}
	}
	if dependencyCycle(plan.Tasks) {
		return ErrDependencyCycle
	}
	return nil
}

func (v *Validator) ValidateFollowups(ctx context.Context, revisionID int64, departmentID, leaderRoleID string, tasks []WorkerTaskProposal) error {
	return v.ValidateDepartmentPlan(ctx, revisionID, departmentID, leaderRoleID, DepartmentPlan{SchemaVersion: DepartmentPlanSchemaVersion, DepartmentID: departmentID, Tasks: tasks, ReviewCriteria: []string{}, Unresolved: []string{}})
}

func roleAssignable(r RoleRef) bool { return r.ID != "" && r.Enabled && r.Executable && !r.Retired }

func dependencyCycle(tasks []WorkerTaskProposal) bool {
	graph := map[string][]string{}
	for _, t := range tasks {
		graph[t.ClientKey] = append([]string(nil), t.Dependencies...)
	}
	state := map[string]uint8{}
	var visit func(string) bool
	visit = func(n string) bool {
		if state[n] == 1 {
			return true
		}
		if state[n] == 2 {
			return false
		}
		state[n] = 1
		for _, d := range graph[n] {
			if visit(d) {
				return true
			}
		}
		state[n] = 2
		return false
	}
	keys := make([]string, 0, len(graph))
	for k := range graph {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if visit(k) {
			return true
		}
	}
	return false
}

func actionDigest(parts ...string) string {
	normalized := make([]string, len(parts))
	for i, p := range parts {
		normalized[i] = strings.TrimSpace(p)
	}
	sum := sha256.Sum256([]byte(strings.Join(normalized, "\x00")))
	return hex.EncodeToString(sum[:])
}
