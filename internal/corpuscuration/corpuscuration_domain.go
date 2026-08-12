// Package corpuscuration implements the governed curation stage that
// turns semantic clusters (internal/corpuscluster) into a Gold Candidate
// Set (owner decision, curation re-run): Investigación proposes, it
// never auto-approves, and this package never calls a model provider
// directly -- every model call goes through the existing governed
// pipeline (internal/modelruntime's CreateInvocationCommand + dispatch:
// authorization, context, egress, execution identity, CostGate,
// provider, accounting, audit), the same pipeline every other role's
// model call in this organization already uses. This package is the
// caller/orchestrator of that pipeline for curation-shaped requests, not
// a parallel one.
//
// Gold Candidate Set is explicitly NOT approved Knowledge (owner
// decision, section 33): P0/P1 selection here is the input to a LATER,
// separate Knowledge Candidate -> approval -> reindex -> embedding
// lifecycle (orgctl rag ingest-pdf / propose / review), never a
// shortcut around it.
package corpuscuration

import "time"

// Tier is the closed set a curated Work lands in (owner decision,
// section 16/17): never "top-1-per-cluster", never "keep 50%" -- a
// cluster can retain 1 Work or 5, whatever preserves useful knowledge,
// and Silver is never treated as "rejected" (section 5: "SILVER !=
// REJECTED").
type Tier string

const (
	TierP0Core     Tier = "P0_core"
	TierP1Strong   Tier = "P1_strong"
	TierSilverOnly Tier = "silver_only"
	// TierReviewRequired is the conservative default when curator
	// confidence is low (owner decision, section 27: "Si curator tiene
	// baja confidence: review_required. NO SILVER automatico" -- never
	// silently demote a possibly-important Work just because the model
	// call under-.performed).
	TierReviewRequired Tier = "review_required"
)

func (t Tier) Valid() bool {
	switch t {
	case TierP0Core, TierP1Strong, TierSilverOnly, TierReviewRequired:
		return true
	default:
		return false
	}
}

// Pass is which curation stage produced a record (owner decision,
// sections 12/13): Pass 1 is metadata-only (cheap, all clusters), Pass 2
// is deep review (only for Pass 1's needs_deep_review output, reads
// already-extracted text, never re-sends a whole PDF automatically).
type Pass string

const (
	Pass1Cheap Pass = "pass1_cheap"
	Pass2Deep  Pass = "pass2_deep"
)

// RubricSignals mirrors the 11-point rubric (owner decision, section
// 14) -- deterministic where this package can compute it, model-scored
// (0 if not yet scored) otherwise. Never free text: every axis is a
// bounded numeric signal so curation output stays structured and
// diffable across reruns (section 30: versioning/reproducibility).
type RubricSignals struct {
	DomainRelevance       float64 `json:"domain_relevance"`
	TechnicalDepth        float64 `json:"technical_depth"`
	EmpiricalQuality      float64 `json:"empirical_quality"`
	MethodologicalClarity float64 `json:"methodological_clarity"`
	ImplementationValue   float64 `json:"implementation_value"`
	Novelty               float64 `json:"novelty"`
	Redundancy            float64 `json:"redundancy"`
	Authority             float64 `json:"authority"`
	Freshness             float64 `json:"freshness"`
	CoverageValue         float64 `json:"coverage_value"`
	Generalizability      float64 `json:"generalizability"`
}

// WorkCuration is the resumable, versioned, per-Work curation outcome
// (owner decision, section 29/30): keyed by WorkID+ClusterID, carries
// every version identifier a later re-run needs to decide whether this
// result is still valid or must be recomputed. No chain-of-thought is
// ever stored here (section 32) -- Reasons is a short structured list of
// rubric-derived justifications, never a reasoning transcript.
type WorkCuration struct {
	WorkID    string `json:"work_id"`
	ClusterID string `json:"cluster_id"`

	Pass Pass `json:"pass"`
	Tier Tier `json:"tier"`

	Reasons   []string `json:"reasons,omitempty"`
	FillsGaps []string `json:"fills_gaps,omitempty"` // topic/domain gap IDs this Work addresses, if any

	Rubric     RubricSignals `json:"rubric"`
	Confidence float64       `json:"confidence"`

	// Versioning (owner decision, section 30): a change to any of these
	// invalidates ONLY the records that depended on it, never the whole
	// corpus's curation state.
	CurationSchemaVersion      string `json:"curation_schema_version"`
	TaxonomyVersion            string `json:"taxonomy_version"`
	ClusterAlgorithmVersion    string `json:"cluster_algorithm_version"`
	CuratorModelProvider       string `json:"curator_model_provider"`
	CuratorModelID             string `json:"curator_model_id"`
	RubricVersion              string `json:"rubric_version"`
	OrganizationContextVersion string `json:"organization_context_version"`

	// InputHash lets a re-run detect "this Work's content or cluster
	// membership changed since last curated" (owner decision, section
	// 29) without re-sending the input to compare against a stored copy.
	InputHash string `json:"input_hash"`

	ModelInvocationID int64 `json:"model_invocation_id,omitempty"`

	CuratedAt time.Time `json:"curated_at"`
}

// CurrentVersions is this package's own pinned identity for everything
// InputHash/WorkCuration's version fields need. Bumping any of these on
// a future change is what triggers selective re-curation (owner
// decision, section 30) -- callers must never hardcode these values
// themselves.
var CurrentVersions = struct {
	CurationSchema   string
	Taxonomy         string
	ClusterAlgorithm string
	Rubric           string
}{
	CurationSchema:   "v1",
	Taxonomy:         "engineering-v2", // matches internal/corpuscensus's topic vocabulary re-run (32 topics + curation re-run's extended taxonomy)
	ClusterAlgorithm: "tfidf-cosine-v1",
	Rubric:           "v1",
}
