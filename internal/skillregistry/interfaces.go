package skillregistry

import (
	"context"
	"time"
)

type Clock interface{ Now() time.Time }

type Repository interface {
	CreateSkill(context.Context, Skill, SkillVersion, string) (Skill, SkillVersion, bool, error)
	GetSkill(context.Context, string, string) (Skill, error)
	GetVersion(context.Context, string, string) (SkillVersion, error)
	ListVersions(context.Context, string, string) ([]SkillVersion, error)
	SaveVersion(context.Context, SkillVersion, int64) (SkillVersion, error)
	CreateAssignment(context.Context, SkillAssignment, string) (SkillAssignment, bool, error)
	GetAssignment(context.Context, string, string) (SkillAssignment, error)
	ListActiveAssignmentsForRole(context.Context, string, string) ([]SkillAssignment, error)
	SaveAssignment(context.Context, SkillAssignment, int64) (SkillAssignment, error)
}

type AuthorizationGate interface {
	AuthorizeProposal(context.Context, string, string, string) error
	AuthorizeLifecycleChange(context.Context, string, string, string, Lifecycle, Lifecycle) error
	AuthorizeAssignment(context.Context, string, string, string, string) error
}

type RoleCapabilityReviewer interface {
	ReviewRoleCapabilities(context.Context, string, string, []string) (reviewRef string, pass bool, err error)
}
