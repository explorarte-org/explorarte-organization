package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/rag"
)

var _ rag.EmbeddingBackfillRepository = (*Store)(nil)

// PendingChunkEmbeddings finds chunks in namespaceKind/namespaceID's active
// generation that have no row yet in whichever table identity's shape
// selects (rag_chunk_embeddings for Gemini, rag_chunk_embeddings_bge_m3 for
// BGE-M3) — the read side of a resumable backfill: a caller processes one
// page, inserts the resulting embeddings (idempotent — see
// InsertChunkEmbedding/InsertBGEM3ChunkEmbedding's ON CONFLICT DO NOTHING),
// and calls again; a chunk that already has a matching row simply never
// comes back, so a crash mid-run loses no progress and a re-run never
// double-embeds. Only the active generation is considered — a superseded
// or still-building generation's chunks are never reachable by Query()
// either (see store.go), so embedding them would be wasted spend.
func (s *Store) PendingChunkEmbeddings(ctx context.Context, organizationID string, namespaceKind rag.NamespaceKind, namespaceID string, identity rag.EmbeddingIdentity, limit int) ([]rag.Chunk, error) {
	if organizationID != s.organizationID || !namespaceKind.Valid() || strings.TrimSpace(namespaceID) == "" {
		return nil, fmt.Errorf("%w: invalid pending-chunk-embeddings request", rag.ErrInvalidRequest)
	}
	if err := identity.Validate(); err != nil {
		return nil, fmt.Errorf("pending chunk embeddings requires a valid embedding identity: %w", err)
	}
	limit, err := normalizeLimit(limit)
	if err != nil {
		return nil, err
	}

	var table, existsClause string
	var args []any
	switch {
	case identity.ModelVersion != "":
		table = "rag_chunk_embeddings"
		existsClause = "e.embedding_model_id=$4 AND e.embedding_model_version=$5"
		args = []any{organizationID, string(namespaceKind), namespaceID, identity.ModelID, identity.ModelVersion, limit}
	case identity.ModelRevision != "":
		table = "rag_chunk_embeddings_bge_m3"
		existsClause = "e.model_revision=$4 AND e.artifact_sha256=$5"
		args = []any{organizationID, string(namespaceKind), namespaceID, identity.ModelRevision, identity.ArtifactSHA256, limit}
	default:
		return nil, fmt.Errorf("%w: embedding identity must be gemini-shaped or bge-m3-shaped", rag.ErrInvalidRequest)
	}

	rows, err := s.pool.Query(ctx, `
SELECT c.chunk_id, c.version_id, c.generation_id, c.chunker_id, c.chunker_version, c.ordinal, c.start_offset, c.end_offset, c.content, c.content_hash, c.media_source_ref, c.media_mime_type
FROM rag_knowledge_chunks c
JOIN rag_index_generations g ON g.organization_id=c.organization_id AND g.generation_id=c.generation_id
WHERE c.organization_id=$1 AND g.namespace_kind=$2 AND g.namespace_id=$3 AND g.status='active'
  AND NOT EXISTS (
    SELECT 1 FROM `+table+` e
    WHERE e.organization_id=c.organization_id AND e.chunk_id=c.chunk_id AND `+existsClause+`
  )
ORDER BY c.chunk_id
LIMIT $6`, args...)
	if err != nil {
		return nil, mapError("pending chunk embeddings", err)
	}
	defer rows.Close()
	chunks := make([]rag.Chunk, 0)
	for rows.Next() {
		var chunk rag.Chunk
		var mediaSourceRef, mediaMimeType *string
		if err := rows.Scan(&chunk.ID, &chunk.VersionID, &chunk.GenerationID, &chunk.ChunkerID, &chunk.ChunkerVersion, &chunk.Ordinal, &chunk.StartOffset, &chunk.EndOffset, &chunk.Content, &chunk.ContentHash, &mediaSourceRef, &mediaMimeType); err != nil {
			return nil, err
		}
		if mediaSourceRef != nil {
			chunk.MediaSourceRef = *mediaSourceRef
		}
		if mediaMimeType != nil {
			chunk.MediaMimeType = *mediaMimeType
		}
		chunks = append(chunks, chunk)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return chunks, nil
}
