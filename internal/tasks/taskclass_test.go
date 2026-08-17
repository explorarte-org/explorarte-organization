package tasks

import "testing"

func TestValidTaskClass(t *testing.T) {
	valid := []string{
		"general.work", "research.corpus_curate",
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
		// TaskClassLegacyUnspecified is syntactically well-formed but is
		// EXCLUSIVELY the one-time historical migration marker -- never a
		// value any caller may assign to a new task (independent review
		// finding: this used to be accepted as "valid" here).
		TaskClassLegacyUnspecified,
	}
	for _, s := range invalid {
		if ValidTaskClass(s) {
			t.Errorf("ValidTaskClass(%q) = true, want false", s)
		}
	}
}

// TestValidateCreateRequest_LegacyUnspecifiedRejected is the independent
// review's required proof: a NEW task explicitly requesting
// TaskClassLegacyUnspecified is rejected outright, never silently
// accepted as if it meant something for a task that isn't historical.
func TestValidateCreateRequest_LegacyUnspecifiedRejected(t *testing.T) {
	request := CreateRequest{
		AssignedRoleID: "ingenieria_ia/qa", IdempotencyKey: "k", Title: "t", Instructions: "i",
		TaskClass: TaskClassLegacyUnspecified, MaxAttempts: 5,
	}
	if err := ValidateCreateRequest(request); err == nil {
		t.Fatal("expected ValidateCreateRequest to reject an explicit legacy.unspecified TaskClass")
	}
}

// TestValidateCreateRequest_OmittedTaskClassIsAllowedThroughValidation
// proves ValidateCreateRequest itself does not reject an omitted
// TaskClass (Service.CreateTask is what defaults it to
// TaskClassGeneralWork, afterward, before persistence).
func TestValidateCreateRequest_OmittedTaskClassIsAllowedThroughValidation(t *testing.T) {
	request := CreateRequest{AssignedRoleID: "ingenieria_ia/qa", IdempotencyKey: "k", Title: "t", Instructions: "i", MaxAttempts: 5}
	if err := ValidateCreateRequest(request); err != nil {
		t.Fatalf("an omitted TaskClass must pass validation (defaulted later): %v", err)
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
