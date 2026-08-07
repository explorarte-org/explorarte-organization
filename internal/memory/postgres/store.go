package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/memory"
	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
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
		return nil, errors.New("memory store requires initialized PostgreSQL")
	}
	organizationID = strings.TrimSpace(organizationID)
	if organizationID == "" {
		return nil, errors.New("memory store requires organization ID")
	}
	return &Store{pool: store.Pool(), organizationID: organizationID}, nil
}

var _ memory.Repository = (*Store)(nil)

func (s *Store) CreateCandidate(ctx context.Context, command memory.CreateCandidateCommand) (memory.Entry, bool, error) {
	entry := command.Entry
	if err := entry.Validate(); err != nil {
		return memory.Entry{}, false, err
	}
	if entry.OrganizationID != s.organizationID {
		return memory.Entry{}, false, fmt.Errorf("%w: memory store organization mismatch", memory.ErrInvalidRequest)
	}
	if entry.Status != memory.StatusCandidate || entry.Revision != 1 || entry.ReviewerID != "" || entry.ReviewedAt != nil {
		return memory.Entry{}, false, fmt.Errorf("%w: persisted candidate must start at candidate revision 1 without review metadata", memory.ErrInvalidEntry)
	}
	key := strings.TrimSpace(command.IdempotencyKey)
	if key == "" {
		return memory.Entry{}, false, fmt.Errorf("%w: idempotency_key is required", memory.ErrInvalidRequest)
	}
	canonicalHash, err := entry.CanonicalHash()
	if err != nil {
		return memory.Entry{}, false, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return memory.Entry{}, false, fmt.Errorf("memory/postgres: begin create candidate: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if existing, ok, err := lookupIdempotency(ctx, tx, s.organizationID, key); err != nil {
		return memory.Entry{}, false, err
	} else if ok {
		if existing.canonicalHash != canonicalHash {
			return memory.Entry{}, false, fmt.Errorf("%w: idempotency key already commits different memory content", memory.ErrConflict)
		}
		value, err := getEntry(ctx, tx, s.organizationID, existing.entryKey)
		if err != nil {
			return memory.Entry{}, false, err
		}
		if err := tx.Commit(ctx); err != nil {
			return memory.Entry{}, false, mapError("commit idempotent candidate", err)
		}
		return value, true, nil
	}
	if entry.SupersedesEntryID != "" {
		var priorRole string
		if err := tx.QueryRow(ctx, `SELECT role_id FROM organizational_memory_versions WHERE organization_id=$1 AND entry_key=$2`, s.organizationID, entry.SupersedesEntryID).Scan(&priorRole); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return memory.Entry{}, false, fmt.Errorf("%w: superseded memory entry %s", memory.ErrEntryNotFound, entry.SupersedesEntryID)
			}
			return memory.Entry{}, false, mapError("resolve superseded memory", err)
		}
		if priorRole != entry.RoleID {
			return memory.Entry{}, false, fmt.Errorf("%w: superseded memory belongs to another role", memory.ErrConflict)
		}
	}
	inserted, err := insertVersion(ctx, tx, entry, canonicalHash)
	if err != nil {
		return memory.Entry{}, false, err
	}
	if !inserted {
		var existingKey string
		if err := tx.QueryRow(ctx, `SELECT entry_key FROM organizational_memory_versions WHERE organization_id=$1 AND canonical_hash=$2`, s.organizationID, canonicalHash).Scan(&existingKey); err != nil {
			return memory.Entry{}, false, mapError("resolve exact memory duplicate", err)
		}
		if err := insertIdempotency(ctx, tx, s.organizationID, key, existingKey, canonicalHash); err != nil {
			return memory.Entry{}, false, err
		}
		value, err := getEntry(ctx, tx, s.organizationID, existingKey)
		if err != nil {
			return memory.Entry{}, false, err
		}
		if err := tx.Commit(ctx); err != nil {
			return memory.Entry{}, false, mapError("commit duplicate candidate", err)
		}
		return value, true, nil
	}
	for i, ref := range entry.EvidenceRefs {
		if _, err := tx.Exec(ctx, `INSERT INTO organizational_memory_evidence_refs (organization_id,entry_key,ordinal,reference,digest) VALUES ($1,$2,$3,$4,$5)`, s.organizationID, entry.ID, i+1, ref.Reference, ref.Digest); err != nil {
			return memory.Entry{}, false, mapError("insert memory evidence reference", err)
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO organizational_memory_state_events (organization_id,entry_key,from_status,to_status,actor_role_id,reason,revision,created_at) VALUES ($1,$2,NULL,'candidate',$3,'candidate_created',1,$4)`, s.organizationID, entry.ID, entry.ProposedBy, entry.UpdatedAt); err != nil {
		return memory.Entry{}, false, mapError("insert memory creation event", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO organizational_memory_entries (organization_id,entry_key,status,reviewer_role_id,reviewed_at,revision,updated_at) VALUES ($1,$2,'candidate',NULL,NULL,1,$3)`, s.organizationID, entry.ID, entry.UpdatedAt); err != nil {
		return memory.Entry{}, false, mapError("insert memory lifecycle projection", err)
	}
	if err := insertIdempotency(ctx, tx, s.organizationID, key, entry.ID, canonicalHash); err != nil {
		return memory.Entry{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return memory.Entry{}, false, mapError("commit memory candidate", err)
	}
	return entry, false, nil
}

func insertVersion(ctx context.Context, tx pgx.Tx, entry memory.Entry, canonicalHash string) (bool, error) {
	result, err := tx.Exec(ctx, `INSERT INTO organizational_memory_versions (
 organization_id,entry_key,role_id,category,problem,correction,source_kind,source_run_id,canonical_hash,proposed_by_role_id,
 data_class,admission_attested_by,source_boundary,admission_evidence_ref,sanitization_evidence_ref,admission_attested_at,supersedes_entry_key,created_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
ON CONFLICT (organization_id,canonical_hash) DO NOTHING`, entry.OrganizationID, entry.ID, entry.RoleID, entry.Category, entry.Problem, entry.Correction, string(entry.SourceKind), entry.SourceRunID, canonicalHash, entry.ProposedBy, string(entry.Admission.DataClass), entry.Admission.AttestedBy, entry.Admission.SourceBoundary, entry.Admission.EvidenceRef, nullableString(entry.Admission.SanitizationEvidenceRef), entry.Admission.AttestedAt, nullableString(entry.SupersedesEntryID), entry.CreatedAt)
	if err != nil {
		return false, mapError("insert memory content version", err)
	}
	return result.RowsAffected() == 1, nil
}

func (s *Store) Get(ctx context.Context, organizationID, entryID string) (memory.Entry, error) {
	organizationID = strings.TrimSpace(organizationID)
	entryID = strings.TrimSpace(entryID)
	if organizationID == "" || entryID == "" {
		return memory.Entry{}, fmt.Errorf("%w: organization_id and entry_id are required", memory.ErrInvalidRequest)
	}
	if organizationID != s.organizationID {
		return memory.Entry{}, fmt.Errorf("%w: memory store organization mismatch", memory.ErrEntryNotFound)
	}
	return getEntry(ctx, s.pool, organizationID, entryID)
}

func (s *Store) Save(ctx context.Context, command memory.SaveCommand) (memory.Entry, error) {
	entry := command.Entry
	if err := entry.Validate(); err != nil {
		return memory.Entry{}, err
	}
	if entry.OrganizationID != s.organizationID {
		return memory.Entry{}, fmt.Errorf("%w: memory store organization mismatch", memory.ErrInvalidRequest)
	}
	if command.ExpectedRevision <= 0 || entry.Revision != command.ExpectedRevision+1 {
		return memory.Entry{}, fmt.Errorf("%w: expected revision %d does not precede entry revision %d", memory.ErrRevisionConflict, command.ExpectedRevision, entry.Revision)
	}
	actor := strings.TrimSpace(command.ActorID)
	reason := strings.TrimSpace(command.Reason)
	if actor == "" || reason == "" {
		return memory.Entry{}, fmt.Errorf("%w: actor and reason are required", memory.ErrInvalidRequest)
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return memory.Entry{}, fmt.Errorf("memory/postgres: begin save: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var currentStatus string
	var currentRevision int64
	var canonicalHash string
	if err := tx.QueryRow(ctx, `SELECT e.status,e.revision,v.canonical_hash FROM organizational_memory_entries e JOIN organizational_memory_versions v ON v.organization_id=e.organization_id AND v.entry_key=e.entry_key WHERE e.organization_id=$1 AND e.entry_key=$2 FOR UPDATE OF e`, s.organizationID, entry.ID).Scan(&currentStatus, &currentRevision, &canonicalHash); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return memory.Entry{}, fmt.Errorf("%w: %s", memory.ErrEntryNotFound, entry.ID)
		}
		return memory.Entry{}, mapError("lock memory entry", err)
	}
	if currentRevision != command.ExpectedRevision {
		return memory.Entry{}, fmt.Errorf("%w: entry %s expected revision %d current %d", memory.ErrRevisionConflict, entry.ID, command.ExpectedRevision, currentRevision)
	}
	if err := memory.ValidateTransition(memory.Status(currentStatus), entry.Status); err != nil {
		return memory.Entry{}, err
	}
	entryHash, err := entry.CanonicalHash()
	if err != nil {
		return memory.Entry{}, err
	}
	if entryHash != canonicalHash {
		return memory.Entry{}, fmt.Errorf("%w: lifecycle mutation changed immutable memory content", memory.ErrConflict)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO organizational_memory_state_events (organization_id,entry_key,from_status,to_status,actor_role_id,reason,revision,created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, s.organizationID, entry.ID, currentStatus, string(entry.Status), actor, reason, entry.Revision, entry.UpdatedAt); err != nil {
		return memory.Entry{}, mapError("insert memory state event", err)
	}
	result, err := tx.Exec(ctx, `UPDATE organizational_memory_entries SET status=$3,reviewer_role_id=$4,reviewed_at=$5,revision=$6,updated_at=$7 WHERE organization_id=$1 AND entry_key=$2 AND revision=$8`, s.organizationID, entry.ID, string(entry.Status), nullableString(entry.ReviewerID), entry.ReviewedAt, entry.Revision, entry.UpdatedAt, command.ExpectedRevision)
	if err != nil {
		return memory.Entry{}, mapError("update memory lifecycle", err)
	}
	if result.RowsAffected() != 1 {
		return memory.Entry{}, fmt.Errorf("%w: entry %s", memory.ErrRevisionConflict, entry.ID)
	}
	if err := tx.Commit(ctx); err != nil {
		return memory.Entry{}, mapError("commit memory lifecycle mutation", err)
	}
	return entry, nil
}

func (s *Store) List(ctx context.Context, filter memory.ListFilter) ([]memory.Entry, error) {
	filter.OrganizationID = strings.TrimSpace(filter.OrganizationID)
	filter.RoleID = strings.TrimSpace(filter.RoleID)
	if filter.OrganizationID == "" || filter.OrganizationID != s.organizationID {
		return nil, fmt.Errorf("%w: invalid organization filter", memory.ErrInvalidRequest)
	}
	if filter.Status != "" && !filter.Status.Valid() {
		return nil, fmt.Errorf("%w: invalid status filter %q", memory.ErrInvalidRequest, filter.Status)
	}
	limit, err := normalizeLimit(filter.Limit)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT e.entry_key FROM organizational_memory_entries e JOIN organizational_memory_versions v ON v.organization_id=e.organization_id AND v.entry_key=e.entry_key WHERE e.organization_id=$1 AND ($2='' OR v.role_id=$2) AND ($3='' OR e.status=$3) ORDER BY e.updated_at DESC,e.entry_key ASC LIMIT $4`, s.organizationID, filter.RoleID, string(filter.Status), limit)
	if err != nil {
		return nil, mapError("list memory entries", err)
	}
	defer rows.Close()
	keys := []string{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, mapError("scan memory list key", err)
		}
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		return nil, mapError("iterate memory list", err)
	}
	result := make([]memory.Entry, 0, len(keys))
	for _, key := range keys {
		entry, err := s.Get(ctx, s.organizationID, key)
		if err != nil {
			return nil, err
		}
		result = append(result, entry)
	}
	return result, nil
}
func (s *Store) ListApproved(ctx context.Context, filter memory.ApprovedFilter) ([]memory.Entry, error) {
	filter.OrganizationID = strings.TrimSpace(filter.OrganizationID)
	filter.RoleID = strings.TrimSpace(filter.RoleID)
	if filter.OrganizationID == "" || filter.RoleID == "" {
		return nil, fmt.Errorf("%w: approved memory requires organization and role scope", memory.ErrInvalidRequest)
	}
	return s.List(ctx, memory.ListFilter{OrganizationID: filter.OrganizationID, RoleID: filter.RoleID, Status: memory.StatusApproved, Limit: filter.Limit})
}

type queryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func getEntry(ctx context.Context, q queryer, organizationID, entryID string) (memory.Entry, error) {
	var entry memory.Entry
	var status, dataClass, sourceKind string
	var reviewer, sanitizationEvidence, supersedes *string
	var reviewedAt *time.Time
	if err := q.QueryRow(ctx, `SELECT v.entry_key,v.organization_id,v.role_id,v.category,v.problem,v.correction,v.source_kind,v.source_run_id,e.status,v.proposed_by_role_id,e.reviewer_role_id,v.data_class,v.admission_attested_by,v.source_boundary,v.admission_evidence_ref,v.sanitization_evidence_ref,v.admission_attested_at,v.supersedes_entry_key,e.revision,v.created_at,e.updated_at,e.reviewed_at FROM organizational_memory_versions v JOIN organizational_memory_entries e ON e.organization_id=v.organization_id AND e.entry_key=v.entry_key WHERE v.organization_id=$1 AND v.entry_key=$2`, organizationID, entryID).Scan(&entry.ID, &entry.OrganizationID, &entry.RoleID, &entry.Category, &entry.Problem, &entry.Correction, &sourceKind, &entry.SourceRunID, &status, &entry.ProposedBy, &reviewer, &dataClass, &entry.Admission.AttestedBy, &entry.Admission.SourceBoundary, &entry.Admission.EvidenceRef, &sanitizationEvidence, &entry.Admission.AttestedAt, &supersedes, &entry.Revision, &entry.CreatedAt, &entry.UpdatedAt, &reviewedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return memory.Entry{}, fmt.Errorf("%w: %s", memory.ErrEntryNotFound, entryID)
		}
		return memory.Entry{}, mapError("get memory entry", err)
	}
	entry.SourceKind = memory.SourceKind(sourceKind)
	entry.Status = memory.Status(status)
	entry.Admission.DataClass = memory.DataClass(dataClass)
	entry.ReviewerID = stringOrEmpty(reviewer)
	entry.Admission.SanitizationEvidenceRef = stringOrEmpty(sanitizationEvidence)
	entry.SupersedesEntryID = stringOrEmpty(supersedes)
	if reviewedAt != nil {
		value := reviewedAt.UTC()
		entry.ReviewedAt = &value
	}
	entry.CreatedAt = entry.CreatedAt.UTC()
	entry.UpdatedAt = entry.UpdatedAt.UTC()
	entry.Admission.AttestedAt = entry.Admission.AttestedAt.UTC()
	rows, err := q.Query(ctx, `SELECT reference,digest FROM organizational_memory_evidence_refs WHERE organization_id=$1 AND entry_key=$2 ORDER BY ordinal ASC`, organizationID, entryID)
	if err != nil {
		return memory.Entry{}, mapError("list memory evidence refs", err)
	}
	defer rows.Close()
	for rows.Next() {
		var ref memory.EvidenceRef
		if err := rows.Scan(&ref.Reference, &ref.Digest); err != nil {
			return memory.Entry{}, mapError("scan memory evidence ref", err)
		}
		entry.EvidenceRefs = append(entry.EvidenceRefs, ref)
	}
	if err := rows.Err(); err != nil {
		return memory.Entry{}, mapError("iterate memory evidence refs", err)
	}
	if err := entry.Validate(); err != nil {
		return memory.Entry{}, fmt.Errorf("memory/postgres: stored entry failed domain validation: %w", err)
	}
	return entry, nil
}

type idempotencyRecord struct{ entryKey, canonicalHash string }

func lookupIdempotency(ctx context.Context, tx pgx.Tx, organizationID, key string) (idempotencyRecord, bool, error) {
	var record idempotencyRecord
	err := tx.QueryRow(ctx, `SELECT entry_key,canonical_hash FROM organizational_memory_idempotency WHERE organization_id=$1 AND idempotency_key=$2 FOR SHARE`, organizationID, key).Scan(&record.entryKey, &record.canonicalHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return idempotencyRecord{}, false, nil
	}
	if err != nil {
		return idempotencyRecord{}, false, mapError("lookup memory idempotency", err)
	}
	return record, true, nil
}
func insertIdempotency(ctx context.Context, tx pgx.Tx, organizationID, key, entryKey, canonicalHash string) error {
	result, err := tx.Exec(ctx, `INSERT INTO organizational_memory_idempotency (organization_id,idempotency_key,entry_key,canonical_hash) VALUES ($1,$2,$3,$4) ON CONFLICT (organization_id,idempotency_key) DO NOTHING`, organizationID, key, entryKey, canonicalHash)
	if err != nil {
		return mapError("insert memory idempotency", err)
	}
	if result.RowsAffected() == 1 {
		return nil
	}
	var existingEntry, existingHash string
	if err := tx.QueryRow(ctx, `SELECT entry_key,canonical_hash FROM organizational_memory_idempotency WHERE organization_id=$1 AND idempotency_key=$2`, organizationID, key).Scan(&existingEntry, &existingHash); err != nil {
		return mapError("re-read memory idempotency", err)
	}
	if existingHash != canonicalHash || existingEntry != entryKey {
		return fmt.Errorf("%w: idempotency key already commits different memory entry", memory.ErrConflict)
	}
	return nil
}
func normalizeLimit(limit int) (int, error) {
	if limit == 0 {
		return 100, nil
	}
	if limit < 0 || limit > maxListLimit {
		return 0, fmt.Errorf("%w: list limit must be between 1 and %d", memory.ErrInvalidRequest, maxListLimit)
	}
	return limit, nil
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
func mapError(operation string, err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "40001", "40P01":
			return fmt.Errorf("%w: %s", memory.ErrRevisionConflict, operation)
		case "23503":
			return fmt.Errorf("%w: %s violates memory reference integrity", memory.ErrConflict, operation)
		case "23505":
			return fmt.Errorf("%w: %s conflicts with existing memory", memory.ErrConflict, operation)
		case "23514":
			return fmt.Errorf("%w: %s violates memory invariant", memory.ErrInvalidEntry, operation)
		}
	}
	return fmt.Errorf("memory/postgres: %s: %w", operation, err)
}
