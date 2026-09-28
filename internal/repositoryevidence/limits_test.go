package repositoryevidence

import "testing"

// Local smoke #35 (root 1848): design workers auditing the executive declined to name a defect
// because the declarations it turned on were outside their 48-line windows. The diet is pinned so
// a change to it is deliberate, and the window reads a function body, not just its signature.
func TestTheEvidenceDietSeesAFunctionNotJustItsSignature(t *testing.T) {
	limits := DefaultLimits()
	if limits.MaxFiles != 12 || limits.MaxRanges != 24 || limits.MaxBytes != 192*1024 || limits.MaxSearches != 16 {
		t.Fatalf("DefaultLimits = %+v", limits)
	}
	if 2*DefaultWindow+1 < 80 {
		t.Fatalf("a %d-line excerpt window cannot hold a typical function body", 2*DefaultWindow+1)
	}
	if limits.MaxLines < 2*DefaultWindow+1 {
		t.Fatalf("MaxLines %d would cut a single %d-line window", limits.MaxLines, 2*DefaultWindow+1)
	}
}
