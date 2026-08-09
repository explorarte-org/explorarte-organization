package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	platformpostgres "github.com/Mireuz13/explorarte-organization/internal/platform/postgres"
	"github.com/Mireuz13/explorarte-organization/internal/rag"
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
		return nil, errors.New("rag store requires initialized PostgreSQL")
	}
	organizationID = strings.TrimSpace(organizationID)
	if organizationID == "" {
		return nil, errors.New("rag store requires organization ID")
	}
	return &Store{pool: store.Pool(), organizationID: organizationID}, nil
}

var _ rag.Repository = (*Store)(nil)

func (s *Store) CreateCandidate(ctx context.Context, command rag.CreateCandidateCommand) (rag.KnowledgeVersion, bool, error) {
	version := command.Version
	if err := version.Validate(); err != nil {
		return rag.KnowledgeVersion{}, false, err
	}
	if version.OrganizationID != s.organizationID {
		return rag.KnowledgeVersion{}, false, fmt.Errorf("%w: rag store organization mismatch", rag.ErrInvalidVersion)
	}
	if version.Lifecycle != rag.LifecycleCandidate || version.Revision != 1 {
		return rag.KnowledgeVersion{}, false, fmt.Errorf("%w: persisted knowledge must start as an unreviewed candidate revision 1", rag.ErrInvalidVersion)
	}
	key := strings.TrimSpace(command.IdempotencyKey)
	if key == "" {
		return rag.KnowledgeVersion{}, false, fmt.Errorf("%w: idempotency_key is required", rag.ErrInvalidVersion)
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return rag.KnowledgeVersion{}, false, fmt.Errorf("rag/postgres: begin create candidate: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if existing, ok, err := lookupIdempotency(ctx, tx, s.organizationID, key); err != nil {
		return rag.KnowledgeVersion{}, false, err
	} else if ok {
		if existing.canonicalHash != version.CanonicalHash {
			return rag.KnowledgeVersion{}, false, fmt.Errorf("%w: idempotency key already commits different knowledge content", rag.ErrIdempotencyConflict)
		}
		value, err := getVersion(ctx, tx, s.organizationID, existing.versionID)
		if err != nil {
			return rag.KnowledgeVersion{}, false, err
		}
		if err := tx.Commit(ctx); err != nil {
			return rag.KnowledgeVersion{}, false, mapError("commit idempotent candidate", err)
		}
		return value, true, nil
	}

	if _, err := tx.Exec(ctx, `INSERT INTO rag_knowledge_documents (organization_id,document_id,namespace_kind,namespace_id,created_by_role_id,created_at) VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT (organization_id,document_id) DO NOTHING`,
		version.OrganizationID, version.DocumentID, string(version.NamespaceKind), version.NamespaceID, version.ProposedBy, version.CreatedAt); err != nil {
		return rag.KnowledgeVersion{}, false, mapError("insert knowledge document identity", err)
	}

	inserted, err := insertVersion(ctx, tx, version)
	if err != nil {
		return rag.KnowledgeVersion{}, false, err
	}
	if !inserted {
		var existingVersionID string
		if err := tx.QueryRow(ctx, `SELECT version_id FROM rag_knowledge_versions WHERE organization_id=$1 AND canonical_hash=$2`, s.organizationID, version.CanonicalHash).Scan(&existingVersionID); err != nil {
			return rag.KnowledgeVersion{}, false, mapError("resolve exact knowledge duplicate", err)
		}
		if err := insertIdempotency(ctx, tx, s.organizationID, key, existingVersionID, version.CanonicalHash); err != nil {
			return rag.KnowledgeVersion{}, false, err
		}
		value, err := getVersion(ctx, tx, s.organizationID, existingVersionID)
		if err != nil {
			return rag.KnowledgeVersion{}, false, err
		}
		if err := tx.Commit(ctx); err != nil {
			return rag.KnowledgeVersion{}, false, mapError("commit duplicate candidate", err)
		}
		return value, true, nil
	}

	for i, ref := range version.EvidenceRefs {
		if _, err := tx.Exec(ctx, `INSERT INTO rag_knowledge_evidence_refs (organization_id,version_id,ordinal,reference,digest) VALUES ($1,$2,$3,$4,$5)`,
			s.organizationID, version.ID, i+1, ref.Reference, ref.Digest); err != nil {
			return rag.KnowledgeVersion{}, false, mapError("insert knowledge evidence reference", err)
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO rag_knowledge_lifecycle_events (organization_id,document_id,version_id,from_lifecycle,to_lifecycle,actor_role_id,reason,revision,created_at) VALUES ($1,$2,$3,NULL,'candidate',$4,'candidate_proposed',1,$5)`,
		s.organizationID, version.DocumentID, version.ID, version.ProposedBy, version.CreatedAt); err != nil {
		return rag.KnowledgeVersion{}, false, mapError("insert knowledge creation event", err)
	}
	if err := insertIdempotency(ctx, tx, s.organizationID, key, version.ID, version.CanonicalHash); err != nil {
		return rag.KnowledgeVersion{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return rag.KnowledgeVersion{}, false, mapError("commit knowledge candidate", err)
	}
	return version, false, nil
}

func insertVersion(ctx context.Context, tx pgx.Tx, v rag.KnowledgeVersion) (bool, error) {
	result, err := tx.Exec(ctx, `INSERT INTO rag_knowledge_versions (
 organization_id,version_id,document_id,namespace_kind,namespace_id,version,title,body,source_kind,source_reference,source_run_ref,
 proposed_by_role_id,data_class,admission_attested_by,source_boundary,admission_evidence_ref,sanitization_evidence_ref,admission_attested_at,
 content_hash,canonical_hash,supersedes_version_id,lifecycle,reviewer_role_id,reviewed_at,revision,created_at,updated_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27)
ON CONFLICT (organization_id,canonical_hash) DO NOTHING`,
		v.OrganizationID, v.ID, v.DocumentID, string(v.NamespaceKind), v.NamespaceID, v.Version, v.Title, v.Body, string(v.SourceKind), v.SourceReference, nullableString(v.SourceRunRef),
		v.ProposedBy, string(v.Admission.DataClass), v.Admission.AttestedBy, v.Admission.SourceBoundary, v.Admission.EvidenceRef, nullableString(v.Admission.SanitizationEvidenceRef), v.Admission.AttestedAt,
		v.ContentHash, v.CanonicalHash, nullableString(v.SupersedesVersionID), string(v.Lifecycle), nil, nil, v.Revision, v.CreatedAt, v.UpdatedAt)
	if err != nil {
		return false, mapError("insert knowledge version content", err)
	}
	return result.RowsAffected() == 1, nil
}

func (s *Store) Get(ctx context.Context, organizationID, versionID string) (rag.KnowledgeVersion, error) {
	organizationID, versionID = strings.TrimSpace(organizationID), strings.TrimSpace(versionID)
	if organizationID == "" || versionID == "" {
		return rag.KnowledgeVersion{}, fmt.Errorf("%w: organization_id and version_id are required", rag.ErrInvalidRequest)
	}
	if organizationID != s.organizationID {
		return rag.KnowledgeVersion{}, fmt.Errorf("%w: rag store organization mismatch", rag.ErrNotFound)
	}
	return getVersion(ctx, s.pool, organizationID, versionID)
}

func (s *Store) Save(ctx context.Context, command rag.SaveCommand) (rag.KnowledgeVersion, error) {
	version := command.Version
	if err := version.Validate(); err != nil {
		return rag.KnowledgeVersion{}, err
	}
	if version.OrganizationID != s.organizationID {
		return rag.KnowledgeVersion{}, fmt.Errorf("%w: rag store organization mismatch", rag.ErrInvalidVersion)
	}
	if command.ExpectedRevision <= 0 || version.Revision != command.ExpectedRevision+1 {
		return rag.KnowledgeVersion{}, fmt.Errorf("%w: expected revision %d does not precede version revision %d", rag.ErrRevisionConflict, command.ExpectedRevision, version.Revision)
	}
	actor := strings.TrimSpace(command.ActorID)
	reason := strings.TrimSpace(command.Reason)
	if actor == "" || reason == "" {
		return rag.KnowledgeVersion{}, fmt.Errorf("%w: actor and reason are required", rag.ErrInvalidRequest)
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return rag.KnowledgeVersion{}, fmt.Errorf("rag/postgres: begin save: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var currentLifecycle string
	var currentRevision int64
	var canonicalHash string
	if err := tx.QueryRow(ctx, `SELECT lifecycle,revision,canonical_hash FROM rag_knowledge_versions WHERE organization_id=$1 AND version_id=$2 FOR UPDATE`, s.organizationID, version.ID).Scan(&currentLifecycle, &currentRevision, &canonicalHash); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return rag.KnowledgeVersion{}, fmt.Errorf("%w: %s", rag.ErrNotFound, version.ID)
		}
		return rag.KnowledgeVersion{}, mapError("lock knowledge version", err)
	}
	if currentRevision != command.ExpectedRevision {
		return rag.KnowledgeVersion{}, fmt.Errorf("%w: version %s expected revision %d current %d", rag.ErrRevisionConflict, version.ID, command.ExpectedRevision, currentRevision)
	}
	if err := rag.ValidateTransition(rag.Lifecycle(currentLifecycle), version.Lifecycle); err != nil {
		return rag.KnowledgeVersion{}, err
	}
	if version.CanonicalHash != canonicalHash {
		return rag.KnowledgeVersion{}, fmt.Errorf("%w: lifecycle mutation changed immutable knowledge content", rag.ErrSourceDrift)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO rag_knowledge_lifecycle_events (organization_id,document_id,version_id,from_lifecycle,to_lifecycle,actor_role_id,reason,revision,created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		s.organizationID, version.DocumentID, version.ID, currentLifecycle, string(version.Lifecycle), actor, reason, version.Revision, version.UpdatedAt); err != nil {
		return rag.KnowledgeVersion{}, mapError("insert knowledge lifecycle event", err)
	}
	result, err := tx.Exec(ctx, `UPDATE rag_knowledge_versions SET lifecycle=$3,reviewer_role_id=$4,reviewed_at=$5,revision=$6,updated_at=$7 WHERE organization_id=$1 AND version_id=$2 AND revision=$8`,
		s.organizationID, version.ID, string(version.Lifecycle), nullableString(version.ReviewerID), version.ReviewedAt, version.Revision, version.UpdatedAt, command.ExpectedRevision)
	if err != nil {
		return rag.KnowledgeVersion{}, mapError("update knowledge lifecycle", err)
	}
	if result.RowsAffected() != 1 {
		return rag.KnowledgeVersion{}, fmt.Errorf("%w: version %s", rag.ErrRevisionConflict, version.ID)
	}
	if err := tx.Commit(ctx); err != nil {
		return rag.KnowledgeVersion{}, mapError("commit knowledge lifecycle mutation", err)
	}
	return version, nil
}

func (s *Store) List(ctx context.Context, filter rag.ListFilter) ([]rag.KnowledgeVersion, error) {
	organizationID := strings.TrimSpace(filter.OrganizationID)
	if organizationID == "" || organizationID != s.organizationID {
		return nil, fmt.Errorf("%w: invalid organization filter", rag.ErrInvalidRequest)
	}
	limit, err := normalizeLimit(filter.Limit)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT version_id FROM rag_knowledge_versions WHERE organization_id=$1 AND ($2='' OR namespace_kind=$2) AND ($3='' OR namespace_id=$3) AND ($4='' OR lifecycle=$4) ORDER BY updated_at DESC, version_id ASC LIMIT $5`,
		organizationID, string(filter.NamespaceKind), filter.NamespaceID, string(filter.Lifecycle), limit)
	if err != nil {
		return nil, mapError("list knowledge versions", err)
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, mapError("scan knowledge version id", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, mapError("iterate knowledge versions", err)
	}
	result := make([]rag.KnowledgeVersion, 0, len(ids))
	for _, id := range ids {
		version, err := getVersion(ctx, s.pool, organizationID, id)
		if err != nil {
			return nil, err
		}
		result = append(result, version)
	}
	return result, nil
}

// ApprovedForNamespace returns every approved document in the namespace,
// paginating internally with a keyset cursor rather than the single
// maxListLimit-bounded query List uses. Reindex needs the complete set to
// build a generation — silently truncating at maxListLimit (as this used to
// do, delegating straight to List) would activate an index that's missing
// every document past the cap, with nothing surfacing that loss.
func (s *Store) ApprovedForNamespace(ctx context.Context, organizationID string, namespaceKind rag.NamespaceKind, namespaceID string) ([]rag.KnowledgeVersion, error) {
	organizationID = strings.TrimSpace(organizationID)
	if organizationID == "" || organizationID != s.organizationID {
		return nil, fmt.Errorf("%w: invalid organization filter", rag.ErrInvalidRequest)
	}
	type cursorRow struct {
		id        string
		updatedAt time.Time
	}
	var (
		result     []rag.KnowledgeVersion
		haveCursor bool
		cursorTime time.Time
		cursorID   string
	)
	for {
		var rows pgx.Rows
		var err error
		if !haveCursor {
			rows, err = s.pool.Query(ctx, `
SELECT version_id, updated_at FROM rag_knowledge_versions
WHERE organization_id=$1 AND namespace_kind=$2 AND namespace_id=$3 AND lifecycle=$4
ORDER BY updated_at DESC, version_id ASC LIMIT $5`,
				organizationID, string(namespaceKind), namespaceID, string(rag.LifecycleApproved), maxListLimit)
		} else {
			rows, err = s.pool.Query(ctx, `
SELECT version_id, updated_at FROM rag_knowledge_versions
WHERE organization_id=$1 AND namespace_kind=$2 AND namespace_id=$3 AND lifecycle=$4
  AND (updated_at < $5 OR (updated_at = $5 AND version_id > $6))
ORDER BY updated_at DESC, version_id ASC LIMIT $7`,
				organizationID, string(namespaceKind), namespaceID, string(rag.LifecycleApproved), cursorTime, cursorID, maxListLimit)
		}
		if err != nil {
			return nil, mapError("list approved knowledge versions page", err)
		}
		page := make([]cursorRow, 0, maxListLimit)
		for rows.Next() {
			var row cursorRow
			if err := rows.Scan(&row.id, &row.updatedAt); err != nil {
				rows.Close()
				return nil, mapError("scan approved knowledge version", err)
			}
			page = append(page, row)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, mapError("iterate approved knowledge versions", err)
		}
		rows.Close()
		for _, row := range page {
			version, err := getVersion(ctx, s.pool, organizationID, row.id)
			if err != nil {
				return nil, err
			}
			result = append(result, version)
		}
		if len(page) < maxListLimit {
			return result, nil
		}
		last := page[len(page)-1]
		haveCursor = true
		cursorTime = last.updatedAt
		cursorID = last.id
	}
}

func (s *Store) Reindex(ctx context.Context, command rag.ReindexCommand) (rag.IndexGeneration, error) {
	organizationID := strings.TrimSpace(command.OrganizationID)
	namespaceID := strings.TrimSpace(command.NamespaceID)
	if organizationID != s.organizationID || namespaceID == "" || !command.NamespaceKind.Valid() {
		return rag.IndexGeneration{}, fmt.Errorf("%w: invalid reindex scope", rag.ErrInvalidGeneration)
	}
	chunkerID, chunkerVersion := strings.TrimSpace(command.ChunkerID), strings.TrimSpace(command.ChunkerVersion)
	if chunkerID == "" || chunkerVersion == "" {
		return rag.IndexGeneration{}, fmt.Errorf("%w: chunker id and version are required", rag.ErrInvalidGeneration)
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return rag.IndexGeneration{}, fmt.Errorf("rag/postgres: begin reindex: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var nextGeneration int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(generation),0)+1 FROM rag_index_generations WHERE organization_id=$1 AND namespace_kind=$2 AND namespace_id=$3`,
		organizationID, string(command.NamespaceKind), namespaceID).Scan(&nextGeneration); err != nil {
		return rag.IndexGeneration{}, mapError("reserve rag index generation", err)
	}
	generationID := fmt.Sprintf("%s-%s-%d", string(command.NamespaceKind), namespaceID, nextGeneration)
	now := time.Now().UTC()
	if _, err := tx.Exec(ctx, `INSERT INTO rag_index_generations (organization_id,generation_id,namespace_kind,namespace_id,generation,status,chunker_id,chunker_version,created_at) VALUES ($1,$2,$3,$4,$5,'building',$6,$7,$8)`,
		organizationID, generationID, string(command.NamespaceKind), namespaceID, nextGeneration, chunkerID, chunkerVersion, now); err != nil {
		return rag.IndexGeneration{}, mapError("insert rag index generation", err)
	}
	// Chunks are caller-supplied (Manager.Reindex derives them fresh from
	// version.Body immediately before calling this, but Store.Reindex is a
	// Repository method — nothing at this layer independently confirmed a
	// chunk's content actually matches its claimed hash, or that its
	// version_id belongs to an approved version in *this* namespace rather
	// than some other one. The existing rag_chunk_insert_guard trigger only
	// checks the version's lifecycle is 'approved', not that it belongs to
	// this generation's namespace at all.
	approvedVersionIDs := make(map[string]bool)
	if len(command.Chunks) > 0 {
		versionIDSet := make(map[string]bool, len(command.Chunks))
		versionIDs := make([]string, 0, len(command.Chunks))
		for _, chunk := range command.Chunks {
			if versionIDSet[chunk.VersionID] {
				continue
			}
			versionIDSet[chunk.VersionID] = true
			versionIDs = append(versionIDs, chunk.VersionID)
		}
		rows, err := tx.Query(ctx, `SELECT version_id FROM rag_knowledge_versions WHERE organization_id=$1 AND namespace_kind=$2 AND namespace_id=$3 AND lifecycle='approved' AND version_id = ANY($4)`,
			organizationID, string(command.NamespaceKind), namespaceID, versionIDs)
		if err != nil {
			return rag.IndexGeneration{}, mapError("verify chunk versions belong to namespace", err)
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return rag.IndexGeneration{}, mapError("scan approved chunk version", err)
			}
			approvedVersionIDs[id] = true
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return rag.IndexGeneration{}, mapError("iterate approved chunk versions", err)
		}
		rows.Close()
	}
	for _, chunk := range command.Chunks {
		if !approvedVersionIDs[chunk.VersionID] {
			return rag.IndexGeneration{}, fmt.Errorf("%w: chunk references a version that is not approved in this namespace: %s", rag.ErrInvalidRequest, chunk.VersionID)
		}
		if rag.ContentHash(chunk.Content) != chunk.ContentHash {
			return rag.IndexGeneration{}, fmt.Errorf("%w: chunk content does not match its claimed content hash", rag.ErrInvalidRequest)
		}
		if chunk.StartOffset < 0 || chunk.EndOffset <= chunk.StartOffset {
			return rag.IndexGeneration{}, fmt.Errorf("%w: chunk offsets are invalid", rag.ErrInvalidRequest)
		}
		chunkID := fmt.Sprintf("%s-%s-%d", generationID, chunk.VersionID, chunk.Ordinal)
		if _, err := tx.Exec(ctx, `INSERT INTO rag_knowledge_chunks (organization_id,chunk_id,generation_id,version_id,chunker_id,chunker_version,ordinal,start_offset,end_offset,content,content_hash,embedding_model_id,embedding_model_version,embedding_dimension) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
			organizationID, chunkID, generationID, chunk.VersionID, chunk.ChunkerID, chunk.ChunkerVersion, chunk.Ordinal, chunk.StartOffset, chunk.EndOffset, chunk.Content, chunk.ContentHash,
			nullableString(chunk.EmbeddingModelID), nullableString(chunk.EmbeddingModelVersion), nullableInt(chunk.EmbeddingDimension)); err != nil {
			return rag.IndexGeneration{}, mapError("insert rag knowledge chunk", err)
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE rag_index_generations SET status='superseded' WHERE organization_id=$1 AND namespace_kind=$2 AND namespace_id=$3 AND status='active'`,
		organizationID, string(command.NamespaceKind), namespaceID); err != nil {
		return rag.IndexGeneration{}, mapError("supersede prior rag index generation", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE rag_index_generations SET status='active',activated_at=$3 WHERE organization_id=$1 AND generation_id=$2 AND status='building'`, organizationID, generationID, now); err != nil {
		return rag.IndexGeneration{}, mapError("activate rag index generation", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return rag.IndexGeneration{}, mapError("commit rag reindex", err)
	}
	return rag.IndexGeneration{ID: generationID, OrganizationID: organizationID, NamespaceKind: command.NamespaceKind, NamespaceID: namespaceID, Generation: nextGeneration, Status: rag.GenerationActive, ChunkerID: chunkerID, ChunkerVersion: chunkerVersion, CreatedAt: now, ActivatedAt: &now}, nil
}

func (s *Store) Query(ctx context.Context, command rag.QueryCommand) ([]rag.QueryResult, error) {
	organizationID := strings.TrimSpace(command.OrganizationID)
	namespaceID := strings.TrimSpace(command.NamespaceID)
	queryText := strings.TrimSpace(command.QueryText)
	if organizationID != s.organizationID || namespaceID == "" || queryText == "" || !command.NamespaceKind.Valid() {
		return nil, fmt.Errorf("%w: invalid query", rag.ErrInvalidRequest)
	}
	limit, err := normalizeLimit(command.Limit)
	if err != nil {
		return nil, err
	}
	var activeGenerationID string
	err = s.pool.QueryRow(ctx, `SELECT generation_id FROM rag_index_generations WHERE organization_id=$1 AND namespace_kind=$2 AND namespace_id=$3 AND status='active'`,
		organizationID, string(command.NamespaceKind), namespaceID).Scan(&activeGenerationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return []rag.QueryResult{}, nil
	}
	if err != nil {
		return nil, mapError("resolve active rag index generation", err)
	}
	rows, err := s.runHybridQuery(ctx, organizationID, activeGenerationID, queryText, command.QueryVector, limit)
	if err != nil {
		return nil, mapError("query rag knowledge chunks", err)
	}
	defer rows.Close()
	results := []rag.QueryResult{}
	for rows.Next() {
		var result rag.QueryResult
		var dataClass string
		if err := rows.Scan(&result.Chunk.ID, &result.Chunk.VersionID, &result.Chunk.ChunkerID, &result.Chunk.ChunkerVersion, &result.Chunk.Ordinal, &result.Chunk.StartOffset, &result.Chunk.EndOffset, &result.Chunk.Content, &result.Chunk.ContentHash,
			&result.DocumentID, &result.Title, &result.SourceReference, &dataClass, &result.CanonicalHash, &result.Score); err != nil {
			return nil, mapError("scan rag query result", err)
		}
		result.Chunk.GenerationID = activeGenerationID
		result.NamespaceKind = command.NamespaceKind
		result.NamespaceID = namespaceID
		result.DataClass = rag.DataClass(dataClass)
		result.GenerationID = activeGenerationID
		refs, err := listEvidenceRefs(ctx, s.pool, organizationID, result.Chunk.VersionID)
		if err != nil {
			return nil, err
		}
		result.EvidenceRefs = refs
		results = append(results, result)
	}
	if err := rows.Err(); err != nil {
		return nil, mapError("iterate rag query results", err)
	}
	return results, nil
}

func (s *Store) ActiveGeneration(ctx context.Context, organizationID string, namespaceKind rag.NamespaceKind, namespaceID string) (rag.IndexGeneration, bool, error) {
	organizationID, namespaceID = strings.TrimSpace(organizationID), strings.TrimSpace(namespaceID)
	if organizationID != s.organizationID || namespaceID == "" || !namespaceKind.Valid() {
		return rag.IndexGeneration{}, false, fmt.Errorf("%w: invalid generation scope", rag.ErrInvalidGeneration)
	}
	var g rag.IndexGeneration
	var status string
	var activatedAt *time.Time
	err := s.pool.QueryRow(ctx, `SELECT generation_id,generation,status,chunker_id,chunker_version,created_at,activated_at FROM rag_index_generations WHERE organization_id=$1 AND namespace_kind=$2 AND namespace_id=$3 AND status='active'`,
		organizationID, string(namespaceKind), namespaceID).Scan(&g.ID, &g.Generation, &status, &g.ChunkerID, &g.ChunkerVersion, &g.CreatedAt, &activatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return rag.IndexGeneration{}, false, nil
	}
	if err != nil {
		return rag.IndexGeneration{}, false, mapError("get active rag index generation", err)
	}
	g.OrganizationID, g.NamespaceKind, g.NamespaceID, g.Status = organizationID, namespaceKind, namespaceID, rag.GenerationStatus(status)
	g.CreatedAt = g.CreatedAt.UTC()
	if activatedAt != nil {
		value := activatedAt.UTC()
		g.ActivatedAt = &value
	}
	return g, true, nil
}

// ExistingEvidenceReferences reports which references starting with
// referencePrefix are already attached to some knowledge version for the
// organization. Used for idempotency checks (e.g. "has this decision-graph
// run already been consolidated into knowledge") by callers that must never
// query rag_knowledge_evidence_refs directly.
func (s *Store) ExistingEvidenceReferences(ctx context.Context, organizationID, referencePrefix string) (map[string]bool, error) {
	organizationID = strings.TrimSpace(organizationID)
	if organizationID != s.organizationID {
		return nil, fmt.Errorf("%w: invalid evidence reference scope", rag.ErrInvalidGeneration)
	}
	rows, err := s.pool.Query(ctx, `SELECT DISTINCT reference FROM rag_knowledge_evidence_refs WHERE organization_id=$1 AND reference LIKE $2`,
		organizationID, escapeLikePrefix(referencePrefix)+"%")
	if err != nil {
		return nil, mapError("list existing rag evidence references", err)
	}
	defer rows.Close()
	existing := make(map[string]bool)
	for rows.Next() {
		var reference string
		if err := rows.Scan(&reference); err != nil {
			return nil, mapError("scan existing rag evidence reference", err)
		}
		existing[reference] = true
	}
	if err := rows.Err(); err != nil {
		return nil, mapError("iterate existing rag evidence references", err)
	}
	return existing, nil
}

func escapeLikePrefix(prefix string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return replacer.Replace(prefix)
}

type queryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func getVersion(ctx context.Context, q queryer, organizationID, versionID string) (rag.KnowledgeVersion, error) {
	var v rag.KnowledgeVersion
	var namespaceKind, sourceKind, dataClass, lifecycle string
	var sourceRunRef, sanitizationEvidence, supersedes, reviewer *string
	var reviewedAt *time.Time
	if err := q.QueryRow(ctx, `SELECT organization_id,version_id,document_id,namespace_kind,namespace_id,version,title,body,source_kind,source_reference,source_run_ref,
 proposed_by_role_id,data_class,admission_attested_by,source_boundary,admission_evidence_ref,sanitization_evidence_ref,admission_attested_at,
 content_hash,canonical_hash,supersedes_version_id,lifecycle,reviewer_role_id,reviewed_at,revision,created_at,updated_at
FROM rag_knowledge_versions WHERE organization_id=$1 AND version_id=$2`, organizationID, versionID).Scan(
		&v.OrganizationID, &v.ID, &v.DocumentID, &namespaceKind, &v.NamespaceID, &v.Version, &v.Title, &v.Body, &sourceKind, &v.SourceReference, &sourceRunRef,
		&v.ProposedBy, &dataClass, &v.Admission.AttestedBy, &v.Admission.SourceBoundary, &v.Admission.EvidenceRef, &sanitizationEvidence, &v.Admission.AttestedAt,
		&v.ContentHash, &v.CanonicalHash, &supersedes, &lifecycle, &reviewer, &reviewedAt, &v.Revision, &v.CreatedAt, &v.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return rag.KnowledgeVersion{}, fmt.Errorf("%w: %s", rag.ErrNotFound, versionID)
		}
		return rag.KnowledgeVersion{}, mapError("get knowledge version", err)
	}
	v.NamespaceKind = rag.NamespaceKind(namespaceKind)
	v.SourceKind = rag.SourceKind(sourceKind)
	v.Admission.DataClass = rag.DataClass(dataClass)
	v.Lifecycle = rag.Lifecycle(lifecycle)
	v.SourceRunRef = stringOrEmpty(sourceRunRef)
	v.Admission.SanitizationEvidenceRef = stringOrEmpty(sanitizationEvidence)
	v.SupersedesVersionID = stringOrEmpty(supersedes)
	v.ReviewerID = stringOrEmpty(reviewer)
	if reviewedAt != nil {
		value := reviewedAt.UTC()
		v.ReviewedAt = &value
	}
	v.CreatedAt = v.CreatedAt.UTC()
	v.UpdatedAt = v.UpdatedAt.UTC()
	v.Admission.AttestedAt = v.Admission.AttestedAt.UTC()
	refs, err := listEvidenceRefs(ctx, q, organizationID, versionID)
	if err != nil {
		return rag.KnowledgeVersion{}, err
	}
	v.EvidenceRefs = refs
	if err := v.Validate(); err != nil {
		return rag.KnowledgeVersion{}, fmt.Errorf("rag/postgres: stored version failed domain validation: %w", err)
	}
	return v, nil
}

func listEvidenceRefs(ctx context.Context, q queryer, organizationID, versionID string) ([]rag.EvidenceRef, error) {
	rows, err := q.Query(ctx, `SELECT reference,digest FROM rag_knowledge_evidence_refs WHERE organization_id=$1 AND version_id=$2 ORDER BY ordinal ASC`, organizationID, versionID)
	if err != nil {
		return nil, mapError("list knowledge evidence refs", err)
	}
	defer rows.Close()
	var refs []rag.EvidenceRef
	for rows.Next() {
		var ref rag.EvidenceRef
		if err := rows.Scan(&ref.Reference, &ref.Digest); err != nil {
			return nil, mapError("scan knowledge evidence ref", err)
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, mapError("iterate knowledge evidence refs", err)
	}
	return refs, nil
}

type idempotencyRecord struct{ versionID, canonicalHash string }

func lookupIdempotency(ctx context.Context, tx pgx.Tx, organizationID, key string) (idempotencyRecord, bool, error) {
	var record idempotencyRecord
	err := tx.QueryRow(ctx, `SELECT version_id,canonical_hash FROM rag_knowledge_idempotency WHERE organization_id=$1 AND idempotency_key=$2 FOR SHARE`, organizationID, key).Scan(&record.versionID, &record.canonicalHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return idempotencyRecord{}, false, nil
	}
	if err != nil {
		return idempotencyRecord{}, false, mapError("lookup knowledge idempotency", err)
	}
	return record, true, nil
}

func insertIdempotency(ctx context.Context, tx pgx.Tx, organizationID, key, versionID, canonicalHash string) error {
	result, err := tx.Exec(ctx, `INSERT INTO rag_knowledge_idempotency (organization_id,idempotency_key,version_id,canonical_hash) VALUES ($1,$2,$3,$4) ON CONFLICT (organization_id,idempotency_key) DO NOTHING`, organizationID, key, versionID, canonicalHash)
	if err != nil {
		return mapError("insert knowledge idempotency", err)
	}
	if result.RowsAffected() == 1 {
		return nil
	}
	var existingVersion, existingHash string
	if err := tx.QueryRow(ctx, `SELECT version_id,canonical_hash FROM rag_knowledge_idempotency WHERE organization_id=$1 AND idempotency_key=$2`, organizationID, key).Scan(&existingVersion, &existingHash); err != nil {
		return mapError("re-read knowledge idempotency", err)
	}
	if existingHash != canonicalHash || existingVersion != versionID {
		return fmt.Errorf("%w: idempotency key already commits different knowledge version", rag.ErrIdempotencyConflict)
	}
	return nil
}

func normalizeLimit(limit int) (int, error) {
	if limit == 0 {
		return 100, nil
	}
	if limit < 0 || limit > maxListLimit {
		return 0, fmt.Errorf("%w: list limit must be between 1 and %d", rag.ErrInvalidRequest, maxListLimit)
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

func nullableInt(value int) *int {
	if value == 0 {
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
			return fmt.Errorf("%w: %s", rag.ErrRevisionConflict, operation)
		case "23503":
			return fmt.Errorf("%w: %s violates rag reference integrity", rag.ErrConflict, operation)
		case "23505":
			return fmt.Errorf("%w: %s conflicts with existing rag state", rag.ErrConflict, operation)
		case "23514":
			return fmt.Errorf("%w: %s violates rag invariant", rag.ErrInvalidVersion, operation)
		}
	}
	return fmt.Errorf("rag/postgres: %s: %w", operation, err)
}
