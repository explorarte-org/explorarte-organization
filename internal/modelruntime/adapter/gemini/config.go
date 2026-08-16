package gemini

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
	ProviderID            = "gemini"
	AdapterID             = "gemini_openai_compat_chat_completions"
	AdapterVersion        = 2
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

func LoadConfig(lookup LookupEnv, maxResponseBytes int) (Config, error) {
	if lookup == nil {
		return Config{}, errors.New("gemini environment lookup is nil")
	}
	cfg := Config{RequestTimeout: defaultRequestTimeout, FailureThreshold: defaultFailureThreshold, OpenDuration: defaultOpenDuration, MaxResponseBytes: maxResponseBytes}
	var err error
	if cfg.Enabled, err = envBool(lookup, "ORG_MODEL_PROVIDER_GEMINI_ENABLED", false); err != nil {
		return Config{}, err
	}
	if raw, ok := lookup("ORG_MODEL_PROVIDER_GEMINI_ENDPOINT_URL"); ok {
		cfg.EndpointURL = strings.TrimSpace(raw)
	}
	if raw, ok := lookup("ORG_MODEL_PROVIDER_GEMINI_CREDENTIAL_FILE"); ok {
		cfg.CredentialFile = strings.TrimSpace(raw)
	}
	if cfg.RequestTimeout, err = envDuration(lookup, "ORG_MODEL_PROVIDER_GEMINI_REQUEST_TIMEOUT", defaultRequestTimeout); err != nil {
		return Config{}, err
	}
	if cfg.FailureThreshold, err = envInt(lookup, "ORG_MODEL_PROVIDER_GEMINI_CIRCUIT_FAILURE_THRESHOLD", defaultFailureThreshold); err != nil {
		return Config{}, err
	}
	if cfg.OpenDuration, err = envDuration(lookup, "ORG_MODEL_PROVIDER_GEMINI_CIRCUIT_OPEN_DURATION", defaultOpenDuration); err != nil {
		return Config{}, err
	}
	if err = cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	if c.MaxResponseBytes < 1024 || c.MaxResponseBytes > 16<<20 {
		return fmt.Errorf("gemini maximum response bytes outside allowed range")
	}
	if c.RequestTimeout < time.Second || c.RequestTimeout > 30*time.Minute {
		return fmt.Errorf("gemini request timeout outside allowed range")
	}
	if c.FailureThreshold < 1 || c.FailureThreshold > 100 {
		return fmt.Errorf("gemini circuit failure threshold outside allowed range")
	}
	if c.OpenDuration < time.Second || c.OpenDuration > 30*time.Minute {
		return fmt.Errorf("gemini circuit open duration outside allowed range")
	}
	if !c.Enabled {
		return nil
	}
	endpoint, err := url.Parse(c.EndpointURL)
	if err != nil {
		return fmt.Errorf("parse gemini endpoint: %w", err)
	}
	if endpoint.Scheme != "https" || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return fmt.Errorf("gemini endpoint must be an absolute HTTPS URL without userinfo, query, or fragment")
	}
	// Google's documented OpenAI-compatible endpoint for Gemini is
	// https://generativelanguage.googleapis.com/v1beta/openai/chat/completions
	// — under /v1beta/openai/, not /v1/, unlike the generic openai_compatible
	// adapter's contract.
	if strings.TrimRight(endpoint.EscapedPath(), "/") != "/v1beta/openai/chat/completions" {
		return fmt.Errorf("gemini endpoint path must be exactly /v1beta/openai/chat/completions")
	}
	if strings.TrimSpace(c.CredentialFile) == "" || !filepath.IsAbs(filepath.Clean(c.CredentialFile)) {
		return fmt.Errorf("gemini credential file must be an absolute path")
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
			return errors.New("gemini redirects are forbidden")
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
