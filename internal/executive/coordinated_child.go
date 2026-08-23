package executive

import (
	"context"
	"fmt"
	"time"
)

// A child task is created by one role for another, and it needs two things
// from its creator before it can legitimately run: a share of the run's budget
// tree, and, when it crosses a role boundary, a durable delegation edge
// authorizing the hand-off.
//
// Creating the task and providing those two things cannot happen in one
// transaction -- tasks, agent budgets and agent messaging are separate stores
// and fusing them would be a distributed transaction bought to solve an
// ordering problem. But an independent child is born ready, and ready means
// claimable, so any worker polling in the window between the two could lease a
// child that has no budget to spend and no delegation authorizing it. Making
// that window narrow does not close it; only ordering does.
//
// So creation is ordered behind a durable publication barrier:
//
//	create the child HELD          -- durable, not claimable
//	ensure the coordination it needs -- idempotent, retryable
//	release the hold               -- now it enters the ordinary lifecycle
//
// The atomicity that matters is not that the three stores commit together. It
// is that before the release the child cannot execute, and after it every
// obligation is already durable. That turns a multi-store sequence into a
// recoverable one.
//
// coordinatedChildren owns that sequence. The Orchestrator asks for a
// coordinated child and does not spell out which store participates or in what
// order, because that ordering IS the invariant and it must have exactly one
// implementation.
type coordinatedChildren struct {
	tasks    childTaskStore
	budgets  childBudgetAttachment
	messages childDelegationSender
	clock    Clock
}

// The three ports below are deliberately narrower than the Orchestrator's own.
// This collaborator creates a child, attaches its budget, sends its delegation
// and publishes it. It has no business being able to claim, finalize, block or
// send completions, and an interface offering those would invite exactly that.
// The Orchestrator satisfies all three with the providers it already holds.
type childTaskStore interface {
	CreateTask(context.Context, CreateTaskCommand) (TaskRecord, bool, error)
	ReleaseCoordinationHold(context.Context, int64) (TaskRecord, error)
}

type childBudgetAttachment interface {
	InheritForChild(ctx context.Context, root, child TaskRecord, depth int64, now time.Time) error
}

type childDelegationSender interface {
	SendDelegation(ctx context.Context, sender, recipient TaskRecord, now time.Time) error
}

func (o *Orchestrator) coordinatedChildren() coordinatedChildren {
	// The optional providers are narrowed through explicit nil checks rather
	// than assigned directly: a nil AgentBudgetProvider assigned into a
	// narrower interface produces a non-nil interface holding a nil value,
	// and "no provider configured" would quietly stop meaning what it says.
	children := coordinatedChildren{tasks: o.tasks, clock: o.clock}
	if o.budgets != nil {
		children.budgets = o.budgets
	}
	if o.messages != nil {
		children.messages = o.messages
	}
	return children
}

// childRequest is one child, described completely.
//
// Root, Sender and Depth are not decoration: Root identifies the budget tree
// the child inherits from, Sender is the role handing work over and therefore
// the author of the delegation edge, and Depth is the child's position in the
// budget tree. They are the coordination, expressed as data, which is what
// lets a single Create serve every phase.
type childRequest struct {
	Root    TaskRecord
	Sender  TaskRecord
	Depth   int64
	Command CreateTaskCommand
}

// Materialize returns a child that is durable, fully coordinated and
// published, or an error. It never returns a task that is claimable but
// uncoordinated.
//
// The name is the one this codebase already uses for turning a plan into
// durable tasks (materializeWorkerTasks), and it is accurate here: creation is
// only the first of three steps, and a child that was created but not
// published has not been materialized.
//
// The second return value is the port's "reused" flag, preserved so callers
// can still tell a fresh child from a resumed one.
//
// Every step is idempotent, so a resumed run repeats the whole sequence
// without producing a second effect: CreateTask reuses by idempotency key,
// budget inheritance is keyed on the child task, the delegation message
// carries the child's own idempotency key, and releasing an already-published
// child is a no-op. That is what makes each crash window recoverable by simply
// running this again:
//
//	crash after create            -- the held child is found and coordinated
//	crash after budget            -- inheritance is not re-debited
//	crash after delegation        -- the message is not re-sent
//	crash before release          -- the child is still held, and gets released
//	crash after release           -- nothing is held, nothing is redone
func (c coordinatedChildren) Materialize(ctx context.Context, request childRequest) (TaskRecord, bool, error) {
	// The hold is set here rather than by callers. A caller that could
	// choose to skip it would be a caller that could reintroduce the race,
	// which is why this is the only place the flag is ever set.
	request.Command.HoldForCoordination = true

	child, reused, err := c.tasks.CreateTask(ctx, request.Command)
	if err != nil {
		return TaskRecord{}, false, err
	}
	// A failure below leaves the child durable and unpublished. That is the
	// correct outcome, not a leak: the creation genuinely happened, and the
	// next attempt finds it and finishes the sequence. Rolling the task back
	// would discard durable truth to tidy up a retryable failure.
	if err = c.ensureCoordination(ctx, request, child); err != nil {
		return TaskRecord{}, false, err
	}
	published, err := c.tasks.ReleaseCoordinationHold(ctx, child.ID)
	if err != nil {
		return TaskRecord{}, false, fmt.Errorf("publish coordinated child task %d: %w", child.ID, err)
	}
	return published, reused, nil
}

// ensureCoordination makes every obligation this child actually has durable.
//
// Which obligations exist is decided by what is configured and by who is
// handing work to whom, never by a default. An unconfigured provider creates
// no obligation, and a role creating its own sub-task creates no delegation.
func (c coordinatedChildren) ensureCoordination(ctx context.Context, request childRequest, child TaskRecord) error {
	now := c.clock.Now()
	if c.budgets != nil {
		if err := c.budgets.InheritForChild(ctx, request.Root, child, request.Depth, now); err != nil {
			return fmt.Errorf("inherit agent budget for task %d: %w", child.ID, err)
		}
	}
	if c.messages != nil && c.delegates(request, child) {
		if err := c.messages.SendDelegation(ctx, request.Sender, child, now); err != nil {
			return fmt.Errorf("send delegation message for task %d: %w", child.ID, err)
		}
	}
	return nil
}

// delegates reports whether this hand-off crosses a role boundary.
//
// A role creating its own sub-task (the CEO's root task spawning its own CEO
// planning task, for instance) crosses no organizational trust boundary:
// nothing is being handed to a different actor, so there is nothing for
// agent-messaging to authenticate. agentmessaging's topology validator denies
// senderRole==recipientRole unconditionally, so this is not a gap being routed
// around -- it is the reason a same-role hop must never reach SendDelegation
// at all, and why no delegation must ever be invented for one.
func (c coordinatedChildren) delegates(request childRequest, child TaskRecord) bool {
	return request.Sender.AssignedRoleID != child.AssignedRoleID
}
