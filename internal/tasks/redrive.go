package tasks

import (
	"context"
	"fmt"
	"strings"
)

// ErrRecoveryEpisodesExhausted reports that a dead letter's recovery chain has
// already reached its permitted number of successor episodes.
//
// It is deliberately distinct from a bare ErrConflict: exhaustion is the
// governed, expected end of a bounded loop, not a contradiction between two
// callers. A caller that cannot tell them apart would either keep asking
// forever or treat a normal stop as an incident.
var ErrRecoveryEpisodesExhausted = fmt.Errorf("%w: recovery episodes exhausted", ErrConflict)

// RedriveCommand asks for one recovery successor to a dead-lettered task.
//
// It is NOT a retry. A terminal fact stays terminal: the dead-lettered task
// keeps its status, its attempts and its failure forever, and recovery happens
// as a NEW task -- a successor episode -- linked to the dead letter that
// justified it. That is why the command carries a whole CreateRequest rather
// than an ID to revive: the successor is free to differ from its predecessor
// (a different base commit, a narrower objective), and the difference is
// exactly what gives it a chance of a different outcome.
type RedriveCommand struct {
	DeadLetterID int64
	// Successor is the new task to create. Its IdempotencyKey must differ
	// from the failed task's, or the "successor" would resolve to the
	// predecessor row itself and recovery would silently be a no-op.
	Successor CreateRequest
	// ObservedChange states what about the world is different now, such
	// that the successor can plausibly succeed where the predecessor
	// deterministically failed. It is required, and it is durable.
	//
	// Without it, recovery degenerates into re-running the same work
	// against the same conditions until the episode budget is gone --
	// the bounded-loop failure this mechanism exists to avoid,
	// reintroduced one level up. The host decides what counts as a change
	// (for an engineering mission: its pinned base commit is no longer the
	// target's head); the engine only refuses to act without one stated.
	ObservedChange string
	// MaxRecoveryEpisodes bounds the chain. The engine derives how many
	// episodes already happened by walking the durable redrive links, so
	// this bounds real history rather than a counter a caller keeps.
	MaxRecoveryEpisodes int
	ActorType           string
	ActorID             string
}

// RedriveResult reports the successor and where it sits in the recovery chain.
type RedriveResult struct {
	Successor Task
	// Created is false when this dead letter was already redriven: the
	// call is idempotent and returns the successor that already exists.
	Created bool
	// Episode is 1 for the first recovery of an original failure, 2 for a
	// recovery of a recovery, and so on.
	Episode int
}

// RedrivePersistence is implemented by stores that can create the successor
// and stamp the dead letter atomically. It is an optional capability for the
// same reason SpecificClaimPersistence is: not every store has to grow a
// method to keep satisfying Persistence.
type RedrivePersistence interface {
	Redrive(context.Context, PreparedRedrive) (RedriveResult, error)
}

// PreparedRedrive is a validated RedriveCommand whose successor has already
// been prepared exactly as CreateTask would have prepared it.
type PreparedRedrive struct {
	DeadLetterID        int64
	MaxRecoveryEpisodes int
	ObservedChange      string
	Successor           PreparedCreate
	ActorType           string
	ActorID             string
	OutboxMaxAttempts   int
}

// RedriveDeadLetter creates the recovery successor of a dead-lettered task.
func (s *Service) RedriveDeadLetter(ctx context.Context, command RedriveCommand) (RedriveResult, error) {
	if command.DeadLetterID <= 0 {
		return RedriveResult{}, fmt.Errorf("%w: dead-letter ID must be positive", ErrInvalidInput)
	}
	command.ObservedChange = strings.TrimSpace(command.ObservedChange)
	if command.ObservedChange == "" {
		return RedriveResult{}, fmt.Errorf("%w: observed_change is required to justify a recovery episode", ErrInvalidInput)
	}
	if len(command.ObservedChange) > 500 {
		return RedriveResult{}, fmt.Errorf("%w: observed_change must be at most 500 bytes", ErrInvalidInput)
	}
	if command.MaxRecoveryEpisodes < 1 {
		return RedriveResult{}, fmt.Errorf("%w: max_recovery_episodes must be at least 1", ErrInvalidInput)
	}
	redriver, ok := s.persistence.(RedrivePersistence)
	if !ok {
		return RedriveResult{}, fmt.Errorf("%w: persistence does not support recovery successors", ErrInvalidInput)
	}
	// Read through the service so the dead letter's organization is
	// checked before anything is created on behalf of it.
	dead, err := s.GetDeadLetter(ctx, command.DeadLetterID)
	if err != nil {
		return RedriveResult{}, err
	}
	failed, err := s.persistence.GetTask(ctx, dead.TaskID)
	if err != nil {
		return RedriveResult{}, err
	}
	if failed.Task.OrganizationID != s.cfg.OrganizationID {
		return RedriveResult{}, fmt.Errorf("%w: dead letter %d", ErrNotFound, command.DeadLetterID)
	}
	if command.Successor.OrganizationID == "" {
		command.Successor.OrganizationID = failed.Task.OrganizationID
	}
	if command.Successor.OrganizationID != failed.Task.OrganizationID {
		return RedriveResult{}, fmt.Errorf("%w: successor must belong to the failed task's organization", ErrInvalidInput)
	}
	if strings.TrimSpace(command.Successor.IdempotencyKey) == failed.Task.IdempotencyKey {
		return RedriveResult{}, fmt.Errorf("%w: successor idempotency key must differ from the failed task's", ErrInvalidInput)
	}
	prepared, err := s.prepareCreate(ctx, command.Successor, command.ActorType, command.ActorID)
	if err != nil {
		return RedriveResult{}, err
	}
	return redriver.Redrive(ctx, PreparedRedrive{
		DeadLetterID:        command.DeadLetterID,
		MaxRecoveryEpisodes: command.MaxRecoveryEpisodes,
		ObservedChange:      command.ObservedChange,
		Successor:           prepared,
		ActorType:           prepared.ActorType,
		ActorID:             prepared.ActorID,
		OutboxMaxAttempts:   s.cfg.OutboxMaxAttempts,
	})
}
