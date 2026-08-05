package modelruntime

import (
	"encoding/json"
	"testing"
	"time"
)

func TestCanonicalizeRawJSON(t *testing.T) {
	got, err := CanonicalizeRawJSON(json.RawMessage(`{"z":1,"a":{"b":2}}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"a":{"b":2},"z":1}` {
		t.Fatalf("got %s", got)
	}
	if _, err = CanonicalizeRawJSON(json.RawMessage(`{} {}`)); err == nil {
		t.Fatal("expected trailing JSON rejection")
	}
}
func TestActionDigestDeterministic(t *testing.T) {
	inv := Invocation{ID: 1, OrganizationID: "explorarte", OrganizationRevisionID: 2, TaskID: 3, AttemptID: 4, DispatchActorRoleID: "x/y", SubjectRoleID: "x/y", ContextSnapshotID: 5, ModelProfileID: "worker-default", ModelProfileVersionID: 6, ProviderID: "test.fake", ProviderModelID: "v1", RequiredCapabilities: []ModelCapability{"b", "a"}, OutputMode: OutputJSON, OutputSchema: json.RawMessage(`{"type":"object"}`), MaxOutputTokens: 100, ThinkingMode: ThinkingDisabled, Deadline: time.Unix(1000, 0).UTC()}
	a, err := ActionDigest(inv)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ActionDigest(inv)
	if err != nil || a != b || len(a) != 64 {
		t.Fatalf("unstable digest %s %s %v", a, b, err)
	}
}

func TestCanonicalizeRawJSONPreservesLargeValidNumber(t *testing.T) {
	got, err := CanonicalizeRawJSON(json.RawMessage(`{"n":1e1000}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"n":1e1000}` {
		t.Fatalf("unexpected canonical number: %s", got)
	}
}
