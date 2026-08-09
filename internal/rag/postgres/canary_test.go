//go:build integration

// R30 phase 8: the canary comparison the whole branch exists to produce —
// lexical-only vs. Gemini-hybrid (vector(768)) vs. BGE-M3-hybrid
// (vector(1024)) on the exact three cases R29's own motivating design
// used ("error-20"<->"error 20" exact, "fallo número veinte"<->"error 20"
// semantic, "error 20" must never match "error 2000"). Real Postgres,
// real Store.Query/RRF fusion, real internal/evaluation/metrics numbers.
//
// The query vectors here are deterministic, hand-constructed synthetic
// vectors, NOT real Gemini or BGE-M3 embeddings — no live Gemini API key
// and no running BGE-M3 sidecar are available in this environment (see
// docs/implementation/branch-30-canary-evaluation-bge-m3/HANDOFF.md). This
// test proves the RRF fusion and profile-selectable table routing work
// correctly end to end against real Postgres; it is not a claim about
// either provider's real embedding quality.
package postgres_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/evaluation/metrics"
	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	"github.com/Mireuz13/explorarte-organization/internal/rag"
	ragpostgres "github.com/Mireuz13/explorarte-organization/internal/rag/postgres"
	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
)

const canaryNamespace = "ingenieria_ia_canary"

func TestR30CanaryComparisonLexicalVsGeminiVsBGEM3(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	platform := openRAGStore(t, ctx)
	t.Cleanup(platform.Close)
	runner, err := platformmigrations.New(platform.Pool(), rootmigrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Up(ctx); err != nil {
		t.Fatal(err)
	}
	resetRAGSchema(t, ctx, platform)
	t.Cleanup(func() { resetRAGSchema(t, context.Background(), platform) })
	syncRAGCanonical(t, ctx, platform)
	store, err := ragpostgres.New(platform, ragIntegrationOrganization)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	clock := &fixedClock{now: now}
	domain := rag.NewService(clock)

	docs := []struct {
		id, body string
	}{
		{"know-canary-exact", "Se registro un error-20 durante el despliegue nocturno del servicio de facturación."},
		{"know-canary-negative", "El sistema reporto error 2000 en el log de auditoria del mismo servicio."},
		{"know-canary-semantic", "Hubo un fallo numero veinte en el pipeline de ingestion de datos, sin relacion lexica con el termino error."},
		// Distractors give the corpus real size (k=3 against a 3-document
		// corpus is a degenerate test: every channel's UNION ALL includes
		// every row regardless of relevance, so a "top-3" check proves
		// nothing about ranking quality). None of these share a word or a
		// digit with the query.
		{"know-canary-distractor-1", "El equipo de finanzas revisa el presupuesto trimestral cada lunes por la mañana."},
		{"know-canary-distractor-2", "La nueva politica de vacaciones entra en vigor el proximo semestre fiscal."},
		{"know-canary-distractor-3", "El catalogo de productos se actualizo con fotografias de mejor resolucion."},
		{"know-canary-distractor-4", "La reunion de directorio se reprogramo para el jueves por la tarde."},
	}
	var chunks []rag.Chunk
	chunkIDByDoc := map[string]string{}
	for i, d := range docs {
		version := proposeVersionInNamespace(t, domain, clock, now.Add(time.Duration(i)*time.Second), d.id, canaryNamespace)
		version.Title = "canary fixture"
		version.Body = d.body
		version = mustReconanonicalize(t, version)
		created, _, err := store.CreateCandidate(ctx, rag.CreateCandidateCommand{Version: version, IdempotencyKey: "idem-" + d.id})
		if err != nil {
			t.Fatal(err)
		}
		clock.now = clock.now.Add(time.Second)
		approved, err := domain.Review(created, rag.ReviewApprove, ragIntegrationReviewer)
		if err != nil {
			t.Fatal(err)
		}
		approved, err = store.Save(ctx, rag.SaveCommand{Version: approved, ExpectedRevision: 1, ActorID: ragIntegrationReviewer, Reason: "ok"})
		if err != nil {
			t.Fatal(err)
		}
		docChunks, err := rag.ChunkBody(approved.ID, rag.DefaultChunkerID, rag.DefaultChunkerVersion, approved.Body)
		if err != nil {
			t.Fatal(err)
		}
		chunks = append(chunks, docChunks...)
	}
	if _, err := store.Reindex(ctx, rag.ReindexCommand{OrganizationID: ragIntegrationOrganization, NamespaceKind: rag.NamespaceDepartment, NamespaceID: canaryNamespace, ChunkerID: rag.DefaultChunkerID, ChunkerVersion: rag.DefaultChunkerVersion, Chunks: chunks}); err != nil {
		t.Fatal(err)
	}
	for _, d := range docs {
		var chunkID string
		if err := platform.Pool().QueryRow(ctx, `SELECT c.chunk_id FROM rag_knowledge_chunks c JOIN rag_knowledge_versions v ON v.organization_id=c.organization_id AND v.version_id=c.version_id WHERE c.organization_id=$1 AND v.document_id=$2`, ragIntegrationOrganization, d.id).Scan(&chunkID); err != nil {
			t.Fatal(err)
		}
		chunkIDByDoc[d.id] = chunkID
	}

	relevant := map[string]struct{}{
		chunkIDByDoc["know-canary-exact"]:    {},
		chunkIDByDoc["know-canary-semantic"]: {},
	}
	forbidden := map[string]struct{}{chunkIDByDoc["know-canary-negative"]: {}}

	// "close" (cosine similarity 1.0 with the query) for the two truly
	// relevant documents; "opposite" (similarity -1.0, the worst possible)
	// for the negative document — guaranteed to rank below every
	// "orthogonal" (similarity 0.0) distractor, deterministically, never
	// by tie-breaking luck. This is what makes the numeric-false-positive
	// check below meaningful instead of a corpus-too-small artifact.
	docKind := map[string]string{
		"know-canary-exact": "close", "know-canary-semantic": "close", "know-canary-negative": "opposite",
		"know-canary-distractor-1": "orthogonal", "know-canary-distractor-2": "orthogonal",
		"know-canary-distractor-3": "orthogonal", "know-canary-distractor-4": "orthogonal",
	}
	syntheticVector := func(dim int, kind string) []float32 {
		v := make([]float32, dim)
		switch kind {
		case "close":
			v[0] = 1
		case "opposite":
			v[0] = -1
		default: // orthogonal
			v[1] = 1
		}
		return v
	}

	run := func(subject string, queryVector []float32, identity rag.EmbeddingIdentity, promptTemplateVersion string) []string {
		results, err := store.Query(ctx, rag.QueryCommand{
			OrganizationID: ragIntegrationOrganization, NamespaceKind: rag.NamespaceDepartment, NamespaceID: canaryNamespace,
			QueryText: "error 20", QueryVector: queryVector, Limit: 10,
			EmbeddingIdentity: identity, EmbeddingPromptTemplateVersion: promptTemplateVersion,
		})
		if err != nil {
			t.Fatalf("subject=%s: %v", subject, err)
		}
		ranked := make([]string, len(results))
		for i, r := range results {
			ranked[i] = r.Chunk.ID
		}
		return ranked
	}

	report := func(subject string, ranked []string) {
		recall := metrics.RecallAt(ranked, relevant, 3)
		ndcg := metrics.NDCGAt(ranked, relevant, 3)
		fpr := metrics.NumericFalsePositiveRate(ranked, forbidden, 3)
		t.Logf("R30 CANARY subject=%s ranked=%v recall@3=%.4f ndcg@3=%.4f numeric_false_positive_rate@3=%.4f", subject, ranked, recall, ndcg, fpr)
		if fpr != 0 {
			t.Fatalf("subject=%s: HARD GATE VIOLATION: positive 20-vs-2000 confusion (numeric_false_positive_rate=%.4f)", subject, fpr)
		}
	}

	// A) lexical-only: no vector channel at all.
	lexical := run("lexical", nil, rag.EmbeddingIdentity{}, "")
	report("lexical", lexical)

	// B) gemini-hybrid: 768-dim synthetic embeddings for every document —
	// including distractors, so the vector channel has real competition
	// instead of trivially ranking all 3 original documents into "top 3".
	inputHashCounter := 200
	for _, d := range docs {
		inputHashCounter++
		if err := store.InsertChunkEmbedding(ctx, rag.ChunkEmbedding{
			OrganizationID: ragIntegrationOrganization, ChunkID: chunkIDByDoc[d.id],
			EmbeddingModelID: "gemini-embedding-2", EmbeddingModelVersion: "v1", EmbeddingDimension: 768,
			PromptTemplateVersion: "prompt-template.v1", InputHash: fmt.Sprintf("%064x", inputHashCounter), Vector: syntheticVector(768, docKind[d.id]), CreatedAt: clock.now,
		}); err != nil {
			t.Fatalf("insert gemini embedding for %s: %v", d.id, err)
		}
	}
	geminiHybrid := run("gemini-hybrid", syntheticVector(768, "close"), rag.EmbeddingIdentity{ModelID: "gemini-embedding-2", ModelVersion: "v1"}, "prompt-template.v1")
	report("gemini-hybrid", geminiHybrid)

	// C) bge-m3-hybrid: 1024-dim synthetic embeddings, separate table, same
	// per-document kind assignment.
	for _, d := range docs {
		inputHashCounter++
		if err := store.InsertBGEM3ChunkEmbedding(ctx, rag.BGEM3ChunkEmbedding{
			OrganizationID: ragIntegrationOrganization, ChunkID: chunkIDByDoc[d.id], EmbeddingModelID: "bge-m3-local",
			ModelRevision: "bge-m3-2024-06", ArtifactSHA256: strings.Repeat("c", 64), TokenizerRevision: "bge-m3-tokenizer-2024-06",
			EmbeddingDimension: 1024, Normalization: "l2", Pooling: "cls", PromptTemplateVersion: "bge-m3-prompt-template.v1",
			InputHash: fmt.Sprintf("%064x", inputHashCounter), Vector: syntheticVector(1024, docKind[d.id]), CreatedAt: clock.now,
		}); err != nil {
			t.Fatalf("insert bge-m3 embedding for %s: %v", d.id, err)
		}
	}
	bgeM3Hybrid := run("bge-m3-hybrid", syntheticVector(1024, "close"), rag.EmbeddingIdentity{
		ModelID: "bge-m3-local", ModelRevision: "bge-m3-2024-06", ArtifactSHA256: strings.Repeat("c", 64),
		TokenizerRevision: "bge-m3-tokenizer-2024-06", Normalization: "l2", Pooling: "cls",
	}, "bge-m3-prompt-template.v1")
	report("bge-m3-hybrid", bgeM3Hybrid)

	// Hard gate: lexical alone must never find the semantic-only document
	// (no shared words with the query) — the whole point of the
	// comparison is that the hybrid modes add real recall lexical cannot.
	lexicalFoundSemantic := false
	for _, id := range lexical {
		if id == chunkIDByDoc["know-canary-semantic"] {
			lexicalFoundSemantic = true
		}
	}
	if lexicalFoundSemantic {
		t.Fatal("lexical-only unexpectedly found the semantic-only document — the canary fixture no longer isolates what it claims to")
	}
}
