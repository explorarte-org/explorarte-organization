package gemini

import (
	"context"
	"encoding/base64"
	"fmt"
	"math"

	"github.com/Mireuz13/explorarte-organization/internal/embeddingruntime"
)

// SupportedMediaMimeTypes is gemini-embedding-2's documented multimodal
// input surface (confirmed against Google's live API docs at
// ai.google.dev/gemini-api/docs/embeddings): images, audio, video, and PDF,
// each mapped into the same embedding space as text. This adapter does not
// (and structurally cannot, without parsing the file itself) verify a
// caller respected the API's per-modality limits documented there (6
// images, 180s audio, 120s video, 6 PDF pages per call) — those are the
// caller's responsibility; this adapter only enforces the MIME type
// allowlist and a byte-size ceiling as a sanity backstop.
var SupportedMediaMimeTypes = map[string]bool{
	"application/pdf": true,
	"image/png":       true,
	"image/jpeg":      true,
	"audio/mpeg":      true,
	"audio/wav":       true,
	"video/mp4":       true,
	"video/quicktime": true,
}

// maxMediaBytes bounds a single inline media item before base64 encoding.
// Google does not publish an exact inline-request byte ceiling for this
// endpoint as of implementation time; this is a conservative backstop
// (consistent with the ~20MB inline-request limits documented elsewhere in
// the Gemini API family) so a caller error cannot silently build an
// unbounded request body.
const maxMediaBytes = 20 << 20

type embedContentConfig struct {
	TaskType             string `json:"taskType,omitempty"`
	OutputDimensionality int    `json:"outputDimensionality,omitempty"`
}

type inlineDataPart struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type contentPart struct {
	Text       string          `json:"text,omitempty"`
	InlineData *inlineDataPart `json:"inlineData,omitempty"`
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
		if !item.Valid() {
			return embeddingruntime.EmbedResponse{}, embeddingruntime.ErrInvalidRequest
		}
		var part contentPart
		if item.IsMedia() {
			if !SupportedMediaMimeTypes[item.MimeType] {
				return embeddingruntime.EmbedResponse{}, fmt.Errorf("%w: unsupported media MIME type %q", embeddingruntime.ErrInvalidRequest, item.MimeType)
			}
			if len(item.Data) > maxMediaBytes {
				return embeddingruntime.EmbedResponse{}, fmt.Errorf("%w: media item %q exceeds maximum inline size", embeddingruntime.ErrInvalidRequest, item.Key)
			}
			part = contentPart{InlineData: &inlineDataPart{MimeType: item.MimeType, Data: base64.StdEncoding.EncodeToString(item.Data)}}
		} else {
			rendered, err := renderPrompt(request.PromptTemplateVersion, item.Task, item.Text)
			if err != nil {
				return embeddingruntime.EmbedResponse{}, err
			}
			part = contentPart{Text: rendered}
		}
		body.Requests = append(body.Requests, embedContentRequest{
			Model:   "models/" + request.ProviderModelID,
			Content: content{Parts: []contentPart{part}},
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
		if err := validateVector(embedding.Values, request.OutputDimensionality); err != nil {
			return embeddingruntime.EmbedResponse{}, fmt.Errorf("position %d: %w", index, err)
		}
		results[index] = embeddingruntime.EmbedResult{Key: request.Items[index].Key, Vector: embedding.Values}
	}
	return embeddingruntime.EmbedResponse{Results: results, InputTokens: decoded.UsageMetadata.PromptTokenCount}, nil
}

// validateVector enforces the same adapter-boundary contract bgem3 already
// has (R31 hardening §6): a response that decodes at the JSON level is not
// yet trustworthy as an embedding. A wrong-dimension or non-finite vector
// must never reach pgvector -- it would either fail there opaquely or,
// worse, silently corrupt a similarity index. expectedDimension of 0 (a
// caller that did not request a specific OutputDimensionality) only checks
// for emptiness/non-finite components, since gemini-embedding-2's default
// dimensionality is provider-chosen in that case, not caller-verifiable.
func validateVector(vector []float32, expectedDimension int) error {
	if len(vector) == 0 {
		return fmt.Errorf("%w: empty vector", ErrInvalidVector)
	}
	if expectedDimension > 0 && len(vector) != expectedDimension {
		return fmt.Errorf("%w: got dimension %d, want %d", ErrInvalidVector, len(vector), expectedDimension)
	}
	for _, component := range vector {
		if math.IsNaN(float64(component)) || math.IsInf(float64(component), 0) {
			return fmt.Errorf("%w: non-finite component", ErrInvalidVector)
		}
	}
	return nil
}
