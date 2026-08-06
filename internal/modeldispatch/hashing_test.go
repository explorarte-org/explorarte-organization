package modeldispatch

import (
	"testing"
	"time"
)

func mustTime(value string) time.Time {
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(err)
	}
	return t
}

func TestPrincipalRequestHashDeterministicAndSensitive(t *testing.T) {
	base := func() (string, error) {
		return PrincipalRequestHash("explorarte", "oracle-01/model-runtime-01", "ingenieria_ia/code-runner", PrincipalLocalProcess, "empresa/human")
	}
	first, err := base()
	if err != nil {
		t.Fatal(err)
	}
	second, err := base()
	if err != nil || first != second || len(first) != 64 {
		t.Fatalf("unstable hash: %s %s %v", first, second, err)
	}
	changedKey, err := PrincipalRequestHash("explorarte", "oracle-02/model-runtime-01", "ingenieria_ia/code-runner", PrincipalLocalProcess, "empresa/human")
	if err != nil || changedKey == first {
		t.Fatalf("principal key was not pinned: %s", changedKey)
	}
	changedRole, err := PrincipalRequestHash("explorarte", "oracle-01/model-runtime-01", "ingenieria_ia/frontend", PrincipalLocalProcess, "empresa/human")
	if err != nil || changedRole == first {
		t.Fatalf("dispatch actor role was not pinned: %s", changedRole)
	}
	changedKind, err := PrincipalRequestHash("explorarte", "oracle-01/model-runtime-01", "ingenieria_ia/code-runner", PrincipalCellProcess, "empresa/human")
	if err != nil || changedKind == first {
		t.Fatalf("principal kind was not pinned: %s", changedKind)
	}
}

func TestAssignmentScopeHashChangesWithScope(t *testing.T) {
	validFrom := mustTime("2026-01-01T00:00:00Z")
	validUntil := mustTime("2026-01-01T01:00:00Z")
	base := func(revision, taskID, attemptID, principalID int64, subject, actor string, max int) (string, error) {
		return AssignmentScopeHash("explorarte", revision, taskID, attemptID, subject, actor, principalID, max, validFrom, validUntil)
	}
	first, err := base(7, 3, 4, 21, "ingenieria_ia/code-runner", "ingenieria_ia/code-runner", 5)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		hash string
	}{}
	add := func(name, hash string, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		cases = append(cases, struct {
			name string
			hash string
		}{name, hash})
	}
	revisionHash, revisionErr := base(8, 3, 4, 21, "ingenieria_ia/code-runner", "ingenieria_ia/code-runner", 5)
	add("revision", revisionHash, revisionErr)
	taskHash, taskErr := base(7, 30, 4, 21, "ingenieria_ia/code-runner", "ingenieria_ia/code-runner", 5)
	add("task", taskHash, taskErr)
	attemptHash, attemptErr := base(7, 3, 40, 21, "ingenieria_ia/code-runner", "ingenieria_ia/code-runner", 5)
	add("attempt", attemptHash, attemptErr)
	subjectHash, subjectErr := base(7, 3, 4, 21, "ingenieria_ia/frontend", "ingenieria_ia/code-runner", 5)
	add("subject", subjectHash, subjectErr)
	actorHash, actorErr := base(7, 3, 4, 21, "ingenieria_ia/code-runner", "ingenieria_ia/frontend", 5)
	add("actor", actorHash, actorErr)
	principalHash, principalErr := base(7, 3, 4, 99, "ingenieria_ia/code-runner", "ingenieria_ia/code-runner", 5)
	add("principal", principalHash, principalErr)
	maxHash, maxErr := base(7, 3, 4, 21, "ingenieria_ia/code-runner", "ingenieria_ia/code-runner", 6)
	add("max", maxHash, maxErr)
	for _, c := range cases {
		if c.hash == first {
			t.Fatalf("case %q did not change the scope hash", c.name)
		}
	}
	valid, err := AssignmentScopeHash("explorarte", 7, 3, 4, "ingenieria_ia/code-runner", "ingenieria_ia/code-runner", 21, 5, validFrom, validUntil.Add(time.Minute))
	if err != nil || valid == first {
		t.Fatalf("valid_until was not pinned: %s", valid)
	}
}

func TestUsageHashDeterministicAndSensitive(t *testing.T) {
	usedAt := mustTime("2026-01-01T00:00:00Z")
	first, err := UsageHash(31, "assignment-hash", 100, 200, 21, usedAt)
	if err != nil {
		t.Fatal(err)
	}
	second, err := UsageHash(31, "assignment-hash", 100, 200, 21, usedAt)
	if err != nil || first != second {
		t.Fatalf("unstable usage hash: %v", err)
	}
	changed, err := UsageHash(31, "assignment-hash", 101, 200, 21, usedAt)
	if err != nil || changed == first {
		t.Fatalf("invocation ID was not pinned in usage hash: %s", changed)
	}
}
