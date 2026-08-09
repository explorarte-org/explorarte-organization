package webevidence

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
)

// rrfK mirrors internal/rag/postgres/hybrid_query.go's constant exactly —
// same reasoning: rank fusion over unrelated scales, not a weighted sum.
const rrfK = 60

// RankedChunk pairs a Chunk with its fused rank score (higher is better).
type RankedChunk struct {
	Chunk Chunk
	Score float64
}

var wordPattern = regexp.MustCompile(`[a-zA-Z0-9]+`)

func tokenize(text string) map[string]struct{} {
	tokens := make(map[string]struct{})
	for _, word := range wordPattern.FindAllString(strings.ToLower(text), -1) {
		tokens[word] = struct{}{}
	}
	return tokens
}

func keywordOverlapScore(queryTokens map[string]struct{}, chunk Chunk) float64 {
	if len(queryTokens) == 0 {
		return 0
	}
	chunkTokens := tokenize(chunk.Text)
	overlap := 0
	for token := range queryTokens {
		if _, ok := chunkTokens[token]; ok {
			overlap++
		}
	}
	return float64(overlap) / float64(len(queryTokens))
}

func cosineSimilarity(a, b []float32) (float64, error) {
	if len(a) != len(b) {
		return 0, fmt.Errorf("webevidence: vector dimension mismatch (%d vs %d)", len(a), len(b))
	}
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	if normA == 0 || normB == 0 {
		return 0, nil
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB)), nil
}

// RankChunks fuses a lexical keyword-overlap channel with an (optional)
// vector-similarity channel by Reciprocal Rank Fusion, exactly the same
// principle internal/rag's hybrid query already uses for approved
// knowledge — reused here, not reinvented, for R30's ephemeral web
// evidence. chunkVectors, if provided, must be 1:1 with chunks by index;
// a nil/empty chunkVectors (or empty queryVector) runs the lexical channel
// alone, which is the intended degradation when no embedding profile is
// available or the embed call itself degraded (see rag.Manager.embedQuery/
// memory.Manager's equivalent for the same pattern).
func RankChunks(queryText string, queryVector []float32, chunks []Chunk, chunkVectors [][]float32) ([]RankedChunk, error) {
	if len(chunks) == 0 {
		return nil, nil
	}
	if len(chunkVectors) > 0 && len(chunkVectors) != len(chunks) {
		return nil, fmt.Errorf("webevidence: chunkVectors has %d entries, want %d (1:1 with chunks)", len(chunkVectors), len(chunks))
	}
	queryTokens := tokenize(queryText)

	type scored struct {
		chunk        Chunk
		lexicalScore float64
		vectorScore  float64
		hasVector    bool
	}
	entries := make([]scored, len(chunks))
	for i, chunk := range chunks {
		entries[i] = scored{chunk: chunk, lexicalScore: keywordOverlapScore(queryTokens, chunk)}
		if len(queryVector) > 0 && len(chunkVectors) > 0 {
			similarity, err := cosineSimilarity(queryVector, chunkVectors[i])
			if err != nil {
				return nil, err
			}
			entries[i].vectorScore = similarity
			entries[i].hasVector = true
		}
	}

	lexicalRank := rankByDescending(entries, func(e scored) float64 { return e.lexicalScore })
	var vectorRank map[int]int
	if len(queryVector) > 0 && len(chunkVectors) > 0 {
		vectorRank = rankByDescending(entries, func(e scored) float64 { return e.vectorScore })
	}

	results := make([]RankedChunk, len(entries))
	for i, entry := range entries {
		rrfScore := 1.0 / float64(rrfK+lexicalRank[i])
		if vectorRank != nil {
			rrfScore += 1.0 / float64(rrfK+vectorRank[i])
		}
		results[i] = RankedChunk{Chunk: entry.chunk, Score: rrfScore}
	}
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		return results[i].Chunk.Ordinal < results[j].Chunk.Ordinal
	})
	return results, nil
}

// rankByDescending returns, for each index in entries, its 1-based rank
// under score (highest score = rank 1), used as RRF's rnk input.
func rankByDescending[T any](entries []T, score func(T) float64) map[int]int {
	indices := make([]int, len(entries))
	for i := range entries {
		indices[i] = i
	}
	sort.SliceStable(indices, func(a, b int) bool {
		return score(entries[indices[a]]) > score(entries[indices[b]])
	})
	rank := make(map[int]int, len(entries))
	for position, index := range indices {
		rank[index] = position + 1
	}
	return rank
}
