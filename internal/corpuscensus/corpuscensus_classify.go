package corpuscensus

import (
	"regexp"
	"strings"
)

// collectionTopics maps a harvester collection name (the topic the
// harvester was TOLD to search for -- rag, context, memory, symbolic,
// efficiency) to the organization's topic vocabulary (Part B section 1).
// This is deliberately the only topic signal this package computes: it
// is a real, direct fact (the collection the harvester's own catalog
// search assigned), not an inferred one. A document is never forced into
// exactly one topic (owner decision, section 1: "no necesitas forzar un
// documento a un unico topic").
var collectionTopics = map[string][]string{
	"rag":        {"rag", "information-retrieval", "semantic-search", "embeddings"},
	"context":    {"context-engineering", "long-context", "context-compression", "token-efficiency"},
	"memory":     {"agent-memory", "memory-os"},
	"symbolic":   {"symbolic-ai", "prolog", "formal-reasoning", "knowledge-representation", "ontologies"},
	"efficiency": {"token-efficiency", "ml-systems"},
}

func TopicsForCollection(collection string) []string {
	if topics, ok := collectionTopics[collection]; ok {
		out := make([]string, len(topics))
		copy(out, topics)
		return out
	}
	return nil
}

// ClassifyAuthorityTier is the deterministic half of Part B section 7.
// Every SilverRecord here already comes from the harvester's `papers`
// table (a resolved scholarly work, not a catalog entry), so only Tier A
// (confirmed venue/publication) and Tier B (scholarly ID present, venue
// unconfirmed -- typically an arXiv preprint) are ever assigned by this
// package; Tier C/D (surveys, Awesome lists, blogs) apply conceptually to
// the discovery catalogs themselves, which never become SilverRecords.
func ClassifyAuthorityTier(p BronzePaper) AuthorityTier {
	if strings.TrimSpace(p.str(p.Venue)) != "" {
		return TierA
	}
	return TierB
}

// DeterministicQuality computes only the signals this package can compute
// without a model call (owner decision, section 6): SourceAuthority from
// the tier, DomainRelevance as a direct 1.0 (the harvester's collection
// assignment IS the relevance signal here -- it was discovered searching
// specifically for that topic), Redundancy from whether this artifact's
// Work has more than one known artifact. MethodologicalSignal and Novelty
// are left unscored (SemanticScoringApplied=false) -- this package never
// pretends to have run a model it did not run.
func DeterministicQuality(tier AuthorityTier, workGroupSize int) QualitySignals {
	authority := 0.7
	if tier == TierA {
		authority = 1.0
	}
	redundancy := 0.0
	if workGroupSize > 1 {
		redundancy = 0.3
	}
	return QualitySignals{
		DomainRelevance:        1.0,
		SourceAuthority:        authority,
		MethodologicalSignal:   0,
		Novelty:                0,
		Redundancy:             redundancy,
		SemanticScoringApplied: false,
	}
}

var evaluationDatasetPattern = regexp.MustCompile(`(?i)\b(hotpotqa|musique|2wikimultihopqa|natural\s*questions|triviaqa|squad\b|ms\s*marco|beir\b|mteb\b|gold\s+evaluation\s+set|benchmark\s+dataset)\b`)

// ClassifySourceType flags evaluation/benchmark datasets so they are
// excluded from the Knowledge namespace (owner decision, section 8: "NO
// INGESTARLOS A KNOWLEDGE" -- avoid evaluation leakage). This is a
// best-effort title/venue match against known benchmark names, not
// exhaustive; anything it flags gets source_type=evaluation and is kept
// entirely out of accepted/review_required.
func ClassifySourceType(title, venue string) string {
	if evaluationDatasetPattern.MatchString(title) || evaluationDatasetPattern.MatchString(venue) {
		return "evaluation"
	}
	return "paper"
}

var referencesHeadingPattern = regexp.MustCompile(`(?im)^\s*(references|bibliography|works\s+cited)\s*$`)
var citationDensityPattern = regexp.MustCompile(`(?i)\b(19|20)\d{2}\b|arxiv:\s*\d{4}\.\d{4,5}|et al\.|\bdoi:`)

// LooksLikeReferencesPage is a best-effort, non-authoritative heuristic
// (owner decision, section 9: references pages are marked, never
// deleted, and this does not change productive retrieval scoring in this
// stage). A page qualifies if it has an explicit References/Bibliography
// heading, OR if it is in the trailing portion of the document AND has a
// high density of citation-shaped tokens (years-in-parens, arXiv IDs,
// "et al.", "doi:").
func LooksLikeReferencesPage(pageNumber, totalPages int, text string) bool {
	if referencesHeadingPattern.MatchString(text) {
		return true
	}
	if totalPages <= 0 {
		return false
	}
	inTrailingPortion := float64(pageNumber) >= 0.7*float64(totalPages)
	if !inTrailingPortion {
		return false
	}
	matches := citationDensityPattern.FindAllStringIndex(text, -1)
	if len(text) == 0 {
		return false
	}
	density := float64(len(matches)) / (float64(len(text)) / 1000.0) // matches per 1000 chars
	return density >= 3.0
}
