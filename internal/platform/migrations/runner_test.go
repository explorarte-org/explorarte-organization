package migrations

import (
	"testing"
	"testing/fstest"
)

func TestLoadOrdersAndChecksumsMigrations(t *testing.T) {
	source := fstest.MapFS{
		"000002_second.up.sql":   {Data: []byte("CREATE TABLE second(id bigint);")},
		"000002_second.down.sql": {Data: []byte("DROP TABLE second;")},
		"000001_first.up.sql":    {Data: []byte("CREATE TABLE first(id bigint);")},
		"000001_first.down.sql":  {Data: []byte("DROP TABLE first;")},
		"README.md":              {Data: []byte("ignored")},
	}
	loaded, err := Load(source)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(loaded) != 2 || loaded[0].Version != 1 || loaded[1].Version != 2 {
		t.Fatalf("unexpected migration order: %+v", loaded)
	}
	if loaded[0].Checksum == "" || loaded[0].Checksum == loaded[1].Checksum {
		t.Fatalf("invalid checksums: %+v", loaded)
	}
}

func TestLoadRejectsIncompletePair(t *testing.T) {
	_, err := Load(fstest.MapFS{"000001_first.up.sql": {Data: []byte("SELECT 1;")}})
	if err == nil {
		t.Fatal("Load() error = nil, want incomplete pair error")
	}
}

func TestLoadRejectsConflictingNames(t *testing.T) {
	_, err := Load(fstest.MapFS{
		"000001_first.up.sql":    {Data: []byte("SELECT 1;")},
		"000001_second.down.sql": {Data: []byte("SELECT 1;")},
	})
	if err == nil {
		t.Fatal("Load() error = nil, want conflicting name error")
	}
}

func TestLoadRejectsMalformedSQLFilename(t *testing.T) {
	_, err := Load(fstest.MapFS{"migration.sql": {Data: []byte("SELECT 1;")}})
	if err == nil {
		t.Fatal("Load() error = nil, want malformed filename error")
	}
}
