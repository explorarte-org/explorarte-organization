package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/corpussemantic"
	"github.com/Mireuz13/explorarte-organization/internal/embeddingruntime/adapter/gemini"
)

// semanticInputFileEntry mirrors the JSON shape this branch's semantic-
// input build step produces (work_id, semantic_input, input_hash, plus
// provenance fields kept for audit but not needed by the embedder
// itself).
type semanticInputFileEntry struct {
	WorkID        string `json:"work_id"`
	SemanticInput string `json:"semantic_input"`
	InputHash     string `json:"input_hash"`
}

// runCorpusEmbed implements `orgctl corpus embed`. Reuses the exact same
// Gemini adapter construction internal/rag/bootstrap uses
// (gemini.LoadConfig + gemini.New, env-driven) -- this command must run
// where ORG_EMBEDDING_PROVIDER_GEMINI_* is configured (the orgd
// container, which already has the credential mounted for RAG; this
// command does not need Poppler/sqlite3, so it does not need the
// pdfingest image at all).
func runCorpusEmbed(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("corpus embed", flag.ContinueOnError)
	flags.SetOutput(stderr)
	inputPath := flags.String("input", "", "path to the semantic-input JSON file (array of {work_id, semantic_input, input_hash})")
	stateFile := flags.String("state-file", "", "path to this command's own resumable embeddings JSONL state file")
	batchSize := flags.Int("batch-size", 50, "Works per Gemini embedContent batch call")
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if strings.TrimSpace(*inputPath) == "" || strings.TrimSpace(*stateFile) == "" {
		fmt.Fprintln(stderr, "--input and --state-file are required")
		return exitUsage
	}

	raw, err := os.ReadFile(*inputPath)
	if err != nil {
		fmt.Fprintf(stderr, "read input file: %v\n", err)
		return exitUsage
	}
	var entries []semanticInputFileEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		fmt.Fprintf(stderr, "decode input file: %v\n", err)
		return exitUsage
	}
	inputs := make([]corpussemantic.SemanticInput, len(entries))
	for i, e := range entries {
		inputs[i] = corpussemantic.SemanticInput{WorkID: e.WorkID, SemanticInput: e.SemanticInput, InputHash: e.InputHash}
	}

	embeddingConfig, err := gemini.LoadConfig(os.LookupEnv, 1<<20)
	if err != nil {
		fmt.Fprintf(stderr, "load Gemini config: %v\n", err)
		return exitInternal
	}
	adapter, err := gemini.New(embeddingConfig)
	if err != nil {
		fmt.Fprintf(stderr, "create Gemini adapter: %v\n", err)
		return exitInternal
	}
	if adapter == nil {
		fmt.Fprintln(stderr, "Gemini embedding provider is disabled (ORG_EMBEDDING_PROVIDER_GEMINI_ENABLED not set)")
		return exitInternal
	}

	store, err := corpussemantic.OpenStore(*stateFile)
	if err != nil {
		fmt.Fprintf(stderr, "open state store: %v\n", err)
		return exitInternal
	}

	cfg := corpussemantic.DefaultEmbedConfig()
	cfg.BatchSize = *batchSize
	cfg.PromptTemplateVersion = gemini.PromptTemplateV1

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Hour)
	defer cancel()
	result, err := corpussemantic.Run(ctx, adapter, store, inputs, cfg)
	if err != nil {
		fmt.Fprintf(stderr, "embed run: %v\n", err)
		return exitInternal
	}

	writeValue(stdout, *jsonOutput, result)
	return exitOK
}

// runCorpusClusterSemantic implements `orgctl corpus cluster-semantic`:
// reads the embeddings state file this same package's `embed` command
// produced and runs average-link agglomerative clustering (owner
// decision: replaces `orgctl corpus cluster`'s TF-IDF/single-link
// baseline as the final clustering decision -- that command's output is
// kept only as an auxiliary lexical diagnostic).
func runCorpusClusterSemantic(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("corpus cluster-semantic", flag.ContinueOnError)
	flags.SetOutput(stderr)
	stateFile := flags.String("state-file", "", "path to the embeddings JSONL state file produced by `orgctl corpus embed`")
	threshold := flags.Float64("threshold", 0.72, "cosine-similarity threshold for average-link merging")
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if strings.TrimSpace(*stateFile) == "" {
		fmt.Fprintln(stderr, "--state-file is required")
		return exitUsage
	}

	store, err := corpussemantic.OpenStore(*stateFile)
	if err != nil {
		fmt.Fprintf(stderr, "open state store: %v\n", err)
		return exitInternal
	}
	records := store.All()
	sort.Slice(records, func(i, j int) bool { return records[i].WorkID < records[j].WorkID })

	workIDs := make([]string, len(records))
	vectors := make([][]float32, len(records))
	for i, r := range records {
		workIDs[i] = r.WorkID
		vectors[i] = r.Vector
	}

	clusters := corpussemantic.AverageLinkCluster(workIDs, vectors, *threshold)

	sizes := make([]int, len(clusters))
	singletons := 0
	var meanSims, minSims []float64
	for i, c := range clusters {
		sizes[i] = len(c.WorkIDs)
		if len(c.WorkIDs) == 1 {
			singletons++
		}
		meanSims = append(meanSims, c.MeanSimilarity)
		minSims = append(minSims, c.MinSimilarity)
	}
	sort.Ints(sizes)

	pctile := func(sorted []int, p float64) int {
		if len(sorted) == 0 {
			return 0
		}
		idx := int(float64(len(sorted)) * p)
		if idx >= len(sorted) {
			idx = len(sorted) - 1
		}
		return sorted[idx]
	}

	type clusterOut struct {
		ID                 string   `json:"id"`
		Size               int      `json:"size"`
		WorkIDs            []string `json:"work_ids"`
		MeanSimilarity     float64  `json:"mean_similarity"`
		MinSimilarity      float64  `json:"min_similarity"`
		CentroidSimilarity float64  `json:"centroid_similarity"`
	}
	out := make([]clusterOut, len(clusters))
	for i, c := range clusters {
		out[i] = clusterOut{ID: c.ID, Size: len(c.WorkIDs), WorkIDs: c.WorkIDs, MeanSimilarity: c.MeanSimilarity, MinSimilarity: c.MinSimilarity, CentroidSimilarity: c.CentroidSimilarity}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Size > out[j].Size })

	result := struct {
		WorksClustered int          `json:"works_clustered"`
		ClusterCount   int          `json:"cluster_count"`
		SingletonCount int          `json:"singleton_count"`
		SingletonPct   float64      `json:"singleton_pct"`
		MedianSize     int          `json:"median_cluster_size"`
		P90Size        int          `json:"p90_cluster_size"`
		P95Size        int          `json:"p95_cluster_size"`
		LargestSize    int          `json:"largest_cluster_size"`
		Threshold      float64      `json:"threshold"`
		Clusters       []clusterOut `json:"clusters"`
	}{
		WorksClustered: len(workIDs),
		ClusterCount:   len(clusters),
		SingletonCount: singletons,
		Threshold:      *threshold,
		Clusters:       out,
	}
	if len(clusters) > 0 {
		result.SingletonPct = float64(singletons) / float64(len(clusters)) * 100
	}
	if len(sizes) > 0 {
		result.MedianSize = sizes[len(sizes)/2]
		result.P90Size = pctile(sizes, 0.9)
		result.P95Size = pctile(sizes, 0.95)
		result.LargestSize = sizes[len(sizes)-1]
	}

	writeValue(stdout, *jsonOutput, result)
	return exitOK
}
