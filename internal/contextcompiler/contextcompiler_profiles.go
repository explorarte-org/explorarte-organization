package contextcompiler

import "github.com/Mireuz13/explorarte-organization/internal/contextengine"

// ResearchCorpusCurateV1TaskClass is the exact purpose/task-class string
// this profile matches -- see R9/R9.1's driver, CONTEXT_POLICY_PURPOSE =
// "department_worker". R10 V1 is scoped narrowly per
// R10_DESIGN_AUDIT.md section 54: only this one task class, nothing else
// (executive.ceo, department.leader, code-runner, QA, visual agents are
// all explicitly untouched -- Compile falls back to the canonical
// snapshot unchanged for any TaskClass that isn't this one).
const ResearchCorpusCurateV1TaskClass = "research.corpus_curate"

// ResearchCorpusCurateV1 is the ONE profile R10 V1 implements, built
// from the real segment composition measured across r9/r9.1 (see
// R10_DESIGN_AUDIT.md sections B/D): every tier this task class actually
// receives is required (no safe scope metadata exists yet to exclude
// any of the policy tiers -- section G, fail closed toward authority),
// and the sole projection is role-catalog.yaml -> the actor's own
// entry.
func ResearchCorpusCurateV1() ContextProfile {
	return ContextProfile{
		ID:        "research.corpus_curate",
		Version:   "v1",
		TaskClass: ResearchCorpusCurateV1TaskClass,
		RequiredTiers: []contextengine.AuthorityTier{
			contextengine.TierImmutableSafety,
			contextengine.TierOwnerDecisions,
			contextengine.TierCanonicalPolicies,
			contextengine.TierOrganizationAgent,
			contextengine.TierDepartmentAgent,
			contextengine.TierRoleProfile,
			contextengine.TierTask,
		},
		Projections: map[string]ProjectionFunc{
			RoleCatalogSourceReference: RoleCatalogSelfEntry,
		},
	}
}

// Registry is the only place ContextProfiles are looked up by TaskClass.
// A task class with no entry here always falls back to the canonical
// snapshot unmodified (Compile), never to an arbitrarily minimal view --
// see R10_DESIGN_AUDIT.md section M/41.
func Registry() map[string]ContextProfile {
	profile := ResearchCorpusCurateV1()
	return map[string]ContextProfile{profile.TaskClass: profile}
}
