package bgem3_test

// This test talks to a real, running BGE-M3 sidecar over loopback HTTP. It
// is opt-in (skipped unless ORG_BGE_M3_LIVE_SMOKE_TEST=1) because it
// depends on external process state (the sidecar must already be running
// with the model loaded) that CI and `go test ./...` cannot assume --
// every other test in this package uses httptest and needs nothing
// running. Its purpose is to catch wire-protocol drift between this Go
// client and a real sidecar implementation that no mock can catch.

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/embeddingruntime"
	"github.com/Mireuz13/explorarte-organization/internal/embeddingruntime/adapter/bgem3"
)

func TestLiveSidecarHealthAndEmbed(t *testing.T) {
	if os.Getenv("ORG_BGE_M3_LIVE_SMOKE_TEST") != "1" {
		t.Skip("set ORG_BGE_M3_LIVE_SMOKE_TEST=1 to run against a live sidecar")
	}
	cfg := bgem3.Config{
		Enabled:               true,
		BaseURL:               envOrDefault("ORG_BGE_M3_LIVE_BASE_URL", "http://127.0.0.1:8901"),
		ModelRevision:         "baai-bge-m3-5617a9f61b028005a4858fdac845db406aefb181",
		ArtifactSHA256:        "b5e0ce3470abf5ef3831aa1bd5553b486803e83251590ab7ff35a117cf6aad38",
		TokenizerRevision:     "baai-bge-m3-5617a9f61b028005a4858fdac845db406aefb181",
		Normalization:         "l2",
		Pooling:               "cls",
		ExpectedDimension:     1024,
		PromptTemplateVersion: bgem3.PromptTemplateV1,
		RequestTimeout:        30 * time.Second,
		MaxConcurrency:        1,
		MaxQueueDepth:         1,
		MaxInputBytes:         32 * 1024,
		MaxItemsPerRequest:    16,
		MaxResponseBytes:      8 << 20,
	}
	adapter, err := bgem3.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()

	health, err := adapter.Healthy(ctx)
	if err != nil {
		t.Fatalf("Healthy: %v", err)
	}
	if !health.Ready() {
		t.Fatalf("sidecar not ready: %+v", health)
	}
	if health.Dimension != 1024 {
		t.Fatalf("dimension = %d, want 1024", health.Dimension)
	}

	resp, err := adapter.Embed(ctx, embeddingruntime.EmbedRequest{
		ProviderID: bgem3.ProviderID, ProviderModelID: cfg.ModelRevision, OutputDimensionality: cfg.ExpectedDimension,
		PromptTemplateVersion: cfg.PromptTemplateVersion,
		Items: []embeddingruntime.EmbedItem{
			{Key: "a", Text: "terapia cognitivo conductual para TDAH inatento", Task: embeddingruntime.TaskDocument},
			{Key: "b", Text: "arquitectura de microservicios con Go y PostgreSQL", Task: embeddingruntime.TaskDocument},
		},
	})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(resp.Results) != 2 {
		t.Fatalf("got %d results, want 2", len(resp.Results))
	}
	byKey := map[string][]float32{}
	for _, r := range resp.Results {
		if len(r.Vector) != 1024 {
			t.Fatalf("key %s: vector length = %d, want 1024", r.Key, len(r.Vector))
		}
		var normSq float64
		for _, v := range r.Vector {
			normSq += float64(v) * float64(v)
		}
		if diff := normSq - 1.0; diff < -0.01 || diff > 0.01 {
			t.Fatalf("key %s: vector L2 norm^2 = %f, want ~1.0 (l2-normalized)", r.Key, normSq)
		}
		byKey[r.Key] = r.Vector
	}
	if _, ok := byKey["a"]; !ok {
		t.Fatalf("missing result for key a")
	}
	if _, ok := byKey["b"]; !ok {
		t.Fatalf("missing result for key b")
	}
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
