package mimo

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
	ProviderID            = "mimo"
	AdapterID             = "mimo_chat_completions"
	AdapterVersion        = 2
	RequestSchemaVersion  = "mimo.chat.completions.request.v1"
	ResponseSchemaVersion = "mimo.chat.completions.response.v1"

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
		return Config{}, errors.New("mimo environment lookup is nil")
	}
	cfg := Config{RequestTimeout: defaultRequestTimeout, FailureThreshold: defaultFailureThreshold, OpenDuration: defaultOpenDuration, MaxResponseBytes: maxResponseBytes}
	var err error
	if cfg.Enabled, err = envBool(lookup, "ORG_MODEL_PROVIDER_MIMO_ENABLED", false); err != nil {
		return Config{}, err
	}
	// EndpointURL is the full chat-completions URL to POST to (e.g.
	// https://token-plan-sgp.xiaomimimo.com/v1/chat/completions) -- the
	// base URL is never hardcoded, it comes entirely from this env var, so
	// the "/v1" prefix (or any future prefix) is whatever the deployment
	// configures, not something this adapter assumes.
	if raw, ok := lookup("ORG_MODEL_PROVIDER_MIMO_ENDPOINT_URL"); ok {
		cfg.EndpointURL = strings.TrimSpace(raw)
	}
	if raw, ok := lookup("ORG_MODEL_PROVIDER_MIMO_CREDENTIAL_FILE"); ok {
		cfg.CredentialFile = strings.TrimSpace(raw)
	}
	if cfg.RequestTimeout, err = envDuration(lookup, "ORG_MODEL_PROVIDER_MIMO_REQUEST_TIMEOUT", defaultRequestTimeout); err != nil {
		return Config{}, err
	}
	if cfg.FailureThreshold, err = envInt(lookup, "ORG_MODEL_PROVIDER_MIMO_CIRCUIT_FAILURE_THRESHOLD", defaultFailureThreshold); err != nil {
		return Config{}, err
	}
	if cfg.OpenDuration, err = envDuration(lookup, "ORG_MODEL_PROVIDER_MIMO_CIRCUIT_OPEN_DURATION", defaultOpenDuration); err != nil {
		return Config{}, err
	}
	if err = cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	if c.MaxResponseBytes < 1024 || c.MaxResponseBytes > 16<<20 {
		return fmt.Errorf("mimo maximum response bytes outside allowed range")
	}
	if c.RequestTimeout < time.Second || c.RequestTimeout > 30*time.Minute {
		return fmt.Errorf("mimo request timeout outside allowed range")
	}
	if c.FailureThreshold < 1 || c.FailureThreshold > 100 {
		return fmt.Errorf("mimo circuit failure threshold outside allowed range")
	}
	if c.OpenDuration < time.Second || c.OpenDuration > 30*time.Minute {
		return fmt.Errorf("mimo circuit open duration outside allowed range")
	}
	if !c.Enabled {
		return nil
	}
	endpoint, err := url.Parse(c.EndpointURL)
	if err != nil {
		return fmt.Errorf("parse mimo endpoint: %w", err)
	}
	if endpoint.Scheme != "https" || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return fmt.Errorf("mimo endpoint must be an absolute HTTPS URL without userinfo, query, or fragment")
	}
	// MiMo's Token Plan endpoint is documented as
	// {base_url}/chat/completions, where base_url is configurable
	// (audit: e.g. https://token-plan-sgp.xiaomimimo.com/v1) -- so unlike
	// DeepSeek's fixed no-prefix path, only the suffix is fixed here.
	if !strings.HasSuffix(strings.TrimRight(endpoint.EscapedPath(), "/"), "/chat/completions") {
		return fmt.Errorf("mimo endpoint path must end with /chat/completions")
	}
	if strings.TrimSpace(c.CredentialFile) == "" || !filepath.IsAbs(filepath.Clean(c.CredentialFile)) {
		return fmt.Errorf("mimo credential file must be an absolute path")
	}
	return nil
}

func defaultHTTPClient(requestTimeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		Proxy:               nil,
		DialContext:         dialer.DialContext,
		ForceAttemptHTTP2:   true,
		MaxIdleConns:        32,
		MaxIdleConnsPerHost: 16,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
		// ResponseHeaderTimeout measures the wait for the FIRST response byte.
		// A non-streaming completion sends none until it has finished
		// generating, so a fixed 90s here silently overrode whatever
		// RequestTimeout the deployment configured and cut calls at half the
		// bound an operator had set. The configured timeout is the authority;
		// this must never be the shorter of the two.
		ResponseHeaderTimeout: requestTimeout,
		ExpectContinueTimeout: time.Second,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
	}
	return &http.Client{
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("mimo redirects are forbidden")
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
