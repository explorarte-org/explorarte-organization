package corpussemantic

import "sort"

// SemanticCluster is the final, threshold-cut output of average-link
// agglomerative clustering. Unlike internal/corpuscluster's single-link
// union-find (which chains transitively through any shared bridge and
// produced a 3,021-member mega-cluster on this corpus), average-link
// only merges two clusters when the MEAN pairwise similarity across
// every cross-pair meets threshold -- a single outlier connection can no
// longer drag unrelated Works into the same group.
type SemanticCluster struct {
	ID                 string
	WorkIDs            []string
	MeanSimilarity     float64 // mean pairwise cosine similarity among all members
	MinSimilarity      float64 // minimum pairwise cosine similarity among all members
	CentroidSimilarity float64 // mean cosine similarity of each member to the cluster's own centroid vector
}

func dot(a, b []float32) float64 {
	var sum float64
	for i := range a {
		sum += float64(a[i]) * float64(b[i])
	}
	return sum
}

func norm2(v []float32) float64 {
	return dot(v, v)
}

// cosineSimilarityMatrix computes every pairwise cosine similarity once
// (embeddings are assumed pre-normalized in direction but not
// necessarily unit-length, so this divides by both norms explicitly
// rather than assuming a unit sphere).
func cosineSimilarityMatrix(vectors [][]float32) [][]float64 {
	n := len(vectors)
	norms := make([]float64, n)
	for i, v := range vectors {
		norms[i] = sqrtApprox(norm2(v))
	}
	matrix := make([][]float64, n)
	for i := range matrix {
		matrix[i] = make([]float64, n)
	}
	for i := 0; i < n; i++ {
		matrix[i][i] = 1.0
		for j := i + 1; j < n; j++ {
			var sim float64
			if norms[i] > 0 && norms[j] > 0 {
				sim = dot(vectors[i], vectors[j]) / (norms[i] * norms[j])
			}
			matrix[i][j] = sim
			matrix[j][i] = sim
		}
	}
	return matrix
}

func sqrtApprox(x float64) float64 {
	if x <= 0 {
		return 0
	}
	z := x
	for i := 0; i < 30; i++ {
		z -= (z*z - x) / (2 * z)
	}
	return z
}

// AverageLinkCluster runs UPGMA (average-link agglomerative clustering)
// over workIDs/vectors using the Lance-Williams update formula for
// average linkage, stopping merges once the best available similarity
// drops below simThreshold (equivalently, distance = 1-similarity rises
// above 1-simThreshold). O(n^2) time/space -- tractable at this
// corpus's scale (~4,000 Works), not appropriate unmodified at 10x this
// size.
func AverageLinkCluster(workIDs []string, vectors [][]float32, simThreshold float64) []SemanticCluster {
	n := len(workIDs)
	if n == 0 {
		return nil
	}
	simMatrix := cosineSimilarityMatrix(vectors)
	distThreshold := 1 - simThreshold

	// dist[i][j]: current linkage distance between active clusters i, j
	// (only meaningful where active[i] && active[j] && i != j).
	dist := make([][]float64, n)
	for i := range dist {
		dist[i] = make([]float64, n)
		for j := range dist[i] {
			dist[i][j] = 1 - simMatrix[i][j]
		}
	}
	members := make([][]int, n) // cluster index -> original point indices
	for i := range members {
		members[i] = []int{i}
	}
	active := make([]bool, n)
	for i := range active {
		active[i] = true
	}
	size := make([]int, n)
	for i := range size {
		size[i] = 1
	}

	// nearestOf[i]/nearestDist[i]: i's closest active partner and that
	// distance -- turns "find the global best pair" into an O(n) scan of
	// this array instead of an O(n^2) rescan of the whole matrix every
	// merge (naive full-rescan would be O(n^3) total across n-1 merges;
	// this brings it to roughly O(n^2), tractable at n~4,000).
	nearestOf := make([]int, n)
	nearestDist := make([]float64, n)
	recomputeNearest := func(i int) {
		best, bestD := -1, 2.0
		for k := 0; k < n; k++ {
			if !active[k] || k == i {
				continue
			}
			if dist[i][k] < bestD {
				bestD = dist[i][k]
				best = k
			}
		}
		nearestOf[i] = best
		nearestDist[i] = bestD
	}
	for i := 0; i < n; i++ {
		recomputeNearest(i)
	}

	activeCount := n
	for activeCount > 1 {
		bestI, bestDist := -1, 2.0
		for i := 0; i < n; i++ {
			if active[i] && nearestOf[i] != -1 && nearestDist[i] < bestDist {
				bestDist = nearestDist[i]
				bestI = i
			}
		}
		if bestI == -1 || bestDist > distThreshold {
			break // no pair left worth merging under threshold
		}
		bestJ := nearestOf[bestI]

		// Lance-Williams update for average linkage (UPGMA): merge j into i.
		si, sj := float64(size[bestI]), float64(size[bestJ])
		for k := 0; k < n; k++ {
			if !active[k] || k == bestI || k == bestJ {
				continue
			}
			updated := (si*dist[bestI][k] + sj*dist[bestJ][k]) / (si + sj)
			dist[bestI][k] = updated
			dist[k][bestI] = updated
		}
		members[bestI] = append(members[bestI], members[bestJ]...)
		size[bestI] = int(si + sj)
		active[bestJ] = false
		activeCount--

		// bestI's own nearest neighbor may have changed (its distances to
		// everyone just got updated); anyone whose nearest was bestI or
		// bestJ needs recomputing too. Everyone else can cheaply check
		// whether the newly-updated dist[bestI][k] beats what they had.
		recomputeNearest(bestI)
		for k := 0; k < n; k++ {
			if !active[k] || k == bestI {
				continue
			}
			if nearestOf[k] == bestI || nearestOf[k] == bestJ {
				recomputeNearest(k)
			} else if dist[k][bestI] < nearestDist[k] {
				nearestOf[k] = bestI
				nearestDist[k] = dist[k][bestI]
			}
		}
	}

	var clusters []SemanticCluster
	for i := 0; i < n; i++ {
		if !active[i] {
			continue
		}
		idxs := members[i]
		ids := make([]string, len(idxs))
		for k, idx := range idxs {
			ids[k] = workIDs[idx]
		}
		sort.Strings(ids)
		mean, min := intraClusterSimilarity(idxs, simMatrix)
		centroidSim := centroidSimilarity(idxs, vectors)
		clusters = append(clusters, SemanticCluster{
			ID: clusterIDOf(ids), WorkIDs: ids,
			MeanSimilarity: mean, MinSimilarity: min, CentroidSimilarity: centroidSim,
		})
	}
	sort.Slice(clusters, func(i, j int) bool { return clusters[i].ID < clusters[j].ID })
	return clusters
}

func intraClusterSimilarity(idxs []int, simMatrix [][]float64) (mean, min float64) {
	if len(idxs) < 2 {
		return 1.0, 1.0 // a singleton is trivially "perfectly coherent" with itself
	}
	min = 2.0
	var sum float64
	var count int
	for a := 0; a < len(idxs); a++ {
		for b := a + 1; b < len(idxs); b++ {
			s := simMatrix[idxs[a]][idxs[b]]
			sum += s
			count++
			if s < min {
				min = s
			}
		}
	}
	return sum / float64(count), min
}

func centroidSimilarity(idxs []int, vectors [][]float32) float64 {
	if len(idxs) == 0 {
		return 0
	}
	dim := len(vectors[idxs[0]])
	centroid := make([]float64, dim)
	for _, idx := range idxs {
		for d := 0; d < dim; d++ {
			centroid[d] += float64(vectors[idx][d])
		}
	}
	for d := range centroid {
		centroid[d] /= float64(len(idxs))
	}
	centroidNorm := 0.0
	for _, v := range centroid {
		centroidNorm += v * v
	}
	centroidNorm = sqrtApprox(centroidNorm)
	if centroidNorm == 0 {
		return 0
	}
	var sum float64
	for _, idx := range idxs {
		var dotv, vNorm float64
		for d := 0; d < dim; d++ {
			dotv += float64(vectors[idx][d]) * centroid[d]
			vNorm += float64(vectors[idx][d]) * float64(vectors[idx][d])
		}
		vNorm = sqrtApprox(vNorm)
		if vNorm > 0 {
			sum += dotv / (vNorm * centroidNorm)
		}
	}
	return sum / float64(len(idxs))
}

func clusterIDOf(sortedWorkIDs []string) string {
	return hashStrings(sortedWorkIDs)
}
