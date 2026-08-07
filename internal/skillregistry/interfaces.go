package skillregistry

import (
	"context"
	"time"
)

type Clock interface{ Now() time.Time }

type Repository interface {
	CreateSkill(context.Context, Skill, SkillVersion, string, GovernanceEvidence) (Skill, SkillVersion, bool, error)
	GetSkill(context.Context, string, string) (Skill, error)
	GetVersion(context.Context, string, string) (SkillVersion, error)
	ListVersions(context.Context, string, string) ([]SkillVersion, error)
	SaveVersion(context.Context, SkillVersion, int64, LifecycleEvent) (SkillVersion, error)
	CreateAssignment(context.Context, SkillAssignment, string, AssignmentEvent) (SkillAssignment, bool, error)
	GetAssignment(context.Context, string, string) (SkillAssignment, error)
	ListActiveAssignmentsForRole(context.Context, string, string) ([]SkillAssignment, error)
	SaveAssignment(context.Context, SkillAssignment, int64, AssignmentEvent) (SkillAssignment, error)
}

type GovernanceEvidence struct {
	DecisionRef string
	ActorRoleID string
	DecidedAt   time.Time
}

type AuthorizationGate interface {
	AuthorizeProposal(context.Context, string, string, string) (GovernanceEvidence, error)
	AuthorizeLifecycleChange(context.Context, string, string, string, Lifecycle, Lifecycle) (GovernanceEvidence, error)
	AuthorizeAssignmentChange(context.Context, string, string, string, string, string) (GovernanceEvidence, error)
}

type SkillSchemaValidator interface {
	ValidateSkillSource(context.Context, string, SourceRecord, Manifest) (validationRef string, pass bool, err error)
}

type RoleCapabilityReviewer interface {
	ReviewRoleCapabilities(context.Context, string, string, []string) (reviewRef string, pass bool, err error)
}

type InstructionSafetyReviewer interface {
	ReviewSkillInstructions(context.Context, string, SourceRecord, Manifest) (reviewRef string, pass bool, err error)
}
