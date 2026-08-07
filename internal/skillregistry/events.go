package skillregistry

import "time"

type LifecycleEvent struct {
	OrganizationID string    `json:"organization_id"`
	SkillID        string    `json:"skill_id"`
	SkillVersionID string    `json:"skill_version_id"`
	From           Lifecycle `json:"from"`
	To             Lifecycle `json:"to"`
	ActorRoleID    string    `json:"actor_role_id"`
	DecisionRef    string    `json:"decision_ref"`
	OccurredAt     time.Time `json:"occurred_at"`
}

type AssignmentEvent struct {
	OrganizationID string    `json:"organization_id"`
	AssignmentID   string    `json:"assignment_id"`
	SkillID        string    `json:"skill_id"`
	SkillVersionID string    `json:"skill_version_id"`
	RoleID         string    `json:"role_id"`
	Action         string    `json:"action"`
	ActorRoleID    string    `json:"actor_role_id"`
	DecisionRef    string    `json:"decision_ref"`
	ReasonCode     string    `json:"reason_code,omitempty"`
	OccurredAt     time.Time `json:"occurred_at"`
}
