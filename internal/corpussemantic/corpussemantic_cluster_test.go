package corpussemantic

import "testing"

func unitVec(dims int, active int, val float32) []float32 {
	v := make([]float32, dims)
	v[active] = val
	return v
}

func TestAverageLinkClusterGroupsNearIdenticalVectors(t *testing.T) {
	ids := []string{"a", "b", "c", "d"}
	vectors := [][]float32{
		{1, 0.1, 0}, // a, b close
		{0.95, 0.15, 0},
		{0, 0, 1}, // c, d close, unrelated to a/b
		{0.02, 0, 0.98},
	}
	clusters := AverageLinkCluster(ids, vectors, 0.8)
	if len(clusters) != 2 {
		t.Fatalf("expected 2 clusters, got %d: %+v", len(clusters), clusters)
	}
	for _, c := range clusters {
		if len(c.WorkIDs) != 2 {
			t.Fatalf("expected each cluster to have 2 members, got %+v", c)
		}
	}
}

func TestAverageLinkClusterDoesNotChainThroughBridge(t *testing.T) {
	// a-b similar, b-c similar, but a-c NOT similar -- average-link
	// should refuse to merge all three into one cluster the way
	// single-link chaining would, unless the true average across the
	// whole group still clears threshold.
	ids := []string{"a", "b", "c"}
	vectors := [][]float32{
		{1, 0, 0},
		{0.6, 0.6, 0.6}, // moderately similar to both a and c
		{0, 0, 1},
	}
	clusters := AverageLinkCluster(ids, vectors, 0.85)
	// a and c are nearly orthogonal (~0 similarity) -- a high threshold
	// should keep them apart even though b sits "between" them.
	var aCluster, cCluster string
	for _, c := range clusters {
		for _, id := range c.WorkIDs {
			if id == "a" {
				aCluster = c.ID
			}
			if id == "c" {
				cCluster = c.ID
			}
		}
	}
	if aCluster == cCluster {
		t.Fatal("expected a and c to stay in different clusters at a high threshold despite b bridging them")
	}
}

func TestAverageLinkClusterAllSingletonsAtHighThreshold(t *testing.T) {
	ids := []string{"a", "b", "c"}
	vectors := [][]float32{
		{1, 0, 0}, {0, 1, 0}, {0, 0, 1}, // mutually orthogonal
	}
	clusters := AverageLinkCluster(ids, vectors, 0.99)
	if len(clusters) != 3 {
		t.Fatalf("expected 3 singletons for orthogonal vectors, got %d: %+v", len(clusters), clusters)
	}
}

func TestIntraClusterSimilarityOfSingletonIsPerfect(t *testing.T) {
	mean, min := intraClusterSimilarity([]int{0}, [][]float64{{1}})
	if mean != 1.0 || min != 1.0 {
		t.Fatalf("singleton mean/min = %v/%v, want 1.0/1.0", mean, min)
	}
}

func TestClusterIDOfIsDeterministic(t *testing.T) {
	id1 := clusterIDOf([]string{"a", "b"})
	id2 := clusterIDOf([]string{"a", "b"})
	if id1 != id2 {
		t.Fatalf("expected deterministic ID, got %s vs %s", id1, id2)
	}
	id3 := clusterIDOf([]string{"a", "c"})
	if id1 == id3 {
		t.Fatal("expected different membership to produce different ID")
	}
}
