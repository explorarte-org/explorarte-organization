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
		row := tx.QueryRow(ctx, `INSERT INTO model_invocations(organization_id,organization_revision_id,task_id,attempt_id,dispatch_actor_role_id,subject_role_id,dispatcher_assignment_id,execution_principal_id,context_snapshot_id,purpose,model_profile_id,model_profile_version_id,provider_id,provider_model_id,model_egress_policy_version_id,model_egress_policy_hash,execution_identity_policy_version_id,execution_identity_policy_hash,required_capabilities,output_mode,output_schema,max_output_tokens,temperature,thinking_mode,idempotency_key,request_hash,status,deadline,correlation_id,causation_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19::jsonb,$20,$21::jsonb,$22,$23,$24,$25,$26,'requested',$27,NULLIF($28,''),NULLIF($29,'')) ON CONFLICT(organization_id,idempotency_key) DO NOTHING RETURNING `+invocationColumns, p.Command.OrganizationID, p.OrganizationRevisionID, p.Command.TaskID, p.Command.AttemptID, p.Assignment.Principal.DispatchActorRoleID, p.Command.SubjectRoleID, p.Assignment.Assignment.ID, p.Assignment.Principal.ID, p.Command.ContextSnapshotID, p.Command.Purpose, p.Binding.Profile.ID, p.Binding.Version.ID, p.Binding.Version.ProviderID, p.Binding.Version.ProviderModelID, p.EgressPolicy.Version.ID, p.EgressPolicy.CanonicalHash, p.IdentityPolicy.Version.ID, p.IdentityPolicy.Version.CanonicalHash, caps, p.Command.OutputMode, outputSchema, p.Command.MaxOutputTokens, p.Command.Temperature, p.Command.ThinkingMode, p.Command.IdempotencyKey, p.RequestHash, p.Command.Deadline, p.Command.CorrelationID, p.Command.CausationID)
		inv, err := scanInvocation(row)
		if err == nil {
			if err = insertModelInput(ctx, tx, inv.ID, p.ModelInput); err != nil {
				return modelruntime.CreateInvocationResult{}, err
			}
			if err = appendInvocationEvent(ctx, tx, inv, modelruntime.AuditInvocationRequested, "role", inv.DispatchActorRoleID, true, outboxMax, map[string]any{"model_input_schema_version": p.ModelInput.Envelope.SchemaVersion, "model_input_digest": p.ModelInput.CanonicalDigest}); err != nil {
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
		existingInput, err := getModelInput(ctx, tx, inv.ID)
		if err != nil {
			return modelruntime.CreateInvocationResult{}, err
		}
		if existingInput.CanonicalDigest != p.ModelInput.CanonicalDigest {
			return modelruntime.CreateInvocationResult{}, fmt.Errorf("%w: idempotent invocation input mismatch", modelruntime.ErrConflict)
		}
		if err = appendInvocationEvent(ctx, tx, inv, modelruntime.AuditInvocationReused, "role", inv.DispatchActorRoleID, false, outboxMax, map[string]any{"model_input_schema_version": p.ModelInput.Envelope.SchemaVersion, "model_input_digest": p.ModelInput.CanonicalDigest}); err != nil {
			return modelruntime.CreateInvocationResult{}, err
		}
		return modelruntime.CreateInvocationResult{Invocation: inv, Reused: true}, nil
	})
}

func insertModelInput(ctx context.Context, tx pgx.Tx, invocationID int64, input modelruntime.PreparedModelInput) error {
	classes, err := json.Marshal(input.Envelope.InputClassifications)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO model_invocation_inputs(invocation_id,context_snapshot_id,schema_version,canonical_bytes,canonical_digest,canonical_projection_digest,stable_prefix_digest,input_classifications,input_classifications_hash) VALUES($1,$2,$3,$4,$5,$6,$7,$8::jsonb,$9)`,
		invocationID, input.Envelope.ContextSnapshotID, input.Envelope.SchemaVersion, input.CanonicalBytes,
		input.CanonicalDigest, input.Envelope.CanonicalProjectionDigest, input.Envelope.StablePrefixDigest, classes,
		input.Envelope.InputClassificationsHash)
	return mapError(err)
}

func getModelInput(ctx context.Context, queryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, invocationID int64) (modelruntime.PreparedModelInput, error) {
	var input modelruntime.PreparedModelInput
	var schemaVersion, projectionDigest, stablePrefixDigest, classificationsHash string
	var contextSnapshotID int64
	var classes []byte
	err := queryer.QueryRow(ctx, `SELECT schema_version,context_snapshot_id,canonical_bytes,canonical_digest,canonical_projection_digest,stable_prefix_digest,input_classifications,input_classifications_hash FROM model_invocation_inputs WHERE invocation_id=$1`, invocationID).Scan(
		&schemaVersion, &contextSnapshotID, &input.CanonicalBytes, &input.CanonicalDigest,
		&projectionDigest, &stablePrefixDigest, &classes, &classificationsHash)
	if err != nil {
		return modelruntime.PreparedModelInput{}, mapError(err)
	}
	if err = json.Unmarshal(input.CanonicalBytes, &input.Envelope); err != nil {
		return modelruntime.PreparedModelInput{}, fmt.Errorf("decode model input: %w", err)
	}
	var storedClasses []string
	if err = json.Unmarshal(classes, &storedClasses); err != nil {
		return modelruntime.PreparedModelInput{}, fmt.Errorf("decode model input classifications: %w", err)
	}
	if input.Envelope.SchemaVersion != schemaVersion || input.Envelope.ContextSnapshotID != contextSnapshotID ||
		input.Envelope.CanonicalProjectionDigest != projectionDigest || input.Envelope.StablePrefixDigest != stablePrefixDigest ||
		input.Envelope.InputClassificationsHash != classificationsHash || !equalStrings(storedClasses, input.Envelope.InputClassifications) {
		return modelruntime.PreparedModelInput{}, fmt.Errorf("%w: model input columns disagree with canonical bytes", modelruntime.ErrConflict)
	}
	return input, nil
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func (s *Store) GetModelInput(ctx context.Context, invocationID int64) (modelruntime.PreparedModelInput, error) {
	return getModelInput(ctx, s.pool, invocationID)
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
