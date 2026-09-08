// Package mistral adapts the Mistral API's OpenAI-compatible Chat
// Completions endpoint for the canonical model runtime, reusing the shared
// OpenAI-compatible transport (NewForProvider) exactly like the Cloudflare
// adapter. No second HTTP stack.
//
// Endpoint policy: the chat endpoint is host-constructed on the FIXED
// official host (https://api.mistral.ai/v1/chat/completions). There is NO
// MISTRAL_ENDPOINT_URL — a model or config can never choose the destination
// (anti-SSRF). The bearer token loads from an external secret file
// (/run/secrets/mistral-api-key) via the canonical secrets loader.
//
// Economic semantics: Mistral is NOT a zero-cost subscription provider.
// The account has finite monthly included usage shared across Studio/API/
// Vibe, and PAYG may or may not be enabled at the organization level. The
// provider therefore goes through normal pricing + cost-ledger reservation
// (NEVER costgate.subscriptionProviders), with a deployment-configured
// local credit ceiling as the fail-closed second barrier.
package mistral

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/modelruntime/adapter/openaicompat"
)

const (
	ProviderID = "mistral"

	// fixedChatCompletionsEndpoint is the ONLY destination this adapter can
	// reach. It is not configurable.
	fixedChatCompletionsEndpoint = "https://api.mistral.ai/v1/chat/completions"

	defaultRequestTimeout   = 2 * time.Minute
	defaultFailureThreshold = 5
	defaultOpenDuration     = 30 * time.Second
)

type LookupEnv func(string) (string, bool)

type Config struct {
	Enabled          bool
	CredentialFile   string
	RequestTimeout   time.Duration
	FailureThreshold int
	OpenDuration     time.Duration
	MaxResponseBytes int
}

// LoadConfig resolves the provider config. There is deliberately NO
// endpoint override: the host owns the destination.
func LoadConfig(lookup LookupEnv, maxResponseBytes int) (Config, error) {
	if lookup == nil {
		return Config{}, errors.New("mistral environment lookup is nil")
	}
	cfg := Config{
		RequestTimeout:   defaultRequestTimeout,
		FailureThreshold: defaultFailureThreshold,
		OpenDuration:     defaultOpenDuration,
		MaxResponseBytes: maxResponseBytes,
	}
	var err error
	if cfg.Enabled, err = envBool(lookup, "ORG_MODEL_PROVIDER_MISTRAL_ENABLED", false); err != nil {
		return Config{}, err
	}
	if raw, ok := lookup("ORG_MODEL_PROVIDER_MISTRAL_CREDENTIAL_FILE"); ok {
		cfg.CredentialFile = strings.TrimSpace(raw)
	}
	if cfg.RequestTimeout, err = envDuration(lookup, "ORG_MODEL_PROVIDER_MISTRAL_REQUEST_TIMEOUT", defaultRequestTimeout); err != nil {
		return Config{}, err
	}
	if cfg.FailureThreshold, err = envInt(lookup, "ORG_MODEL_PROVIDER_MISTRAL_CIRCUIT_FAILURE_THRESHOLD", defaultFailureThreshold); err != nil {
		return Config{}, err
	}
	if cfg.OpenDuration, err = envDuration(lookup, "ORG_MODEL_PROVIDER_MISTRAL_CIRCUIT_OPEN_DURATION", defaultOpenDuration); err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate enforces the structural contract.
func (c Config) Validate() error {
	if c.MaxResponseBytes < 1024 || c.MaxResponseBytes > 16<<20 {
		return fmt.Errorf("mistral maximum response bytes outside allowed range")
	}
	if c.RequestTimeout < time.Second || c.RequestTimeout > 30*time.Minute {
		return fmt.Errorf("mistral request timeout outside allowed range")
	}
	if c.FailureThreshold < 1 || c.FailureThreshold > 100 {
		return fmt.Errorf("mistral circuit failure threshold outside allowed range")
	}
	if c.OpenDuration < time.Second || c.OpenDuration > 30*time.Minute {
		return fmt.Errorf("mistral circuit open duration outside allowed range")
	}
	if !c.Enabled {
		return nil
	}
	if strings.TrimSpace(c.CredentialFile) == "" || !filepath.IsAbs(filepath.Clean(c.CredentialFile)) {
		return fmt.Errorf("mistral credential file must be an absolute path")
	}
	return c.openAIConfig().Validate()
}

// openAIConfig maps onto the shared OpenAI-compatible transport with the
// FIXED official endpoint.
func (c Config) openAIConfig() openaicompat.Config {
	return openaicompat.Config{
		Enabled:          c.Enabled,
		EndpointURL:      fixedChatCompletionsEndpoint,
		CredentialFile:   c.CredentialFile,
		RequestTimeout:   c.RequestTimeout,
		FailureThreshold: c.FailureThreshold,
		OpenDuration:     c.OpenDuration,
		MaxResponseBytes: c.MaxResponseBytes,
	}
}

// New builds the canonical adapter instance under the host-owned
// "mistral" provider identity.
func New(config Config) (*openaicompat.Adapter, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return openaicompat.NewForProvider(config.openAIConfig(), ProviderID)
}

// Endpoint returns the fixed chat endpoint (exposed for tests/observability;
// never configurable at runtime).
func (c Config) Endpoint() string { return fixedChatCompletionsEndpoint }

func envBool(lookup LookupEnv, key string, fallback bool) (bool, error) {
	raw, ok := lookup(key)
	if !ok || strings.TrimSpace(raw) == "" {
		return fallback, nil
	}
	value, err := strconv.ParseBool(strings.TrimSpace(raw))
	if err != nil {
		return fallback, fmt.Errorf("%s: %w", key, err)
	}
	return value, nil
}

func envInt(lookup LookupEnv, key string, fallback int) (int, error) {
	raw, ok := lookup(key)
	if !ok || strings.TrimSpace(raw) == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return fallback, fmt.Errorf("%s: %w", key, err)
	}
	return value, nil
}

func envDuration(lookup LookupEnv, key string, fallback time.Duration) (time.Duration, error) {
	raw, ok := lookup(key)
	if !ok || strings.TrimSpace(raw) == "" {
		return fallback, nil
	}
	value, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil {
		return fallback, fmt.Errorf("%s: %w", key, err)
	}
	return value, nil
}
