package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/skillregistry"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const maxListLimit = 1000

type Store struct {
	pool           *pgxpool.Pool
	organizationID string
}

func New(store *platformpostgres.Store, organizationID string) (*Store, error) {
	if store == nil || store.Pool() == nil {
		return nil, errors.New("skill registry store requires initialized PostgreSQL")
	}
	organizationID = strings.TrimSpace(organizationID)
	if organizationID == "" {
		return nil, errors.New("skill registry store requires organization ID")
	}
	return &Store{pool: store.Pool(), organizationID: organizationID}, nil
}

var _ skillregistry.Repository = (*Store)(nil)

func (s *Store) CreateSkill(ctx context.Context, skill skillregistry.Skill, version skillregistry.SkillVersion, idempotencyKey string, evidence skillregistry.GovernanceEvidence) (skillregistry.Skill, skillregistry.SkillVersion, bool, error) {
	if err := skill.Validate(); err != nil {
		return skillregistry.Skill{}, skillregistry.SkillVersion{}, false, err
	}
	if err := version.Validate(); err != nil {
		return skillregistry.Skill{}, skillregistry.SkillVersion{}, false, err
	}
	if skill.OrganizationID != s.organizationID || version.OrganizationID != s.organizationID {
		return skillregistry.Skill{}, skillregistry.SkillVersion{}, false, fmt.Errorf("%w: skill registry store organization mismatch", skillregistry.ErrInvalidSkill)
	}
	if version.SkillID != skill.ID || version.Lifecycle != skillregistry.LifecycleDraft || version.Revision != 1 {
		return skillregistry.Skill{}, skillregistry.SkillVersion{}, false, fmt.Errorf("%w: persisted skill must start as an unreviewed draft version 1", skillregistry.ErrInvalidVersion)
	}
	key := strings.TrimSpace(idempotencyKey)
	if key == "" {
		return skillregistry.Skill{}, skillregistry.SkillVersion{}, false, fmt.Errorf("%w: idempotency_key is required", skillregistry.ErrInvalidVersion)
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return skillregistry.Skill{}, skillregistry.SkillVersion{}, false, fmt.Errorf("skillregistry/postgres: begin create skill: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if existing, ok, err := lookupSkillIdempotency(ctx, tx, s.organizationID, key); err != nil {
		return skillregistry.Skill{}, skillregistry.SkillVersion{}, false, err
	} else if ok {
		if existing.canonicalHash != version.CanonicalHash {
			return skillregistry.Skill{}, skillregistry.SkillVersion{}, false, fmt.Errorf("%w: idempotency key already commits different skill content", skillregistry.ErrIdempotencyConflict)
		}
		storedSkill, storedVersion, err := getSkillAndVersion(ctx, tx, s.organizationID, skill.ID, existing.versionID)
		if err != nil {
			return skillregistry.Skill{}, skillregistry.SkillVersion{}, false, err
		}
		if err := tx.Commit(ctx); err != nil {
			return skillregistry.Skill{}, skillregistry.SkillVersion{}, false, mapError("commit idempotent skill", err)
		}
		return storedSkill, storedVersion, true, nil
	}

	if _, err := tx.Exec(ctx, `INSERT INTO skill_registry_skills (organization_id,skill_id,created_by_role_id,created_at) VALUES ($1,$2,$3,$4) ON CONFLICT (organization_id,skill_id) DO NOTHING`, skill.OrganizationID, skill.ID, skill.CreatedByRole, skill.CreatedAt); err != nil {
		return skillregistry.Skill{}, skillregistry.SkillVersion{}, false, mapError("insert skill identity", err)
	}

	inserted, err := insertVersion(ctx, tx, version)
	if err != nil {
		return skillregistry.Skill{}, skillregistry.SkillVersion{}, false, err
	}
	if !inserted {
		var existingVersionID string
		if err := tx.QueryRow(ctx, `SELECT version_id FROM skill_registry_versions WHERE organization_id=$1 AND canonical_hash=$2`, s.organizationID, version.CanonicalHash).Scan(&existingVersionID); err != nil {
			return skillregistry.Skill{}, skillregistry.SkillVersion{}, false, mapError("resolve exact skill duplicate", err)
		}
		if err := insertSkillIdempotency(ctx, tx, s.organizationID, key, existingVersionID, version.CanonicalHash); err != nil {
			return skillregistry.Skill{}, skillregistry.SkillVersion{}, false, err
		}
		storedSkill, storedVersion, err := getSkillAndVersion(ctx, tx, s.organizationID, skill.ID, existingVersionID)
		if err != nil {
			return skillregistry.Skill{}, skillregistry.SkillVersion{}, false, err
		}
		if err := tx.Commit(ctx); err != nil {
			return skillregistry.Skill{}, skillregistry.SkillVersion{}, false, mapError("commit duplicate skill", err)
		}
		return storedSkill, storedVersion, true, nil
	}

	if _, err := tx.Exec(ctx, `INSERT INTO skill_registry_lifecycle_events (organization_id,skill_id,version_id,from_lifecycle,to_lifecycle,actor_role_id,decision_ref,revision,occurred_at) VALUES ($1,$2,$3,NULL,'draft',$4,$5,1,$6)`, skill.OrganizationID, skill.ID, version.ID, skill.CreatedByRole, evidence.DecisionRef, version.CreatedAt); err != nil {
		return skillregistry.Skill{}, skillregistry.SkillVersion{}, false, mapError("insert skill creation event", err)
	}
	if err := insertSkillIdempotency(ctx, tx, s.organizationID, key, version.ID, version.CanonicalHash); err != nil {
		return skillregistry.Skill{}, skillregistry.SkillVersion{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return skillregistry.Skill{}, skillregistry.SkillVersion{}, false, mapError("commit skill creation", err)
	}
	return skill, version, false, nil
}

func insertVersion(ctx context.Context, tx pgx.Tx, version skillregistry.SkillVersion) (bool, error) {
	manifest, err := json.Marshal(version.Manifest)
	if err != nil {
		return false, fmt.Errorf("skillregistry/postgres: encode manifest: %w", err)
	}
	source, err := json.Marshal(version.Source)
	if err != nil {
		return false, fmt.Errorf("skillregistry/postgres: encode source: %w", err)
	}
	result, err := tx.Exec(ctx, `INSERT INTO skill_registry_versions (
 organization_id,version_id,skill_id,version,lifecycle,manifest,source,content_hash,manifest_hash,canonical_hash,
 owner_approval,validation,activation_approval,supersedes_version_id,revision,created_at,updated_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
ON CONFLICT (organization_id,canonical_hash) DO NOTHING`,
		version.OrganizationID, version.ID, version.SkillID, version.Version, string(version.Lifecycle), json.RawMessage(manifest), json.RawMessage(source),
		version.ContentHash, version.ManifestHash, version.CanonicalHash,
		nil, nil, nil, nullableString(version.SupersedesVersion), version.Revision, version.CreatedAt, version.UpdatedAt)
	if err != nil {
		return false, mapError("insert skill version content", err)
	}
	return result.RowsAffected() == 1, nil
}

func (s *Store) GetSkill(ctx context.Context, organizationID, skillID string) (skillregistry.Skill, error) {
	organizationID, skillID = strings.TrimSpace(organizationID), strings.TrimSpace(skillID)
	if organizationID == "" || skillID == "" {
		return skillregistry.Skill{}, fmt.Errorf("%w: organization_id and skill_id are required", skillregistry.ErrInvalidSkill)
	}
	if organizationID != s.organizationID {
		return skillregistry.Skill{}, fmt.Errorf("%w: skill registry store organization mismatch", skillregistry.ErrNotFound)
	}
	return getSkill(ctx, s.pool, organizationID, skillID)
}

func (s *Store) GetVersion(ctx context.Context, organizationID, versionID string) (skillregistry.SkillVersion, error) {
	organizationID, versionID = strings.TrimSpace(organizationID), strings.TrimSpace(versionID)
	if organizationID == "" || versionID == "" {
		return skillregistry.SkillVersion{}, fmt.Errorf("%w: organization_id and version_id are required", skillregistry.ErrInvalidVersion)
	}
	if organizationID != s.organizationID {
		return skillregistry.SkillVersion{}, fmt.Errorf("%w: skill registry store organization mismatch", skillregistry.ErrNotFound)
	}
	return getVersion(ctx, s.pool, organizationID, versionID)
}

func (s *Store) ListVersions(ctx context.Context, organizationID, skillID string) ([]skillregistry.SkillVersion, error) {
	organizationID, skillID = strings.TrimSpace(organizationID), strings.TrimSpace(skillID)
	if organizationID == "" || skillID == "" || organizationID != s.organizationID {
		return nil, fmt.Errorf("%w: invalid organization or skill filter", skillregistry.ErrInvalidVersion)
	}
	rows, err := s.pool.Query(ctx, `SELECT version_id FROM skill_registry_versions WHERE organization_id=$1 AND skill_id=$2 ORDER BY version DESC`, organizationID, skillID)
	if err != nil {
		return nil, mapError("list skill versions", err)
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, mapError("scan skill version id", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, mapError("iterate skill versions", err)
	}
	result := make([]skillregistry.SkillVersion, 0, len(ids))
	for _, id := range ids {
		version, err := getVersion(ctx, s.pool, organizationID, id)
		if err != nil {
			return nil, err
		}
		result = append(result, version)
	}
	return result, nil
}

func (s *Store) SaveVersion(ctx context.Context, version skillregistry.SkillVersion, expectedRevision int64, event skillregistry.LifecycleEvent) (skillregistry.SkillVersion, error) {
	if err := version.Validate(); err != nil {
		return skillregistry.SkillVersion{}, err
	}
	if version.OrganizationID != s.organizationID {
		return skillregistry.SkillVersion{}, fmt.Errorf("%w: skill registry store organization mismatch", skillregistry.ErrInvalidVersion)
	}
	if expectedRevision <= 0 || version.Revision != expectedRevision+1 {
		return skillregistry.SkillVersion{}, fmt.Errorf("%w: expected revision %d does not precede version revision %d", skillregistry.ErrRevisionConflict, expectedRevision, version.Revision)
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return skillregistry.SkillVersion{}, fmt.Errorf("skillregistry/postgres: begin save version: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var currentLifecycle string
	var currentRevision int64
	var canonicalHash string
	if err := tx.QueryRow(ctx, `SELECT lifecycle,revision,canonical_hash FROM skill_registry_versions WHERE organization_id=$1 AND version_id=$2 FOR UPDATE`, s.organizationID, version.ID).Scan(&currentLifecycle, &currentRevision, &canonicalHash); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return skillregistry.SkillVersion{}, fmt.Errorf("%w: %s", skillregistry.ErrNotFound, version.ID)
		}
		return skillregistry.SkillVersion{}, mapError("lock skill version", err)
	}
	if currentRevision != expectedRevision {
		return skillregistry.SkillVersion{}, fmt.Errorf("%w: version %s expected revision %d current %d", skillregistry.ErrRevisionConflict, version.ID, expectedRevision, currentRevision)
	}
	if err := skillregistry.ValidateTransition(skillregistry.Lifecycle(currentLifecycle), version.Lifecycle); err != nil {
		return skillregistry.SkillVersion{}, err
	}
	if version.CanonicalHash != canonicalHash {
		return skillregistry.SkillVersion{}, fmt.Errorf("%w: lifecycle mutation changed immutable skill content", skillregistry.ErrSourceDrift)
	}

	if _, err := tx.Exec(ctx, `INSERT INTO skill_registry_lifecycle_events (organization_id,skill_id,version_id,from_lifecycle,to_lifecycle,actor_role_id,decision_ref,revision,occurred_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		s.organizationID, version.SkillID, version.ID, currentLifecycle, string(version.Lifecycle), event.ActorRoleID, event.DecisionRef, version.Revision, version.UpdatedAt); err != nil {
		return skillregistry.SkillVersion{}, mapError("insert skill lifecycle event", err)
	}

	ownerApproval, err := marshalEvidence(version.OwnerApproval)
	if err != nil {
		return skillregistry.SkillVersion{}, err
	}
	validation, err := marshalValidation(version.Validation)
	if err != nil {
		return skillregistry.SkillVersion{}, err
	}
	activationApproval, err := marshalEvidence(version.ActivationApproval)
	if err != nil {
		return skillregistry.SkillVersion{}, err
	}
	result, err := tx.Exec(ctx, `UPDATE skill_registry_versions SET lifecycle=$3,owner_approval=$4,validation=$5,activation_approval=$6,revision=$7,updated_at=$8 WHERE organization_id=$1 AND version_id=$2 AND revision=$9`,
		s.organizationID, version.ID, string(version.Lifecycle), ownerApproval, validation, activationApproval, version.Revision, version.UpdatedAt, expectedRevision)
	if err != nil {
		return skillregistry.SkillVersion{}, mapError("update skill lifecycle", err)
	}
	if result.RowsAffected() != 1 {
		return skillregistry.SkillVersion{}, fmt.Errorf("%w: version %s", skillregistry.ErrRevisionConflict, version.ID)
	}
	if err := tx.Commit(ctx); err != nil {
		return skillregistry.SkillVersion{}, mapError("commit skill lifecycle mutation", err)
	}
	return version, nil
}

func (s *Store) CreateAssignment(ctx context.Context, assignment skillregistry.SkillAssignment, idempotencyKey string, event skillregistry.AssignmentEvent) (skillregistry.SkillAssignment, bool, error) {
	if err := assignment.Validate(); err != nil {
		return skillregistry.SkillAssignment{}, false, err
	}
	if assignment.OrganizationID != s.organizationID {
		return skillregistry.SkillAssignment{}, false, fmt.Errorf("%w: skill registry store organization mismatch", skillregistry.ErrInvalidAssignment)
	}
	if assignment.Status != skillregistry.AssignmentActive || assignment.Revision != 1 {
		return skillregistry.SkillAssignment{}, false, fmt.Errorf("%w: persisted assignment must start active at revision 1", skillregistry.ErrInvalidAssignment)
	}
	key := strings.TrimSpace(idempotencyKey)
	if key == "" {
		return skillregistry.SkillAssignment{}, false, fmt.Errorf("%w: idempotency_key is required", skillregistry.ErrInvalidAssignment)
	}
	identityHash := assignmentIdentityHash(assignment)
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return skillregistry.SkillAssignment{}, false, fmt.Errorf("skillregistry/postgres: begin create assignment: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var existingAssignmentID, existingHash string
	err = tx.QueryRow(ctx, `SELECT assignment_id,identity_hash FROM skill_registry_assignment_idempotency WHERE organization_id=$1 AND idempotency_key=$2 FOR SHARE`, s.organizationID, key).Scan(&existingAssignmentID, &existingHash)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return skillregistry.SkillAssignment{}, false, mapError("lookup assignment idempotency", err)
	}
	if err == nil {
		if existingHash != identityHash {
			return skillregistry.SkillAssignment{}, false, fmt.Errorf("%w: idempotency key already commits a different assignment", skillregistry.ErrIdempotencyConflict)
		}
		existing, err := getAssignment(ctx, tx, s.organizationID, existingAssignmentID)
		if err != nil {
			return skillregistry.SkillAssignment{}, false, err
		}
		if err := tx.Commit(ctx); err != nil {
			return skillregistry.SkillAssignment{}, false, mapError("commit idempotent assignment", err)
		}
		return existing, true, nil
	}

	if _, err := tx.Exec(ctx, `INSERT INTO skill_registry_assignments (organization_id,assignment_id,role_id,skill_id,skill_version_id,status,capability_review_ref,assigned_by_role_id,assignment_decision_ref,revision,assigned_at,updated_at) VALUES ($1,$2,$3,$4,$5,'active',$6,$7,$8,1,$9,$10)`,
		assignment.OrganizationID, assignment.ID, assignment.RoleID, assignment.SkillID, assignment.SkillVersionID, assignment.CapabilityReviewRef, assignment.AssignedBy, assignment.AssignmentDecisionRef, assignment.AssignedAt, assignment.UpdatedAt); err != nil {
		return skillregistry.SkillAssignment{}, false, mapErrorAssignmentConflict(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO skill_registry_assignment_events (organization_id,assignment_id,skill_id,skill_version_id,role_id,action,actor_role_id,decision_ref,reason_code,revision,occurred_at) VALUES ($1,$2,$3,$4,$5,'assign',$6,$7,NULL,1,$8)`,
		assignment.OrganizationID, assignment.ID, assignment.SkillID, assignment.SkillVersionID, assignment.RoleID, event.ActorRoleID, event.DecisionRef, assignment.AssignedAt); err != nil {
		return skillregistry.SkillAssignment{}, false, mapError("insert assignment event", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO skill_registry_assignment_idempotency (organization_id,idempotency_key,assignment_id,identity_hash) VALUES ($1,$2,$3,$4)`, s.organizationID, key, assignment.ID, identityHash); err != nil {
		return skillregistry.SkillAssignment{}, false, mapError("insert assignment idempotency", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return skillregistry.SkillAssignment{}, false, mapError("commit assignment creation", err)
	}
	return assignment, false, nil
}

func (s *Store) GetAssignment(ctx context.Context, organizationID, assignmentID string) (skillregistry.SkillAssignment, error) {
	organizationID, assignmentID = strings.TrimSpace(organizationID), strings.TrimSpace(assignmentID)
	if organizationID == "" || assignmentID == "" {
		return skillregistry.SkillAssignment{}, fmt.Errorf("%w: organization_id and assignment_id are required", skillregistry.ErrInvalidAssignment)
	}
	if organizationID != s.organizationID {
		return skillregistry.SkillAssignment{}, fmt.Errorf("%w: skill registry store organization mismatch", skillregistry.ErrNotFound)
	}
	return getAssignment(ctx, s.pool, organizationID, assignmentID)
}

func (s *Store) ListActiveAssignmentsForRole(ctx context.Context, organizationID, roleID string) ([]skillregistry.SkillAssignment, error) {
	organizationID, roleID = strings.TrimSpace(organizationID), strings.TrimSpace(roleID)
	if organizationID == "" || roleID == "" || organizationID != s.organizationID {
		return nil, fmt.Errorf("%w: invalid organization or role filter", skillregistry.ErrInvalidAssignment)
	}
	rows, err := s.pool.Query(ctx, `SELECT assignment_id FROM skill_registry_assignments WHERE organization_id=$1 AND role_id=$2 AND status='active' ORDER BY assigned_at DESC LIMIT $3`, organizationID, roleID, maxListLimit)
	if err != nil {
		return nil, mapError("list active skill assignments", err)
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, mapError("scan assignment id", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, mapError("iterate active skill assignments", err)
	}
	result := make([]skillregistry.SkillAssignment, 0, len(ids))
	for _, id := range ids {
		assignment, err := getAssignment(ctx, s.pool, organizationID, id)
		if err != nil {
			return nil, err
		}
		result = append(result, assignment)
	}
	return result, nil
}

func (s *Store) SaveAssignment(ctx context.Context, assignment skillregistry.SkillAssignment, expectedRevision int64, event skillregistry.AssignmentEvent) (skillregistry.SkillAssignment, error) {
	if err := assignment.Validate(); err != nil {
		return skillregistry.SkillAssignment{}, err
	}
	if assignment.OrganizationID != s.organizationID {
		return skillregistry.SkillAssignment{}, fmt.Errorf("%w: skill registry store organization mismatch", skillregistry.ErrInvalidAssignment)
	}
	if expectedRevision <= 0 || assignment.Revision != expectedRevision+1 {
		return skillregistry.SkillAssignment{}, fmt.Errorf("%w: expected revision %d does not precede assignment revision %d", skillregistry.ErrRevisionConflict, expectedRevision, assignment.Revision)
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return skillregistry.SkillAssignment{}, fmt.Errorf("skillregistry/postgres: begin save assignment: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var currentStatus string
	var currentRevision int64
	if err := tx.QueryRow(ctx, `SELECT status,revision FROM skill_registry_assignments WHERE organization_id=$1 AND assignment_id=$2 FOR UPDATE`, s.organizationID, assignment.ID).Scan(&currentStatus, &currentRevision); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return skillregistry.SkillAssignment{}, fmt.Errorf("%w: %s", skillregistry.ErrNotFound, assignment.ID)
		}
		return skillregistry.SkillAssignment{}, mapError("lock skill assignment", err)
	}
	if currentRevision != expectedRevision {
		return skillregistry.SkillAssignment{}, fmt.Errorf("%w: assignment %s expected revision %d current %d", skillregistry.ErrRevisionConflict, assignment.ID, expectedRevision, currentRevision)
	}
	if skillregistry.AssignmentStatus(currentStatus) != skillregistry.AssignmentActive || assignment.Status != skillregistry.AssignmentRevoked {
		return skillregistry.SkillAssignment{}, fmt.Errorf("%w: invalid assignment transition %s -> %s", skillregistry.ErrInvalidAssignment, currentStatus, assignment.Status)
	}

	if _, err := tx.Exec(ctx, `INSERT INTO skill_registry_assignment_events (organization_id,assignment_id,skill_id,skill_version_id,role_id,action,actor_role_id,decision_ref,reason_code,revision,occurred_at) VALUES ($1,$2,$3,$4,$5,'revoke',$6,$7,$8,$9,$10)`,
		s.organizationID, assignment.ID, assignment.SkillID, assignment.SkillVersionID, assignment.RoleID, event.ActorRoleID, event.DecisionRef, nullableString(event.ReasonCode), assignment.Revision, assignment.UpdatedAt); err != nil {
		return skillregistry.SkillAssignment{}, mapError("insert assignment revoke event", err)
	}
	result, err := tx.Exec(ctx, `UPDATE skill_registry_assignments SET status='revoked',revision=$3,updated_at=$4,revoked_at=$5,revoke_reason=$6 WHERE organization_id=$1 AND assignment_id=$2 AND revision=$7`,
		s.organizationID, assignment.ID, assignment.Revision, assignment.UpdatedAt, assignment.RevokedAt, assignment.RevokeReason, expectedRevision)
	if err != nil {
		return skillregistry.SkillAssignment{}, mapError("update skill assignment", err)
	}
	if result.RowsAffected() != 1 {
		return skillregistry.SkillAssignment{}, fmt.Errorf("%w: assignment %s", skillregistry.ErrRevisionConflict, assignment.ID)
	}
	if err := tx.Commit(ctx); err != nil {
		return skillregistry.SkillAssignment{}, mapError("commit assignment revocation", err)
	}
	return assignment, nil
}

type queryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func getSkill(ctx context.Context, q queryer, organizationID, skillID string) (skillregistry.Skill, error) {
	var skill skillregistry.Skill
	if err := q.QueryRow(ctx, `SELECT organization_id,skill_id,created_by_role_id,created_at FROM skill_registry_skills WHERE organization_id=$1 AND skill_id=$2`, organizationID, skillID).Scan(&skill.OrganizationID, &skill.ID, &skill.CreatedByRole, &skill.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return skillregistry.Skill{}, fmt.Errorf("%w: %s", skillregistry.ErrNotFound, skillID)
		}
		return skillregistry.Skill{}, mapError("get skill", err)
	}
	skill.CreatedAt = skill.CreatedAt.UTC()
	return skill, nil
}

func getVersion(ctx context.Context, q queryer, organizationID, versionID string) (skillregistry.SkillVersion, error) {
	var version skillregistry.SkillVersion
	var lifecycle string
	var manifest, source []byte
	var ownerApproval, validation, activationApproval []byte
	var supersedes *string
	if err := q.QueryRow(ctx, `SELECT organization_id,version_id,skill_id,version,lifecycle,manifest,source,content_hash,manifest_hash,canonical_hash,owner_approval,validation,activation_approval,supersedes_version_id,revision,created_at,updated_at FROM skill_registry_versions WHERE organization_id=$1 AND version_id=$2`, organizationID, versionID).Scan(
		&version.OrganizationID, &version.ID, &version.SkillID, &version.Version, &lifecycle, &manifest, &source, &version.ContentHash, &version.ManifestHash, &version.CanonicalHash, &ownerApproval, &validation, &activationApproval, &supersedes, &version.Revision, &version.CreatedAt, &version.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return skillregistry.SkillVersion{}, fmt.Errorf("%w: %s", skillregistry.ErrNotFound, versionID)
		}
		return skillregistry.SkillVersion{}, mapError("get skill version", err)
	}
	version.Lifecycle = skillregistry.Lifecycle(lifecycle)
	version.SupersedesVersion = stringOrEmpty(supersedes)
	version.CreatedAt = version.CreatedAt.UTC()
	version.UpdatedAt = version.UpdatedAt.UTC()
	if err := json.Unmarshal(manifest, &version.Manifest); err != nil {
		return skillregistry.SkillVersion{}, fmt.Errorf("skillregistry/postgres: decode manifest: %w", err)
	}
	if err := json.Unmarshal(source, &version.Source); err != nil {
		return skillregistry.SkillVersion{}, fmt.Errorf("skillregistry/postgres: decode source: %w", err)
	}
	if ownerApproval != nil {
		var value skillregistry.ApprovalEvidence
		if err := json.Unmarshal(ownerApproval, &value); err != nil {
			return skillregistry.SkillVersion{}, fmt.Errorf("skillregistry/postgres: decode owner approval: %w", err)
		}
		version.OwnerApproval = &value
	}
	if validation != nil {
		var value skillregistry.ValidationEvidence
		if err := json.Unmarshal(validation, &value); err != nil {
			return skillregistry.SkillVersion{}, fmt.Errorf("skillregistry/postgres: decode validation evidence: %w", err)
		}
		version.Validation = &value
	}
	if activationApproval != nil {
		var value skillregistry.ApprovalEvidence
		if err := json.Unmarshal(activationApproval, &value); err != nil {
			return skillregistry.SkillVersion{}, fmt.Errorf("skillregistry/postgres: decode activation approval: %w", err)
		}
		version.ActivationApproval = &value
	}
	if err := version.Validate(); err != nil {
		return skillregistry.SkillVersion{}, fmt.Errorf("skillregistry/postgres: stored version failed domain validation: %w", err)
	}
	return version, nil
}

func getSkillAndVersion(ctx context.Context, q queryer, organizationID, skillID, versionID string) (skillregistry.Skill, skillregistry.SkillVersion, error) {
	skill, err := getSkill(ctx, q, organizationID, skillID)
	if err != nil {
		return skillregistry.Skill{}, skillregistry.SkillVersion{}, err
	}
	version, err := getVersion(ctx, q, organizationID, versionID)
	if err != nil {
		return skillregistry.Skill{}, skillregistry.SkillVersion{}, err
	}
	return skill, version, nil
}

func getAssignment(ctx context.Context, q queryer, organizationID, assignmentID string) (skillregistry.SkillAssignment, error) {
	var assignment skillregistry.SkillAssignment
	var status string
	var revokedAt *time.Time
	var revokeReason *string
	if err := q.QueryRow(ctx, `SELECT organization_id,assignment_id,role_id,skill_id,skill_version_id,status,capability_review_ref,assigned_by_role_id,assignment_decision_ref,revision,assigned_at,updated_at,revoked_at,revoke_reason FROM skill_registry_assignments WHERE organization_id=$1 AND assignment_id=$2`, organizationID, assignmentID).Scan(
		&assignment.OrganizationID, &assignment.ID, &assignment.RoleID, &assignment.SkillID, &assignment.SkillVersionID, &status, &assignment.CapabilityReviewRef, &assignment.AssignedBy, &assignment.AssignmentDecisionRef, &assignment.Revision, &assignment.AssignedAt, &assignment.UpdatedAt, &revokedAt, &revokeReason); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return skillregistry.SkillAssignment{}, fmt.Errorf("%w: %s", skillregistry.ErrNotFound, assignmentID)
		}
		return skillregistry.SkillAssignment{}, mapError("get skill assignment", err)
	}
	assignment.Status = skillregistry.AssignmentStatus(status)
	assignment.AssignedAt = assignment.AssignedAt.UTC()
	assignment.UpdatedAt = assignment.UpdatedAt.UTC()
	if revokedAt != nil {
		value := revokedAt.UTC()
		assignment.RevokedAt = &value
	}
	assignment.RevokeReason = stringOrEmpty(revokeReason)
	if err := assignment.Validate(); err != nil {
		return skillregistry.SkillAssignment{}, fmt.Errorf("skillregistry/postgres: stored assignment failed domain validation: %w", err)
	}
	return assignment, nil
}

type skillIdempotencyRecord struct{ versionID, canonicalHash string }

func lookupSkillIdempotency(ctx context.Context, tx pgx.Tx, organizationID, key string) (skillIdempotencyRecord, bool, error) {
	var record skillIdempotencyRecord
	err := tx.QueryRow(ctx, `SELECT version_id,canonical_hash FROM skill_registry_skill_idempotency WHERE organization_id=$1 AND idempotency_key=$2 FOR SHARE`, organizationID, key).Scan(&record.versionID, &record.canonicalHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return skillIdempotencyRecord{}, false, nil
	}
	if err != nil {
		return skillIdempotencyRecord{}, false, mapError("lookup skill idempotency", err)
	}
	return record, true, nil
}

func insertSkillIdempotency(ctx context.Context, tx pgx.Tx, organizationID, key, versionID, canonicalHash string) error {
	result, err := tx.Exec(ctx, `INSERT INTO skill_registry_skill_idempotency (organization_id,idempotency_key,version_id,canonical_hash) VALUES ($1,$2,$3,$4) ON CONFLICT (organization_id,idempotency_key) DO NOTHING`, organizationID, key, versionID, canonicalHash)
	if err != nil {
		return mapError("insert skill idempotency", err)
	}
	if result.RowsAffected() == 1 {
		return nil
	}
	var existingVersion, existingHash string
	if err := tx.QueryRow(ctx, `SELECT version_id,canonical_hash FROM skill_registry_skill_idempotency WHERE organization_id=$1 AND idempotency_key=$2`, organizationID, key).Scan(&existingVersion, &existingHash); err != nil {
		return mapError("re-read skill idempotency", err)
	}
	if existingHash != canonicalHash || existingVersion != versionID {
		return fmt.Errorf("%w: idempotency key already commits different skill version", skillregistry.ErrIdempotencyConflict)
	}
	return nil
}

func assignmentIdentityHash(assignment skillregistry.SkillAssignment) string {
	body := strings.Join([]string{"skill-registry-assignment.v1", assignment.OrganizationID, assignment.RoleID, assignment.SkillID, assignment.SkillVersionID, assignment.AssignedBy, assignment.AssignmentDecisionRef}, "|")
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

func marshalEvidence(value *skillregistry.ApprovalEvidence) ([]byte, error) {
	if value == nil {
		return nil, nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("skillregistry/postgres: encode approval evidence: %w", err)
	}
	return raw, nil
}

func marshalValidation(value *skillregistry.ValidationEvidence) ([]byte, error) {
	if value == nil {
		return nil, nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("skillregistry/postgres: encode validation evidence: %w", err)
	}
	return raw, nil
}

func nullableString(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

func stringOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func mapErrorAssignmentConflict(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return fmt.Errorf("%w: role already has an active assignment for this skill", skillregistry.ErrAssignmentConflict)
	}
	return mapError("insert skill assignment", err)
}

func mapError(operation string, err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "40001", "40P01":
			return fmt.Errorf("%w: %s", skillregistry.ErrRevisionConflict, operation)
		case "23503":
			return fmt.Errorf("%w: %s violates skill registry reference integrity", skillregistry.ErrInvalidVersion, operation)
		case "23505":
			return fmt.Errorf("%w: %s conflicts with existing skill registry state", skillregistry.ErrAssignmentConflict, operation)
		case "23514":
			return fmt.Errorf("%w: %s violates skill registry invariant", skillregistry.ErrInvalidVersion, operation)
		}
	}
	return fmt.Errorf("skillregistry/postgres: %s: %w", operation, err)
}
