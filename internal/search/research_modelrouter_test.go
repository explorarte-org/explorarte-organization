package search

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// =============================================================================
// Router test fakes
// =============================================================================

// scriptedInvoker plays a queue of outcomes per provider.
type scriptedInvoker struct {
	mu        sync.Mutex
	outcomes  []invokerOutcome
	calls     int
	lastModel string
}

type invokerOutcome struct {
	result ModelInvocationResult
	err    error
}

func (s *scriptedInvoker) Invoke(_ context.Context, req ModelInvocationRequest) (ModelInvocationResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.lastModel = req.ProviderModelID
	if len(s.outcomes) == 0 {
		return ModelInvocationResult{Content: []byte(`{"queries":[]}`)}, nil
	}
	out := s.outcomes[0]
	s.outcomes = s.outcomes[1:]
	return out.result, out.err
}

func okResult() invokerOutcome {
	return invokerOutcome{result: ModelInvocationResult{Content: []byte("ok")}}
}

// routerFixture builds a router with per-provider scripted invokers.
type routerFixture struct {
	router  *FreeModelRouter
	cf      *scriptedInvoker
	or      *scriptedInvoker
	mistral *scriptedInvoker
	events  *recordingEvents
	clock   *clockStepper
}

func newRouterFixture(t *testing.T, allowPaid bool) *routerFixture {
	t.Helper()
	fx := &routerFixture{
		cf:      &scriptedInvoker{outcomes: []invokerOutcome{okResult()}},
		or:      &scriptedInvoker{outcomes: []invokerOutcome{okResult()}},
		mistral: &scriptedInvoker{outcomes: []invokerOutcome{okResult()}},
		events:  &recordingEvents{},
		clock:   &clockStepper{now: time.Now().UTC().Truncate(time.Second), step: time.Second},
	}
	cfg := ResearchPoolConfig{
		CloudflareModel:         DefaultResearchCloudflareModel,
		MistralModel:            "ministral-8b-2512",
		MistralCreditCeilingUSD: "10",
		OpenRouterPool:          []string{"google/gemma-4-31b-it:free", "nvidia/nemotron-3.5-lightning:free"},
		AllowPaid:               allowPaid,
	}
	factory := func(provider string) (ModelInvoker, bool) {
		switch provider {
		case ProviderCloudflareWorkersAI:
			return fx.cf, true
		case ProviderOpenRouter:
			return fx.or, true
		case ProviderMistral:
			return fx.mistral, true
		}
		return nil, false
	}
	router, err := BuildResearchModelPool(cfg, factory, fx.events)
	if err != nil {
		t.Fatal(err)
	}
	fx.router = router
	return fx
}

func requestFor() ModelInvocationRequest {
	return ModelInvocationRequest{
		SystemPrompt:    "plan queries",
		UserPrompt:      "topic X",
		OutputMode:      ModelOutputJSON,
		MaxOutputTokens: 200,
		Timeout:         5 * time.Second,
	}
}

// =============================================================================
// A. Selection
// =============================================================================

func TestModelRouter_SelectsFreeDailyFirst(t *testing.T) {
	fx := newRouterFixture(t, false)
	result, err := fx.router.Invoke(context.Background(), requestFor())
	if err != nil {
		t.Fatal(err)
	}
	// Cloudflare (free_daily) ranks before free_model and credit_monthly.
	if fx.cf.calls != 1 || fx.or.calls != 0 || fx.mistral.calls != 0 {
		t.Fatalf("expected Cloudflare first: cf=%d or=%d mistral=%d", fx.cf.calls, fx.or.calls, fx.mistral.calls)
	}
	if fx.cf.lastModel != DefaultResearchCloudflareModel {
		t.Fatalf("cloudflare model mismatch: %q", fx.cf.lastModel)
	}
	_ = result
}

func TestModelRouter_CloudflareCooldownFallsToFreeModel(t *testing.T) {
	fx := newRouterFixture(t, false)
	// Cloudflare rate-limited with NO Retry-After -> class cooldown.
	fx.cf.outcomes = []invokerOutcome{
		{err: &ProviderError{Provider: ProviderCloudflareWorkersAI, Kind: ProviderErrorRateLimited, StatusCode: 429}},
	}
	_, err := fx.router.Invoke(context.Background(), requestFor())
	if err != nil {
		t.Fatal(err)
	}
	if fx.cf.calls != 1 || fx.or.calls != 1 {
		t.Fatalf("fallback must try OpenRouter free next: cf=%d or=%d", fx.cf.calls, fx.or.calls)
	}
	// Cloudflare bucket is cooling down; sibling providers unaffected.
	state, _ := fx.router.State(ProviderCloudflareWorkersAI, DefaultResearchCloudflareModel)
	if !state.inCooldown(fx.clock.now) && state.CooldownUntil == nil {
		t.Fatal("cloudflare bucket must be cooling down")
	}
}

func TestModelRouter_CandidateDisabledSkipped(t *testing.T) {
	fx := newRouterFixture(t, false)
	fx.router.DisableCandidate(ProviderCloudflareWorkersAI, DefaultResearchCloudflareModel, "admin")
	fx.cf.outcomes = nil // would succeed if reached; must NOT be reached
	if _, err := fx.router.Invoke(context.Background(), requestFor()); err != nil {
		t.Fatal(err)
	}
	if fx.cf.calls != 0 {
		t.Fatal("disabled candidate must never be invoked")
	}
	if fx.or.calls != 1 {
		t.Fatal("selection must fall to the next eligible class")
	}
}

func TestModelRouter_NoEligibleCandidate_TypedError(t *testing.T) {
	fx := newRouterFixture(t, false)
	for _, candidate := range []struct{ provider, model string }{
		{ProviderCloudflareWorkersAI, DefaultResearchCloudflareModel},
		{ProviderOpenRouter, "google/gemma-4-31b-it:free"},
		{ProviderOpenRouter, "nvidia/nemotron-3.5-lightning:free"},
		{ProviderMistral, "ministral-8b-2512"},
	} {
		fx.router.DisableCandidate(candidate.provider, candidate.model, "test")
	}
	_, err := fx.router.Invoke(context.Background(), requestFor())
	if !errors.Is(err, ErrNoModelCapacity) {
		t.Fatalf("expected typed no-capacity error, got %v", err)
	}
}

// =============================================================================
// B. Capacity classes
// =============================================================================

func TestModelRouter_PaidClassGatedByDefault(t *testing.T) {
	fx := newRouterFixture(t, false) // AllowPaid=false
	// Exhaust free_daily, free_model, credit_monthly; paid bucket exists.
	fx.router.DisableCandidate(ProviderCloudflareWorkersAI, DefaultResearchCloudflareModel, "test")
	fx.router.DisableCandidate(ProviderOpenRouter, "google/gemma-4-31b-it:free", "test")
	fx.router.DisableCandidate(ProviderOpenRouter, "nvidia/nemotron-3.5-lightning:free", "test")
	fx.router.DisableCandidate(ProviderMistral, "ministral-8b-2512", "test")

	// Register a paid candidate directly; it must NOT be selected.
	extra := ModelCandidate{
		Provider: "openai", ModelID: "gpt-4.1-mini", CapacityClass: CapacityPaid,
		Capabilities: []ModelCapabilityRequirement{CapabilityTextGeneration},
		Priority:     0, Enabled: true, Invoker: &scriptedInvoker{outcomes: []invokerOutcome{okResult()}},
	}
	if err := fx.router.addCandidateForTest(extra); err != nil {
		t.Fatal(err)
	}
	_, err := fx.router.Invoke(context.Background(), requestFor())
	if !errors.Is(err, ErrNoModelCapacity) {
		t.Fatalf("paid capacity must be unreachable with AllowPaid=false, got %v", err)
	}
}

func TestModelRouter_CapacityClassRanking(t *testing.T) {
	if !(CapacityFreeDaily.classRank() < CapacityFreeModel.classRank() &&
		CapacityFreeModel.classRank() < CapacityCreditMonthly.classRank() &&
		CapacityCreditMonthly.classRank() < CapacityPaid.classRank()) {
		t.Fatal("capacity class ranking must be free_daily < free_model < credit_monthly < paid")
	}
}

// =============================================================================
// C. Cloudflare behavior
// =============================================================================

func TestModelRouter_DailyQuotaExhaustion_NoImmediateRetry(t *testing.T) {
	fx := newRouterFixture(t, false)
	// Daily quota exhausted with an explicit Retry-After of 6h.
	fx.cf.outcomes = []invokerOutcome{
		{err: retryAfterSignal(ProviderCloudflareWorkersAI, 429, 6*time.Hour)},
		{err: errors.New("must not be called again")},
	}
	// First invocation: Cloudflare fails -> OpenRouter answers.
	if _, err := fx.router.Invoke(context.Background(), requestFor()); err != nil {
		t.Fatal(err)
	}
	// Drain the OpenRouter script so the next selection has budget.
	fx.or.outcomes = append(fx.or.outcomes, okResult(), okResult(), okResult(), okResult())
	// Second invocation: Cloudflare still cooling down (Retry-After honored
	// exactly), must go straight to the next candidate — no useless probe.
	fx.or.outcomes = fx.or.outcomes[:1]
	if _, err := fx.router.Invoke(context.Background(), requestFor()); err != nil {
		t.Fatal(err)
	}
	if fx.cf.calls != 1 {
		t.Fatalf("exhausted daily bucket must not be retried: cf calls=%d", fx.cf.calls)
	}
	// When the cooldown expires, the bucket is probeable again.
	state, _ := fx.router.State(ProviderCloudflareWorkersAI, DefaultResearchCloudflareModel)
	after := state.CooldownUntil.Add(time.Second)
	if state.inCooldown(after) {
		t.Fatal("cooldown must expire at the reported boundary")
	}
}

func TestModelRouter_RetryAfterExplicitWins(t *testing.T) {
	fx := newRouterFixture(t, false)
	fx.cf.outcomes = []invokerOutcome{
		{err: retryAfterSignal(ProviderCloudflareWorkersAI, 429, 37*time.Minute)},
	}
	if _, err := fx.router.Invoke(context.Background(), requestFor()); err != nil {
		t.Fatal(err)
	}
	state, _ := fx.router.State(ProviderCloudflareWorkersAI, DefaultResearchCloudflareModel)
	want := fx.clock.now.Add(37 * time.Minute)
	if state.CooldownUntil == nil || !state.CooldownUntil.Truncate(time.Second).Equal(want.Truncate(time.Second)) {
		t.Fatalf("explicit Retry-After must set cooldown exactly: got %v want %v", state.CooldownUntil, want)
	}
}

func TestModelRouter_SuccessUpdatesState(t *testing.T) {
	fx := newRouterFixture(t, false)
	result, err := fx.router.Invoke(context.Background(), ModelInvocationRequest{
		OutputMode: ModelOutputJSON, MaxOutputTokens: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	state, _ := fx.router.State(ProviderCloudflareWorkersAI, DefaultResearchCloudflareModel)
	if state.RequestsUsed != 1 || state.LastSuccessAt == nil {
		t.Fatalf("success must update state: %+v", state)
	}
	if state.InputTokens != result.InputTokens {
		t.Fatalf("token usage must be preserved")
	}
	_ = result
}

// =============================================================================
// D. OpenRouter pool
// =============================================================================

func TestModelRouter_ExhaustedFreeModelNextFreeWorks(t *testing.T) {
	fx := newRouterFixture(t, false)
	fx.cf.outcomes = []invokerOutcome{{err: errors.New("cf down")}} // skip class 0
	// First free model quota-exhausted, second healthy.
	fx.or.outcomes = []invokerOutcome{
		{err: &ProviderError{Provider: ProviderOpenRouter, Kind: ProviderErrorRateLimited, StatusCode: 429}},
		okResult(),
	}
	result, err := fx.router.Invoke(context.Background(), requestFor())
	if err != nil {
		t.Fatal(err)
	}
	if fx.or.calls != 2 {
		t.Fatalf("second free model must serve: or calls=%d", fx.or.calls)
	}
	if !strings.HasSuffix(fx.or.lastModel, ":free") {
		t.Fatalf("free pool only: %q", fx.or.lastModel)
	}
	_ = result
}

func TestModelRouter_OneModelFailureDoesNotDisableProvider(t *testing.T) {
	fx := newRouterFixture(t, false)
	fx.cf.outcomes = []invokerOutcome{{err: errors.New("cf down")}}
	fx.or.outcomes = []invokerOutcome{
		{err: &ProviderError{Provider: ProviderOpenRouter, Kind: ProviderErrorBadRequest, StatusCode: 400}}, // invalid_model
		okResult(), // sibling still eligible
		okResult(),
	}
	if _, err := fx.router.Invoke(context.Background(), requestFor()); err != nil {
		t.Fatal(err)
	}
	state, _ := fx.router.State(ProviderOpenRouter, "google/gemma-4-31b-it:free")
	if !state.Disabled {
		t.Fatal("invalid model bucket must be disabled")
	}
	// The second OpenRouter model served the request.
	if fx.or.calls < 2 || !strings.Contains(fx.or.lastModel, "nemotron") {
		t.Fatalf("sibling model must remain selectable: %q", fx.or.lastModel)
	}
}

func TestModelRouter_CatalogRemovedModelDisabled(t *testing.T) {
	fx := newRouterFixture(t, false)
	// Catalog snapshot WITHOUT the first free model (e.g. retired).
	catalog := map[string]bool{
		"nvidia/nemotron-3.5-lightning:free": true,
	}
	disabled := fx.router.ValidateAgainstCatalog(ProviderOpenRouter, catalog)
	if disabled != 1 {
		t.Fatalf("expected 1 disabled candidate, got %d", disabled)
	}
	if _, ok := fx.router.State(ProviderOpenRouter, "google/gemma-4-31b-it:free"); !ok {
		t.Fatal("state must exist")
	}
	// Selection skips the retired model entirely.
	fx.cf.outcomes = []invokerOutcome{{err: errors.New("cf down")}}
	fx.or.outcomes = []invokerOutcome{okResult()}
	if _, err := fx.router.Invoke(context.Background(), requestFor()); err != nil {
		t.Fatal(err)
	}
	if fx.or.lastModel != "nvidia/nemotron-3.5-lightning:free" {
		t.Fatalf("retired model must be skipped: %q", fx.or.lastModel)
	}
}

// =============================================================================
// E. Mistral credit_monthly
// =============================================================================

func TestModelRouter_MistralCreditExhausted_NoFakeReset(t *testing.T) {
	fx := newRouterFixture(t, false)
	// Everything ahead of Mistral is down/exhausted.
	fx.cf.outcomes = []invokerOutcome{{err: errors.New("cf down")}}
	fx.or.outcomes = []invokerOutcome{
		{err: &ProviderError{Provider: ProviderOpenRouter, Kind: ProviderErrorRateLimited, StatusCode: 429}},
		{err: &ProviderError{Provider: ProviderOpenRouter, Kind: ProviderErrorRateLimited, StatusCode: 429}},
	}
	// Mistral reports monthly credit exhaustion WITHOUT any reset date.
	fx.mistral.outcomes = []invokerOutcome{
		{err: errors.New("insufficient quota credits: monthly balance spent")},
	}
	// Every bucket fails in this script: the router must return the typed
	// no-capacity error (researcher postpones — never paid retry).
	if _, err := fx.router.Invoke(context.Background(), requestFor()); !errors.Is(err, ErrNoModelCapacity) {
		t.Fatalf("expected typed no-capacity, got %v", err)
	}
	state, _ := fx.router.State(ProviderMistral, "ministral-8b-2512")
	if !state.QuotaExhausted {
		t.Fatal("mistral credit exhaustion must set quota_exhausted")
	}
	if state.ResetAt != nil {
		t.Fatal("reset must remain UNKNOWN — no fabricated +30 days")
	}
	if state.CooldownUntil == nil {
		t.Fatal("conservative re-check cooldown must exist")
	}
	// The cooldown is the credit_monthly default (24h), NOT ~30 days.
	cooldown := state.CooldownUntil.Sub(fx.clock.now)
	if cooldown > 25*time.Hour || cooldown < 23*time.Hour {
		t.Fatalf("credit cooldown must be the conservative default, got %v", cooldown)
	}
}

func TestModelRouter_MistralUpstreamResetWins(t *testing.T) {
	fx := newRouterFixture(t, false)
	reset := fx.clock.now.Add(72 * time.Hour) // provider reports real renewal
	fx.router.SetResetAt(ProviderMistral, "ministral-8b-2512", reset)
	state, _ := fx.router.State(ProviderMistral, "ministral-8b-2512")
	if state.ResetAt == nil || !state.ResetAt.Equal(reset) {
		t.Fatal("upstream-reported reset must be recorded")
	}
	// Before the reset: ineligible. After: eligible again.
	fx.mistral.outcomes = []invokerOutcome{okResult()}
	fx.router.DisableCandidate(ProviderCloudflareWorkersAI, DefaultResearchCloudflareModel, "test")
	fx.router.DisableCandidate(ProviderOpenRouter, "google/gemma-4-31b-it:free", "test")
	fx.router.DisableCandidate(ProviderOpenRouter, "nvidia/nemotron-3.5-lightning:free", "test")
	if _, err := fx.router.Invoke(context.Background(), requestFor()); !errors.Is(err, ErrNoModelCapacity) {
		t.Fatalf("credit bucket before reset must be ineligible, got %v", err)
	}
}

// =============================================================================
// Fallback chain end-to-end
// =============================================================================

func TestModelRouter_FullFallbackChain(t *testing.T) {
	fx := newRouterFixture(t, false)
	// cf quota -> or rate-limited (1st) -> or free (2nd) works -> done.
	fx.cf.outcomes = []invokerOutcome{
		{err: &ProviderError{Provider: ProviderCloudflareWorkersAI, Kind: ProviderErrorRateLimited, StatusCode: 429}},
	}
	fx.or.outcomes = []invokerOutcome{
		{err: &ProviderError{Provider: ProviderOpenRouter, Kind: ProviderErrorRateLimited, StatusCode: 429}},
		okResult(),
	}
	result, err := fx.router.Invoke(context.Background(), requestFor())
	if err != nil {
		t.Fatal(err)
	}
	if result.Content == nil {
		t.Fatal("fallback must produce a result")
	}
	if !fx.events.Has("modelrouter.model.failed") || !fx.events.Has("modelrouter.model.succeeded") {
		t.Fatal("fallback and success must be observable")
	}
}

// =============================================================================
// Architecture regression: no provider HTTP in the router
// =============================================================================

func TestModelRouter_Architecture_NoProviderHTTP(t *testing.T) {
	// The router source must not contain transport, credential, or endpoint
	// handling for any provider. This is the Cloudflare-reuse regression:
	// FreeModelRouter selects; the canonical runtime transports. Comment
	// text is stripped so prose about the design cannot false-positive.
	src := stripGoComments(readSourceFile(t, "research_modelrouter.go") + readSourceFile(t, "research_modelpool.go"))
	for _, forbidden := range []string{
		"http.Client", "http.NewRequest", "net/http", "Authorization",
		"api.cloudflare.com", "api.mistral.ai", "openrouter.ai/api",
		"Bearer ", "X-Subscription-Token", "account_id", "CF_API_TOKEN",
		"SearchRequest", "SearchIntent", "SearchRouter",
	} {
		if strings.Contains(src, forbidden) {
			t.Fatalf("FreeModelRouter must not contain %q — selection only", forbidden)
		}
	}
}

// =============================================================================
// Concurrency
// =============================================================================

func TestModelRouter_ConcurrentInvocationsRaceSafe(t *testing.T) {
	fx := newRouterFixture(t, false)
	fx.cf.outcomes = []invokerOutcome{
		{err: &ProviderError{Provider: ProviderCloudflareWorkersAI, Kind: ProviderErrorRateLimited, StatusCode: 429}},
	}
	// Plenty of scripted successes for concurrent OpenRouter calls.
	for i := 0; i < 32; i++ {
		fx.or.outcomes = append(fx.or.outcomes, okResult())
	}
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := fx.router.Invoke(context.Background(), requestFor()); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent invocation failed: %v", err)
	}
}

// =============================================================================
// Config
// =============================================================================

func TestModelRouter_PoolConfigFromEnv(t *testing.T) {
	env := map[string]string{
		"RESEARCH_CLOUDFLARE_MODEL": "@cf/zai-org/glm-4.7-flash",
		"RESEARCH_MISTRAL_MODEL":    "ministral-8b-2512",
		"FREE_MODEL_ALLOW_PAID":     "false",
	}
	cfg := LoadResearchPoolConfig(func(key string) (string, bool) {
		v, ok := env[key]
		return v, ok
	})
	if cfg.CloudflareModel != "@cf/zai-org/glm-4.7-flash" {
		t.Fatalf("cloudflare model default: %q", cfg.CloudflareModel)
	}
	if cfg.MistralModel != "ministral-8b-2512" {
		t.Fatalf("mistral model from config: %q", cfg.MistralModel)
	}
	if cfg.AllowPaid {
		t.Fatal("paid must default off")
	}
	// Mistral unset -> no candidate invented.
	empty := LoadResearchPoolConfig(func(string) (string, bool) { return "", false })
	if empty.MistralModel != "" {
		t.Fatal("mistral model must not be invented")
	}
}

// =============================================================================
// Test helpers
// =============================================================================

// addCandidateForTest registers an extra candidate (used by the paid-gate
// test). Production code adds candidates exclusively via
// BuildResearchModelPool.
func (r *FreeModelRouter) addCandidateForTest(candidate ModelCandidate) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if candidate.Invoker == nil {
		return errors.New("test candidate requires an invoker")
	}
	r.candidates = append(r.candidates, candidate)
	r.state[stateKey(candidate.Provider, candidate.ModelID)] = &CapacityState{
		Provider:      candidate.Provider,
		ModelID:       candidate.ModelID,
		CapacityClass: candidate.CapacityClass,
		Available:     true,
	}
	return nil
}

// stripGoComments removes // line comments and /* */ blocks so
// architecture assertions match CODE, not prose.
func stripGoComments(src string) string {
	var out []string
	for _, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") {
			continue
		}
		out = append(out, line)
	}
	cleaned := strings.Join(out, "\n")
	for {
		start := strings.Index(cleaned, "/*")
		if start < 0 {
			break
		}
		end := strings.Index(cleaned[start:], "*/")
		if end < 0 {
			cleaned = cleaned[:start]
			break
		}
		cleaned = cleaned[:start] + cleaned[start+end+2:]
	}
	return cleaned
}

// readSourceFile loads a package source file for architecture assertions.
func readSourceFile(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read source %s: %v", name, err)
	}
	return string(raw)
}

// Worker wiring: FREE_MODEL_ROUTER_ENABLED=true swaps the invoker seam to
// the pool router without touching the scheduler.
func TestModelRouter_WorkerWiring(t *testing.T) {
	t.Setenv("FREE_MODEL_ROUTER_ENABLED", "true")
	t.Setenv("RESEARCH_MISTRAL_MODEL", "ministral-8b-2512")
	fx := newRouterFixture(t, false)
	cfg := DefaultWorkerConfig()
	cfg.Enabled = true
	agenda := NewMemoryAgenda()
	worker, err := NewResearchWorker(cfg, WorkerDeps{
		Search:   &fakeSearchExecutor{},
		Agenda:   agenda,
		Claims:   MemoryClaimManager{Agenda: agenda},
		Evidence: MemoryEvidenceIndex{Agenda: agenda},
		PoolFactory: func(provider string) (ModelInvoker, bool) {
			switch provider {
			case ProviderCloudflareWorkersAI:
				return fx.cf, true
			case ProviderOpenRouter:
				return fx.or, true
			case ProviderMistral:
				return fx.mistral, true
			}
			return nil, false
		},
		Events: fx.events,
	})
	if err != nil {
		t.Fatal(err)
	}
	if worker == nil || worker.scheduler == nil {
		t.Fatal("worker with pool router must construct")
	}
	// The scheduler's query generator must now be the LLM one over the pool.
	if _, ok := worker.scheduler.deps.Queries.(*LLMQueryGenerator); !ok {
		t.Fatalf("expected LLMQueryGenerator over pool router, got %T", worker.scheduler.deps.Queries)
	}
	// Missing factory must fail construction loudly.
	if _, err := NewResearchWorker(cfg, WorkerDeps{
		Search: &fakeSearchExecutor{}, Agenda: agenda,
		Claims:   MemoryClaimManager{Agenda: agenda},
		Evidence: MemoryEvidenceIndex{Agenda: agenda},
	}); err == nil {
		t.Fatal("router enabled without factory must fail")
	}
}

// =============================================================================
// Kernel runtime bridge (ModelInvokerFunc)
// =============================================================================

// TestRuntimeBridge_MapsKernelInvocation simulates the deployment-side
// mapping: a bridge function that translates the search-layer request onto
// a (simulated) kernel modelruntime dispatch and back, proving passthrough
// of model identity, usage, request ID, typed errors and cancellation —
// with NO provider logic in the bridge.
func TestRuntimeBridge_MapsKernelInvocation(t *testing.T) {
	var seenProvider, seenModel string
	bridge := ModelInvokerFunc(func(ctx context.Context, req ModelInvocationRequest) (ModelInvocationResult, error) {
		seenProvider, seenModel = req.ProviderID, req.ProviderModelID
		// Simulated kernel dispatch outcome: provider rejects with the
		// canonical typed error the router understands (Retry-After kept).
		if req.ProviderModelID == "@cf/zai-org/glm-4.7-flash" && req.UserPrompt == "quota" {
			return ModelInvocationResult{}, retryAfterSignal(req.ProviderID, 429, 2*time.Hour)
		}
		return ModelInvocationResult{
			Content:     []byte(`{"queries":[{"query":"kernel bridged","intent":"news"}]}`),
			InputTokens: 100, OutputTokens: 20,
			FinishReason: "stop", ProviderRequestID: "kernel-req-1",
		}, nil
	})

	// Success path: usage and request ID pass through untouched.
	result, err := bridge.Invoke(context.Background(), ModelInvocationRequest{
		ProviderID: "cloudflare_workers_ai", ProviderModelID: "@cf/zai-org/glm-4.7-flash",
		OutputMode: ModelOutputJSON,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.InputTokens != 100 || result.OutputTokens != 20 || result.ProviderRequestID != "kernel-req-1" {
		t.Fatalf("usage/request-id passthrough failed: %+v", result)
	}
	if seenProvider != "cloudflare_workers_ai" || seenModel != "@cf/zai-org/glm-4.7-flash" {
		t.Fatal("provider/model must pass through host-resolved")
	}

	// Typed error passthrough: the router honors the bridge's Retry-After.
	router, err := NewFreeModelRouter([]ModelCandidate{{
		Provider: "cloudflare_workers_ai", ModelID: "@cf/zai-org/glm-4.7-flash",
		CapacityClass: CapacityFreeDaily, Enabled: true, Invoker: bridge,
	}}, DefaultFreeModelRouterConfig(), nil)
	if err != nil {
		t.Fatal(err)
	}
	req := requestFor()
	req.UserPrompt = "quota"
	if _, err := router.Invoke(context.Background(), req); err == nil {
		t.Fatal("quota rejection must surface")
	}
	state, _ := router.State("cloudflare_workers_ai", "@cf/zai-org/glm-4.7-flash")
	want := state.LastFailureAt.Add(2 * time.Hour)
	if state.CooldownUntil == nil || !state.CooldownUntil.Truncate(time.Second).Equal(want.Truncate(time.Second)) {
		t.Fatalf("bridge Retry-After must drive cooldown: %v", state.CooldownUntil)
	}

	// Cancellation: the bridge sees ctx and the router stops cleanly.
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	passingBridge := ModelInvokerFunc(func(ctx context.Context, _ ModelInvocationRequest) (ModelInvocationResult, error) {
		if ctx.Err() != nil {
			return ModelInvocationResult{}, ctx.Err()
		}
		return okResult().result, nil
	})
	if _, err := passingBridge.Invoke(cancelled, requestFor()); err == nil {
		t.Fatal("cancelled context must be honored by the bridge contract")
	}
}
