// Package completion implements Phase 2 of docs/canonical/reasoning-assurance.yaml
// (task_obligation_and_completion_verifier): an independent verifier that a task's
// completion claim is actually true, before a caller is allowed to record it as
// terminally completed. It never mutates internal/tasks state — callers gate their
// own Finalize(FinalCompleted) call on VerdictPass, the same way every other
// cross-package reader in this repo (cellworker/postgres, decisiongraphtrace) reads
// another branch's tables via SQL without importing that branch's Go package.
package completion

import "time"

// VerificationLabel mirrors docs/canonical/reasoning-assurance.yaml's
// decision_graph.verification_labels vocabulary, reused here so obligation-level
// results speak the same language the canon already defines.
type VerificationLabel string

const (
	LabelVerified     VerificationLabel = "verified"
	LabelInferred     VerificationLabel = "inferred"
	LabelUnknown      VerificationLabel = "unknown"
	LabelContradicted VerificationLabel = "contradicted"
)

func (l VerificationLabel) Valid() bool {
	switch l {
	case LabelVerified, LabelInferred, LabelUnknown, LabelContradicted:
		return true
	default:
		return false
	}
}

// ObligationID enumerates the five checks reasoning-assurance.yaml Phase 2 scopes
// exactly (task_obligation_and_completion_verifier.scope).
type ObligationID string

const (
	ObligationRequirementsSatisfied  ObligationID = "requirements_satisfied"
	ObligationArtifactExists         ObligationID = "artifact_exists"
	ObligationChecksPassed           ObligationID = "checks_passed"
	ObligationApprovalPresent        ObligationID = "approval_present"
	ObligationNoRejectedBranchReused ObligationID = "no_rejected_branch_reused"
)

// ObligationResult is the outcome of independently checking one obligation against
// the system of record it belongs to (internal/tasks, internal/staging,
// internal/authorization or internal/decisiongraph) rather than trusting whatever
// internal/tasks' own self-attested Requirement/Evidence rows already claim.
type ObligationResult struct {
	Obligation    ObligationID
	Label         VerificationLabel
	Detail        string
	RequirementID int64 // 0 when the obligation is task-scoped, not requirement-scoped
}

// Verdict is the aggregate decision a caller gates Finalize(FinalCompleted) on.
type Verdict string

const (
	// VerdictPass: every required obligation is verified or inferred, none contradicted.
	VerdictPass Verdict = "pass"
	// VerdictFail: at least one required obligation is contradicted by independent
	// evidence — the completion claim is actively false, not merely unproven.
	VerdictFail Verdict = "fail"
	// VerdictInconclusive: no obligation is contradicted, but at least one required
	// obligation could not be independently verified (label unknown). Per
	// reasoning-assurance.yaml's own rule — "Absence of proof is not proof of
	// falsity" — this is deliberately distinct from VerdictFail: it blocks
	// automatic completion the same way, but the corrective action is "produce
	// evidence", not "the task is broken".
	VerdictInconclusive Verdict = "inconclusive"
)

// VerificationRequest identifies exactly which task attempt to verify.
// decisiongraph.Run already carries TaskID and AttemptID directly, so
// ObligationNoRejectedBranchReused resolves the run itself rather than requiring
// the caller to know a run ID up front.
type VerificationRequest struct {
	TaskID    int64
	AttemptID int64
}

type VerificationResult struct {
	TaskID      int64
	AttemptID   int64
	Verdict     Verdict
	Obligations []ObligationResult
	VerifiedAt  time.Time
}
