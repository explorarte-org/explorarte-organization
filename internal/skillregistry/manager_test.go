package skillregistry

import (
	"context"
	"errors"
	"testing"
	"time"
)

type recordingGate struct {
	proposals   []string
	transitions [][2]Lifecycle
	assignments []string
	err         error
}

func (g *recordingGate) AuthorizeProposal(_ context.Context, _, _, skillID string) (GovernanceEvidence, error) {
	g.proposals = append(g.proposals, skillID)
	if g.err != nil {
		return GovernanceEvidence{}, g.err
	}
	return GovernanceEvidence{DecisionRef: "authz:propose:" + skillID, ActorRoleID: "empresa/human", DecidedAt: time.Now()}, nil
}

func (g *recordingGate) AuthorizeLifecycleChange(_ context.Context, _, _, _ string, from, to Lifecycle) (GovernanceEvidence, error) {
	g.transitions = append(g.transitions, [2]Lifecycle{from, to})
	if g.err != nil {
		return GovernanceEvidence{}, g.err
	}
	return GovernanceEvidence{DecisionRef: "authz:lifecycle:" + string(from) + "->" + string(to), ActorRoleID: "empresa/human", DecidedAt: time.Now()}, nil
}

func (g *recordingGate) AuthorizeAssignmentChange(_ context.Context, _, _, roleID, skillID, action string) (GovernanceEvidence, error) {
	g.assignments = append(g.assignments, action+":"+skillID+":"+roleID)
	if g.err != nil {
		return GovernanceEvidence{}, g.err
	}
	return GovernanceEvidence{DecisionRef: "authz:assignment:" + action, ActorRoleID: "empresa/human", DecidedAt: time.Now()}, nil
}

type fakeRepository struct {
	skills            map[string]Skill
	versions          map[string]SkillVersion
	skillIdempotency  map[string]string
	assignments       map[string]SkillAssignment
	assignIdempotency map[string]string
}

func newFakeRepository() *fakeRepository {
	return &fakeRepository{
		skills: map[string]Skill{}, versions: map[string]SkillVersion{}, skillIdempotency: map[string]string{},
		assignments: map[string]SkillAssignment{}, assignIdempotency: map[string]string{},
	}
}

func (r *fakeRepository) CreateSkill(_ context.Context, skill Skill, version SkillVersion, idempotencyKey string, _ GovernanceEvidence) (Skill, SkillVersion, bool, error) {
	if existingID, ok := r.skillIdempotency[idempotencyKey]; ok {
		return r.skills[skill.ID], r.versions[existingID], true, nil
	}
	r.skills[skill.ID] = skill
	r.versions[version.ID] = version
	r.skillIdempotency[idempotencyKey] = version.ID
	return skill, version, false, nil
}

func (r *fakeRepository) GetSkill(_ context.Context, _, skillID string) (Skill, error) {
	skill, ok := r.skills[skillID]
	if !ok {
		return Skill{}, ErrNotFound
	}
	return skill, nil
}

func (r *fakeRepository) GetVersion(_ context.Context, _, versionID string) (SkillVersion, error) {
	version, ok := r.versions[versionID]
	if !ok {
		return SkillVersion{}, ErrNotFound
	}
	return version, nil
}

func (r *fakeRepository) ListVersions(_ context.Context, _, skillID string) ([]SkillVersion, error) {
	values := []SkillVersion{}
	for _, version := range r.versions {
		if version.SkillID == skillID {
			values = append(values, version)
		}
	}
	return values, nil
}

func (r *fakeRepository) SaveVersion(_ context.Context, version SkillVersion, expectedRevision int64, _ LifecycleEvent) (SkillVersion, error) {
	current, ok := r.versions[version.ID]
	if !ok {
		return SkillVersion{}, ErrNotFound
	}
	if current.Revision != expectedRevision {
		return SkillVersion{}, ErrRevisionConflict
	}
	r.versions[version.ID] = version
	return version, nil
}

func (r *fakeRepository) CreateAssignment(_ context.Context, assignment SkillAssignment, idempotencyKey string, _ AssignmentEvent) (SkillAssignment, bool, error) {
	if existingID, ok := r.assignIdempotency[idempotencyKey]; ok {
		return r.assignments[existingID], true, nil
	}
	r.assignments[assignment.ID] = assignment
	r.assignIdempotency[idempotencyKey] = assignment.ID
	return assignment, false, nil
}

func (r *fakeRepository) GetAssignment(_ context.Context, _, assignmentID string) (SkillAssignment, error) {
	assignment, ok := r.assignments[assignmentID]
	if !ok {
		return SkillAssignment{}, ErrNotFound
	}
	return assignment, nil
}

func (r *fakeRepository) ListActiveAssignmentsForRole(_ context.Context, _, roleID string) ([]SkillAssignment, error) {
	values := []SkillAssignment{}
	for _, assignment := range r.assignments {
		if assignment.RoleID == roleID && assignment.Status == AssignmentActive {
			values = append(values, assignment)
		}
	}
	return values, nil
}

func (r *fakeRepository) SaveAssignment(_ context.Context, assignment SkillAssignment, expectedRevision int64, _ AssignmentEvent) (SkillAssignment, error) {
	current, ok := r.assignments[assignment.ID]
	if !ok {
		return SkillAssignment{}, ErrNotFound
	}
	if current.Revision != expectedRevision {
		return SkillAssignment{}, ErrRevisionConflict
	}
	r.assignments[assignment.ID] = assignment
	return assignment, nil
}

func newTestManager(t *testing.T, gate AuthorizationGate) (*Manager, *fixedClock) {
	t.Helper()
	clock := &fixedClock{now: time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)}
	manager, err := NewManager(NewService(clock), newFakeRepository(), gate)
	if err != nil {
		t.Fatal(err)
	}
	return manager, clock
}

func TestManagerProposeIsIdempotent(t *testing.T) {
	gate := &recordingGate{}
	manager, _ := newTestManager(t, gate)
	ctx := context.Background()
	command := validDraftCommand(time.Now())
	skill1, version1, reused1, err := manager.Propose(ctx, ProposeRequest{Command: command, IdempotencyKey: "key-1"})
	if err != nil || reused1 {
		t.Fatalf("first propose skill=%+v version=%+v reused=%v err=%v", skill1, version1, reused1, err)
	}
	skill2, version2, reused2, err := manager.Propose(ctx, ProposeRequest{Command: command, IdempotencyKey: "key-1"})
	if err != nil || !reused2 || version2.ID != version1.ID {
		t.Fatalf("second propose skill=%+v version=%+v reused=%v err=%v", skill2, version2, reused2, err)
	}
	if len(gate.proposals) != 2 {
		t.Fatalf("authorization should run on every propose attempt, even idempotent replays, ran %d times", len(gate.proposals))
	}
}

func TestManagerTransitionRejectsStaleRevision(t *testing.T) {
	gate := &recordingGate{}
	manager, clock := newTestManager(t, gate)
	ctx := context.Background()
	_, version, _, err := manager.Propose(ctx, ProposeRequest{Command: validDraftCommand(clock.now), IdempotencyKey: "key-1"})
	if err != nil {
		t.Fatal(err)
	}
	clock.now = clock.now.Add(time.Minute)
	_, err = manager.HumanApprove(ctx, LifecycleMutationRequest{OrganizationID: version.OrganizationID, VersionID: version.ID, ExpectedRevision: 99, ActorRoleID: "empresa/human"}, ApprovalEvidence{DecisionRef: "authz:1", ApprovedBy: "empresa/human", ApprovedAt: clock.now})
	if !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale revision err=%v", err)
	}
}

func TestManagerLifecycleAndAssignmentFlow(t *testing.T) {
	gate := &recordingGate{}
	manager, clock := newTestManager(t, gate)
	ctx := context.Background()
	_, version, _, err := manager.Propose(ctx, ProposeRequest{Command: validDraftCommand(clock.now), IdempotencyKey: "key-1"})
	if err != nil {
		t.Fatal(err)
	}
	mutation := func(expected int64) LifecycleMutationRequest {
		return LifecycleMutationRequest{OrganizationID: version.OrganizationID, VersionID: version.ID, ExpectedRevision: expected, ActorRoleID: "empresa/human"}
	}
	clock.now = clock.now.Add(time.Minute)
	version, err = manager.HumanApprove(ctx, mutation(1), ApprovalEvidence{DecisionRef: "authz:1", ApprovedBy: "empresa/human", ApprovedAt: clock.now})
	if err != nil {
		t.Fatal(err)
	}
	clock.now = clock.now.Add(time.Minute)
	version, err = manager.QualifyCandidate(ctx, mutation(2), validValidationEvidence(clock.now, true))
	if err != nil {
		t.Fatal(err)
	}
	clock.now = clock.now.Add(time.Minute)
	version, err = manager.Activate(ctx, mutation(3), ApprovalEvidence{DecisionRef: "authz:2", ApprovedBy: "empresa/human", ApprovedAt: clock.now})
	if err != nil {
		t.Fatal(err)
	}
	if version.Lifecycle != LifecycleActive {
		t.Fatalf("version=%+v", version)
	}
	if len(gate.transitions) != 3 {
		t.Fatalf("expected 3 lifecycle authorizations, got %d", len(gate.transitions))
	}
	if gate.transitions[2] != [2]Lifecycle{LifecycleCandidate, LifecycleActive} {
		t.Fatalf("activation transition not authorized as expected: %+v", gate.transitions[2])
	}

	clock.now = clock.now.Add(time.Minute)
	assignment, reused, err := manager.Assign(ctx, AssignRequest{
		VersionID:      version.ID,
		Command:        AssignCommand{AssignmentID: "assign-1", OrganizationID: version.OrganizationID, RoleID: "ingenieria_ia/frontend", AssignedBy: "empresa/human", AssignmentDecisionRef: "authz:3", CapabilityReviewRef: "role-capreview:1"},
		IdempotencyKey: "assign-key-1",
	})
	if err != nil || reused {
		t.Fatalf("assign assignment=%+v reused=%v err=%v", assignment, reused, err)
	}
	if len(gate.assignments) != 1 || gate.assignments[0] != "assign:"+version.SkillID+":ingenieria_ia/frontend" {
		t.Fatalf("assignment authorization not recorded as expected: %+v", gate.assignments)
	}

	clock.now = clock.now.Add(time.Minute)
	revoked, err := manager.RevokeAssignment(ctx, RevokeRequest{OrganizationID: version.OrganizationID, AssignmentID: assignment.ID, ExpectedRevision: 1, ActorRoleID: "empresa/human", Reason: "role_restructured"})
	if err != nil {
		t.Fatal(err)
	}
	if revoked.Status != AssignmentRevoked || revoked.RevokedAt == nil {
		t.Fatalf("revoked=%+v", revoked)
	}
}

func TestManagerPropagatesAuthorizationDenial(t *testing.T) {
	gate := &recordingGate{err: errors.New("denied")}
	manager, clock := newTestManager(t, gate)
	_, _, _, err := manager.Propose(context.Background(), ProposeRequest{Command: validDraftCommand(clock.now), IdempotencyKey: "key-1"})
	if err == nil {
		t.Fatal("expected authorization denial to propagate")
	}
}
