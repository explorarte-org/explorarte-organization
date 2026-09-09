package search

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func envLookup(env map[string]string) LookupEnv {
	return func(name string) (string, bool) {
		if v, ok := env[name]; ok {
			return v, true
		}
		return "", false
	}
}

func TestWebConfig_DisabledByDefault(t *testing.T) {
	cfg, err := LoadWebProviderConfig(envLookup(make(map[string]string)), ProviderEnv{Name: "BRAVE", SecretName: "brave-api-key"}, DefaultBraveEndpoint)
	if err != nil {
		t.Fatalf("LoadWebProviderConfig: %v", err)
	}
	if cfg.Enabled {
		t.Error("provider must default to disabled")
	}
	if cfg.EndpointURL != DefaultBraveEndpoint {
		t.Errorf("endpoint = %q", cfg.EndpointURL)
	}
}

func TestWebConfig_EnabledWithOverrides(t *testing.T) {
	env := make(map[string]string)
	env["ORG_SEARCH_PROVIDER_BRAVE_ENABLED"] = "true"
	env["ORG_SEARCH_PROVIDER_BRAVE_ENDPOINT_URL"] = "https://proxy.example/brave"
	env["ORG_SEARCH_PROVIDER_BRAVE_REQUEST_TIMEOUT"] = "3s"
	env["ORG_SEARCH_PROVIDER_BRAVE_MAX_RETRIES"] = "0"
	cfg, err := LoadWebProviderConfig(envLookup(env), ProviderEnv{Name: "BRAVE", SecretName: "brave-api-key"}, DefaultBraveEndpoint)
	if err != nil {
		t.Fatalf("LoadWebProviderConfig: %v", err)
	}
	if !cfg.Enabled {
		t.Error("expected enabled")
	}
	if cfg.EndpointURL != "https://proxy.example/brave" {
		t.Errorf("endpoint = %q", cfg.EndpointURL)
	}
	if cfg.RequestTimeout != 3*time.Second {
		t.Errorf("timeout = %v", cfg.RequestTimeout)
	}
	if cfg.MaxRetries != 0 {
		t.Errorf("max_retries = %d", cfg.MaxRetries)
	}
}

func TestWebConfig_InvalidDurationRejected(t *testing.T) {
	env := make(map[string]string)
	env["ORG_SEARCH_PROVIDER_TAVILY_ENABLED"] = "true"
	env["ORG_SEARCH_PROVIDER_TAVILY_REQUEST_TIMEOUT"] = "not-a-duration"
	_, err := LoadWebProviderConfig(envLookup(env), ProviderEnv{Name: "TAVILY", SecretName: "tavily-api-key"}, DefaultTavilyEndpoint)
	if err == nil {
		t.Fatal("expected error for invalid duration")
	}
}

func TestWebConfig_NegativeRetriesRejected(t *testing.T) {
	env := make(map[string]string)
	env["ORG_SEARCH_PROVIDER_SERPAPI_ENABLED"] = "true"
	env["ORG_SEARCH_PROVIDER_SERPAPI_MAX_RETRIES"] = "-1"
	_, err := LoadWebProviderConfig(envLookup(env), ProviderEnv{Name: "SERPAPI", SecretName: "serpapi-api-key"}, DefaultSerpapiEndpoint)
	if err == nil {
		t.Fatal("expected error for negative retries")
	}
}

func TestReadProviderSecret_FromCredentialFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "brave-key")
	if err := os.WriteFile(path, []byte("secret-file-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := WebProviderConfig{Enabled: true, CredentialFile: path}
	key, err := readProviderSecret(cfg, ProviderEnv{Name: "BRAVE", SecretName: "brave-api-key"})
	if err != nil {
		t.Fatalf("readProviderSecret: %v", err)
	}
	if key != "secret-file-value" {
		t.Errorf("key = %q", key)
	}
}

func TestReadProviderSecret_RelativePathRejected(t *testing.T) {
	cfg := WebProviderConfig{Enabled: true, CredentialFile: "relative/key"}
	_, err := readProviderSecret(cfg, ProviderEnv{Name: "BRAVE", SecretName: "brave-api-key"})
	if err == nil {
		t.Fatal("expected error for relative credential path")
	}
}

func TestReadProviderSecret_UnconfiguredReturnsEmpty(t *testing.T) {
	cfg := WebProviderConfig{Enabled: true}
	key, err := readProviderSecret(cfg, ProviderEnv{Name: "BRAVE", SecretName: "definitely-missing-secret-name-xyz"})
	if err != nil {
		t.Fatalf("readProviderSecret: %v", err)
	}
	if key != "" {
		t.Errorf("expected empty key, got %q", key)
	}
}

func TestWebProviderSet_RegisterSkipsDisabled(t *testing.T) {
	reg := NewRegistry()
	set := &WebProviderSet{}
	set.Tavily = NewTavilyProvider(WebProviderConfig{Enabled: true}, "k", NewClientHTTPDoer(nil))

	count, err := set.RegisterInto(reg)
	if err != nil {
		t.Fatalf("RegisterInto: %v", err)
	}
	if count != 1 {
		t.Errorf("registered = %d, want 1", count)
	}
	if reg.Get(ProviderTavily) == nil {
		t.Error("Tavily should be registered")
	}
	if reg.Get(ProviderBrave) != nil {
		t.Error("Brave should stay unregistered when absent")
	}
}

func TestNewWebProviders_OnlyEnabledWithKeys(t *testing.T) {
	// No credentials anywhere: every provider must come back nil, no error,
	// so startup survives a fully-unconfigured web stack.
	set, err := NewWebProviders(envLookup(make(map[string]string)), NewClientHTTPDoer(nil))
	if err != nil {
		t.Fatalf("NewWebProviders: %v", err)
	}
	if set.Brave != nil || set.Tavily != nil || set.Serpapi != nil || set.Google != nil {
		t.Error("no provider should be constructed without credentials")
	}
}

func TestNewWebProviders_OnlyEnglishBraveBuilds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "brave-key")
	if err := os.WriteFile(path, []byte("real-brave-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := make(map[string]string)
	env["ORG_SEARCH_PROVIDER_BRAVE_ENABLED"] = "true"
	env["ORG_SEARCH_PROVIDER_BRAVE_CREDENTIAL_FILE"] = path
	set, err := NewWebProviders(envLookup(env), NewClientHTTPDoer(nil))
	if err != nil {
		t.Fatalf("NewWebProviders: %v", err)
	}
	if set.Brave == nil {
		t.Error("Brave should be built")
	}
	if set.Tavily != nil || set.Serpapi != nil || set.Google != nil {
		t.Error("only Brave should be built")
	}
}

func TestParseRetryAfter(t *testing.T) {
	if parseRetryAfter("42") != 42*time.Second {
		t.Error("42s expected")
	}
	if parseRetryAfter("") != 0 {
		t.Error("empty -> 0")
	}
	if parseRetryAfter("abc") != 0 {
		t.Error("non-numeric -> 0")
	}
}

func TestProviderError_MessageHasNoSecretsOrURLs(t *testing.T) {
	e := &ProviderError{Provider: ProviderSerpapi, Kind: ProviderErrorUnauthorized, StatusCode: 401}
	msg := e.Error()
	if strings.Contains(msg, "?") || strings.Contains(msg, "api_key") {
		t.Errorf("error message must not embed request URL or secrets: %q", msg)
	}
	if !strings.Contains(msg, "unauthorized") {
		t.Errorf("message should carry the kind: %q", msg)
	}
}
