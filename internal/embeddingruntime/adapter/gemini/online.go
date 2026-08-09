package gemini

import (
	"context"
	"fmt"

	"github.com/Mireuz13/explorarte-organization/internal/embeddingruntime"
)

type embedContentConfig struct {
	TaskType             string `json:"taskType,omitempty"`
	OutputDimensionality int    `json:"outputDimensionality,omitempty"`
}

type contentPart struct {
	Text string `json:"text"`
}

type content struct {
	Parts []contentPart `json:"parts"`
}

type embedContentRequest struct {
	Model              string             `json:"model"`
	Content            content            `json:"content"`
	EmbedContentConfig embedContentConfig `json:"embedContentConfig"`
}

type batchEmbedContentsRequest struct {
	Requests []embedContentRequest `json:"requests"`
}

type embeddingValue struct {
	Values []float32 `json:"values"`
	Shape  []int     `json:"shape,omitempty"`
}

type promptTokenDetail struct {
	Modality   string `json:"modality"`
	TokenCount int64  `json:"tokenCount"`
}

type usageMetadata struct {
	PromptTokenCount   int64               `json:"promptTokenCount"`
	PromptTokenDetails []promptTokenDetail `json:"promptTokenDetails,omitempty"`
}

type batchEmbedContentsResponse struct {
	Embeddings    []embeddingValue `json:"embeddings"`
	UsageMetadata usageMetadata    `json:"usageMetadata"`
}

// Embed implements embeddingruntime.OnlineAdapter against Gemini's
// synchronous batchEmbedContents endpoint — synchronous despite the name;
// this is NOT the discounted asynchronous Batch API (see batch.go for
// that). It groups every item in request.Items into a single HTTP call,
// which Google's documentation states preserves response order exactly
// matching request order; Embed still verifies the returned count matches
// before trusting that ordering, rather than assuming the documented
// behavior always holds.
func (a *Adapter) Embed(ctx context.Context, request embeddingruntime.EmbedRequest) (embeddingruntime.EmbedResponse, error) {
	if request.ProviderID != ProviderID || request.ProviderModelID == "" || len(request.Items) == 0 {
		return embeddingruntime.EmbedResponse{}, embeddingruntime.ErrInvalidRequest
	}
	body := batchEmbedContentsRequest{Requests: make([]embedContentRequest, 0, len(request.Items))}
	for _, item := range request.Items {
		if item.Key == "" || item.Text == "" || !item.Task.Valid() {
			return embeddingruntime.EmbedResponse{}, embeddingruntime.ErrInvalidRequest
		}
		rendered, err := renderPrompt(request.PromptTemplateVersion, item.Task, item.Text)
		if err != nil {
			return embeddingruntime.EmbedResponse{}, err
		}
		body.Requests = append(body.Requests, embedContentRequest{
			Model:   "models/" + request.ProviderModelID,
			Content: content{Parts: []contentPart{{Text: rendered}}},
			EmbedContentConfig: embedContentConfig{
				TaskType: taskTypeField(item.Task), OutputDimensionality: request.OutputDimensionality,
			},
		})
	}

	var decoded batchEmbedContentsResponse
	path := "/v1beta/models/" + request.ProviderModelID + ":batchEmbedContents"
	if _, err := a.doJSON(ctx, "POST", path, body, &decoded); err != nil {
		return embeddingruntime.EmbedResponse{}, err
	}
	if len(decoded.Embeddings) != len(request.Items) {
		return embeddingruntime.EmbedResponse{}, fmt.Errorf("%w: sent %d texts, got %d vectors", embeddingruntime.ErrResultCountMismatch, len(request.Items), len(decoded.Embeddings))
	}
	results := make([]embeddingruntime.EmbedResult, len(request.Items))
	for index, embedding := range decoded.Embeddings {
		if len(embedding.Values) == 0 {
			return embeddingruntime.EmbedResponse{}, fmt.Errorf("%w: empty vector at position %d", embeddingruntime.ErrResultCountMismatch, index)
		}
		results[index] = embeddingruntime.EmbedResult{Key: request.Items[index].Key, Vector: embedding.Values}
	}
	return embeddingruntime.EmbedResponse{Results: results, InputTokens: decoded.UsageMetadata.PromptTokenCount}, nil
}
