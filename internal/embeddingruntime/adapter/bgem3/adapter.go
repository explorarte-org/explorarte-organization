package bgem3

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/embeddingruntime"
)

// Adapter implements embeddingruntime.OnlineAdapter against a local BGE-M3
// sidecar. There is no BatchAdapter implementation: batching is a remote,
// billed-API concept (see the gemini adapter) that has no meaning for a
// local, unbilled process — every call here is synchronous and bounded.
type Adapter struct {
	config   Config
	client   *http.Client
	baseURL  string
	sem      chan struct{}
	inSystem int64
	allowed  int64
	metrics  *Metrics
	now      func() time.Time
}

var _ embeddingruntime.OnlineAdapter = (*Adapter)(nil)

// New builds an Adapter, or returns (nil, nil) when config.Enabled is
// false — the same "absent adapter, not an error" convention the gemini
// adapter uses, so a caller can treat a disabled provider as simply
// unavailable rather than special-casing every call site.
func New(config Config) (*Adapter, error) {
	return newAdapter(config, nil, time.Now)
}

func newAdapter(config Config, client *http.Client, now func() time.Time) (*Adapter, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if !config.Enabled {
		return nil, nil
	}
	baseURL := config.BaseURL
	if client == nil {
		client = defaultHTTPClient(config.RequestTimeout)
		if strings.HasPrefix(config.BaseURL, "unix://") {
			socketPath := strings.TrimPrefix(config.BaseURL, "unix://")
			dialer := &net.Dialer{Timeout: 5 * time.Second}
			transport := client.Transport.(*http.Transport)
			transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
				return dialer.DialContext(ctx, "unix", socketPath)
			}
			baseURL = "http://unix"
		}
	}
	if now == nil {
		now = time.Now
	}
	return &Adapter{
		config: config, client: client, baseURL: baseURL,
		sem: make(chan struct{}, config.MaxConcurrency), allowed: int64(config.MaxConcurrency) + int64(config.MaxQueueDepth),
		metrics: &Metrics{}, now: now,
	}, nil
}

func (a *Adapter) Metrics() MetricsSnapshot { return a.metrics.Snapshot() }

// Embed validates the request against the pinned configuration (provider
// id, model revision, dimension, item/byte bounds), reserves a bounded
// queue slot (failing fast with ErrQueueFull rather than blocking
// unboundedly), and rejects the sidecar's response unless: the sidecar's
// reported model_revision AND artifact_sha256 both match the pinned
// Config (not just model_revision — a sidecar could serve different
// weights under the same revision string), the sidecar's reported
// prompt_template_version matches what the request asked for, and every
// returned vector matches by key, has the exact expected dimension, and
// contains no NaN/Inf component — R30's explicit requirement, not an
// incidental check.
func (a *Adapter) Embed(ctx context.Context, request embeddingruntime.EmbedRequest) (embeddingruntime.EmbedResponse, error) {
	if a == nil {
		return embeddingruntime.EmbedResponse{}, ErrDisabled
	}
	if request.ProviderID != ProviderID || request.ProviderModelID != a.config.ModelRevision || request.OutputDimensionality != a.config.ExpectedDimension {
		return embeddingruntime.EmbedResponse{}, embeddingruntime.ErrInvalidRequest
	}
	if len(request.Items) == 0 || len(request.Items) > a.config.MaxItemsPerRequest {
		return embeddingruntime.EmbedResponse{}, embeddingruntime.ErrInvalidRequest
	}
	wireItems := make([]wireItem, len(request.Items))
	for i, item := range request.Items {
		if item.Key == "" || item.Text == "" || !item.Task.Valid() {
			return embeddingruntime.EmbedResponse{}, embeddingruntime.ErrInvalidRequest
		}
		if len(item.Text) > a.config.MaxInputBytes {
			return embeddingruntime.EmbedResponse{}, fmt.Errorf("%w: item %q exceeds max input bytes", embeddingruntime.ErrInvalidRequest, item.Key)
		}
		wireItems[i] = wireItem{
			Key: item.Key, Text: item.Text, Task: string(item.Task),
			InputHash: inputHash(a.config.ModelRevision, request.PromptTemplateVersion, string(item.Task), item.Text),
		}
	}

	if !a.acquire() {
		a.metrics.recordQueueRejection()
		return embeddingruntime.EmbedResponse{}, ErrQueueFull
	}
	defer a.release()

	start := a.now()
	var decoded embedWireResponse
	err := a.doJSON(ctx, "POST", "/v1/embed", embedWireRequest{
		ModelRevision: a.config.ModelRevision, PromptTemplateVersion: request.PromptTemplateVersion,
		IdempotencyKey: requestIdempotencyKey(wireItems), Items: wireItems,
	}, &decoded)
	wall := a.now().Sub(start)
	if err != nil {
		a.metrics.recordCall(len(request.Items), wall, true)
		return embeddingruntime.EmbedResponse{}, err
	}
	if decoded.ModelRevision != a.config.ModelRevision || decoded.ArtifactSHA256 != a.config.ArtifactSHA256 {
		a.metrics.recordCall(len(request.Items), wall, true)
		return embeddingruntime.EmbedResponse{}, ErrModelIdentityDrift
	}
	// R31 hardening §7: see BGE_SIDECAR_CONTRACT_UPDATE_REQUIRED in
	// health.go -- tokenizer_revision/normalization/pooling are pinned in
	// Config but were never attested by the wire response before this
	// change, meaning a sidecar could silently use a different tokenizer
	// or pooling strategy and this adapter had no way to detect it.
	if decoded.TokenizerRevision != a.config.TokenizerRevision || decoded.Normalization != a.config.Normalization || decoded.Pooling != a.config.Pooling {
		a.metrics.recordCall(len(request.Items), wall, true)
		return embeddingruntime.EmbedResponse{}, fmt.Errorf("%w: sidecar attested tokenizer_revision=%q normalization=%q pooling=%q, want %q/%q/%q",
			ErrModelIdentityDrift, decoded.TokenizerRevision, decoded.Normalization, decoded.Pooling,
			a.config.TokenizerRevision, a.config.Normalization, a.config.Pooling)
	}
	if decoded.PromptTemplateVersion != request.PromptTemplateVersion {
		a.metrics.recordCall(len(request.Items), wall, true)
		return embeddingruntime.EmbedResponse{}, fmt.Errorf("%w: sidecar used prompt template %q, request asked for %q", ErrModelIdentityDrift, decoded.PromptTemplateVersion, request.PromptTemplateVersion)
	}
	if decoded.Dimension != a.config.ExpectedDimension {
		a.metrics.recordCall(len(request.Items), wall, true)
		return embeddingruntime.EmbedResponse{}, fmt.Errorf("%w: sidecar dimension %d, want %d", ErrInvalidVector, decoded.Dimension, a.config.ExpectedDimension)
	}
	if len(decoded.Results) != len(request.Items) {
		a.metrics.recordCall(len(request.Items), wall, true)
		return embeddingruntime.EmbedResponse{}, fmt.Errorf("%w: sent %d texts, got %d vectors", embeddingruntime.ErrResultCountMismatch, len(request.Items), len(decoded.Results))
	}
	byKey := make(map[string][]float32, len(decoded.Results))
	for _, r := range decoded.Results {
		byKey[r.Key] = r.Vector
	}
	results := make([]embeddingruntime.EmbedResult, len(request.Items))
	for i, item := range request.Items {
		vector, ok := byKey[item.Key]
		if !ok {
			a.metrics.recordCall(len(request.Items), wall, true)
			return embeddingruntime.EmbedResponse{}, fmt.Errorf("%w: missing result for key %q", embeddingruntime.ErrResultCountMismatch, item.Key)
		}
		if err := validateVector(vector, a.config.ExpectedDimension); err != nil {
			a.metrics.recordCall(len(request.Items), wall, true)
			return embeddingruntime.EmbedResponse{}, err
		}
		results[i] = embeddingruntime.EmbedResult{Key: item.Key, Vector: vector}
	}
	a.metrics.recordCall(len(request.Items), wall, false)
	return embeddingruntime.EmbedResponse{Results: results, InputTokens: 0}, nil
}

func (a *Adapter) acquire() bool {
	if atomic.AddInt64(&a.inSystem, 1) > a.allowed {
		atomic.AddInt64(&a.inSystem, -1)
		return false
	}
	a.sem <- struct{}{}
	return true
}

func (a *Adapter) release() {
	<-a.sem
	atomic.AddInt64(&a.inSystem, -1)
}

func (a *Adapter) doJSON(ctx context.Context, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}
	requestCtx := ctx
	cancel := func() {}
	if a.config.RequestTimeout > 0 {
		requestCtx, cancel = context.WithTimeout(ctx, a.config.RequestTimeout)
	}
	defer cancel()
	httpRequest, err := http.NewRequestWithContext(requestCtx, method, strings.TrimRight(a.baseURL, "/")+path, reader)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/json")
	response, err := a.client.Do(httpRequest)
	if err != nil {
		return fmt.Errorf("embeddingruntime bge-m3: request failed: %w", err)
	}
	defer response.Body.Close()
	responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, int64(a.config.MaxResponseBytes)+1))
	if readErr != nil {
		return fmt.Errorf("read response: %w", readErr)
	}
	if len(responseBody) > a.config.MaxResponseBytes {
		return fmt.Errorf("response exceeds %d bytes", a.config.MaxResponseBytes)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("embeddingruntime bge-m3: sidecar rejected request (status %d): %s", response.StatusCode, boundedPreview(responseBody))
	}
	if out != nil {
		if err := json.Unmarshal(responseBody, out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

func validateVector(vector []float32, expectedDimension int) error {
	if len(vector) != expectedDimension {
		return fmt.Errorf("%w: got dimension %d, want %d", ErrInvalidVector, len(vector), expectedDimension)
	}
	for _, component := range vector {
		if math.IsNaN(float64(component)) || math.IsInf(float64(component), 0) {
			return fmt.Errorf("%w: non-finite component", ErrInvalidVector)
		}
	}
	return nil
}

// inputHash never includes anything beyond a one-way digest of the text —
// it is the "input hash" R30's hardening requirement asks the wire
// protocol to carry, so the sidecar can dedupe/cache without this adapter
// (or its logs) ever handling the raw text more than once, in memory,
// for the single request that needs it.
func inputHash(modelRevision, promptTemplateVersion, task, text string) string {
	sum := sha256.Sum256([]byte(modelRevision + "\x00" + promptTemplateVersion + "\x00" + task + "\x00" + text))
	return hex.EncodeToString(sum[:])
}

func requestIdempotencyKey(items []wireItem) string {
	hash := sha256.New()
	for _, item := range items {
		hash.Write([]byte(item.Key + "\x00" + item.InputHash + "\x00"))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func boundedPreview(body []byte) string {
	const max = 300
	if len(body) > max {
		return string(body[:max])
	}
	return string(body)
}
