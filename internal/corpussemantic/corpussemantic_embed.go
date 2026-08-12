package corpussemantic

import (
	"context"
	"fmt"

	"github.com/Mireuz13/explorarte-organization/internal/embeddingruntime"
)

// SemanticInput is one Work's already-built embedding input (title
// resolution and title+abstract composition happen upstream of this
// package -- see the corpus's semantic_inputs.json build step; this
// package only embeds and clusters, it does not decide what a Work's
// canonical title is).
type SemanticInput struct {
	WorkID        string
	SemanticInput string
	InputHash     string
}

// EmbedConfig pins the adapter identity this package embeds under --
// mirrors internal/rag/bootstrap's own ragEmbeddingProviderID/ModelID/
// Dimension constants so a report reader can confirm this used the same
// space Knowledge retrieval will later use, not a different one.
type EmbedConfig struct {
	ProviderID            string
	ProviderModelID       string
	OutputDimensionality  int
	PromptTemplateVersion string
	BatchSize             int // items per OnlineAdapter.Embed call -- conservative, Google does not publish a hard batchEmbedContents cap
}

func DefaultEmbedConfig() EmbedConfig {
	return EmbedConfig{
		ProviderID: "gemini", ProviderModelID: "gemini-embedding-2",
		OutputDimensionality: 768, PromptTemplateVersion: "prompt-template.v1", BatchSize: 50,
	}
}

type EmbedResult struct {
	Requested        int
	AlreadyCached    int
	Embedded         int
	Failed           int
	TotalInputTokens int64
}

// Run embeds every SemanticInput not already Valid in Store, in
// Config.BatchSize chunks, flushing the Store after every batch
// (periodic checkpointing, not Flush-only-at-the-end).
func Run(ctx context.Context, adapter embeddingruntime.OnlineAdapter, store *Store, inputs []SemanticInput, cfg EmbedConfig) (EmbedResult, error) {
	result := EmbedResult{Requested: len(inputs)}
	var pending []SemanticInput
	for _, in := range inputs {
		if _, ok := store.Valid(in.WorkID, in.InputHash); ok {
			result.AlreadyCached++
			continue
		}
		pending = append(pending, in)
	}

	batchSize := cfg.BatchSize
	if batchSize <= 0 {
		batchSize = 50
	}

	for start := 0; start < len(pending); start += batchSize {
		end := start + batchSize
		if end > len(pending) {
			end = len(pending)
		}
		batch := pending[start:end]

		items := make([]embeddingruntime.EmbedItem, len(batch))
		for i, in := range batch {
			items[i] = embeddingruntime.EmbedItem{Key: in.WorkID, Text: in.SemanticInput, Task: embeddingruntime.TaskDocument}
		}
		response, err := adapter.Embed(ctx, embeddingruntime.EmbedRequest{
			ProviderID: cfg.ProviderID, ProviderModelID: cfg.ProviderModelID,
			OutputDimensionality: cfg.OutputDimensionality, PromptTemplateVersion: cfg.PromptTemplateVersion,
			Items: items,
		})
		if err != nil {
			result.Failed += len(batch)
			if flushErr := store.Flush(); flushErr != nil {
				return result, fmt.Errorf("corpussemantic: flush after batch error: %w", flushErr)
			}
			return result, fmt.Errorf("corpussemantic: embed batch starting at %d: %w", start, err)
		}

		byKey := make(map[string][]float32, len(response.Results))
		for _, r := range response.Results {
			byKey[r.Key] = r.Vector
		}
		for _, in := range batch {
			vector, ok := byKey[in.WorkID]
			if !ok {
				result.Failed++
				continue
			}
			store.Put(EmbeddingRecord{
				WorkID: in.WorkID, InputHash: in.InputHash, Vector: vector,
				EmbeddingProviderID: cfg.ProviderID, EmbeddingModelID: cfg.ProviderModelID,
				EmbeddingDimension: cfg.OutputDimensionality, InputTokens: response.InputTokens / int64(len(batch)),
			})
			result.Embedded++
		}
		result.TotalInputTokens += response.InputTokens

		if err := store.Flush(); err != nil {
			return result, fmt.Errorf("corpussemantic: checkpoint flush: %w", err)
		}
	}

	return result, nil
}
