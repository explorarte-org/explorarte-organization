package search

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// =============================================================================
// Finding Policy V2
//
// V1 classified durable_candidate purely by counting new evidence (>=3).
// Three fresh URLs from the same aggregator are NOT durable knowledge. V2
// separates the four signals the spec requires and centralizes thresholds:
//
//	Novelty          — evidence unseen within the topic's window (host)
//	EvidenceQuality  — canonical/structured provenance signal (host)
//	Independence     — distinct sources (domain-level, host)
//	Durability       — atemporal vs temporal claim shape (host)
//
// No ML. Deterministic, testable, explicable. The LLM may SUGGEST, the host
// DECIDES (see research_querygen.go for the suggestion boundary).
// =============================================================================

// FindingPolicyConfig centralizes every durable-candidate threshold.
type FindingPolicyConfig struct {
	// MinDurableEvidence: minimum distinct novel evidence refs.
	MinDurableEvidence int
	// MinIndependentSources: minimum distinct domains among novel evidence.
	MinIndependentSources int
	// MinEvidenceQuality: minimum mean quality score (0..1).
	MinEvidenceQuality float64
	// ImportantEvidence: novel evidence count that elevates informative -> important.
	ImportantEvidence int
}

// DefaultFindingPolicyConfig returns conservative V2 defaults.
func DefaultFindingPolicyConfig() FindingPolicyConfig {
	return FindingPolicyConfig{
		MinDurableEvidence:    3,
		MinIndependentSources: 2,
		MinEvidenceQuality:    0.6,
		ImportantEvidence:     2,
	}
}

// Validate enforces sane thresholds.
func (c FindingPolicyConfig) Validate() error {
	if c.MinDurableEvidence < 1 || c.MinDurableEvidence > 100 {
		return fmt.Errorf("%w: min durable evidence outside 1..100", ErrInvalidRequest)
	}
	if c.MinIndependentSources < 1 || c.MinIndependentSources > 50 {
		return fmt.Errorf("%w: min independent sources outside 1..50", ErrInvalidRequest)
	}
	if c.MinEvidenceQuality < 0 || c.MinEvidenceQuality > 1 {
		return fmt.Errorf("%w: min evidence quality outside 0..1", ErrInvalidRequest)
	}
	if c.ImportantEvidence < 1 {
		return fmt.Errorf("%w: important evidence threshold must be >= 1", ErrInvalidRequest)
	}
	return nil
}

// FindingDecision is the host's classification verdict with explanations.
type FindingDecision struct {
	Classification FindingClassification
	// DurableEligible reports whether the evidence met the durable bar
	// (even when the final classification is important, e.g. temporal).
	DurableEligible bool
	// Reasons documents each gate's outcome for observability.
	Reasons []string
}

// FindingPolicy evaluates novel evidence deterministically.
type FindingPolicy struct {
	Cfg FindingPolicyConfig
}

// NewFindingPolicy validates config and returns the policy.
func NewFindingPolicy(cfg FindingPolicyConfig) (*FindingPolicy, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &FindingPolicy{Cfg: cfg}, nil
}

// Evaluate classifies a batch of novel evidence. `novel` holds ONLY the
// evidence refs the NoveltyEvaluator accepted (novelty is a precondition,
// not part of this decision).
func (p *FindingPolicy) Evaluate(novel []EvidenceRef, results []SearchResult) FindingDecision {
	decision := FindingDecision{Classification: FindingInformative}
	if len(novel) == 0 {
		decision.Reasons = append(decision.Reasons, "no_novel_evidence")
		decision.Classification = FindingIrrelevant
		return decision
	}

	// --- Independence: distinct domains (aggregator reposts collapse) ------
	domains := distinctDomains(novel)
	decision.Reasons = append(decision.Reasons,
		fmt.Sprintf("independent_sources=%d", len(domains)))

	// --- Evidence quality: provenance-weighted mean ------------------------
	total := 0.0
	for _, ref := range novel {
		total += evidenceQualityScore(ref, results)
	}
	meanQuality := total / float64(len(novel))
	decision.Reasons = append(decision.Reasons,
		fmt.Sprintf("evidence_quality=%.2f", meanQuality))

	// --- Durability: atemporal claim shape vs temporal fact ----------------
	durableShape, temporalShape := durabilityShape(novel, results)
	decision.Reasons = append(decision.Reasons,
		fmt.Sprintf("durable_shape=%v temporal_shape=%v", durableShape, temporalShape))

	// --- Classification gates ----------------------------------------------
	durableEligible := len(novel) >= p.Cfg.MinDurableEvidence &&
		len(domains) >= p.Cfg.MinIndependentSources &&
		meanQuality >= p.Cfg.MinEvidenceQuality &&
		durableShape
	decision.DurableEligible = durableEligible

	switch {
	case durableEligible:
		decision.Classification = FindingDurableCandidate
		decision.Reasons = append(decision.Reasons, "durable_gates_all_passed")
	case temporalShape && len(novel) >= p.Cfg.ImportantEvidence:
		// Temporal facts (pricing change, outage, release) are important
		// intelligence even though they are not durable knowledge.
		decision.Classification = FindingImportant
		decision.Reasons = append(decision.Reasons, "temporal_fact_promoted_to_important_not_durable")
	case len(novel) >= p.Cfg.ImportantEvidence:
		decision.Classification = FindingImportant
		decision.Reasons = append(decision.Reasons, "evidence_count_promoted_to_important")
	default:
		decision.Classification = FindingInformative
		decision.Reasons = append(decision.Reasons, "informative_only")
	}
	return decision
}

// evidenceQualityScore scores ONE evidence ref from its provenance, 0..1.
//
// Strong signals (additive):
//   - structured canonical IDs (DOI/arXiv/ISBN): these ARE canonical
//     identification, not provider reputation;
//   - SourceType from provider metadata (paper/preprint/book/documentation);
//   - canonical provider metadata presence (publisher/journal/license).
//
// Weak: bare URL+snippet with no structured metadata. Aggregator/repost
// markers in SourceType are penalized.
func evidenceQualityScore(ref EvidenceRef, results []SearchResult) float64 {
	score := 0.15 // bare URL baseline
	// Structured IDs are the strongest canonical signal.
	if ref.DOI != "" || ref.ArxivID != "" || ref.ISBN != "" {
		score += 0.45
	}
	// Locate the matching result for SourceType/metadata.
	for _, r := range results {
		if !sameEvidence(r, ref) {
			continue
		}
		switch strings.ToLower(r.SourceType) {
		case "paper", "journal_article", "preprint", "book", "documentation", "primary_source":
			score += 0.25
		case "aggregator", "repost", "social":
			score -= 0.2
		}
		if r.Publisher != "" || r.License != "" || r.Year > 0 {
			score += 0.15
		}
		break
	}
	if score < 0 {
		score = 0
	}
	if score > 1 {
		score = 1
	}
	return score
}

// durabilityShape reports whether the evidence looks atemporal (papers,
// books, specs — durable candidates) or temporal (pricing/outage/release
// news — important but not durable).
func durabilityShape(novel []EvidenceRef, results []SearchResult) (durableShape, temporalShape bool) {
	temporalKeywords := []string{"pricing", "price", "outage", "release notes", "changelog", "tariff", "free tier"}
	for _, ref := range novel {
		if ref.DOI != "" || ref.ArxivID != "" || ref.ISBN != "" {
			// Canonical scholarly/book identity is durable by shape.
			durableShape = true
			continue
		}
		haystack := strings.ToLower(ref.Title + " " + ref.URL)
		isTemporal := false
		for _, kw := range temporalKeywords {
			if strings.Contains(haystack, kw) {
				isTemporal = true
				break
			}
		}
		if isTemporal {
			temporalShape = true
			continue
		}
		// Locate SourceType metadata for the remaining evidence.
		for _, r := range results {
			if !sameEvidence(r, ref) {
				continue
			}
			switch strings.ToLower(r.SourceType) {
			case "paper", "journal_article", "book", "documentation", "primary_source":
				durableShape = true
			case "news", "aggregator", "repost":
				temporalShape = true
			}
			break
		}
	}
	return durableShape, temporalShape
}

// distinctDomains returns the count of distinct registrable domains.
func distinctDomains(refs []EvidenceRef) []string {
	seen := map[string]bool{}
	out := make([]string, 0)
	for _, ref := range refs {
		domain := domainOf(ref.URL)
		if domain == "" || seen[domain] {
			continue
		}
		seen[domain] = true
		out = append(out, domain)
	}
	sort.Strings(out)
	return out
}

// domainOf extracts a lowercase host, stripping www.
func domainOf(rawURL string) string {
	if rawURL == "" {
		return ""
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" {
		// Tolerate schemeless URLs.
		if !strings.Contains(rawURL, "://") {
			parsed, err = url.Parse("https://" + rawURL)
			if err != nil || parsed.Host == "" {
				return ""
			}
		} else {
			return ""
		}
	}
	host := strings.ToLower(parsed.Host)
	if idx := strings.LastIndex(host, ":"); idx >= 0 {
		host = host[:idx]
	}
	host = strings.TrimPrefix(host, "www.")
	return host
}

// sameEvidence matches a SearchResult to its EvidenceRef.
func sameEvidence(r SearchResult, ref EvidenceRef) bool {
	url := r.CanonicalURL
	if url == "" {
		url = r.URL
	}
	if ref.URL != "" && url == ref.URL {
		return true
	}
	if ref.DOI != "" && r.DOI == ref.DOI {
		return true
	}
	if ref.ArxivID != "" && r.ArxivID == ref.ArxivID {
		return true
	}
	if ref.ISBN != "" && r.ISBN == ref.ISBN {
		return true
	}
	return false
}
