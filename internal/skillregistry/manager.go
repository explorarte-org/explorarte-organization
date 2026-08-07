package skillregistry

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

const (
	CapabilityPropose  = "organization.propose_skill"
	CapabilityActivate = "organization.activate_skill"
)

type Manager struct {
	domain     *Service
	repository Repository
	gate       AuthorizationGate
}

func NewManager(domain *Service, repository Repository, gate AuthorizationGate) (*Manager, error) {
	if domain == nil {
		return nil, errors.New("skill registry manager requires domain service")
	}
	if repository == nil {
		return nil, errors.New("skill registry manager requires repository")
	}
	if gate == nil {
		return nil, errors.New("skill registry manager requires authorization gate")
	}
	return &Manager{domain: domain, repository: repository, gate: gate}, nil
}

type ProposeRequest struct {
	Command        CreateDraftCommand
	IdempotencyKey string
}

func (m *Manager) Propose(ctx context.Context, request ProposeRequest) (Skill, SkillVersion, bool, error) {
	key := strings.TrimSpace(request.IdempotencyKey)
	if key == "" {
		return Skill{}, SkillVersion{}, false, fmt.Errorf("%w: idempotency_key is required", ErrInvalidVersion)
	}
	skill, version, err := m.domain.CreateDraft(request.Command)
	if err != nil {
		return Skill{}, SkillVersion{}, false, err
	}
	evidence, err := m.gate.AuthorizeProposal(ctx, skill.OrganizationID, skill.CreatedByRole, skill.ID)
	if err != nil {
		return Skill{}, SkillVersion{}, false, err
	}
	return m.repository.CreateSkill(ctx, skill, version, key, evidence)
}

type LifecycleMutationRequest struct {
	OrganizationID   string
	VersionID        string
	ExpectedRevision int64
	ActorRoleID      string
}

func (m *Manager) HumanApprove(ctx context.Context, request LifecycleMutationRequest, approval ApprovalEvidence) (SkillVersion, error) {
	return m.transition(ctx, request, func(v SkillVersion) (SkillVersion, error) {
		return m.domain.HumanApprove(v, approval)
	})
}

func (m *Manager) QualifyCandidate(ctx context.Context, request LifecycleMutationRequest, evidence ValidationEvidence) (SkillVersion, error) {
	return m.transition(ctx, request, func(v SkillVersion) (SkillVersion, error) {
		return m.domain.QualifyCandidate(v, evidence)
	})
}

func (m *Manager) Activate(ctx context.Context, request LifecycleMutationRequest, approval ApprovalEvidence) (SkillVersion, error) {
	return m.transition(ctx, request, func(v SkillVersion) (SkillVersion, error) {
		return m.domain.Activate(v, approval)
	})
}

func (m *Manager) Suspend(ctx context.Context, request LifecycleMutationRequest) (SkillVersion, error) {
	return m.transition(ctx, request, m.domain.Suspend)
}

func (m *Manager) Retire(ctx context.Context, request LifecycleMutationRequest) (SkillVersion, error) {
	return m.transition(ctx, request, m.domain.Retire)
}

func (m *Manager) transition(ctx context.Context, request LifecycleMutationRequest, mutate func(SkillVersion) (SkillVersion, error)) (SkillVersion, error) {
	organizationID := strings.TrimSpace(request.OrganizationID)
	versionID := strings.TrimSpace(request.VersionID)
	actorRoleID := strings.TrimSpace(request.ActorRoleID)
	if organizationID == "" || versionID == "" || actorRoleID == "" {
		return SkillVersion{}, fmt.Errorf("%w: organization_id, version_id, and actor_role_id are required", ErrInvalidVersion)
	}
	if request.ExpectedRevision <= 0 {
		return SkillVersion{}, fmt.Errorf("%w: expected_revision must be positive", ErrInvalidVersion)
	}
	current, err := m.repository.GetVersion(ctx, organizationID, versionID)
	if err != nil {
		return SkillVersion{}, err
	}
	if current.Revision != request.ExpectedRevision {
		return SkillVersion{}, fmt.Errorf("%w: version %s expected revision %d current %d", ErrRevisionConflict, current.ID, request.ExpectedRevision, current.Revision)
	}
	updated, err := mutate(current)
	if err != nil {
		return SkillVersion{}, err
	}
	evidence, err := m.gate.AuthorizeLifecycleChange(ctx, organizationID, actorRoleID, current.SkillID, current.Lifecycle, updated.Lifecycle)
	if err != nil {
		return SkillVersion{}, err
	}
	event := LifecycleEvent{
		OrganizationID: organizationID,
		SkillID:        current.SkillID,
		SkillVersionID: current.ID,
		From:           current.Lifecycle,
		To:             updated.Lifecycle,
		ActorRoleID:    actorRoleID,
		DecisionRef:    evidence.DecisionRef,
		OccurredAt:     updated.UpdatedAt,
	}
	return m.repository.SaveVersion(ctx, updated, request.ExpectedRevision, event)
}

type AssignRequest struct {
	VersionID      string
	Command        AssignCommand
	IdempotencyKey string
}

func (m *Manager) Assign(ctx context.Context, request AssignRequest) (SkillAssignment, bool, error) {
	organizationID := strings.TrimSpace(request.Command.OrganizationID)
	versionID := strings.TrimSpace(request.VersionID)
	key := strings.TrimSpace(request.IdempotencyKey)
	if organizationID == "" || versionID == "" || key == "" {
		return SkillAssignment{}, false, fmt.Errorf("%w: organization_id, version_id, and idempotency_key are required", ErrInvalidAssignment)
	}
	version, err := m.repository.GetVersion(ctx, organizationID, versionID)
	if err != nil {
		return SkillAssignment{}, false, err
	}
	assignment, err := m.domain.Assign(version, request.Command)
	if err != nil {
		return SkillAssignment{}, false, err
	}
	evidence, err := m.gate.AuthorizeAssignmentChange(ctx, organizationID, assignment.AssignedBy, assignment.RoleID, assignment.SkillID, "assign")
	if err != nil {
		return SkillAssignment{}, false, err
	}
	event := AssignmentEvent{
		OrganizationID: organizationID,
		AssignmentID:   assignment.ID,
		SkillID:        assignment.SkillID,
		SkillVersionID: assignment.SkillVersionID,
		RoleID:         assignment.RoleID,
		Action:         "assign",
		ActorRoleID:    assignment.AssignedBy,
		DecisionRef:    evidence.DecisionRef,
		OccurredAt:     assignment.AssignedAt,
	}
	return m.repository.CreateAssignment(ctx, assignment, key, event)
}

type RevokeRequest struct {
	OrganizationID   string
	AssignmentID     string
	ExpectedRevision int64
	ActorRoleID      string
	Reason           string
}

func (m *Manager) RevokeAssignment(ctx context.Context, request RevokeRequest) (SkillAssignment, error) {
	organizationID := strings.TrimSpace(request.OrganizationID)
	assignmentID := strings.TrimSpace(request.AssignmentID)
	actorRoleID := strings.TrimSpace(request.ActorRoleID)
	if organizationID == "" || assignmentID == "" || actorRoleID == "" {
		return SkillAssignment{}, fmt.Errorf("%w: organization_id, assignment_id, and actor_role_id are required", ErrInvalidAssignment)
	}
	if request.ExpectedRevision <= 0 {
		return SkillAssignment{}, fmt.Errorf("%w: expected_revision must be positive", ErrInvalidAssignment)
	}
	current, err := m.repository.GetAssignment(ctx, organizationID, assignmentID)
	if err != nil {
		return SkillAssignment{}, err
	}
	if current.Revision != request.ExpectedRevision {
		return SkillAssignment{}, fmt.Errorf("%w: assignment %s expected revision %d current %d", ErrRevisionConflict, current.ID, request.ExpectedRevision, current.Revision)
	}
	updated, err := m.domain.RevokeAssignment(current, request.Reason)
	if err != nil {
		return SkillAssignment{}, err
	}
	evidence, err := m.gate.AuthorizeAssignmentChange(ctx, organizationID, actorRoleID, current.RoleID, current.SkillID, "revoke")
	if err != nil {
		return SkillAssignment{}, err
	}
	event := AssignmentEvent{
		OrganizationID: organizationID,
		AssignmentID:   updated.ID,
		SkillID:        updated.SkillID,
		SkillVersionID: updated.SkillVersionID,
		RoleID:         updated.RoleID,
		Action:         "revoke",
		ActorRoleID:    actorRoleID,
		DecisionRef:    evidence.DecisionRef,
		ReasonCode:     updated.RevokeReason,
		OccurredAt:     *updated.RevokedAt,
	}
	return m.repository.SaveAssignment(ctx, updated, request.ExpectedRevision, event)
}

func (m *Manager) GetSkill(ctx context.Context, organizationID, skillID string) (Skill, error) {
	organizationID, skillID = strings.TrimSpace(organizationID), strings.TrimSpace(skillID)
	if organizationID == "" || skillID == "" {
		return Skill{}, fmt.Errorf("%w: organization_id and skill_id are required", ErrInvalidSkill)
	}
	return m.repository.GetSkill(ctx, organizationID, skillID)
}

func (m *Manager) GetVersion(ctx context.Context, organizationID, versionID string) (SkillVersion, error) {
	organizationID, versionID = strings.TrimSpace(organizationID), strings.TrimSpace(versionID)
	if organizationID == "" || versionID == "" {
		return SkillVersion{}, fmt.Errorf("%w: organization_id and version_id are required", ErrInvalidVersion)
	}
	return m.repository.GetVersion(ctx, organizationID, versionID)
}

func (m *Manager) ListVersions(ctx context.Context, organizationID, skillID string) ([]SkillVersion, error) {
	organizationID, skillID = strings.TrimSpace(organizationID), strings.TrimSpace(skillID)
	if organizationID == "" || skillID == "" {
		return nil, fmt.Errorf("%w: organization_id and skill_id are required", ErrInvalidVersion)
	}
	return m.repository.ListVersions(ctx, organizationID, skillID)
}

func (m *Manager) GetAssignment(ctx context.Context, organizationID, assignmentID string) (SkillAssignment, error) {
	organizationID, assignmentID = strings.TrimSpace(organizationID), strings.TrimSpace(assignmentID)
	if organizationID == "" || assignmentID == "" {
		return SkillAssignment{}, fmt.Errorf("%w: organization_id and assignment_id are required", ErrInvalidAssignment)
	}
	return m.repository.GetAssignment(ctx, organizationID, assignmentID)
}

func (m *Manager) ListActiveAssignmentsForRole(ctx context.Context, organizationID, roleID string) ([]SkillAssignment, error) {
	organizationID, roleID = strings.TrimSpace(organizationID), strings.TrimSpace(roleID)
	if organizationID == "" || roleID == "" {
		return nil, fmt.Errorf("%w: organization_id and role_id are required", ErrInvalidAssignment)
	}
	return m.repository.ListActiveAssignmentsForRole(ctx, organizationID, roleID)
}
