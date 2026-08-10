package corpuscensus

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func requireSQLite3(t *testing.T) string {
	t.Helper()
	binary, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skipf("sqlite3 not available: %v", err)
	}
	return binary
}

func buildFixtureDB(t *testing.T) string {
	t.Helper()
	binary := requireSQLite3(t)
	path := filepath.Join(t.TempDir(), "fixture.sqlite3")
	schema := `
CREATE TABLE papers (
	canonical_id TEXT PRIMARY KEY, title TEXT, year INTEGER, authors_json TEXT NOT NULL DEFAULT '[]',
	doi TEXT, arxiv_id TEXT, acl_id TEXT, openreview_id TEXT, venue TEXT, publication_date TEXT,
	license TEXT, sha256 TEXT, local_path TEXT, status TEXT NOT NULL DEFAULT 'pending', last_error TEXT,
	created_at TEXT NOT NULL DEFAULT '', updated_at TEXT NOT NULL DEFAULT ''
);
CREATE TABLE sources (
	id INTEGER PRIMARY KEY AUTOINCREMENT, canonical_id TEXT NOT NULL, collection TEXT NOT NULL,
	repo_name TEXT NOT NULL, source_file TEXT NOT NULL, source_line INTEGER NOT NULL, raw_url TEXT NOT NULL, title_hint TEXT
);
INSERT INTO papers (canonical_id, title, year, doi, sha256, local_path, status) VALUES
	('doi:10.1/a', 'A Paper', 2024, '10.1/a', 'deadbeef', '/tmp/a.pdf', 'downloaded');
INSERT INTO sources (canonical_id, collection, repo_name, source_file, source_line, raw_url) VALUES
	('doi:10.1/a', 'rag', 'awesome-rag', 'README.md', 12, 'https://arxiv.org/pdf/x');
`
	cmd := exec.Command(binary, path)
	cmd.Stdin = strings.NewReader(schema)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fixture db: %v (%s)", err, out)
	}
	return path
}

func TestSQLiteCLIReaderListsPapersAndSources(t *testing.T) {
	dbPath := buildFixtureDB(t)
	reader, err := NewSQLiteCLIReader(dbPath)
	if err != nil {
		t.Skipf("sqlite3 not available: %v", err)
	}
	papers, err := reader.ListPapers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(papers) != 1 || papers[0].CanonicalID != "doi:10.1/a" {
		t.Fatalf("papers=%+v", papers)
	}
	if papers[0].DOI == nil || *papers[0].DOI != "10.1/a" {
		t.Fatalf("doi=%v", papers[0].DOI)
	}

	sources, err := reader.ListSources(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 1 || sources[0].Collection != "rag" {
		t.Fatalf("sources=%+v", sources)
	}
}
