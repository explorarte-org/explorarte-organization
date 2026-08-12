package corpuscensus

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// BronzePaper mirrors one row of the harvester's own `papers` table
// verbatim (column names match, deliberately -- this package is a reader,
// never a migrator of the harvester's schema). Bronze is immutable: this
// package only ever runs read-only queries against the harvester's
// SQLite DB (or, in practice, a `.backup` snapshot of it taken before
// processing starts, since the harvester itself may still be running).
type BronzePaper struct {
	CanonicalID     string  `json:"canonical_id"`
	Title           string  `json:"title"`
	Year            *int    `json:"year"`
	AuthorsJSON     string  `json:"authors_json"`
	DOI             *string `json:"doi"`
	ArxivID         *string `json:"arxiv_id"`
	ACLID           *string `json:"acl_id"`
	OpenReviewID    *string `json:"openreview_id"`
	Venue           *string `json:"venue"`
	PublicationDate *string `json:"publication_date"`
	License         *string `json:"license"`
	SHA256          *string `json:"sha256"`
	LocalPath       *string `json:"local_path"`
	Status          string  `json:"status"`
	LastError       *string `json:"last_error"`
}

func (p BronzePaper) str(field *string) string {
	if field == nil {
		return ""
	}
	return *field
}

// BronzeSource mirrors one row of the harvester's `sources` table: which
// discovery catalog surfaced a canonical_id, and where in it.
type BronzeSource struct {
	CanonicalID string `json:"canonical_id"`
	Collection  string `json:"collection"`
	RepoName    string `json:"repo_name"`
	SourceFile  string `json:"source_file"`
	RawURL      string `json:"raw_url"`
}

// HarvesterReader is the seam between this package and the harvester's
// SQLite state -- an interface so tests can supply an in-memory fixture
// instead of shelling out.
type HarvesterReader interface {
	ListPapers(ctx context.Context) ([]BronzePaper, error)
	ListSources(ctx context.Context) ([]BronzeSource, error)
}

// SQLiteCLIReader implements HarvesterReader via the `sqlite3` CLI binary
// in read-only mode, invoked with an explicit argv slice -- never a
// shell, mirroring internal/pdfingest/poppler's own subprocess
// discipline. The query strings below are fixed constants, never built
// from caller input, so there is no injection surface despite this being
// "SQL to a CLI tool."
type SQLiteCLIReader struct {
	Binary string // resolved via exec.LookPath by NewSQLiteCLIReader
	DBPath string
}

func NewSQLiteCLIReader(dbPath string) (*SQLiteCLIReader, error) {
	binary, err := exec.LookPath("sqlite3")
	if err != nil {
		return nil, fmt.Errorf("corpuscensus: sqlite3 binary not found: %w", err)
	}
	return &SQLiteCLIReader{Binary: binary, DBPath: dbPath}, nil
}

const papersQuery = `SELECT canonical_id, title, year, authors_json, doi, arxiv_id, acl_id, openreview_id, venue, publication_date, license, sha256, local_path, status, last_error FROM papers ORDER BY canonical_id;`

const sourcesQuery = `SELECT canonical_id, collection, repo_name, source_file, raw_url FROM sources ORDER BY canonical_id, id;`

func (r *SQLiteCLIReader) query(ctx context.Context, sql string, out any) error {
	cmd := exec.CommandContext(ctx, r.Binary, "-readonly", "-json", r.DBPath, sql)
	stdout, err := cmd.Output()
	if err != nil {
		detail := ""
		if exitErr, ok := err.(*exec.ExitError); ok {
			detail = strings.TrimSpace(string(exitErr.Stderr))
		}
		return fmt.Errorf("corpuscensus: sqlite3 query failed: %w (%s)", err, detail)
	}
	trimmed := strings.TrimSpace(string(stdout))
	if trimmed == "" {
		return nil // empty table -- valid, out stays as its zero value
	}
	if err := json.Unmarshal([]byte(trimmed), out); err != nil {
		return fmt.Errorf("corpuscensus: decode sqlite3 JSON output: %w", err)
	}
	return nil
}

func (r *SQLiteCLIReader) ListPapers(ctx context.Context) ([]BronzePaper, error) {
	var papers []BronzePaper
	if err := r.query(ctx, papersQuery, &papers); err != nil {
		return nil, err
	}
	return papers, nil
}

func (r *SQLiteCLIReader) ListSources(ctx context.Context) ([]BronzeSource, error) {
	var sources []BronzeSource
	if err := r.query(ctx, sourcesQuery, &sources); err != nil {
		return nil, err
	}
	return sources, nil
}
