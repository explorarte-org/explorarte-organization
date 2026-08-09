package webevidence

import (
	"context"
	"fmt"
	"time"
)

// Fetcher performs the actual bounded, classified download this package
// never does itself. R30 deliberately ships no real implementation — no
// search/fetch provider is chosen in this phase (see the package doc
// comment) — only the interface and a fake for tests. Whatever
// implementation a later branch wires in is responsible for its own
// egress policy, size bounds, and content-type classification before a
// single byte reaches Ingest.
type Fetcher interface {
	Fetch(ctx context.Context, url string) (RawPage, error)
}

// IDGenerator produces a unique Evidence.ID — injected so Ingest stays
// deterministic and testable rather than reaching for crypto/rand or a
// wall-clock-derived ID itself.
type IDGenerator func() string

// Ingest is the harness's single entry point tying fetch, sanitize, hash,
// and chunk together into one validated, ready-to-Save Evidence. It never
// calls Store itself (the caller decides whether/when to persist) and
// never touches an embedding adapter or ranking (those are per-query
// concerns — see rank.go — not per-ingest ones). ttl must be within
// [MinTTL, MaxTTL]; Ingest does not clamp or default it, so a caller
// requesting an out-of-bounds TTL fails loudly here rather than silently
// downstream in Evidence.Validate.
func Ingest(ctx context.Context, fetcher Fetcher, organizationID string, taskID int64, url string, ttl time.Duration, newID IDGenerator, now time.Time) (Evidence, error) {
	if fetcher == nil {
		return Evidence{}, fmt.Errorf("%w: fetcher is required", ErrInvalidEvidence)
	}
	if newID == nil {
		return Evidence{}, fmt.Errorf("%w: id generator is required", ErrInvalidEvidence)
	}
	page, err := fetcher.Fetch(ctx, url)
	if err != nil {
		return Evidence{}, fmt.Errorf("webevidence: fetch %q: %w", url, err)
	}
	chunks, findings, err := Sanitize(page)
	if err != nil {
		return Evidence{}, err
	}
	evidence := Evidence{
		ID: newID(), OrganizationID: organizationID, TaskID: taskID, URL: page.URL,
		ContentHash: page.contentHash(), CapturedAt: now, ExpiresAt: now.Add(ttl),
		Chunks: chunks, SanitizationFindings: findings,
	}
	if err := evidence.Validate(); err != nil {
		return Evidence{}, err
	}
	return evidence, nil
}
