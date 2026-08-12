package corpusenrich

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

var batchEndpoint = "https://api.semanticscholar.org/graph/v1/paper/batch?fields=paperId,title,abstract,year,externalIds"

// Client is a plain net/http client -- no subprocess, no shell, no CLI
// tool involved (unlike corpuscensus's SQLite/Poppler adapters, this is
// a pure network call, so there is nothing to LookPath or sandbox via a
// separate image). Confirmed live: works unauthenticated, so no API key
// handling exists in this package at all today.
type Client struct {
	HTTPClient *http.Client
}

func NewClient(timeout time.Duration) *Client {
	return &Client{HTTPClient: &http.Client{Timeout: timeout}}
}

// wireEntry mirrors one element of the batch response -- null for a
// paperId Semantic Scholar could not resolve (kept distinct from "found
// but no abstract").
type wireEntry struct {
	PaperID     string `json:"paperId"`
	Title       string `json:"title"`
	Abstract    string `json:"abstract"`
	Year        int    `json:"year"`
	ExternalIDs struct {
		ArXiv string `json:"ArXiv"`
		DOI   string `json:"DOI"`
	} `json:"externalIds"`
}

// ErrRateLimited signals the caller should back off -- this package
// never retries internally; the orchestrator decides the backoff policy
// so it stays visible/auditable rather than hidden inside the client.
var ErrRateLimited = fmt.Errorf("corpusenrich: rate limited (429)")

// FetchBatch sends up to 500 paperIDs (Semantic Scholar's own documented
// batch cap) in one POST and returns one AbstractRecord per input ID, in
// the same order -- a nil response slot becomes a zero-value record with
// HTTPStatus set to the overall response's status so the caller can tell
// "this specific ID wasn't found" apart from "the whole request failed."
func (c *Client) FetchBatch(ctx context.Context, paperIDs []string) ([]AbstractRecord, error) {
	if len(paperIDs) == 0 {
		return nil, nil
	}
	if len(paperIDs) > 500 {
		return nil, fmt.Errorf("corpusenrich: batch of %d exceeds Semantic Scholar's 500-ID cap", len(paperIDs))
	}
	payload, err := json.Marshal(struct {
		IDs []string `json:"ids"`
	}{paperIDs})
	if err != nil {
		return nil, fmt.Errorf("corpusenrich: encode request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, batchEndpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("corpusenrich: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("corpusenrich: request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("corpusenrich: read response: %w", err)
	}

	now := time.Now().UTC()
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, ErrRateLimited
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("corpusenrich: unexpected status %d: %s", resp.StatusCode, boundedPreview(body))
	}

	var entries []*wireEntry
	if err := json.Unmarshal(body, &entries); err != nil {
		return nil, fmt.Errorf("corpusenrich: decode response: %w", err)
	}
	if len(entries) != len(paperIDs) {
		return nil, fmt.Errorf("corpusenrich: sent %d IDs, got %d results", len(paperIDs), len(entries))
	}

	records := make([]AbstractRecord, len(paperIDs))
	for i, id := range paperIDs {
		entry := entries[i]
		if entry == nil {
			records[i] = AbstractRecord{PaperID: id, HTTPStatus: resp.StatusCode, FetchedAt: now}
			continue
		}
		records[i] = AbstractRecord{
			PaperID: id, Title: entry.Title, Abstract: entry.Abstract, Year: entry.Year,
			ArxivID: entry.ExternalIDs.ArXiv, DOI: entry.ExternalIDs.DOI,
			HTTPStatus: resp.StatusCode, FetchedAt: now,
		}
	}
	return records, nil
}

func boundedPreview(body []byte) string {
	const max = 300
	if len(body) > max {
		return string(body[:max]) + "..."
	}
	return string(body)
}
