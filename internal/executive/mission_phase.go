package executive

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/designfreeze"
	"github.com/Mireuz13/explorarte-organization/internal/engineeringmission"
	"github.com/Mireuz13/explorarte-organization/internal/missionplan"
	"github.com/Mireuz13/explorarte-organization/internal/repositoryevidence"
)

// The implementation-mission phase is the last step the Executive performs
// before engineering owns the work. It runs only after a design freeze, and it
// ends the moment a governed mission exists: the Executive can ask for a
// mission, and holds no way to execute, review, approve, promote or apply one.
//
// It is opt-in per run, like the freeze phase, so a run that only wanted a
// design decision never provisions anything.

const (
	// MissionRequirementKey governs whether this phase runs at all and is
	// satisfied when the mission exists.
	MissionRequirementKey = "implementation-mission"
	// InternalCodeScopeRequirementKey is how an owner widens a run from
	// documentation to code. Its ABSENCE is the safe default: a run that says
	// nothing about scope gets the narrowest one.
	InternalCodeScopeRequirementKey = "mission-scope-internal-code"

	TaskClassCoordinationImplementationPlan = "coordination.implementation_plan"

	ReasonImplementationPlanUnavailable  = "implementation_plan_unavailable"
	ReasonMissionProvisioningUnavailable = "mission_provisioning_unavailable"
	ReasonMissionPolicyRejected          = "mission_policy_rejected"
	ReasonMissionRejected                = "mission_rejected"
)

// ProgramTargetResolver reports the exact commit the program's promotion
// target currently points at. It is a port rather than a git call so the
// Executive never shells out, and so the value is whatever the deployment says
// it is rather than whatever the working tree happens to be.
type ProgramTargetResolver interface {
	ResolveProgramTargetSHA(context.Context) (string, error)
}

// MissionProvisioner creates a governed engineering mission from an
// already-derived policy and an already-validated plan.
//
// The narrowness is the point. This is the Executive's ENTIRE engineering
// surface: one method that brings a mission into existence. There is no
// promote, no approve, no apply, and no way to widen a policy after the fact,
// so the Executive cannot reach past the boundary even by mistake.
type MissionProvisioner interface {
	ProvisionMission(context.Context, MissionProvisionCommand) (MissionRecord, error)
}

type MissionProvisionCommand struct {
	Policy            engineeringmission.MissionPolicy
	PlanJSON          []byte
	RequestedByRoleID string
	ActorType         string
	ActorID           string
	// CorrelationID and CausationID bind the mission to the campaign that
	// provisioned it. The program budget ceiling is enforced by
	// correlation at reservation time, so a mission created without one
	// spends outside the budget that authorised its existence -- and any
	// later attempt to recover it has no ceiling to admit it against.
	//
	// They are carried here, at creation, rather than reconstructed
	// afterwards from "the probable root": a budget association derived by
	// search is a guess about authority, and authority is not something to
	// guess at.
	CorrelationID string
	CausationID   string
}

type MissionRecord struct {
	TaskID int64
}

// WithMissionProvisioning enables the phase. A run whose root carries the
// mission requirement while this is unset blocks rather than proceeding: the
// alternative is a run that silently ends at a freeze while claiming it would
// implement something.
// WithSnapshotSources lets the host verify that a repository citation was
// really in front of the model that made it.
//
// Without it, citations are never verified and the review bundle authorizes
// none: a design may still claim things about code, and the reviewer correctly
// treats every such claim as ungrounded. Failing that way round is deliberate
// -- the alternative is authorizing citations nobody checked.
func WithSnapshotSources(reader SnapshotSourceReader) OrchestratorOption {
	return func(o *Orchestrator) { o.snapshotSources = reader }
}

// WithRepositoryEvidenceSource hands the orchestrator the same repository
// sensor the context builder uses, so adjudicated obligations can be probed
// for supplyability against the pinned tree before they bind a round.
//
// Optional: without it proposals are adopted unprobed, and the round's own
// preflight stays the last line of defence -- later and more expensive, but
// equally fail-closed.
func WithRepositoryEvidenceSource(repositoryID string, source repositoryevidence.Source) OrchestratorOption {
	return func(o *Orchestrator) {
		o.repositoryID = repositoryID
		o.repositorySource = source
	}
}

func WithMissionProvisioning(target ProgramTargetResolver, provisioner MissionProvisioner) OrchestratorOption {
	return func(o *Orchestrator) {
		o.programTarget = target
		o.missions = provisioner
	}
}

func (o *Orchestrator) driveImplementationMission(ctx context.Context, root TaskRecord, all []TaskRecord) (Run, bool, error) {
	requirement, found := findRequirementByKey(root.Requirements, MissionRequirementKey)
	if !found || requirement.Status == "satisfied" {
		return Run{}, false, nil
	}
	// A mission may never precede the decision that authorized it.
	freeze, frozen := findRequirementByKey(root.Requirements, designfreeze.RequirementKey)
	if !frozen || freeze.Status != "satisfied" {
		run, blockErr := o.blockRoot(ctx, root, "design_not_frozen",
			"an implementation mission requires a satisfied design freeze")
		return run, true, blockErr
	}
	if o.missions == nil || o.programTarget == nil {
		run, blockErr := o.blockRoot(ctx, root, ReasonMissionProvisioningUnavailable,
			"this Executive is not configured to provision engineering missions")
		return run, true, blockErr
	}

	leader, err := o.registry.GetLeader(ctx, root.AssignedUnitID)
	if err != nil || !leader.Enabled || !leader.Executable {
		leader, err = o.implementationLeader(ctx, all)
		if err != nil {
			run, blockErr := o.blockRoot(ctx, root, ReasonImplementationPlanUnavailable, err.Error())
			return run, true, blockErr
		}
	}

	planTask, ok := findTaskByKey(all, childKey(root.ID, "implementation-plan"))
	if !ok {
		planTask, _, err = o.tasks.CreateTask(ctx, CreateTaskCommand{
			RequestedByRoleID: CEORoleID, AssignedRoleID: leader.ID,
			TaskClass:      TaskClassCoordinationImplementationPlan,
			IdempotencyKey: childKey(root.ID, "implementation-plan"),
			Title:          "Implementation plan for frozen design",
			Instructions: "The design is frozen. Produce ImplementationPlan JSON: the objective, the exact " +
				"repository-relative files to change, a unified diff for each, and what verification is expected. " +
				"Naming a path is a request, not a grant -- the host decides which paths are permitted, which gates " +
				"must pass, and which commit the work is based on.\n\nOWNER GOAL:\n" + root.Instructions,
			AcceptanceCriteria: []string{
				"Return strict ImplementationPlan JSON",
				"Every change carries a real unified diff",
				"Do not declare allowed paths, gates, budgets or approvals",
			},
			Priority: 96, MaxAttempts: 3, CorrelationID: root.CorrelationID, CausationID: taskCausation(root.ID),
			Requirements: []RequirementProposal{{Key: "typed_implementation_plan", Type: "result", Description: "Validated ImplementationPlan invocation result", Required: true}},
		})
		if err != nil {
			return Run{}, true, err
		}
	}
	if planTask.Status != "completed" {
		if _, err = o.driveTypedTask(ctx, root, planTask, ImplementationPlanOutputSchema(), PurposeImplementationPlan, func(result InvocationResult) error {
			_, parseErr := ParseImplementationPlan(result.JSONOutput, o.limits)
			return parseErr
		}); err != nil {
			run, phaseErr := o.handlePhaseError(ctx, root, planTask, err)
			return run, true, phaseErr
		}
		run, statusErr := o.Status(ctx, root.ID)
		return run, true, statusErr
	}
	planResult, ok := o.resultForCompletedTask(ctx, planTask)
	if !ok {
		run, blockErr := o.blockRoot(ctx, root, "implementation_plan_result_missing",
			"completed implementation plan has no durable invocation result")
		return run, true, blockErr
	}
	plan, err := ParseImplementationPlan(planResult.JSONOutput, o.limits)
	if err != nil {
		run, blockErr := o.blockRoot(ctx, root, "implementation_plan_invalid", err.Error())
		return run, true, blockErr
	}

	// The base is the commit the DESIGN was decided about, not whatever the
	// target points at now.
	//
	// Reading the current head here was the silent retarget: a design whose
	// evidence cited S0, whose reviewer read S0 and whose adjudicator ruled on
	// S0 would be implemented against S1, and the substitution left no trace
	// anywhere. It would have been right often enough to be trusted.
	baseSHA, err := o.frozenDesignBaseSHA(ctx, root)
	if err != nil {
		return Run{}, true, err
	}

	// The target moving between freeze and provisioning is a concurrency
	// event about the world, not a defect in the design. During bootstrap it
	// fails closed: the alternative is either implementing a decision against
	// a repository nobody reviewed, or quietly re-deciding it, and both are
	// worse than stopping with the fact recorded.
	//
	// Making this cheap -- revalidating only the surface the design actually
	// relied on, and recovering by successor when that surface is untouched --
	// is deliberately left to the organization.
	current, err := o.programTarget.ResolveProgramTargetSHA(ctx)
	if err != nil {
		return Run{}, true, err
	}
	if strings.TrimSpace(current) != baseSHA {
		// DURABLE-EVIDENCE-PROOF-CONTRACT: a proof minted under baseSHA
		// attests exactly that SHA's content -- once the world has moved,
		// it must never again read as "already delivered" for a future
		// round pinned to a different SHA. Invalidated in the SAME pass
		// that blocks the run on this exact fact, not a separate sweep
		// that could race or be skipped. Best-effort: a failure here must
		// never stop the block itself from taking effect -- fail-closed
		// blocking the run is the load-bearing action; tombstoning stale
		// proofs is a durability cleanup on top of it.
		if o.evidenceProofs != nil {
			_ = o.evidenceProofs.InvalidateProofs(ctx, root.ID, strings.TrimSpace(current))
		}
		run, blockErr := o.blockRoot(ctx, root, ReasonWorldChangedSinceFreeze,
			fmt.Sprintf("the design was decided about %s and the promotion target is now %s; a decision about one repository is not a decision about another",
				baseSHA, strings.TrimSpace(current)))
		return run, true, blockErr
	}

	changes := make([]missionplan.Change, 0, len(plan.Changes))
	for _, change := range plan.Changes {
		changes = append(changes, missionplan.Change{Path: change.Path, Intent: change.Intent, Patch: change.Patch})
	}
	derived, err := missionplan.Derive(missionplan.Request{
		TaskID: 0, BaseSHA: baseSHA, Scope: missionScope(root),
		Objective:          plan.Objective,
		Changes:            changes,
		AcceptanceCriteria: root.AcceptanceCriteria,
	})
	if err != nil {
		// A plan that asks for more reach than its scope allows is a refusal,
		// not a retry: re-running the same leader would produce the same
		// request.
		run, blockErr := o.blockRoot(ctx, root, ReasonMissionPolicyRejected, err.Error())
		return run, true, blockErr
	}
	planJSON, err := missionplan.EncodePlan(derived.Plan)
	if err != nil {
		return Run{}, true, err
	}

	principal, err := o.principals.ResolveRoleBoundPrincipal(ctx, CEORoleID)
	if err != nil {
		return Run{}, true, err
	}
	mission, err := o.missions.ProvisionMission(ctx, MissionProvisionCommand{
		Policy: derived.Policy, PlanJSON: planJSON,
		RequestedByRoleID: CEORoleID, ActorType: "service", ActorID: principal.ID,
		CorrelationID: root.CorrelationID, CausationID: taskCausation(root.ID),
	})
	if err != nil {
		// A refused mission is a refusal, not a retry -- the same reason the
		// policy rejection above blocks rather than returning. Re-submitting
		// the identical policy and plan is refused identically, so returning
		// the error would leave the root executable and the worker would
		// resume it forever.
		if errors.Is(err, ErrMissionRejected) {
			run, blockErr := o.blockRoot(ctx, root, ReasonMissionRejected, err.Error())
			return run, true, blockErr
		}
		return Run{}, true, err
	}

	if err = o.tasks.RecordEvidence(ctx, EvidenceCommand{
		TaskID: root.ID, RequirementID: requirement.ID, Type: "approval",
		Reference:  fmt.Sprintf("engineering-mission://%d", mission.TaskID),
		Digest:     policyDigest(derived.Policy),
		RecordedBy: orchestratorWorkerID,
		Metadata: map[string]any{
			"mission_task_id": mission.TaskID, "base_sha": derived.Policy.BaseSHA,
			"allowed_paths": derived.Policy.AllowedPaths, "scope": string(missionScope(root)),
			"implementation_plan_task_id": planTask.ID,
		},
		Satisfies: true,
	}); err != nil {
		return Run{}, true, err
	}
	return Run{}, false, nil
}

// missionScope is assigned by the host from a durable owner requirement, never
// proposed by the model. Absence is the narrowest scope, so a run that says
// nothing about reach gets the least of it.
func missionScope(root TaskRecord) missionplan.Scope {
	if _, widened := findRequirementByKey(root.Requirements, InternalCodeScopeRequirementKey); widened {
		return missionplan.ScopeInternalCode
	}
	return missionplan.ScopeDocumentation
}

// implementationLeader falls back to the leader that actually produced a
// department deliverable when the root carries no unit of its own.
func (o *Orchestrator) implementationLeader(ctx context.Context, all []TaskRecord) (RoleRef, error) {
	for _, task := range all {
		if task.TaskClass != TaskClassCoordinationDeptReview || task.AssignedUnitID == "" {
			continue
		}
		leader, err := o.registry.GetLeader(ctx, task.AssignedUnitID)
		if err == nil && leader.Enabled && leader.Executable {
			return leader, nil
		}
	}
	return RoleRef{}, fmt.Errorf("no dispatchable department leader can author an implementation plan")
}

func policyDigest(policy engineeringmission.MissionPolicy) string {
	_, digest, err := policy.MarshalEvidence()
	if err != nil {
		return ""
	}
	return digest
}
