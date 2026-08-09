package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/memory"
)

var _ memory.EmbeddingBackfillRepository = (*Store)(nil)

// PendingEntryEmbeddings finds approved entries for roleID that have no row
// yet in whichever table identity's shape selects
// (organizational_memory_embeddings for Gemini,
// organizational_memory_embeddings_bge_m3 for BGE-M3) — mirrors
// internal/rag/postgres's PendingChunkEmbeddings exactly, same resumability
// argument: a caller processes one page, inserts the resulting embeddings
// (idempotent — see InsertEntryEmbedding/InsertBGEM3EntryEmbedding's ON
// CONFLICT DO NOTHING), and calls again; an entry that already has a
// matching row simply never comes back.
func (s *Store) PendingEntryEmbeddings(ctx context.Context, organizationID, roleID string, identity memory.EmbeddingIdentity, limit int) ([]string, error) {
	organizationID = strings.TrimSpace(organizationID)
	roleID = strings.TrimSpace(roleID)
	if organizationID != s.organizationID || roleID == "" {
		return nil, memory.ErrInvalidRequest
	}
	if err := identity.Validate(); err != nil {
		return nil, fmt.Errorf("pending entry embeddings requires a valid embedding identity: %w", err)
	}
	limit, err := normalizeLimit(limit)
	if err != nil {
		return nil, err
	}

	var table, existsClause string
	var args []any
	switch {
	case identity.ModelVersion != "":
		table = "organizational_memory_embeddings"
		existsClause = "e.embedding_model_id=$3 AND e.embedding_model_version=$4"
		args = []any{organizationID, roleID, identity.ModelID, identity.ModelVersion, limit}
	case identity.ModelRevision != "":
		table = "organizational_memory_embeddings_bge_m3"
		existsClause = "e.model_revision=$3 AND e.artifact_sha256=$4"
		args = []any{organizationID, roleID, identity.ModelRevision, identity.ArtifactSHA256, limit}
	default:
		return nil, fmt.Errorf("%w: embedding identity must be gemini-shaped or bge-m3-shaped", memory.ErrInvalidRequest)
	}

	rows, err := s.pool.Query(ctx, `
SELECT v.entry_key
FROM organizational_memory_versions v
JOIN organizational_memory_entries m ON m.organization_id=v.organization_id AND m.entry_key=v.entry_key
WHERE v.organization_id=$1 AND v.role_id=$2 AND m.status='approved'
  AND NOT EXISTS (
    SELECT 1 FROM `+table+` e
    WHERE e.organization_id=v.organization_id AND e.entry_key=v.entry_key AND `+existsClause+`
  )
ORDER BY v.entry_key
LIMIT $5`, args...)
	if err != nil {
		return nil, mapError("pending entry embeddings", err)
	}
	defer rows.Close()
	entryKeys := make([]string, 0)
	for rows.Next() {
		var entryKey string
		if err := rows.Scan(&entryKey); err != nil {
			return nil, err
		}
		entryKeys = append(entryKeys, entryKey)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return entryKeys, nil
}
