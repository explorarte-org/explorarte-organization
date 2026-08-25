package executive

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
)

// Autonomous ambiguity reconciliation (B2, AUTONOMY-SMOKE-017-R14).
//
// Model Runtime owns the verdict: when a provider send may have started and
// its outcome died in transport, the invocation is durably `ambiguous` and
// NOTHING transitions that row -- not cancel (a no-op on terminal rows), not
// reconcile (which only classifies non-terminal dispatches). R14 hit exactly
// this: an adjudication call exceeded its transport timeout after send start,
// the reconciler marked it ambiguous, and priorExecutionBarrier then refused
// every future attempt forever. The barrier's own comment demands "explicit
// reconciliation", but no such primitive existed: fail-closed without a
// keyhole. Two unblocks of the root were re-blocked within a second each,
// which is how the defect was proven.
//
// The repair keeps every property the barrier defends and adds only the
// missing fact:
//
//	ambiguous invocation            -> stays ambiguous, forever, immutable.
//	   host policy inspects it      -> pure_model executions only (v1).
//	ambiguity-resolution://<invID>  -> durable task evidence, write-once:
//	                                   disposition=retry_authorized,
//	                                   authority=host_policy.
//	barrier                         -> blocks iff NO valid resolution exists
//	                                   for THAT exact invocation.
//
// What the resolution asserts is deliberately NOT "the call did not happen".
// It asserts something narrower and true: an operational host policy has
// inspected this ambiguity and accepted the duplicate-call risk of one
// retry. Provenance survives; the historical row is never rewritten.
//
// Authority is the HOST, mechanically -- never a model judgment and never a
// per-incident human favor -- because the decision depends on operational
// invariants (what kind of execution could have side effects), not on
// cognition. Classifying by effect is what keeps this from becoming a global
// "ambiguous implies retry": today every execution this Executive drives is
// tool-free text generation (HarnessRunCommand exposes no tool surface), so
// v1 authorizes exactly those and refuses everything else. When the
// organization later runs tools with real external effects, their classes
// arrive here as new cases and simply do not auto-authorize.

// ExecutionEffectClass states what repeating an ambiguous execution could
// affect. pure_model means the execution was cognitive text generation with
// no external side effect beyond cost; unknown means the classifier cannot
// prove that, and unknown never auto-authorizes.
type ExecutionEffectClass string

const (
	// EffectPureModel: the execution was one of this Executive's typed
	// cognitive drives -- plan, review, worker deliverable, adversarial
	// review, adjudication, closure, implementation proposal. Repeating it
	// costs tokens; it cannot move anything outside the organization.
	EffectPureModel ExecutionEffectClass = "pure_model"
	// EffectUnknown: everything not provably pure. Future classes
	// (idempotent_read, idempotent_write, externally_idempotent,
	// compensatable, non_idempotent) are named here so the frontier is
	// explicit; until a class is PROVEN safe it stays unknown, and unknown
	// blocks exactly as the pre-B2 barrier did.
	EffectUnknown ExecutionEffectClass = "unknown"
)

// ambiguityResolutionReference is where one invocation's resolution lives:
// durable task evidence on the OWNING task, namespaced by the exact
// invocation ID. One ambiguity, one resolution; there is deliberately no
// "ack all".
const AmbiguityResolutionReference = "ambiguity-resolution://"

func ambiguityResolutionReference(invocationID int64) string {
	return AmbiguityResolutionReference + strconv.FormatInt(invocationID, 10)
}

// The disposition vocabulary. retry_authorized is the only one v1 can mean:
// confirmed_succeeded / confirmed_failed would require materializing a
// provider result nobody has, so they are left to a future that can.
const (
	AmbiguityDispositionRetryAuthorized = "retry_authorized"
	AmbiguityAuthorityHostPolicy        = "host_policy"
	AmbiguityPolicyPureModelV1          = "pure_model_execution_v1"
)

// executivePureModelTaskClasses is the allowlist of TaskClass values whose
// executions are, by construction of driveTypedTask + HarnessRunCommand,
// tool-free text generation. Anything else classifies unknown.
var executivePureModelTaskClasses = map[string]struct{}{
	TaskClassCoordinationCEOPlan:            {},
	TaskClassCoordinationDeptPlan:           {},
	TaskClassCoordinationDeptReview:         {},
	TaskClassCoordinationCEOClosure:         {},
	TaskClassCoordinationAdversarialReview:  {},
	TaskClassCoordinationDesignAdjudication: {},
	TaskClassCoordinationImplementationPlan: {},
	TaskClassGeneralWork:                    {},
	"engineering.design":                    {},
}

func executionEffectClass(task TaskRecord) ExecutionEffectClass {
	if _, ok := executivePureModelTaskClasses[task.TaskClass]; ok {
		return EffectPureModel
	}
	return EffectUnknown
}

// validAmbiguityResolution reports whether an evidence row is a well-formed
// retry authorization for THIS exact invocation, issued by the host policy.
// A row with any other shape is foreign or corrupt: it never authorizes.
func validAmbiguityResolution(evidence EvidenceRecord, invocationID int64) bool {
	if evidence.Reference != ambiguityResolutionReference(invocationID) {
		return false
	}
	return evidence.Metadata["resolution"] == AmbiguityDispositionRetryAuthorized &&
		evidence.Metadata["authority"] == AmbiguityAuthorityHostPolicy &&
		evidence.Metadata["policy"] == AmbiguityPolicyPureModelV1
}

// findAmbiguityResolution reads back whether the owning task already carries
// a valid resolution for this invocation. Reading before writing is what
// makes re-application idempotent -- the same guard convention the evidence
// requirements store uses, because the engine appends evidence without
// uniqueness.
func (o *Orchestrator) findAmbiguityResolution(ctx context.Context, owningTaskID, invocationID int64) (bool, error) {
	detail, err := o.tasks.GetTask(ctx, owningTaskID)
	if err != nil {
		return false, err
	}
	for _, evidence := range detail.Evidence {
		if validAmbiguityResolution(evidence, invocationID) {
			return true, nil
		}
	}
	return false, nil
}

// reconcileAmbiguousInvocation is THE single decision point every ambiguity
// writer consults instead of blocking blindly. It returns true when the
// invocation may be treated as reconciled -- either because a valid
// resolution already existed (idempotent re-read) or because the host policy
// just authorized one (autonomous first application). It returns false when
// the caller must block exactly as the pre-B2 code did.
//
// Nothing about the invocation, its attempt, its accounting, or its budget
// changes here. The only write is the resolution fact on the owning task.
func (o *Orchestrator) reconcileAmbiguousInvocation(ctx context.Context, owningTask TaskRecord, invocation InvocationRecord) (bool, error) {
	resolved, err := o.findAmbiguityResolution(ctx, owningTask.ID, invocation.ID)
	if err != nil {
		return false, err
	}
	if resolved {
		return true, nil
	}
	class := executionEffectClass(owningTask)
	if class != EffectPureModel {
		// Unknown effect: the policy refuses, nothing is written, and the
		// caller blocks. A human decision path for non-pure classes is a
		// future checkpoint, deliberately out of scope here.
		return false, nil
	}
	reason := fmt.Sprintf(
		"ambiguous model execution after send started; %s authorizes one retry: invocation=%d attempt=%d task=%d",
		AmbiguityPolicyPureModelV1, invocation.ID, invocation.AttemptID, owningTask.ID)
	fact, err := json.Marshal(map[string]string{
		"resolution":   AmbiguityDispositionRetryAuthorized,
		"authority":    AmbiguityAuthorityHostPolicy,
		"policy":       AmbiguityPolicyPureModelV1,
		"effect_class": string(class),
		"reason":       reason,
	})
	if err != nil {
		return false, err
	}
	digest := sha256.Sum256(append([]byte("ambiguity_resolution\x00"), fact...))
	// Type is "result", not a bespoke string: EvidenceCommand.Type crosses
	// runtimeadapter into tasks.Service.ValidateEvidence, whose enum admits
	// only artifact/check/approval/condition/result. The resolution's identity
	// lives entirely in its namespaced reference and metadata -- the same way
	// evidence-requirements:// facts ride Type "result". A bespoke type here
	// passes every in-package fake and dies at the production boundary with
	// "evidence type is invalid".
	if err := o.tasks.RecordEvidence(ctx, EvidenceCommand{
		TaskID: owningTask.ID, Type: "result",
		Reference: ambiguityResolutionReference(invocation.ID),
		Digest:    hex.EncodeToString(digest[:]), RecordedBy: orchestratorWorkerID,
		Metadata: map[string]any{
			"resolution":   AmbiguityDispositionRetryAuthorized,
			"authority":    AmbiguityAuthorityHostPolicy,
			"policy":       AmbiguityPolicyPureModelV1,
			"effect_class": string(class),
			"reason":       reason,
		},
	}); err != nil {
		return false, err
	}
	return true, nil
}

// unreconciledAmbiguities scans every non-terminal child of the correlation
// for ambiguous invocations the policy cannot resolve. It AUTO-APPLIES the
// policy on the way through: a resolvable ambiguity becomes resolved as a
// side effect of being inspected, which is what makes the next resume pass
// proceed without any operator step. It returns true if at least one
// ambiguity remains that would still block.
//
// Children holding an ACTIVE lease are skipped on purpose. A live lease means
// another process may legitimately be about to record a result -- exactly
// what inspectUnadoptableAttempt protects -- and authorizing a second
// provider call beside it would betray the adoption rule. Their fate is
// decided by the adoption checks, not by this policy.
func (o *Orchestrator) unreconciledAmbiguities(ctx context.Context, root TaskRecord, children []TaskRecord) (bool, error) {
	for _, child := range children {
		if child.ID == root.ID || isTerminalTask(child.Status) || child.Status == "awaiting_verification" {
			continue
		}
		if child.ActiveLease != nil {
			continue
		}
		detail, err := o.tasks.GetTask(ctx, child.ID)
		if err != nil {
			return false, err
		}
		for _, attempt := range detail.Attempts {
			invocations, err := o.models.FindTaskAttemptInvocations(ctx, child.ID, attempt.ID)
			if err != nil {
				return false, err
			}
			for _, invocation := range invocations {
				if invocation.Status != "ambiguous" {
					continue
				}
				ok, err := o.reconcileAmbiguousInvocation(ctx, detail, invocation)
				if err != nil {
					return false, err
				}
				if !ok {
					return true, nil
				}
			}
		}
	}
	return false, nil
}
