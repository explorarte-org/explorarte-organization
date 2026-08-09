package postgres

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/memory"
)

// rrfK mirrors internal/rag/postgres/hybrid_query.go's constant exactly —
// see that file's doc comment for why RRF over a weighted sum.
const rrfK = 60

// vectorChannelClause mirrors internal/rag/postgres's function of the same
// name exactly — same dimension-selects-table discipline, same identity
// filter (a chunk/entry's embedding table primary key allows more than
// one row per entry, so without this filter the vector channel could
// return more than one row for the same entry — see that file's doc
// comment for the full rationale), same deliberate small duplication
// internal/memory already accepts elsewhere (it cannot import internal/rag).
func vectorChannelClause(queryVector []float32, identity memory.EmbeddingIdentity, promptTemplateVersion string, args *[]any) (table string, encoded string, whereClause string, err error) {
	if err := identity.Validate(); err != nil {
		return "", "", "", fmt.Errorf("vector channel requires a valid embedding identity: %w", err)
	}
	if strings.TrimSpace(promptTemplateVersion) == "" {
		return "", "", "", fmt.Errorf("%w: vector channel requires a prompt template version", memory.ErrInvalidRequest)
	}
	switch len(queryVector) {
	case entryEmbeddingDimension:
		if identity.ModelVersion == "" {
			return "", "", "", fmt.Errorf("%w: a 768-dimensional query vector requires a gemini-shaped embedding identity (model_version set)", memory.ErrInvalidRequest)
		}
		enc, encErr := encodeVector(queryVector)
		if encErr != nil {
			return "", "", "", encErr
		}
		*args = append(*args, identity.ModelID, identity.ModelVersion, promptTemplateVersion)
		n := len(*args)
		where := fmt.Sprintf("e.embedding_model_id=$%d AND e.embedding_model_version=$%d AND e.prompt_template_version=$%d", n-2, n-1, n)
		return "organizational_memory_embeddings", enc, where, nil
	case bgeM3EntryEmbeddingDimension:
		if identity.ModelRevision == "" {
			return "", "", "", fmt.Errorf("%w: a 1024-dimensional query vector requires a bge-m3-shaped embedding identity (model_revision set)", memory.ErrInvalidRequest)
		}
		enc, encErr := encodeVectorBGEM3(queryVector)
		if encErr != nil {
			return "", "", "", encErr
		}
		*args = append(*args, identity.ModelID, identity.ModelRevision, identity.ArtifactSHA256, identity.TokenizerRevision, identity.Normalization, identity.Pooling, promptTemplateVersion)
		n := len(*args)
		where := fmt.Sprintf(
			"e.embedding_model_id=$%d AND e.model_revision=$%d AND e.artifact_sha256=$%d AND e.tokenizer_revision=$%d AND e.normalization=$%d AND e.pooling=$%d AND e.prompt_template_version=$%d",
			n-6, n-5, n-4, n-3, n-2, n-1, n,
		)
		return "organizational_memory_embeddings_bge_m3", enc, where, nil
	default:
		return "", "", "", fmt.Errorf("%w: query vector has unexpected dimension %d (want %d or %d)", memory.ErrInvalidRequest, len(queryVector), entryEmbeddingDimension, bgeM3EntryEmbeddingDimension)
	}
}

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
// deterministically instead of returning nothing. identity/
// promptTemplateVersion are required whenever queryVector is non-empty —
// see EmbeddingRepository.Search's doc comment.
func (s *Store) Search(ctx context.Context, organizationID, roleID, queryText string, queryVector []float32, identity memory.EmbeddingIdentity, promptTemplateVersion string, limit int) ([]memory.Entry, error) {
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
		table, encoded, identityWhere, err := vectorChannelClause(queryVector, identity, promptTemplateVersion, &args)
		if err != nil {
			return nil, err
		}
		args = append(args, encoded)
		vectorParam := "$" + strconv.Itoa(len(args))
		vectorCTE = `,
vector_matches AS (
    SELECT e.entry_key, ROW_NUMBER() OVER (ORDER BY e.embedding <=> ` + vectorParam + `::vector ASC) AS rnk
    FROM ` + table + ` e
    JOIN organizational_memory_versions v ON v.organization_id=e.organization_id AND v.entry_key=e.entry_key
    WHERE e.organization_id=$1 AND v.role_id=$2 AND ` + identityWhere + `
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
