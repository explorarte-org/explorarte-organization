// Package webevidence is R30's harness for evidence gathered from the
// open web: fetch (via a caller-supplied Fetcher — this package chooses no
// real search/fetch provider), sanitize, hash, chunk, embed (via the same
// embeddingruntime.OnlineAdapter every other retrieval channel already
// uses — no new provider surface), and rank. Every Evidence this package
// produces is task-scoped, mandatorily time-limited, and permanently
// classified as untrusted data — never an instruction, never a candidate
// for RAG/Memory promotion by anything in this package. Promoting web
// content into permanent organizational knowledge is a decision this
// package structurally cannot make: it has no dependency on
// internal/rag or internal/memory, and produces no AdmissionAttestation.
// A human or policy that wants to promote a piece of web evidence must go
// through rag.Manager.Propose/memory.Manager.Propose exactly like any
// other candidate, with a real attestation — the same review discipline
// as everything else this system treats as organizational knowledge.
package webevidence

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// MinTTL/MaxTTL bound how long a piece of web evidence may remain
// queryable. There is no "forever" value — Evidence.Validate rejects a
// zero or unbounded TTL, so ephemerality is enforced at construction time,
// not left to a caller's discipline.
const (
	MinTTL = time.Minute
	MaxTTL = 7 * 24 * time.Hour
)

// RawPage is what a Fetcher returns — the caller-supplied, bounded,
// classified download. This package defines the shape a fetch must
// produce; it never performs the fetch itself.
type RawPage struct {
	URL         string
	Body        []byte
	ContentType string
	FetchedAt   time.Time
}

func (p RawPage) contentHash() string {
	sum := sha256.Sum256(p.Body)
	return hex.EncodeToString(sum[:])
}

// SanitizationFinding records that a prompt-injection-shaped pattern was
// detected in a chunk — never a reason to drop the chunk (the whole page
// is untrusted data regardless), only an audit signal. Excerpt is bounded
// so a finding itself never becomes a vector for smuggling a large amount
// of untrusted text into a log or downstream record.
type SanitizationFinding struct {
	ChunkOrdinal int
	Pattern      string
	Excerpt      string
}

// Chunk is one ephemeral, bounded slice of a sanitized page's text.
type Chunk struct {
	Ordinal int
	Text    string
}

// Evidence is one fetched-and-sanitized web page, always task-scoped,
// always time-limited, always untrusted. See the package doc comment for
// why nothing here can become a RAG/Memory candidate.
type Evidence struct {
	ID                   string
	OrganizationID       string
	TaskID               int64
	URL                  string
	ContentHash          string
	CapturedAt           time.Time
	ExpiresAt            time.Time
	Chunks               []Chunk
	SanitizationFindings []SanitizationFinding
}

var (
	ErrInvalidEvidence = errors.New("webevidence: invalid evidence")
	ErrExpired         = errors.New("webevidence: evidence has expired")
)

func (e Evidence) Validate() error {
	if strings.TrimSpace(e.ID) == "" {
		return fmt.Errorf("%w: id is required", ErrInvalidEvidence)
	}
	if strings.TrimSpace(e.OrganizationID) == "" {
		return fmt.Errorf("%w: organization id is required", ErrInvalidEvidence)
	}
	if e.TaskID <= 0 {
		return fmt.Errorf("%w: task id is required — web evidence is always task-scoped, never global", ErrInvalidEvidence)
	}
	if strings.TrimSpace(e.URL) == "" {
		return fmt.Errorf("%w: url is required", ErrInvalidEvidence)
	}
	if len(e.ContentHash) != 64 {
		return fmt.Errorf("%w: content hash must be a 64-character hex digest", ErrInvalidEvidence)
	}
	if e.CapturedAt.IsZero() {
		return fmt.Errorf("%w: captured_at is required", ErrInvalidEvidence)
	}
	if e.ExpiresAt.IsZero() || !e.ExpiresAt.After(e.CapturedAt) {
		return fmt.Errorf("%w: expires_at must be after captured_at — evidence with no expiry is not evidence this package can produce", ErrInvalidEvidence)
	}
	ttl := e.ExpiresAt.Sub(e.CapturedAt)
	if ttl < MinTTL || ttl > MaxTTL {
		return fmt.Errorf("%w: ttl %s out of bounds [%s, %s]", ErrInvalidEvidence, ttl, MinTTL, MaxTTL)
	}
	if len(e.Chunks) == 0 {
		return fmt.Errorf("%w: at least one chunk is required", ErrInvalidEvidence)
	}
	seen := make(map[int]struct{}, len(e.Chunks))
	for _, chunk := range e.Chunks {
		if strings.TrimSpace(chunk.Text) == "" {
			return fmt.Errorf("%w: chunk %d has empty text", ErrInvalidEvidence, chunk.Ordinal)
		}
		if _, dup := seen[chunk.Ordinal]; dup {
			return fmt.Errorf("%w: duplicate chunk ordinal %d", ErrInvalidEvidence, chunk.Ordinal)
		}
		seen[chunk.Ordinal] = struct{}{}
	}
	return nil
}

// Expired reports whether e is past its mandatory TTL as of now — callers
// (in particular any Store) must treat an expired Evidence as absent, not
// merely stale.
func (e Evidence) Expired(now time.Time) bool {
	return !now.Before(e.ExpiresAt)
}

// Citation is the bounded, provenance-carrying reference a coordinator or
// CEO-tier consumer actually cites — never the raw page, never more than
// one chunk's worth of text.
type Citation struct {
	URL          string
	ContentHash  string
	CapturedAt   time.Time
	ChunkOrdinal int
	Excerpt      string
}

const maxCitationExcerptBytes = 500

// NewCitation builds a Citation for chunkOrdinal within evidence, bounding
// the excerpt so a citation can never smuggle an entire page's worth of
// untrusted text past whatever budget/logging limits apply to a citation.
func NewCitation(evidence Evidence, chunkOrdinal int) (Citation, error) {
	if err := evidence.Validate(); err != nil {
		return Citation{}, err
	}
	for _, chunk := range evidence.Chunks {
		if chunk.Ordinal != chunkOrdinal {
			continue
		}
		excerpt := chunk.Text
		if len(excerpt) > maxCitationExcerptBytes {
			excerpt = excerpt[:maxCitationExcerptBytes]
		}
		return Citation{
			URL: evidence.URL, ContentHash: evidence.ContentHash, CapturedAt: evidence.CapturedAt,
			ChunkOrdinal: chunkOrdinal, Excerpt: excerpt,
		}, nil
	}
	return Citation{}, fmt.Errorf("%w: no chunk with ordinal %d in evidence %s", ErrInvalidEvidence, chunkOrdinal, evidence.ID)
}
