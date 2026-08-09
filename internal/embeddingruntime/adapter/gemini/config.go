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
	ProviderID = "gemini"

	// PromptTemplateV1 renders EmbedItem.Text with a deterministic, versioned
	// prefix distinguishing query vs document intent. gemini-embedding-2
	// does not honor the legacy task_type request field the way older
	// embedding models did (confirmed against Google's live documentation
	// at implementation time: task specification for this model is
	// prompt-based). The adapter also sets the taskType field defensively
	// (see request.go) since Google's general embeddings API schema still
	// documents it as a valid field — sending both costs nothing and covers
	// either interpretation. Bumping this constant is a deliberate,
	// versioned decision: vectors produced under different template
	// versions are not comparable to each other, so changing the prefix
	// format requires re-embedding everything under the new version, never
	// silently changing what an existing version means.
	PromptTemplateV1 = "prompt-template.v1"

	defaultRequestTimeout   = 2 * time.Minute
	defaultFailureThreshold = 5
	defaultOpenDuration     = 30 * time.Second
)

type LookupEnv func(string) (string, bool)

type Config struct {
	Enabled          bool
	BaseURL          string
	CredentialFile   string
	RequestTimeout   time.Duration
	FailureThreshold int
	OpenDuration     time.Duration
	MaxResponseBytes int
}

func LoadConfig(lookup LookupEnv, maxResponseBytes int) (Config, error) {
	if lookup == nil {
		return Config{}, errors.New("embeddingruntime gemini environment lookup is nil")
	}
	cfg := Config{RequestTimeout: defaultRequestTimeout, FailureThreshold: defaultFailureThreshold, OpenDuration: defaultOpenDuration, MaxResponseBytes: maxResponseBytes}
	var err error
	if cfg.Enabled, err = envBool(lookup, "ORG_EMBEDDING_PROVIDER_GEMINI_ENABLED", false); err != nil {
		return Config{}, err
	}
	if raw, ok := lookup("ORG_EMBEDDING_PROVIDER_GEMINI_BASE_URL"); ok {
		cfg.BaseURL = strings.TrimSpace(raw)
	}
	if raw, ok := lookup("ORG_EMBEDDING_PROVIDER_GEMINI_CREDENTIAL_FILE"); ok {
		cfg.CredentialFile = strings.TrimSpace(raw)
	}
	if cfg.RequestTimeout, err = envDuration(lookup, "ORG_EMBEDDING_PROVIDER_GEMINI_REQUEST_TIMEOUT", defaultRequestTimeout); err != nil {
		return Config{}, err
	}
	if cfg.FailureThreshold, err = envInt(lookup, "ORG_EMBEDDING_PROVIDER_GEMINI_CIRCUIT_FAILURE_THRESHOLD", defaultFailureThreshold); err != nil {
		return Config{}, err
	}
	if cfg.OpenDuration, err = envDuration(lookup, "ORG_EMBEDDING_PROVIDER_GEMINI_CIRCUIT_OPEN_DURATION", defaultOpenDuration); err != nil {
		return Config{}, err
	}
	if err = cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	if c.MaxResponseBytes < 1024 || c.MaxResponseBytes > 16<<20 {
		return fmt.Errorf("embeddingruntime gemini maximum response bytes outside allowed range")
	}
	if c.RequestTimeout < time.Second || c.RequestTimeout > 30*time.Minute {
		return fmt.Errorf("embeddingruntime gemini request timeout outside allowed range")
	}
	if c.FailureThreshold < 1 || c.FailureThreshold > 100 {
		return fmt.Errorf("embeddingruntime gemini circuit failure threshold outside allowed range")
	}
	if c.OpenDuration < time.Second || c.OpenDuration > 30*time.Minute {
		return fmt.Errorf("embeddingruntime gemini circuit open duration outside allowed range")
	}
	if !c.Enabled {
		return nil
	}
	endpoint, err := url.Parse(c.BaseURL)
	if err != nil {
		return fmt.Errorf("parse embeddingruntime gemini base url: %w", err)
	}
	if endpoint.Scheme != "https" || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || endpoint.Path != "" {
		return fmt.Errorf("embeddingruntime gemini base url must be an absolute HTTPS host with no path/userinfo/query/fragment (e.g. https://generativelanguage.googleapis.com)")
	}
	if strings.TrimSpace(c.CredentialFile) == "" || !filepath.IsAbs(filepath.Clean(c.CredentialFile)) {
		return fmt.Errorf("embeddingruntime gemini credential file must be an absolute path")
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
			return errors.New("embeddingruntime gemini redirects are forbidden")
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
