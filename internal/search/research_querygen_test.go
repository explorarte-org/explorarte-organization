package search

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// fakeModelInvoker is a scriptable ModelInvoker.
type fakeModelInvoker struct {
	response string
	err      error
	calls    int
	lastReq  ModelInvocationRequest
}

func (f *fakeModelInvoker) Invoke(_ context.Context, req ModelInvocationRequest) (ModelInvocationResult, error) {
	f.calls++
	f.lastReq = req
	if f.err != nil {
		return ModelInvocationResult{}, f.err
	}
	return ModelInvocationResult{Content: []byte(f.response), FinishReason: "stop"}, nil
}

func newTestGenerator(t *testing.T, invoker ModelInvoker) *LLMQueryGenerator {
	t.Helper()
	gen, err := NewLLMQueryGenerator(invoker, "openrouter", "minimax/minimax-m3:free",
		[]SearchIntent{IntentWebGeneral, IntentAcademic, IntentPreprint})
	if err != nil {
		t.Fatal(err)
	}
	return gen
}

func querygenTopic() ResearchTopic {
	topic := watchTopic("topic-querygen")
	topic.AllowedIntents = []SearchIntent{IntentAcademic, IntentPreprint}
	return topic
}

// D. valid JSON -> queries
func TestQueryGen_ValidJSON(t *testing.T) {
	invoker := &fakeModelInvoker{response: `{"queries":[
		{"query":"autonomous agent self improvement evaluation 2026","intent":"academic"},
		{"query":"agent self reflection benchmark preprint","intent":"preprint"}]}`}
	gen := newTestGenerator(t, invoker)
	queries, err := gen.GenerateQueries(context.Background(), querygenTopic(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(queries) != 2 {
		t.Fatalf("expected 2 queries, got %d", len(queries))
	}
	if !strings.Contains(invoker.lastReq.SystemPrompt, "Never name search providers") {
		t.Fatal("system prompt must forbid provider authority")
	}
}

// D. malformed JSON
func TestQueryGen_MalformedJSON(t *testing.T) {
	invoker := &fakeModelInvoker{response: `this is not json at all`}
	gen := newTestGenerator(t, invoker)
	_, err := gen.GenerateQueries(context.Background(), querygenTopic(), 2)
	var failure *ModelFailureError
	if !errors.As(err, &failure) || failure.Class != ModelFailureMalformed {
		t.Fatalf("expected malformed_output failure, got %v", err)
	}
}

// D. fenced JSON tolerated
func TestQueryGen_FencedJSON(t *testing.T) {
	invoker := &fakeModelInvoker{response: "```json\n{\"queries\":[{\"query\":\"valid fenced query\",\"intent\":\"academic\"}]}\n```"}
	gen := newTestGenerator(t, invoker)
	queries, err := gen.GenerateQueries(context.Background(), querygenTopic(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(queries) != 1 || queries[0] != "valid fenced query" {
		t.Fatalf("fenced JSON must parse, got %v", queries)
	}
}

// D. too many queries -> capped
func TestQueryGen_TooManyCapped(t *testing.T) {
	invoker := &fakeModelInvoker{response: `{"queries":[
		{"query":"q one academic","intent":"academic"},
		{"query":"q two preprint","intent":"preprint"},
		{"query":"q three academic","intent":"academic"},
		{"query":"q four preprint","intent":"preprint"},
		{"query":"q five academic","intent":"academic"}]}`}
	gen := newTestGenerator(t, invoker)
	queries, err := gen.GenerateQueries(context.Background(), querygenTopic(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(queries) != 2 {
		t.Fatalf("host must cap at maxQueries=2, got %d", len(queries))
	}
}

// D. invalid intent -> falls back to topic allow-list
func TestQueryGen_InvalidIntentFallback(t *testing.T) {
	invoker := &fakeModelInvoker{response: `{"queries":[
		{"query":"model claims book_general","intent":"book_general"},
		{"query":"valid academic query","intent":"academic"}]}`}
	gen := newTestGenerator(t, invoker)
	queries, err := gen.GenerateQueries(context.Background(), querygenTopic(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(queries) != 2 {
		t.Fatalf("invalid intent should fall back, not drop, got %d", len(queries))
	}
}

// D. duplicate queries (batch and history window)
func TestQueryGen_DuplicatesSuppressed(t *testing.T) {
	invoker := &fakeModelInvoker{response: `{"queries":[
		{"query":"identical query text","intent":"academic"},
		{"query":"Identical Query Text","intent":"preprint"},
		{"query":"genuinely different query","intent":"academic"}]}`}
	gen := newTestGenerator(t, invoker)
	queries, err := gen.GenerateQueries(context.Background(), querygenTopic(), 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(queries) != 2 {
		t.Fatalf("case/whitespace duplicates must be suppressed, got %v", queries)
	}
}

// D. empty and junk queries dropped
func TestQueryGen_EmptyAndJunkDropped(t *testing.T) {
	invoker := &fakeModelInvoker{response: `{"queries":[
		{"query":"   ","intent":"academic"},
		{"query":"https://evil.example/?api_key=secret","intent":"academic"},
		{"query":"Bearer sk-abc123 tokens","intent":"academic"},
		{"query":"legitimate search terms","intent":"academic"}]}`}
	gen := newTestGenerator(t, invoker)
	queries, err := gen.GenerateQueries(context.Background(), querygenTopic(), 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(queries) != 1 || queries[0] != "legitimate search terms" {
		t.Fatalf("empty/URL/secret-bearing queries must drop, got %v", queries)
	}
}

// D. quota exhausted / rate limited classification
func TestQueryGen_FailureClassification(t *testing.T) {
	cases := []struct {
		err   error
		class ModelFailureClass
	}{
		{&ProviderError{Provider: "openrouter", Kind: ProviderErrorRateLimited, StatusCode: 429}, ModelFailureRateLimited},
		{&ProviderError{Provider: "openrouter", Kind: ProviderErrorUnavailable, StatusCode: 503}, ModelFailureUnavailable},
		{&ProviderError{Provider: "openrouter", Kind: ProviderErrorUnauthorized, StatusCode: 401}, ModelFailurePermanent},
		{&ProviderError{Provider: "openrouter", Kind: ProviderErrorTimeout, StatusCode: 408}, ModelFailureTransient},
		{errors.New("insufficient quota credits for this model"), ModelFailureQuotaExhausted},
		{context.DeadlineExceeded, ModelFailureTransient},
	}
	for i, tc := range cases {
		invoker := &fakeModelInvoker{err: tc.err}
		gen := newTestGenerator(t, invoker)
		_, err := gen.GenerateQueries(context.Background(), querygenTopic(), 1)
		var failure *ModelFailureError
		if !errors.As(err, &failure) {
			t.Fatalf("case %d: expected ModelFailureError, got %v", i, err)
		}
		if failure.Class != tc.class {
			t.Fatalf("case %d: class mismatch, want %s got %s", i, tc.class, failure.Class)
		}
		// Every quota-class failure must postpone with backoff > tick.
		if d := PostponeFor(failure.Class); d < 10*time.Minute {
			t.Fatalf("case %d: backoff %v must never be shorter than a tick", i, d)
		}
	}
}

// E. host authority: model cannot smuggle extra authority fields
func TestQueryGen_HostAuthority_NoSmuggling(t *testing.T) {
	// The model emits provider, priority, promote_to_rag, department fields.
	// Structural decoding ignores them; the generator output is []string
	// (queries only), so there is NO channel for this authority.
	invoker := &fakeModelInvoker{response: `{
		"queries":[{"query":"try to smuggle","intent":"academic",
			"provider":"brave","priority":"critical",
			"promote_to_rag":true,"department":"ceo"}],
		"provider":"brave","promote_to_rag":true,"department":"ceo",
		"priority":1.0}`}
	gen := newTestGenerator(t, invoker)
	queries, err := gen.GenerateQueries(context.Background(), querygenTopic(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(queries) != 1 || queries[0] != "try to smuggle" {
		t.Fatalf("query text must survive, authority fields must not: %v", queries)
	}
	// The invocation carried the HOST-resolved model, not a model-chosen one.
	if invoker.lastReq.ProviderModelID != "minimax/minimax-m3:free" {
		t.Fatalf("runtime model must be host-resolved, got %q", invoker.lastReq.ProviderModelID)
	}
}

// Input hygiene: prompts carry no secrets and minimal context only.
func TestQueryGen_PromptHygiene(t *testing.T) {
	invoker := &fakeModelInvoker{response: `{"queries":[{"query":"x","intent":"academic"}]}`}
	gen := newTestGenerator(t, invoker)
	topic := querygenTopic()
	topic.Description = "desc with details"
	if _, err := gen.GenerateQueries(context.Background(), topic, 1); err != nil {
		t.Fatal(err)
	}
	combined := invoker.lastReq.SystemPrompt + invoker.lastReq.UserPrompt
	for _, forbidden := range []string{"sk-", "api_key", "Bearer", "password"} {
		if strings.Contains(strings.ToLower(combined), strings.ToLower(forbidden)) {
			t.Fatalf("prompt must never contain %q", forbidden)
		}
	}
	// Minimal context: no unrelated missions/memory markers.
	if strings.Contains(combined, "MissionID") || strings.Contains(combined, "knowledge rag") {
		t.Fatal("prompt must stay minimal")
	}
}

// Query history diversity across generations.
func TestQueryGen_HistoryDiversity(t *testing.T) {
	invoker := &fakeModelInvoker{response: `{"queries":[{"query":"same query forever","intent":"academic"}]}`}
	gen := newTestGenerator(t, invoker)
	history := NewMemoryQueryHistory()
	gen.History = history
	topic := querygenTopic()
	first, err := gen.GenerateQueries(context.Background(), topic, 1)
	if err != nil {
		t.Fatal(err)
	}
	// Same model output again: history suppresses the repeat -> malformed
	// (no valid NEW queries).
	_, err = gen.GenerateQueries(context.Background(), topic, 1)
	if err == nil {
		t.Fatalf("repeated query must be suppressed by history, got %v", first)
	}
	var failure *ModelFailureError
	if !errors.As(err, &failure) || failure.Class != ModelFailureMalformed {
		t.Fatalf("expected malformed (no new queries), got %v", err)
	}
}

// Config validation.
func TestQueryGen_ConfigValidation(t *testing.T) {
	if _, err := NewLLMQueryGenerator(nil, "openrouter", "m", []SearchIntent{IntentAcademic}); err == nil {
		t.Fatal("nil invoker must fail")
	}
	if _, err := NewLLMQueryGenerator(&fakeModelInvoker{}, "openrouter", "", []SearchIntent{IntentAcademic}); err == nil {
		t.Fatal("empty model must fail")
	}
	if _, err := NewLLMQueryGenerator(&fakeModelInvoker{}, "openrouter", "m", nil); err == nil {
		t.Fatal("no allowed intents must fail")
	}
	if err := (QueryGeneratorConfig{MaxQueries: 0}).Validate(); err == nil {
		t.Fatal("zero max queries must fail")
	}
}

// Model identity is host-resolved and changeable without recompiling.
func TestQueryGen_ModelIdentityHostResolved(t *testing.T) {
	for _, model := range []string{"minimax/minimax-m3:free", "google/gemma-4-31b-it:free", "nvidia/nemotron-3.5-lightning:free"} {
		gen, err := NewLLMQueryGenerator(&fakeModelInvoker{}, "openrouter", model,
			[]SearchIntent{IntentAcademic})
		if err != nil {
			t.Fatal(err)
		}
		if gen.Model != model {
			t.Fatalf("model identity must be injectable, got %s", gen.Model)
		}
	}
	_ = fmt.Sprint() // keep fmt import if unused elsewhere
}
