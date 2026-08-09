package postgres

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/rag"
)

var _ rag.EmbeddingRepository = (*Store)(nil)

const chunkEmbeddingDimension = 768

// encodeVector renders a []float32 in pgvector's textual input format
// ("[v1,v2,...]"), which Postgres parses via an explicit ::vector cast.
// This package intentionally has no pgx-level vector codec (no
// github.com/pgvector/pgvector-go dependency): registering pgvector's type
// OIDs is a per-connection AfterConnect hook, and the only shared place to
// install one is internal/platform/postgres.Open — used by every store in
// this application, including ones with no reason to require the pgvector
// extension to be installed at all. Text-cast avoids that blast radius
// entirely, at the cost of formatting the literal by hand here.
func encodeVector(vector []float32) (string, error) {
	if len(vector) != chunkEmbeddingDimension {
		return "", fmt.Errorf("%w: vector has %d dimensions, want %d", rag.ErrInvalidChunk, len(vector), chunkEmbeddingDimension)
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

func (s *Store) InsertChunkEmbedding(ctx context.Context, embedding rag.ChunkEmbedding) error {
	if embedding.OrganizationID != s.organizationID || strings.TrimSpace(embedding.ChunkID) == "" ||
		strings.TrimSpace(embedding.EmbeddingModelID) == "" || strings.TrimSpace(embedding.EmbeddingModelVersion) == "" ||
		strings.TrimSpace(embedding.PromptTemplateVersion) == "" || strings.TrimSpace(embedding.InputHash) == "" {
		return fmt.Errorf("%w: invalid chunk embedding", rag.ErrInvalidChunk)
	}
	if embedding.EmbeddingDimension != chunkEmbeddingDimension {
		return fmt.Errorf("%w: embedding dimension %d, want %d", rag.ErrInvalidChunk, embedding.EmbeddingDimension, chunkEmbeddingDimension)
	}
	encoded, err := encodeVector(embedding.Vector)
	if err != nil {
		return err
	}
	if embedding.CreatedAt.IsZero() {
		return fmt.Errorf("%w: created_at is required", rag.ErrInvalidChunk)
	}
	_, err = s.pool.Exec(ctx, `
INSERT INTO rag_chunk_embeddings (organization_id, chunk_id, embedding_model_id, embedding_model_version, embedding_dimension, prompt_template_version, input_hash, embedding, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8::vector,$9)
ON CONFLICT (organization_id, chunk_id, embedding_model_id, embedding_model_version) DO NOTHING`,
		embedding.OrganizationID, embedding.ChunkID, embedding.EmbeddingModelID, embedding.EmbeddingModelVersion,
		embedding.EmbeddingDimension, embedding.PromptTemplateVersion, embedding.InputHash, encoded, embedding.CreatedAt.UTC())
	if err != nil {
		return mapError("insert chunk embedding", err)
	}
	return nil
}

// NearestChunks runs an exact (non-ANN) nearest-neighbor search: no HNSW
// index exists yet (see migration 000028) — production has 0 chunks as of
// R29, so an approximate index is premature and would filter by
// generation/organization *after* traversing the index, which is worse
// than an exact scan once that scoping is applied inside the query itself,
// as it is here.
func (s *Store) NearestChunks(ctx context.Context, organizationID, generationID string, queryVector []float32, limit int) ([]rag.ScoredChunk, error) {
	if organizationID != s.organizationID || strings.TrimSpace(generationID) == "" {
		return nil, fmt.Errorf("%w: invalid nearest-chunks request", rag.ErrInvalidRequest)
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
SELECT c.chunk_id, e.embedding <=> $3::vector AS distance
FROM rag_chunk_embeddings e
JOIN rag_knowledge_chunks c ON c.organization_id=e.organization_id AND c.chunk_id=e.chunk_id
WHERE e.organization_id=$1 AND c.generation_id=$2
ORDER BY distance ASC
LIMIT $4`, organizationID, generationID, encoded, limit)
	if err != nil {
		return nil, mapError("nearest chunks", err)
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

func (s *Store) CreateEmbeddingBatchJob(ctx context.Context, job rag.EmbeddingBatchJob, items []rag.EmbeddingBatchJobItem) (rag.EmbeddingBatchJob, error) {
	if job.OrganizationID != s.organizationID || strings.TrimSpace(job.GenerationID) == "" ||
		strings.TrimSpace(job.ProviderID) == "" || strings.TrimSpace(job.ProviderModelID) == "" || len(items) == 0 {
		return rag.EmbeddingBatchJob{}, fmt.Errorf("%w: invalid embedding batch job", rag.ErrInvalidRequest)
	}
	if job.ItemCount != len(items) {
		return rag.EmbeddingBatchJob{}, fmt.Errorf("%w: item_count %d does not match %d supplied items", rag.ErrInvalidRequest, job.ItemCount, len(items))
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return rag.EmbeddingBatchJob{}, mapError("begin embedding batch job transaction", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	now := job.CreatedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	var providerJobName *string
	if strings.TrimSpace(job.ProviderJobName) != "" {
		providerJobName = &job.ProviderJobName
	}
	// A job row is only ever created after CreateBatch already succeeded
	// against the provider (see internal/embeddingruntime.BatchAdapter) —
	// this store never models a "not yet submitted" state, so submitted_at
	// is always set to the same timestamp as created_at here, satisfying
	// the schema's CHECK that completed_at can only be set once
	// submitted_at is.
	row := tx.QueryRow(ctx, `
INSERT INTO rag_embedding_batch_jobs (organization_id, namespace_kind, namespace_id, generation_id, provider_id, provider_model_id, provider_job_name, status, shard_index, item_count, submitted_at, created_at, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$11,$11)
RETURNING id`,
		job.OrganizationID, string(job.NamespaceKind), job.NamespaceID, job.GenerationID, job.ProviderID, job.ProviderModelID,
		providerJobName, job.Status, job.ShardIndex, job.ItemCount, now)
	if err := row.Scan(&job.ID); err != nil {
		return rag.EmbeddingBatchJob{}, mapError("insert embedding batch job", err)
	}
	job.CreatedAt, job.UpdatedAt = now, now
	job.SubmittedAt = &now

	for _, item := range items {
		if strings.TrimSpace(item.ItemKey) == "" || strings.TrimSpace(item.ChunkID) == "" {
			return rag.EmbeddingBatchJob{}, fmt.Errorf("%w: embedding batch job item requires item_key and chunk_id", rag.ErrInvalidRequest)
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO rag_embedding_batch_job_items (job_id, item_key, organization_id, chunk_id, status)
VALUES ($1,$2,$3,$4,'pending')`, job.ID, item.ItemKey, job.OrganizationID, item.ChunkID); err != nil {
			return rag.EmbeddingBatchJob{}, mapError("insert embedding batch job item", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return rag.EmbeddingBatchJob{}, mapError("commit embedding batch job", err)
	}
	return job, nil
}

// RecordEmbeddingBatchJobItemResult records one item's outcome and, on
// success, inserts the resulting ChunkEmbedding — both in the same
// transaction, so an item is never marked succeeded without its embedding
// actually being persisted, or vice versa.
func (s *Store) RecordEmbeddingBatchJobItemResult(ctx context.Context, jobID int64, itemKey string, embedding *rag.ChunkEmbedding, errorMessage string) error {
	if jobID <= 0 || strings.TrimSpace(itemKey) == "" {
		return fmt.Errorf("%w: invalid embedding batch job item result", rag.ErrInvalidRequest)
	}
	if (embedding == nil) == (strings.TrimSpace(errorMessage) == "") {
		return fmt.Errorf("%w: exactly one of embedding or errorMessage must be set", rag.ErrInvalidRequest)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return mapError("begin embedding batch job item transaction", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if embedding != nil {
		encoded, err := encodeVector(embedding.Vector)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO rag_chunk_embeddings (organization_id, chunk_id, embedding_model_id, embedding_model_version, embedding_dimension, prompt_template_version, input_hash, embedding, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8::vector,$9)
ON CONFLICT (organization_id, chunk_id, embedding_model_id, embedding_model_version) DO NOTHING`,
			embedding.OrganizationID, embedding.ChunkID, embedding.EmbeddingModelID, embedding.EmbeddingModelVersion,
			embedding.EmbeddingDimension, embedding.PromptTemplateVersion, embedding.InputHash, encoded, embedding.CreatedAt.UTC()); err != nil {
			return mapError("insert chunk embedding for batch item", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE rag_embedding_batch_job_items SET status='succeeded', error_message=NULL WHERE job_id=$1 AND item_key=$2`, jobID, itemKey); err != nil {
			return mapError("mark embedding batch job item succeeded", err)
		}
	} else {
		if _, err := tx.Exec(ctx, `UPDATE rag_embedding_batch_job_items SET status='failed', error_message=$3 WHERE job_id=$1 AND item_key=$2`, jobID, itemKey, errorMessage); err != nil {
			return mapError("mark embedding batch job item failed", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return mapError("commit embedding batch job item result", err)
	}
	return nil
}

func (s *Store) CompleteEmbeddingBatchJob(ctx context.Context, jobID int64, status string, completedAt time.Time, failedItemCount int) error {
	if jobID <= 0 || completedAt.IsZero() || failedItemCount < 0 {
		return fmt.Errorf("%w: invalid embedding batch job completion", rag.ErrInvalidRequest)
	}
	tag, err := s.pool.Exec(ctx, `
UPDATE rag_embedding_batch_jobs SET status=$2, completed_at=$3, failed_item_count=$4, updated_at=$3
WHERE id=$1 AND organization_id=$5`, jobID, status, completedAt.UTC(), failedItemCount, s.organizationID)
	if err != nil {
		return mapError("complete embedding batch job", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: embedding batch job %d not found", rag.ErrInvalidRequest, jobID)
	}
	return nil
}
