// Package metrics computes the retrieval-quality measurements R30's
// canary evaluation needs to compare lexical, Gemini-hybrid and
// BGE-M3-hybrid modes with real numbers instead of intuition: Recall@K,
// nDCG@K, MRR, identifier precision, paraphrase recall, and numeric
// false-positive rate. Every function here is pure — a ranked list of
// result IDs in, a float out — so it has no Postgres, network or clock
// dependency and is trivially reproducible.
package metrics

import "math"

// RecallAt reports the fraction of relevant IDs that appear anywhere in
// the first k entries of ranked. relevant must be non-empty; an empty
// ranked list simply scores 0.
func RecallAt(ranked []string, relevant map[string]struct{}, k int) float64 {
	if len(relevant) == 0 {
		return 0
	}
	if k > len(ranked) {
		k = len(ranked)
	}
	found := 0
	for i := 0; i < k; i++ {
		if _, ok := relevant[ranked[i]]; ok {
			found++
		}
	}
	return float64(found) / float64(len(relevant))
}

// NDCGAt is the normalized discounted cumulative gain at k, with binary
// relevance (an ID is either in relevant or not) — the standard reduction
// when there is no graded relevance score to work with.
func NDCGAt(ranked []string, relevant map[string]struct{}, k int) float64 {
	if len(relevant) == 0 {
		return 0
	}
	if k > len(ranked) {
		k = len(ranked)
	}
	dcg := 0.0
	for i := 0; i < k; i++ {
		if _, ok := relevant[ranked[i]]; ok {
			dcg += 1 / math.Log2(float64(i)+2)
		}
	}
	idealHits := len(relevant)
	if idealHits > k {
		idealHits = k
	}
	idcg := 0.0
	for i := 0; i < idealHits; i++ {
		idcg += 1 / math.Log2(float64(i)+2)
	}
	if idcg == 0 {
		return 0
	}
	return dcg / idcg
}

// ReciprocalRank is 1/rank of the first relevant ID in ranked (1-indexed),
// or 0 if none of the relevant IDs appear. MRR over a suite is the mean of
// this value across cases — callers average it themselves, since this
// package has no notion of a "suite".
func ReciprocalRank(ranked []string, relevant map[string]struct{}) float64 {
	for i, id := range ranked {
		if _, ok := relevant[id]; ok {
			return 1 / float64(i+1)
		}
	}
	return 0
}

// IdentifierPrecisionAt is RecallAt's dual for the exact-identifier
// channel: of the first k results, what fraction are true positives
// (relevant), rather than what fraction of the relevant set was found.
// R30's "positive 20-vs-2000 confusion" hard gate is precisely a drop in
// this metric to less than 1.0 on a fixture built to contain that
// near-miss.
func IdentifierPrecisionAt(ranked []string, relevant map[string]struct{}, k int) float64 {
	if k > len(ranked) {
		k = len(ranked)
	}
	if k == 0 {
		return 1
	}
	found := 0
	for i := 0; i < k; i++ {
		if _, ok := relevant[ranked[i]]; ok {
			found++
		}
	}
	return float64(found) / float64(k)
}

// NumericFalsePositiveRate is the fraction of the first k results that are
// in forbiddenNearMisses (e.g. "error 2000" when the query was "error
// 20") — a numeric confusion the exact-identifier channel must never
// produce, whatever channel supplied the result.
func NumericFalsePositiveRate(ranked []string, forbiddenNearMisses map[string]struct{}, k int) float64 {
	if k > len(ranked) {
		k = len(ranked)
	}
	if k == 0 {
		return 0
	}
	hits := 0
	for i := 0; i < k; i++ {
		if _, ok := forbiddenNearMisses[ranked[i]]; ok {
			hits++
		}
	}
	return float64(hits) / float64(k)
}
