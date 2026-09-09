package search

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// =============================================================================
// FreeModelRouter V1
//
// A capacity-aware model SELECTOR — not a model provider. It sits between
// the researcher's QueryGenerator and the canonical model runtime:
//
//	AutonomousResearchScheduler
//	          ↓
//	   LLMQueryGenerator
//	          ↓
//	    ModelInvoker        ← FreeModelRouter satisfies this seam
//	          ↓
//	FreeModelRouter (selection/policy only)
//	          ↓
//	 candidate.Invoker      ← the CANONICAL runtime's per-provider invoker
//	  ├── cloudflare_workers_ai (@cf/zai-org/glm-4.7-flash, free_daily)
//	  ├── openrouter (:free catalog-validated IDs, free_model)
//	  └── mistral (monthly credit, credit_monthly)
//
// Non-goals (enforced by regression): no provider HTTP here, no API keys,
// no Authorization headers, no provider knowledge in the scheduler, no
// paid fallback by default, no LLM/RL in selection. A failure of one model
// never marks the whole provider offline.
// =============================================================================

// CapacityClass is the economic semantics of a model bucket.
type CapacityClass string

const (
	// CapacityFreeDaily: included daily allocation (Cloudflare Workers AI
	// free tier Neurons reset daily).
	CapacityFreeDaily CapacityClass = "free_daily"
	// CapacityFreeModel: provider-hosted free model variants
	// (OpenRouter ":free") with their own rate/quota limits.
	CapacityFreeModel CapacityClass = "free_model"
	// CapacityCreditMonthly: finite monthly credit on the provider account
	// (Mistral US$10/month). NOT paid fallback, NOT unlimited free.
	CapacityCreditMonthly CapacityClass = "credit_monthly"
	// CapacityPaid: pay-as-you-go budget. Gated by explicit policy
	// (AllowPaid) — default OFF for research.
	CapacityPaid CapacityClass = "paid"
)

// Valid reports whether the class is canonical.
func (c CapacityClass) Valid() bool {
	switch c {
	case CapacityFreeDaily, CapacityFreeModel, CapacityCreditMonthly, CapacityPaid:
		return true
	}
	return false
}

// classRank orders consumption preference: consume included/free capacity
// before finite credit, and credit before paid budget.
func (c CapacityClass) classRank() int {
	switch c {
	case CapacityFreeDaily:
		return 0
	case CapacityFreeModel:
		return 1
	case CapacityCreditMonthly:
		return 2
	case CapacityPaid:
		return 3
	}
	return 99
}

// ModelCapabilityRequirement names what a caller needs from a model.
type ModelCapabilityRequirement string

const (
	CapabilityTextGeneration ModelCapabilityRequirement = "text_generation"
	CapabilityStructuredJSON ModelCapabilityRequirement = "structured_json"
	CapabilityReasoning      ModelCapabilityRequirement = "reasoning"
	CapabilityToolCalling    ModelCapabilityRequirement = "tool_calling"
)

// ModelCandidate is one selectable model bucket. The Invoker is ALWAYS the
// canonical runtime's per-provider invoker, injected by deployment wiring —
// the router never constructs HTTP clients or credentials.
type ModelCandidate struct {
	Provider      string // canonical provider ID (e.g. "cloudflare_workers_ai")
	ModelID       string // provider model identity (e.g. "@cf/zai-org/glm-4.7-flash")
	CapacityClass CapacityClass
	Capabilities  []ModelCapabilityRequirement
	Priority      int // tie-break within a class; lower runs first
	Enabled       bool
	Invoker       ModelInvoker // canonical runtime boundary; nil = not wired
}

// stateKey namespaces capacity state per provider|model so one model's
// failure never poisons its siblings.
func stateKey(provider, modelID string) string {
	return provider + "|" + modelID
}

// CapacityState is the mutable capacity picture of one candidate.
type CapacityState struct {
	Provider      string
	ModelID       string
	CapacityClass CapacityClass

	Available      bool
	RateLimited    bool
	QuotaExhausted bool

	CooldownUntil *time.Time
	// ResetAt is honored ONLY when upstream actually reports it; never
	// fabricated (no fake "+30 days" for Mistral credit).
	ResetAt *time.Time

	RequestsUsed  int64
	InputTokens   int64
	OutputTokens  int64
	LastSuccessAt *time.Time
	LastFailureAt *time.Time

	// LastFailureClass records the taxonomy class of the most recent
	// failure for observability.
	LastFailureClass string
	// Disabled marks catalog-removed or administratively disabled models.
	Disabled bool
}

// inCooldown reports whether the cooldown window is active at now.
func (s *CapacityState) inCooldown(now time.Time) bool {
	if s.CooldownUntil == nil {
		return false
	}
	return now.Before(*s.CooldownUntil)
}

// FreeModelRouterConfig centralizes policy knobs (no scattered numbers).
type FreeModelRouterConfig struct {
	// AllowPaid gates paid capacity. Default false: when every
	// free/credit bucket is exhausted the router returns a typed
	// no-capacity error and the researcher postpones.
	AllowPaid bool

	// CooldownDefaults per class, used ONLY when upstream gave no
	// Retry-After/reset signal. Conservative, provider-aware.
	CooldownDefaults map[CapacityClass]time.Duration

	// Clock for deterministic tests.
	Clock func() time.Time
}

// DefaultFreeModelRouterConfig returns conservative V1 defaults.
func DefaultFreeModelRouterConfig() FreeModelRouterConfig {
	return FreeModelRouterConfig{
		AllowPaid: false,
		CooldownDefaults: map[CapacityClass]time.Duration{
			CapacityFreeDaily:     6 * time.Hour,  // daily bucket: conservative probe, reset honored when reported
			CapacityFreeModel:     1 * time.Hour,  // provider rate limits vary per model
			CapacityCreditMonthly: 24 * time.Hour, // unknown reset: slow, configurable re-check — never fake +30d
			CapacityPaid:          10 * time.Minute,
		},
		Clock: func() time.Time { return time.Now().UTC() },
	}
}

// Validate enforces sane bounds.
func (c FreeModelRouterConfig) Validate() error {
	for _, class := range []CapacityClass{CapacityFreeDaily, CapacityFreeModel, CapacityCreditMonthly} {
		d, ok := c.CooldownDefaults[class]
		if !ok || d < time.Minute {
			return fmt.Errorf("%w: cooldown default missing/too small for %q", ErrInvalidRequest, class)
		}
	}
	return nil
}

// ErrNoModelCapacity is the typed outcome when every eligible candidate is
// exhausted/unavailable and paid capacity is not allowed. The researcher
// maps it to a postponement — never to a paid retry.
var ErrNoModelCapacity = errors.New("modelrouter: no eligible model capacity")

// ModelRouterEventSink receives safe selection/failure events. The research
// event sink satisfies it, so modelrouter.* events flow into the same
// durable outbox.
type ModelRouterEventSink interface {
	ResearchEvent(event string, fields map[string]any)
}

// FreeModelRouter implements ModelInvoker.
type FreeModelRouter struct {
	mu               sync.Mutex
	candidates       []ModelCandidate
	state            map[string]*CapacityState
	cfg              FreeModelRouterConfig
	events           ModelRouterEventSink
	selectionEmitted bool
}

// NewFreeModelRouter validates config and builds the router over injected
// canonical-runtime invokers.
func NewFreeModelRouter(candidates []ModelCandidate, cfg FreeModelRouterConfig, events ModelRouterEventSink) (*FreeModelRouter, error) {
	if len(candidates) == 0 {
		return nil, fmt.Errorf("%w: model router requires at least one candidate", ErrInvalidRequest)
	}
	if cfg.Clock == nil {
		cfg.Clock = func() time.Time { return time.Now().UTC() }
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	router := &FreeModelRouter{
		state:  map[string]*CapacityState{},
		cfg:    cfg,
		events: events,
	}
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate.Provider) == "" || strings.TrimSpace(candidate.ModelID) == "" {
			return nil, fmt.Errorf("%w: candidate provider and model are required", ErrInvalidRequest)
		}
		if !candidate.CapacityClass.Valid() {
			return nil, fmt.Errorf("%w: unknown capacity class %q", ErrInvalidRequest, candidate.CapacityClass)
		}
		state := &CapacityState{
			Provider:      candidate.Provider,
			ModelID:       candidate.ModelID,
			CapacityClass: candidate.CapacityClass,
			Available:     true,
		}
		router.candidates = append(router.candidates, candidate)
		router.state[stateKey(candidate.Provider, candidate.ModelID)] = state
	}
	return router, nil
}

// Compile-time proof of the seam: the router IS a ModelInvoker, so
// LLMQueryGenerator and the scheduler require zero changes.
var _ ModelInvoker = (*FreeModelRouter)(nil)

// Invoke selects the best eligible candidate and executes through the
// canonical runtime invoker, updating capacity state on every outcome.
func (r *FreeModelRouter) Invoke(ctx context.Context, req ModelInvocationRequest) (ModelInvocationResult, error) {
	for {
		candidate, ok := r.selectCandidate(req)
		if !ok {
			r.emit("modelrouter.no_capacity", map[string]any{})
			return ModelInvocationResult{}, ErrNoModelCapacity
		}
		r.emit("modelrouter.model.selected", map[string]any{
			"provider": candidate.Provider, "model": candidate.ModelID,
			"capacity_class": string(candidate.CapacityClass),
		})

		// Delegate to the CANONICAL runtime invoker with the candidate's
		// identity — the router adds selection, never transport.
		invocation := req
		invocation.ProviderID = candidate.Provider
		invocation.ProviderModelID = candidate.ModelID
		result, err := candidate.Invoker.Invoke(ctx, invocation)

		if err == nil {
			r.recordSuccess(candidate, result)
			return result, nil
		}
		class := ClassifyRouterFailure(err)
		retryAfter := retryAfterFrom(err)
		r.recordFailure(candidate, class, retryAfter)
		r.emit("modelrouter.model.failed", map[string]any{
			"provider": candidate.Provider, "model": candidate.ModelID,
			"capacity_class": string(candidate.CapacityClass),
			"failure_class":  string(class),
			"cooldown_until": r.cooldownString(candidate),
		})
		if errors.Is(err, context.Canceled) || ctx.Err() != nil {
			// Caller cancellation is not a capacity event: stop entirely.
			return ModelInvocationResult{}, err
		}
		// Fall through to the next eligible candidate.
	}
}

// eligible reports whether a candidate may run right now.
func (r *FreeModelRouter) eligible(candidate ModelCandidate, now time.Time) bool {
	state := r.state[stateKey(candidate.Provider, candidate.ModelID)]
	if state == nil || state.Disabled || !state.Available {
		return false
	}
	if !candidate.Enabled {
		return false
	}
	if candidate.Invoker == nil {
		return false // canonical runtime invoker not wired: skip honestly
	}
	if !capabilitiesSatisfied(candidate.Capabilities, reqCapabilities{}) {
		return false
	}
	if state.inCooldown(now) {
		return false
	}
	if state.QuotaExhausted {
		// Quota exhaustion blocks until an explicit reset (or the class
		// cooldown) passes; with no known reset the cooldown default IS
		// the recovery probe interval.
		if state.inCooldown(now) || state.CooldownUntil == nil {
			return false
		}
	}
	if candidate.CapacityClass == CapacityPaid && !r.cfg.AllowPaid {
		return false
	}
	return true
}

// reqCapabilities is a placeholder for per-request capability needs; V1
// callers need text_generation + structured_json, which every candidate
// declares. Extensible without changing the seam.
type reqCapabilities struct{}

func capabilitiesSatisfied(capabilities []ModelCapabilityRequirement, _ reqCapabilities) bool {
	// V1: candidates declare capabilities; the router only requires that a
	// candidate explicitly list text_generation (QueryGenerator's JSON mode
	// is enforced host-side by strict parsing, not by provider features).
	for _, c := range capabilities {
		if c == CapabilityTextGeneration {
			return true
		}
	}
	return len(capabilities) == 0
}

// selectCandidate picks the highest-priority eligible candidate:
// capacity class rank → candidate priority → stable model ID. Deterministic,
// no LLM, no randomness.
func (r *FreeModelRouter) selectCandidate(req ModelInvocationRequest) (ModelCandidate, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.cfg.Clock()
	r.emitSelectionStarted()

	type ranked struct {
		candidate ModelCandidate
		classRank int
	}
	options := make([]ranked, 0, len(r.candidates))
	for _, candidate := range r.candidates {
		if !r.eligible(candidate, now) {
			continue
		}
		options = append(options, ranked{candidate: candidate, classRank: candidate.CapacityClass.classRank()})
	}
	if len(options) == 0 {
		return ModelCandidate{}, false
	}
	sort.Slice(options, func(i, j int) bool {
		if options[i].classRank != options[j].classRank {
			return options[i].classRank < options[j].classRank
		}
		if options[i].candidate.Priority != options[j].candidate.Priority {
			return options[i].candidate.Priority < options[j].candidate.Priority
		}
		return options[i].candidate.ModelID < options[j].candidate.ModelID
	})
	return options[0].candidate, true
}

// recordSuccess updates usage and clears failure state for ONE bucket.
func (r *FreeModelRouter) recordSuccess(candidate ModelCandidate, result ModelInvocationResult) {
	r.mu.Lock()
	defer r.mu.Unlock()
	state := r.state[stateKey(candidate.Provider, candidate.ModelID)]
	if state == nil {
		return
	}
	now := r.cfg.Clock()
	state.RequestsUsed++
	state.InputTokens += result.InputTokens
	state.OutputTokens += result.OutputTokens
	state.LastSuccessAt = &now
	state.LastFailureClass = ""
	state.CooldownUntil = nil
	state.RateLimited = false
	state.QuotaExhausted = false
	state.Available = true
	if result.ProviderRequestID == "" {
		result.ProviderRequestID = state.Provider + ":" + state.ModelID
	}
	r.emit("modelrouter.model.succeeded", map[string]any{
		"provider": candidate.Provider, "model": candidate.ModelID,
		"capacity_class": string(candidate.CapacityClass),
		"input_tokens":   result.InputTokens, "output_tokens": result.OutputTokens,
	})
}

// recordFailure applies provider-aware cooldown semantics. One model's
// failure never disables sibling models on the same provider.
func (r *FreeModelRouter) recordFailure(candidate ModelCandidate, class ModelFailureClass, retryAfter time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	state := r.state[stateKey(candidate.Provider, candidate.ModelID)]
	if state == nil {
		return
	}
	now := r.cfg.Clock()
	state.LastFailureAt = &now
	state.LastFailureClass = string(class)
	state.RequestsUsed++

	// Explicit upstream signal ALWAYS wins over defaults.
	if retryAfter > 0 {
		until := now.Add(retryAfter)
		state.CooldownUntil = &until
	}
	switch class {
	case ModelFailureRateLimited:
		state.RateLimited = true
		if retryAfter <= 0 {
			until := now.Add(r.cfg.CooldownDefaults[state.CapacityClass])
			state.CooldownUntil = &until
		}
	case ModelFailureQuotaExhausted:
		state.QuotaExhausted = true
		if retryAfter <= 0 {
			// No fabricated reset: the class default is a conservative
			// RE-CHECK interval (e.g. Mistral credit: 24h probe), and an
			// upstream-reported ResetAt replaces it the moment it exists.
			until := now.Add(r.cfg.CooldownDefaults[state.CapacityClass])
			state.CooldownUntil = &until
		}
	case ModelFailureInvalidModel, ModelFailurePermanent, ModelFailureUnauthorized:
		// These will not heal: disable the bucket, siblings unaffected.
		state.Disabled = true
		state.Available = false
		state.CooldownUntil = nil
	default: // unavailable, transient, malformed
		state.Available = true
		if retryAfter <= 0 {
			until := now.Add(r.cfg.CooldownDefaults[state.CapacityClass])
			state.CooldownUntil = &until
		}
	}
	r.emit("modelrouter.capacity.exhausted", map[string]any{
		"provider": candidate.Provider, "model": candidate.ModelID,
		"capacity_class": string(candidate.CapacityClass),
		"failure_class":  string(class),
	})
}

// SetResetAt records an upstream-reported reset (usage/balance endpoint,
// rate-limit headers). It replaces cooldown defaults with real evidence.
func (r *FreeModelRouter) SetResetAt(provider, modelID string, resetAt time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	state := r.state[stateKey(provider, modelID)]
	if state == nil {
		return
	}
	state.ResetAt = &resetAt
	state.CooldownUntil = &resetAt
	if !r.cfg.Clock().Before(resetAt) {
		// Reset already passed: bucket is probeable again.
		state.QuotaExhausted = false
		state.RateLimited = false
		state.CooldownUntil = nil
	}
}

// DisableCandidate removes a model from selection (catalog disappearance,
// admin action). Provider siblings stay selectable.
func (r *FreeModelRouter) DisableCandidate(provider, modelID, reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	state := r.state[stateKey(provider, modelID)]
	if state == nil {
		return
	}
	state.Disabled = true
	state.Available = false
	if r.events != nil && reason != "" {
		r.events.ResearchEvent("modelrouter.model.failed", map[string]any{
			"provider": provider, "model": modelID, "reason": reason,
		})
	}
}

// ValidateAgainstCatalog disables candidates whose model ID is not present
// in a validated provider catalog (the minimax/minimax-m3:free lesson: a
// configured-but-nonexistent model must fail loudly into `disabled`, never
// burn invocations). Missing provider catalogs are ignored (no claim).
func (r *FreeModelRouter) ValidateAgainstCatalog(provider string, catalog map[string]bool) int {
	if catalog == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	disabled := 0
	for i := range r.candidates {
		candidate := &r.candidates[i]
		if candidate.Provider != provider {
			continue
		}
		if !catalog[candidate.ModelID] {
			if state := r.state[stateKey(candidate.Provider, candidate.ModelID)]; state != nil {
				state.Disabled = true
				state.Available = false
				disabled++
			}
		}
	}
	return disabled
}

// State returns a snapshot of one bucket's capacity state (thread-safe copy).
func (r *FreeModelRouter) State(provider, modelID string) (CapacityState, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	state, ok := r.state[stateKey(provider, modelID)]
	if !ok {
		return CapacityState{}, false
	}
	return *state, true
}

// cooldownString renders the active cooldown for events (safe field).
func (r *FreeModelRouter) cooldownString(candidate ModelCandidate) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	state := r.state[stateKey(candidate.Provider, candidate.ModelID)]
	if state == nil || state.CooldownUntil == nil {
		return ""
	}
	return state.CooldownUntil.UTC().Format(time.RFC3339)
}

// retryAfterFrom extracts an upstream Retry-After hint from the error chain
// via the existing ProviderError type. Zero means "no explicit signal".
func retryAfterFrom(err error) time.Duration {
	var providerErr *ProviderError
	if errors.As(err, &providerErr) {
		return providerErr.RetryAfter
	}
	return 0
}

// retryAfterSignal is the runtime-friendly constructor: runtimes that parse
// upstream headers wrap them into a ProviderError so the router honors the
// explicit signal.
func retryAfterSignal(provider string, status int, wait time.Duration) error {
	return &ProviderError{Provider: ProviderID(provider), Kind: ProviderErrorRateLimited, StatusCode: status, RetryAfter: wait}
}

// ClassifyRouterFailure extends the model failure taxonomy with the
// router-relevant classes. Existing classifications are preserved.
func ClassifyRouterFailure(err error) ModelFailureClass {
	if err == nil {
		return ""
	}
	var providerErr *ProviderError
	if errors.As(err, &providerErr) {
		switch providerErr.Kind {
		case ProviderErrorRateLimited:
			return ModelFailureRateLimited
		case ProviderErrorUnauthorized, ProviderErrorForbidden:
			return ModelFailureUnauthorized
		case ProviderErrorMalformedResponse:
			return ModelFailureMalformed
		case ProviderErrorUnavailable:
			return ModelFailureUnavailable
		case ProviderErrorTimeout:
			return ModelFailureTransient
		case ProviderErrorBadRequest:
			// A rejected model name/param is a config error, not capacity.
			return ModelFailureInvalidModel
		default:
			return ModelFailureTransient
		}
	}
	return ClassifyModelFailure(err)
}

// Additional failure classes for the router taxonomy (additive to the
// researcher's ModelFailureClass constants).
const (
	ModelFailureInvalidModel ModelFailureClass = "invalid_model"
	ModelFailureUnauthorized ModelFailureClass = "unauthorized"
)

// emit is a nil-safe event sink call.
func (r *FreeModelRouter) emit(event string, fields map[string]any) {
	if r.events == nil {
		return
	}
	r.events.ResearchEvent(event, fields)
}

// emitSelectionStarted emits the selection marker once per router lifetime.
func (r *FreeModelRouter) emitSelectionStarted() {
	if r.selectionEmitted {
		return
	}
	r.selectionEmitted = true
	r.emit("modelrouter.selection.started", map[string]any{})
}
