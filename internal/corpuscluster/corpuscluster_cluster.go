// Package corpuscluster groups a Silver corpus's Works into semantic
// clusters -- subproblems (query rewriting, GraphRAG construction, memory
// consolidation), not broad topics (owner decision, curation re-run
// section 9/10: "semantic cluster != duplicate group", and a cluster
// like "RAG: 800 papers" is explicitly called out as useless). This
// package never proposes a Knowledge candidate, never writes to the
// organization's Postgres, and never calls a model -- clustering here is
// a cheap, deterministic, reproducible TF-IDF/cosine step over Work
// titles (the only free-text field the harvester's schema carries; no
// abstract field exists), the input to a LATER curation stage
// (internal/corpuscuration), not curation itself.
//
// Why TF-IDF over titles instead of a real embedding model: the owner's
// own instruction explicitly frames this as needing to stay "barata"
// (cheap) and Work-level, and the organization's one local embedding
// service (internal/embeddingruntime/adapter/bgem3, BGE-M3) is not
// currently wired into the running deployment (no env vars configured in
// compose.yaml -- confirmed by inspection, not assumed) and its identity
// pinning is designed for the RAG pipeline's strict provenance
// requirements, not ad hoc clustering. TF-IDF/cosine is dependency-free,
// fully local, and reproducible byte-for-byte across runs; a real
// embedding-based upgrade is a natural, isolated future improvement to
// just this package, documented as such rather than silently substituted.
package corpuscluster

import (
	"math"
	"regexp"
	"sort"
	"strings"
)

var clusterStopwords = map[string]bool{
	"the": true, "of": true, "and": true, "a": true, "an": true, "in": true, "on": true,
	"for": true, "to": true, "with": true, "via": true, "using": true, "based": true,
	"is": true, "are": true, "by": true, "from": true, "as": true, "at": true, "we": true,
	"this": true, "that": true, "into": true, "towards": true, "toward": true, "new": true,
}

var wordTokenPattern = regexp.MustCompile(`[a-z0-9]+`)

func tokenize(title string) []string {
	raw := wordTokenPattern.FindAllString(strings.ToLower(title), -1)
	tokens := make([]string, 0, len(raw))
	for _, t := range raw {
		if len(t) < 3 || clusterStopwords[t] {
			continue
		}
		tokens = append(tokens, t)
	}
	return tokens
}

// WorkInput is the minimal shape this package needs from a Silver
// record -- decoupled from corpuscensus.SilverRecord so this package has
// no import dependency on it (callers adapt at the boundary).
type WorkInput struct {
	WorkID string
	Title  string
}

// sparseVector is a term -> TF-IDF weight map. Sparse because most
// documents share only a handful of the corpus's total vocabulary.
type sparseVector map[string]float64

func norm(v sparseVector) float64 {
	sum := 0.0
	for _, w := range v {
		sum += w * w
	}
	return math.Sqrt(sum)
}

func cosineSimilarity(a, b sparseVector, normA, normB float64) float64 {
	if normA == 0 || normB == 0 {
		return 0
	}
	dot := 0.0
	small, large := a, b
	if len(a) > len(b) {
		small, large = b, a
	}
	for term, wa := range small {
		if wb, ok := large[term]; ok {
			dot += wa * wb
		}
	}
	return dot / (normA * normB)
}

// BuildTFIDF computes a sparse TF-IDF vector per Work from its title
// tokens. Deterministic: the same input slice (in the same order)
// always produces the same output.
func BuildTFIDF(works []WorkInput) map[string]sparseVector {
	docTokens := make(map[string][]string, len(works))
	docFreq := make(map[string]int)
	for _, w := range works {
		tokens := tokenize(w.Title)
		docTokens[w.WorkID] = tokens
		seen := make(map[string]bool, len(tokens))
		for _, t := range tokens {
			if !seen[t] {
				seen[t] = true
				docFreq[t]++
			}
		}
	}
	n := float64(len(works))
	vectors := make(map[string]sparseVector, len(works))
	for _, w := range works {
		tokens := docTokens[w.WorkID]
		termFreq := make(map[string]int, len(tokens))
		for _, t := range tokens {
			termFreq[t]++
		}
		vec := make(sparseVector, len(termFreq))
		for term, tf := range termFreq {
			df := float64(docFreq[term])
			idf := math.Log(n/df) + 1 // +1 keeps IDF positive even when df==n (a term in every title), so a fully-shared term still contributes some weight rather than vanishing to 0
			vec[term] = float64(tf) * idf
		}
		vectors[w.WorkID] = vec
	}
	return vectors
}

// Cluster is one group of Works this package believes address a similar
// subproblem. ID is a stable hash of its sorted member WorkIDs so re-runs
// on unchanged input produce the same ID (owner decision: resumability,
// section 29/30 -- a curation record needs a stable cluster_id to attach
// to).
type Cluster struct {
	ID      string
	WorkIDs []string
}

// BuildClusters groups Works via an inverted-index-driven, threshold-
// based single-link union-find over TF-IDF cosine similarity: two Works
// merge into the same cluster if their similarity meets threshold. The
// inverted index (term -> works containing it) means this only ever
// compares Work pairs that share at least one significant title term --
// O(works * avg_shared_bucket_size), not O(works^2) -- which is both why
// this runs in seconds at this corpus's scale and why two completely
// unrelated Works (e.g. "Prolog" vs "ASR") are never compared at all,
// closer to guaranteeing no giant catch-all cluster than a naive
// full-pairwise approach would.
func BuildClusters(works []WorkInput, threshold float64) []Cluster {
	vectors := BuildTFIDF(works)
	norms := make(map[string]float64, len(vectors))
	for id, v := range vectors {
		norms[id] = norm(v)
	}

	invertedIndex := make(map[string][]string) // term -> WorkIDs
	for _, w := range works {
		for term := range vectors[w.WorkID] {
			invertedIndex[term] = append(invertedIndex[term], w.WorkID)
		}
	}

	uf := newClusterUnionFind()
	for _, w := range works {
		uf.find(w.WorkID)
	}
	compared := make(map[string]bool) // dedupe pair comparisons across shared terms
	for _, w := range works {
		vecA := vectors[w.WorkID]
		normA := norms[w.WorkID]
		candidates := make(map[string]bool)
		for term := range vecA {
			for _, other := range invertedIndex[term] {
				if other != w.WorkID {
					candidates[other] = true
				}
			}
		}
		for other := range candidates {
			pairKey := pairKeyOf(w.WorkID, other)
			if compared[pairKey] {
				continue
			}
			compared[pairKey] = true
			sim := cosineSimilarity(vecA, vectors[other], normA, norms[other])
			if sim >= threshold {
				uf.union(w.WorkID, other)
			}
		}
	}

	groups := make(map[string][]string)
	for _, w := range works {
		root := uf.find(w.WorkID)
		groups[root] = append(groups[root], w.WorkID)
	}
	clusters := make([]Cluster, 0, len(groups))
	for _, members := range groups {
		sort.Strings(members)
		clusters = append(clusters, Cluster{ID: clusterIDOf(members), WorkIDs: members})
	}
	sort.Slice(clusters, func(i, j int) bool { return clusters[i].ID < clusters[j].ID })
	return clusters
}

func pairKeyOf(a, b string) string {
	if a > b {
		a, b = b, a
	}
	return a + "\x00" + b
}
