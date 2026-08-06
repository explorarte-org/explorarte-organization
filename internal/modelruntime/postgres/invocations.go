package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
	"github.com/jackc/pgx/v5"
)

func (s *Store) CreateInvocation(ctx context.Context, p modelruntime.PreparedInvocation, outboxMax int) (modelruntime.CreateInvocationResult, error) {
	return withTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) (modelruntime.CreateInvocationResult, error) {
		caps, _ := json.Marshal(p.RequiredCapabilities)
		var outputSchema any
		if len(p.OutputSchema) > 0 {
			outputSchema = string(p.OutputSchema)
		}
		row := tx.QueryRow(ctx, `INSERT INTO model_invocations(organization_id,organization_revision_id,task_id,attempt_id,dispatch_actor_role_id,subject_role_id,dispatcher_assignment_id,execution_principal_id,context_snapshot_id,purpose,model_profile_id,model_profile_version_id,provider_id,provider_model_id,model_egress_policy_version_id,model_egress_policy_hash,required_capabilities,output_mode,output_schema,max_output_tokens,temperature,thinking_mode,idempotency_key,request_hash,status,deadline,correlation_id,causation_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17::jsonb,$18,$19::jsonb,$20,$21,$22,$23,$24,'requested',$25,NULLIF($26,''),NULLIF($27,'')) ON CONFLICT(organization_id,idempotency_key) DO NOTHING RETURNING `+invocationColumns, p.Command.OrganizationID, p.OrganizationRevisionID, p.Command.TaskID, p.Command.AttemptID, p.Assignment.Principal.DispatchActorRoleID, p.Command.SubjectRoleID, p.Assignment.Assignment.ID, p.Assignment.Principal.ID, p.Command.ContextSnapshotID, p.Command.Purpose, p.Binding.Profile.ID, p.Binding.Version.ID, p.Binding.Version.ProviderID, p.Binding.Version.ProviderModelID, p.EgressPolicy.Version.ID, p.EgressPolicy.CanonicalHash, caps, p.Command.OutputMode, outputSchema, p.Command.MaxOutputTokens, p.Command.Temperature, p.Command.ThinkingMode, p.Command.IdempotencyKey, p.RequestHash, p.Command.Deadline, p.Command.CorrelationID, p.Command.CausationID)
		inv, err := scanInvocation(row)
		if err == nil {
			if err = appendInvocationEvent(ctx, tx, inv, modelruntime.AuditInvocationRequested, "role", inv.DispatchActorRoleID, true, outboxMax, nil); err != nil {
				return modelruntime.CreateInvocationResult{}, err
			}
			return modelruntime.CreateInvocationResult{Invocation: inv}, nil
		}
		if !errors.Is(err, modelruntime.ErrNotFound) {
			return modelruntime.CreateInvocationResult{}, err
		}
		inv, err = scanInvocation(tx.QueryRow(ctx, `SELECT `+invocationColumns+` FROM model_invocations WHERE organization_id=$1 AND idempotency_key=$2 FOR UPDATE`, p.Command.OrganizationID, p.Command.IdempotencyKey))
		if err != nil {
			return modelruntime.CreateInvocationResult{}, err
		}
		if inv.RequestHash != p.RequestHash {
			return modelruntime.CreateInvocationResult{}, fmt.Errorf("%w: idempotency key reused with different request", modelruntime.ErrConflict)
		}
		if err = appendInvocationEvent(ctx, tx, inv, modelruntime.AuditInvocationReused, "role", inv.DispatchActorRoleID, false, outboxMax, nil); err != nil {
			return modelruntime.CreateInvocationResult{}, err
		}
		return modelruntime.CreateInvocationResult{Invocation: inv, Reused: true}, nil
	})
}
func (s *Store) GetInvocation(ctx context.Context, id int64) (modelruntime.Invocation, error) {
	return scanInvocation(s.pool.QueryRow(ctx, `SELECT `+invocationColumns+` FROM model_invocations WHERE id=$1`, id))
}
func (s *Store) ListInvocations(ctx context.Context, organizationID string, limit int) ([]modelruntime.Invocation, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+invocationColumns+` FROM model_invocations WHERE organization_id=$1 ORDER BY id DESC LIMIT $2`, organizationID, limit)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	result := []modelruntime.Invocation{}
	for rows.Next() {
		v, scanErr := scanInvocation(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, v)
	}
	return result, mapError(rows.Err())
}
