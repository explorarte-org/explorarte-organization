package staging

import "fmt"

func ValidateWorkspaceTransition(from, to WorkspaceStatus) error {
	if !from.Valid() || !to.Valid() {
		return fmt.Errorf("%w: unknown workspace state", ErrInvalidTransition)
	}
	allowed := map[WorkspaceStatus]map[WorkspaceStatus]bool{
		WorkspaceProvisioning:   {WorkspaceActive: true, WorkspaceFailed: true},
		WorkspaceActive:         {WorkspaceSealed: true, WorkspaceAbandoned: true, WorkspaceFailed: true},
		WorkspaceSealed:         {WorkspaceCleanupPending: true},
		WorkspaceAbandoned:      {WorkspaceCleanupPending: true},
		WorkspaceFailed:         {WorkspaceCleanupPending: true},
		WorkspaceCleanupPending: {WorkspaceCleaned: true, WorkspaceFailed: true},
	}
	if !allowed[from][to] {
		return fmt.Errorf("%w: workspace %s -> %s", ErrInvalidTransition, from, to)
	}
	return nil
}

func ValidatePromotionTransition(from, to PromotionStatus) error {
	if !from.Valid() || !to.Valid() {
		return fmt.Errorf("%w: unknown promotion state", ErrInvalidTransition)
	}
	allowed := map[PromotionStatus]map[PromotionStatus]bool{
		PromotionRequested:     {PromotionAwaitingGates: true, PromotionCancelled: true, PromotionFailed: true},
		PromotionAwaitingGates: {PromotionApproved: true, PromotionRejected: true, PromotionCancelled: true, PromotionFailed: true},
		PromotionApproved:      {PromotionApplied: true, PromotionConflicted: true, PromotionFailed: true, PromotionCancelled: true},
	}
	if !allowed[from][to] {
		return fmt.Errorf("%w: promotion %s -> %s", ErrInvalidTransition, from, to)
	}
	return nil
}
