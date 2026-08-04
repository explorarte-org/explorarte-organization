package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/staging"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const workspaceColumns = `id,organization_id,organization_revision_id,task_id,attempt_id,repository_id,repository_config_hash,workspace_key,workspace_ref,base_commit,target_ref,status,holder_id,actor_role_id,artifact_requirement_id,candidate_commit,candidate_tree,manifest_digest,patch_digest,changed_file_count,version,status_reason_code,status_reason,created_at,updated_at,sealed_at,abandoned_at,cleaned_at`
const promotionColumns = `id,workspace_id,task_id,repository_id,target_ref,expected_base_commit,candidate_commit,status,requested_by_role_id,approved_by_role_id,status_reason_code,status_reason,version,created_at,updated_at,approved_at,applied_at,rejected_at,cancelled_at`

func scanWorkspace(row scanner) (staging.Workspace, error) {
	var value staging.Workspace
	if err := row.Scan(&value.ID, &value.OrganizationID, &value.OrganizationRevisionID, &value.TaskID, &value.AttemptID, &value.RepositoryID, &value.RepositoryConfigHash, &value.WorkspaceKey, &value.WorkspaceRef, &value.BaseCommit, &value.TargetRef, &value.Status, &value.HolderID, &value.ActorRoleID, &value.ArtifactRequirementID, &value.CandidateCommit, &value.CandidateTree, &value.ManifestDigest, &value.PatchDigest, &value.ChangedFileCount, &value.Version, &value.StatusReasonCode, &value.StatusReason, &value.CreatedAt, &value.UpdatedAt, &value.SealedAt, &value.AbandonedAt, &value.CleanedAt); err != nil {
		return staging.Workspace{}, mapError(err)
	}
	return value, nil
}

func scanPromotion(row scanner) (staging.Promotion, error) {
	var value staging.Promotion
	if err := row.Scan(&value.ID, &value.WorkspaceID, &value.TaskID, &value.RepositoryID, &value.TargetRef, &value.ExpectedBaseCommit, &value.CandidateCommit, &value.Status, &value.RequestedByRoleID, &value.ApprovedByRoleID, &value.StatusReasonCode, &value.StatusReason, &value.Version, &value.CreatedAt, &value.UpdatedAt, &value.ApprovedAt, &value.AppliedAt, &value.RejectedAt, &value.CancelledAt); err != nil {
		return staging.Promotion{}, mapError(err)
	}
	return value, nil
}

func withTx[T any](ctx context.Context, pool *pgxpool.Pool, options pgx.TxOptions, fn func(pgx.Tx) (T, error)) (result T, err error) {
	tx, err := pool.BeginTx(ctx, options)
	if err != nil {
		return result, mapError(err)
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			_ = rollback(tx)
			panic(recovered)
		}
		if err != nil {
			_ = rollback(tx)
		}
	}()
	result, err = fn(tx)
	if err != nil {
		return result, err
	}
	if err = tx.Commit(ctx); err != nil {
		return result, mapError(err)
	}
	return result, nil
}
func rollback(tx pgx.Tx) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return tx.Rollback(ctx)
}

func lockWorkspace(ctx context.Context, tx pgx.Tx, id int64) (staging.Workspace, error) {
	return scanWorkspace(tx.QueryRow(ctx, `SELECT `+workspaceColumns+` FROM staging_workspaces WHERE id=$1 FOR UPDATE`, id))
}
func lockPromotion(ctx context.Context, tx pgx.Tx, id int64) (staging.Promotion, error) {
	value, err := scanPromotion(tx.QueryRow(ctx, `SELECT `+promotionColumns+` FROM staging_promotions WHERE id=$1 FOR UPDATE`, id))
	if errors.Is(err, staging.ErrWorkspaceNotFound) {
		return staging.Promotion{}, staging.ErrWorkspaceNotFound
	}
	return value, err
}

func appendEvent(ctx context.Context, tx pgx.Tx, aggregateType string, aggregateID int64, eventType, actorType, actorID string, payload map[string]any, outboxMax int) error {
	if payload == nil {
		payload = map[string]any{}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	var sequence int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(sequence),0)+1 FROM staging_events WHERE aggregate_type=$1 AND aggregate_id=$2`, aggregateType, aggregateID).Scan(&sequence); err != nil {
		return mapError(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO staging_events(aggregate_type,aggregate_id,sequence,event_type,actor_type,actor_id,payload) VALUES($1,$2,$3,$4,$5,$6,$7::jsonb)`, aggregateType, aggregateID, sequence, eventType, actorType, actorID, body); err != nil {
		return mapError(err)
	}
	outbox := map[string]any{"schema_version": 1, "aggregate_type": aggregateType, "aggregate_id": aggregateID, "event_type": eventType}
	for _, key := range []string{"repository_id", "task_id", "attempt_id", "workspace_id", "promotion_id", "candidate_commit", "expected_base_commit", "manifest_digest", "patch_digest", "changed_file_count", "reason_code"} {
		if value, ok := payload[key]; ok {
			outbox[key] = value
		}
	}
	outboxBody, err := json.Marshal(outbox)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO outbox_events(aggregate_type,aggregate_id,event_type,schema_version,payload,max_attempts) VALUES($1,$2,$3,1,$4::jsonb,$5)`, "staging."+aggregateType, strconv.FormatInt(aggregateID, 10), eventType, outboxBody, outboxMax); err != nil {
		return mapError(err)
	}
	audit := map[string]any{"aggregate_type": aggregateType, "aggregate_id": aggregateID, "event_type": eventType}
	for _, key := range []string{"repository_id", "task_id", "attempt_id", "workspace_id", "promotion_id", "candidate_commit", "expected_base_commit", "manifest_digest", "patch_digest", "changed_file_count", "reason_code"} {
		if value, ok := payload[key]; ok {
			audit[key] = value
		}
	}
	auditBody, err := json.Marshal(audit)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(event_type,actor_type,actor_id,subject_type,subject_id,payload) VALUES($1,$2,$3,$4,$5,$6::jsonb)`, eventType, actorType, actorID, "staging_"+aggregateType, strconv.FormatInt(aggregateID, 10), auditBody); err != nil {
		return mapError(err)
	}
	return nil
}

func insertArtifact(ctx context.Context, tx pgx.Tx, artifact staging.StoredArtifact) (staging.Artifact, error) {
	if _, err := tx.Exec(ctx, `INSERT INTO staging_artifacts(digest,size_bytes,media_type,storage_key) VALUES($1,$2,$3,$4) ON CONFLICT (digest) DO NOTHING`, artifact.Digest, artifact.SizeBytes, artifact.MediaType, artifact.StorageKey); err != nil {
		return staging.Artifact{}, mapError(err)
	}
	var value staging.Artifact
	if err := tx.QueryRow(ctx, `SELECT id,digest,size_bytes,media_type,storage_key,created_at FROM staging_artifacts WHERE digest=$1`, artifact.Digest).Scan(&value.ID, &value.Digest, &value.SizeBytes, &value.MediaType, &value.StorageKey, &value.CreatedAt); err != nil {
		return staging.Artifact{}, mapError(err)
	}
	if value.SizeBytes != artifact.SizeBytes || value.StorageKey != artifact.StorageKey {
		return staging.Artifact{}, staging.ErrArtifactCorrupt
	}
	return value, nil
}

func recordTaskRequirement(ctx context.Context, tx pgx.Tx, taskID, requirementID int64, result, reference, digest, actor string, metadata map[string]any, outboxMax int) error {
	var requirementType string
	var status string
	if err := tx.QueryRow(ctx, `SELECT requirement_type,status FROM task_requirements WHERE id=$1 AND task_id=$2 FOR UPDATE`, requirementID, taskID).Scan(&requirementType, &status); err != nil {
		return mapError(err)
	}
	if status != "pending" {
		return fmt.Errorf("%w: requirement already %s", staging.ErrConflict, status)
	}
	meta, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	var evidenceID int64
	if err := tx.QueryRow(ctx, `INSERT INTO task_evidence(task_id,requirement_id,evidence_type,reference,digest,recorded_by,metadata) VALUES($1,$2,$3,$4,NULLIF($5,''),$6,$7::jsonb) RETURNING id`, taskID, requirementID, requirementType, reference, digest, actor, meta).Scan(&evidenceID); err != nil {
		return mapError(err)
	}
	if result != "satisfied" && result != "failed" {
		return staging.ErrInvalidInput
	}
	if _, err := tx.Exec(ctx, `UPDATE task_requirements SET status=$2,satisfied_at=CASE WHEN $2='satisfied' THEN clock_timestamp() ELSE NULL END,updated_at=clock_timestamp() WHERE id=$1 AND status='pending'`, requirementID, result); err != nil {
		return mapError(err)
	}
	var taskVersion int64
	if err := tx.QueryRow(ctx, `UPDATE tasks SET version=version+1,updated_at=clock_timestamp() WHERE id=$1 RETURNING version`, taskID).Scan(&taskVersion); err != nil {
		return mapError(err)
	}
	var sequence int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(sequence),0)+1 FROM task_events WHERE task_id=$1`, taskID).Scan(&sequence); err != nil {
		return mapError(err)
	}
	eventType := "task.requirement_" + result
	taskPayload := map[string]any{"requirement_id": requirementID, "requirement_type": requirementType, "verification_result": result, "evidence_id": evidenceID}
	taskBody, _ := json.Marshal(taskPayload)
	if _, err := tx.Exec(ctx, `INSERT INTO task_events(task_id,sequence,event_type,actor_type,actor_id,payload) VALUES($1,$2,$3,'role',$4,$5::jsonb)`, taskID, sequence, eventType, actor, taskBody); err != nil {
		return mapError(err)
	}
	minimal, _ := json.Marshal(map[string]any{"schema_version": 1, "task_id": taskID, "event_type": eventType, "task_version": taskVersion})
	if _, err := tx.Exec(ctx, `INSERT INTO outbox_events(aggregate_type,aggregate_id,event_type,schema_version,payload,max_attempts) VALUES('task',$1,$2,1,$3::jsonb,$4)`, strconv.FormatInt(taskID, 10), eventType, minimal, outboxMax); err != nil {
		return mapError(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(event_type,actor_type,actor_id,subject_type,subject_id,payload) VALUES($1,'role',$2,'task',$3,$4::jsonb)`, eventType, actor, strconv.FormatInt(taskID, 10), minimal); err != nil {
		return mapError(err)
	}
	return nil
}
