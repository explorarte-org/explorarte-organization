package executive

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestExecutivePlanStrictParseAndForbiddenFields(t *testing.T) {
	limits := DefaultLimits()
	good := []byte(`{"schema_version":"executive-plan/v1","objective":"ship","department_requests":[{"unit_id":"ingenieria_ia","objective":"plan","deliverable":"report","priority":1,"constraints":[]}],"global_constraints":[],"success_criteria":["done"],"owner_decisions_required":[]}`)
	if _, err := ParseExecutivePlan(good, limits); err != nil {
		t.Fatalf("parse good plan: %v", err)
	}
	unknown := []byte(`{"schema_version":"executive-plan/v1","objective":"ship","department_requests":[],"global_constraints":[],"success_criteria":["done"],"owner_decisions_required":[],"surprise":true}`)
	if _, err := ParseExecutivePlan(unknown, limits); !errors.Is(err, ErrContractRejected) {
		t.Fatalf("unknown field err=%v", err)
	}
	provider := []byte(`{"schema_version":"executive-plan/v1","objective":"ship","department_requests":[{"unit_id":"ingenieria_ia","objective":"plan","deliverable":"report","priority":1,"constraints":[],"provider":"unapproved-vendor"}],"global_constraints":[],"success_criteria":["done"],"owner_decisions_required":[]}`)
	if _, err := ParseExecutivePlan(provider, limits); !errors.Is(err, ErrForbiddenField) {
		t.Fatalf("provider field err=%v", err)
	}
	model := []byte(`{"schema_version":"executive-plan/v1","objective":"ship","department_requests":[{"unit_id":"ingenieria_ia","objective":"plan","deliverable":"report","priority":1,"constraints":[],"model":"anything"}],"global_constraints":[],"success_criteria":["done"],"owner_decisions_required":[]}`)
	if _, err := ParseExecutivePlan(model, limits); !errors.Is(err, ErrForbiddenField) {
		t.Fatalf("model field err=%v", err)
	}
	grant := []byte(`{"schema_version":"executive-plan/v1","objective":"ship","department_requests":[{"unit_id":"ingenieria_ia","objective":"plan","deliverable":"report","priority":1,"constraints":[]}],"global_constraints":["x"],"success_criteria":["done"],"owner_decisions_required":[],"capability_grant":"*"}`)
	if _, err := ParseExecutivePlan(grant, limits); !errors.Is(err, ErrForbiddenField) {
		t.Fatalf("capability grant err=%v", err)
	}
}

func TestExecutivePlanBounds(t *testing.T) {
	limits := DefaultLimits()
	limits.MaxDepartments = 1
	body := []byte(`{"schema_version":"executive-plan/v1","objective":"ship","department_requests":[{"unit_id":"a","objective":"x","deliverable":"y","priority":1,"constraints":[]},{"unit_id":"b","objective":"x","deliverable":"y","priority":1,"constraints":[]}],"global_constraints":[],"success_criteria":["done"],"owner_decisions_required":[]}`)
	if _, err := ParseExecutivePlan(body, limits); !errors.Is(err, ErrPlanTooLarge) {
		t.Fatalf("bounds err=%v", err)
	}
	oversize := []byte(strings.Repeat("x", limits.MaxInputBytes+1))
	if _, err := ParseExecutivePlan(oversize, limits); !errors.Is(err, ErrPlanTooLarge) {
		t.Fatalf("oversize err=%v", err)
	}
}

type fakeRegistry struct {
	rev     RevisionRef
	units   map[string]UnitRef
	roles   map[string]RoleRef
	leaders map[string]RoleRef
}

func (f fakeRegistry) CurrentRevision(context.Context) (RevisionRef, error) { return f.rev, nil }
func (f fakeRegistry) GetUnit(_ context.Context, id string) (UnitRef, error) {
	v, ok := f.units[id]
	if !ok {
		return UnitRef{}, errors.New("not found")
	}
	return v, nil
}
func (f fakeRegistry) GetRole(_ context.Context, id string) (RoleRef, error) {
	v, ok := f.roles[id]
	if !ok {
		return RoleRef{}, errors.New("not found")
	}
	return v, nil
}
func (f fakeRegistry) GetLeader(_ context.Context, id string) (RoleRef, error) {
	v, ok := f.leaders[id]
	if !ok {
		return RoleRef{}, errors.New("not found")
	}
	return v, nil
}

type allowAuthz struct{}

func (allowAuthz) Evaluate(context.Context, AuthorizationRequest) (AuthorizationDecision, error) {
	return AuthorizationDecision{Allowed: true, Effect: "allow"}, nil
}

func testValidator(t *testing.T) (*Validator, fakeRegistry) {
	t.Helper()
	leader := RoleRef{ID: "ingenieria_ia/orquestador", UnitID: "ingenieria_ia", Enabled: true, Executable: true, CanonicalLeader: true}
	worker := RoleRef{ID: "ingenieria_ia/backend_engineer", UnitID: "ingenieria_ia", Enabled: true, Executable: true}
	other := RoleRef{ID: "marketing/estratega_crecimiento", UnitID: "marketing", Enabled: true, Executable: true, CanonicalLeader: true}
	r := fakeRegistry{rev: RevisionRef{ID: 7}, units: map[string]UnitRef{"ingenieria_ia": {ID: "ingenieria_ia", Operational: true, LeaderRoleID: leader.ID}, "marketing": {ID: "marketing", Operational: true, LeaderRoleID: other.ID}}, roles: map[string]RoleRef{leader.ID: leader, worker.ID: worker, other.ID: other}, leaders: map[string]RoleRef{"ingenieria_ia": leader, "marketing": other}}
	v, err := NewValidator(r, allowAuthz{}, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	return v, r
}

func TestExecutivePlanRegistryResolvesLeader(t *testing.T) {
	v, _ := testValidator(t)
	p := ExecutivePlan{SchemaVersion: ExecutivePlanSchemaVersion, Objective: "x", DepartmentRequests: []DepartmentRequest{{UnitID: "ingenieria_ia", Objective: "x", Deliverable: "y"}}, SuccessCriteria: []string{"done"}}
	leaders, err := v.ValidateExecutivePlan(context.Background(), 7, p)
	if err != nil {
		t.Fatal(err)
	}
	if leaders["ingenieria_ia"].ID != "ingenieria_ia/orquestador" {
		t.Fatalf("leader=%q", leaders["ingenieria_ia"].ID)
	}
}
func TestInvalidDepartmentRejected(t *testing.T) {
	v, _ := testValidator(t)
	p := ExecutivePlan{SchemaVersion: ExecutivePlanSchemaVersion, Objective: "x", DepartmentRequests: []DepartmentRequest{{UnitID: "inventado", Objective: "x", Deliverable: "y"}}, SuccessCriteria: []string{"done"}}
	if _, err := v.ValidateExecutivePlan(context.Background(), 7, p); !errors.Is(err, ErrRegistryMismatch) {
		t.Fatalf("err=%v", err)
	}
}
func TestCrossDepartmentWorkerRejected(t *testing.T) {
	v, _ := testValidator(t)
	p := DepartmentPlan{SchemaVersion: DepartmentPlanSchemaVersion, DepartmentID: "ingenieria_ia", Tasks: []WorkerTaskProposal{{ClientKey: "a", AssignedRoleID: "marketing/estratega_crecimiento", Title: "x", Instructions: "x", AcceptanceCriteria: []string{"done"}}}}
	if err := v.ValidateDepartmentPlan(context.Background(), 7, "ingenieria_ia", "ingenieria_ia/orquestador", p); !errors.Is(err, ErrCrossDepartmentDelegation) {
		t.Fatalf("err=%v", err)
	}
}
func TestRetiredRoleRejected(t *testing.T) {
	v, r := testValidator(t)
	w := r.roles["ingenieria_ia/backend_engineer"]
	w.Retired = true
	r.roles[w.ID] = w
	v, _ = NewValidator(r, allowAuthz{}, DefaultLimits())
	p := DepartmentPlan{SchemaVersion: DepartmentPlanSchemaVersion, DepartmentID: "ingenieria_ia", Tasks: []WorkerTaskProposal{{ClientKey: "a", AssignedRoleID: w.ID, Title: "x", Instructions: "x", AcceptanceCriteria: []string{"done"}}}}
	if err := v.ValidateDepartmentPlan(context.Background(), 7, "ingenieria_ia", "ingenieria_ia/orquestador", p); !errors.Is(err, ErrRoleNotAssignable) {
		t.Fatalf("err=%v", err)
	}
}
func TestDependencyCycleRejected(t *testing.T) {
	v, _ := testValidator(t)
	p := DepartmentPlan{SchemaVersion: DepartmentPlanSchemaVersion, DepartmentID: "ingenieria_ia", Tasks: []WorkerTaskProposal{{ClientKey: "a", AssignedRoleID: "ingenieria_ia/backend_engineer", Title: "a", Instructions: "a", AcceptanceCriteria: []string{"done"}, Dependencies: []string{"b"}}, {ClientKey: "b", AssignedRoleID: "ingenieria_ia/backend_engineer", Title: "b", Instructions: "b", AcceptanceCriteria: []string{"done"}, Dependencies: []string{"a"}}}}
	if err := v.ValidateDepartmentPlan(context.Background(), 7, "ingenieria_ia", "ingenieria_ia/orquestador", p); !errors.Is(err, ErrDependencyCycle) {
		t.Fatalf("err=%v", err)
	}
}

func TestBudgetNormalFormulaAndBounds(t *testing.T) {
	// Plan, adjudicate and close, plus two calls per department and the
	// worker attempts the plan asked for.
	if got := NormalExpectedCalls(3, 8, true); got != 17 {
		t.Fatalf("got=%d", got)
	}
	l := DefaultLimits()
	b := InvocationBudget{CEOCalls: ceoPhases, LeaderCalls: 6, WorkerAttempts: 8}
	if err := b.Validate(l, 3); err != nil {
		t.Fatal(err)
	}

	// The happy path of a design-frozen run is three CEO calls. It used to be
	// rejected, which is what killed AUTONOMY-SMOKE-001's root 294 with the
	// adversarial review already complete and the adjudication under way.
	if err := (InvocationBudget{CEOCalls: ceoPhases}).Validate(l, 1); err != nil {
		t.Fatalf("a design-frozen campaign must fit its own happy path: %v", err)
	}
	// And each of those phases may be retried, because every governed task is
	// created with attempts to spend. A ceiling that forbids the engine's own
	// recovery fails the run for recovering.
	if err := (InvocationBudget{CEOCalls: ceoPhases + 1}).Validate(l, 1); err != nil {
		t.Fatalf("one retry of one CEO phase must fit: %v", err)
	}
	// Past that, a campaign is looping rather than working.
	if err := (InvocationBudget{CEOCalls: ceoPhases*governedTaskAttempts + 1}).Validate(l, 1); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("err=%v", err)
	}
}
func TestProjectRunRestartProjection(t *testing.T) {
	root := TaskRecord{ID: 1, AssignedRoleID: CEORoleID, CorrelationID: "executive:x", Status: "ready"}
	children := []TaskRecord{{ID: 2, IdempotencyKey: "executive:1:ceo-plan", Status: "completed"}, {ID: 3, IdempotencyKey: "executive:1:leader-plan:ingenieria_ia", Status: "completed"}, {ID: 4, IdempotencyKey: "executive:1:worker:ingenieria_ia:a", Status: "running"}}
	if got := ProjectRun(root, children).State; got != StateWorkerExecution {
		t.Fatalf("state=%s", got)
	}
	root.Status = "blocked"
	root.ReasonCode = "dispatch_assignment_required"
	if got := ProjectRun(root, children); got.State != StateBlocked || got.ReasonCode != "dispatch_assignment_required" {
		t.Fatalf("run=%+v", got)
	}
}
