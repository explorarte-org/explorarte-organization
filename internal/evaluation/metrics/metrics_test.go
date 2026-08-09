package metrics

import "testing"

func set(ids ...string) map[string]struct{} {
	m := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		m[id] = struct{}{}
	}
	return m
}

func TestRecallAt(t *testing.T) {
	ranked := []string{"a", "b", "c", "d", "e"}
	relevant := set("c", "z")
	if got := RecallAt(ranked, relevant, 3); got != 0.5 {
		t.Fatalf("recall@3=%v want 0.5", got)
	}
	if got := RecallAt(ranked, relevant, 1); got != 0 {
		t.Fatalf("recall@1=%v want 0", got)
	}
	if got := RecallAt(ranked, set(), 3); got != 0 {
		t.Fatalf("recall with no relevant ids=%v want 0", got)
	}
}

func TestNDCGAt(t *testing.T) {
	relevant := set("a")
	perfect := NDCGAt([]string{"a", "b", "c"}, relevant, 3)
	if perfect != 1 {
		t.Fatalf("perfect ndcg=%v want 1", perfect)
	}
	worse := NDCGAt([]string{"b", "a", "c"}, relevant, 3)
	if worse >= perfect {
		t.Fatalf("ndcg with relevant item ranked lower should be worse: worse=%v perfect=%v", worse, perfect)
	}
	absent := NDCGAt([]string{"b", "c", "d"}, relevant, 3)
	if absent != 0 {
		t.Fatalf("ndcg with no relevant hit=%v want 0", absent)
	}
}

func TestReciprocalRank(t *testing.T) {
	relevant := set("c")
	if got := ReciprocalRank([]string{"a", "b", "c"}, relevant); got != 1.0/3 {
		t.Fatalf("mrr=%v want 1/3", got)
	}
	if got := ReciprocalRank([]string{"a", "b"}, relevant); got != 0 {
		t.Fatalf("mrr with no hit=%v want 0", got)
	}
}

func TestIdentifierPrecisionAt(t *testing.T) {
	relevant := set("error-20")
	// "error 20" correctly retrieved, "error 2000" is a false positive
	// mixed into the top-2 — precision@2 must reflect exactly one hit.
	ranked := []string{"error-20", "error-2000"}
	if got := IdentifierPrecisionAt(ranked, relevant, 2); got != 0.5 {
		t.Fatalf("precision@2=%v want 0.5", got)
	}
}

func TestNumericFalsePositiveRate(t *testing.T) {
	forbidden := set("error-2000")
	ranked := []string{"error-20", "error-2000", "error-2000-b"}
	if got := NumericFalsePositiveRate(ranked, forbidden, 3); got != 1.0/3 {
		t.Fatalf("false positive rate=%v want 1/3", got)
	}
	if got := NumericFalsePositiveRate(ranked, set(), 3); got != 0 {
		t.Fatalf("false positive rate with nothing forbidden=%v want 0", got)
	}
}
