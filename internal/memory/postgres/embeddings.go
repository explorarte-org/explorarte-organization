package postgres

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/memory"
)

var _ memory.EmbeddingRepository = (*Store)(nil)

const entryEmbeddingDimension = 768

// encodeVector mirrors internal/rag/postgres/embeddings.go's function of the
// same name exactly, byte for byte — internal/memory cannot import
// internal/rag (see scripts/check-memory-fitness.sh), and neither package
// registers a pgx-level vector codec (see that file's doc comment for why:
// registering pgvector's type OIDs is a per-connection hook on the pool
// shared by every store in this application, including ones with no reason
// to require the extension). Duplicated deliberately rather than shared.
func encodeVector(vector []float32) (string, error) {
	if len(vector) != entryEmbeddingDimension {
		return "", fmt.Errorf("%w: vector has %d dimensions, want %d", memory.ErrInvalidEntry, len(vector), entryEmbeddingDimension)
	}
	var builder strings.Builder
	builder.WriteByte('[')
	for index, value := range vector {
		if index > 0 {
			builder.WriteByte(',')
		}
		builder.WriteString(strconv.FormatFloat(float64(value), 'g', -1, 32))
	}
	builder.WriteByte(']')
	return builder.String(), nil
}

func (s *Store) InsertEntryEmbedding(ctx context.Context, embedding memory.EntryEmbedding) error {
	if embedding.OrganizationID != s.organizationID || strings.TrimSpace(embedding.EntryID) == "" ||
		strings.TrimSpace(embedding.EmbeddingModelID) == "" || strings.TrimSpace(embedding.EmbeddingModelVersion) == "" ||
		strings.TrimSpace(embedding.PromptTemplateVersion) == "" || strings.TrimSpace(embedding.InputHash) == "" {
		return fmt.Errorf("%w: invalid entry embedding", memory.ErrInvalidEntry)
	}
	if embedding.EmbeddingDimension != entryEmbeddingDimension {
		return fmt.Errorf("%w: embedding dimension %d, want %d", memory.ErrInvalidEntry, embedding.EmbeddingDimension, entryEmbeddingDimension)
	}
	encoded, err := encodeVector(embedding.Vector)
	if err != nil {
		return err
	}
	if embedding.CreatedAt.IsZero() {
		return fmt.Errorf("%w: created_at is required", memory.ErrInvalidEntry)
	}
	_, err = s.pool.Exec(ctx, `
INSERT INTO organizational_memory_embeddings (organization_id, entry_key, embedding_model_id, embedding_model_version, embedding_dimension, prompt_template_version, input_hash, embedding, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8::vector,$9)
ON CONFLICT (organization_id, entry_key, embedding_model_id, embedding_model_version) DO NOTHING`,
		embedding.OrganizationID, embedding.EntryID, embedding.EmbeddingModelID, embedding.EmbeddingModelVersion,
		embedding.EmbeddingDimension, embedding.PromptTemplateVersion, embedding.InputHash, encoded, embedding.CreatedAt.UTC())
	if err != nil {
		return mapError("insert entry embedding", err)
	}
	return nil
}

// NearestEntries runs an exact (non-ANN) nearest-neighbor search — same
// rationale as internal/rag/postgres's NearestChunks: no HNSW index yet,
// and production has ~0 memory entries as of R29.
func (s *Store) NearestEntries(ctx context.Context, organizationID, roleID string, queryVector []float32, limit int) ([]memory.ScoredEntry, error) {
	if organizationID != s.organizationID || strings.TrimSpace(roleID) == "" {
		return nil, fmt.Errorf("%w: invalid nearest-entries request", memory.ErrInvalidRequest)
	}
	limit, err := normalizeLimit(limit)
	if err != nil {
		return nil, err
	}
	encoded, err := encodeVector(queryVector)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
SELECT e.entry_key, e.embedding <=> $3::vector AS distance
FROM organizational_memory_embeddings e
JOIN organizational_memory_versions v ON v.organization_id=e.organization_id AND v.entry_key=e.entry_key
JOIN organizational_memory_entries m ON m.organization_id=e.organization_id AND m.entry_key=e.entry_key AND m.status='approved'
WHERE e.organization_id=$1 AND v.role_id=$2
ORDER BY distance ASC
LIMIT $4`, organizationID, roleID, encoded, limit)
	if err != nil {
		return nil, mapError("nearest entries", err)
	}
	defer rows.Close()
	results := make([]memory.ScoredEntry, 0)
	for rows.Next() {
		var scored memory.ScoredEntry
		if err := rows.Scan(&scored.EntryID, &scored.Distance); err != nil {
			return nil, err
		}
		results = append(results, scored)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}
