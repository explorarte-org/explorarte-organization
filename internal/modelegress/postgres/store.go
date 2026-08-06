package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sort"

	"github.com/Mireuz13/explorarte-organization/internal/modelegress"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ pool *pgxpool.Pool }

type ruleQuerier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func expectedMaterializedRules(policy modelegress.CanonicalPolicy) []modelegress.Rule {
	rules := make([]modelegress.Rule, 0, len(policy.HardDenies)+len(policy.Rules))
	for _, deny := range policy.HardDenies {
		rules = append(rules, modelegress.Rule{
			ProviderID: "*", DataClassification: deny.DataClassification, Effect: modelegress.EffectDeny,
			ReasonCode: deny.ReasonCode, HardDeny: true,
		})
	}
	rules = append(rules, policy.Rules...)
	sort.Slice(rules, func(i, j int) bool {
		left := rules[i].ProviderID + "\x00" + string(rules[i].DataClassification)
		right := rules[j].ProviderID + "\x00" + string(rules[j].DataClassification)
		return left < right
	})
	return rules
}

func materializedRules(ctx context.Context, queryer ruleQuerier, policyVersionID int64) ([]modelegress.Rule, error) {
	rows, err := queryer.Query(ctx, `
SELECT provider_id,data_classification,effect,reason_code,hard_deny
FROM model_egress_rules
WHERE policy_version_id=$1
ORDER BY provider_id,data_classification`, policyVersionID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var rules []modelegress.Rule
	for rows.Next() {
		var rule modelegress.Rule
		if err = rows.Scan(&rule.ProviderID, &rule.DataClassification, &rule.Effect, &rule.ReasonCode, &rule.HardDeny); err != nil {
			return nil, mapError(err)
		}
		rules = append(rules, rule)
	}
	return rules, mapError(rows.Err())
}

func equalRules(left, right []modelegress.Rule) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func New(store *platformpostgres.Store) (*Store, error) {
	if store == nil || store.Pool() == nil {
		return nil, errors.New("model egress store requires initialized PostgreSQL")
	}
	return &Store{pool: store.Pool()}, nil
}

func mapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return modelegress.ErrPolicyNotFound
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var networkError net.Error
	if errors.As(err, &networkError) {
		return err
	}
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		switch postgresError.Code {
		case "23505":
			return fmt.Errorf("%w: PostgreSQL %s", modelegress.ErrPolicyConflict, postgresError.Code)
		case "23503", "23514", "22P02":
			return fmt.Errorf("%w: PostgreSQL %s", modelegress.ErrInvalidPolicy, postgresError.Code)
		}
	}
	return err
}

func withTx[T any](ctx context.Context, pool *pgxpool.Pool, options pgx.TxOptions, fn func(pgx.Tx) (T, error)) (T, error) {
	var zero T
	tx, err := pool.BeginTx(ctx, options)
	if err != nil {
		return zero, mapError(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	value, err := fn(tx)
	if err != nil {
		return zero, err
	}
	if err = tx.Commit(ctx); err != nil {
		return zero, mapError(err)
	}
	return value, nil
}

func (s *Store) RecordValidated(ctx context.Context, organizationID string, revisionID int64, canonicalHash string) error {
	payload, _ := json.Marshal(map[string]any{"organization_revision_id": revisionID, "canonical_hash": canonicalHash})
	_, err := s.pool.Exec(ctx, `
INSERT INTO audit_events(event_type,actor_type,actor_id,subject_type,subject_id,payload)
VALUES('model.egress_registry_validated','system','orgctl','model_egress_registry',$1,$2::jsonb)`, organizationID, payload)
	return mapError(err)
}

func (s *Store) Status(ctx context.Context, plan modelegress.RegistryPlan) (modelegress.RegistryStatus, error) {
	if err := modelegress.ValidateRegistryPlan(plan); err != nil {
		return modelegress.RegistryStatus{}, err
	}
	status := modelegress.RegistryStatus{
		OrganizationID: plan.OrganizationID, OrganizationRevisionID: plan.OrganizationRevisionID,
		PolicyID: plan.Policy.PolicyID, PolicyVersion: plan.Policy.PolicyVersion,
		CanonicalHash: plan.CanonicalHash,
	}
	var versionID int64
	var materializedHash, materializedPolicyID string
	var materializedPolicyVersion, ruleCount int
	err := s.pool.QueryRow(ctx, `
SELECT b.policy_version_id,b.canonical_hash,v.policy_id,v.policy_version,COUNT(r.id)
FROM model_egress_revision_bindings b
JOIN model_egress_policy_versions v
  ON v.id=b.policy_version_id AND v.organization_id=b.organization_id AND v.canonical_hash=b.canonical_hash
LEFT JOIN model_egress_rules r ON r.policy_version_id=v.id
WHERE b.organization_id=$1 AND b.organization_revision_id=$2
GROUP BY b.policy_version_id,b.canonical_hash,v.policy_id,v.policy_version`, plan.OrganizationID, plan.OrganizationRevisionID).Scan(&versionID, &materializedHash, &materializedPolicyID, &materializedPolicyVersion, &ruleCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return status, nil
	}
	if err != nil {
		return status, mapError(err)
	}
	status.PolicyVersionID = versionID
	status.MaterializedHash = materializedHash
	status.Rules = ruleCount
	rules, rulesErr := materializedRules(ctx, s.pool, versionID)
	if rulesErr != nil {
		return status, rulesErr
	}
	status.Synchronized = materializedHash == plan.CanonicalHash &&
		materializedPolicyID == plan.Policy.PolicyID &&
		materializedPolicyVersion == plan.Policy.PolicyVersion &&
		equalRules(rules, expectedMaterializedRules(plan.Policy))
	return status, nil
}

func (s *Store) Apply(ctx context.Context, plan modelegress.RegistryPlan) (modelegress.RegistrySyncResult, error) {
	if err := modelegress.ValidateRegistryPlan(plan); err != nil {
		return modelegress.RegistrySyncResult{}, err
	}
	return withTx(ctx, s.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) (modelegress.RegistrySyncResult, error) {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "model-egress:"+plan.OrganizationID); err != nil {
			return modelegress.RegistrySyncResult{}, mapError(err)
		}
		var currentRevision int64
		var currentHash string
		if err := tx.QueryRow(ctx, `
SELECT o.current_revision_id, r.document_hashes->>'model-egress-policy.yaml'
FROM organizations o
JOIN organization_registry_revisions r ON r.id=o.current_revision_id
WHERE o.id=$1
FOR UPDATE OF o`, plan.OrganizationID).Scan(&currentRevision, &currentHash); err != nil {
			return modelegress.RegistrySyncResult{}, mapError(err)
		}
		if currentRevision != plan.OrganizationRevisionID || currentHash != plan.CanonicalHash {
			return modelegress.RegistrySyncResult{}, modelegress.ErrPolicyStale
		}

		var policyVersionID int64
		var existingHash, existingStatus string
		err := tx.QueryRow(ctx, `
SELECT id,canonical_hash,status
FROM model_egress_policy_versions
WHERE organization_id=$1 AND policy_id=$2 AND policy_version=$3`, plan.OrganizationID, plan.Policy.PolicyID, plan.Policy.PolicyVersion).Scan(&policyVersionID, &existingHash, &existingStatus)
		switch {
		case err == nil:
			if existingStatus != "materialized" {
				return modelegress.RegistrySyncResult{}, fmt.Errorf("%w: policy version is not materialized", modelegress.ErrPolicyConflict)
			}
			if existingHash != plan.CanonicalHash {
				return modelegress.RegistrySyncResult{}, fmt.Errorf("%w: policy version already exists with different hash", modelegress.ErrPolicyConflict)
			}
		case errors.Is(err, pgx.ErrNoRows):
			var existingVersion int
			err = tx.QueryRow(ctx, `
SELECT policy_version
FROM model_egress_policy_versions
WHERE organization_id=$1 AND policy_id=$2 AND canonical_hash=$3`, plan.OrganizationID, plan.Policy.PolicyID, plan.CanonicalHash).Scan(&existingVersion)
			if err == nil {
				return modelegress.RegistrySyncResult{}, fmt.Errorf("%w: policy hash already exists as version %d", modelegress.ErrPolicyConflict, existingVersion)
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				return modelegress.RegistrySyncResult{}, mapError(err)
			}
			if err = tx.QueryRow(ctx, `
INSERT INTO model_egress_policy_versions(
    organization_id,policy_id,policy_version,canonical_hash,
    introduced_by_organization_revision_id,status
) VALUES($1,$2,$3,$4,$5,'materializing')
RETURNING id`, plan.OrganizationID, plan.Policy.PolicyID, plan.Policy.PolicyVersion, plan.CanonicalHash, plan.OrganizationRevisionID).Scan(&policyVersionID); err != nil {
				return modelegress.RegistrySyncResult{}, mapError(err)
			}
			for _, deny := range plan.Policy.HardDenies {
				if _, err = tx.Exec(ctx, `
INSERT INTO model_egress_rules(
    policy_version_id,organization_id,provider_id,data_classification,effect,reason_code,hard_deny
) VALUES($1,$2,'*',$3,'deny',$4,TRUE)`, policyVersionID, plan.OrganizationID, deny.DataClassification, deny.ReasonCode); err != nil {
					return modelegress.RegistrySyncResult{}, mapError(err)
				}
			}
			for _, rule := range plan.Policy.Rules {
				if _, err = tx.Exec(ctx, `
INSERT INTO model_egress_rules(
    policy_version_id,organization_id,provider_id,data_classification,effect,reason_code,hard_deny
) VALUES($1,$2,$3,$4,$5,$6,FALSE)`, policyVersionID, plan.OrganizationID, rule.ProviderID, rule.DataClassification, rule.Effect, rule.ReasonCode); err != nil {
					return modelegress.RegistrySyncResult{}, mapError(err)
				}
			}
			finalized, finalizeErr := tx.Exec(ctx, `
UPDATE model_egress_policy_versions
SET status='materialized'
WHERE id=$1 AND organization_id=$2 AND status='materializing'`, policyVersionID, plan.OrganizationID)
			if finalizeErr != nil {
				return modelegress.RegistrySyncResult{}, mapError(finalizeErr)
			}
			if finalized.RowsAffected() != 1 {
				return modelegress.RegistrySyncResult{}, modelegress.ErrPolicyConflict
			}
		default:
			return modelegress.RegistrySyncResult{}, mapError(err)
		}

		actualRules, rulesErr := materializedRules(ctx, tx, policyVersionID)
		if rulesErr != nil {
			return modelegress.RegistrySyncResult{}, rulesErr
		}
		if !equalRules(actualRules, expectedMaterializedRules(plan.Policy)) {
			return modelegress.RegistrySyncResult{}, fmt.Errorf("%w: materialized rules differ from canonical policy", modelegress.ErrPolicyConflict)
		}

		var existingPolicyID int64
		var bindingHash string
		err = tx.QueryRow(ctx, `
SELECT policy_version_id,canonical_hash
FROM model_egress_revision_bindings
WHERE organization_id=$1 AND organization_revision_id=$2`, plan.OrganizationID, plan.OrganizationRevisionID).Scan(&existingPolicyID, &bindingHash)
		if err == nil {
			if existingPolicyID != policyVersionID || bindingHash != plan.CanonicalHash {
				return modelegress.RegistrySyncResult{}, fmt.Errorf("%w: organization revision already bound to another policy", modelegress.ErrPolicyConflict)
			}
			return modelegress.RegistrySyncResult{
				NoOp: true, OrganizationRevisionID: plan.OrganizationRevisionID, PolicyVersionID: policyVersionID,
				PolicyID: plan.Policy.PolicyID, PolicyVersion: plan.Policy.PolicyVersion,
				CanonicalHash: plan.CanonicalHash, Rules: len(plan.Policy.Rules) + len(plan.Policy.HardDenies),
			}, nil
		} else if errors.Is(err, pgx.ErrNoRows) {
			if _, err = tx.Exec(ctx, `
INSERT INTO model_egress_revision_bindings(
    organization_id,organization_revision_id,policy_version_id,canonical_hash
) VALUES($1,$2,$3,$4)`, plan.OrganizationID, plan.OrganizationRevisionID, policyVersionID, plan.CanonicalHash); err != nil {
				return modelegress.RegistrySyncResult{}, mapError(err)
			}
		} else {
			return modelegress.RegistrySyncResult{}, mapError(err)
		}

		payload, _ := json.Marshal(map[string]any{
			"organization_revision_id": plan.OrganizationRevisionID,
			"policy_version_id":        policyVersionID,
			"policy_id":                plan.Policy.PolicyID,
			"policy_version":           plan.Policy.PolicyVersion,
			"canonical_hash":           plan.CanonicalHash,
			"rules":                    len(plan.Policy.Rules) + len(plan.Policy.HardDenies),
		})
		if _, err = tx.Exec(ctx, `
INSERT INTO audit_events(event_type,actor_type,actor_id,subject_type,subject_id,payload)
VALUES('model.egress_registry_synced','system','orgctl','model_egress_registry',$1,$2::jsonb)`, plan.OrganizationID, payload); err != nil {
			return modelegress.RegistrySyncResult{}, mapError(err)
		}
		return modelegress.RegistrySyncResult{Applied: true, OrganizationRevisionID: plan.OrganizationRevisionID, PolicyVersionID: policyVersionID, PolicyID: plan.Policy.PolicyID, PolicyVersion: plan.Policy.PolicyVersion, CanonicalHash: plan.CanonicalHash, Rules: len(plan.Policy.Rules) + len(plan.Policy.HardDenies)}, nil
	})
}

func (s *Store) ResolveForRevision(ctx context.Context, organizationID string, revisionID int64) (modelegress.ResolvedPolicy, error) {
	var resolved modelegress.ResolvedPolicy
	err := s.pool.QueryRow(ctx, `
SELECT v.id,v.organization_id,v.policy_id,v.policy_version,v.canonical_hash,
       v.introduced_by_organization_revision_id,v.status,v.created_at,
       b.organization_revision_id,b.canonical_hash
FROM model_egress_revision_bindings b
JOIN model_egress_policy_versions v
  ON v.id=b.policy_version_id AND v.organization_id=b.organization_id AND v.canonical_hash=b.canonical_hash
WHERE b.organization_id=$1 AND b.organization_revision_id=$2`, organizationID, revisionID).Scan(
		&resolved.Version.ID, &resolved.Version.OrganizationID, &resolved.Version.PolicyID,
		&resolved.Version.PolicyVersion, &resolved.Version.CanonicalHash,
		&resolved.Version.IntroducedByOrganizationRevisionID, &resolved.Version.Status,
		&resolved.Version.CreatedAt, &resolved.OrganizationRevisionID, &resolved.CanonicalHash,
	)
	if err != nil {
		return resolved, mapError(err)
	}
	resolved.DefaultAction = modelegress.EffectDeny
	rows, err := s.pool.Query(ctx, `
SELECT provider_id,data_classification,effect,reason_code,hard_deny
FROM model_egress_rules
WHERE policy_version_id=$1
ORDER BY provider_id,data_classification`, resolved.Version.ID)
	if err != nil {
		return resolved, mapError(err)
	}
	defer rows.Close()
	for rows.Next() {
		var rule modelegress.Rule
		if err = rows.Scan(&rule.ProviderID, &rule.DataClassification, &rule.Effect, &rule.ReasonCode, &rule.HardDeny); err != nil {
			return resolved, mapError(err)
		}
		resolved.Rules = append(resolved.Rules, rule)
	}
	return resolved, mapError(rows.Err())
}
