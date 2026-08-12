package corpussemantic

import (
	"fmt"
	"math/rand"
	"testing"
	"time"
)

func TestAverageLinkClusterPerformanceAtRealisticScale(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping perf test in -short mode")
	}
	n := 4000
	dim := 768
	rng := rand.New(rand.NewSource(42))
	ids := make([]string, n)
	vectors := make([][]float32, n)
	for i := 0; i < n; i++ {
		ids[i] = fmt.Sprintf("work-%05d", i)
		v := make([]float32, dim)
		for d := 0; d < dim; d++ {
			v[d] = float32(rng.NormFloat64())
		}
		vectors[i] = v
	}
	start := time.Now()
	clusters := AverageLinkCluster(ids, vectors, 0.5)
	elapsed := time.Since(start)
	t.Logf("n=%d dim=%d -> %d clusters in %s", n, dim, len(clusters), elapsed)
	if elapsed > 2*time.Minute {
		t.Fatalf("clustering took too long: %s (n=%d)", elapsed, n)
	}
}
