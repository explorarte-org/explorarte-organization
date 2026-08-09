package postgres

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/memory"
)

var _ memory.BGEM3EmbeddingRepository = (*Store)(nil)

const bgeM3EntryEmbeddingDimension = 1024

// encodeVectorBGEM3 mirrors internal/rag/postgres's function of the same
// name exactly — same deliberate duplication rationale as encodeVector
// above.
func encodeVectorBGEM3(vector []float32) (string, error) {
	if len(vector) != bgeM3EntryEmbeddingDimension {
		return "", fmt.Errorf("%w: vector has %d dimensions, want %d", memory.ErrInvalidEntry, len(vector), bgeM3EntryEmbeddingDimension)
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

func (s *Store) InsertBGEM3EntryEmbedding(ctx context.Context, embedding memory.BGEM3EntryEmbedding) error {
	if embedding.OrganizationID != s.organizationID || strings.TrimSpace(embedding.EntryID) == "" ||
		strings.TrimSpace(embedding.EmbeddingModelID) == "" || strings.TrimSpace(embedding.ModelRevision) == "" ||
		strings.TrimSpace(embedding.ArtifactSHA256) == "" || strings.TrimSpace(embedding.TokenizerRevision) == "" ||
		strings.TrimSpace(embedding.Normalization) == "" || strings.TrimSpace(embedding.Pooling) == "" ||
		strings.TrimSpace(embedding.PromptTemplateVersion) == "" || strings.TrimSpace(embedding.InputHash) == "" {
		return fmt.Errorf("%w: invalid bge-m3 entry embedding", memory.ErrInvalidEntry)
	}
	if embedding.EmbeddingDimension != bgeM3EntryEmbeddingDimension {
		return fmt.Errorf("%w: embedding dimension %d, want %d", memory.ErrInvalidEntry, embedding.EmbeddingDimension, bgeM3EntryEmbeddingDimension)
	}
	encoded, err := encodeVectorBGEM3(embedding.Vector)
	if err != nil {
		return err
	}
	if embedding.CreatedAt.IsZero() {
		return fmt.Errorf("%w: created_at is required", memory.ErrInvalidEntry)
	}
	_, err = s.pool.Exec(ctx, `
INSERT INTO organizational_memory_embeddings_bge_m3
    (organization_id, entry_key, embedding_model_id, model_revision, artifact_sha256, tokenizer_revision,
     embedding_dimension, normalization, pooling, prompt_template_version, input_hash, embedding, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12::vector,$13)
ON CONFLICT (organization_id, entry_key, model_revision, artifact_sha256) DO NOTHING`,
		embedding.OrganizationID, embedding.EntryID, embedding.EmbeddingModelID, embedding.ModelRevision, embedding.ArtifactSHA256,
		embedding.TokenizerRevision, embedding.EmbeddingDimension, embedding.Normalization, embedding.Pooling,
		embedding.PromptTemplateVersion, embedding.InputHash, encoded, embedding.CreatedAt.UTC())
	if err != nil {
		return mapError("insert bge-m3 entry embedding", err)
	}
	return nil
}

func (s *Store) NearestBGEM3Entries(ctx context.Context, organizationID, roleID string, queryVector []float32, limit int) ([]memory.ScoredEntry, error) {
	if organizationID != s.organizationID || strings.TrimSpace(roleID) == "" {
		return nil, fmt.Errorf("%w: invalid nearest-entries request", memory.ErrInvalidRequest)
	}
	limit, err := normalizeLimit(limit)
	if err != nil {
		return nil, err
	}
	encoded, err := encodeVectorBGEM3(queryVector)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
SELECT e.entry_key, e.embedding <=> $3::vector AS distance
FROM organizational_memory_embeddings_bge_m3 e
JOIN organizational_memory_versions v ON v.organization_id=e.organization_id AND v.entry_key=e.entry_key
JOIN organizational_memory_entries m ON m.organization_id=e.organization_id AND m.entry_key=e.entry_key AND m.status='approved'
WHERE e.organization_id=$1 AND v.role_id=$2
ORDER BY distance ASC
LIMIT $4`, organizationID, roleID, encoded, limit)
	if err != nil {
		return nil, mapError("nearest bge-m3 entries", err)
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
