package staging

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

func (s *Service) Reconcile(ctx context.Context, batch int) (ReconcileResult, error) {
	if batch < 1 || batch > 500 {
		return ReconcileResult{}, ErrInvalidInput
	}
	var result ReconcileResult
	stale, err := s.persistence.ListStaleWorkspaces(ctx, s.cfg.StaleAfter, batch)
	if err != nil {
		return result, err
	}
	for _, workspace := range stale {
		inspection, inspectErr := s.git.InspectWorktree(ctx, s.workspaceRef(workspace))
		if inspectErr != nil && !errors.Is(inspectErr, ErrWorkspaceMissing) {
			continue
		}
		if !inspection.Exists {
			if _, markErr := s.persistence.FailWorkspace(ctx, workspace.ID, "staging-reconciler", "workspace_missing", "workspace directory is missing"); markErr == nil {
				result.MissingWorkspaces++
			}
			continue
		}
		// Recover the crash window after git worktree add but before the database
		// transition to active. This does not create or modify a worktree.
		if workspace.Status == WorkspaceProvisioning {
			if inspection.Clean && inspection.HeadCommit == workspace.BaseCommit {
				if _, activateErr := s.persistence.ActivateWorkspace(ctx, workspace.ID, workspace.Version, workspace.WorkspaceRef, "staging-reconciler"); activateErr == nil {
					result.RecoveredWorkspaces++
				}
			} else {
				_, _ = s.persistence.FailWorkspace(ctx, workspace.ID, "staging-reconciler", "provisioning_workspace_mismatch", "provisioning workspace differs from the requested base")
			}
		}
	}
	known, err := s.persistence.ListWorkspaceKeys(ctx)
	if err != nil {
		return result, err
	}
	entries, err := os.ReadDir(s.cfg.WorkspaceRoot)
	if err != nil {
		return result, err
	}
	for _, entry := range entries {
		if result.QuarantinedDirectories >= batch {
			break
		}
		if entry.Name() == ".control" {
			continue
		}
		if _, ok := known[entry.Name()]; ok {
			continue
		}
		if ValidateWorkspaceKey(entry.Name()) != nil {
			continue
		}
		source := filepath.Join(s.cfg.WorkspaceRoot, entry.Name())
		info, statErr := os.Lstat(source)
		if statErr != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			continue
		}
		destination := filepath.Join(s.cfg.QuarantineRoot, entry.Name()+"-"+strconv.FormatInt(time.Now().UTC().UnixNano(), 10))
		if err := PathWithin(s.cfg.QuarantineRoot, destination); err != nil {
			continue
		}
		if err := os.Rename(source, destination); err == nil {
			result.QuarantinedDirectories++
		}
	}
	promotions, err := s.persistence.ListRecoverablePromotions(ctx, batch)
	if err != nil {
		return result, err
	}
	for _, item := range promotions {
		repository, hash, getErr := s.catalog.Get(ctx, item.Promotion.RepositoryID)
		if getErr != nil || hash != item.Workspace.RepositoryConfigHash {
			continue
		}
		current, readErr := s.git.ReadTarget(ctx, repository, item.Promotion.TargetRef)
		if readErr != nil {
			continue
		}
		if current == item.Promotion.CandidateCommit {
			if _, markErr := s.persistence.MarkPromotionApplied(ctx, item.Promotion.ID, "staging-reconciler"); markErr == nil {
				result.RecoveredPromotions++
			}
			continue
		}
		if current != item.Promotion.ExpectedBaseCommit {
			if _, markErr := s.persistence.MarkPromotionConflicted(ctx, item.Promotion.ID, "target_moved", "target ref differs from expected base during reconciliation"); markErr == nil {
				result.ConflictedPromotions++
			}
		}
	}
	pending, err := s.persistence.ListCleanupPending(ctx, batch)
	if err != nil {
		return result, err
	}
	for _, workspace := range pending {
		inspection, inspectErr := s.git.InspectWorktree(ctx, s.workspaceRef(workspace))
		if inspectErr != nil {
			continue
		}
		if !inspection.Exists {
			if _, completeErr := s.persistence.CompleteCleanup(ctx, workspace.ID, "staging-reconciler"); completeErr == nil {
				result.RecoveredCleanup++
			}
		}
	}
	return result, nil
}

func (r ReconcileResult) Changed() bool {
	return r.RecoveredWorkspaces+r.MissingWorkspaces+r.QuarantinedDirectories+r.RecoveredPromotions+r.ConflictedPromotions+r.RecoveredCleanup > 0
}
func (r ReconcileResult) String() string {
	return fmt.Sprintf("recovered_workspaces=%d missing=%d quarantined=%d recovered_promotions=%d conflicted=%d cleanup=%d", r.RecoveredWorkspaces, r.MissingWorkspaces, r.QuarantinedDirectories, r.RecoveredPromotions, r.ConflictedPromotions, r.RecoveredCleanup)
}

var _ StagingReconciler = (*Service)(nil)
