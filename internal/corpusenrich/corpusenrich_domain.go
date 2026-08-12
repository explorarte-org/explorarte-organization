// Package corpusenrich fetches abstracts for Works this corpus already
// resolved a Semantic Scholar paperId for (internal/corpuscensus's
// Bronze/Silver stages never touch this -- it is a separate, later
// enrichment step whose only output is an abstract-by-paperId JSONL
// file, consumed next by semantic clustering). Confirmed live (owner-
// directed test, 20 real paperIds, no API key): the batch endpoint
// (POST /graph/v1/paper/batch) returns 200, 100% abstract coverage,
// ~1.25s for 20 IDs, no rate-limit response observed. This package
// still treats 429/5xx as a real, expected condition (bounded backoff,
// then stop -- never silent infinite retry).
package corpusenrich

import "time"

// AbstractRecord is one Semantic Scholar batch response entry, reduced
// to only the fields this corpus needs (never storing the full raw API
// response -- unlike s2_id_cache upstream, whose job is caching
// everything, this package's job is producing a clean enrichment input).
type AbstractRecord struct {
	PaperID    string    `json:"paper_id"`
	Title      string    `json:"title"`
	Abstract   string    `json:"abstract"` // empty string means S2 had no abstract for this paper -- not an error
	Year       int       `json:"year,omitempty"`
	ArxivID    string    `json:"arxiv_id,omitempty"`
	DOI        string    `json:"doi,omitempty"`
	HTTPStatus int       `json:"http_status"`
	FetchedAt  time.Time `json:"fetched_at"`
}

func (r AbstractRecord) HasAbstract() bool { return r.Abstract != "" }
