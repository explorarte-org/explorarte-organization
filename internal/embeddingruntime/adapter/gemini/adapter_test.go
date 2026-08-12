package gemini

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/embeddingruntime"
)

// fakeVector returns a dimension-correct, all-finite fake embedding for
// tests that exercise something other than vector validation itself (R31
// hardening §6 -- Embed now rejects any vector whose length does not match
// the request's OutputDimensionality, so a fixture vector must actually be
// that length or every such test would fail on the new check instead of
// testing what it was written to test).
func fakeVector(dimension int) []float32 {
	vector := make([]float32, dimension)
	for i := range vector {
		vector[i] = 0.001 * float32(i%1000)
	}
	return vector
}

func writeCredentialFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("test-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func newTestAdapter(t *testing.T, handler http.HandlerFunc) *Adapter {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	cfg := Config{
		Enabled: true, BaseURL: server.URL, CredentialFile: writeCredentialFile(t),
		RequestTimeout: 5 * time.Second, FailureThreshold: 2, OpenDuration: time.Minute, MaxResponseBytes: 1 << 20,
	}
	adapter, err := newAdapter(cfg, server.Client(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if adapter == nil {
		t.Fatal("expected non-nil adapter for enabled config")
	}
	return adapter
}

func TestEmbedSendsBothTaskTypeAndPromptPrefixAndParsesUsage(t *testing.T) {
	var capturedPath string
	var capturedBody batchEmbedContentsRequest
	adapter := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&capturedBody); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(batchEmbedContentsResponse{
			Embeddings:    []embeddingValue{{Values: fakeVector(768)}},
			UsageMetadata: usageMetadata{PromptTokenCount: 7},
		})
	})

	response, err := adapter.Embed(t.Context(), embeddingruntime.EmbedRequest{
		ProviderID: ProviderID, ProviderModelID: "gemini-embedding-2", OutputDimensionality: 768,
		PromptTemplateVersion: PromptTemplateV1,
		Items:                 []embeddingruntime.EmbedItem{{Key: "chunk-1", Text: "hola mundo", Task: embeddingruntime.TaskDocument}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if capturedPath != "/v1beta/models/gemini-embedding-2:batchEmbedContents" {
		t.Fatalf("path=%q", capturedPath)
	}
	if len(capturedBody.Requests) != 1 {
		t.Fatalf("requests=%d", len(capturedBody.Requests))
	}
	sent := capturedBody.Requests[0]
	if sent.EmbedContentConfig.TaskType != "RETRIEVAL_DOCUMENT" {
		t.Fatalf("taskType=%q", sent.EmbedContentConfig.TaskType)
	}
	if sent.Content.Parts[0].Text != "task: search result | document: hola mundo" {
		t.Fatalf("rendered text=%q", sent.Content.Parts[0].Text)
	}
	if len(response.Results) != 1 || response.Results[0].Key != "chunk-1" || len(response.Results[0].Vector) != 768 {
		t.Fatalf("results=%+v", response.Results)
	}
	if response.InputTokens != 7 {
		t.Fatalf("input tokens=%d want 7", response.InputTokens)
	}
}

func TestEmbedRejectsResultCountMismatch(t *testing.T) {
	adapter := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(batchEmbedContentsResponse{Embeddings: []embeddingValue{}})
	})
	_, err := adapter.Embed(t.Context(), embeddingruntime.EmbedRequest{
		ProviderID: ProviderID, ProviderModelID: "gemini-embedding-2", PromptTemplateVersion: PromptTemplateV1,
		Items: []embeddingruntime.EmbedItem{{Key: "chunk-1", Text: "x", Task: embeddingruntime.TaskQuery}},
	})
	if err == nil {
		t.Fatal("expected result count mismatch error")
	}
}

// R31 hardening §6: the adapter boundary must reject a wrong-dimension
// vector even though the response decoded successfully -- before this fix,
// a vector shorter than request.OutputDimensionality reached the caller
// unvalidated.
func TestEmbedRejectsWrongDimensionVector(t *testing.T) {
	adapter := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(batchEmbedContentsResponse{
			Embeddings: []embeddingValue{{Values: fakeVector(3)}},
		})
	})
	_, err := adapter.Embed(t.Context(), embeddingruntime.EmbedRequest{
		ProviderID: ProviderID, ProviderModelID: "gemini-embedding-2", OutputDimensionality: 768,
		PromptTemplateVersion: PromptTemplateV1,
		Items:                 []embeddingruntime.EmbedItem{{Key: "chunk-1", Text: "x", Task: embeddingruntime.TaskQuery}},
	})
	if err == nil {
		t.Fatal("expected wrong-dimension vector to be rejected")
	}
	if !errors.Is(err, ErrInvalidVector) {
		t.Fatalf("expected ErrInvalidVector, got: %v", err)
	}
}

// R31 hardening §6: a NaN component must never reach pgvector.
// R31 hardening §6: validateVector itself must reject a NaN component.
// This is a direct unit test of the pure function rather than a full
// HTTP+JSON round trip: Go's encoding/json (the decoder doJSON actually
// uses) has no valid JSON syntax that parses into a NaN float -- unlike
// bgem3's sidecar (Python-originated, where a bare `NaN` token is a common
// non-standard JSON extension bgem3's own decoder must and does handle),
// Gemini's real API returns standard-compliant JSON where this specific
// wire shape cannot occur. The check stays as defense-in-depth (a future
// decode path, or a provider response with a non-finite value smuggled
// through some other numeric edge case, must still be caught here) and is
// verified directly rather than asserting an unreachable wire scenario.
func TestValidateVectorRejectsNaNComponent(t *testing.T) {
	vector := fakeVector(768)
	vector[42] = float32(math.NaN())
	if err := validateVector(vector, 768); !errors.Is(err, ErrInvalidVector) {
		t.Fatalf("expected ErrInvalidVector for NaN component, got: %v", err)
	}
}

// R31 hardening §6: a +Inf component must never reach pgvector. Unlike
// NaN, this IS reachable through Gemini's real (standard-compliant) JSON
// wire format: a JSON number with an exponent large enough to overflow
// float64 (e.g. 1e400) is valid JSON syntax, and Go's decoder parses it
// into +Inf rather than rejecting it -- a real provider response could
// carry this, not just a hypothetical malformed one.
// R31 hardening §6: validateVector must reject a +Inf component. Also a
// direct unit test, for the same reason as the NaN case above: confirmed
// empirically that Go's json decoder rejects a number too large for
// float32 (json: cannot unmarshal number 1e400 into ... float32) BEFORE
// ever reaching validateVector, rather than truncating it to +Inf as a
// float64-intermediate decode might. The check still guards against a
// future decode path that narrows float64->float32 without erroring on
// overflow (a real, easy-to-introduce bug class), so it stays as
// defense-in-depth rather than dead code.
func TestValidateVectorRejectsInfComponent(t *testing.T) {
	vector := fakeVector(768)
	vector[7] = float32(math.Inf(1))
	if err := validateVector(vector, 768); !errors.Is(err, ErrInvalidVector) {
		t.Fatalf("expected ErrInvalidVector for +Inf component, got: %v", err)
	}
}

// R31 hardening §6: a valid, correctly-dimensioned, all-finite vector must
// still pass -- this is the regression guard against the above three tests
// becoming vacuously true.
func TestEmbedAcceptsValidVector(t *testing.T) {
	adapter := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(batchEmbedContentsResponse{Embeddings: []embeddingValue{{Values: fakeVector(768)}}})
	})
	response, err := adapter.Embed(t.Context(), embeddingruntime.EmbedRequest{
		ProviderID: ProviderID, ProviderModelID: "gemini-embedding-2", OutputDimensionality: 768,
		PromptTemplateVersion: PromptTemplateV1,
		Items:                 []embeddingruntime.EmbedItem{{Key: "chunk-1", Text: "x", Task: embeddingruntime.TaskQuery}},
	})
	if err != nil {
		t.Fatalf("expected a valid vector to be accepted, got: %v", err)
	}
	if len(response.Results) != 1 || len(response.Results[0].Vector) != 768 {
		t.Fatalf("results=%+v", response.Results)
	}
}

func TestEmbedMapsTextTooLongError(t *testing.T) {
	adapter := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(providerErrorEnvelope{Error: struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
			Status  string `json:"status"`
		}{Code: 400, Message: "input token count exceeds the maximum limit", Status: "INVALID_ARGUMENT"}})
	})
	_, err := adapter.Embed(t.Context(), embeddingruntime.EmbedRequest{
		ProviderID: ProviderID, ProviderModelID: "gemini-embedding-2", PromptTemplateVersion: PromptTemplateV1,
		Items: []embeddingruntime.EmbedItem{{Key: "chunk-1", Text: "x", Task: embeddingruntime.TaskQuery}},
	})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !isTextTooLongErrorTest(err) {
		t.Fatalf("err=%v want ErrTextTooLong", err)
	}
}

func isTextTooLongErrorTest(err error) bool {
	return err == embeddingruntime.ErrTextTooLong
}

func TestEmbedRejectsUnknownPromptTemplateVersion(t *testing.T) {
	adapter := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("must not reach the network with an invalid template version")
	})
	_, err := adapter.Embed(t.Context(), embeddingruntime.EmbedRequest{
		ProviderID: ProviderID, ProviderModelID: "gemini-embedding-2", PromptTemplateVersion: "unknown-version",
		Items: []embeddingruntime.EmbedItem{{Key: "chunk-1", Text: "x", Task: embeddingruntime.TaskQuery}},
	})
	if err == nil {
		t.Fatal("expected an error for unknown template version")
	}
}

func TestCircuitBreakerOpensAfterConsecutiveFailures(t *testing.T) {
	calls := 0
	adapter := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(providerErrorEnvelope{})
	})
	req := embeddingruntime.EmbedRequest{
		ProviderID: ProviderID, ProviderModelID: "gemini-embedding-2", PromptTemplateVersion: PromptTemplateV1,
		Items: []embeddingruntime.EmbedItem{{Key: "chunk-1", Text: "x", Task: embeddingruntime.TaskQuery}},
	}
	for i := 0; i < 2; i++ {
		if _, err := adapter.Embed(t.Context(), req); err == nil {
			t.Fatal("expected failure")
		}
	}
	if calls != 2 {
		t.Fatalf("calls=%d want 2 before breaker opens", calls)
	}
	if _, err := adapter.Embed(t.Context(), req); err == nil {
		t.Fatal("expected breaker-open error")
	}
	if calls != 2 {
		t.Fatalf("calls=%d want still 2 — breaker must short-circuit without another HTTP call", calls)
	}
}

func TestCreateBatchRejectsDuplicateKeys(t *testing.T) {
	adapter := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("must not reach the network with duplicate keys")
	})
	_, err := adapter.CreateBatch(t.Context(), embeddingruntime.CreateBatchRequest{
		ProviderID: ProviderID, ProviderModelID: "gemini-embedding-2", PromptTemplateVersion: PromptTemplateV1,
		Items: []embeddingruntime.EmbedItem{
			{Key: "dup", Text: "a", Task: embeddingruntime.TaskDocument},
			{Key: "dup", Text: "b", Task: embeddingruntime.TaskDocument},
		},
	})
	if err == nil {
		t.Fatal("expected duplicate key error")
	}
}

func TestCreateBatchAndGetBatchRoundTrip(t *testing.T) {
	var capturedPath string
	var capturedBody asyncBatchEmbedContentRequest
	adapter := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		if r.Method == http.MethodPost {
			if err := json.NewDecoder(r.Body).Decode(&capturedBody); err != nil {
				t.Fatal(err)
			}
			_ = json.NewEncoder(w).Encode(batchJobResource{Name: "batches/job-1"})
			return
		}
		_ = json.NewEncoder(w).Encode(batchJobResource{
			Name:     "batches/job-1",
			Metadata: batchJobMetadata{State: "JOB_STATE_RUNNING", BatchStats: batchJobStats{FailedRequestCount: "0"}},
		})
	})

	created, err := adapter.CreateBatch(t.Context(), embeddingruntime.CreateBatchRequest{
		ProviderID: ProviderID, ProviderModelID: "gemini-embedding-2", PromptTemplateVersion: PromptTemplateV1,
		Items: []embeddingruntime.EmbedItem{{Key: "chunk-1", Text: "x", Task: embeddingruntime.TaskDocument}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if capturedPath != "/v1beta/models/gemini-embedding-2:asyncBatchEmbedContent" {
		t.Fatalf("create path=%q", capturedPath)
	}
	if len(capturedBody.InputConfig.Requests.Requests) != 1 || capturedBody.InputConfig.Requests.Requests[0].Metadata.CustomKey != "chunk-1" {
		t.Fatalf("captured body=%+v", capturedBody)
	}
	if created.ProviderJobName != "batches/job-1" {
		t.Fatalf("job name=%q", created.ProviderJobName)
	}

	state, err := adapter.GetBatch(t.Context(), created.ProviderJobName)
	if err != nil {
		t.Fatal(err)
	}
	if capturedPath != "/v1beta/batches/job-1" {
		t.Fatalf("get path=%q", capturedPath)
	}
	if state.Status != embeddingruntime.BatchJobRunning || state.Status.Terminal() {
		t.Fatalf("state=%+v", state)
	}
}

func TestReadBatchResultsRejectsNonTerminalJob(t *testing.T) {
	adapter := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(batchJobResource{
			Name: "batches/job-2", Metadata: batchJobMetadata{State: "JOB_STATE_RUNNING"},
		})
	})
	if _, err := adapter.ReadBatchResults(t.Context(), "batches/job-2"); err != embeddingruntime.ErrJobNotReady {
		t.Fatalf("err=%v want ErrJobNotReady", err)
	}
}

func TestReadBatchResultsHandlesMixedSuccessAndPerItemErrors(t *testing.T) {
	adapter := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(batchJobResource{
			Name:     "batches/job-3",
			Metadata: batchJobMetadata{State: "JOB_STATE_SUCCEEDED", BatchStats: batchJobStats{FailedRequestCount: "1"}},
			Response: &batchJobOutput{InlinedResponses: []batchInlineResponseItem{
				{Metadata: batchItemMetadata{CustomKey: "chunk-1"}, Response: &batchInlineEmbedContent{Embedding: embeddingValue{Values: []float32{0.1}}}},
				{Metadata: batchItemMetadata{CustomKey: "chunk-2"}, Error: &batchJobError{Message: "input token count exceeds the maximum limit"}},
			}},
		})
	})
	results, err := adapter.ReadBatchResults(t.Context(), "batches/job-3")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("results=%d want 2", len(results))
	}
	if results[0].Key != "chunk-1" || len(results[0].Vector) != 1 || results[0].Err != "" {
		t.Fatalf("result[0]=%+v", results[0])
	}
	if results[1].Key != "chunk-2" || results[1].Vector != nil || results[1].Err == "" {
		t.Fatalf("result[1]=%+v", results[1])
	}
}

func TestConfigValidateRejectsDisallowedBaseURL(t *testing.T) {
	cfg := Config{Enabled: true, BaseURL: "https://generativelanguage.googleapis.com/v1beta", CredentialFile: "/tmp/x", RequestTimeout: time.Second, FailureThreshold: 1, OpenDuration: time.Second, MaxResponseBytes: 2048}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected rejection of a base url carrying a path")
	}
}

func TestDisabledConfigYieldsNilAdapter(t *testing.T) {
	cfg := Config{Enabled: false, RequestTimeout: time.Second, FailureThreshold: 1, OpenDuration: time.Second, MaxResponseBytes: 2048}
	adapter, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if adapter != nil {
		t.Fatal("expected nil adapter for disabled config")
	}
}

func TestEmbedSendsInlineDataForMediaItem(t *testing.T) {
	var capturedBody batchEmbedContentsRequest
	adapter := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&capturedBody); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(batchEmbedContentsResponse{
			Embeddings: []embeddingValue{{Values: fakeVector(768)}},
		})
	})

	pdfBytes := []byte("%PDF-1.4 fake pdf bytes for test")
	response, err := adapter.Embed(t.Context(), embeddingruntime.EmbedRequest{
		ProviderID: ProviderID, ProviderModelID: "gemini-embedding-2", OutputDimensionality: 768,
		PromptTemplateVersion: PromptTemplateV1,
		Items: []embeddingruntime.EmbedItem{
			{Key: "paper-1-p1", MimeType: "application/pdf", Data: pdfBytes, Task: embeddingruntime.TaskDocument},
		},
	})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(capturedBody.Requests) != 1 {
		t.Fatalf("requests=%d", len(capturedBody.Requests))
	}
	sent := capturedBody.Requests[0]
	if sent.Content.Parts[0].Text != "" {
		t.Fatalf("expected empty text part for media item, got %q", sent.Content.Parts[0].Text)
	}
	if sent.Content.Parts[0].InlineData == nil {
		t.Fatalf("expected inline_data part for media item")
	}
	if sent.Content.Parts[0].InlineData.MimeType != "application/pdf" {
		t.Fatalf("mimeType=%q", sent.Content.Parts[0].InlineData.MimeType)
	}
	decoded, err := base64.StdEncoding.DecodeString(sent.Content.Parts[0].InlineData.Data)
	if err != nil {
		t.Fatalf("decode base64: %v", err)
	}
	if string(decoded) != string(pdfBytes) {
		t.Fatalf("round-tripped bytes = %q, want %q", decoded, pdfBytes)
	}
	if sent.EmbedContentConfig.TaskType != "RETRIEVAL_DOCUMENT" {
		t.Fatalf("taskType=%q", sent.EmbedContentConfig.TaskType)
	}
	if len(response.Results) != 1 || response.Results[0].Key != "paper-1-p1" {
		t.Fatalf("results=%+v", response.Results)
	}
}

func TestEmbedRejectsUnsupportedMediaMimeType(t *testing.T) {
	adapter := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("must not send a request for an invalid item")
	})
	_, err := adapter.Embed(t.Context(), embeddingruntime.EmbedRequest{
		ProviderID: ProviderID, ProviderModelID: "gemini-embedding-2", OutputDimensionality: 768,
		PromptTemplateVersion: PromptTemplateV1,
		Items: []embeddingruntime.EmbedItem{
			{Key: "x", MimeType: "application/zip", Data: []byte("PK\x03\x04"), Task: embeddingruntime.TaskDocument},
		},
	})
	if err == nil {
		t.Fatal("expected error for unsupported MIME type")
	}
}

func TestEmbedRejectsOversizedMediaItem(t *testing.T) {
	adapter := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("must not send a request for an oversized item")
	})
	_, err := adapter.Embed(t.Context(), embeddingruntime.EmbedRequest{
		ProviderID: ProviderID, ProviderModelID: "gemini-embedding-2", OutputDimensionality: 768,
		PromptTemplateVersion: PromptTemplateV1,
		Items: []embeddingruntime.EmbedItem{
			{Key: "x", MimeType: "application/pdf", Data: make([]byte, maxMediaBytes+1), Task: embeddingruntime.TaskDocument},
		},
	})
	if err == nil {
		t.Fatal("expected error for oversized media item")
	}
}

func TestEmbedRejectsItemWithBothTextAndMedia(t *testing.T) {
	adapter := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("must not send a request for an invalid item")
	})
	_, err := adapter.Embed(t.Context(), embeddingruntime.EmbedRequest{
		ProviderID: ProviderID, ProviderModelID: "gemini-embedding-2", OutputDimensionality: 768,
		PromptTemplateVersion: PromptTemplateV1,
		Items: []embeddingruntime.EmbedItem{
			{Key: "x", Text: "hola", MimeType: "application/pdf", Data: []byte("%PDF"), Task: embeddingruntime.TaskDocument},
		},
	})
	if err == nil {
		t.Fatal("expected error for item with both text and media set")
	}
}

func TestEmbedRejectsItemWithNeitherTextNorMedia(t *testing.T) {
	adapter := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("must not send a request for an invalid item")
	})
	_, err := adapter.Embed(t.Context(), embeddingruntime.EmbedRequest{
		ProviderID: ProviderID, ProviderModelID: "gemini-embedding-2", OutputDimensionality: 768,
		PromptTemplateVersion: PromptTemplateV1,
		Items: []embeddingruntime.EmbedItem{
			{Key: "x", Task: embeddingruntime.TaskDocument},
		},
	})
	if err == nil {
		t.Fatal("expected error for item with neither text nor media set")
	}
}

func TestCreateBatchSendsInlineDataForMediaItem(t *testing.T) {
	var capturedBody asyncBatchEmbedContentRequest
	adapter := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&capturedBody); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(batchJobResource{Name: "batches/test-job-1"})
	})

	pdfBytes := []byte("%PDF-1.4 fake pdf bytes for batch test")
	response, err := adapter.CreateBatch(t.Context(), embeddingruntime.CreateBatchRequest{
		ProviderID: ProviderID, ProviderModelID: "gemini-embedding-2", OutputDimensionality: 768,
		PromptTemplateVersion: PromptTemplateV1,
		Items: []embeddingruntime.EmbedItem{
			{Key: "paper-1-p1", MimeType: "application/pdf", Data: pdfBytes, Task: embeddingruntime.TaskDocument},
		},
	})
	if err != nil {
		t.Fatalf("CreateBatch: %v", err)
	}
	if response.ProviderJobName != "batches/test-job-1" {
		t.Fatalf("job name=%q", response.ProviderJobName)
	}
	requests := capturedBody.InputConfig.Requests.Requests
	if len(requests) != 1 {
		t.Fatalf("requests=%d", len(requests))
	}
	part := requests[0].Request.Content.Parts[0]
	if part.InlineData == nil || part.InlineData.MimeType != "application/pdf" {
		t.Fatalf("part=%+v", part)
	}
}

func TestEmbedSendsAPIKeyViaGoogHeaderNotBearer(t *testing.T) {
	var gotGoogHeader, gotAuthHeader string
	adapter := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		gotGoogHeader = r.Header.Get("x-goog-api-key")
		gotAuthHeader = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(batchEmbedContentsResponse{
			Embeddings: []embeddingValue{{Values: fakeVector(768)}},
		})
	})
	_, err := adapter.Embed(t.Context(), embeddingruntime.EmbedRequest{
		ProviderID: ProviderID, ProviderModelID: "gemini-embedding-2", OutputDimensionality: 768,
		PromptTemplateVersion: PromptTemplateV1,
		Items:                 []embeddingruntime.EmbedItem{{Key: "a", Text: "hola", Task: embeddingruntime.TaskDocument}},
	})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if gotGoogHeader != "test-token" {
		t.Fatalf("x-goog-api-key = %q, want the credential file's token", gotGoogHeader)
	}
	if gotAuthHeader != "" {
		t.Fatalf("Authorization header must not be set for API-key auth, got %q", gotAuthHeader)
	}
}
