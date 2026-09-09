package search

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

type RoutingTable struct {
	WebGeneral        Route
	News              Route
	Academic          Route
	Biomedical        Route
	Preprint          Route
	BookGeneral       Route
	BookAcademicOA    Route
	BookPublicDomain  Route
	DOIResolve        Route
	OpenAccessResolve Route
}

type Route []ProviderID

// DefaultRoutingTable returns the V1 declarative routing configuration.
// Invariants:
// - GoogleWeb is LAST for web_general/news (expensive/quota-limited fallback)
// - OpenAlex does NOT appear in web_general or BookGeneral
// - OpenAlex only appears in Academic, Biomedical, Preprint, DOIResolve, OpenAccessResolve
func DefaultRoutingTable() RoutingTable {
	return RoutingTable{
		// Web: BraveWeb primary, then Tavily, Serpapi, GoogleWeb as fallback
		WebGeneral: Route{ProviderBrave, ProviderTavily, ProviderSerpapi, ProviderGoogleWeb},
		// News: Tavily primary (best for news), Serpapi, GoogleWeb fallback
		News: Route{ProviderTavily, ProviderSerpapi, ProviderGoogleWeb},
		// Academic: OpenAlex primary for structured academic search
		Academic: Route{ProviderOpenAlex, ProviderArxiv, ProviderCrossref},
		// Biomedical: PubMed primary, OpenAlex for broader coverage
		Biomedical: Route{ProviderPubmed, ProviderOpenAlex},
		// Preprint: arXiv primary for preprints
		Preprint: Route{ProviderArxiv, ProviderOpenAlex},
		// BookGeneral: GoogleBooks primary, OpenLibrary as fallback
		// OpenAlex does NOT participate in general book search
		BookGeneral: Route{ProviderGoogleBooks, ProviderOpenlibrary},
		// BookAcademicOA: DOAB and OAPEN for open access academic books
		BookAcademicOA: Route{ProviderDoab, ProviderOapen},
		// BookPublicDomain: Gutendex for public domain books
		BookPublicDomain: Route{ProviderGutendex},
		// DOIResolve: Crossref primary, Unpaywall for OA resolution
		DOIResolve: Route{ProviderCrossref, ProviderUnpaywall},
		// OpenAccessResolve: Unpaywall primary
		OpenAccessResolve: Route{ProviderUnpaywall, ProviderOpenAlex},
	}
}

func (t RoutingTable) RouteFor(intent SearchIntent) (Route, error) {
	switch intent {
	case IntentWebGeneral:
		return t.WebGeneral, nil
	case IntentNews:
		return t.News, nil
	case IntentAcademic:
		return t.Academic, nil
	case IntentBiomedical:
		return t.Biomedical, nil
	case IntentPreprint:
		return t.Preprint, nil
	case IntentBookGeneral:
		return t.BookGeneral, nil
	case IntentBookAcademicOA:
		return t.BookAcademicOA, nil
	case IntentBookPublicDomain:
		return t.BookPublicDomain, nil
	case IntentDOIResolve:
		return t.DOIResolve, nil
	case IntentOpenAccessResolve:
		return t.OpenAccessResolve, nil
	default:
		return nil, fmt.Errorf("search: no route for intent %q", intent)
	}
}

type Registry struct {
	mu        sync.RWMutex
	providers map[ProviderID]Provider
}

func NewRegistry() *Registry {
	return &Registry{providers: make(map[ProviderID]Provider)}
}

func (r *Registry) Register(id ProviderID, p Provider) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.providers[id]; exists {
		return fmt.Errorf("search: provider %q already registered", id)
	}
	r.providers[id] = p
	return nil
}

func (r *Registry) Get(id ProviderID) Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.providers[id]
}

type Router struct {
	registry      *Registry
	routingTable  RoutingTable
	policies      map[RoleID]RolePolicy
	cache         Cache
	usage         UsageLedger
	sufficiency   SufficiencyEvaluator
	logger        Logger
	clock         func() time.Time
	requestIDFunc func() string
}

type RouterConfig struct {
	Table         RoutingTable
	Policies      map[RoleID]RolePolicy
	Cache         Cache
	Usage         UsageLedger
	Sufficiency   SufficiencyEvaluator
	Logger        Logger
	Clock         func() time.Time
	RequestIDFunc func() string
}

func NewRouter(cfg RouterConfig) (*Router, error) {
	if cfg.Policies == nil {
		cfg.Policies = DefaultRolePolicies()
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	if cfg.RequestIDFunc == nil {
		cfg.RequestIDFunc = newRequestID
	}
	if cfg.Logger == nil {
		cfg.Logger = NoopLogger{}
	}
	if cfg.Table.WebGeneral == nil {
		cfg.Table = DefaultRoutingTable()
	}
	return &Router{
		registry:      NewRegistry(),
		routingTable:  cfg.Table,
		policies:      cfg.Policies,
		cache:         cfg.Cache,
		usage:         cfg.Usage,
		sufficiency:   cfg.Sufficiency,
		logger:        cfg.Logger,
		clock:         cfg.Clock,
		requestIDFunc: cfg.RequestIDFunc,
	}, nil
}

type SearchResponse struct {
	RequestID     string
	Intent        SearchIntent
	RoleID        string
	ProviderRoute Route
	Results       []SearchResult
	CacheHit      bool
	CompletedAt   time.Time
	Metrics       []RequestMetrics
}

func (r *Router) Search(ctx context.Context, req SearchRequest) (SearchResponse, error) {
	rid := r.requestIDFunc()

	roleID := RoleID(req.RoleID)
	policy, ok := r.policies[roleID]
	if !ok {
		return SearchResponse{}, fmt.Errorf("%w: role %q not found", ErrPolicyDenied, req.RoleID)
	}
	if !intentAllowed(req.Intent, policy.AllowedIntents) {
		return SearchResponse{}, fmt.Errorf("%w: intent %q not allowed for role %q", ErrPolicyDenied, req.Intent, req.RoleID)
	}

	if r.cache != nil {
		entry, ok := r.cache.Get(req, r.clock())
		if ok {
			return SearchResponse{
				RequestID:     rid,
				Intent:        req.Intent,
				RoleID:        req.RoleID,
				ProviderRoute: nil,
				Results:       entry.Results,
				CacheHit:      true,
				CompletedAt:   r.clock().UTC(),
			}, nil
		}
	}

	route, err := r.routingTable.RouteFor(req.Intent)
	if err != nil {
		return SearchResponse{}, err
	}

	var allResults []SearchResult
	var metrics []RequestMetrics
	var lastErr error

	for i, pid := range route {
		provider := r.registry.Get(pid)
		if provider == nil {
			continue
		}
		if !provider.Supports(req.Intent) {
			continue
		}

		r.logger.Log(Event{Kind: EventProviderStarted, RequestID: rid, Provider: string(pid), Intent: string(req.Intent)})

		start := r.clock()
		results, err := provider.Search(ctx, req)
		duration := r.clock().Sub(start)

		r.logger.Log(Event{Kind: EventProviderCompleted, RequestID: rid, Provider: string(pid), Intent: string(req.Intent), Results: len(results)})

		m := RequestMetrics{
			RequestID:   rid,
			MissionID:   req.MissionID,
			RoleID:      req.RoleID,
			Provider:    pid,
			Intent:      req.Intent,
			Duration:    duration,
			ResultCount: len(results),
			Success:     err == nil && len(results) > 0,
		}
		if err != nil {
			m.ErrorCode = err.Error()
		}
		metrics = append(metrics, m)

		if err != nil {
			lastErr = err
			r.logger.Log(Event{Kind: EventProviderFailed, RequestID: rid, Provider: string(pid), Intent: string(req.Intent), Error: err.Error()})
			continue
		}
		if len(results) == 0 {
			r.logger.Log(Event{Kind: EventProviderFailed, RequestID: rid, Provider: string(pid), Intent: string(req.Intent), Error: "empty results"})
			continue
		}

		for j := range results {
			results[j].Provenance = Provenance{
				RequestID:     rid,
				MissionID:     req.MissionID,
				RoleID:        req.RoleID,
				Provider:      string(pid),
				ProviderRoute: route.Strings(),
				ProviderIndex: i,
				Intent:        req.Intent,
				RetrievedAt:   r.clock().UTC(),
				QueryDigest:   digestQuery(req.Query),
			}
		}

		allResults = append(allResults, results...)

		if r.sufficiency != nil {
			out := r.sufficiency.Evaluate(SufficiencyInput{
				Results:      results,
				Intent:       req.Intent,
				RequireMulti: req.RequireIndependentSources,
			})
			if out.Sufficient {
				r.logger.Log(Event{Kind: EventCompleted, RequestID: rid, Intent: string(req.Intent), Results: len(allResults)})
				break
			} else {
				r.logger.Log(Event{Kind: EventProviderInsufficient, RequestID: rid, Provider: string(pid), Intent: string(req.Intent), Results: len(results)})
			}
		} else if req.MaxResults > 0 && len(allResults) >= req.MaxResults {
			break
		}
	}

	// Record usage metrics BEFORE checking if all providers failed
	// This ensures we always record metrics even when no results are returned
	if r.usage != nil {
		for _, m := range metrics {
			r.usage.Record(m)
		}
	}

	if len(allResults) == 0 && lastErr != nil {
		return SearchResponse{}, fmt.Errorf("%w: all providers failed", ErrNoProvider)
	}

	if len(allResults) > 1 {
		dedupe := NewDeduplicator(DedupeOptions{PreferFirstSeen: true})
		merged, _ := dedupe.MergeWithResults(allResults)
		if len(merged) > 0 {
			allResults = merged[0]
		}
	}

	if req.MaxResults > 0 && len(allResults) > req.MaxResults {
		allResults = allResults[:req.MaxResults]
	}

	if r.cache != nil {
		r.cache.Put(req, req, CacheEntry{
			Results:  allResults,
			StoredAt: r.clock().UTC(),
			Ttl:      DefaultTTLs[req.Intent],
		})
	}

	return SearchResponse{
		RequestID:     rid,
		Intent:        req.Intent,
		RoleID:        req.RoleID,
		ProviderRoute: route,
		Results:       allResults,
		CacheHit:      false,
		CompletedAt:   r.clock().UTC(),
		Metrics:       metrics,
	}, nil
}

func (r Route) Strings() []string {
	out := make([]string, len(r))
	for i, id := range r {
		out[i] = string(id)
	}
	return out
}

func intentAllowed(intent SearchIntent, allowed []SearchIntent) bool {
	for _, a := range allowed {
		if a == intent {
			return true
		}
	}
	return false
}

func digestQuery(q string) string {
	sum := sha256.Sum256([]byte(q))
	return hex.EncodeToString(sum[:])
}

func newRequestID() string {
	var b [16]byte
	now := time.Now().UTC().UnixNano()
	for i := 0; i < 8; i++ {
		b[i] = byte(now >> (8 * i))
	}
	sum := sha256.Sum256(b[:])
	return hex.EncodeToString(sum[:16])
}
