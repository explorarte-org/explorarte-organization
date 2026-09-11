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

// Source references for the two canonical documents the executive
// EXECUTION-PURPOSE profiles below selectively exclude. Named the same
// way RoleCatalogSourceReference already is (docs/canonical/<file>),
// against the exact LogicalName internal/contextengine/canonical/provider.go
// registers them under.
const (
	decisionsRequiredSourceReference = "docs/canonical/decisions-required.yaml"
	modelRoutingSourceReference      = "docs/canonical/model-routing.yaml"
	leaderWorkerMapSourceReference   = "docs/canonical/leader-worker-map.yaml"
)

// executiveRequiredTiersBase is the tier floor every executive
// EXECUTION-PURPOSE profile below shares (EXECUTIVE_CONTEXT_PROFILE_SCOPING_FIX_V1):
// TierImmutableSafety and TierCanonicalPolicies are non-negotiable for
// every one of them (cell-boundaries.yaml, instruction-precedence.yaml,
// and the rest of TierCanonicalPolicies all remain fully included --
// only role-catalog.yaml is ever projected, and only decisions-required.yaml/
// model-routing.yaml are ever excluded, both via source-level exclusion,
// never by dropping the tier). TierOrganizationAgent/TierDepartmentAgent/
// TierRoleProfile/TierTask carry the actor's own AGENT.md/PERFIL.md/task
// content -- every purpose needs its own identity and its own work.
func executiveRequiredTiersBase() []contextengine.AuthorityTier {
	return []contextengine.AuthorityTier{
		contextengine.TierImmutableSafety,
		contextengine.TierCanonicalPolicies,
		contextengine.TierOrganizationAgent,
		contextengine.TierDepartmentAgent,
		contextengine.TierRoleProfile,
		contextengine.TierTask,
	}
}

// executiveRequiredTiersWithOwnerDecisions is executiveRequiredTiersBase
// plus TierOwnerDecisions, for the two purposes whose own output schema
// carries an owner-decision field (owner_decisions_required /
// blocked_items+unresolved_decisions) and therefore must never lose
// access to decisions-required.yaml: executive_ceo_plan and
// executive_ceo_closure. decisionApplicabilityPolicy (internal/executive)
// remains the semantic defense against reactivating an inapplicable
// decision -- this tier requirement only guarantees the source stays
// physically present for that policy to act on; it changes nothing about
// how the model is instructed to interpret it.
func executiveRequiredTiersWithOwnerDecisions() []contextengine.AuthorityTier {
	return append(executiveRequiredTiersBase(), contextengine.TierOwnerDecisions)
}

// ExecutiveCEOPlanV1TaskClass is the ExecutionPurpose string this profile
// registers at the EXECUTION-PURPOSE selector tier -- the exact value
// internal/executive.PurposeCEOPlan carries as ExecutionPurpose("ceo-plan"),
// duplicated here as a plain string (never imported) to keep
// contextcompiler free of any dependency on internal/executive.
const ExecutiveCEOPlanV1ExecutionPurpose = "ceo-plan"

// ExecutiveCEOPlanV1 keeps every canonical source CEO-plan already
// received (organization.yaml, capability-matrix.yaml, leader-worker-map.yaml,
// decisions-required.yaml, and the conservatively-kept
// reasoning-assurance.yaml/memory-policy.yaml/architecture-characteristics.yaml/
// model-routing.yaml -- none of these are excluded in V1) and projects
// ONLY role-catalog.yaml, down to the CEO's own entry plus every
// canonical_leader:true entry: the department leaders a decomposition
// must be able to name, without the ~150 individual worker identities no
// decomposition ever addresses directly.
func ExecutiveCEOPlanV1() ContextProfile {
	return ContextProfile{
		ID:               "executive.ceo_plan",
		Version:          "v1",
		ExecutionPurpose: ExecutiveCEOPlanV1ExecutionPurpose,
		RequiredTiers:    executiveRequiredTiersWithOwnerDecisions(),
		Projections: map[string]ProjectionFunc{
			RoleCatalogSourceReference: RoleCatalogSelfAndDepartmentLeaders,
		},
	}
}

// ExecutiveCEOClosureV1ExecutionPurpose is internal/executive.PurposeCEOClosure's
// ExecutionPurpose("ceo-closure") value, duplicated as a plain string for
// the same reason as ExecutiveCEOPlanV1ExecutionPurpose.
const ExecutiveCEOClosureV1ExecutionPurpose = "ceo-closure"

// ExecutiveCEOClosureV1 excludes model-routing.yaml (provider/model/profile
// are already resolved host-side by the time closure runs -- closure
// reports on a campaign, it never selects a provider) and projects
// role-catalog.yaml down to the CEO's own entry only, via the SAME
// RoleCatalogSelfEntry research.corpus_curate/v1 already uses -- closure
// summarizes department verdicts it already has as durable evidence, not
// role identities it must look up. capability-matrix.yaml,
// decisions-required.yaml, and every other canonical source are kept in
// full in V1 (conservative: "if in doubt, keep it").
func ExecutiveCEOClosureV1() ContextProfile {
	return ContextProfile{
		ID:               "executive.ceo_closure",
		Version:          "v1",
		ExecutionPurpose: ExecutiveCEOClosureV1ExecutionPurpose,
		RequiredTiers:    executiveRequiredTiersWithOwnerDecisions(),
		Projections: map[string]ProjectionFunc{
			RoleCatalogSourceReference: RoleCatalogSelfEntry,
		},
		ExcludedSources: map[string]bool{
			modelRoutingSourceReference: true,
		},
	}
}

// ExecutiveDepartmentPlanV1ExecutionPurpose is
// internal/executive.PurposeDepartmentPlan's ExecutionPurpose("department-plan").
const ExecutiveDepartmentPlanV1ExecutionPurpose = "department-plan"

// ExecutiveDepartmentPlanV1 excludes decisions-required.yaml (a department
// plan's output schema has no owner-decision field at all -- there is
// nothing in its contract to report one into) and model-routing.yaml
// (routing is resolved host-side), and projects role-catalog.yaml down to
// every role sharing the department leader's own department field --
// its own leader and every worker of that one unit, read off
// role-catalog.yaml's own `department` field, never another
// department's roster.
func ExecutiveDepartmentPlanV1() ContextProfile {
	return ContextProfile{
		ID:               "executive.department_plan",
		Version:          "v1",
		ExecutionPurpose: ExecutiveDepartmentPlanV1ExecutionPurpose,
		RequiredTiers:    executiveRequiredTiersBase(),
		Projections: map[string]ProjectionFunc{
			RoleCatalogSourceReference: RoleCatalogOwnDepartment,
		},
		ExcludedSources: map[string]bool{
			decisionsRequiredSourceReference: true,
			modelRoutingSourceReference:      true,
		},
	}
}

// ExecutiveDepartmentWorkerV1ExecutionPurpose is
// internal/executive.PurposeDepartmentWorker's ExecutionPurpose("department-worker").
const ExecutiveDepartmentWorkerV1ExecutionPurpose = "department-worker"

// ExecutiveDepartmentWorkerV1 excludes decisions-required.yaml and
// model-routing.yaml (same reasons as department-plan) plus
// leader-worker-map.yaml in full (a worker executes one already-assigned
// task; it does not decompose or route across the department roster the
// way its leader does), and projects role-catalog.yaml down to the
// worker's own entry plus its own department's canonical_leader entry --
// extending RoleCatalogSelfEntry's exact self-lookup rather than
// replacing it. organization.yaml, capability-matrix.yaml,
// reasoning-assurance.yaml, memory-policy.yaml and
// architecture-characteristics.yaml stay in full in V1: none of their
// necessity for a worker has been demonstrated false.
func ExecutiveDepartmentWorkerV1() ContextProfile {
	return ContextProfile{
		ID:               "executive.department_worker",
		Version:          "v1",
		ExecutionPurpose: ExecutiveDepartmentWorkerV1ExecutionPurpose,
		RequiredTiers:    executiveRequiredTiersBase(),
		Projections: map[string]ProjectionFunc{
			RoleCatalogSourceReference: RoleCatalogSelfAndOwnLeaderEntries,
		},
		ExcludedSources: map[string]bool{
			decisionsRequiredSourceReference: true,
			modelRoutingSourceReference:      true,
			leaderWorkerMapSourceReference:   true,
		},
	}
}

// ExecutiveDepartmentReviewV1ExecutionPurpose is
// internal/executive.PurposeDepartmentReview's ExecutionPurpose("department-review").
const ExecutiveDepartmentReviewV1ExecutionPurpose = "department-review"

// ExecutiveDepartmentReviewV1 excludes decisions-required.yaml and
// model-routing.yaml (same reasons as department-plan/worker) and
// projects role-catalog.yaml to the reviewer's own department --
// RoleCatalogOwnDepartment, the same conservative unit-scoped projection
// department-plan uses, per the round's own explicit fallback guidance
// for review ("si eso exige dataflow nuevo demasiado grande: usar todos
// los roles del ActorUnitID actual"). capability-matrix.yaml is kept in
// full: a review may need to judge whether a worker's deliverable stayed
// within its granted capability.
func ExecutiveDepartmentReviewV1() ContextProfile {
	return ContextProfile{
		ID:               "executive.department_review",
		Version:          "v1",
		ExecutionPurpose: ExecutiveDepartmentReviewV1ExecutionPurpose,
		RequiredTiers:    executiveRequiredTiersBase(),
		Projections: map[string]ProjectionFunc{
			RoleCatalogSourceReference: RoleCatalogOwnDepartment,
		},
		ExcludedSources: map[string]bool{
			decisionsRequiredSourceReference: true,
			modelRoutingSourceReference:      true,
		},
	}
}

// explicitFullContextProfile registers a purpose at the EXECUTION-PURPOSE
// tier with NO exclusions and NO projections at all -- materially
// identical to the canonical-fallback view it replaces, but now with
// Matched=true/SelectionKind=SelectionExecutionPurpose/
// FellBackToCanonical=false instead of an implicit fallback. Used for the
// three known purposes this round deliberately does not optimize
// (adversarial-review, design-adjudication, implementation-plan): each
// keeps the full canonical bundle, but the choice to do so is now
// explicit and provenance-recorded rather than an accident of no profile
// existing.
func explicitFullContextProfile(id, executionPurpose string) ContextProfile {
	return ContextProfile{
		ID:               id,
		Version:          "v1",
		ExecutionPurpose: executionPurpose,
		RequiredTiers:    executiveRequiredTiersWithOwnerDecisions(),
	}
}

// ExecutiveAdversarialReviewV1ExecutionPurpose is
// internal/executive.PurposeAdversarialReview's ExecutionPurpose("adversarial-review").
const ExecutiveAdversarialReviewV1ExecutionPurpose = "adversarial-review"

// ExecutiveDesignAdjudicationV1ExecutionPurpose is
// internal/executive.PurposeDesignAdjudication's ExecutionPurpose("design-adjudication").
const ExecutiveDesignAdjudicationV1ExecutionPurpose = "design-adjudication"

// ExecutiveImplementationPlanV1ExecutionPurpose is
// internal/executive.PurposeImplementationPlan's ExecutionPurpose("implementation-plan").
const ExecutiveImplementationPlanV1ExecutionPurpose = "implementation-plan"

// defaultSelectorRegistry is the ONE place ContextProfiles are looked up
// by semantic selector (M1.3 replaces the old Registry()[TaskClassOf(...)]
// with this). A selector with no matching, applicable entry always falls
// back to the canonical snapshot unmodified (Compile), never to an
// arbitrarily minimal view -- see R10_DESIGN_AUDIT.md section M/41 and
// SelectorRegistry.Select.
//
// EXECUTIVE_CONTEXT_PROFILE_SCOPING_FIX_V1 populates the EXECUTION-PURPOSE
// tier (previously nil) with one entry per known executive
// ExecutionPurpose, unrestricted on ActorRoleID/ActorUnitID (each of
// these 8 purposes is a closed, host-validated enum on its own -- see
// internal/executive.ExecutionPurpose.Valid -- so no further applicability
// axis is needed the way research.corpus_curate needed one against an
// open-ended TaskClass). A genuinely unknown/synthetic ExecutionPurpose
// still falls through to SelectionCanonical exactly as before.
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
	[]ProfileEntry{
		{Profile: ExecutiveCEOPlanV1()},
		{Profile: ExecutiveCEOClosureV1()},
		{Profile: ExecutiveDepartmentPlanV1()},
		{Profile: ExecutiveDepartmentWorkerV1()},
		{Profile: ExecutiveDepartmentReviewV1()},
		{Profile: explicitFullContextProfile("executive.adversarial_review", ExecutiveAdversarialReviewV1ExecutionPurpose)},
		{Profile: explicitFullContextProfile("executive.design_adjudication", ExecutiveDesignAdjudicationV1ExecutionPurpose)},
		{Profile: explicitFullContextProfile("executive.implementation_plan", ExecutiveImplementationPlanV1ExecutionPurpose)},
	},
	nil, // no EXACT-tier profile registered yet (M1.3 V1)
)
