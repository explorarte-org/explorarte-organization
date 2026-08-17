package tasks

import "testing"

func TestValidTaskClass(t *testing.T) {
	valid := []string{
		"general.work", "legacy.unspecified", "research.corpus_curate",
		"coordination.ceo_plan", "owner.goal", "a.b.c",
	}
	for _, s := range valid {
		if !ValidTaskClass(s) {
			t.Errorf("ValidTaskClass(%q) = false, want true", s)
		}
	}
	invalid := []string{
		"", "notdotted", "Has.Uppercase", "has space.here", "trailing.dot.",
		".leadingdot", "has/slash.here", "a..b",
	}
	for _, s := range invalid {
		if ValidTaskClass(s) {
			t.Errorf("ValidTaskClass(%q) = true, want false", s)
		}
	}
}

// TestHashCreateRequestLegacy_MatchesPreM13Shape proves the legacy
// recomputation is byte-identical to what HashCreateRequest always
// produced before TaskClass existed on CreateRequest: a request with
// TaskClass populated must hash, under the legacy function, to the exact
// same value it would have hashed to before TaskClass was ever added.
func TestHashCreateRequestLegacy_MatchesPreM13Shape(t *testing.T) {
	withoutTaskClass := CreateRequest{
		AssignedRoleID: "investigacion/research_worker_hourly", IdempotencyKey: "k", Title: "t", Instructions: "i",
	}
	preM13Hash, err := HashCreateRequest(withoutTaskClass)
	if err != nil {
		t.Fatal(err)
	}
	withTaskClass := withoutTaskClass
	withTaskClass.TaskClass = "research.corpus_curate"
	legacyHash, err := HashCreateRequestLegacy(withTaskClass)
	if err != nil {
		t.Fatal(err)
	}
	if legacyHash != preM13Hash {
		t.Fatalf("HashCreateRequestLegacy(with TaskClass) = %s, want the exact pre-M1.3 hash %s", legacyHash, preM13Hash)
	}
	v2Hash, err := HashCreateRequest(withTaskClass)
	if err != nil {
		t.Fatal(err)
	}
	if v2Hash == preM13Hash {
		t.Fatal("HashCreateRequest must diverge from the pre-M1.3 hash once TaskClass is non-empty, or the two hash spaces are not actually distinct")
	}
}
