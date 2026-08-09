package postgres

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/rag"
)

var _ rag.BGEM3EmbeddingRepository = (*Store)(nil)

const bgeM3EmbeddingDimension = 1024

// encodeVectorBGEM3 mirrors encodeVector exactly but checks against
// bgeM3EmbeddingDimension — kept as a separate function, not a
// parameterized one, so a future change to either family's dimension
// check can never accidentally affect the other by editing shared code.
func encodeVectorBGEM3(vector []float32) (string, error) {
	if len(vector) != bgeM3EmbeddingDimension {
		return "", fmt.Errorf("%w: vector has %d dimensions, want %d", rag.ErrInvalidChunk, len(vector), bgeM3EmbeddingDimension)
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

func (s *Store) InsertBGEM3ChunkEmbedding(ctx context.Context, embedding rag.BGEM3ChunkEmbedding) error {
	if embedding.OrganizationID != s.organizationID || strings.TrimSpace(embedding.ChunkID) == "" ||
		strings.TrimSpace(embedding.EmbeddingModelID) == "" || strings.TrimSpace(embedding.ModelRevision) == "" ||
		strings.TrimSpace(embedding.ArtifactSHA256) == "" || strings.TrimSpace(embedding.TokenizerRevision) == "" ||
		strings.TrimSpace(embedding.Normalization) == "" || strings.TrimSpace(embedding.Pooling) == "" ||
		strings.TrimSpace(embedding.PromptTemplateVersion) == "" || strings.TrimSpace(embedding.InputHash) == "" {
		return fmt.Errorf("%w: invalid bge-m3 chunk embedding", rag.ErrInvalidChunk)
	}
	if embedding.EmbeddingDimension != bgeM3EmbeddingDimension {
		return fmt.Errorf("%w: embedding dimension %d, want %d", rag.ErrInvalidChunk, embedding.EmbeddingDimension, bgeM3EmbeddingDimension)
	}
	encoded, err := encodeVectorBGEM3(embedding.Vector)
	if err != nil {
		return err
	}
	if embedding.CreatedAt.IsZero() {
		return fmt.Errorf("%w: created_at is required", rag.ErrInvalidChunk)
	}
	_, err = s.pool.Exec(ctx, `
INSERT INTO rag_chunk_embeddings_bge_m3
    (organization_id, chunk_id, embedding_model_id, model_revision, artifact_sha256, tokenizer_revision,
     embedding_dimension, normalization, pooling, prompt_template_version, input_hash, embedding, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12::vector,$13)
ON CONFLICT (organization_id, chunk_id, model_revision, artifact_sha256) DO NOTHING`,
		embedding.OrganizationID, embedding.ChunkID, embedding.EmbeddingModelID, embedding.ModelRevision, embedding.ArtifactSHA256,
		embedding.TokenizerRevision, embedding.EmbeddingDimension, embedding.Normalization, embedding.Pooling,
		embedding.PromptTemplateVersion, embedding.InputHash, encoded, embedding.CreatedAt.UTC())
	if err != nil {
		return mapError("insert bge-m3 chunk embedding", err)
	}
	return nil
}

// NearestBGEM3Chunks is exact/brute-force, same reasoning as NearestChunks
// (see embeddings.go): no ANN index until real volume justifies one.
func (s *Store) NearestBGEM3Chunks(ctx context.Context, organizationID, generationID string, queryVector []float32, limit int) ([]rag.ScoredChunk, error) {
	if organizationID != s.organizationID || strings.TrimSpace(generationID) == "" {
		return nil, fmt.Errorf("%w: invalid nearest-chunks request", rag.ErrInvalidRequest)
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
SELECT c.chunk_id, e.embedding <=> $3::vector AS distance
FROM rag_chunk_embeddings_bge_m3 e
JOIN rag_knowledge_chunks c ON c.organization_id=e.organization_id AND c.chunk_id=e.chunk_id
WHERE e.organization_id=$1 AND c.generation_id=$2
ORDER BY distance ASC
LIMIT $4`, organizationID, generationID, encoded, limit)
	if err != nil {
		return nil, mapError("nearest bge-m3 chunks", err)
	}
	defer rows.Close()
	results := make([]rag.ScoredChunk, 0)
	for rows.Next() {
		var scored rag.ScoredChunk
		if err := rows.Scan(&scored.ChunkID, &scored.Distance); err != nil {
			return nil, err
		}
		results = append(results, scored)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}
