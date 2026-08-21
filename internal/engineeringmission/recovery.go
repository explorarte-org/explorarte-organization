package engineeringmission

import (
	"context"
	"errors"
	"fmt"
	"strings"

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
)

// RecoveryDecision explains one dead letter's eligibility.
type RecoveryDecision struct {
	DeadLetterID int64
	TaskID       int64
	Reason       RecoveryReason
	// ObservedChange is set only when Reason is RecoveryEligible, and is
	// the justification carried into the successor's durable record.
	ObservedChange string
	// BaseSHA is what the failed mission pinned; Head is what the target
	// points at now. Both are reported for every decision so that a refusal
	// is as inspectable as an approval.
	BaseSHA string
	Head    string
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
	Tasks   RecoveryPort
	Mission MissionResolver
	Head    HeadResolver

	RepositoryID string
	TargetRef    string

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
	if r.Tasks == nil || r.Head == nil || r.Mission == nil {
		return decision, fmt.Errorf("recovery requires a task port, a mission resolver and a head resolver")
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
	decision.BaseSHA = policy.BaseSHA

	classified, err := r.classify(ctx, dead.TaskID)
	if err != nil {
		return decision, err
	}
	if classified == nil {
		decision.Reason = RecoveryUnclassified
		return decision, nil
	}
	if !*classified {
		decision.Reason = RecoveryPermanent
		return decision, nil
	}

	head, err := r.Head.ResolveHead(ctx, r.RepositoryID, r.TargetRef)
	if err != nil {
		return decision, err
	}
	decision.Head = strings.TrimSpace(head)
	if decision.Head == "" {
		return decision, fmt.Errorf("head resolver returned no commit for %s@%s", r.RepositoryID, r.TargetRef)
	}
	if decision.Head == policy.BaseSHA {
		decision.Reason = RecoveryUnchangedWorld
		return decision, nil
	}

	decision.Reason = RecoveryEligible
	decision.ObservedChange = fmt.Sprintf("%s@%s advanced from %s to %s since the mission failed",
		r.RepositoryID, r.TargetRef, policy.BaseSHA, decision.Head)
	return decision, nil
}

// classify reports the runtime's own durable verdict on the last attempt that
// reached a verdict: true retryable, false permanent, nil never classified.
//
// It reads attempts rather than the failure text for the reason stated on
// RecoveryReason, and it takes the LAST classified attempt because that is the
// one whose failure actually exhausted the task.
func (r Recovery) classify(ctx context.Context, taskID int64) (*bool, error) {
	attempts, err := r.Tasks.ListAttempts(ctx, taskID)
	if err != nil {
		return nil, err
	}
	var verdict *bool
	for _, attempt := range attempts {
		if attempt.Retryable == nil {
			continue
		}
		if attempt.State != tasks.AttemptFailed && attempt.State != tasks.AttemptLeaseExpired {
			continue
		}
		value := *attempt.Retryable
		verdict = &value
	}
	return verdict, nil
}

// Recover evaluates one dead letter and, if it is eligible, opens the
// successor episode. It returns the decision it acted on.
func (r Recovery) Recover(ctx context.Context, dead tasks.DeadLetter) (RecoveryDecision, tasks.RedriveResult, error) {
	decision, err := r.Evaluate(ctx, dead)
	if err != nil || !decision.Eligible() {
		return decision, tasks.RedriveResult{}, err
	}
	if r.MaxRecoveryEpisodes < 1 {
		return decision, tasks.RedriveResult{}, fmt.Errorf("max_recovery_episodes must be configured before recovery can run")
	}

	successor, err := r.successorRequest(ctx, dead.TaskID, decision.Head)
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
	if errors.Is(err, tasks.ErrRecoveryEpisodesExhausted) {
		decision.Reason = RecoveryExhausted
		decision.ObservedChange = ""
		return decision, tasks.RedriveResult{}, nil
	}
	if err != nil {
		return decision, tasks.RedriveResult{}, err
	}
	return decision, result, nil
}

// RecoverPending sweeps the dead-letter queue and opens every successor it is
// entitled to open, returning one decision per dead letter it considered.
//
// A failure on one dead letter does not abandon the rest: a sweep that stops
// at the first problem leaves recoverable work indefinitely unrecovered
// because of an unrelated neighbour.
func (r Recovery) RecoverPending(ctx context.Context, limit int) ([]RecoveryDecision, error) {
	if limit <= 0 {
		limit = 50
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
func (r Recovery) successorRequest(ctx context.Context, failedTaskID int64, head string) (tasks.CreateRequest, error) {
	policy, err := r.Mission.Resolve(ctx, failedTaskID)
	if err != nil {
		return tasks.CreateRequest{}, err
	}
	policy.TaskID = 0
	policy.BaseSHA = head
	policy, err = policy.Normalize()
	if err != nil {
		return tasks.CreateRequest{}, err
	}
	_, digest, err := policy.MarshalEvidence()
	if err != nil {
		return tasks.CreateRequest{}, err
	}
	failed, err := r.Tasks.GetTask(ctx, failedTaskID)
	if err != nil {
		return tasks.CreateRequest{}, err
	}
	// The successor is requested by whoever requested the mission that
	// failed. Re-attributing it to the recovery machinery would erase the
	// only durable record of who actually wanted this work done.
	requestedBy := r.RequestedBy
	if failed.Task.RequestedByRoleID != nil && *failed.Task.RequestedByRoleID != "" {
		requestedBy = *failed.Task.RequestedByRoleID
	}
	// The idempotency key is the successor policy's own content digest,
	// exactly as Service.Create derives it. Because BaseSHA is part of what
	// is hashed, a successor pinned to a new commit necessarily gets a key
	// distinct from its predecessor's -- the observed change that justifies
	// the episode is the same fact that gives it a separate identity.
	return tasks.CreateRequest{
		OrganizationID:     failed.Task.OrganizationID,
		RequestedByRoleID:  requestedBy,
		AssignedRoleID:     CodeRunnerRole,
		Title:              missionTitle(policy.Objective),
		Instructions:       failed.Task.Instructions,
		AcceptanceCriteria: policy.AcceptanceCriteria,
		IdempotencyKey:     "engineering-mission/" + digest,
		Requirements: []tasks.RequirementSpec{
			{Key: "candidate-artifact", Type: tasks.RequirementArtifact, Description: "sealed engineering candidate", Required: boolPtr(true)},
			{Key: "engineering-required-gates", Type: tasks.RequirementCheck, Description: "all declared engineering gates pass", Required: boolPtr(true)},
			{Key: "review", Type: tasks.RequirementApproval, Description: "independent engineering review", Required: boolPtr(true)},
		},
	}, nil
}

// The task engine service is the intended RecoveryPort. Asserting it here is
// what keeps the port and its only real implementation from drifting apart in
// separate files with nothing comparing them.
var _ RecoveryPort = (*tasks.Service)(nil)

var _ MissionResolver = Service{}
