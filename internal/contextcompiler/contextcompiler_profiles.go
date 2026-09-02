package contextcompiler

import "github.com/Mireuz13/explorarte-organization/internal/contextengine"

// ResearchCorpusCurateV1TaskClass is the exact TaskClass this profile
// registers at the TASK-CLASS selector tier (M1.3). R10 V1 is scoped
// narrowly per R10_DESIGN_AUDIT.md section 54: only this one task class,
// nothing else (executive.ceo, department.leader, code-runner, QA, visual
// agents are all explicitly untouched -- Compile falls back to the
// canonical snapshot unchanged for any selector this doesn't match).
const ResearchCorpusCurateV1TaskClass = "research.corpus_curate"

// researchWorkerHourlyRoleID is the exact role this profile remains
// applicable to (M1.3 section 11/12): TaskClass=research.corpus_curate
// alone is never sufficient --
// an unrelated role/unit proposing this TaskClass must still
// canonical-fallback, exactly as it did before M1.3 (when the ActorRoleID
// proxy was the only thing gating this profile at all).
const (
	researchWorkerHourlyRoleID = "investigacion/research_worker_hourly"
	researchUnitID             = "investigacion"
)

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

// defaultSelectorRegistry is the ONE place ContextProfiles are looked up
// by semantic selector (M1.3 replaces the old Registry()[TaskClassOf(...)]
// with this). A selector with no matching, applicable entry always falls
// back to the canonical snapshot unmodified (Compile), never to an
// arbitrarily minimal view -- see R10_DESIGN_AUDIT.md section M/41 and
// SelectorRegistry.Select.
var defaultSelectorRegistry = MustBuildSelectorRegistry(
	[]ProfileEntry{
		{
			Profile: ResearchCorpusCurateV1(),
			// Both axes restricted (AND-ed): TaskClass=research.corpus_curate
			// proposed for an unrelated role or a role outside the
			// investigacion unit must still canonical-fallback (M1.3
			// section 11/12/13).
			ApplicableActorRoleIDs: []string{researchWorkerHourlyRoleID},
			ApplicableActorUnitIDs: []string{researchUnitID},
		},
	},
	nil, // no EXECUTION-PURPOSE-tier profile registered yet (M1.3 V1)
	nil, // no EXACT-tier profile registered yet (M1.3 V1)
)
