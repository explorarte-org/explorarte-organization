package search

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// =============================================================================
// Web provider configuration (V1)
//
// Env naming follows the canonical repository pattern used by every other
// HTTP adapter in the org (see internal/modelruntime/adapter/*/config.go):
//
//   ORG_SEARCH_PROVIDER_<NAME>_ENABLED
//   ORG_SEARCH_PROVIDER_<NAME>_ENDPOINT_URL
//   ORG_SEARCH_PROVIDER_<NAME>_CREDENTIAL_FILE
//   ORG_SEARCH_PROVIDER_<NAME>_REQUEST_TIMEOUT
//   ORG_SEARCH_PROVIDER_<NAME>_MAX_RETRIES
//
// Credentials resolve in the same order the org's readSecret helpers do:
//   1. ORG_SEARCH_PROVIDER_<NAME>_CREDENTIAL_FILE (absolute path to the key)
//   2. /etc/explorarte/secrets/<secret-name>
//   3. /run/secrets/<secret-name>
// The production compose.yaml already mounts /run/secrets/brave-api-key and
// /run/secrets/tavily-api-key, so providers configured there work out of the
// box. A provider without a credential stays disabled and simply does not
// register; it never panics and never blocks startup.
// =============================================================================

type LookupEnv func(name string) (value string, ok bool)

type WebProviderConfig struct {
	Enabled        bool
	EndpointURL    string
	CredentialFile string
	RequestTimeout time.Duration
	MaxRetries     int
	EngineID       string // optional per-provider engine id (Google Custom Search `cx`)
}

type ProviderEnv struct {
	Name       string // canonical provider name: "BRAVE", "TAVILY", ...
	SecretName string // secret file basename: "brave-api-key", ...
}

// envBool parses a boolean env value with a fallback, mirroring the
// modelruntime adapters' environment helper.
func envBool(lookup LookupEnv, key string, fallback bool) (bool, error) {
	raw, ok := lookup(key)
	if !ok || strings.TrimSpace(raw) == "" {
		return fallback, nil
	}
	value, err := strconv.ParseBool(strings.TrimSpace(raw))
	if err != nil {
		return false, fmt.Errorf("%s: %w", key, err)
	}
	return value, nil
}

// envDuration parses a duration env value (e.g. "10s", "2m") with a fallback.
func envDuration(lookup LookupEnv, key string, fallback time.Duration) (time.Duration, error) {
	raw, ok := lookup(key)
	if !ok || strings.TrimSpace(raw) == "" {
		return fallback, nil
	}
	value, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return value, nil
}

// envInt parses an integer env value with a fallback.
func envInt(lookup LookupEnv, key string, fallback int) (int, error) {
	raw, ok := lookup(key)
	if !ok || strings.TrimSpace(raw) == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return value, nil
}

// LoadWebProviderConfig loads the canonical ORG_SEARCH_PROVIDER_<NAME>_*
// configuration for a web provider. Enabled defaults to false: an
// unconfigured provider stays absent rather than half-wired.
func LoadWebProviderConfig(lookup LookupEnv, pe ProviderEnv, defaultEndpoint string) (WebProviderConfig, error) {
	if lookup == nil {
		return WebProviderConfig{}, errors.New("search: web provider env lookup is nil")
	}
	prefix := "ORG_SEARCH_PROVIDER_" + pe.Name
	cfg := WebProviderConfig{
		EndpointURL:    defaultEndpoint,
		RequestTimeout: 15 * time.Second,
		MaxRetries:     1, // V1 retry policy: a single retry for transient failures
	}
	var err error
	if cfg.Enabled, err = envBool(lookup, prefix+"_ENABLED", false); err != nil {
		return WebProviderConfig{}, err
	}
	if raw, ok := lookup(prefix + "_ENDPOINT_URL"); ok && strings.TrimSpace(raw) != "" {
		cfg.EndpointURL = strings.TrimSpace(raw)
	}
	if raw, ok := lookup(prefix + "_CREDENTIAL_FILE"); ok && strings.TrimSpace(raw) != "" {
		cfg.CredentialFile = strings.TrimSpace(raw)
	}
	if raw, ok := lookup(prefix + "_ENGINE_ID"); ok && strings.TrimSpace(raw) != "" {
		cfg.EngineID = strings.TrimSpace(raw)
	} else if raw, ok := lookup("ORG_SEARCH_PROVIDER_GOOGLE_ENGINE_ID"); ok && strings.TrimSpace(raw) != "" && pe.Name == "GOOGLE" {
		// Canonical global spelling for the Google Custom Search engine id.
		cfg.EngineID = strings.TrimSpace(raw)
	}
	if cfg.RequestTimeout, err = envDuration(lookup, prefix+"_REQUEST_TIMEOUT", cfg.RequestTimeout); err != nil {
		return WebProviderConfig{}, err
	}
	if cfg.MaxRetries, err = envInt(lookup, prefix+"_MAX_RETRIES", cfg.MaxRetries); err != nil {
		return WebProviderConfig{}, err
	}
	if cfg.MaxRetries < 0 {
		return WebProviderConfig{}, fmt.Errorf("search: %s_max_retries must be >= 0", prefix)
	}
	return cfg, nil
}

// readProviderSecret resolves a provider credential. It checks the explicit
// credential file first, then the org's standard secret mount paths. Returns
// "" (with no error) when nothing is configured, so callers can treat the
// provider as disabled without panicking.
func readProviderSecret(cfg WebProviderConfig, pe ProviderEnv) (string, error) {
	if strings.TrimSpace(cfg.CredentialFile) != "" {
		if !filepath.IsAbs(filepath.Clean(cfg.CredentialFile)) {
			return "", fmt.Errorf("search: %s credential file must be an absolute path", pe.Name)
		}
		data, err := os.ReadFile(cfg.CredentialFile)
		if err != nil {
			return "", fmt.Errorf("search: %s credential file unreadable: %w", pe.Name, err)
		}
		return strings.TrimSpace(string(data)), nil
	}
	paths := []string{
		filepath.Join("/etc/explorarte/secrets", pe.SecretName),
		filepath.Join("/run/secrets", pe.SecretName),
	}
	for _, p := range paths {
		if data, err := os.ReadFile(p); err == nil {
			return strings.TrimSpace(string(data)), nil
		}
	}
	return "", nil
}

// WebProviderSet is an explicit, injectable construction point for the four
// V1 web providers. It avoids a global service locator: callers build a set
// and register the providers they want; disabled providers appear as nil and
// are skipped.
type WebProviderSet struct {
	Brave   Provider
	Tavily  Provider
	Serpapi Provider
	Google  Provider
}

// RegisterWebProviders registers every non-nil provider of the set into the
// registry and returns how many were registered. Absent providers are not an
// error: startup must survive any subset of web providers being configured.
func (s *WebProviderSet) RegisterInto(reg *Registry) (int, error) {
	count := 0
	if s.Brave != nil {
		if err := reg.Register(ProviderBrave, s.Brave); err != nil {
			return count, err
		}
		count++
	}
	if s.Tavily != nil {
		if err := reg.Register(ProviderTavily, s.Tavily); err != nil {
			return count, err
		}
		count++
	}
	if s.Serpapi != nil {
		if err := reg.Register(ProviderSerpapi, s.Serpapi); err != nil {
			return count, err
		}
		count++
	}
	if s.Google != nil {
		if err := reg.Register(ProviderGoogleWeb, s.Google); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

// NewWebProviders constructs the four V1 web adapters from environment
// configuration. Each provider that is enabled and has a resolvable key is
// built; everything else stays nil. The construction is explicit and fully
// testable: callers may pass any HTTPDoer (httptest-backed in tests). When
// doer is nil, a production *http.Client is created for the default timeout.
func NewWebProviders(lookup LookupEnv, doer HTTPDoer) (*WebProviderSet, error) {
	if doer == nil {
		doer = NewClientHTTPDoer(defaultWebClient(15 * time.Second))
	}
	set := &WebProviderSet{}

	braveCfg, err := LoadWebProviderConfig(lookup, ProviderEnv{Name: "BRAVE", SecretName: "brave-api-key"}, DefaultBraveEndpoint)
	if err != nil {
		return nil, err
	}
	if braveCfg.Enabled {
		if key, err := readProviderSecret(braveCfg, ProviderEnv{Name: "BRAVE", SecretName: "brave-api-key"}); err != nil {
			return nil, err
		} else if key != "" {
			set.Brave = NewBraveProvider(braveCfg, key, doer)
		}
	}

	tavilyCfg, err := LoadWebProviderConfig(lookup, ProviderEnv{Name: "TAVILY", SecretName: "tavily-api-key"}, DefaultTavilyEndpoint)
	if err != nil {
		return nil, err
	}
	if tavilyCfg.Enabled {
		if key, err := readProviderSecret(tavilyCfg, ProviderEnv{Name: "TAVILY", SecretName: "tavily-api-key"}); err != nil {
			return nil, err
		} else if key != "" {
			set.Tavily = NewTavilyProvider(tavilyCfg, key, doer)
		}
	}

	serpapiCfg, err := LoadWebProviderConfig(lookup, ProviderEnv{Name: "SERPAPI", SecretName: "serpapi-api-key"}, DefaultSerpapiEndpoint)
	if err != nil {
		return nil, err
	}
	if serpapiCfg.Enabled {
		if key, err := readProviderSecret(serpapiCfg, ProviderEnv{Name: "SERPAPI", SecretName: "serpapi-api-key"}); err != nil {
			return nil, err
		} else if key != "" {
			set.Serpapi = NewSerpapiProvider(serpapiCfg, key, doer)
		}
	}

	googleCfg, err := LoadWebProviderConfig(lookup, ProviderEnv{Name: "GOOGLE", SecretName: "google-search-api-key"}, DefaultGoogleEndpoint)
	if err != nil {
		return nil, err
	}
	if googleCfg.Enabled {
		if key, err := readProviderSecret(googleCfg, ProviderEnv{Name: "GOOGLE", SecretName: "google-search-api-key"}); err != nil {
			return nil, err
		} else if key != "" {
			set.Google = NewGoogleWebProvider(googleCfg, key, doer)
		}
	}

	return set, nil
}

// The four canonical V1 endpoints, overridable via ORG_SEARCH_PROVIDER_*_ENDPOINT_URL.
const (
	DefaultBraveEndpoint   = "https://api.search.brave.com/res/v1/web/search"
	DefaultTavilyEndpoint  = "https://api.tavily.com/search"
	DefaultSerpapiEndpoint = "https://serpapi.com/search"
	DefaultGoogleEndpoint  = "https://www.googleapis.com/customsearch/v1"
)
