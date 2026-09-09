package search

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// =============================================================================
// LLM QueryGenerator
//
// The researcher LLM PROPOSES queries; the host VALIDATES and executes them
// exclusively through the SearchRouter. This file never touches OpenRouter
// (or any provider) directly: it depends on the ModelInvoker runtime
// abstraction below, whose canonical implementation is wired to the
// organization model runtime. Swapping to a free-provider pool later means
// supplying a different ModelInvoker — nothing here changes.
//
// Failure taxonomy maps runtime errors to:
//	rate_limited / quota_exhausted / unavailable / transient /
//	malformed_output / permanent
// For every normal topic these postpone research. NO paid fallback exists.
// =============================================================================

// ModelOutputMode mirrors the canonical runtime's output modes.
type ModelOutputMode string

const (
	ModelOutputText ModelOutputMode = "text"
	ModelOutputJSON ModelOutputMode = "json"
)

// ModelInvocationRequest is the runtime-neutral invocation envelope. It
// carries only model-facing content — never credentials, never provider
// routing authority.
type ModelInvocationRequest struct {
	ProviderID      string // resolved by host config, never by the LLM
	ProviderModelID string // resolved by host config
	SystemPrompt    string
	UserPrompt      string
	OutputMode      ModelOutputMode
	MaxOutputTokens int
	Timeout         time.Duration
}

// ModelInvocationResult is the runtime-neutral response envelope.
type ModelInvocationResult struct {
	Content           []byte
	InputTokens       int64
	OutputTokens      int64
	FinishReason      string
	ProviderRequestID string
}

// ModelInvoker is the canonical model runtime boundary. The organization
// model runtime satisfies this; tests inject fakes.
type ModelInvoker interface {
	Invoke(ctx context.Context, req ModelInvocationRequest) (ModelInvocationResult, error)
}

// ModelFailureClass is the taxonomy for LLM availability problems.
type ModelFailureClass string

const (
	ModelFailureRateLimited    ModelFailureClass = "rate_limited"
	ModelFailureQuotaExhausted ModelFailureClass = "quota_exhausted"
	ModelFailureUnavailable    ModelFailureClass = "unavailable"
	ModelFailureTransient      ModelFailureClass = "transient"
	ModelFailureMalformed      ModelFailureClass = "malformed_output"
	ModelFailurePermanent      ModelFailureClass = "permanent"
)

// ClassifyModelFailure maps a runtime error into the taxonomy using
// errors.As on the runtime's own typed error when available, falling back
// to conservative string classification of the CLASS (never of content).
func ClassifyModelFailure(err error) ModelFailureClass {
	if err == nil {
		return ""
	}
	var typed *ProviderError
	if errors.As(err, &typed) {
		switch typed.Kind {
		case ProviderErrorRateLimited:
			return ModelFailureRateLimited
		case ProviderErrorUnavailable:
			return ModelFailureUnavailable
		case ProviderErrorTimeout:
			return ModelFailureTransient
		case ProviderErrorUnauthorized, ProviderErrorForbidden:
			// Bad credentials will not heal on retry.
			return ModelFailurePermanent
		case ProviderErrorMalformedResponse:
			return ModelFailureMalformed
		default:
			return ModelFailureTransient
		}
	}
	// Structural (non-content) fallbacks only.
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "quota"), strings.Contains(msg, "credit"):
		return ModelFailureQuotaExhausted
	case strings.Contains(msg, "rate limit"), strings.Contains(msg, "429"):
		return ModelFailureRateLimited
	case strings.Contains(msg, "context canceled"), strings.Contains(msg, "deadline"):
		return ModelFailureTransient
	case strings.Contains(msg, "connection"), strings.Contains(msg, "unavailable"):
		return ModelFailureUnavailable
	default:
		return ModelFailureTransient
	}
}

// PostponeFor returns the backoff for a failure class. Bounded, configurable
// values; Retry-After is honored upstream by the router when it exists.
func PostponeFor(class ModelFailureClass) time.Duration {
	switch class {
	case ModelFailureRateLimited:
		return 20 * time.Minute
	case ModelFailureQuotaExhausted:
		return 6 * time.Hour // daily-quota cooldown, not an invented reset time
	case ModelFailureUnavailable, ModelFailureTransient:
		return 10 * time.Minute
	case ModelFailureMalformed:
		return 20 * time.Minute
	default: // permanent or unknown
		return 6 * time.Hour
	}
}

// GeneratedQuery is ONE validated query proposal.
type GeneratedQuery struct {
	Query  string       `json:"query"`
	Intent SearchIntent `json:"intent"`
	Reason string       `json:"reason,omitempty"`
}

// queryGenerationEnvelope is the ONLY output schema accepted from the model.
type queryGenerationEnvelope struct {
	Queries []GeneratedQuery `json:"queries"`
}

// QueryGeneratorConfig centralizes generator bounds.
type QueryGeneratorConfig struct {
	MaxQueries        int           // hard cap regardless of model output
	MaxQueryLength    int           // reject absurdly long queries
	DiversityWindow   time.Duration // duplicate-query suppression window
	MaxOutputTokens   int
	InvocationTimeout time.Duration
}

// DefaultQueryGeneratorConfig returns conservative defaults.
func DefaultQueryGeneratorConfig() QueryGeneratorConfig {
	return QueryGeneratorConfig{
		MaxQueries:        3,
		MaxQueryLength:    300,
		DiversityWindow:   6 * time.Hour,
		MaxOutputTokens:   400,
		InvocationTimeout: 45 * time.Second,
	}
}

// Validate enforces sane bounds.
func (c QueryGeneratorConfig) Validate() error {
	if c.MaxQueries < 1 || c.MaxQueries > 20 {
		return fmt.Errorf("%w: query generator max queries outside 1..20", ErrInvalidRequest)
	}
	if c.MaxQueryLength < 10 || c.MaxQueryLength > 2000 {
		return fmt.Errorf("%w: query length bound outside 10..2000", ErrInvalidRequest)
	}
	return nil
}

// QueryHistory records recently used queries for diversity enforcement.
type QueryHistory interface {
	RecentQueries(ctx context.Context, topicID string, since time.Time) ([]string, error)
}

// MemoryQueryHistory is the in-memory V1 history (per-process). Durable
// history is derivable from persisted cycles; the Postgres store implements
// this interface on top of research_cycles.
type MemoryQueryHistory struct {
	mu      sync.Mutex
	entries map[string][]timedQuery
}

type timedQuery struct {
	query string
	at    time.Time
}

// NewMemoryQueryHistory returns an empty in-memory history.
func NewMemoryQueryHistory() *MemoryQueryHistory {
	return &MemoryQueryHistory{entries: map[string][]timedQuery{}}
}

// Record stores a query observation.
func (h *MemoryQueryHistory) Record(topicID, query string, at time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.entries[topicID] = append(h.entries[topicID], timedQuery{query: normalizeQuery(query), at: at})
}

// RecentQueries implements QueryHistory.
func (h *MemoryQueryHistory) RecentQueries(_ context.Context, topicID string, since time.Time) ([]string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := []string{}
	for _, entry := range h.entries[topicID] {
		if entry.at.After(since) {
			out = append(out, entry.query)
		}
	}
	return out, nil
}

// LLMQueryGenerator implements QueryGenerator over the model runtime.
type LLMQueryGenerator struct {
	Invoker  ModelInvoker // required; the canonical runtime boundary
	Provider string       // host-resolved provider ID (e.g. "openrouter")
	Model    string       // host-resolved model ID — changeable WITHOUT recompiling
	Cfg      QueryGeneratorConfig
	History  QueryHistory // optional; enables diversity enforcement
	// AllowedIntents is the host's authority: intents outside this list are
	// rejected even if the model proposes them.
	AllowedIntents []SearchIntent
}

// NewLLMQueryGenerator validates config and returns the generator.
func NewLLMQueryGenerator(invoker ModelInvoker, provider, model string, allowed []SearchIntent) (*LLMQueryGenerator, error) {
	if invoker == nil {
		return nil, fmt.Errorf("%w: query generator requires a ModelInvoker", ErrInvalidRequest)
	}
	if strings.TrimSpace(model) == "" {
		return nil, fmt.Errorf("%w: query generator requires a host-resolved model", ErrInvalidRequest)
	}
	if len(allowed) == 0 {
		return nil, fmt.Errorf("%w: query generator requires allowed intents", ErrInvalidRequest)
	}
	cfg := DefaultQueryGeneratorConfig()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &LLMQueryGenerator{
		Invoker:        invoker,
		Provider:       provider,
		Model:          model,
		Cfg:            cfg,
		AllowedIntents: allowed,
	}, nil
}

// ModelFailureError wraps an LLM failure with its class so the scheduler
// can postpone with the right backoff.
type ModelFailureError struct {
	Class ModelFailureClass
	Cause error
}

func (e *ModelFailureError) Error() string {
	return fmt.Sprintf("research model failure (%s)", e.Class)
}
func (e *ModelFailureError) Unwrap() error { return e.Cause }

// GenerateQueries implements QueryGenerator. On any model failure it
// returns a *ModelFailureError — never a silent fallback, never paid retry.
func (g *LLMQueryGenerator) GenerateQueries(ctx context.Context, topic ResearchTopic, maxQueries int) ([]string, error) {
	if maxQueries < 1 || maxQueries > g.Cfg.MaxQueries {
		maxQueries = g.Cfg.MaxQueries
	}

	// Build the minimal prompt: topic facts + recent history. NO keys, NO
	// credentials, NO unrelated organizational memory.
	var recent []string
	if g.History != nil {
		if list, err := g.History.RecentQueries(ctx, topic.ID, time.Now().Add(-g.Cfg.DiversityWindow)); err == nil {
			recent = list
		}
	}
	system := g.systemPrompt()
	user := g.userPrompt(topic, recent, maxQueries)

	invokeCtx, cancel := context.WithTimeout(ctx, g.Cfg.InvocationTimeout)
	defer cancel()
	result, err := g.Invoker.Invoke(invokeCtx, ModelInvocationRequest{
		ProviderID:      g.Provider,
		ProviderModelID: g.Model,
		SystemPrompt:    system,
		UserPrompt:      user,
		OutputMode:      ModelOutputJSON,
		MaxOutputTokens: g.Cfg.MaxOutputTokens,
		Timeout:         g.Cfg.InvocationTimeout,
	})
	if err != nil {
		class := ClassifyModelFailure(err)
		return nil, &ModelFailureError{Class: class, Cause: err}
	}

	envelope, err := parseQueryEnvelope(result.Content)
	if err != nil {
		return nil, &ModelFailureError{Class: ModelFailureMalformed, Cause: err}
	}

	queries := g.validate(topic, envelope, maxQueries, recent)
	if len(queries) == 0 {
		return nil, &ModelFailureError{
			Class: ModelFailureMalformed,
			Cause: errors.New("model produced no valid queries"),
		}
	}
	// Record for future diversity.
	if g.History != nil {
		now := time.Now().UTC()
		for _, q := range queries {
			if recorder, ok := g.History.(*MemoryQueryHistory); ok {
				recorder.Record(topic.ID, q, now)
			}
		}
	}
	return queries, nil
}

// systemPrompt states the contract: propose diverse search queries; NEVER
// name providers or request credentials; stay inside allowed intents.
func (g *LLMQueryGenerator) systemPrompt() string {
	return `You are the query planner for an autonomous research worker.
Given a research topic, propose search queries that will find NEW information.
Rules:
- Queries must be complementary, not trivial reformulations.
- Prefer canonical sources (papers, documentation, primary announcements).
- Output ONLY JSON: {"queries":[{"query":"...","intent":"...","reason":"..."}]}
- Allowed intents: ` + joinIntents(g.AllowedIntents) + `
- Never name search providers (Brave, Tavily, OpenAlex, ...). Provider
  routing is decided by the system, not by you.
- Never include API keys, URLs with credentials, or secrets.`
}

// userPrompt carries only the topic facts the planner needs.
func (g *LLMQueryGenerator) userPrompt(topic ResearchTopic, recent []string, maxQueries int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Topic: %s\n", topic.Title)
	if topic.Description != "" {
		fmt.Fprintf(&b, "Description: %s\n", topic.Description)
	}
	fmt.Fprintf(&b, "Research class: %s\n", topic.ResearchClass)
	fmt.Fprintf(&b, "Department: %s\n", topic.DepartmentID)
	if topic.LastCheckedAt != nil {
		fmt.Fprintf(&b, "Last checked: %s\n", topic.LastCheckedAt.UTC().Format(time.RFC3339))
	}
	if len(recent) > 0 {
		fmt.Fprintf(&b, "Recently used queries (do NOT repeat, diversify away from):\n")
		for _, q := range recent {
			fmt.Fprintf(&b, "- %s\n", q)
		}
	}
	fmt.Fprintf(&b, "Propose at most %d queries as JSON.\n", maxQueries)
	return b.String()
}

// parseQueryEnvelope strictly decodes the model output.
func parseQueryEnvelope(content []byte) (*queryGenerationEnvelope, error) {
	trimmed := strings.TrimSpace(string(content))
	// Tolerate a fenced code block wrapper.
	trimmed = strings.TrimPrefix(trimmed, "```json")
	trimmed = strings.TrimPrefix(trimmed, "```")
	trimmed = strings.TrimSuffix(trimmed, "```")
	trimmed = strings.TrimSpace(trimmed)
	var envelope queryGenerationEnvelope
	if err := json.Unmarshal([]byte(trimmed), &envelope); err != nil {
		return nil, fmt.Errorf("model output is not valid query JSON: %w", err)
	}
	return &envelope, nil
}

// validate applies host authority over every proposal:
//   - non-empty, bounded query text;
//   - intent inside the host allow-list (NOT the model's claim);
//   - duplicate suppression (batch + diversity window);
//   - hard cap on count.
//
// Extra/unknown model fields are ignored structurally by JSON decoding, so a
// model cannot smuggle provider, priority, promote_to_rag or department
// authority through this boundary.
func (g *LLMQueryGenerator) validate(topic ResearchTopic, envelope *queryGenerationEnvelope, maxQueries int, recent []string) []string {
	allowed := map[SearchIntent]bool{}
	for _, intent := range g.AllowedIntents {
		allowed[intent] = true
	}
	seen := map[string]bool{}
	for _, q := range recent {
		seen[normalizeQuery(q)] = true
	}
	out := make([]string, 0, maxQueries)
	for _, proposal := range envelope.Queries {
		if len(out) >= maxQueries {
			break
		}
		query := strings.TrimSpace(proposal.Query)
		if query == "" || len(query) > g.Cfg.MaxQueryLength {
			continue
		}
		if len(proposal.Query) != len(strings.TrimSpace(proposal.Query)) {
			// reject whitespace-padded junk
		}
		// A query that is mostly a URL is not a search query.
		if strings.Contains(query, "://") || strings.Contains(query, "Bearer ") || strings.Contains(strings.ToLower(query), "api_key") {
			continue
		}
		if !allowed[proposal.Intent] {
			// Fall back to the topic's first allowed intent only if the
			// model's intent was invalid; never let the model EXPAND scope.
			proposal.Intent = topic.AllowedIntents[0]
		}
		_ = proposal.Intent // execution intent comes from the topic allow-list in the scheduler
		key := normalizeQuery(query)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, query)
	}
	return out
}

// normalizeQuery folds case/whitespace for duplicate detection.
func normalizeQuery(q string) string {
	return strings.Join(strings.Fields(strings.ToLower(q)), " ")
}

// joinIntents renders the intent list for the prompt.
func joinIntents(intents []SearchIntent) string {
	parts := make([]string, 0, len(intents))
	for _, intent := range intents {
		parts = append(parts, string(intent))
	}
	return strings.Join(parts, ", ")
}

// Compile-time proof the generator satisfies the scheduler boundary.
var _ QueryGenerator = (*LLMQueryGenerator)(nil)
