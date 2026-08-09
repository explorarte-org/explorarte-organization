package webevidence

import "testing"

func TestRankChunksLexicalOnlyOrdersByKeywordOverlap(t *testing.T) {
	chunks := []Chunk{
		{Ordinal: 0, Text: "the reactor core temperature exceeded the safety threshold"},
		{Ordinal: 1, Text: "unrelated content about quarterly earnings"},
		{Ordinal: 2, Text: "reactor safety threshold procedures and reactor maintenance"},
	}
	ranked, err := RankChunks("reactor safety threshold", nil, chunks, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(ranked) != 3 {
		t.Fatalf("ranked=%d want 3", len(ranked))
	}
	if ranked[0].Chunk.Ordinal == 1 {
		t.Fatalf("least relevant chunk ranked first: %+v", ranked)
	}
	if ranked[len(ranked)-1].Chunk.Ordinal != 1 {
		t.Fatalf("unrelated chunk should rank last: %+v", ranked)
	}
}

func TestRankChunksFusesVectorChannel(t *testing.T) {
	chunks := []Chunk{
		{Ordinal: 0, Text: "shares no words with the query"},
		{Ordinal: 1, Text: "also shares no words with the query text"},
	}
	// Chunk 0's vector is identical to the query vector (similarity 1);
	// chunk 1's is orthogonal-ish. Neither chunk matches lexically, so a
	// lexical-only rank would tie them — the vector channel must be what
	// breaks the tie in chunk 0's favor.
	queryVector := []float32{1, 0, 0, 0}
	chunkVectors := [][]float32{
		{1, 0, 0, 0},
		{0, 1, 0, 0},
	}
	ranked, err := RankChunks("completely different query", queryVector, chunks, chunkVectors)
	if err != nil {
		t.Fatal(err)
	}
	if ranked[0].Chunk.Ordinal != 0 {
		t.Fatalf("expected chunk 0 (cosine-identical) to rank first, got %+v", ranked)
	}
}

func TestRankChunksRejectsMismatchedVectorCount(t *testing.T) {
	chunks := []Chunk{{Ordinal: 0, Text: "a"}, {Ordinal: 1, Text: "b"}}
	if _, err := RankChunks("q", []float32{1}, chunks, [][]float32{{1}}); err == nil {
		t.Fatal("expected error when chunkVectors count does not match chunks count")
	}
}

func TestRankChunksEmptyInputReturnsNil(t *testing.T) {
	ranked, err := RankChunks("q", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ranked != nil {
		t.Fatalf("ranked=%v want nil", ranked)
	}
}

func TestCosineSimilarityRejectsDimensionMismatch(t *testing.T) {
	if _, err := cosineSimilarity([]float32{1, 2}, []float32{1, 2, 3}); err == nil {
		t.Fatal("expected error for mismatched vector dimensions")
	}
}
