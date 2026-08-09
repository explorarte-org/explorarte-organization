// Package bgem3 implements embeddingruntime.OnlineAdapter against a local
// BAAI/bge-m3 sidecar process — R30's local, operational embedding index,
// kept strictly separate from Gemini (R29's frozen reference/canary
// baseline): different dimension (1024 vs 768), different derived tables,
// never mixed in a single query.
//
// This package is a narrow HTTP client. It never spawns, embeds, or links
// against the sidecar's own runtime (no Python inside orgd) — the sidecar
// is a separate, independently hardened process this adapter only talks
// to over loopback HTTP or a Unix domain socket, per R30's hardware gate.
// Every hardening requirement that belongs on the Go side lives here:
// loopback-only endpoint validation, pinned model identity (revision +
// artifact SHA-256, never auto-resolved), bounded concurrency and queue
// depth, byte/item limits, deadlines from ctx, dimension/NaN/Inf/empty
// vector validation, and never logging full input text. Requirements that
// belong to the sidecar process itself (unprivileged user, read-only model
// directory, no network after provisioning, no auto-download at boot) are
// documented in the accompanying runbook, not enforced from here — a
// remote HTTP client structurally cannot enforce another process's
// privilege boundary.
package bgem3

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	// ProviderID identifies this adapter in embeddingruntime.EmbedRequest
	// and in derived-table rows (see migration for R30 phase 5) — never
	// "gemini", so a BGE-M3 vector can never be mistaken for one of
	// Gemini's by a query that only checks provider_id.
	ProviderID = "bge-m3-local"

	// PromptTemplateV1 is BGE-M3's own versioned query/document prefix
	// convention, independent of Gemini's PromptTemplateV1 in the gemini
	// adapter package — the two must never be assumed compatible even
	// though they share a name pattern.
	PromptTemplateV1 = "bge-m3-prompt-template.v1"

	defaultRequestTimeout = 30 * time.Second
	sha256HexLength       = 64
)

var artifactSHA256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type LookupEnv func(string) (string, bool)

// Config pins every value R30's hardware/hardening gate requires be fixed
// and auditable rather than left to runtime discovery: the exact model
// artifact, its dimension, and hard ceilings on what a single process is
// allowed to do to itself or the sidecar.
type Config struct {
	Enabled bool
	// BaseURL must resolve to loopback (127.0.0.1/::1/localhost) or a
	// "unix://" path — Validate rejects anything else. This is the Go-side
	// half of "no red después de aprovisionamiento": the client itself
	// cannot be pointed at a remote host even by misconfiguration.
	BaseURL string
	// ModelRevision and ArtifactSHA256 are the pinned identity of the
	// weights this adapter expects to be talking to. Health (see health.go)
	// compares the sidecar's reported values against these on every
	// readiness check — a sidecar that silently swapped weights, or was
	// never given the pinned artifact, is treated as unhealthy, never used.
	ModelRevision  string
	ArtifactSHA256 string
	// ExpectedDimension is 1024 for BAAI/bge-m3 dense output — fixed here,
	// not read from the sidecar, so a dimension mismatch is caught as a
	// hard local error rather than silently propagating a wrong-shaped
	// vector into a vector(1024) column.
	ExpectedDimension     int
	PromptTemplateVersion string
	RequestTimeout        time.Duration
	// MaxConcurrency bounds in-flight requests to the sidecar; MaxQueueDepth
	// bounds how many callers may wait for a slot before Embed fails fast
	// instead of queuing unboundedly — R30's "concurrencia inicial=1" for
	// the current test VPS is expressed by setting both to 1.
	MaxConcurrency int
	MaxQueueDepth  int
	// MaxInputBytes and MaxItemsPerRequest bound a single Embed call's
	// footprint — required so one caller cannot exhaust the sidecar's
	// bounded queue/memory on its own.
	MaxInputBytes      int
	MaxItemsPerRequest int
	MaxResponseBytes   int
}

func LoadConfig(lookup LookupEnv) (Config, error) {
	if lookup == nil {
		return Config{}, errors.New("embeddingruntime bge-m3 environment lookup is nil")
	}
	cfg := Config{
		RequestTimeout: defaultRequestTimeout, ExpectedDimension: 1024, PromptTemplateVersion: PromptTemplateV1,
		MaxConcurrency: 1, MaxQueueDepth: 1, MaxInputBytes: 32 * 1024, MaxItemsPerRequest: 16, MaxResponseBytes: 8 << 20,
	}
	var err error
	if cfg.Enabled, err = envBool(lookup, "ORG_EMBEDDING_PROVIDER_BGE_M3_ENABLED", false); err != nil {
		return Config{}, err
	}
	if raw, ok := lookup("ORG_EMBEDDING_PROVIDER_BGE_M3_BASE_URL"); ok {
		cfg.BaseURL = strings.TrimSpace(raw)
	}
	if raw, ok := lookup("ORG_EMBEDDING_PROVIDER_BGE_M3_MODEL_REVISION"); ok {
		cfg.ModelRevision = strings.TrimSpace(raw)
	}
	if raw, ok := lookup("ORG_EMBEDDING_PROVIDER_BGE_M3_ARTIFACT_SHA256"); ok {
		cfg.ArtifactSHA256 = strings.ToLower(strings.TrimSpace(raw))
	}
	if cfg.RequestTimeout, err = envDuration(lookup, "ORG_EMBEDDING_PROVIDER_BGE_M3_REQUEST_TIMEOUT", cfg.RequestTimeout); err != nil {
		return Config{}, err
	}
	if cfg.MaxConcurrency, err = envInt(lookup, "ORG_EMBEDDING_PROVIDER_BGE_M3_MAX_CONCURRENCY", cfg.MaxConcurrency); err != nil {
		return Config{}, err
	}
	if cfg.MaxQueueDepth, err = envInt(lookup, "ORG_EMBEDDING_PROVIDER_BGE_M3_MAX_QUEUE_DEPTH", cfg.MaxQueueDepth); err != nil {
		return Config{}, err
	}
	if cfg.MaxInputBytes, err = envInt(lookup, "ORG_EMBEDDING_PROVIDER_BGE_M3_MAX_INPUT_BYTES", cfg.MaxInputBytes); err != nil {
		return Config{}, err
	}
	if cfg.MaxItemsPerRequest, err = envInt(lookup, "ORG_EMBEDDING_PROVIDER_BGE_M3_MAX_ITEMS_PER_REQUEST", cfg.MaxItemsPerRequest); err != nil {
		return Config{}, err
	}
	if err = cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	if c.ExpectedDimension <= 0 || c.ExpectedDimension > 8192 {
		return fmt.Errorf("embeddingruntime bge-m3 expected dimension out of bounds")
	}
	if c.RequestTimeout < 100*time.Millisecond || c.RequestTimeout > 5*time.Minute {
		return fmt.Errorf("embeddingruntime bge-m3 request timeout out of bounds")
	}
	if c.MaxConcurrency < 1 || c.MaxConcurrency > 64 {
		return fmt.Errorf("embeddingruntime bge-m3 max concurrency out of bounds")
	}
	if c.MaxQueueDepth < 0 || c.MaxQueueDepth > 256 {
		return fmt.Errorf("embeddingruntime bge-m3 max queue depth out of bounds")
	}
	if c.MaxInputBytes < 1 || c.MaxInputBytes > 1<<20 {
		return fmt.Errorf("embeddingruntime bge-m3 max input bytes out of bounds")
	}
	if c.MaxItemsPerRequest < 1 || c.MaxItemsPerRequest > 256 {
		return fmt.Errorf("embeddingruntime bge-m3 max items per request out of bounds")
	}
	if c.MaxResponseBytes < 1024 || c.MaxResponseBytes > 64<<20 {
		return fmt.Errorf("embeddingruntime bge-m3 max response bytes out of bounds")
	}
	if strings.TrimSpace(c.PromptTemplateVersion) == "" {
		return fmt.Errorf("embeddingruntime bge-m3 prompt template version is required")
	}
	if !c.Enabled {
		return nil
	}
	if strings.TrimSpace(c.ModelRevision) == "" {
		return fmt.Errorf("embeddingruntime bge-m3 model revision is required when enabled")
	}
	if len(c.ArtifactSHA256) != sha256HexLength || !artifactSHA256Pattern.MatchString(c.ArtifactSHA256) {
		return fmt.Errorf("embeddingruntime bge-m3 artifact sha256 must be a 64-character lowercase hex digest, pinned at provisioning time, never auto-resolved")
	}
	if err := validateLoopbackURL(c.BaseURL); err != nil {
		return fmt.Errorf("embeddingruntime bge-m3 base url: %w", err)
	}
	return nil
}

// validateLoopbackURL is the Go-side enforcement of R30's "loopback/Unix
// socket only" requirement: whatever process hosts the sidecar, this
// client refuses to be configured to reach anything else, including by a
// misconfigured environment variable pointing at a public host.
func validateLoopbackURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return errors.New("base url is required when enabled")
	}
	if strings.HasPrefix(raw, "unix://") {
		if strings.TrimPrefix(raw, "unix://") == "" {
			return errors.New("unix socket path is empty")
		}
		return nil
	}
	endpoint, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("parse base url: %w", err)
	}
	if endpoint.Scheme != "http" {
		return fmt.Errorf("base url must use http (loopback, no TLS termination needed) or unix://, got scheme %q", endpoint.Scheme)
	}
	host := endpoint.Hostname()
	if !isLoopbackHost(host) {
		return fmt.Errorf("base url host %q is not loopback — bge-m3 must never be reachable off-host", host)
	}
	if endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return errors.New("base url must not carry userinfo, query, or fragment")
	}
	return nil
}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func defaultHTTPClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 15 * time.Second}
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           dialer.DialContext,
		MaxIdleConns:          8,
		MaxIdleConnsPerHost:   8,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: timeout,
		ExpectContinueTimeout: time.Second,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
	}
	return &http.Client{
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("embeddingruntime bge-m3 redirects are forbidden")
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
