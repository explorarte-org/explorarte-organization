package bgem3

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/embeddingruntime"
)

const (
	testModelRevision  = "bge-m3-2024-06"
	testArtifactSHA256 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testDimension      = 8 // small for fast tests; production config uses 1024
)

func baseTestConfig(baseURL string) Config {
	return Config{
		Enabled: true, BaseURL: baseURL, ModelRevision: testModelRevision, ArtifactSHA256: testArtifactSHA256,
		TokenizerRevision: "bge-m3-tokenizer-2024-06", Normalization: "l2", Pooling: "cls",
		ExpectedDimension: testDimension, PromptTemplateVersion: PromptTemplateV1, RequestTimeout: 2 * time.Second,
		MaxConcurrency: 1, MaxQueueDepth: 0, MaxInputBytes: 1024, MaxItemsPerRequest: 4, MaxResponseBytes: 1 << 16,
	}
}

func flatVector(dim int, value float32) []float32 {
	v := make([]float32, dim)
	for i := range v {
		v[i] = value
	}
	return v
}

func fakeEmbedHandler(t *testing.T, respond func(req embedWireRequest) (int, embedWireResponse)) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embed" || r.Method != http.MethodPost {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var req embedWireRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		status, resp := respond(req)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(resp)
	}
}

func validEmbedRequest(items ...embeddingruntime.EmbedItem) embeddingruntime.EmbedRequest {
	return embeddingruntime.EmbedRequest{
		ProviderID: ProviderID, ProviderModelID: testModelRevision, OutputDimensionality: testDimension,
		PromptTemplateVersion: PromptTemplateV1, Items: items,
	}
}

func TestNewReturnsNilWhenDisabled(t *testing.T) {
	// A disabled config still needs its non-identity bounds (timeouts,
	// concurrency, size limits) to be within range — the same rule
	// LoadConfig's defaults satisfy for a real caller — only the pinned
	// model identity (revision/artifact hash/endpoint) is exempt while
	// disabled, matching gemini's Config.Validate convention.
	cfg := baseTestConfig("")
	cfg.Enabled = false
	adapter, err := New(cfg)
	if err != nil || adapter != nil {
		t.Fatalf("adapter=%v err=%v, want nil/nil", adapter, err)
	}
}

func TestConfigValidateRejectsNonLoopbackURL(t *testing.T) {
	cfg := baseTestConfig("http://203.0.113.5:8080")
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected non-loopback base url to be rejected")
	}
}

func TestConfigValidateAcceptsLoopbackAndUnixSocket(t *testing.T) {
	for _, url := range []string{"http://127.0.0.1:8091", "http://localhost:8091", "unix:///tmp/bge-m3.sock"} {
		cfg := baseTestConfig(url)
		if err := cfg.Validate(); err != nil {
			t.Fatalf("url=%s: unexpected error: %v", url, err)
		}
	}
}

func TestConfigValidateRejectsShortOrUppercaseArtifactHash(t *testing.T) {
	cfg := baseTestConfig("http://127.0.0.1:8091")
	cfg.ArtifactSHA256 = "not-a-hash"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected invalid artifact hash to be rejected")
	}
	cfg.ArtifactSHA256 = strings.ToUpper(testArtifactSHA256)
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected uppercase artifact hash to be rejected")
	}
}

func TestEmbedHappyPath(t *testing.T) {
	server := httptest.NewServer(fakeEmbedHandler(t, func(req embedWireRequest) (int, embedWireResponse) {
		if req.ModelRevision != testModelRevision {
			t.Fatalf("unexpected model revision in request: %s", req.ModelRevision)
		}
		results := make([]wireResult, len(req.Items))
		for i, item := range req.Items {
			results[i] = wireResult{Key: item.Key, Vector: flatVector(testDimension, 0.5)}
		}
		return http.StatusOK, embedWireResponse{ModelRevision: testModelRevision, Dimension: testDimension, Results: results, TextCount: len(req.Items)}
	}))
	defer server.Close()

	adapter, err := New(baseTestConfig(server.URL))
	if err != nil || adapter == nil {
		t.Fatalf("adapter=%v err=%v", adapter, err)
	}
	response, err := adapter.Embed(context.Background(), validEmbedRequest(
		embeddingruntime.EmbedItem{Key: "a", Text: "hola mundo", Task: embeddingruntime.TaskDocument},
		embeddingruntime.EmbedItem{Key: "b", Text: "fallo numero veinte", Task: embeddingruntime.TaskQuery},
	))
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Results) != 2 || response.Results[0].Key != "a" || len(response.Results[0].Vector) != testDimension {
		t.Fatalf("response=%+v", response)
	}
	snapshot := adapter.Metrics()
	if snapshot.Calls != 1 || snapshot.Failures != 0 || snapshot.TotalTexts != 2 {
		t.Fatalf("metrics=%+v", snapshot)
	}
}

func TestEmbedRejectsWrongDimension(t *testing.T) {
	server := httptest.NewServer(fakeEmbedHandler(t, func(req embedWireRequest) (int, embedWireResponse) {
		results := []wireResult{{Key: req.Items[0].Key, Vector: flatVector(testDimension-1, 0.1)}}
		return http.StatusOK, embedWireResponse{ModelRevision: testModelRevision, Dimension: testDimension - 1, Results: results}
	}))
	defer server.Close()
	adapter, _ := New(baseTestConfig(server.URL))
	if _, err := adapter.Embed(context.Background(), validEmbedRequest(embeddingruntime.EmbedItem{Key: "a", Text: "x", Task: embeddingruntime.TaskDocument})); err == nil {
		t.Fatal("expected dimension mismatch to be rejected")
	}
}

func TestValidateVectorRejectsNonFiniteAndWrongShape(t *testing.T) {
	if err := validateVector(flatVector(testDimension, 0.1), testDimension); err != nil {
		t.Fatalf("unexpected error for a well-formed vector: %v", err)
	}
	if err := validateVector(flatVector(testDimension-1, 0.1), testDimension); err == nil {
		t.Fatal("expected wrong-dimension vector to be rejected")
	}
	if err := validateVector(nil, testDimension); err == nil {
		t.Fatal("expected empty vector to be rejected")
	}
	nan := flatVector(testDimension, 0.1)
	nan[2] = float32(math.NaN())
	if err := validateVector(nan, testDimension); err == nil {
		t.Fatal("expected NaN component to be rejected")
	}
	inf := flatVector(testDimension, 0.1)
	inf[3] = float32(math.Inf(1))
	if err := validateVector(inf, testDimension); err == nil {
		t.Fatal("expected +Inf component to be rejected")
	}
}

// TestEmbedRejectsMalformedNumericResponse exercises the same rejection
// from the wire: a sidecar that emits a non-JSON-numeric token (as some
// non-Go encoders do for NaN/Infinity, since the JSON spec disallows both)
// must fail decode, never silently produce a zero-valued vector that would
// pass validateVector by coincidence.
func TestEmbedRejectsMalformedNumericResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model_revision":"` + testModelRevision + `","dimension":8,"results":[{"key":"a","vector":[NaN,0.1,0.1,0.1,0.1,0.1,0.1,0.1]}]}`))
	}))
	defer server.Close()
	adapter, _ := New(baseTestConfig(server.URL))
	if _, err := adapter.Embed(context.Background(), validEmbedRequest(embeddingruntime.EmbedItem{Key: "a", Text: "x", Task: embeddingruntime.TaskDocument})); err == nil {
		t.Fatal("expected malformed numeric response to be rejected")
	}
}

func TestEmbedRejectsResultCountMismatch(t *testing.T) {
	server := httptest.NewServer(fakeEmbedHandler(t, func(req embedWireRequest) (int, embedWireResponse) {
		return http.StatusOK, embedWireResponse{ModelRevision: testModelRevision, Dimension: testDimension, Results: nil}
	}))
	defer server.Close()
	adapter, _ := New(baseTestConfig(server.URL))
	if _, err := adapter.Embed(context.Background(), validEmbedRequest(embeddingruntime.EmbedItem{Key: "a", Text: "x", Task: embeddingruntime.TaskDocument})); err == nil {
		t.Fatal("expected result count mismatch to be rejected")
	}
}

func TestEmbedRejectsMissingKeyInResponse(t *testing.T) {
	server := httptest.NewServer(fakeEmbedHandler(t, func(req embedWireRequest) (int, embedWireResponse) {
		return http.StatusOK, embedWireResponse{ModelRevision: testModelRevision, Dimension: testDimension, Results: []wireResult{{Key: "wrong-key", Vector: flatVector(testDimension, 0.1)}}}
	}))
	defer server.Close()
	adapter, _ := New(baseTestConfig(server.URL))
	if _, err := adapter.Embed(context.Background(), validEmbedRequest(embeddingruntime.EmbedItem{Key: "a", Text: "x", Task: embeddingruntime.TaskDocument})); err == nil {
		t.Fatal("expected missing key to be rejected")
	}
}

func TestEmbedRejectsModelIdentityDrift(t *testing.T) {
	server := httptest.NewServer(fakeEmbedHandler(t, func(req embedWireRequest) (int, embedWireResponse) {
		return http.StatusOK, embedWireResponse{ModelRevision: "some-other-revision", Dimension: testDimension, Results: []wireResult{{Key: req.Items[0].Key, Vector: flatVector(testDimension, 0.1)}}}
	}))
	defer server.Close()
	adapter, _ := New(baseTestConfig(server.URL))
	if _, err := adapter.Embed(context.Background(), validEmbedRequest(embeddingruntime.EmbedItem{Key: "a", Text: "x", Task: embeddingruntime.TaskDocument})); err == nil {
		t.Fatal("expected model identity drift to be rejected")
	}
}

func TestEmbedRejectsOversizedInput(t *testing.T) {
	server := httptest.NewServer(fakeEmbedHandler(t, func(req embedWireRequest) (int, embedWireResponse) {
		t.Fatal("sidecar should never be called for an oversized item")
		return http.StatusOK, embedWireResponse{}
	}))
	defer server.Close()
	adapter, _ := New(baseTestConfig(server.URL))
	oversized := strings.Repeat("x", 2000)
	if _, err := adapter.Embed(context.Background(), validEmbedRequest(embeddingruntime.EmbedItem{Key: "a", Text: oversized, Task: embeddingruntime.TaskDocument})); err == nil {
		t.Fatal("expected oversized item to be rejected before any request")
	}
}

func TestEmbedRejectsProviderOrDimensionMismatch(t *testing.T) {
	adapter, _ := New(baseTestConfig("http://127.0.0.1:1"))
	bad := validEmbedRequest(embeddingruntime.EmbedItem{Key: "a", Text: "x", Task: embeddingruntime.TaskDocument})
	bad.ProviderID = "gemini"
	if _, err := adapter.Embed(context.Background(), bad); err == nil {
		t.Fatal("expected provider id mismatch to be rejected")
	}
	bad2 := validEmbedRequest(embeddingruntime.EmbedItem{Key: "a", Text: "x", Task: embeddingruntime.TaskDocument})
	bad2.OutputDimensionality = testDimension + 1
	if _, err := adapter.Embed(context.Background(), bad2); err == nil {
		t.Fatal("expected dimension mismatch to be rejected")
	}
}

func TestEmbedBoundedQueueRejectsBeyondCapacity(t *testing.T) {
	release := make(chan struct{})
	var inFlight int32
	server := httptest.NewServer(fakeEmbedHandler(t, func(req embedWireRequest) (int, embedWireResponse) {
		atomic.AddInt32(&inFlight, 1)
		<-release
		return http.StatusOK, embedWireResponse{ModelRevision: testModelRevision, Dimension: testDimension, Results: []wireResult{{Key: req.Items[0].Key, Vector: flatVector(testDimension, 0.1)}}}
	}))
	defer server.Close()

	cfg := baseTestConfig(server.URL)
	cfg.MaxConcurrency = 1
	cfg.MaxQueueDepth = 0
	adapter, _ := New(cfg)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = adapter.Embed(context.Background(), validEmbedRequest(embeddingruntime.EmbedItem{Key: "a", Text: "x", Task: embeddingruntime.TaskDocument}))
	}()
	// Wait for the first call to actually reach the sidecar so the second
	// call below finds the single concurrency slot occupied.
	for atomic.LoadInt32(&inFlight) == 0 {
		time.Sleep(time.Millisecond)
	}
	_, err := adapter.Embed(context.Background(), validEmbedRequest(embeddingruntime.EmbedItem{Key: "b", Text: "y", Task: embeddingruntime.TaskDocument}))
	if err != ErrQueueFull {
		t.Fatalf("err=%v, want ErrQueueFull", err)
	}
	close(release)
	wg.Wait()
	snapshot := adapter.Metrics()
	if snapshot.QueueRejections != 1 {
		t.Fatalf("queue rejections=%d, want 1", snapshot.QueueRejections)
	}
}

func TestEmbedRespectsContextCancellation(t *testing.T) {
	block := make(chan struct{})
	server := httptest.NewServer(fakeEmbedHandler(t, func(req embedWireRequest) (int, embedWireResponse) {
		<-block
		return http.StatusOK, embedWireResponse{}
	}))
	// server.Close() blocks until the in-flight handler goroutine returns,
	// which only happens once block is closed — deferred in this order so
	// close(block) runs first (defers are LIFO) and server.Close() never
	// waits on itself.
	defer server.Close()
	defer close(block)
	adapter, _ := New(baseTestConfig(server.URL))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := adapter.Embed(ctx, validEmbedRequest(embeddingruntime.EmbedItem{Key: "a", Text: "x", Task: embeddingruntime.TaskDocument})); err == nil {
		t.Fatal("expected context deadline to cause a failure")
	}
}

func TestHealthyRejectsIdentityDrift(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Health{Status: "ready", ModelRevision: "different-revision", ArtifactSHA256: testArtifactSHA256, Dimension: testDimension})
	}))
	defer server.Close()
	adapter, _ := New(baseTestConfig(server.URL))
	if _, err := adapter.Healthy(context.Background()); err != ErrModelIdentityDrift {
		t.Fatalf("err=%v, want ErrModelIdentityDrift", err)
	}
}

func TestHealthyAcceptsMatchingIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/health" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Health{Status: "ready", ModelRevision: testModelRevision, ArtifactSHA256: testArtifactSHA256, Dimension: testDimension, PeakRSSBytes: 123, CPUTimeMS: 45})
	}))
	defer server.Close()
	adapter, _ := New(baseTestConfig(server.URL))
	health, err := adapter.Healthy(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !health.Ready() || health.PeakRSSBytes != 123 {
		t.Fatalf("health=%+v", health)
	}
}
