package mistral

// Model catalog: GET https://api.mistral.ai/v1/models
// Host-fixed, HTTPS, same bearer secret as the chat adapter, no redirects,
// bounded body, timeout, context cancellation. Feeds model validation
// (exists, not archived, completion_chat). Cached with bounded TTL
// (default 30m); singleflight so concurrent readers share one refresh.
// No per-invocation /v1/models request is ever issued.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	fixedModelsEndpoint     = "https://api.mistral.ai/v1/models"
	DefaultCatalogTTL       = 30 * time.Minute
	maxCatalogResponseBytes = 4 << 20
	catalogHTTPTimeout      = 15 * time.Second
)

var (
	ErrCatalogUnavailable     = errors.New("mistral catalog unavailable")
	ErrCatalogUnauthorized    = errors.New("mistral catalog unauthorized")
	ErrModelNotInCatalog      = errors.New("mistral model not in catalog")
	ErrModelArchived          = errors.New("mistral model archived")
	ErrModelCapabilityMissing = errors.New("mistral model missing completion_chat capability")
	ErrCatalogMalformed       = errors.New("mistral catalog response malformed")
	ErrCatalogOversized       = errors.New("mistral catalog response oversized")
)

// CatalogEntry is the host's normalized view of one live Mistral model.
//
// The real GET /v1/models response (verified live against the account,
// 2026-09) does not carry a flat "archived" boolean or a "capabilities"
// string array -- both were assumed, not observed, by an earlier version
// of this file. The real shape is:
//
//	{"id": "...", "capabilities": {"completion_chat": true, ...}, "deprecation": null, ...}
//
// Archived is derived from "deprecation" being present and non-null: no
// account-visible model currently has a non-null deprecation, so its
// concrete shape (string date vs. object) has not been observed live.
// Presence/nullness is the only fact this code depends on.
type CatalogEntry struct {
	ID             string
	Archived       bool
	CompletionChat bool
}

type CatalogSnapshot struct {
	Models    []CatalogEntry
	FetchedAt time.Time
	ExpiresAt time.Time
}

func (s *CatalogSnapshot) lookup(modelID string) (CatalogEntry, bool) {
	for _, m := range s.Models {
		if m.ID == modelID {
			return m, true
		}
	}
	return CatalogEntry{}, false
}

type CatalogConfig struct {
	TokenLoader func(context.Context) (string, error)
	TTL         time.Duration
	Now         func() time.Time
	Client      *http.Client
}

type Catalog struct {
	cfg    CatalogConfig
	ttl    time.Duration
	client *http.Client

	mu           sync.Mutex
	snapshot     *CatalogSnapshot
	inflight     bool
	inflightDone chan struct{}
	inflightErr  error
	inflightSnap *CatalogSnapshot
}

func NewCatalog(cfg CatalogConfig) (*Catalog, error) {
	if cfg.TokenLoader == nil {
		return nil, errors.New("mistral catalog: nil token loader")
	}
	ttl := cfg.TTL
	if ttl <= 0 {
		ttl = DefaultCatalogTTL
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	client := cfg.Client
	if client == nil {
		client = &http.Client{
			Timeout: catalogHTTPTimeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}
	return &Catalog{cfg: cfg, ttl: ttl, client: client}, nil
}

// ValidateModel: nil when the model exists, is not archived, and serves
// completion_chat. Never issues a per-invocation upstream request.
func (c *Catalog) ValidateModel(ctx context.Context, modelID string) error {
	snap, err := c.Snapshot(ctx)
	if err != nil {
		return err
	}
	entry, ok := snap.lookup(modelID)
	if !ok {
		return fmt.Errorf("%w: %s", ErrModelNotInCatalog, modelID)
	}
	if entry.Archived {
		return fmt.Errorf("%w: %s", ErrModelArchived, modelID)
	}
	if !entry.CompletionChat {
		return fmt.Errorf("%w: %s", ErrModelCapabilityMissing, modelID)
	}
	return nil
}

// Snapshot returns the current snapshot, refreshing upstream when absent or
// expired. Concurrent callers block on the in-flight refresh and share its
// result: at most one upstream request.
func (c *Catalog) Snapshot(ctx context.Context) (*CatalogSnapshot, error) {
	c.mu.Lock()
	now := c.cfg.Now()
	if c.snapshot != nil && now.Before(c.snapshot.ExpiresAt) {
		snap := c.snapshot
		c.mu.Unlock()
		return snap, nil
	}
	if c.inflight {
		done := c.inflightDone
		c.mu.Unlock()
		select {
		case <-done:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		c.mu.Lock()
		snap, err := c.inflightSnap, c.inflightErr
		c.mu.Unlock()
		if err != nil {
			return nil, err
		}
		if snap == nil {
			return nil, ErrCatalogUnavailable
		}
		return snap, nil
	}
	c.inflight = true
	c.inflightDone = make(chan struct{})
	c.mu.Unlock()

	snap, err := c.fetch(ctx)

	c.mu.Lock()
	c.inflight = false
	c.inflightErr = err
	c.inflightSnap = snap
	if err == nil && snap != nil {
		c.snapshot = snap
	}
	close(c.inflightDone)
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if snap == nil {
		return nil, ErrCatalogUnavailable
	}
	return snap, nil
}

// fetch performs the upstream GET with all security constraints.
func (c *Catalog) fetch(ctx context.Context) (*CatalogSnapshot, error) {
	token, err := c.cfg.TokenLoader(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: token load: %v", ErrCatalogUnavailable, err)
	}
	parsed, err := url.Parse(fixedModelsEndpoint)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.Host != "api.mistral.ai" {
		return nil, fmt.Errorf("%w: endpoint rejected", ErrCatalogUnavailable)
	}
	reqCtx, cancel := context.WithTimeout(ctx, catalogHTTPTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, fixedModelsEndpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCatalogUnavailable, err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCatalogUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, ErrCatalogUnauthorized
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: status %d", ErrCatalogUnavailable, resp.StatusCode)
	}
	limited := io.LimitReader(resp.Body, maxCatalogResponseBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("%w: read: %v", ErrCatalogUnavailable, err)
	}
	if len(body) > maxCatalogResponseBytes {
		return nil, ErrCatalogOversized
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return nil, ErrCatalogMalformed
	}
	var wire struct {
		Data []struct {
			ID           string          `json:"id"`
			Deprecation  json.RawMessage `json:"deprecation"`
			Capabilities struct {
				CompletionChat bool `json:"completion_chat"`
			} `json:"capabilities"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCatalogMalformed, err)
	}
	now := c.cfg.Now()
	snap := &CatalogSnapshot{FetchedAt: now, ExpiresAt: now.Add(c.ttl)}
	for _, m := range wire.Data {
		if m.ID == "" {
			continue
		}
		archived := len(m.Deprecation) > 0 && string(m.Deprecation) != "null"
		snap.Models = append(snap.Models, CatalogEntry{ID: m.ID, Archived: archived, CompletionChat: m.Capabilities.CompletionChat})
	}
	return snap, nil
}
