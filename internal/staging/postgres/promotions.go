package postgres

import (
	"context"
	"fmt"

	"github.com/Mireuz13/explorarte-organization/internal/staging"
	"github.com/jackc/pgx/v5"
)

func (s *Store) RecordCheck(ctx context.Context, command staging.RecordCheckCommand) (staging.Check, error) {
	return withTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) (staging.Check, error) {
		workspace, err := lockWorkspace(ctx, tx, command.WorkspaceID)
		if err != nil {
			return staging.Check{}, err
		}
		if workspace.Status != staging.WorkspaceSealed {
			return staging.Check{}, staging.ErrWorkspaceNotActive
		}
		var requirementType, requirementStatus string
		if err := tx.QueryRow(ctx, `SELECT requirement_type,status FROM task_requirements WHERE id=$1 AND task_id=$2 FOR UPDATE`, command.RequirementID, workspace.TaskID).Scan(&requirementType, &requirementStatus); err != nil {
			return staging.Check{}, mapError(err)
		}
		if requirementType != "check" || requirementStatus != "pending" {
			return staging.Check{}, staging.ErrConflict
		}
		result := "satisfied"
		eventType := "staging.check_passed"
		if command.Status == staging.CheckFailed {
			result = "failed"
			eventType = "staging.check_failed"
		} else if command.Status != staging.CheckPassed {
			return staging.Check{}, staging.ErrInvalidInput
		}
		var value staging.Check
		if err := tx.QueryRow(ctx, `INSERT INTO staging_checks(workspace_id,task_id,requirement_id,name,status,reference,digest,actor_role_id) VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,''),$8) RETURNING id,workspace_id,task_id,requirement_id,name,status,reference,digest,actor_role_id,created_at`, workspace.ID, workspace.TaskID, command.RequirementID, command.Name, command.Status, command.Reference, command.Digest, command.ActorRoleID).Scan(&value.ID, &value.WorkspaceID, &value.TaskID, &value.RequirementID, &value.Name, &value.Status, &value.Reference, &value.Digest, &value.ActorRoleID, &value.CreatedAt); err != nil {
			return staging.Check{}, mapError(err)
		}
		if err := recordTaskRequirement(ctx, tx, workspace.TaskID, command.RequirementID, result, command.Reference, command.Digest, command.ActorRoleID, map[string]any{"workspace_id": workspace.ID, "check_name": command.Name}, s.outboxMaxAttempts); err != nil {
			return staging.Check{}, err
		}
		if err := appendEvent(ctx, tx, "workspace", workspace.ID, eventType, "role", command.ActorRoleID, map[string]any{"repository_id": workspace.RepositoryID, "task_id": workspace.TaskID, "attempt_id": workspace.AttemptID, "workspace_id": workspace.ID}, s.outboxMaxAttempts); err != nil {
			return staging.Check{}, err
		}
		if command.Status == staging.CheckPassed {
			if _, err := maybeApprovePromotionForWorkspace(ctx, tx, workspace, command.ActorRoleID, s.outboxMaxAttempts); err != nil {
				return staging.Check{}, err
			}
		}
		return value, nil
	})
}

func (s *Store) RequestPromotion(ctx context.Context, command staging.RequestPromotionCommand) (staging.Promotion, error) {
	return withTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) (staging.Promotion, error) {
		workspace, err := lockWorkspace(ctx, tx, command.WorkspaceID)
		if err != nil {
			return staging.Promotion{}, err
		}
		if workspace.Status != staging.WorkspaceSealed || workspace.CandidateCommit == nil || workspace.CandidateTree == nil {
			return staging.Promotion{}, staging.ErrWorkspaceNotActive
		}
		var taskStatus string
		if err := tx.QueryRow(ctx, `SELECT status FROM tasks WHERE id=$1 FOR UPDATE`, workspace.TaskID).Scan(&taskStatus); err != nil {
			return staging.Promotion{}, mapError(err)
		}
		if taskStatus != "awaiting_verification" {
			return staging.Promotion{}, fmt.Errorf("%w: task must await verification", staging.ErrInvalidTransition)
		}
		var artifactCount, checkCount, approvalCount, artifactSatisfied, failed int
		if err := tx.QueryRow(ctx, `SELECT COUNT(*) FILTER(WHERE requirement_type='artifact' AND required),COUNT(*) FILTER(WHERE requirement_type='check' AND required),COUNT(*) FILTER(WHERE requirement_type='approval' AND required),COUNT(*) FILTER(WHERE requirement_type='artifact' AND required AND status='satisfied'),COUNT(*) FILTER(WHERE required AND requirement_type IN ('artifact','check','approval') AND status='failed') FROM task_requirements WHERE task_id=$1`, workspace.TaskID).Scan(&artifactCount, &checkCount, &approvalCount, &artifactSatisfied, &failed); err != nil {
			return staging.Promotion{}, mapError(err)
		}
		if artifactCount < 1 || checkCount < 1 || approvalCount < 1 || artifactSatisfied < artifactCount || failed > 0 {
			return staging.Promotion{}, staging.ErrPromotionGatesUnsatisfied
		}
		promotion, err := scanPromotion(tx.QueryRow(ctx, `INSERT INTO staging_promotions(workspace_id,task_id,repository_id,target_ref,expected_base_commit,candidate_commit,status,requested_by_role_id) VALUES($1,$2,$3,$4,$5,$6,'requested',$7) RETURNING `+promotionColumns, workspace.ID, workspace.TaskID, workspace.RepositoryID, workspace.TargetRef, workspace.BaseCommit, *workspace.CandidateCommit, command.ActorRoleID))
		if err != nil {
			return staging.Promotion{}, err
		}
		basePayload := map[string]any{"repository_id": promotion.RepositoryID, "task_id": promotion.TaskID, "workspace_id": promotion.WorkspaceID, "promotion_id": promotion.ID, "candidate_commit": promotion.CandidateCommit, "expected_base_commit": promotion.ExpectedBaseCommit}
		if err := appendEvent(ctx, tx, "promotion", promotion.ID, "staging.promotion_requested", "role", command.ActorRoleID, basePayload, s.outboxMaxAttempts); err != nil {
			return staging.Promotion{}, err
		}
		promotion, err = scanPromotion(tx.QueryRow(ctx, `UPDATE staging_promotions SET status='awaiting_gates',version=version+1,updated_at=clock_timestamp() WHERE id=$1 RETURNING `+promotionColumns, promotion.ID))
		if err != nil {
			return staging.Promotion{}, err
		}
		return promotion, nil
	})
}

func (s *Store) SubmitReview(ctx context.Context, command staging.SubmitReviewCommand, selfApprovalAllowed bool) (staging.Promotion, error) {
	return withTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) (staging.Promotion, error) {
		promotion, err := lockPromotion(ctx, tx, command.PromotionID)
		if err != nil {
			return staging.Promotion{}, err
		}
		if promotion.Status != staging.PromotionAwaitingGates {
			return staging.Promotion{}, staging.ErrInvalidTransition
		}
		workspace, err := lockWorkspace(ctx, tx, promotion.WorkspaceID)
		if err != nil {
			return staging.Promotion{}, err
		}
		if workspace.Status != staging.WorkspaceSealed {
			return staging.Promotion{}, staging.ErrWorkspaceNotActive
		}
		if workspace.ActorRoleID == command.ActorRoleID && !selfApprovalAllowed {
			return staging.Promotion{}, fmt.Errorf("%w: candidate author cannot self-approve", staging.ErrCapabilityDenied)
		}
		if command.Decision != staging.ReviewApprove && command.Decision != staging.ReviewReject {
			return staging.Promotion{}, staging.ErrInvalidInput
		}
		var requirementType, status string
		if err := tx.QueryRow(ctx, `SELECT requirement_type,status FROM task_requirements WHERE id=$1 AND task_id=$2 FOR UPDATE`, command.RequirementID, promotion.TaskID).Scan(&requirementType, &status); err != nil {
			return staging.Promotion{}, mapError(err)
		}
		if requirementType != "approval" || status != "pending" {
			return staging.Promotion{}, staging.ErrConflict
		}
		var priorDecision string
		priorErr := tx.QueryRow(ctx, `SELECT decision FROM staging_reviews WHERE promotion_id=$1 AND actor_role_id=$2 ORDER BY created_at,id LIMIT 1`, promotion.ID, command.ActorRoleID).Scan(&priorDecision)
		if priorErr != nil && priorErr != pgx.ErrNoRows {
			return staging.Promotion{}, mapError(priorErr)
		}
		if priorErr == nil && priorDecision != string(command.Decision) {
			return staging.Promotion{}, fmt.Errorf("%w: actor cannot both approve and reject one promotion", staging.ErrConflict)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO staging_reviews(promotion_id,requirement_id,decision,actor_role_id,reason,reference,digest) VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,''))`, promotion.ID, command.RequirementID, command.Decision, command.ActorRoleID, command.Reason, command.Reference, command.Digest); err != nil {
			return staging.Promotion{}, mapError(err)
		}
		result := "satisfied"
		if command.Decision == staging.ReviewReject {
			result = "failed"
		}
		if err := recordTaskRequirement(ctx, tx, promotion.TaskID, command.RequirementID, result, command.Reference, command.Digest, command.ActorRoleID, map[string]any{"promotion_id": promotion.ID, "decision": command.Decision, "reason": command.Reason}, s.outboxMaxAttempts); err != nil {
			return staging.Promotion{}, err
		}
		payload := map[string]any{"repository_id": promotion.RepositoryID, "task_id": promotion.TaskID, "workspace_id": promotion.WorkspaceID, "promotion_id": promotion.ID, "candidate_commit": promotion.CandidateCommit}
		if command.Decision == staging.ReviewReject {
			promotion, err = scanPromotion(tx.QueryRow(ctx, `UPDATE staging_promotions SET status='rejected',status_reason_code='review_rejected',status_reason=$2,rejected_at=clock_timestamp(),version=version+1,updated_at=clock_timestamp() WHERE id=$1 RETURNING `+promotionColumns, promotion.ID, command.Reason))
			if err != nil {
				return staging.Promotion{}, err
			}
			payload["reason_code"] = "review_rejected"
			if err := appendEvent(ctx, tx, "promotion", promotion.ID, "staging.promotion_rejected", "role", command.ActorRoleID, payload, s.outboxMaxAttempts); err != nil {
				return staging.Promotion{}, err
			}
			return promotion, nil
		}
		promotion, err = maybeApprovePromotionForWorkspace(ctx, tx, workspace, command.ActorRoleID, s.outboxMaxAttempts)
		if err != nil {
			return staging.Promotion{}, err
		}
		return promotion, nil
	})
}

func maybeApprovePromotionForWorkspace(ctx context.Context, tx pgx.Tx, workspace staging.Workspace, gateActor string, outboxMax int) (staging.Promotion, error) {
	promotion, err := scanPromotion(tx.QueryRow(ctx, `SELECT `+promotionColumns+` FROM staging_promotions WHERE workspace_id=$1 AND status='awaiting_gates' FOR UPDATE`, workspace.ID))
	if err != nil {
		if err == staging.ErrWorkspaceNotFound {
			return staging.Promotion{}, nil
		}
		return staging.Promotion{}, err
	}
	var unsatisfied int
	if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM task_requirements WHERE task_id=$1 AND required AND requirement_type IN ('artifact','check','approval') AND status<>'satisfied'`, promotion.TaskID).Scan(&unsatisfied); err != nil {
		return staging.Promotion{}, mapError(err)
	}
	if unsatisfied != 0 {
		return promotion, nil
	}
	var approver string
	if err := tx.QueryRow(ctx, `SELECT actor_role_id FROM staging_reviews WHERE promotion_id=$1 AND decision='approve' ORDER BY created_at,id LIMIT 1`, promotion.ID).Scan(&approver); err != nil {
		return staging.Promotion{}, mapError(err)
	}
	promotion, err = scanPromotion(tx.QueryRow(ctx, `UPDATE staging_promotions SET status='approved',approved_by_role_id=$2,approved_at=clock_timestamp(),version=version+1,updated_at=clock_timestamp() WHERE id=$1 AND status='awaiting_gates' RETURNING `+promotionColumns, promotion.ID, approver))
	if err != nil {
		return staging.Promotion{}, err
	}
	payload := map[string]any{"repository_id": promotion.RepositoryID, "task_id": promotion.TaskID, "workspace_id": promotion.WorkspaceID, "promotion_id": promotion.ID, "candidate_commit": promotion.CandidateCommit}
	if err := appendEvent(ctx, tx, "promotion", promotion.ID, "staging.promotion_gates_satisfied", "role", gateActor, payload, outboxMax); err != nil {
		return staging.Promotion{}, err
	}
	if err := appendEvent(ctx, tx, "promotion", promotion.ID, "staging.promotion_approved", "role", approver, payload, outboxMax); err != nil {
		return staging.Promotion{}, err
	}
	return promotion, nil
}

func (s *Store) GetPromotion(ctx context.Context, id int64) (staging.Promotion, error) {
	return scanPromotion(s.pool.QueryRow(ctx, `SELECT `+promotionColumns+` FROM staging_promotions WHERE id=$1`, id))
}
func (s *Store) ListPromotions(ctx context.Context, filter staging.PromotionFilter) ([]staging.Promotion, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `SELECT `+promotionColumns+` FROM staging_promotions WHERE ($1='' OR status=$1) ORDER BY created_at DESC,id DESC LIMIT $2`, string(filter.Status), limit)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var result []staging.Promotion
	for rows.Next() {
		value, err := scanPromotion(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, mapError(rows.Err())
}

func (s *Store) GetPromotionApplyContext(ctx context.Context, id int64) (staging.PromotionApplyContext, error) {
	return withTx(ctx, s.pool, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) (staging.PromotionApplyContext, error) {
		promotion, err := scanPromotion(tx.QueryRow(ctx, `SELECT `+promotionColumns+` FROM staging_promotions WHERE id=$1`, id))
		if err != nil {
			return staging.PromotionApplyContext{}, err
		}
		workspace, err := scanWorkspace(tx.QueryRow(ctx, `SELECT `+workspaceColumns+` FROM staging_workspaces WHERE id=$1`, promotion.WorkspaceID))
		if err != nil {
			return staging.PromotionApplyContext{}, err
		}
		if promotion.Status != staging.PromotionApproved || workspace.Status != staging.WorkspaceSealed {
			return staging.PromotionApplyContext{}, staging.ErrPromotionGatesUnsatisfied
		}
		var taskStatus string
		var unsatisfied int
		if err := tx.QueryRow(ctx, `SELECT status FROM tasks WHERE id=$1`, promotion.TaskID).Scan(&taskStatus); err != nil {
			return staging.PromotionApplyContext{}, mapError(err)
		}
		if taskStatus != "awaiting_verification" {
			return staging.PromotionApplyContext{}, staging.ErrInvalidTransition
		}
		if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM task_requirements WHERE task_id=$1 AND required AND requirement_type IN ('artifact','check','approval') AND status<>'satisfied'`, promotion.TaskID).Scan(&unsatisfied); err != nil {
			return staging.PromotionApplyContext{}, mapError(err)
		}
		if unsatisfied > 0 {
			return staging.PromotionApplyContext{}, staging.ErrPromotionGatesUnsatisfied
		}
		rows, err := tx.Query(ctx, `SELECT wa.kind,a.storage_key FROM staging_workspace_artifacts wa JOIN staging_artifacts a ON a.id=wa.artifact_id WHERE wa.workspace_id=$1 AND wa.kind IN ('manifest','binary_patch') ORDER BY wa.kind`, workspace.ID)
		if err != nil {
			return staging.PromotionApplyContext{}, mapError(err)
		}
		defer rows.Close()
		var manifestRef, patchRef string
		for rows.Next() {
			var kind, ref string
			if err := rows.Scan(&kind, &ref); err != nil {
				return staging.PromotionApplyContext{}, mapError(err)
			}
			switch kind {
			case "manifest":
				manifestRef = ref
			case "binary_patch":
				patchRef = ref
			}
		}
		if err := rows.Err(); err != nil {
			return staging.PromotionApplyContext{}, mapError(err)
		}
		if manifestRef == "" || patchRef == "" {
			return staging.PromotionApplyContext{}, staging.ErrArtifactCorrupt
		}
		return staging.PromotionApplyContext{Promotion: promotion, Workspace: workspace, ManifestReference: manifestRef, PatchReference: patchRef}, nil
	})
}

func (s *Store) MarkPromotionApplied(ctx context.Context, id int64, actor string) (staging.Promotion, error) {
	return s.transitionPromotion(ctx, id, actor, staging.PromotionApplied, "staging.promotion_applied", "", "")
}
func (s *Store) MarkPromotionConflicted(ctx context.Context, id int64, code, reason string) (staging.Promotion, error) {
	return s.transitionPromotion(ctx, id, "reconciler", staging.PromotionConflicted, "staging.promotion_conflicted", code, reason)
}
func (s *Store) MarkPromotionFailed(ctx context.Context, id int64, code, reason string) (staging.Promotion, error) {
	return s.transitionPromotion(ctx, id, "reconciler", staging.PromotionFailed, "staging.promotion_failed", code, reason)
}
func (s *Store) CancelPromotion(ctx context.Context, command staging.CancelPromotionCommand) (staging.Promotion, error) {
	return s.transitionPromotion(ctx, command.PromotionID, command.ActorRoleID, staging.PromotionCancelled, "staging.promotion_cancelled", command.ReasonCode, command.Reason)
}

func (s *Store) transitionPromotion(ctx context.Context, id int64, actor string, target staging.PromotionStatus, event, code, reason string) (staging.Promotion, error) {
	return withTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) (staging.Promotion, error) {
		promotion, err := lockPromotion(ctx, tx, id)
		if err != nil {
			return staging.Promotion{}, err
		}
		if promotion.Status == target {
			return promotion, nil
		}
		if err := staging.ValidatePromotionTransition(promotion.Status, target); err != nil {
			return staging.Promotion{}, err
		}
		promotion, err = scanPromotion(tx.QueryRow(ctx, `UPDATE staging_promotions SET status=$2,status_reason_code=NULLIF($3,''),status_reason=NULLIF($4,''),applied_at=CASE WHEN $2='applied' THEN clock_timestamp() ELSE applied_at END,rejected_at=CASE WHEN $2='rejected' THEN clock_timestamp() ELSE rejected_at END,cancelled_at=CASE WHEN $2='cancelled' THEN clock_timestamp() ELSE cancelled_at END,version=version+1,updated_at=clock_timestamp() WHERE id=$1 RETURNING `+promotionColumns, id, target, code, reason))
		if err != nil {
			return staging.Promotion{}, err
		}
		payload := map[string]any{"repository_id": promotion.RepositoryID, "task_id": promotion.TaskID, "workspace_id": promotion.WorkspaceID, "promotion_id": promotion.ID, "candidate_commit": promotion.CandidateCommit, "expected_base_commit": promotion.ExpectedBaseCommit}
		if code != "" {
			payload["reason_code"] = code
		}
		if err := appendEvent(ctx, tx, "promotion", promotion.ID, event, "role", actor, payload, s.outboxMaxAttempts); err != nil {
			return staging.Promotion{}, err
		}
		return promotion, nil
	})
}

func (s *Store) ListRecoverablePromotions(ctx context.Context, limit int) ([]staging.PromotionApplyContext, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `SELECT id FROM staging_promotions WHERE status='approved' ORDER BY updated_at,id LIMIT $1`, limit)
	if err != nil {
		return nil, mapError(err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, mapError(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, mapError(err)
	}
	rows.Close()
	result := make([]staging.PromotionApplyContext, 0, len(ids))
	for _, id := range ids {
		value, err := s.GetPromotionApplyContext(ctx, id)
		if err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, nil
}
