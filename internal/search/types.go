package search

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// =============================================================================
// Search Intent
// =============================================================================

type SearchIntent string

const (
	IntentWebGeneral        SearchIntent = "web_general"
	IntentNews              SearchIntent = "news"
	IntentAcademic          SearchIntent = "academic"
	IntentBiomedical        SearchIntent = "biomedical"
	IntentPreprint          SearchIntent = "preprint"
	IntentBookGeneral       SearchIntent = "book_general"
	IntentBookAcademicOA    SearchIntent = "book_academic_oa"
	IntentBookPublicDomain  SearchIntent = "book_public_domain"
	IntentDOIResolve        SearchIntent = "doi_resolve"
	IntentOpenAccessResolve SearchIntent = "open_access_resolve"
)

func (s SearchIntent) String() string { return string(s) }

// RoleID is a boundary type; no canonical RoleID exists in the org kernel.
type RoleID string

// =============================================================================
// Provider IDs
// =============================================================================

type ProviderID string

const (
	ProviderBrave       ProviderID = "brave_web"
	ProviderTavily      ProviderID = "tavily"
	ProviderSerpapi     ProviderID = "serpapi"
	ProviderGoogleWeb   ProviderID = "google_web"
	ProviderOpenAlex    ProviderID = "openalex"
	ProviderArxiv       ProviderID = "arxiv"
	ProviderPubmed      ProviderID = "pubmed"
	ProviderCrossref    ProviderID = "crossref"
	ProviderUnpaywall   ProviderID = "unpaywall"
	ProviderGoogleBooks ProviderID = "google_books"
	ProviderDoab        ProviderID = "doab"
	ProviderOapen       ProviderID = "oapen"
	ProviderGutendex    ProviderID = "gutendex"
	ProviderOpenlibrary ProviderID = "openlibrary"
)

func (p ProviderID) String() string { return string(p) }

// =============================================================================
// Search Request
// =============================================================================

type SearchRequest struct {
	Query                     string
	Intent                    SearchIntent
	RoleID                    string
	MissionID                 string
	MaxResults                int
	RequireIndependentSources bool
}

// =============================================================================
// Search Result
// =============================================================================

type SearchResult struct {
	Provider     string
	Title        string
	URL          string
	Snippet      string
	Authors      []string
	PublishedAt  *time.Time
	DOI          string
	ISBN         string
	ArxivID      string
	GutenbergID  string
	OpenAccess   bool
	PeerReviewed *bool
	SourceType   string
	Score        float64
	Metadata     map[string]any
	Provenance   Provenance
	CanonicalURL string
	Abstract     string
	Publisher    string
	Year         int
	License      string
	Citations    int
	Keywords     []string
	Theme        string
	dedupOrder   int64 // internal: preserves insertion order in deduplication
}

func (r SearchResult) Validate() error {
	if strings.TrimSpace(r.Provider) == "" {
		return fmt.Errorf("%w: provider is required", ErrInvalidRequest)
	}
	if strings.TrimSpace(r.URL) == "" {
		return fmt.Errorf("%w: url is required", ErrInvalidRequest)
	}
	if strings.TrimSpace(r.Title) == "" {
		return fmt.Errorf("%w: title is required", ErrInvalidRequest)
	}
	return nil
}

// =============================================================================
// Provenance
// =============================================================================

type Provenance struct {
	RequestID     string
	MissionID     string
	RoleID        string
	Provider      string
	ProviderRoute []string
	ProviderIndex int
	Intent        SearchIntent
	RetrievedAt   time.Time
	QueryDigest   string // SHA256 hash of query for correlation without exposure
}

// =============================================================================
// Role Policy
// =============================================================================

type RolePolicy struct {
	AllowedIntents        []SearchIntent
	AllowGoogleFallback   bool
	AllowRAGPromotion     bool
	CanSubmitRAGCandidate bool
	CanPromoteToKnowledge bool
}

func DefaultRolePolicies() map[RoleID]RolePolicy {
	return map[RoleID]RolePolicy{
		RoleCEO: {
			AllowedIntents: []SearchIntent{
				IntentWebGeneral,
				IntentNews,
				IntentAcademic,
				IntentBookGeneral,
				IntentDOIResolve,
				IntentOpenAccessResolve,
			},
			AllowGoogleFallback:   true,
			AllowRAGPromotion:     true,
			CanSubmitRAGCandidate: false,
			CanPromoteToKnowledge: false,
		},
		RoleMarketing: {
			AllowedIntents: []SearchIntent{
				IntentWebGeneral,
				IntentNews,
			},
			AllowGoogleFallback:   true,
			AllowRAGPromotion:     true,
			CanSubmitRAGCandidate: false,
			CanPromoteToKnowledge: false,
		},
		RoleResearch: {
			AllowedIntents: []SearchIntent{
				IntentAcademic,
				IntentBiomedical,
				IntentPreprint,
				IntentBookGeneral,
				IntentBookAcademicOA,
				IntentBookPublicDomain,
				IntentDOIResolve,
				IntentOpenAccessResolve,
				IntentNews,
			},
			AllowGoogleFallback:   false,
			AllowRAGPromotion:     true,
			CanSubmitRAGCandidate: true,
			CanPromoteToKnowledge: false,
		},
		RoleAdversarial: {
			AllowedIntents: []SearchIntent{
				IntentWebGeneral,
				IntentNews,
			},
			AllowGoogleFallback:   false,
			AllowRAGPromotion:     false,
			CanSubmitRAGCandidate: false,
			CanPromoteToKnowledge: false,
		},
	}
}

// Role IDs
const (
	RoleCEO         RoleID = "ceo"
	RoleMarketing   RoleID = "marketing"
	RoleResearch    RoleID = "research"
	RoleAdversarial RoleID = "adversarial"
)

// =============================================================================
// Usage Ledger
// =============================================================================

type RequestMetrics struct {
	RequestID        string
	MissionID        string
	RoleID           string
	Provider         ProviderID
	Intent           SearchIntent
	CacheHit         bool
	EstimatedCostUSD float64
	Duration         time.Duration
	ResultCount      int
	Success          bool
	ErrorCode        string
	FallbackReason   FallbackReason
}

type UsageEntry struct {
	RequestID        string
	MissionID        string
	RoleID           string
	Provider         ProviderID
	Intent           SearchIntent
	CacheHit         bool
	EstimatedCostUSD float64
	Duration         time.Duration
	ResultCount      int
	Success          bool
	ErrorCode        string
	Timestamp        time.Time
}

type UsageLedger interface {
	Record(m RequestMetrics)
	Get(opts UsageLedgerOpts) ([]UsageEntry, error)
}

type UsageLedgerOpts struct {
	Since    time.Time
	Until    time.Time
	RoleID   string
	Provider ProviderID
	Intent   SearchIntent
}

// =============================================================================
// Errors
// =============================================================================

var (
	ErrPolicyDenied   = fmt.Errorf("search: policy denied")
	ErrNoProvider     = fmt.Errorf("search: no provider available")
	ErrInvalidRequest = fmt.Errorf("search: invalid request")
)

// =============================================================================
// Fallback Reason
// =============================================================================

type FallbackReason string

const (
	FallbackNone         FallbackReason = ""
	FallbackInsufficient FallbackReason = "insufficient_results"
	FallbackError        FallbackReason = "provider_error"
	FallbackEmpty        FallbackReason = "empty_results"
	FallbackSufficient   FallbackReason = "sufficient_results"
)

// =============================================================================
// Provider Interface
// =============================================================================

type Provider interface {
	ProviderID() ProviderID
	Name() string
	Supports(intent SearchIntent) bool
	Search(ctx context.Context, req SearchRequest) ([]SearchResult, error)
}
