package corpuscluster

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

type clusterUnionFind struct {
	parent map[string]string
}

func newClusterUnionFind() *clusterUnionFind {
	return &clusterUnionFind{parent: make(map[string]string)}
}

func (u *clusterUnionFind) find(x string) string {
	if _, ok := u.parent[x]; !ok {
		u.parent[x] = x
	}
	root := x
	for u.parent[root] != root {
		root = u.parent[root]
	}
	for u.parent[x] != root {
		u.parent[x], x = root, u.parent[x]
	}
	return root
}

func (u *clusterUnionFind) union(a, b string) {
	ra, rb := u.find(a), u.find(b)
	if ra != rb {
		u.parent[ra] = rb
	}
}

// clusterIDOf derives a stable cluster_id from the sorted set of member
// WorkIDs -- re-running BuildClusters on unchanged input always yields
// the same ID for the same group (owner decision, section 30:
// reproducibility requires a stable cluster_id a curation record can
// reference across runs).
func clusterIDOf(sortedMemberIDs []string) string {
	digest := sha256.Sum256([]byte(strings.Join(sortedMemberIDs, "\x00")))
	return "cluster-" + hex.EncodeToString(digest[:])[:16]
}
