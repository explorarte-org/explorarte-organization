package cloudflare

import "testing"

func TestLoadConfigBuildsWorkersAIChatCompletionsEndpoint(t *testing.T) {
	values := map[string]string{
		"ORG_MODEL_PROVIDER_CLOUDFLARE_ENABLED":         "true",
		"CLOUDFLARE_ACCOUNT_ID":                         "fabb52cbceafa6980b54437a9aa21adf",
		"ORG_MODEL_PROVIDER_CLOUDFLARE_CREDENTIAL_FILE": "/run/secrets/cloudflare-api-token",
	}
	config, err := LoadConfig(func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := config.EndpointURL(), "https://api.cloudflare.com/client/v4/accounts/fabb52cbceafa6980b54437a9aa21adf/ai/v1/chat/completions"; got != want {
		t.Fatalf("endpoint=%q want %q", got, want)
	}
	adapter, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	if adapter.ProviderID() != ProviderID {
		t.Fatalf("provider=%q want %q", adapter.ProviderID(), ProviderID)
	}
	if descriptor := adapter.Descriptor(); descriptor.ProviderID != ProviderID {
		t.Fatalf("descriptor provider=%q want %q", descriptor.ProviderID, ProviderID)
	}
}

func TestLoadConfigRejectsInvalidAccountID(t *testing.T) {
	values := map[string]string{
		"ORG_MODEL_PROVIDER_CLOUDFLARE_ENABLED":         "true",
		"CLOUDFLARE_ACCOUNT_ID":                         "not-an-account",
		"ORG_MODEL_PROVIDER_CLOUDFLARE_CREDENTIAL_FILE": "/run/secrets/cloudflare-api-token",
	}
	if _, err := LoadConfig(func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}, 1<<20); err == nil {
		t.Fatal("expected invalid account ID error")
	}
}

func TestDisabledConfigDoesNotRequireCloudflareCredentials(t *testing.T) {
	config, err := LoadConfig(func(string) (string, bool) { return "", false }, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if config.Enabled {
		t.Fatal("disabled config unexpectedly enabled")
	}
	if _, err := New(config); err != nil {
		t.Fatal(err)
	}
}
