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
func TestInvocationRequestHashPinsEgressPolicy(t *testing.T) {
	deadline := time.Unix(2000, 0).UTC()
	command := CreateInvocationCommand{
		OrganizationID: "explorarte", TaskID: 3, AttemptID: 4,
		DispatchActorRoleID: "ingenieria_ia/code-runner", SubjectRoleID: "ingenieria_ia/code-runner",
		ContextSnapshotID: 5, Purpose: "fixture", OutputMode: OutputJSON,
		MaxOutputTokens: 100, ThinkingMode: ThinkingDisabled, Deadline: deadline,
	}
	binding := ResolvedBinding{
		Profile: Profile{ID: "worker-default"},
		Version: ProfileVersion{ID: 6, ProviderID: "test.fake", ProviderModelID: "v1"},
	}
	policyHash := SHA256Bytes([]byte("policy"))
	first, err := invocationRequestHash(command, 7, binding, []ModelCapability{"structured.output"}, []byte(`{"type":"object"}`), 17, policyHash)
	if err != nil {
		t.Fatal(err)
	}
	changedVersion, err := invocationRequestHash(command, 7, binding, []ModelCapability{"structured.output"}, []byte(`{"type":"object"}`), 18, policyHash)
	if err != nil {
		t.Fatal(err)
	}
	changedHash, err := invocationRequestHash(command, 7, binding, []ModelCapability{"structured.output"}, []byte(`{"type":"object"}`), 17, SHA256Bytes([]byte("other-policy")))
	if err != nil {
		t.Fatal(err)
	}
	if first == changedVersion || first == changedHash || changedVersion == changedHash {
		t.Fatalf("egress policy was not pinned in request hash: %s %s %s", first, changedVersion, changedHash)
	}
}

func TestActionDigestDeterministic(t *testing.T) {
	inv := Invocation{ID: 1, OrganizationID: "explorarte", OrganizationRevisionID: 2, TaskID: 3, AttemptID: 4, DispatchActorRoleID: "x/y", SubjectRoleID: "x/y", ContextSnapshotID: 5, ModelProfileID: "worker-default", ModelProfileVersionID: 6, ProviderID: "test.fake", ProviderModelID: "v1", ModelEgressPolicyVersionID: int64Pointer(17), ModelEgressPolicyHash: SHA256Bytes([]byte("policy")), RequestHash: SHA256Bytes([]byte("request")), RequiredCapabilities: []ModelCapability{"b", "a"}, OutputMode: OutputJSON, OutputSchema: json.RawMessage(`{"type":"object"}`), MaxOutputTokens: 100, ThinkingMode: ThinkingDisabled, Deadline: time.Unix(1000, 0).UTC()}
	a, err := ActionDigest(inv)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ActionDigest(inv)
	if err != nil || a != b || len(a) != 64 {
		t.Fatalf("unstable digest %s %s %v", a, b, err)
	}
}

func TestActionDigestChangesWithPinnedScope(t *testing.T) {
	base := Invocation{ID: 1, RequestHash: SHA256Bytes([]byte("request")), ModelEgressPolicyVersionID: int64Pointer(17), ModelEgressPolicyHash: SHA256Bytes([]byte("policy"))}
	first, err := ActionDigest(base)
	if err != nil {
		t.Fatal(err)
	}
	cases := []Invocation{base, base, base, base}
	cases[0].ModelEgressPolicyVersionID = int64Pointer(17)
	cases[1].ModelEgressPolicyVersionID = int64Pointer(17)
	cases[2].ModelEgressPolicyVersionID = int64Pointer(18)
	cases[3].ModelEgressPolicyVersionID = int64Pointer(17)
	cases[0].ID++
	cases[1].RequestHash = SHA256Bytes([]byte("other-request"))
	cases[3].ModelEgressPolicyHash = SHA256Bytes([]byte("other-policy"))
	for index, candidate := range cases {
		digest, digestErr := ActionDigest(candidate)
		if digestErr != nil {
			t.Fatalf("case %d: %v", index, digestErr)
		}
		if digest == first {
			t.Fatalf("case %d did not change digest", index)
		}
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
