package migrations_test

import (
	"io/fs"
	"strconv"
	"testing"
	"testing/fstest"

	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
)

// A migration rehearsal (up, down, up) runs on a database of its own (testdbguard.FreshDatabase)
// holding the migrations through its own version and no later one. Rehearsed on the shared
// database at the compiled tip, a rehearsal broke as soon as a later migration depended on the
// tables it drops ("cannot drop table ... because other objects depend on it"), expected a tip
// that later migrations had moved, and left the schema in a state the next package tripped on.

// migrationsThrough is the embedded migration set cut at version: what the compiled set was when
// that migration was the newest.
func migrationsThrough(t *testing.T, version int64) fs.FS {
	t.Helper()
	cut := fstest.MapFS{}
	entries, err := fs.ReadDir(rootmigrations.Files, ".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || len(entry.Name()) < 6 {
			continue
		}
		v, err := strconv.ParseInt(entry.Name()[:6], 10, 64)
		if err != nil || v > version {
			continue
		}
		body, err := fs.ReadFile(rootmigrations.Files, entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		cut[entry.Name()] = &fstest.MapFile{Data: body}
	}
	return cut
}
