package postgres

import (
	"context"
	"strconv"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/memory"
)

// rrfK mirrors internal/rag/postgres/hybrid_query.go's constant exactly —
// see that file's doc comment for why RRF over a weighted sum.
const rrfK = 60

func rrfCandidatePoolSize(limit int) int {
	pool := limit * 5
	if pool < 50 {
		pool = 50
	}
	return pool
}

// Search fuses exact-identifier and vector channels by Reciprocal Rank
// Fusion, scoped to organizationID+roleID+status='approved' inside each
// channel's own CTE. Memory has no lexical (ts_rank) channel yet — unlike
// rag_knowledge_chunks, organizational_memory_versions carries no
// content_tsv column as of R29; adding one is a natural, self-contained
// follow-up migration, not done here. When queryVector is empty, Search
// still runs the exact channel so entries with a shared identifier surface
// deterministically instead of returning nothing.
func (s *Store) Search(ctx context.Context, organizationID, roleID, queryText string, queryVector []float32, limit int) ([]memory.Entry, error) {
	organizationID = strings.TrimSpace(organizationID)
	roleID = strings.TrimSpace(roleID)
	if organizationID != s.organizationID || roleID == "" {
		return nil, memory.ErrInvalidRequest
	}
	limit, err := normalizeLimit(limit)
	if err != nil {
		return nil, err
	}
	poolSize := rrfCandidatePoolSize(limit)
	args := []any{organizationID, roleID, queryText, poolSize, limit}
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
    SELECT e.entry_key, ROW_NUMBER() OVER (ORDER BY e.embedding <=> ` + vectorParam + `::vector ASC) AS rnk
    FROM organizational_memory_embeddings e
    JOIN organizational_memory_versions v ON v.organization_id=e.organization_id AND v.entry_key=e.entry_key
    WHERE e.organization_id=$1 AND v.role_id=$2
    ORDER BY e.embedding <=> ` + vectorParam + `::vector ASC
    LIMIT $4
)`
		vectorUnion = `
    UNION ALL
    SELECT entry_key, rnk FROM vector_matches`
	}

	query := `
WITH exact_matches AS (
    SELECT v.entry_key, ROW_NUMBER() OVER (ORDER BY v.entry_key ASC) AS rnk
    FROM organizational_memory_versions v
    WHERE v.organization_id=$1 AND v.role_id=$2 AND v.identifier_tokens && extract_digit_runs($3)
    ORDER BY v.entry_key ASC
    LIMIT $4
)` + vectorCTE + `,
fused AS (
    SELECT entry_key, SUM(1.0 / (` + strconv.Itoa(rrfK) + ` + rnk)) AS rrf_score
    FROM (
        SELECT entry_key, rnk FROM exact_matches` + vectorUnion + `
    ) all_channels
    GROUP BY entry_key
)
SELECT f.entry_key
FROM fused f
JOIN organizational_memory_entries m ON m.organization_id=$1 AND m.entry_key=f.entry_key AND m.status='approved'
ORDER BY f.rrf_score DESC, f.entry_key ASC
LIMIT $5`

	rows, err := s.pool.Query(ctx, strings.TrimSpace(query), args...)
	if err != nil {
		return nil, mapError("search organizational memory", err)
	}
	entryKeys := make([]string, 0, limit)
	for rows.Next() {
		var entryKey string
		if err := rows.Scan(&entryKey); err != nil {
			rows.Close()
			return nil, err
		}
		entryKeys = append(entryKeys, entryKey)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	entries := make([]memory.Entry, 0, len(entryKeys))
	for _, entryKey := range entryKeys {
		entry, err := s.Get(ctx, organizationID, entryKey)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, nil
}
