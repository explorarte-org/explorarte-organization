package main

import "testing"

func TestProgramTargetIsExact(t *testing.T) {
	if !validProgramTargetRef("refs/heads/v2/program-context-memory-001") {
		t.Fatal("canonical program ref denied")
	}
	for _, ref := range []string{"refs/heads/main", "refs/heads/release/test", "refs/heads/production", "refs/heads/v2/another-program", "refs/heads/foo"} {
		if validProgramTargetRef(ref) {
			t.Fatalf("non-canonical ref accepted: %s", ref)
		}
	}
}
