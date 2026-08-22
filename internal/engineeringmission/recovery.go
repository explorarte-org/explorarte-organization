package engineeringmission

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/staging"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

// RecoveryReason is the durable verdict on whether a dead-lettered mission may
// have a successor episode, and why.
//
// Every value is decided from records the runtime already wrote: the attempt
// the engine classified, the redrive links that exist, and the commit the
// mission pinned versus the one the target actually points at. None of them is
// decided by reading an error message. A failure summary is a description
// written for a human; treating it as a control signal makes recovery depend
// on provider copy that can change without notice.
type RecoveryReason string

const (
	// RecoveryEligible: the runtime classified the failure retryable, the
	// world the mission failed against no longer exists, and the chain has
	// budget left.
	RecoveryEligible RecoveryReason = "eligible"
	// RecoveryUnclassified: no attempt carries a durable retryable verdict.
	// Fail closed -- an unclassified failure is not evidence of a transient
	// one, and recovering on absence of information is how a deterministic
	// failure gets retried until the budget is gone.
	RecoveryUnclassified RecoveryReason = "unclassified"
	// RecoveryPermanent: the runtime classified the failure as not
	// retryable. Repeating it cannot change the outcome.
	RecoveryPermanent RecoveryReason = "permanent"
	// RecoveryUnchangedWorld: the failure was transient by classification,
	// but the mission's pinned base commit is still exactly what the target
	// points at. A successor would do the same work under the same
	// conditions, so its most likely outcome is the same failure.
	//
	// This is the condition that keeps a bounded recovery loop from
	// becoming an expensive way to fail five more times: eligibility needs
	// both a failure that COULD pass on a repeat and a world in which
	// something relevant actually moved.
	RecoveryUnchangedWorld RecoveryReason = "unchanged_world"
	// RecoveryAlreadyRecovered: this dead letter already has its successor.
	RecoveryAlreadyRecovered RecoveryReason = "already_recovered"
	// RecoveryExhausted: the chain already used its permitted episodes.
	RecoveryExhausted RecoveryReason = "exhausted"
	// RecoveryNotAMission: the dead-lettered task carries no mission policy,
	// so this policy has nothing to say about it.
	RecoveryNotAMission RecoveryReason = "not_a_mission"
	// RecoveryNoWorkspace: the failed mission never recorded a workspace,
	// so there is no durable record of which repository, target ref and
	// base commit it actually worked against. Without that, "the world
	// moved" cannot be evaluated against the mission's own world, only
	// against the worker's current configuration -- which is not the same
	// question. Fail closed.
	RecoveryNoWorkspace RecoveryReason = "no_workspace"
	// RecoveryUnreconciled: the failed mission has model invocations still
	// in the durable 'ambiguous' state. The provider may already have
	// received and acted on the request. Until that is settled, repeating
	// the work would duplicate side effects while spending again.
	RecoveryUnreconciled RecoveryReason = "unreconciled_ambiguous_outcome"
	// RecoveryBudgetExhausted: the program that owns this work cannot
	// absorb another episode the size of the one that failed.
	RecoveryBudgetExhausted RecoveryReason = "budget_exhausted"
	// RecoveryOrphanedSuccessor: this dead letter is stamped, but its
	// successor is still held and its mission policy never became durable
	// -- and the policy cannot be reconstructed, because the world moved
	// again since. The successor stays blocked and visible rather than
	// being published with a guessed policy or silently forgotten.
	RecoveryOrphanedSuccessor RecoveryReason = "orphaned_successor"
	// RecoveryIdentityTaken: an identical successor already exists under a
	// different dead letter. Adopting it would link work scheduled for
	// another failure into this chain and report a recovery that never
	// scheduled anything new.
	RecoveryIdentityTaken RecoveryReason = "successor_identity_taken"
)

// RecoveryDecision explains one dead letter's eligibility.
type RecoveryDecision struct {
	DeadLetterID int64
	TaskID       int64
	Reason       RecoveryReason
	// ObservedChange is set only when Reason is RecoveryEligible, and is
	// the justification carried into the successor's durable record.
	ObservedChange string
	// BaseSHA is what the failed mission actually worked against; Head is
	// what that mission's own target points at now. RepositoryID and
	// TargetRef name the world both refer to. All are reported for every
	// decision so that a refusal is as inspectable as an approval.
	BaseSHA      string
	Head         string
	RepositoryID string
	TargetRef    string
	// Budget carries the admission answer when one was reached.
	Budget BudgetVerdict
}

func (d RecoveryDecision) Eligible() bool { return d.Reason == RecoveryEligible }

// HeadResolver reports the commit a target ref currently points at.
//
// It is a port rather than a git call so that the recovery policy stays
// testable without a repository, and so that the deployment decides which
// repository is authoritative -- the same reason WorkspaceResolver takes its
// repository and target as trusted configuration instead of task input.
type HeadResolver interface {
	ResolveHead(ctx context.Context, repositoryID, targetRef string) (string, error)
}

// RecoveryPort is the slice of the task engine recovery needs.
type RecoveryPort interface {
	tasks.TaskReader
	RedriveDeadLetter(context.Context, tasks.RedriveCommand) (tasks.RedriveResult, error)
	RecordEvidence(context.Context, tasks.RecordEvidenceCommand) (tasks.Evidence, error)
	ReleaseCoordinationHold(context.Context, tasks.ReleaseCoordinationHoldCommand) (tasks.Task, error)
}

// WorkspaceReader reads the workspaces a task actually opened.
//
// A workspace row is the only durable record of the repository, target ref
// and base commit an attempt really used. Recovery reads it instead of its own
// configuration because those can differ: a worker repointed at another target
// would otherwise decide that a mission's world had changed on the strength of
// a ref that mission never touched.
type WorkspaceReader interface {
	ListWorkspaces(context.Context, staging.WorkspaceFilter) ([]staging.Workspace, error)
}

// AmbiguityReader reports model invocations whose outcome never settled.
type AmbiguityReader interface {
	UnreconciledAmbiguousInvocations(ctx context.Context, taskID int64) (int, error)
}

// MissionResolver reads a task back as the engineering mission it declared.
//
// Recovery depends on this narrow shape rather than on the concrete Service so
// that the eligibility rules can be exercised without standing up the whole
// task service behind them. Service satisfies it.
type MissionResolver interface {
	Resolve(context.Context, int64) (MissionPolicy, error)
}

// Recovery decides whether dead-lettered engineering missions get successor
// episodes, and opens them.
//
// It never revives a task. The dead-lettered mission keeps its status, its
// attempts and its failure permanently; recovery is a NEW mission, pinned to
// the commit the target points at now, linked to the dead letter that
// justified it.
type Recovery struct {
	Tasks      RecoveryPort
	Mission    MissionResolver
	Head       HeadResolver
	Workspaces WorkspaceReader
	Ambiguity  AmbiguityReader
	Budget     BudgetAdmission

	// MaxRecoveryEpisodes bounds one failure's chain of successors. It is
	// deliberately separate from a task's own MaxAttempts and from the
	// program budget: attempts bound one episode's retries, this bounds how
	// many episodes a single original failure may spawn, and the budget
	// bounds spend across everything. Collapsing them would mean one limit
	// silently standing in for three different questions.
	MaxRecoveryEpisodes int

	// RequestedBy is the fallback requester for a mission whose own
	// requester was never recorded.
	RequestedBy string
	ActorType   string
	ActorID     string
}

// Evaluate decides one dead letter without changing anything.
func (r Recovery) Evaluate(ctx context.Context, dead tasks.DeadLetter) (RecoveryDecision, error) {
	decision := RecoveryDecision{DeadLetterID: dead.ID, TaskID: dead.TaskID}
	if r.Tasks == nil || r.Head == nil || r.Mission == nil || r.Workspaces == nil || r.Ambiguity == nil || r.Budget == nil {
		return decision, fmt.Errorf("recovery requires task, mission, workspace, ambiguity, head and budget ports")
	}
	if dead.RedriveTaskID != nil {
		decision.Reason = RecoveryAlreadyRecovered
		return decision, nil
	}

	policy, err := r.Mission.Resolve(ctx, dead.TaskID)
	if err != nil {
		// A dead letter that is not an engineering mission is simply not
		// this policy's business. Reporting it as an error would make an
		// ordinary sweep over a mixed queue look like a malfunction.
		decision.Reason = RecoveryNotAMission
		return decision, nil
	}

	classified, classifyErr := r.classify(ctx, dead.TaskID)
	if classifyErr != nil {
		return decision, classifyErr
	}
	if classified == nil {
		decision.Reason = RecoveryUnclassified
		return decision, nil
	}
	if !*classified {
		decision.Reason = RecoveryPermanent
		return decision, nil
	}

	// An outcome nobody settled is not a transient failure. Checked before
	// anything else costs a git read, and before budget, because the answer
	// does not depend on either.
	unsettled, err := r.Ambiguity.UnreconciledAmbiguousInvocations(ctx, dead.TaskID)
	if err != nil {
		return decision, err
	}
	if unsettled > 0 {
		decision.Reason = RecoveryUnreconciled
		return decision, nil
	}

	world, err := r.worldOf(ctx, dead.TaskID, policy)
	if err != nil {
		return decision, err
	}
	if world == nil {
		decision.Reason = RecoveryNoWorkspace
		return decision, nil
	}
	decision.RepositoryID, decision.TargetRef, decision.BaseSHA = world.RepositoryID, world.TargetRef, world.BaseCommit

	head, err := r.Head.ResolveHead(ctx, world.RepositoryID, world.TargetRef)
	if err != nil {
		return decision, err
	}
	decision.Head = strings.TrimSpace(head)
	if decision.Head == "" {
		return decision, fmt.Errorf("head resolver returned no commit for %s@%s", world.RepositoryID, world.TargetRef)
	}
	if decision.Head == world.BaseCommit {
		decision.Reason = RecoveryUnchangedWorld
		return decision, nil
	}

	// Budget last: it is the only check whose answer can change minute to
	// minute, so deciding it against a candidate that already passed every
	// stable test keeps the verdict meaningful.
	verdict, err := r.Budget.Admit(ctx, dead.TaskID)
	if err != nil {
		return decision, err
	}
	decision.Budget = verdict
	if !verdict.Admitted {
		decision.Reason = RecoveryBudgetExhausted
		return decision, nil
	}

	decision.Reason = RecoveryEligible
	decision.ObservedChange = fmt.Sprintf("%s@%s advanced from %s to %s since the mission failed",
		world.RepositoryID, world.TargetRef, world.BaseCommit, decision.Head)
	return decision, nil
}

// worldOf reports the repository, target ref and base commit the failed
// mission ACTUALLY worked against, from its own most recent workspace.
//
// Adversarial review broke the first version here: it compared the mission's
// pinned base against whatever ORG_CODE_RUNNER_TARGET_REF the running worker
// happened to be configured with. A mission pinned to one promotion target
// would then be "recovered" because an unrelated branch moved, and its
// successor retargeted at a commit that mission would never check out. "The
// repo advanced" is not "the world relevant to this mission changed".
//
// nil means the mission never opened a workspace, which is fail-closed.
func (r Recovery) worldOf(ctx context.Context, taskID int64, policy MissionPolicy) (*staging.Workspace, error) {
	spaces, err := r.Workspaces.ListWorkspaces(ctx, staging.WorkspaceFilter{TaskID: taskID, Limit: 200})
	if err != nil {
		return nil, err
	}
	var latest *staging.Workspace
	for index := range spaces {
		repository := strings.TrimSpace(spaces[index].RepositoryID)
		target := strings.TrimSpace(spaces[index].TargetRef)
		base := strings.TrimSpace(spaces[index].BaseCommit)
		if repository == "" || target == "" || base == "" {
			continue
		}
		// The mission policy is the authority on which commit this
		// mission was pinned to; the workspace is the authority on which
		// repository and ref it used. A workspace whose base disagrees
		// with the policy describes some other piece of work -- a
		// different attempt under a repointed worker, or a mis-pinned
		// run -- and treating it as this mission's world would let
		// recovery declare movement against a base the policy never
		// used. Adversarial review round 2, finding A.
		if base != policy.BaseSHA {
			continue
		}
		if latest == nil || spaces[index].ID > latest.ID {
			latest = &spaces[index]
		}
	}
	return latest, nil
}

// classify reports the runtime's own durable verdict on the attempt that
// EXHAUSTED the task: true retryable, false permanent, nil never classified.
//
// It reads attempts rather than the failure text for the reason stated on
// RecoveryReason.
//
// Which attempt is read is the whole safety property, and the first version of
// this function got it wrong: it walked every attempt and skipped the ones
// carrying no verdict, so a task whose FINAL attempt was never classified
// inherited an earlier attempt's "retryable" and recovered on a failure nobody
// had classified. Adversarial review found it with attempts [true, nil].
//
// The exhausting attempt is the only one that can justify an episode, and if
// IT carries no verdict the answer is nil -- fail closed. Absence of a
// classification is not evidence of a transient failure.
func (r Recovery) classify(ctx context.Context, taskID int64) (*bool, error) {
	attempts, err := r.Tasks.ListAttempts(ctx, taskID)
	if err != nil {
		return nil, err
	}
	var exhausting *tasks.Attempt
	for index := range attempts {
		attempt := attempts[index]
		if attempt.State != tasks.AttemptFailed && attempt.State != tasks.AttemptLeaseExpired {
			continue
		}
		if exhausting == nil || attempt.Ordinal > exhausting.Ordinal {
			exhausting = &attempts[index]
		}
	}
	if exhausting == nil {
		return nil, nil
	}
	if exhausting.Retryable == nil {
		return nil, nil
	}
	verdict := *exhausting.Retryable
	return &verdict, nil
}

// Recover evaluates one dead letter and, if it is eligible, opens the
// successor episode. It returns the decision it acted on.
//
// The successor is born under a COORDINATION HOLD -- durable immediately,
// claimable by nobody -- and is published only once its mission policy, pinned
// to the new head, is durable against it. If anything below fails, the
// successor stays blocked: visible, never claimed, nothing spent.
func (r Recovery) Recover(ctx context.Context, dead tasks.DeadLetter) (RecoveryDecision, tasks.RedriveResult, error) {
	decision, err := r.Evaluate(ctx, dead)
	if err != nil {
		return decision, tasks.RedriveResult{}, err
	}
	// A stamped letter is not necessarily a finished recovery: the process
	// can die between the redrive commit and publication. Round 2 of
	// adversarial review showed the first version returned here and never
	// came back, so the successor stayed blocked forever while its comment
	// claimed the sweep would repair it. Now it actually does.
	if decision.Reason == RecoveryAlreadyRecovered && dead.RedriveTaskID != nil {
		return r.repair(ctx, dead, decision)
	}
	if !decision.Eligible() {
		return decision, tasks.RedriveResult{}, nil
	}
	if r.MaxRecoveryEpisodes < 1 {
		return decision, tasks.RedriveResult{}, fmt.Errorf("max_recovery_episodes must be configured before recovery can run")
	}

	successor, policy, err := r.successorRequest(ctx, dead.TaskID, decision.Head)
	if err != nil {
		return decision, tasks.RedriveResult{}, err
	}
	result, err := r.Tasks.RedriveDeadLetter(ctx, tasks.RedriveCommand{
		DeadLetterID:        dead.ID,
		Successor:           successor,
		ObservedChange:      decision.ObservedChange,
		MaxRecoveryEpisodes: r.MaxRecoveryEpisodes,
		ActorType:           r.ActorType,
		ActorID:             r.ActorID,
	})
	switch {
	case errors.Is(err, tasks.ErrRecoveryEpisodesExhausted):
		decision.Reason, decision.ObservedChange = RecoveryExhausted, ""
		return decision, tasks.RedriveResult{}, nil
	case errors.Is(err, tasks.ErrSuccessorIdentityTaken):
		// Governed, not exceptional: an identical successor already
		// exists under another dead letter. Surfacing this as an error
		// made the sweep retry it every minute forever.
		decision.Reason, decision.ObservedChange = RecoveryIdentityTaken, ""
		return decision, tasks.RedriveResult{}, nil
	case err != nil:
		return decision, tasks.RedriveResult{}, err
	}

	if err := r.publishSuccessor(ctx, result.Successor, policy); err != nil {
		return decision, result, err
	}
	return decision, result, nil
}

// repair finishes a recovery that was interrupted after its successor became
// durable but before it was published.
//
// The successor's policy cannot simply be rebuilt from today's head: the head
// may have moved again, and publishing a policy the successor's identity was
// not derived from would bind it to work it never claimed to be. So the
// candidate policy is checked against the successor's own idempotency key, and
// only a match is published. A mismatch leaves the task blocked and says so.
func (r Recovery) repair(ctx context.Context, dead tasks.DeadLetter, decision RecoveryDecision) (RecoveryDecision, tasks.RedriveResult, error) {
	successorDetail, err := r.Tasks.GetTask(ctx, *dead.RedriveTaskID)
	if err != nil {
		return decision, tasks.RedriveResult{}, err
	}
	if successorDetail.Task.Status != tasks.StatusBlocked {
		return decision, tasks.RedriveResult{}, nil
	}
	policy, err := r.Mission.Resolve(ctx, dead.TaskID)
	if err != nil {
		return decision, tasks.RedriveResult{}, nil
	}
	world, err := r.worldOf(ctx, dead.TaskID, policy)
	if err != nil || world == nil {
		decision.Reason = RecoveryOrphanedSuccessor
		return decision, tasks.RedriveResult{}, err
	}
	head, err := r.Head.ResolveHead(ctx, world.RepositoryID, world.TargetRef)
	if err != nil {
		return decision, tasks.RedriveResult{}, err
	}
	candidate, err := successorPolicy(policy, strings.TrimSpace(head))
	if err != nil {
		return decision, tasks.RedriveResult{}, err
	}
	if err := r.publishSuccessor(ctx, successorDetail.Task, candidate); err != nil {
		if errors.Is(err, errSuccessorIdentityMismatch) {
			decision.Reason = RecoveryOrphanedSuccessor
			return decision, tasks.RedriveResult{}, nil
		}
		return decision, tasks.RedriveResult{}, err
	}
	return decision, tasks.RedriveResult{Successor: successorDetail.Task}, nil
}

// errSuccessorIdentityMismatch reports that a policy was offered for a
// successor whose identity was derived from a different one.
var errSuccessorIdentityMismatch = errors.New("successor policy does not match its identity")

// publishSuccessor makes the held successor a real, claimable mission: its
// policy becomes durable against its own ID, and only then is the hold
// released. It is idempotent, so an interrupted sweep finishes the job later.
//
// The identity guard is what stops one worker from writing its own policy onto
// a successor another worker created: the task's idempotency key IS the digest
// of the policy it was created for, so a policy that does not reproduce that
// key is not this task's policy, whatever else it might be.
func (r Recovery) publishSuccessor(ctx context.Context, successor tasks.Task, policy MissionPolicy) error {
	identity, err := successorPolicy(policy, policy.BaseSHA)
	if err != nil {
		return err
	}
	_, keyDigest, err := identity.MarshalEvidence()
	if err != nil {
		return err
	}
	if successor.IdempotencyKey != "engineering-mission/"+keyDigest {
		return fmt.Errorf("%w: task %d", errSuccessorIdentityMismatch, successor.ID)
	}

	bound := identity
	bound.TaskID = successor.ID
	bound, err = bound.Normalize()
	if err != nil {
		return err
	}
	metadata, digest, err := bound.MarshalEvidence()
	if err != nil {
		return err
	}
	reference := "engineering-mission://" + fmt.Sprint(successor.ID)
	detail, err := r.Tasks.GetTask(ctx, successor.ID)
	if err != nil {
		return err
	}
	recorded := false
	for _, evidence := range detail.Evidence {
		if evidence.Reference == reference {
			recorded = true
			break
		}
	}
	if !recorded {
		if _, err := r.Tasks.RecordEvidence(ctx, tasks.RecordEvidenceCommand{
			TaskID: successor.ID, Type: tasks.RequirementResult, Reference: reference,
			Digest: digest, RecordedBy: r.ActorID, Metadata: metadata,
		}); err != nil {
			return err
		}
	}
	if detail.Task.Status != tasks.StatusBlocked {
		return nil
	}
	if _, err := r.Tasks.ReleaseCoordinationHold(ctx, tasks.ReleaseCoordinationHoldCommand{
		TaskID: successor.ID, ActorType: r.ActorType, ActorID: r.ActorID,
	}); err != nil {
		return err
	}
	return nil
}

// successorPolicy derives the identity-form policy of a successor pinned to
// head: the predecessor's mission with a new base and no task bound yet.
//
// It exists so that the digest used for the successor's idempotency key and
// the digest checked before publishing it are produced by ONE function. Two
// derivations of "the successor's policy" would eventually disagree, and the
// disagreement would appear as a successor nobody could publish.
func successorPolicy(policy MissionPolicy, head string) (MissionPolicy, error) {
	policy.TaskID = 0
	policy.BaseSHA = head
	return policy.Normalize()
}

// RecoverPending sweeps the dead-letter queue and opens every successor it is
// entitled to open, returning one decision per dead letter it considered.
//
// A failure on one dead letter does not abandon the rest: a sweep that stops
// at the first problem leaves recoverable work indefinitely unrecovered
// because of an unrelated neighbour.
func (r Recovery) RecoverPending(ctx context.Context, limit int) ([]RecoveryDecision, error) {
	if limit <= 0 {
		// Dead letters are listed newest first and a permanent failure
		// stays listed forever, so a small window would eventually be
		// filled entirely by letters that can never be recovered, hiding
		// eligible work behind them. Evaluation of an ineligible letter
		// is one or two cheap reads, and the sweep only runs when the
		// runner has nothing else to do.
		limit = 200
	}
	letters, err := r.Tasks.ListDeadLetters(ctx, limit)
	if err != nil {
		return nil, err
	}
	decisions := make([]RecoveryDecision, 0, len(letters))
	var failures []error
	for _, dead := range letters {
		decision, _, recoverErr := r.Recover(ctx, dead)
		if recoverErr != nil {
			failures = append(failures, fmt.Errorf("dead letter %d: %w", dead.ID, recoverErr))
			continue
		}
		decisions = append(decisions, decision)
	}
	return decisions, errors.Join(failures...)
}

// successorRequest builds the recovery mission: the same objective, paths,
// criteria and gates, pinned to the commit that makes a different outcome
// possible.
//
// It returns the policy alongside the request because the policy must become
// durable against the successor's own ID after creation -- a task carrying
// mission instructions but no mission policy is not a mission.
func (r Recovery) successorRequest(ctx context.Context, failedTaskID int64, head string) (tasks.CreateRequest, MissionPolicy, error) {
	policy, err := r.Mission.Resolve(ctx, failedTaskID)
	if err != nil {
		return tasks.CreateRequest{}, MissionPolicy{}, err
	}
	policy, err = successorPolicy(policy, head)
	if err != nil {
		return tasks.CreateRequest{}, MissionPolicy{}, err
	}
	_, digest, err := policy.MarshalEvidence()
	if err != nil {
		return tasks.CreateRequest{}, MissionPolicy{}, err
	}
	failed, err := r.Tasks.GetTask(ctx, failedTaskID)
	if err != nil {
		return tasks.CreateRequest{}, MissionPolicy{}, err
	}
	// The successor is requested by whoever requested the mission that
	// failed. Re-attributing it to the recovery machinery would erase the
	// only durable record of who actually wanted this work done.
	requestedBy := r.RequestedBy
	if failed.Task.RequestedByRoleID != nil && *failed.Task.RequestedByRoleID != "" {
		requestedBy = *failed.Task.RequestedByRoleID
	}
	// The correlation is what binds a task to its program, and the program
	// ceiling is enforced BY CORRELATION at reservation time. Round 2 of
	// adversarial review found the episode being admitted against a
	// program's remaining budget and then created without it: permission
	// granted against a ceiling the resulting spend would never debit,
	// leaving every later admission looking at a remainder that never
	// moved. Carrying it forward is what makes the admission mean
	// something -- and it is also what makes the admission merely an early
	// stop rather than the only gate, since ReserveWithinProgramCeiling
	// then refuses the actual invocations once the ceiling is reached.
	correlation := ""
	if failed.Task.CorrelationID != nil {
		correlation = *failed.Task.CorrelationID
	}
	// The successor is caused by the failure it recovers, not by whatever
	// caused the original mission.
	causation := fmt.Sprint(failedTaskID)
	return tasks.CreateRequest{
		OrganizationID:     failed.Task.OrganizationID,
		RequestedByRoleID:  requestedBy,
		AssignedRoleID:     CodeRunnerRole,
		Title:              missionTitle(policy.Objective),
		Instructions:       failed.Task.Instructions,
		AcceptanceCriteria: policy.AcceptanceCriteria,
		IdempotencyKey:     "engineering-mission/" + digest,
		CorrelationID:      correlation,
		CausationID:        causation,
		// Born held: durable at once, claimable by nobody until its
		// mission policy is durable too. See publishSuccessor.
		HoldForCoordination: true,
		Requirements: []tasks.RequirementSpec{
			{Key: "candidate-artifact", Type: tasks.RequirementArtifact, Description: "sealed engineering candidate", Required: boolPtr(true)},
			{Key: "engineering-required-gates", Type: tasks.RequirementCheck, Description: "all declared engineering gates pass", Required: boolPtr(true)},
			{Key: "review", Type: tasks.RequirementApproval, Description: "independent engineering review", Required: boolPtr(true)},
		},
	}, policy, nil
}

// The task engine service is the intended RecoveryPort. Asserting it here is
// what keeps the port and its only real implementation from drifting apart in
// separate files with nothing comparing them.
var _ RecoveryPort = (*tasks.Service)(nil)

var _ MissionResolver = Service{}
