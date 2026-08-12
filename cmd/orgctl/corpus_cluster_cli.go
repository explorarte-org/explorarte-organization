package main

import (
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/corpuscensus"
	"github.com/Mireuz13/explorarte-organization/internal/corpuscluster"
)

// runCorpusCluster implements `orgctl corpus cluster`: reads the Silver
// state file corpus census already produced (never re-reads Bronze,
// never re-runs Poppler), keeps only Works eligible for curation
// (accepted or review_required -- everything else is already terminal
// and excluded per its own Decision: duplicate/superseded/invalid/
// encrypted/timeout/low_relevance/quarantine never enter clustering),
// and groups them into semantic clusters. Writes nothing back to the
// Silver file; clustering output is this command's own JSON, consumed
// next by `orgctl corpus curate` (a separate command).
func runCorpusCluster(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("corpus cluster", flag.ContinueOnError)
	flags.SetOutput(stderr)
	stateFile := flags.String("state-file", "", "path to the Silver JSONL state file produced by `orgctl corpus census`")
	threshold := flags.Float64("threshold", 0.28, "cosine-similarity threshold for merging two Works into the same cluster")
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if strings.TrimSpace(*stateFile) == "" {
		fmt.Fprintln(stderr, "--state-file is required")
		return exitUsage
	}

	store, err := corpuscensus.OpenStateStore(*stateFile)
	if err != nil {
		fmt.Fprintf(stderr, "open state store: %v\n", err)
		return exitInternal
	}

	var works []corpuscluster.WorkInput
	titleByID := make(map[string]string)
	for _, record := range store.All() {
		if record.Decision != corpuscensus.DecisionAccepted && record.Decision != corpuscensus.DecisionReviewRequired {
			continue
		}
		works = append(works, corpuscluster.WorkInput{WorkID: record.WorkID, Title: record.Title})
		titleByID[record.WorkID] = record.Title
	}
	sort.Slice(works, func(i, j int) bool { return works[i].WorkID < works[j].WorkID })

	clusters := corpuscluster.BuildClusters(works, *threshold)

	sizes := make([]int, len(clusters))
	singletons := 0
	for i, c := range clusters {
		sizes[i] = len(c.WorkIDs)
		if len(c.WorkIDs) == 1 {
			singletons++
		}
	}
	sort.Ints(sizes)

	type clusterOut struct {
		ID      string   `json:"id"`
		WorkIDs []string `json:"work_ids"`
		Titles  []string `json:"titles"`
	}
	out := make([]clusterOut, len(clusters))
	for i, c := range clusters {
		titles := make([]string, len(c.WorkIDs))
		for j, id := range c.WorkIDs {
			titles[j] = titleByID[id]
		}
		out[i] = clusterOut{ID: c.ID, WorkIDs: c.WorkIDs, Titles: titles}
	}

	result := struct {
		WorksClustered   int          `json:"works_clustered"`
		ClusterCount     int          `json:"cluster_count"`
		SingletonCount   int          `json:"singleton_count"`
		MedianSize       int          `json:"median_cluster_size"`
		P90Size          int          `json:"p90_cluster_size"`
		LargestSize      int          `json:"largest_cluster_size"`
		Threshold        float64      `json:"threshold"`
		AlgorithmVersion string       `json:"algorithm_version"`
		Clusters         []clusterOut `json:"clusters"`
	}{
		WorksClustered:   len(works),
		ClusterCount:     len(clusters),
		SingletonCount:   singletons,
		Threshold:        *threshold,
		AlgorithmVersion: "tfidf-cosine-v1",
		Clusters:         out,
	}
	if len(sizes) > 0 {
		result.MedianSize = sizes[len(sizes)/2]
		result.P90Size = sizes[(len(sizes)*9)/10]
		result.LargestSize = sizes[len(sizes)-1]
	}

	writeValue(stdout, *jsonOutput, result)
	return exitOK
}
