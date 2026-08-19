package xai

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	ProviderID     = "xai"
	AdapterID      = "xai_chat_completions"
	AdapterVersion = 1
	// xAI serves the OpenAI chat-completions request and response shapes, so
	// the canonical schema versions are the shared ones rather than
	// provider-private names. The differences that do exist are in which
	// fields are current, not in the envelope -- see adapter.go.
	RequestSchemaVersion  = "openai.chat.completions.request.v1"
	ResponseSchemaVersion = "openai.chat.completions.response.v1"

	defaultRequestTimeout   = 2 * time.Minute
	defaultFailureThreshold = 5
	defaultOpenDuration     = 30 * time.Second
)

type LookupEnv func(string) (string, bool)

type Config struct {
	Enabled          bool
	EndpointURL      string
	CredentialFile   string
	RequestTimeout   time.Duration
	FailureThreshold int
	OpenDuration     time.Duration
	MaxResponseBytes int
}

// LoadConfig follows the env naming every other HTTP adapter here uses
// (ORG_MODEL_PROVIDER_<NAME>_ENDPOINT_URL / _CREDENTIAL_FILE /
// _REQUEST_TIMEOUT / _CIRCUIT_*), not a per-provider spelling. Enabled
// defaults to false, so an unconfigured deployment leaves the adversarial
// reviewer's provider absent from the registry rather than half-wired.
func LoadConfig(lookup LookupEnv, maxResponseBytes int) (Config, error) {
	if lookup == nil {
		return Config{}, errors.New("xai environment lookup is nil")
	}
	cfg := Config{RequestTimeout: defaultRequestTimeout, FailureThreshold: defaultFailureThreshold, OpenDuration: defaultOpenDuration, MaxResponseBytes: maxResponseBytes}
	var err error
	if cfg.Enabled, err = envBool(lookup, "ORG_MODEL_PROVIDER_XAI_ENABLED", false); err != nil {
		return Config{}, err
	}
	if raw, ok := lookup("ORG_MODEL_PROVIDER_XAI_ENDPOINT_URL"); ok {
		cfg.EndpointURL = strings.TrimSpace(raw)
	}
	if raw, ok := lookup("ORG_MODEL_PROVIDER_XAI_CREDENTIAL_FILE"); ok {
		cfg.CredentialFile = strings.TrimSpace(raw)
	}
	if cfg.RequestTimeout, err = envDuration(lookup, "ORG_MODEL_PROVIDER_XAI_REQUEST_TIMEOUT", defaultRequestTimeout); err != nil {
		return Config{}, err
	}
	if cfg.FailureThreshold, err = envInt(lookup, "ORG_MODEL_PROVIDER_XAI_CIRCUIT_FAILURE_THRESHOLD", defaultFailureThreshold); err != nil {
		return Config{}, err
	}
	if cfg.OpenDuration, err = envDuration(lookup, "ORG_MODEL_PROVIDER_XAI_CIRCUIT_OPEN_DURATION", defaultOpenDuration); err != nil {
		return Config{}, err
	}
	if err = cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	if c.MaxResponseBytes < 1024 || c.MaxResponseBytes > 16<<20 {
		return fmt.Errorf("xai maximum response bytes outside allowed range")
	}
	if c.RequestTimeout < time.Second || c.RequestTimeout > 30*time.Minute {
		return fmt.Errorf("xai request timeout outside allowed range")
	}
	if c.FailureThreshold < 1 || c.FailureThreshold > 100 {
		return fmt.Errorf("xai circuit failure threshold outside allowed range")
	}
	if c.OpenDuration < time.Second || c.OpenDuration > 30*time.Minute {
		return fmt.Errorf("xai circuit open duration outside allowed range")
	}
	if !c.Enabled {
		return nil
	}
	endpoint, err := url.Parse(c.EndpointURL)
	if err != nil {
		return fmt.Errorf("parse xai endpoint: %w", err)
	}
	if endpoint.Scheme != "https" || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return fmt.Errorf("xai endpoint must be an absolute HTTPS URL without userinfo, query, or fragment")
	}
	// xAI's documented chat endpoint is https://api.x.ai/v1/chat/completions.
	// The path is pinned exactly rather than by suffix so a misconfigured
	// proxy path cannot quietly satisfy the check.
	if strings.TrimRight(endpoint.EscapedPath(), "/") != "/v1/chat/completions" {
		return fmt.Errorf("xai endpoint path must be exactly /v1/chat/completions")
	}
	if strings.TrimSpace(c.CredentialFile) == "" || !filepath.IsAbs(filepath.Clean(c.CredentialFile)) {
		return fmt.Errorf("xai credential file must be an absolute path")
	}
	return nil
}

func defaultHTTPClient() *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          32,
		MaxIdleConnsPerHost:   16,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 90 * time.Second,
		ExpectContinueTimeout: time.Second,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
	}
	return &http.Client{
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("xai redirects are forbidden")
		},
	}
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
