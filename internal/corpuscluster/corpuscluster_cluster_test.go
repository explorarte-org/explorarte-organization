package corpuscluster

import "testing"

func clusterOf(t *testing.T, clusters []Cluster, workID string) Cluster {
	t.Helper()
	for _, c := range clusters {
		for _, id := range c.WorkIDs {
			if id == workID {
				return c
			}
		}
	}
	t.Fatalf("work %q not found in any cluster", workID)
	return Cluster{}
}

func TestBuildClustersGroupsSimilarTitles(t *testing.T) {
	works := []WorkInput{
		{WorkID: "w1", Title: "Hierarchical Retrieval Augmented Generation for Long Documents"},
		{WorkID: "w2", Title: "Hierarchical Retrieval Augmented Generation for Multi-hop Question Answering"},
		{WorkID: "w3", Title: "Speaker Diarization for Automatic Speech Recognition Systems"},
	}
	clusters := BuildClusters(works, 0.2)
	c1 := clusterOf(t, clusters, "w1")
	c2 := clusterOf(t, clusters, "w2")
	if c1.ID != c2.ID {
		t.Fatalf("expected w1 and w2 (both hierarchical RAG) to cluster together, got %s vs %s", c1.ID, c2.ID)
	}
	c3 := clusterOf(t, clusters, "w3")
	if c3.ID == c1.ID {
		t.Fatal("expected w3 (unrelated ASR topic) to be in a different cluster")
	}
}

func TestBuildClustersProducesSingletonForUniqueWork(t *testing.T) {
	works := []WorkInput{
		{WorkID: "w1", Title: "A Completely Unrelated Topic About Quantum Chemistry Simulations"},
		{WorkID: "w2", Title: "Prolog Based Symbolic Reasoning For Multi Agent Systems"},
	}
	clusters := BuildClusters(works, 0.5)
	if len(clusters) != 2 {
		t.Fatalf("expected 2 singleton clusters for unrelated titles, got %d: %+v", len(clusters), clusters)
	}
}

func TestBuildClustersIsDeterministicAcrossReruns(t *testing.T) {
	works := []WorkInput{
		{WorkID: "w1", Title: "Context Compression Techniques For Long Context Language Models"},
		{WorkID: "w2", Title: "Token Efficient Context Compression For Long Context Transformers"},
		{WorkID: "w3", Title: "Graph RAG Construction From Unstructured Documents"},
	}
	first := BuildClusters(works, 0.25)
	second := BuildClusters(works, 0.25)
	if len(first) != len(second) {
		t.Fatalf("non-deterministic cluster count: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i].ID != second[i].ID {
			t.Fatalf("non-deterministic cluster ID at index %d: %s vs %s", i, first[i].ID, second[i].ID)
		}
	}
}

func TestClusterIDStableForSameMembership(t *testing.T) {
	id1 := clusterIDOf([]string{"a", "b", "c"})
	id2 := clusterIDOf([]string{"a", "b", "c"})
	if id1 != id2 {
		t.Fatalf("expected stable ID for identical membership, got %s vs %s", id1, id2)
	}
	id3 := clusterIDOf([]string{"a", "b"})
	if id1 == id3 {
		t.Fatal("expected different membership to produce different ID")
	}
}
