package postgres

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/rag"
	"github.com/jackc/pgx/v5"
)

// vectorChannelClause selects, from the query vector's own dimension,
// which embeddings table and encoder the vector channel must use — 768 is
// Gemini's frozen reference index (rag_chunk_embeddings), 1024 is BGE-M3's
// local operational index (rag_chunk_embeddings_bge_m3). It also builds
// the identity WHERE clause every vector-channel query must carry: a
// chunk's embedding table primary key deliberately allows more than one
// row per chunk (re-embedding under a new model revision is a new row,
// never an UPDATE — see migrations 000028/000032), so without this filter
// a chunk re-embedded under a second revision would contribute two rows
// to vector_matches — two ranks, summed together by the fused CTE's
// GROUP BY, i.e. two RRF votes for one chunk, and potentially comparing
// the query vector against an incompatible embedding space that merely
// happens to share a dimension. Every identity field the schema records
// is part of this filter — not just model id/revision, but artifact
// hash, tokenizer, normalization, and pooling for BGE-M3, and prompt
// template version for both — because any of them changing means the
// vector space changed, even if the dimension didn't.
//
// args is appended to in place; the returned clause references the
// resulting positions ($N) directly, so callers never have to reason
// about parameter offsets themselves.
func vectorChannelClause(queryVector []float32, identity rag.EmbeddingIdentity, promptTemplateVersion string, args *[]any) (table string, encoded string, whereClause string, err error) {
	if err := identity.Validate(); err != nil {
		return "", "", "", fmt.Errorf("vector channel requires a valid embedding identity: %w", err)
	}
	if strings.TrimSpace(promptTemplateVersion) == "" {
		return "", "", "", fmt.Errorf("%w: vector channel requires a prompt template version", rag.ErrInvalidRequest)
	}
	switch len(queryVector) {
	case chunkEmbeddingDimension:
		if identity.ModelVersion == "" {
			return "", "", "", fmt.Errorf("%w: a 768-dimensional query vector requires a gemini-shaped embedding identity (model_version set)", rag.ErrInvalidRequest)
		}
		encoded, encErr := encodeVector(queryVector)
		if encErr != nil {
			return "", "", "", encErr
		}
		*args = append(*args, identity.ModelID, identity.ModelVersion, promptTemplateVersion)
		n := len(*args)
		where := fmt.Sprintf("e.embedding_model_id=$%d AND e.embedding_model_version=$%d AND e.prompt_template_version=$%d", n-2, n-1, n)
		return "rag_chunk_embeddings", encoded, where, nil
	case bgeM3EmbeddingDimension:
		if identity.ModelRevision == "" {
			return "", "", "", fmt.Errorf("%w: a 1024-dimensional query vector requires a bge-m3-shaped embedding identity (model_revision set)", rag.ErrInvalidRequest)
		}
		encoded, encErr := encodeVectorBGEM3(queryVector)
		if encErr != nil {
			return "", "", "", encErr
		}
		*args = append(*args, identity.ModelID, identity.ModelRevision, identity.ArtifactSHA256, identity.TokenizerRevision, identity.Normalization, identity.Pooling, promptTemplateVersion)
		n := len(*args)
		where := fmt.Sprintf(
			"e.embedding_model_id=$%d AND e.model_revision=$%d AND e.artifact_sha256=$%d AND e.tokenizer_revision=$%d AND e.normalization=$%d AND e.pooling=$%d AND e.prompt_template_version=$%d",
			n-6, n-5, n-4, n-3, n-2, n-1, n,
		)
		return "rag_chunk_embeddings_bge_m3", encoded, where, nil
	default:
		return "", "", "", fmt.Errorf("%w: query vector has unexpected dimension %d (want %d or %d)", rag.ErrInvalidRequest, len(queryVector), chunkEmbeddingDimension, bgeM3EmbeddingDimension)
	}
}

// rrfK is the standard Reciprocal Rank Fusion constant (score = 1/(k+rank)).
// Chosen over a weighted sum of raw channel scores because ts_rank and
// vector cosine distance live on unrelated, unbounded scales that would
// need per-corpus tuning to combine meaningfully — RRF only needs each
// channel's *rank ordering*, which stays comparable across channels without
// any tuning, and stays fully deterministic for the same query+corpus.
const rrfK = 60

// rrfCandidatePoolSize bounds how many rows each channel contributes to the
// fusion before the final LIMIT is applied — wide enough that a chunk
// ranked, say, 30th on one channel but 1st on another still has a chance to
// surface after fusion, without each channel scanning unboundedly.
func rrfCandidatePoolSize(limit int) int {
	pool := limit * 5
	if pool < 50 {
		pool = 50
	}
	return pool
}

// runHybridQuery fuses three independent retrieval channels by Reciprocal
// Rank Fusion, each scoped to organizationID+generationID+lifecycle=
// 'approved' *inside its own CTE* — never as a filter applied after
// fusion, which could let an out-of-scope row's rank distort another row's
// score before ever being filtered out:
//
//   - exact: identifier_tokens overlap (migration 000029) — the channel
//     that exists specifically because neither FTS nor embeddings reliably
//     distinguish "20" from "2000" or survive PostgreSQL's tokenizer
//     attaching a leading hyphen to a trailing number.
//   - lexical: ts_rank/plainto_tsquery, exactly as Query used before R29.
//   - vector: cosine distance against exactly one embeddings table, chosen
//     by queryVector's own dimension, filtered to rows matching the exact
//     embedding identity supplied (see vectorChannelClause) — never both
//     tables in the same query, and never more than one row per chunk even
//     when a chunk has been re-embedded under more than one revision.
//     Exact/brute-force search (no ANN index yet — see migrations
//     000028/000032). Included only when queryVector is non-empty; a chunk
//     with no embedding row yet simply cannot appear via this channel,
//     which is the intended graceful degradation (whether from no
//     embedding having been computed, or from Manager.embedQuery skipping
//     the call entirely — e.g. the active profile's provider being
//     unavailable), not a bug to work around.
func (s *Store) runHybridQuery(ctx context.Context, organizationID, generationID, queryText string, queryVector []float32, identity rag.EmbeddingIdentity, promptTemplateVersion string, limit int) (pgx.Rows, error) {
	poolSize := rrfCandidatePoolSize(limit)
	args := []any{organizationID, generationID, queryText, poolSize, limit}
	vectorCTE := ""
	vectorUnion := ""
	if len(queryVector) > 0 {
		table, encoded, identityWhere, err := vectorChannelClause(queryVector, identity, promptTemplateVersion, &args)
		if err != nil {
			return nil, err
		}
		args = append(args, encoded)
		vectorParam := "$" + strconv.Itoa(len(args))
		vectorCTE = `,
vector_matches AS (
    SELECT e.chunk_id, ROW_NUMBER() OVER (ORDER BY e.embedding <=> ` + vectorParam + `::vector ASC) AS rnk
    FROM ` + table + ` e
    JOIN rag_knowledge_chunks c ON c.organization_id=e.organization_id AND c.chunk_id=e.chunk_id
    WHERE e.organization_id=$1 AND c.generation_id=$2 AND ` + identityWhere + `
    ORDER BY e.embedding <=> ` + vectorParam + `::vector ASC
    LIMIT $4
)`
		vectorUnion = `
    UNION ALL
    SELECT chunk_id, rnk FROM vector_matches`
	}

	query := `
WITH exact_matches AS (
    SELECT c.chunk_id, ROW_NUMBER() OVER (ORDER BY c.chunk_id ASC) AS rnk
    FROM rag_knowledge_chunks c
    WHERE c.organization_id=$1 AND c.generation_id=$2 AND c.identifier_tokens && extract_digit_runs($3)
    ORDER BY c.chunk_id ASC
    LIMIT $4
),
lexical_matches AS (
    SELECT c.chunk_id, ROW_NUMBER() OVER (ORDER BY ts_rank(c.content_tsv, plainto_tsquery('simple', $3)) DESC, c.chunk_id ASC) AS rnk
    FROM rag_knowledge_chunks c
    WHERE c.organization_id=$1 AND c.generation_id=$2 AND c.content_tsv @@ plainto_tsquery('simple', $3)
    ORDER BY ts_rank(c.content_tsv, plainto_tsquery('simple', $3)) DESC, c.chunk_id ASC
    LIMIT $4
)` + vectorCTE + `,
fused AS (
    SELECT chunk_id, SUM(1.0 / (` + strconv.Itoa(rrfK) + ` + rnk)) AS rrf_score
    FROM (
        SELECT chunk_id, rnk FROM exact_matches
        UNION ALL
        SELECT chunk_id, rnk FROM lexical_matches` + vectorUnion + `
    ) all_channels
    GROUP BY chunk_id
)
SELECT c.chunk_id,c.version_id,c.chunker_id,c.chunker_version,c.ordinal,c.start_offset,c.end_offset,c.content,c.content_hash,
 v.document_id,v.title,v.source_reference,v.data_class,v.canonical_hash,
 f.rrf_score AS score
FROM fused f
JOIN rag_knowledge_chunks c ON c.organization_id=$1 AND c.chunk_id=f.chunk_id
JOIN rag_knowledge_versions v ON v.organization_id=c.organization_id AND v.version_id=c.version_id AND v.lifecycle='approved'
ORDER BY score DESC, c.chunk_id ASC
LIMIT $5`

	return s.pool.Query(ctx, strings.TrimSpace(query), args...)
}
