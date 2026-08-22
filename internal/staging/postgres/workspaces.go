package postgres

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/staging"
	"github.com/jackc/pgx/v5"
)

func (s *Store) CreateProvisioning(ctx context.Context, command staging.PreparedWorkspace) (staging.Workspace, error) {
	return withTx(ctx, s.pool, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(tx pgx.Tx) (staging.Workspace, error) {
		var taskStatus, attemptState, requirementType, requirementStatus string
		var requirementRequired bool
		var org string
		var revision int64
		if err := tx.QueryRow(ctx, `SELECT t.organization_id,t.organization_revision_id,t.status,a.state,r.requirement_type,r.status,r.required FROM tasks t JOIN task_attempts a ON a.task_id=t.id AND a.id=$2 JOIN task_requirements r ON r.task_id=t.id AND r.id=$3 WHERE t.id=$1 FOR UPDATE`, command.TaskID, command.AttemptID, command.ArtifactRequirementID).Scan(&org, &revision, &taskStatus, &attemptState, &requirementType, &requirementStatus, &requirementRequired); err != nil {
			return staging.Workspace{}, mapError(err)
		}
		if org != command.OrganizationID || revision != command.OrganizationRevisionID || taskStatus != "running" || attemptState != "running" {
			return staging.Workspace{}, fmt.Errorf("%w: task attempt is not an active running execution", staging.ErrLeaseInvalid)
		}
		if requirementType != "artifact" || requirementStatus != "pending" || !requirementRequired {
			return staging.Workspace{}, fmt.Errorf("%w: artifact requirement must be pending", staging.ErrInvalidInput)
		}
		workspace, err := scanWorkspace(tx.QueryRow(ctx, `INSERT INTO staging_workspaces(organization_id,organization_revision_id,task_id,attempt_id,repository_id,repository_config_hash,workspace_key,workspace_ref,base_commit,target_ref,status,holder_id,actor_role_id,artifact_requirement_id) VALUES($1,$2,$3,$4,$5,$6,$7,'',$8,$9,'provisioning',$10,$11,$12) RETURNING `+workspaceColumns, command.OrganizationID, command.OrganizationRevisionID, command.TaskID, command.AttemptID, command.RepositoryID, command.RepositoryConfigHash, command.WorkspaceKey, command.BaseCommit, command.TargetRef, command.HolderID, command.ActorRoleID, command.ArtifactRequirementID))
		if err != nil {
			return staging.Workspace{}, err
		}
		workspaceRef := "refs/explorarte/workspaces/" + strconv.FormatInt(workspace.ID, 10)
		workspace, err = scanWorkspace(tx.QueryRow(ctx, `UPDATE staging_workspaces SET workspace_ref=$2,version=version+1,updated_at=clock_timestamp() WHERE id=$1 RETURNING `+workspaceColumns, workspace.ID, workspaceRef))
		if err != nil {
			return staging.Workspace{}, err
		}
		payload := map[string]any{"repository_id": workspace.RepositoryID, "task_id": workspace.TaskID, "attempt_id": workspace.AttemptID, "workspace_id": workspace.ID}
		if err := appendEvent(ctx, tx, "workspace", workspace.ID, "staging.workspace_created", "role", command.ActorRoleID, payload, s.outboxMaxAttempts); err != nil {
			return staging.Workspace{}, err
		}
		return workspace, nil
	})
}

func (s *Store) ActivateWorkspace(ctx context.Context, id, version int64, workspaceRef, actor string) (staging.Workspace, error) {
	return withTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) (staging.Workspace, error) {
		workspace, err := lockWorkspace(ctx, tx, id)
		if err != nil {
			return staging.Workspace{}, err
		}
		if workspace.Version != version || workspace.Status != staging.WorkspaceProvisioning {
			return staging.Workspace{}, staging.ErrConflict
		}
		if workspace.WorkspaceRef != workspaceRef {
			return staging.Workspace{}, staging.ErrConflict
		}
		workspace, err = scanWorkspace(tx.QueryRow(ctx, `UPDATE staging_workspaces SET status='active',version=version+1,updated_at=clock_timestamp() WHERE id=$1 AND version=$2 RETURNING `+workspaceColumns, id, version))
		if err != nil {
			return staging.Workspace{}, err
		}
		if err := appendEvent(ctx, tx, "workspace", id, "staging.workspace_activated", "role", actor, map[string]any{"repository_id": workspace.RepositoryID, "task_id": workspace.TaskID, "attempt_id": workspace.AttemptID, "workspace_id": id}, s.outboxMaxAttempts); err != nil {
			return staging.Workspace{}, err
		}
		return workspace, nil
	})
}

func (s *Store) FailWorkspace(ctx context.Context, id int64, actor, code, reason string) (staging.Workspace, error) {
	return withTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) (staging.Workspace, error) {
		workspace, err := lockWorkspace(ctx, tx, id)
		if err != nil {
			return staging.Workspace{}, err
		}
		if workspace.Status == staging.WorkspaceFailed {
			return workspace, nil
		}
		if err := staging.ValidateWorkspaceTransition(workspace.Status, staging.WorkspaceFailed); err != nil {
			return staging.Workspace{}, err
		}
		workspace, err = scanWorkspace(tx.QueryRow(ctx, `UPDATE staging_workspaces SET status='failed',status_reason_code=$2,status_reason=$3,version=version+1,updated_at=clock_timestamp() WHERE id=$1 RETURNING `+workspaceColumns, id, code, reason))
		if err != nil {
			return staging.Workspace{}, err
		}
		if err := appendEvent(ctx, tx, "workspace", id, "staging.workspace_failed", "role", actor, map[string]any{"repository_id": workspace.RepositoryID, "task_id": workspace.TaskID, "attempt_id": workspace.AttemptID, "reason_code": code}, s.outboxMaxAttempts); err != nil {
			return staging.Workspace{}, err
		}
		return workspace, nil
	})
}

func (s *Store) SealWorkspace(ctx context.Context, command staging.SealPersistenceCommand) (staging.Workspace, error) {
	return withTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) (staging.Workspace, error) {
		workspace, err := lockWorkspace(ctx, tx, command.WorkspaceID)
		if err != nil {
			return staging.Workspace{}, err
		}
		if workspace.Version != command.ExpectedVersion {
			return staging.Workspace{}, staging.ErrConflict
		}
		if workspace.Status != staging.WorkspaceActive {
			return staging.Workspace{}, staging.ErrWorkspaceNotActive
		}
		manifest, err := insertArtifact(ctx, tx, command.Manifest)
		if err != nil {
			return staging.Workspace{}, err
		}
		patch, err := insertArtifact(ctx, tx, command.Patch)
		if err != nil {
			return staging.Workspace{}, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO staging_workspace_artifacts(workspace_id,artifact_id,kind) VALUES($1,$2,'manifest'),($1,$3,'binary_patch')`, workspace.ID, manifest.ID, patch.ID); err != nil {
			return staging.Workspace{}, mapError(err)
		}
		workspace, err = scanWorkspace(tx.QueryRow(ctx, `UPDATE staging_workspaces SET status='sealed',candidate_commit=$2,candidate_tree=$3,manifest_digest=$4,patch_digest=$5,changed_file_count=$6,sealed_at=clock_timestamp(),version=version+1,updated_at=clock_timestamp() WHERE id=$1 AND status='active' RETURNING `+workspaceColumns, workspace.ID, command.CandidateCommit, command.CandidateTree, command.Manifest.Digest, command.Patch.Digest, command.ChangedFileCount))
		if err != nil {
			return staging.Workspace{}, err
		}
		if workspace.ArtifactRequirementID == nil {
			return staging.Workspace{}, staging.ErrInvalidInput
		}
		metadata := map[string]any{"workspace_id": workspace.ID, "artifact_kind": "manifest"}
		if err := recordTaskRequirement(ctx, tx, workspace.TaskID, *workspace.ArtifactRequirementID, "satisfied", command.Manifest.StorageKey, command.Manifest.Digest, command.ActorRoleID, metadata, s.outboxMaxAttempts); err != nil {
			return staging.Workspace{}, err
		}
		payload := map[string]any{"repository_id": workspace.RepositoryID, "task_id": workspace.TaskID, "attempt_id": workspace.AttemptID, "workspace_id": workspace.ID, "candidate_commit": command.CandidateCommit, "manifest_digest": command.Manifest.Digest, "patch_digest": command.Patch.Digest, "changed_file_count": command.ChangedFileCount}
		if err := appendEvent(ctx, tx, "workspace", workspace.ID, "staging.workspace_sealed", "role", command.ActorRoleID, payload, s.outboxMaxAttempts); err != nil {
			return staging.Workspace{}, err
		}
		return workspace, nil
	})
}

func (s *Store) AbandonWorkspace(ctx context.Context, command staging.AbandonWorkspaceCommand) (staging.Workspace, error) {
	return withTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) (staging.Workspace, error) {
		workspace, err := lockWorkspace(ctx, tx, command.WorkspaceID)
		if err != nil {
			return staging.Workspace{}, err
		}
		if workspace.Status == staging.WorkspaceAbandoned {
			return workspace, nil
		}
		if err := staging.ValidateWorkspaceTransition(workspace.Status, staging.WorkspaceAbandoned); err != nil {
			return staging.Workspace{}, err
		}
		workspace, err = scanWorkspace(tx.QueryRow(ctx, `UPDATE staging_workspaces SET status='abandoned',status_reason_code=$2,status_reason=$3,abandoned_at=clock_timestamp(),version=version+1,updated_at=clock_timestamp() WHERE id=$1 RETURNING `+workspaceColumns, workspace.ID, command.ReasonCode, command.Reason))
		if err != nil {
			return staging.Workspace{}, err
		}
		if err := appendEvent(ctx, tx, "workspace", workspace.ID, "staging.workspace_abandoned", "role", command.ActorRoleID, map[string]any{"repository_id": workspace.RepositoryID, "task_id": workspace.TaskID, "attempt_id": workspace.AttemptID, "reason_code": command.ReasonCode}, s.outboxMaxAttempts); err != nil {
			return staging.Workspace{}, err
		}
		return workspace, nil
	})
}

func (s *Store) BeginCleanup(ctx context.Context, id int64, actor string) (staging.Workspace, error) {
	return withTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) (staging.Workspace, error) {
		workspace, err := lockWorkspace(ctx, tx, id)
		if err != nil {
			return staging.Workspace{}, err
		}
		if workspace.Status == staging.WorkspaceCleanupPending {
			return workspace, nil
		}
		if err := staging.ValidateWorkspaceTransition(workspace.Status, staging.WorkspaceCleanupPending); err != nil {
			return staging.Workspace{}, err
		}
		var active int
		if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM staging_promotions WHERE workspace_id=$1 AND status IN ('requested','awaiting_gates','approved')`, id).Scan(&active); err != nil {
			return staging.Workspace{}, mapError(err)
		}
		if active > 0 {
			return staging.Workspace{}, fmt.Errorf("%w: promotion is still active", staging.ErrConflict)
		}
		workspace, err = scanWorkspace(tx.QueryRow(ctx, `UPDATE staging_workspaces SET status='cleanup_pending',version=version+1,updated_at=clock_timestamp() WHERE id=$1 RETURNING `+workspaceColumns, id))
		if err != nil {
			return staging.Workspace{}, err
		}
		if err := appendEvent(ctx, tx, "workspace", id, "staging.workspace_cleanup_requested", "role", actor, map[string]any{"repository_id": workspace.RepositoryID, "task_id": workspace.TaskID, "attempt_id": workspace.AttemptID}, s.outboxMaxAttempts); err != nil {
			return staging.Workspace{}, err
		}
		return workspace, nil
	})
}

func (s *Store) CompleteCleanup(ctx context.Context, id int64, actor string) (staging.Workspace, error) {
	return withTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) (staging.Workspace, error) {
		workspace, err := lockWorkspace(ctx, tx, id)
		if err != nil {
			return staging.Workspace{}, err
		}
		if workspace.Status == staging.WorkspaceCleaned {
			return workspace, nil
		}
		if err := staging.ValidateWorkspaceTransition(workspace.Status, staging.WorkspaceCleaned); err != nil {
			return staging.Workspace{}, err
		}
		workspace, err = scanWorkspace(tx.QueryRow(ctx, `UPDATE staging_workspaces SET status='cleaned',cleaned_at=clock_timestamp(),version=version+1,updated_at=clock_timestamp() WHERE id=$1 RETURNING `+workspaceColumns, id))
		if err != nil {
			return staging.Workspace{}, err
		}
		if err := appendEvent(ctx, tx, "workspace", id, "staging.workspace_cleaned", "role", actor, map[string]any{"repository_id": workspace.RepositoryID, "task_id": workspace.TaskID, "attempt_id": workspace.AttemptID}, s.outboxMaxAttempts); err != nil {
			return staging.Workspace{}, err
		}
		return workspace, nil
	})
}

func (s *Store) GetWorkspace(ctx context.Context, id int64) (staging.Workspace, error) {
	return scanWorkspace(s.pool.QueryRow(ctx, `SELECT `+workspaceColumns+` FROM staging_workspaces WHERE id=$1`, id))
}
func (s *Store) ListWorkspaces(ctx context.Context, filter staging.WorkspaceFilter) ([]staging.Workspace, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `SELECT `+workspaceColumns+` FROM staging_workspaces WHERE ($1='' OR status=$1) AND ($2=0 OR task_id=$2) ORDER BY created_at DESC,id DESC LIMIT $3`, string(filter.Status), filter.TaskID, limit)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var result []staging.Workspace
	for rows.Next() {
		value, err := scanWorkspace(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, mapError(rows.Err())
}

func (s *Store) ListStaleWorkspaces(ctx context.Context, age time.Duration, limit int) ([]staging.Workspace, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `SELECT `+workspaceColumns+` FROM staging_workspaces WHERE status IN ('provisioning','active') AND updated_at < clock_timestamp()-make_interval(secs => $1) ORDER BY updated_at,id LIMIT $2`, int64(age/time.Second), limit)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var result []staging.Workspace
	for rows.Next() {
		value, err := scanWorkspace(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, mapError(rows.Err())
}
func (s *Store) ListWorkspaceKeys(ctx context.Context) (map[string]struct{}, error) {
	rows, err := s.pool.Query(ctx, `SELECT workspace_key FROM staging_workspaces`)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	result := map[string]struct{}{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, mapError(err)
		}
		result[key] = struct{}{}
	}
	return result, mapError(rows.Err())
}
func (s *Store) ListCleanupPending(ctx context.Context, limit int) ([]staging.Workspace, error) {
	return s.ListWorkspaces(ctx, staging.WorkspaceFilter{Status: staging.WorkspaceCleanupPending, Limit: limit})
}
