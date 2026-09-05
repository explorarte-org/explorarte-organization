package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/executionharness"
	"github.com/jackc/pgx/v5"
)

// EnsureRunDescriptor records the immutable execution contract before the
// first event of a Harness run. The INSERT is deliberately conflict-neutral:
// an existing row is read and compared by canonical digest so a re-entry with
// changed identity fails closed instead of overwriting the original facts.
func (s *Store) EnsureRunDescriptor(ctx context.Context, descriptor executionharness.RunDescriptor) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if descriptor.OrganizationID != s.organizationID {
		return fmt.Errorf("%w: descriptor organization is outside store scope", executionharness.ErrRunDescriptorCorrupt)
	}
	normalized, digest, tools, err := canonicalDescriptorStorage(descriptor)
	if err != nil {
		return err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return mapError(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, `
		INSERT INTO execution_run_descriptors(
			organization_id,harness_run_id,task_id,attempt_id,role_id,
			execution_principal_id,context_id,context_version,context_digest,
			execution_profile_id,model_policy_ref,build_ref,max_turns,max_tool_calls,
			frozen_tools,identity_digest,canonical_digest
		)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
		ON CONFLICT (organization_id,harness_run_id) DO NOTHING`,
		normalized.OrganizationID, normalized.RunID, normalized.TaskID, normalized.AttemptID,
		normalized.RoleID, normalized.ExecutionPrincipalID, normalized.ContextID,
		normalized.ContextVersion, normalized.ContextDigest, normalized.ExecutionProfileID,
		normalized.ModelPolicyRef, normalized.BuildRef, normalized.MaxTurns,
		normalized.MaxToolCalls, tools, normalized.IdentityDigest, digest); err != nil {
		return mapError(err)
	}
	var storedDigest string
	if err = tx.QueryRow(ctx, `
		SELECT canonical_digest
		FROM execution_run_descriptors
		WHERE organization_id=$1 AND harness_run_id=$2`, normalized.OrganizationID, normalized.RunID).Scan(&storedDigest); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: descriptor disappeared during ensure", executionharness.ErrRunDescriptorCorrupt)
		}
		return mapError(err)
	}
	if storedDigest != digest {
		return fmt.Errorf("%w: frozen execution identity differs", executionharness.ErrRunDescriptorConflict)
	}
	if err = tx.Commit(ctx); err != nil {
		return mapError(err)
	}
	return nil
}

// ReadRunDescriptor returns the metadata-only descriptor for one organization
// and Harness run. The stored canonical digest is checked again at the read
// boundary so a malformed or manually altered row cannot become MemoryOS
// input.
func (s *Store) ReadRunDescriptor(ctx context.Context, organizationID, runID string) (executionharness.RunDescriptor, error) {
	if err := ctx.Err(); err != nil {
		return executionharness.RunDescriptor{}, err
	}
	if organizationID != s.organizationID || strings.TrimSpace(runID) == "" {
		return executionharness.RunDescriptor{}, fmt.Errorf("%w: descriptor is outside store scope", executionharness.ErrRunDescriptorNotFound)
	}
	var descriptor executionharness.RunDescriptor
	var toolsJSON []byte
	var storedDigest string
	err := s.pool.QueryRow(ctx, `
		SELECT harness_run_id,organization_id,task_id,attempt_id,role_id,
		       execution_principal_id,context_id,context_version,context_digest,
		       execution_profile_id,model_policy_ref,build_ref,max_turns,max_tool_calls,
		       frozen_tools,identity_digest,canonical_digest
		FROM execution_run_descriptors
		WHERE organization_id=$1 AND harness_run_id=$2`, organizationID, runID).Scan(
		&descriptor.RunID, &descriptor.OrganizationID, &descriptor.TaskID, &descriptor.AttemptID,
		&descriptor.RoleID, &descriptor.ExecutionPrincipalID, &descriptor.ContextID,
		&descriptor.ContextVersion, &descriptor.ContextDigest, &descriptor.ExecutionProfileID,
		&descriptor.ModelPolicyRef, &descriptor.BuildRef, &descriptor.MaxTurns,
		&descriptor.MaxToolCalls, &toolsJSON, &descriptor.IdentityDigest, &storedDigest)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return executionharness.RunDescriptor{}, fmt.Errorf("%w: descriptor not found", executionharness.ErrRunDescriptorNotFound)
		}
		return executionharness.RunDescriptor{}, mapError(err)
	}
	if err = json.Unmarshal(toolsJSON, &descriptor.FrozenTools); err != nil {
		return executionharness.RunDescriptor{}, fmt.Errorf("%w: frozen tool metadata is undecodable", executionharness.ErrRunDescriptorCorrupt)
	}
	if err = descriptor.Validate(); err != nil {
		return executionharness.RunDescriptor{}, fmt.Errorf("%w: descriptor metadata failed validation", executionharness.ErrRunDescriptorCorrupt)
	}
	digest, err := descriptor.CanonicalDigest()
	if err != nil || digest != storedDigest {
		return executionharness.RunDescriptor{}, fmt.Errorf("%w: canonical descriptor digest mismatch", executionharness.ErrRunDescriptorCorrupt)
	}
	return descriptor, nil
}

// canonicalDescriptorStorage returns the normalized descriptor and the exact
// canonical tool array that are persisted. CanonicalBytes sorts tools and
// converts a nil tool slice to an empty array, so callers cannot create two
// durable identities by changing input order or nil-vs-empty representation.
func canonicalDescriptorStorage(descriptor executionharness.RunDescriptor) (executionharness.RunDescriptor, string, []byte, error) {
	body, err := descriptor.CanonicalBytes()
	if err != nil {
		return executionharness.RunDescriptor{}, "", nil, err
	}
	var normalized executionharness.RunDescriptor
	if err = json.Unmarshal(body, &normalized); err != nil {
		return executionharness.RunDescriptor{}, "", nil, fmt.Errorf("%w: canonical descriptor is undecodable", executionharness.ErrRunDescriptorCorrupt)
	}
	digest, err := executionharness.DescriptorDigest(normalized)
	if err != nil {
		return executionharness.RunDescriptor{}, "", nil, err
	}
	tools, err := json.Marshal(normalized.FrozenTools)
	if err != nil {
		return executionharness.RunDescriptor{}, "", nil, err
	}
	return normalized, digest, tools, nil
}

var _ executionharness.RunDescriptorStore = (*Store)(nil)
