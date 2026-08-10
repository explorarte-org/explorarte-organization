// Package corpuscensus turns a paper harvester's raw output (a SQLite
// state DB plus downloaded PDFs on disk) into a canonical, deduplicated,
// classified, resumable census -- the preprocessing stage that runs
// BEFORE any orgctl rag ingest-pdf / propose / review / reindex /
// backfill-embeddings call. It never touches internal/rag, never creates
// a knowledge candidate, and never writes to the organization's Postgres
// database: its only durable output is a local, resumable JSONL state
// file (Silver records) plus an aggregate Census this package computes
// from that state. Bulk ingestion of the accepted Works is a deliberately
// separate, later step (owner decision, Part B of the engineering corpus
// work) -- this package stops at "here is what we have and what we
// recommend," never at "and now it is in the RAG."
//
// Mirrors internal/pdfingest's own boundary discipline: this package is
// the only place besides internal/pdfingest/poppler that touches
// subprocess-based PDF/SQLite tooling, and cmd/orgd / internal/app never
// import it (enforced by scripts/check-pdfingest-fitness.sh's existing
// os/exec allowlist plus the equivalent check added for this package).
package corpuscensus

import "time"

// IdentityKind ranks how a Work's identity was established -- the order
// this type's constants are declared in IS the preference order used by
// ResolveWorkIdentity (owner decision, Part B section 4): scholarly IDs
// before normalized title, and SHA-256 last because a hash identifies one
// Artifact, never a Work (two different arXiv revisions of the same paper
// have different SHA-256 but are the same Work).
type IdentityKind string

const (
	IdentityDOI        IdentityKind = "doi"
	IdentityArxiv      IdentityKind = "arxiv"
	IdentityACL        IdentityKind = "acl"
	IdentityOpenReview IdentityKind = "openreview"
	IdentityTitleYear  IdentityKind = "title_year"
	IdentitySHA256     IdentityKind = "sha256"
)

// Decision is the closed set of outcomes a Bronze row (or a group of rows
// resolved to the same Work) can land in after preprocessing. Distinct
// states are required so a later stage can tell "this needs a human to
// look at it" (review_required, quarantine) apart from "this is fine, it
// is simply not the copy we picked" (superseded) apart from "do not ever
// ingest this into Knowledge" (invalid, low_relevance) -- collapsing any
// of these into one bucket would make the bulk-ingestion recommendation
// (report field 35) either too conservative or unsafe.
type Decision string

const (
	DecisionAccepted       Decision = "accepted"
	DecisionReviewRequired Decision = "review_required"
	DecisionDuplicate      Decision = "duplicate"
	DecisionSuperseded     Decision = "superseded"
	DecisionInvalid        Decision = "invalid"
	DecisionEncrypted      Decision = "encrypted"
	DecisionTimeout        Decision = "timeout"
	DecisionLowRelevance   Decision = "low_relevance"
	DecisionQuarantine     Decision = "quarantine"
)

func (d Decision) Valid() bool {
	switch d {
	case DecisionAccepted, DecisionReviewRequired, DecisionDuplicate, DecisionSuperseded,
		DecisionInvalid, DecisionEncrypted, DecisionTimeout, DecisionLowRelevance, DecisionQuarantine:
		return true
	default:
		return false
	}
}

// TimeoutPolicy distinguishes "a slow but valid PDF" from "give up" (owner
// decision, Part B section 10): a genuinely large/complex PDF like
// 2408.06292v3.pdf (186 pages, ~2m32s) must never be quarantined as
// invalid just because it is slow. retryable_timeout means one retry at a
// longer bound is worth trying; hard_timeout means the retry also timed
// out and this package stops trying (never unbounded processing).
type TimeoutPolicy string

const (
	TimeoutNone      TimeoutPolicy = "normal"
	TimeoutRetryable TimeoutPolicy = "retryable_timeout"
	TimeoutHard      TimeoutPolicy = "hard_timeout"
)

// AuthorityTier is a coarse, deterministic proxy for source authority
// (owner decision, Part B section 7). Every SilverRecord this package
// produces comes from the harvester's `papers` table -- i.e. it already
// passed the harvester's own resolution to a real scholarly work -- so in
// practice this package only ever assigns Tier A or Tier B; catalogs
// (Awesome-list READMEs) are discovery sources, never become
// SilverRecords, and are reported separately as DiscoveryCatalogs in the
// Census, never given a tier of their own.
type AuthorityTier string

const (
	TierA AuthorityTier = "A" // has a confirmed peer-reviewed venue/publication
	TierB AuthorityTier = "B" // scholarly ID (DOI/arXiv/ACL/OpenReview) but no confirmed venue
)

// QualitySignals separates deterministic signals (computed here, cheap,
// reproducible) from semantic signals (owner decision, Part B section 6:
// "no usar un unico LLM score como verdad" -- a model may assist
// classification later, but never autoapproves Knowledge). This package
// deliberately does NOT call any model: MethodologicalSignal and Novelty
// are left at 0 with SemanticScoringApplied=false, so a report reader can
// tell "not yet scored" apart from "scored zero."
type QualitySignals struct {
	DomainRelevance        float64 `json:"domain_relevance"`
	SourceAuthority        float64 `json:"source_authority"`
	MethodologicalSignal   float64 `json:"methodological_signal"`
	Novelty                float64 `json:"novelty"`
	Redundancy             float64 `json:"redundancy"`
	SemanticScoringApplied bool    `json:"semantic_scoring_applied"`
}

// Score is an ordering/recommendation aid only (owner decision: "Quality
// Score solo ordena/recomienda revision. NO sustituye governance") -- it
// is never read by any authorization or approval gate.
func (q QualitySignals) Score() float64 {
	return q.DomainRelevance + q.SourceAuthority + q.MethodologicalSignal + q.Novelty - q.Redundancy
}

// PDFValidation is this package's own record of what Poppler (via
// internal/pdfingest.Processor, reused unmodified) found -- deliberately
// NOT the same type as pdfingest.Result: this package needs a compact,
// JSON-serializable, checkpointable summary, not the actual page PDF
// bytes (which stay in memory only long enough to be discarded here --
// this package never uploads anything to Object Storage or proposes a
// candidate; that is orgctl rag ingest-pdf's job, later, per document,
// only for Works this census marks accepted).
type PDFValidation struct {
	Valid            bool          `json:"valid"`
	Encrypted        bool          `json:"encrypted"`
	Pages            int           `json:"pages"`
	EmptyTextPages   int           `json:"empty_text_pages"`
	ReferencesPages  int           `json:"references_pages"`
	ParserName       string        `json:"parser_name,omitempty"`
	ParserVersion    string        `json:"parser_version,omitempty"`
	ParserStatus     string        `json:"parser_status"`
	TimeoutPolicy    TimeoutPolicy `json:"timeout_policy"`
	QuarantineReason string        `json:"quarantine_reason,omitempty"`
}

// SourceRef mirrors one row of the harvester's own `sources` table:
// exactly which discovery catalog/README/line first surfaced this
// canonical_id, kept as an array because the same Work is frequently
// discovered via more than one catalog (owner decision, Part B section 4:
// "una obra puede tener multiples artifacts" and multiple discovery
// paths; no extra epistemic weight for appearing in more catalogs, but
// the provenance is kept).
type SourceRef struct {
	Collection string `json:"collection"`
	RepoName   string `json:"repo_name"`
	SourceFile string `json:"source_file"`
	RawURL     string `json:"raw_url"`
}

// SilverRecord is this package's normalized, durable, resumable unit of
// state -- one per Bronze artifact row from the harvester's `papers`
// table. Bronze itself (the original PDF bytes on disk, the harvester's
// SQLite DB) is never modified; this record is the ONLY thing this
// package writes, and it writes it to a local JSONL state file, never to
// the organization's Postgres.
type SilverRecord struct {
	WorkID     string `json:"work_id"`
	ArtifactID string `json:"artifact_id"` // the harvester's own canonical_id for this row

	SHA256 string `json:"sha256"`

	Title   string   `json:"title"`
	Authors []string `json:"authors,omitempty"`
	Year    int      `json:"year,omitempty"`

	DOI          string `json:"doi,omitempty"`
	ArxivID      string `json:"arxiv_id,omitempty"`
	ACLID        string `json:"acl_id,omitempty"`
	OpenReviewID string `json:"openreview_id,omitempty"`

	Language   string `json:"language"`
	SourceType string `json:"source_type"` // "paper" | "evaluation" (owner decision, section 8)

	Collection      string      `json:"collection"`
	DiscoveredVia   []SourceRef `json:"discovered_via,omitempty"`
	CanonicalSource string      `json:"canonical_source,omitempty"`

	Topics        []string       `json:"topics,omitempty"`
	AuthorityTier AuthorityTier  `json:"authority_tier"`
	Quality       QualitySignals `json:"quality"`

	PDF PDFValidation `json:"pdf"`

	Decision            Decision `json:"decision"`
	DecisionReason      string   `json:"decision_reason,omitempty"`
	IsCanonicalArtifact bool     `json:"is_canonical_artifact"`
	SupersededBy        string   `json:"superseded_by,omitempty"` // WorkID's canonical ArtifactID, when Decision==superseded

	SeenInSelfImprovingAgentsSeed bool `json:"seen_in_self_improving_agents_seed,omitempty"`

	ArtifactBytes int64 `json:"artifact_bytes,omitempty"`

	ProcessedAt time.Time `json:"processed_at"`
}
