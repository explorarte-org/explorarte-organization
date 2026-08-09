package gemini

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/embeddingruntime"
)

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
			Embeddings:    []embeddingValue{{Values: []float32{0.1, 0.2, 0.3}}},
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
	if len(response.Results) != 1 || response.Results[0].Key != "chunk-1" || len(response.Results[0].Vector) != 3 {
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
