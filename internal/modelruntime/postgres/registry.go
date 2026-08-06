package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"

	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
	"github.com/jackc/pgx/v5"
)

func (s *Store) RecordRegistryValidated(ctx context.Context, organizationID, hash string) error {
	payload, _ := json.Marshal(map[string]any{"canonical_hash": hash})
	_, err := s.pool.Exec(ctx, `INSERT INTO audit_events(event_type,actor_type,actor_id,subject_type,subject_id,payload) VALUES($1,'system','orgctl','model_registry',$2,$3::jsonb)`, modelruntime.AuditRegistryValidated, organizationID, payload)
	return mapError(err)
}
func (s *Store) RegistryStatus(ctx context.Context, organizationID string, revisionID int64, canonicalHash string) (modelruntime.RegistryStatus, error) {
	status := modelruntime.RegistryStatus{
		OrganizationID:         organizationID,
		OrganizationRevisionID: revisionID,
		CanonicalHash:          canonicalHash,
	}
	if revisionID <= 0 {
		return status, fmt.Errorf("%w: organization revision is required", modelruntime.ErrInvalidRequest)
	}
	if err := s.pool.QueryRow(ctx, `
SELECT COALESCE(MAX(v.canonical_document_hash),''),
       (SELECT COUNT(*) FROM model_providers p
          WHERE p.organization_id=$1 AND p.organization_revision_id=$2),
       COUNT(DISTINCT v.profile_id),
       COUNT(*)
FROM model_profile_versions v
WHERE v.organization_id=$1
  AND v.organization_revision_id=$2
  AND v.canonical_document_hash=$3`, organizationID, revisionID, canonicalHash).Scan(
		&status.MaterializedHash,
		&status.Providers,
		&status.Profiles,
		&status.ProfileVersions,
	); err != nil {
		return status, mapError(err)
	}
	if status.MaterializedHash == canonicalHash {
		if err := s.pool.QueryRow(ctx, `
SELECT COUNT(*)
FROM role_model_bindings b
JOIN model_profile_versions v
  ON v.id=b.model_profile_version_id
 AND v.organization_id=b.organization_id
 AND v.profile_id=b.profile_id
WHERE b.organization_id=$1
  AND b.organization_revision_id=$2
  AND v.organization_revision_id=$2
  AND v.canonical_document_hash=$3`, organizationID, revisionID, canonicalHash).Scan(&status.Bindings); err != nil {
			return status, mapError(err)
		}
	}
	status.Synchronized = status.MaterializedHash == canonicalHash && status.ProfileVersions > 0
	return status, nil
}

func (s *Store) ApplyRegistry(ctx context.Context, plan modelruntime.RegistryPlan, outboxMax int) (modelruntime.RegistrySyncResult, error) {
	_ = outboxMax // registry events are audit-only by the closed event contract.
	return withTx(ctx, s.pool, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(tx pgx.Tx) (modelruntime.RegistrySyncResult, error) {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, `model-registry:`+plan.OrganizationID); err != nil {
			return modelruntime.RegistrySyncResult{}, mapError(err)
		}
		var existingProviders, existingProfiles, existingVersions, existingBindings int
		if err := tx.QueryRow(ctx, `
SELECT
  (SELECT COUNT(*) FROM model_providers p
    WHERE p.organization_id=$1 AND p.organization_revision_id=$2),
  COUNT(DISTINCT v.profile_id),
  COUNT(*),
  (SELECT COUNT(*)
     FROM role_model_bindings b
     JOIN model_profile_versions bv
       ON bv.id=b.model_profile_version_id
      AND bv.organization_id=b.organization_id
      AND bv.profile_id=b.profile_id
    WHERE b.organization_id=$1
      AND b.organization_revision_id=$2
      AND bv.organization_revision_id=$2
      AND bv.canonical_document_hash=$3)
FROM model_profile_versions v
WHERE v.organization_id=$1
  AND v.organization_revision_id=$2
  AND v.canonical_document_hash=$3`, plan.OrganizationID, plan.OrganizationRevisionID, plan.CanonicalHash).Scan(
			&existingProviders, &existingProfiles, &existingVersions, &existingBindings,
		); err != nil {
			return modelruntime.RegistrySyncResult{}, mapError(err)
		}
		if existingProviders+existingProfiles+existingVersions+existingBindings > 0 {
			if existingProviders != len(plan.Providers) || existingProfiles != len(plan.Profiles) || existingVersions != len(plan.Versions) || existingBindings != len(plan.Bindings) {
				return modelruntime.RegistrySyncResult{}, fmt.Errorf("%w: partial model registry materialization detected", modelruntime.ErrConflict)
			}
			return modelruntime.RegistrySyncResult{
				NoOp:                   true,
				CanonicalHash:          plan.CanonicalHash,
				OrganizationRevisionID: plan.OrganizationRevisionID,
				Providers:              len(plan.Providers),
				Profiles:               len(plan.Profiles),
				Versions:               len(plan.Versions),
				Bindings:               len(plan.Bindings),
			}, nil
		}

		for _, provider := range plan.Providers {
			if _, err := tx.Exec(ctx, `
INSERT INTO model_providers(
    organization_id,id,transport,adapter_status,dispatch_enabled,
    direct_http_forbidden,canonical_hash,organization_revision_id
) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`,
				provider.OrganizationID,
				provider.ID,
				provider.Transport,
				provider.AdapterStatus,
				provider.DispatchEnabled,
				provider.DirectHTTPForbidden,
				provider.CanonicalHash,
				provider.OrganizationRevisionID,
			); err != nil {
				return modelruntime.RegistrySyncResult{}, mapError(err)
			}
		}

		for _, profile := range plan.Profiles {
			tag, err := tx.Exec(ctx, `
INSERT INTO model_profiles(organization_id,id,policy_id)
VALUES($1,$2,$3)
ON CONFLICT(organization_id,id) DO NOTHING`, profile.OrganizationID, profile.ID, profile.PolicyID)
			if err != nil {
				return modelruntime.RegistrySyncResult{}, mapError(err)
			}
			if tag.RowsAffected() == 0 {
				var policyID string
				if err = tx.QueryRow(ctx, `SELECT policy_id FROM model_profiles WHERE organization_id=$1 AND id=$2`, profile.OrganizationID, profile.ID).Scan(&policyID); err != nil {
					return modelruntime.RegistrySyncResult{}, mapError(err)
				}
				if policyID != profile.PolicyID {
					return modelruntime.RegistrySyncResult{}, fmt.Errorf("%w: profile %s policy is immutable", modelruntime.ErrConflict, profile.ID)
				}
			}
		}

		versionIDs := make(map[string]int64, len(plan.Versions))
		for _, version := range plan.Versions {
			var nextVersion int
			if err := tx.QueryRow(ctx, `
SELECT COALESCE(MAX(version_number),0)+1
FROM model_profile_versions
WHERE organization_id=$1 AND profile_id=$2`, version.OrganizationID, version.ProfileID).Scan(&nextVersion); err != nil {
				return modelruntime.RegistrySyncResult{}, mapError(err)
			}
			var id int64
			if err := tx.QueryRow(ctx, `
INSERT INTO model_profile_versions(
    organization_id,profile_id,version_number,organization_revision_id,
    canonical_document_hash,version_hash,provider_id,provider_model_id,
    transport,reasoning_effort,decision_status,adapter_status,dispatch_enabled
) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,NULLIF($10,''),NULLIF($11,''),$12,$13)
RETURNING id`,
				version.OrganizationID,
				version.ProfileID,
				nextVersion,
				version.OrganizationRevisionID,
				version.CanonicalDocumentHash,
				version.VersionHash,
				version.ProviderID,
				version.ProviderModelID,
				version.Transport,
				version.ReasoningEffort,
				version.DecisionStatus,
				version.AdapterStatus,
				version.DispatchEnabled,
			).Scan(&id); err != nil {
				return modelruntime.RegistrySyncResult{}, mapError(err)
			}
			versionIDs[version.ProfileID] = id
		}

		for _, capability := range plan.CapabilitySnapshots {
			versionID, ok := versionIDs[capability.ProfileID]
			if !ok {
				return modelruntime.RegistrySyncResult{}, fmt.Errorf("%w: capability profile version missing", modelruntime.ErrConflict)
			}
			body, err := json.Marshal(capability.Capabilities)
			if err != nil {
				return modelruntime.RegistrySyncResult{}, err
			}
			if _, err = tx.Exec(ctx, `
INSERT INTO model_capability_snapshots(
    organization_id,model_profile_version_id,capabilities,capability_hash
) VALUES($1,$2,$3::jsonb,$4)`, capability.OrganizationID, versionID, body, capability.CapabilityHash); err != nil {
				return modelruntime.RegistrySyncResult{}, mapError(err)
			}
		}

		bindings := append([]modelruntime.RoleBinding(nil), plan.Bindings...)
		sort.Slice(bindings, func(i, j int) bool { return bindings[i].RoleID < bindings[j].RoleID })
		for _, binding := range bindings {
			versionID, ok := versionIDs[binding.ProfileID]
			if !ok {
				return modelruntime.RegistrySyncResult{}, fmt.Errorf("%w: binding profile version missing", modelruntime.ErrConflict)
			}
			if _, err := tx.Exec(ctx, `
INSERT INTO role_model_bindings(
    organization_id,organization_revision_id,role_id,policy_id,
    profile_id,model_profile_version_id,binding_hash,active
) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`,
				binding.OrganizationID,
				binding.OrganizationRevisionID,
				binding.RoleID,
				binding.PolicyID,
				binding.ProfileID,
				versionID,
				binding.BindingHash,
				binding.Active,
			); err != nil {
				return modelruntime.RegistrySyncResult{}, mapError(err)
			}
		}

		payload, err := json.Marshal(map[string]any{
			"canonical_hash":           plan.CanonicalHash,
			"organization_revision_id": plan.OrganizationRevisionID,
			"providers":                len(plan.Providers),
			"profiles":                 len(plan.Profiles),
			"versions":                 len(plan.Versions),
			"bindings":                 len(plan.Bindings),
		})
		if err != nil {
			return modelruntime.RegistrySyncResult{}, err
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO audit_events(event_type,actor_type,actor_id,subject_type,subject_id,payload)
VALUES($1,'system','orgctl','model_registry',$2,$3::jsonb)`, modelruntime.AuditRegistrySynced, plan.OrganizationID, payload); err != nil {
			return modelruntime.RegistrySyncResult{}, mapError(err)
		}
		return modelruntime.RegistrySyncResult{
			Applied:                true,
			CanonicalHash:          plan.CanonicalHash,
			OrganizationRevisionID: plan.OrganizationRevisionID,
			Providers:              len(plan.Providers),
			Profiles:               len(plan.Profiles),
			Versions:               len(plan.Versions),
			Bindings:               len(plan.Bindings),
		}, nil
	})
}

func (s *Store) GetBinding(ctx context.Context, organizationID string, revisionID int64, roleID string) (modelruntime.ResolvedBinding, error) {
	var out modelruntime.ResolvedBinding
	var caps []byte
	err := s.pool.QueryRow(ctx, `
SELECT b.organization_id,b.organization_revision_id,b.role_id,b.policy_id,b.profile_id,b.model_profile_version_id,b.binding_hash,b.active,b.created_at,
       p.organization_id,p.id,p.policy_id,p.created_at,
       v.id,v.organization_id,v.profile_id,v.version_number,v.organization_revision_id,v.canonical_document_hash,v.version_hash,v.provider_id,v.provider_model_id,v.transport,COALESCE(v.reasoning_effort,''),COALESCE(v.decision_status,''),v.adapter_status,v.dispatch_enabled,v.created_at,
       c.id,c.organization_id,c.model_profile_version_id,c.capabilities,c.capability_hash,c.created_at,
       pr.organization_id,pr.id,pr.transport,pr.adapter_status,pr.dispatch_enabled,pr.direct_http_forbidden,pr.canonical_hash,pr.organization_revision_id,pr.created_at
FROM role_model_bindings b
JOIN model_profiles p ON p.organization_id=b.organization_id AND p.id=b.profile_id
JOIN model_profile_versions v ON v.id=b.model_profile_version_id AND v.organization_id=b.organization_id
JOIN model_capability_snapshots c ON c.model_profile_version_id=v.id
JOIN model_providers pr ON pr.organization_id=v.organization_id AND pr.id=v.provider_id AND pr.organization_revision_id=v.organization_revision_id
WHERE b.organization_id=$1 AND b.organization_revision_id=$2 AND b.role_id=$3 AND b.active`, organizationID, revisionID, roleID).Scan(
		&out.Binding.OrganizationID, &out.Binding.OrganizationRevisionID, &out.Binding.RoleID, &out.Binding.PolicyID, &out.Binding.ProfileID, &out.Binding.ModelProfileVersionID, &out.Binding.BindingHash, &out.Binding.Active, &out.Binding.CreatedAt,
		&out.Profile.OrganizationID, &out.Profile.ID, &out.Profile.PolicyID, &out.Profile.CreatedAt,
		&out.Version.ID, &out.Version.OrganizationID, &out.Version.ProfileID, &out.Version.VersionNumber, &out.Version.OrganizationRevisionID, &out.Version.CanonicalDocumentHash, &out.Version.VersionHash, &out.Version.ProviderID, &out.Version.ProviderModelID, &out.Version.Transport, &out.Version.ReasoningEffort, &out.Version.DecisionStatus, &out.Version.AdapterStatus, &out.Version.DispatchEnabled, &out.Version.CreatedAt,
		&out.Capabilities.ID, &out.Capabilities.OrganizationID, &out.Capabilities.ModelProfileVersionID, &caps, &out.Capabilities.CapabilityHash, &out.Capabilities.CreatedAt,
		&out.Provider.OrganizationID, &out.Provider.ID, &out.Provider.Transport, &out.Provider.AdapterStatus, &out.Provider.DispatchEnabled, &out.Provider.DirectHTTPForbidden, &out.Provider.CanonicalHash, &out.Provider.OrganizationRevisionID, &out.Provider.CreatedAt)
	if err != nil {
		mapped := mapError(err)
		if errors.Is(mapped, modelruntime.ErrNotFound) {
			return out, modelruntime.ErrBindingNotFound
		}
		return out, mapped
	}
	if err = json.Unmarshal(caps, &out.Capabilities.Capabilities); err != nil {
		return out, err
	}
	out.Capabilities.ProfileID = out.Profile.ID
	return out, nil
}

func intString(v int64) string { return strconv.FormatInt(v, 10) }
