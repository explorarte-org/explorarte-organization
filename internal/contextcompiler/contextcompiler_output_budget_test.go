package contextcompiler

import "testing"

// 1. 1 Work/2 Work small-cluster floor uses 4500 where applicable
func TestCorpusCurateOutputTokenBudget_SmallClusterFloorIs4500(t *testing.T) {
	for _, n := range []int{1, 2} {
		got := CorpusCurateOutputTokenBudget(n)
		if got != 4500 {
			t.Fatalf("n_works=%d: expected corrected floor 4500, got %d", n, got)
		}
	}
}

// 2. larger clusters retain previous scaling
func TestCorpusCurateOutputTokenBudget_LargerClustersRetainHistoricalScaling(t *testing.T) {
	cases := []struct {
		nWorks int
		want   int
	}{
		{3, 4200}, // 600+1200*3=4200, already above the historical floor of 3000, unaffected
		{5, 6600}, // sentinel scluster-aac0f99841969e76
		{7, 9000},
		{8, 10200}, // sentinel scluster-a16533182030ccd4
		{10, 12600},
	}
	for _, c := range cases {
		got := CorpusCurateOutputTokenBudget(c.nWorks)
		if got != c.want {
			t.Fatalf("n_works=%d: expected historical scaling %d, got %d", c.nWorks, c.want, got)
		}
	}
}

// 3. upper cap remains unchanged
func TestCorpusCurateOutputTokenBudget_UpperCapUnchanged(t *testing.T) {
	for _, n := range []int{18, 30, 100} {
		got := CorpusCurateOutputTokenBudget(n)
		if got != 16000 {
			t.Fatalf("n_works=%d: expected cap 16000, got %d", n, got)
		}
	}
}

// boundary: n_works where raw==historical floor exactly (n=2 -> 3000)
// must also get the corrected floor, not the old clamp.
func TestCorpusCurateOutputTokenBudget_ExactHistoricalFloorBoundaryCorrected(t *testing.T) {
	if got := CorpusCurateOutputTokenBudget(2); got != 4500 {
		t.Fatalf("n_works=2 (raw=3000, exactly the historical floor): expected 4500, got %d", got)
	}
	// n_works=3 (raw=4200) must NOT be affected -- confirms the boundary
	// is exact, not an off-by-one that also bumps clusters already above
	// the historical floor.
	if got := CorpusCurateOutputTokenBudget(3); got != 4200 {
		t.Fatalf("n_works=3 (raw=4200, already above historical floor): expected unchanged 4200, got %d", got)
	}
}
