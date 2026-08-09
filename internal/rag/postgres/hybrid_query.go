package postgres

import (
	"context"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

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
//   - vector: cosine distance against rag_chunk_embeddings, exact/
//     brute-force search (no ANN index yet — see migration 000028).
//     Included only when queryVector is non-empty; a chunk with no
//     embedding row yet simply cannot appear via this channel, which is
//     the intended graceful degradation, not a bug to work around.
func (s *Store) runHybridQuery(ctx context.Context, organizationID, generationID, queryText string, queryVector []float32, limit int) (pgx.Rows, error) {
	poolSize := rrfCandidatePoolSize(limit)
	args := []any{organizationID, generationID, queryText, poolSize, limit}
	vectorCTE := ""
	vectorUnion := ""
	if len(queryVector) > 0 {
		encoded, err := encodeVector(queryVector)
		if err != nil {
			return nil, err
		}
		args = append(args, encoded)
		vectorParam := "$" + strconv.Itoa(len(args))
		vectorCTE = `,
vector_matches AS (
    SELECT e.chunk_id, ROW_NUMBER() OVER (ORDER BY e.embedding <=> ` + vectorParam + `::vector ASC) AS rnk
    FROM rag_chunk_embeddings e
    JOIN rag_knowledge_chunks c ON c.organization_id=e.organization_id AND c.chunk_id=e.chunk_id
    WHERE e.organization_id=$1 AND c.generation_id=$2
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
