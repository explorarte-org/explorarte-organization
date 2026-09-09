// Package cloudflare adapts Cloudflare Workers AI's OpenAI-compatible
// Chat Completions endpoint for the model runtime.
package cloudflare

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
	ProviderID = "cloudflare_workers_ai"

	defaultRequestTimeout   = 10 * time.Minute
	defaultFailureThreshold = 5
	defaultOpenDuration     = 30 * time.Second
)

type LookupEnv func(string) (string, bool)

type Config struct {
	Enabled          bool
	AccountID        string
	CredentialFile   string
	RequestTimeout   time.Duration
	FailureThreshold int
	OpenDuration     time.Duration
	MaxResponseBytes int
}

func LoadConfig(lookup LookupEnv, maxResponseBytes int) (Config, error) {
	if lookup == nil {
		return Config{}, errors.New("cloudflare Workers AI environment lookup is nil")
	}
	cfg := Config{
		RequestTimeout:   defaultRequestTimeout,
		FailureThreshold: defaultFailureThreshold,
		OpenDuration:     defaultOpenDuration,
		MaxResponseBytes: maxResponseBytes,
	}
	var err error
	if cfg.Enabled, err = envBool(lookup, "ORG_MODEL_PROVIDER_CLOUDFLARE_ENABLED", false); err != nil {
		return Config{}, err
	}
	if raw, ok := lookup("CLOUDFLARE_ACCOUNT_ID"); ok {
		cfg.AccountID = strings.TrimSpace(raw)
	}
	if raw, ok := lookup("ORG_MODEL_PROVIDER_CLOUDFLARE_CREDENTIAL_FILE"); ok {
		cfg.CredentialFile = strings.TrimSpace(raw)
	}
	if cfg.RequestTimeout, err = envDuration(lookup, "ORG_MODEL_PROVIDER_CLOUDFLARE_REQUEST_TIMEOUT", defaultRequestTimeout); err != nil {
		return Config{}, err
	}
	if cfg.FailureThreshold, err = envInt(lookup, "ORG_MODEL_PROVIDER_CLOUDFLARE_CIRCUIT_FAILURE_THRESHOLD", defaultFailureThreshold); err != nil {
		return Config{}, err
	}
	if cfg.OpenDuration, err = envDuration(lookup, "ORG_MODEL_PROVIDER_CLOUDFLARE_CIRCUIT_OPEN_DURATION", defaultOpenDuration); err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) EndpointURL() string {
	return "https://api.cloudflare.com/client/v4/accounts/" + c.AccountID + "/ai/v1/chat/completions"
}

func (c Config) Validate() error {
	if c.MaxResponseBytes < 1024 || c.MaxResponseBytes > 16<<20 {
		return fmt.Errorf("cloudflare Workers AI maximum response bytes outside allowed range")
	}
	if c.RequestTimeout < time.Second || c.RequestTimeout > 30*time.Minute {
		return fmt.Errorf("cloudflare Workers AI request timeout outside allowed range")
	}
	if c.FailureThreshold < 1 || c.FailureThreshold > 100 {
		return fmt.Errorf("cloudflare Workers AI circuit failure threshold outside allowed range")
	}
	if c.OpenDuration < time.Second || c.OpenDuration > 30*time.Minute {
		return fmt.Errorf("cloudflare Workers AI circuit open duration outside allowed range")
	}
	if !c.Enabled {
		return nil
	}
	if !validAccountID(c.AccountID) {
		return fmt.Errorf("cloudflare Workers AI account ID must be a 32-character hexadecimal ID")
	}
	if strings.TrimSpace(c.CredentialFile) == "" || !filepath.IsAbs(filepath.Clean(c.CredentialFile)) {
		return fmt.Errorf("cloudflare Workers AI credential file must be an absolute path")
	}
	return c.openAIConfig().Validate()
}

func (c Config) openAIConfig() openaicompat.Config {
	return openaicompat.Config{
		Enabled:          c.Enabled,
		EndpointURL:      c.EndpointURL(),
		CredentialFile:   c.CredentialFile,
		RequestTimeout:   c.RequestTimeout,
		FailureThreshold: c.FailureThreshold,
		OpenDuration:     c.OpenDuration,
		MaxResponseBytes: c.MaxResponseBytes,
	}
}

func New(config Config) (*openaicompat.Adapter, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return openaicompat.NewForProvider(config.openAIConfig(), ProviderID)
}

func validAccountID(value string) bool {
	if len(value) != 32 {
		return false
	}
	return strings.Trim(value, "0123456789abcdefABCDEF") == ""
}

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
