package mistral

// Host-owned identity / fixed-endpoint regression tests, plus the
// cross-provider matrix against the Cloudflare adapter. These accompany the
// READY_FOR_HUMAN_APPROVAL package; no canonical approval is implied.

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
	"github.com/Mireuz13/explorarte-organization/internal/modelruntime/adapter/cloudflare"
	"github.com/Mireuz13/explorarte-organization/internal/modelruntime/adapter/openaicompat"
)

func validConfig() Config {
	return Config{
		Enabled:          true,
		CredentialFile:   "/tmp/mistral-test-token",
		RequestTimeout:   time.Minute,
		FailureThreshold: 5,
		OpenDuration:     30 * time.Second,
		MaxResponseBytes: 1 << 20,
	}
}

func TestMistralAdapter_HasOwnProviderIdentity(t *testing.T) {
	adapter, err := New(validConfig())
	if err != nil {
		t.Fatal(err)
	}
	if got := adapter.Descriptor().ProviderID; got != ProviderID {
		t.Fatalf("mistral instance descriptor must be %q, got %q", ProviderID, got)
	}
}

func TestMistralEndpointIsFixed_NoConfigurableOverride(t *testing.T) {
	if got := validConfig().Endpoint(); got != "https://api.mistral.ai/v1/chat/completions" {
		t.Fatalf("endpoint must be the fixed official host, got %q", got)
	}
	// There is deliberately no env var that can change the destination.
	lookup := func(key string) (string, bool) {
		if key == "MISTRAL_ENDPOINT_URL" {
			return "https://evil.example/v1/chat/completions", true
		}
		return "", false
	}
	cfg, err := LoadConfig(lookup, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Endpoint() != "https://api.mistral.ai/v1/chat/completions" {
		t.Fatalf("endpoint override env must be ignored entirely, got %q", cfg.Endpoint())
	}
}

func TestMistralEnabledRequiresAbsoluteCredentialPath(t *testing.T) {
	cfg := validConfig()
	cfg.CredentialFile = "relative/path"
	if err := cfg.Validate(); err == nil {
		t.Fatal("enabled mistral requires an absolute credential path")
	}
	disabled := validConfig()
	disabled.Enabled = false
	disabled.CredentialFile = ""
	if err := disabled.Validate(); err != nil {
		t.Fatalf("disabled config must not require credentials: %v", err)
	}
}

func TestCrossProviderMatrix(t *testing.T) {
	// Preflight loads the bearer token from disk; provide throwaway files.
	for _, path := range []string{"/tmp/mistral-test-token", "/tmp/cf-test-token", "/tmp/openai-test-token"} {
		if err := os.WriteFile(path, []byte("test-token-not-a-secret"), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Remove(path) })
	}
	mistralAdapter, err := New(validConfig())
	if err != nil {
		t.Fatal(err)
	}
	cfAdapter, err := cloudflare.New(cloudflare.Config{
		Enabled: true, AccountID: "0123456789abcdef0123456789abcdef",
		CredentialFile: "/tmp/cf-test-token", RequestTimeout: time.Minute,
		FailureThreshold: 5, OpenDuration: 30 * time.Second, MaxResponseBytes: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	openAIAdapter, err := openaicompat.New(openaicompat.Config{
		Enabled: true, EndpointURL: "https://provider.example/v1/chat/completions",
		CredentialFile: "/tmp/openai-test-token", RequestTimeout: time.Minute,
		FailureThreshold: 5, OpenDuration: 30 * time.Second, MaxResponseBytes: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Minute)
	requests := []modelruntime.ProviderPreflightRequest{
		{ProviderID: "cloudflare_workers_ai", ProviderModelID: "any", Deadline: deadline},
		{ProviderID: "openai_compatible", ProviderModelID: "any", Deadline: deadline},
	}
	for _, req := range requests {
		if err := mistralAdapter.Preflight(context.Background(), req); err == nil {
			t.Fatalf("mistral instance must reject foreign provider %q", req.ProviderID)
		}
	}
	if err := mistralAdapter.Preflight(context.Background(), modelruntime.ProviderPreflightRequest{
		ProviderID: ProviderID, ProviderModelID: "ministral-8b-latest", Deadline: deadline,
	}); err != nil {
		t.Fatalf("own provider must pass identity: %v", err)
	}
	if err := cfAdapter.Preflight(context.Background(), modelruntime.ProviderPreflightRequest{
		ProviderID: ProviderID, ProviderModelID: "any", Deadline: deadline,
	}); err == nil {
		t.Fatal("cloudflare instance must reject the mistral provider")
	}
	if err := openAIAdapter.Preflight(context.Background(), modelruntime.ProviderPreflightRequest{
		ProviderID: ProviderID, ProviderModelID: "any", Deadline: deadline,
	}); err == nil {
		t.Fatal("legacy openai_compatible instance must reject the mistral provider")
	}
}
