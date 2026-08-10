package gemini

import (
	"context"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/embeddingruntime"
)

// The asyncBatchEmbedContent / batches/* surface below is the part of this
// adapter with the least direct confirmation: Google's public
// documentation describes it primarily through its Python/JS SDKs
// (batches.create_embeddings) rather than a fully worked REST example for
// embeddings specifically. The shapes here follow Google's general
// long-running-operation resource pattern (name/metadata/done/response),
// which is well precedented across Google Cloud APIs, and the specific
// field names (inputConfig.requests.requests[].request/.metadata.customKey
// for create, response.inlinedResponses[] for results) come from the
// closest available documentation fragments at implementation time. This
// is the single most important thing to verify against a real API key
// before this path handles real production reindex traffic — see R29's
// plan file, "Puntos a reconfirmar durante la implementación".

type batchRequestItem struct {
	Request  embedContentRequest `json:"request"`
	Metadata batchItemMetadata   `json:"metadata"`
}

type batchItemMetadata struct {
	CustomKey string `json:"customKey"`
}

type asyncBatchEmbedContentRequest struct {
	DisplayName string           `json:"displayName,omitempty"`
	InputConfig batchInputConfig `json:"inputConfig"`
}

type batchInputConfig struct {
	Requests batchInlineRequests `json:"requests"`
}

type batchInlineRequests struct {
	Requests []batchRequestItem `json:"requests"`
}

type batchJobResource struct {
	Name     string           `json:"name"`
	Done     bool             `json:"done"`
	Metadata batchJobMetadata `json:"metadata"`
	Response *batchJobOutput  `json:"response,omitempty"`
	Error    *batchJobError   `json:"error,omitempty"`
}

type batchJobMetadata struct {
	State      string        `json:"state"`
	BatchStats batchJobStats `json:"batchStats"`
}

type batchJobStats struct {
	// Google's batchStats counters are documented as strings in
	// long-running-operation resources (protobuf int64 over JSON), not
	// JSON numbers — decoded via strconv, never assumed to be a bare number.
	FailedRequestCount string `json:"failedRequestCount"`
}

type batchJobError struct {
	Message string `json:"message"`
}

type batchJobOutput struct {
	InlinedResponses []batchInlineResponseItem `json:"inlinedResponses"`
}

type batchInlineResponseItem struct {
	Metadata batchItemMetadata        `json:"metadata"`
	Response *batchInlineEmbedContent `json:"response,omitempty"`
	Error    *batchJobError           `json:"error,omitempty"`
}

type batchInlineEmbedContent struct {
	Embedding embeddingValue `json:"embedding"`
}

func mapJobState(state string) embeddingruntime.BatchJobStatus {
	switch state {
	case "JOB_STATE_PENDING":
		return embeddingruntime.BatchJobPending
	case "JOB_STATE_RUNNING":
		return embeddingruntime.BatchJobRunning
	case "JOB_STATE_SUCCEEDED":
		return embeddingruntime.BatchJobSucceeded
	case "JOB_STATE_FAILED":
		return embeddingruntime.BatchJobFailed
	case "JOB_STATE_CANCELLED":
		return embeddingruntime.BatchJobCancelled
	case "JOB_STATE_EXPIRED":
		return embeddingruntime.BatchJobExpired
	default:
		return ""
	}
}

// CreateBatch submits an asynchronous embeddings job. Google's Batch API
// documents job creation as NOT idempotent — see
// embeddingruntime.CreateBatchResponse's doc comment. If this call returns
// an error indistinguishable from "the request may or may not have reached
// Google" (a transport failure, not a decoded error response), the caller
// must treat the outcome as embeddingruntime.ErrJobAmbiguous territory and
// resolve it by listing/inspecting jobs out of band rather than retrying
// blindly — CreateBatch itself cannot tell the difference from inside a
// single call.
func (a *Adapter) CreateBatch(ctx context.Context, request embeddingruntime.CreateBatchRequest) (embeddingruntime.CreateBatchResponse, error) {
	if request.ProviderID != ProviderID || request.ProviderModelID == "" || len(request.Items) == 0 {
		return embeddingruntime.CreateBatchResponse{}, embeddingruntime.ErrInvalidRequest
	}
	items := make([]batchRequestItem, 0, len(request.Items))
	seenKeys := make(map[string]struct{}, len(request.Items))
	for _, item := range request.Items {
		if !item.Valid() {
			return embeddingruntime.CreateBatchResponse{}, embeddingruntime.ErrInvalidRequest
		}
		if _, exists := seenKeys[item.Key]; exists {
			return embeddingruntime.CreateBatchResponse{}, fmt.Errorf("%w: duplicate item key %q", embeddingruntime.ErrInvalidRequest, item.Key)
		}
		seenKeys[item.Key] = struct{}{}
		var part contentPart
		if item.IsMedia() {
			if !SupportedMediaMimeTypes[item.MimeType] {
				return embeddingruntime.CreateBatchResponse{}, fmt.Errorf("%w: unsupported media MIME type %q", embeddingruntime.ErrInvalidRequest, item.MimeType)
			}
			if len(item.Data) > maxMediaBytes {
				return embeddingruntime.CreateBatchResponse{}, fmt.Errorf("%w: media item %q exceeds maximum inline size", embeddingruntime.ErrInvalidRequest, item.Key)
			}
			part = contentPart{InlineData: &inlineDataPart{MimeType: item.MimeType, Data: base64.StdEncoding.EncodeToString(item.Data)}}
		} else {
			rendered, err := renderPrompt(request.PromptTemplateVersion, item.Task, item.Text)
			if err != nil {
				return embeddingruntime.CreateBatchResponse{}, err
			}
			part = contentPart{Text: rendered}
		}
		items = append(items, batchRequestItem{
			Request: embedContentRequest{
				Model:   "models/" + request.ProviderModelID,
				Content: content{Parts: []contentPart{part}},
				EmbedContentConfig: embedContentConfig{
					TaskType: taskTypeField(item.Task), OutputDimensionality: request.OutputDimensionality,
				},
			},
			Metadata: batchItemMetadata{CustomKey: item.Key},
		})
	}
	body := asyncBatchEmbedContentRequest{
		DisplayName: "r29-embedding-batch",
		InputConfig: batchInputConfig{Requests: batchInlineRequests{Requests: items}},
	}
	var decoded batchJobResource
	path := "/v1beta/models/" + request.ProviderModelID + ":asyncBatchEmbedContent"
	if _, err := a.doJSON(ctx, "POST", path, body, &decoded); err != nil {
		return embeddingruntime.CreateBatchResponse{}, err
	}
	if decoded.Name == "" {
		return embeddingruntime.CreateBatchResponse{}, fmt.Errorf("embeddingruntime gemini: batch create response missing job name")
	}
	return embeddingruntime.CreateBatchResponse{ProviderJobName: decoded.Name}, nil
}

func (a *Adapter) GetBatch(ctx context.Context, providerJobName string) (embeddingruntime.BatchJobState, error) {
	if strings.TrimSpace(providerJobName) == "" {
		return embeddingruntime.BatchJobState{}, embeddingruntime.ErrInvalidRequest
	}
	var decoded batchJobResource
	if _, err := a.doJSON(ctx, "GET", "/v1beta/"+providerJobName, nil, &decoded); err != nil {
		return embeddingruntime.BatchJobState{}, err
	}
	status := mapJobState(decoded.Metadata.State)
	if !status.Valid() {
		return embeddingruntime.BatchJobState{}, fmt.Errorf("embeddingruntime gemini: unrecognized job state %q", decoded.Metadata.State)
	}
	var failedCount int64
	if raw := strings.TrimSpace(decoded.Metadata.BatchStats.FailedRequestCount); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return embeddingruntime.BatchJobState{}, fmt.Errorf("embeddingruntime gemini: invalid failedRequestCount %q: %w", raw, err)
		}
		failedCount = parsed
	}
	return embeddingruntime.BatchJobState{ProviderJobName: decoded.Name, Status: status, FailedItemCount: failedCount}, nil
}

func (a *Adapter) CancelBatch(ctx context.Context, providerJobName string) error {
	if strings.TrimSpace(providerJobName) == "" {
		return embeddingruntime.ErrInvalidRequest
	}
	_, err := a.doJSON(ctx, "POST", "/v1beta/"+providerJobName+":cancel", struct{}{}, nil)
	return err
}

func (a *Adapter) ReadBatchResults(ctx context.Context, providerJobName string) ([]embeddingruntime.BatchItemResult, error) {
	if strings.TrimSpace(providerJobName) == "" {
		return nil, embeddingruntime.ErrInvalidRequest
	}
	var decoded batchJobResource
	if _, err := a.doJSON(ctx, "GET", "/v1beta/"+providerJobName, nil, &decoded); err != nil {
		return nil, err
	}
	status := mapJobState(decoded.Metadata.State)
	if !status.Terminal() {
		return nil, embeddingruntime.ErrJobNotReady
	}
	if status != embeddingruntime.BatchJobSucceeded {
		return nil, fmt.Errorf("embeddingruntime gemini: job %s ended in state %s, no results to read", providerJobName, status)
	}
	if decoded.Response == nil {
		return nil, fmt.Errorf("embeddingruntime gemini: job %s reported succeeded with no response payload", providerJobName)
	}
	results := make([]embeddingruntime.BatchItemResult, 0, len(decoded.Response.InlinedResponses))
	for _, item := range decoded.Response.InlinedResponses {
		if item.Metadata.CustomKey == "" {
			return nil, fmt.Errorf("embeddingruntime gemini: batch result item missing its client-assigned key")
		}
		result := embeddingruntime.BatchItemResult{Key: item.Metadata.CustomKey}
		switch {
		case item.Error != nil:
			result.Err = item.Error.Message
		case item.Response != nil && len(item.Response.Embedding.Values) > 0:
			result.Vector = item.Response.Embedding.Values
		default:
			return nil, fmt.Errorf("embeddingruntime gemini: batch result item %q has neither a response nor an error", item.Metadata.CustomKey)
		}
		results = append(results, result)
	}
	return results, nil
}
