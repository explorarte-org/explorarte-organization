package cloudflare

// Host-owned provider identity and endpoint-fixture regression tests,
// demanded before canonical approval (review round 2026-09-08, P1 items):
//
//  1. NewForProvider is host-owned: the providerID is fixed by
//     cloudflare.New() — NEVER derived from CanonicalRequest. A Cloudflare
//     adapter instance must reject a preflight whose request carries
//     provider "openai_compatible", and the legacy openai-compatible
//     instance must equally reject "cloudflare_workers_ai". Generalizing
//     the transport must not turn openaicompat into a door for dynamically
//     invented providers.
//
//  2. The endpoint is host-constructed from a strictly validated account
//     ID (32 hex chars) against the FIXED api.cloudflare.com host. There is
//     no CLOUDFLARE_ENDPOINT_URL env; traversal/query/at-sign payloads in
//     the account ID are rejected by the 32-hex rule.

import (
	"context"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
	"github.com/Mireuz13/explorarte-organization/internal/modelruntime/adapter/openaicompat"
)

func testConfig(accountID string) Config {
	return Config{
		Enabled:          true,
		AccountID:        accountID,
		CredentialFile:   "/tmp/cloudflare-test-token",
		RequestTimeout:   time.Minute,
		FailureThreshold: 5,
		OpenDuration:     30 * time.Second,
		MaxResponseBytes: 1 << 20,
	}
}

func TestCloudflareAdapter_HasOwnProviderIdentity(t *testing.T) {
	adapter, err := New(testConfig("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	if got := adapter.Descriptor().ProviderID; got != ProviderID {
		t.Fatalf("cloudflare instance descriptor must be %q, got %q", ProviderID, got)
	}
}

func TestLegacyOpenAICompatibleIdentityUnchanged(t *testing.T) {
	adapter, err := openaicompat.New(openaicompat.Config{
		Enabled:          true,
		EndpointURL:      "https://provider.example/v1/chat/completions",
		CredentialFile:   "/tmp/openai-test-token",
		RequestTimeout:   time.Minute,
		FailureThreshold: 5,
		OpenDuration:     30 * time.Second,
		MaxResponseBytes: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := adapter.Descriptor().ProviderID; got != "openai_compatible" {
		t.Fatalf("legacy New() must stay openai_compatible, got %q", got)
	}
}

func TestPreflightRejectsCrossProviderRequests(t *testing.T) {
	cloudflareAdapter, err := New(testConfig("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	openAIAdapter, err := openaicompat.New(openaicompat.Config{
		Enabled:          true,
		EndpointURL:      "https://provider.example/v1/chat/completions",
		CredentialFile:   "/tmp/openai-test-token",
		RequestTimeout:   time.Minute,
		FailureThreshold: 5,
		OpenDuration:     30 * time.Second,
		MaxResponseBytes: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Minute)

	// Cloudflare instance + request claiming openai_compatible -> DENY.
	err = cloudflareAdapter.Preflight(context.Background(), modelruntime.ProviderPreflightRequest{
		ProviderID: "openai_compatible", ProviderModelID: "any", Deadline: deadline,
	})
	if err == nil {
		t.Fatal("cloudflare instance must reject a request carrying the openai_compatible provider")
	}

	// OpenAI-compatible instance + request claiming cloudflare -> DENY.
	err = openAIAdapter.Preflight(context.Background(), modelruntime.ProviderPreflightRequest{
		ProviderID: ProviderID, ProviderModelID: "any", Deadline: deadline,
	})
	if err == nil {
		t.Fatal("openai_compatible instance must reject a request carrying the cloudflare provider")
	}
}

func TestAccountIDValidationBlocksInjectionShapes(t *testing.T) {
	cases := map[string]string{
		"traversal": "foo/../../bar",
		"query":     "0123456789abcdef0123456789abcdef?x=1",
		"atSign":    "@evil.com",
		"empty":     "",
		"short":     "0123456789abcdef",
		"nonHex":    "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz",
		"padding":   "0123456789abcdef0123456789abcdef ",
	}
	for name, accountID := range cases {
		cfg := testConfig(accountID)
		if err := cfg.Validate(); err == nil {
			t.Fatalf("%s: invalid account ID must fail validation", name)
		}
	}
	// And the fixed host is unaffected by whichever account ID passes.
	valid := testConfig("0123456789abcdef0123456789abcdef")
	if got := valid.EndpointURL(); got != "https://api.cloudflare.com/client/v4/accounts/0123456789abcdef0123456789abcdef/ai/v1/chat/completions" {
		t.Fatalf("endpoint must be host-constructed on the fixed Cloudflare host, got %q", got)
	}
}
