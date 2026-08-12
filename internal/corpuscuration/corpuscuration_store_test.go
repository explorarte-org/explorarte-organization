package corpuscuration

import (
	"path/filepath"
	"testing"
)

func TestInputHashOfIsStableForSameInput(t *testing.T) {
	h1 := InputHashOf("w1", "c1", "Title", []string{"w1", "w2"})
	h2 := InputHashOf("w1", "c1", "Title", []string{"w2", "w1"}) // different order, same set
	if h1 != h2 {
		t.Fatalf("expected order-independent hash, got %s vs %s", h1, h2)
	}
}

func TestInputHashOfChangesWithClusterMembership(t *testing.T) {
	h1 := InputHashOf("w1", "c1", "Title", []string{"w1", "w2"})
	h2 := InputHashOf("w1", "c1", "Title", []string{"w1", "w2", "w3"})
	if h1 == h2 {
		t.Fatal("expected hash to change when cluster membership changes")
	}
}

func TestStoreResumeAfterFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "curation.jsonl")
	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	rec := WorkCuration{
		WorkID: "w1", ClusterID: "c1", Tier: TierP0Core,
		InputHash: "hash-a", CurationSchemaVersion: CurrentVersions.CurationSchema,
		TaxonomyVersion: CurrentVersions.Taxonomy, ClusterAlgorithmVersion: CurrentVersions.ClusterAlgorithm,
		RubricVersion: CurrentVersions.Rubric,
	}
	store.Put(rec)
	if err := store.Flush(); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	got, valid := reopened.Valid("w1", "c1", "hash-a")
	if !valid || got.Tier != TierP0Core {
		t.Fatalf("expected a valid resumed record, got valid=%v rec=%+v", valid, got)
	}
}

func TestStoreInvalidatesOnInputHashChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "curation.jsonl")
	store, _ := OpenStore(path)
	store.Put(WorkCuration{
		WorkID: "w1", ClusterID: "c1", Tier: TierP0Core, InputHash: "old-hash",
		CurationSchemaVersion: CurrentVersions.CurationSchema, TaxonomyVersion: CurrentVersions.Taxonomy,
		ClusterAlgorithmVersion: CurrentVersions.ClusterAlgorithm, RubricVersion: CurrentVersions.Rubric,
	})
	_, valid := store.Valid("w1", "c1", "new-hash")
	if valid {
		t.Fatal("expected invalidation when InputHash no longer matches (Work or cluster changed)")
	}
}

func TestStoreInvalidatesOnTaxonomyVersionBump(t *testing.T) {
	path := filepath.Join(t.TempDir(), "curation.jsonl")
	store, _ := OpenStore(path)
	store.Put(WorkCuration{
		WorkID: "w1", ClusterID: "c1", Tier: TierP0Core, InputHash: "hash-a",
		CurationSchemaVersion: CurrentVersions.CurationSchema, TaxonomyVersion: "engineering-v1", // stale
		ClusterAlgorithmVersion: CurrentVersions.ClusterAlgorithm, RubricVersion: CurrentVersions.Rubric,
	})
	_, valid := store.Valid("w1", "c1", "hash-a")
	if valid {
		t.Fatal("expected invalidation when the pinned taxonomy version changed")
	}
}

func TestStoreRerunIsIdempotentNotDuplicating(t *testing.T) {
	path := filepath.Join(t.TempDir(), "curation.jsonl")
	store, _ := OpenStore(path)
	rec := WorkCuration{WorkID: "w1", ClusterID: "c1", Tier: TierP0Core, InputHash: "hash-a"}
	store.Put(rec)
	store.Flush()

	reopened, _ := OpenStore(path)
	reopened.Put(WorkCuration{WorkID: "w1", ClusterID: "c1", Tier: TierP1Strong, InputHash: "hash-a"})
	reopened.Flush()

	final, _ := OpenStore(path)
	all := final.All()
	if len(all) != 1 {
		t.Fatalf("expected exactly 1 record after rerun, got %d", len(all))
	}
	if all[0].Tier != TierP1Strong {
		t.Fatalf("expected the rerun's value to win, got %s", all[0].Tier)
	}
}
