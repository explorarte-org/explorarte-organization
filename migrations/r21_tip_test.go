package migrations_test

import (
	"testing"

	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
)

func TestMigrationTipIs19AndContiguous(t *testing.T) {
	loaded, err := platformmigrations.Load(rootmigrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 19 {
		t.Fatalf("migration count=%d want 19", len(loaded))
	}
	for index, migration := range loaded {
		want := int64(index + 1)
		if migration.Version != want {
			t.Fatalf("migration[%d].version=%d want %d", index, migration.Version, want)
		}
	}
	if loaded[len(loaded)-1].Name != "enforce_rag_document_version_namespace_match" {
		t.Fatalf("migration 19 name=%q", loaded[len(loaded)-1].Name)
	}
}
