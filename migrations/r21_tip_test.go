package migrations_test

import (
	"testing"

	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
	rootmigrations "github.com/Mireuz13/explorarte-organization/migrations"
)

// TestMigrationTipAndContiguity preserves both branch contracts after the
// rebase of the security branch onto main.
//
// Before the rebase the two branches each defined their own 000041, and this
// worktree carried a deliberate hole at that version while its own migration
// sat at 000042. Main now supplies 000041
// (harden_rag_knowledge_version_immutability), so the hole is filled and the
// sequence is contiguous again. Both facts are asserted together on purpose:
// contiguity is Worker A's invariant, and the identity of 000042/000043 is
// this branch's.
func TestMigrationTipAndContiguity(t *testing.T) {
	loaded, err := platformmigrations.Load(rootmigrations.Files)
	if err != nil {
		t.Fatal(err)
	}

	const wantCount = 43
	if len(loaded) != wantCount {
		t.Fatalf("migration count=%d want %d", len(loaded), wantCount)
	}
	for index, migration := range loaded {
		want := int64(index + 1)
		if migration.Version != want {
			t.Fatalf("migration[%d].version=%d want %d (the sequence must stay contiguous)", index, migration.Version, want)
		}
	}

	expected := map[int64]string{
		41: "harden_rag_knowledge_version_immutability",
		42: "add_agent_message_authorization_and_hardening",
		43: "restrict_agent_message_type",
	}
	byVersion := make(map[int64]string, len(loaded))
	for _, migration := range loaded {
		byVersion[migration.Version] = migration.Name
	}
	for version, name := range expected {
		if byVersion[version] != name {
			t.Fatalf("migration %06d name=%q want %q", version, byVersion[version], name)
		}
	}

	if tip := loaded[len(loaded)-1]; tip.Version != 43 {
		t.Fatalf("migration tip=%06d want 000043", tip.Version)
	}
}
