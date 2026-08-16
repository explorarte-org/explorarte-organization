package modelruntime

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/modeldispatch"
)

func fixtureResolvedAssignment() modeldispatch.ResolvedAssignment {
	return modeldispatch.ResolvedAssignment{
		Assignment: modeldispatch.DispatcherAssignment{ID: 9, AssignmentHash: SHA256Bytes([]byte("assignment"))},
		Principal:  modeldispatch.ExecutionPrincipal{ID: 11, PrincipalKey: "oracle-01/model-runtime-01", DispatchActorRoleID: "ingenieria_ia/code-runner"},
	}
}

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
		SubjectRoleID:     "ingenieria_ia/code-runner",
		ContextSnapshotID: 5, Purpose: "fixture", OutputMode: OutputJSON,
		MaxOutputTokens: 100, ThinkingMode: ThinkingDisabled, Deadline: deadline,
	}
	binding := ResolvedBinding{
		Profile: Profile{ID: "worker-default"},
		Version: ProfileVersion{ID: 6, ProviderID: "test.fake", ProviderModelID: "v1"},
	}
	policyHash := SHA256Bytes([]byte("policy"))
	identityPolicyHash := SHA256Bytes([]byte("identity-policy"))
	inputDigest := SHA256Bytes([]byte("model-input"))
	assignment := fixtureResolvedAssignment()
	first, err := invocationRequestHash(command, 7, binding, []ModelCapability{"structured.output"}, []byte(`{"type":"object"}`), inputDigest, 17, policyHash, 27, identityPolicyHash, assignment)
	if err != nil {
		t.Fatal(err)
	}
	changedVersion, err := invocationRequestHash(command, 7, binding, []ModelCapability{"structured.output"}, []byte(`{"type":"object"}`), inputDigest, 18, policyHash, 27, identityPolicyHash, assignment)
	if err != nil {
		t.Fatal(err)
	}
	changedHash, err := invocationRequestHash(command, 7, binding, []ModelCapability{"structured.output"}, []byte(`{"type":"object"}`), inputDigest, 17, SHA256Bytes([]byte("other-policy")), 27, identityPolicyHash, assignment)
	if err != nil {
		t.Fatal(err)
	}
	changedAssignment := assignment
	changedAssignment.Assignment.ID = 999
	changedAssignmentHash, err := invocationRequestHash(command, 7, binding, []ModelCapability{"structured.output"}, []byte(`{"type":"object"}`), inputDigest, 17, policyHash, 27, identityPolicyHash, changedAssignment)
	if err != nil {
		t.Fatal(err)
	}
	if first == changedVersion || first == changedHash || changedVersion == changedHash || first == changedAssignmentHash {
		t.Fatalf("egress policy or assignment was not pinned in request hash: %s %s %s %s", first, changedVersion, changedHash, changedAssignmentHash)
	}
	changedInput, err := invocationRequestHash(command, 7, binding, []ModelCapability{"structured.output"}, []byte(`{"type":"object"}`), SHA256Bytes([]byte("changed-input")), 17, policyHash, 27, identityPolicyHash, assignment)
	if err != nil || changedInput == first {
		t.Fatalf("model input digest was not pinned in request hash: first=%s changed=%s err=%v", first, changedInput, err)
	}
}

func TestActionDigestDeterministic(t *testing.T) {
	inv := Invocation{ID: 1, OrganizationID: "explorarte", OrganizationRevisionID: 2, TaskID: 3, AttemptID: 4, DispatchActorRoleID: "x/y", SubjectRoleID: "x/y", DispatcherAssignmentID: int64Pointer(9), ExecutionPrincipalID: int64Pointer(11), ContextSnapshotID: 5, ModelProfileID: "worker-default", ModelProfileVersionID: 6, ProviderID: "test.fake", ProviderModelID: "v1", ModelEgressPolicyVersionID: int64Pointer(17), ModelEgressPolicyHash: SHA256Bytes([]byte("policy")), ExecutionIdentityPolicyVersionID: int64Pointer(27), ExecutionIdentityPolicyHash: SHA256Bytes([]byte("identity-policy")), RequestHash: SHA256Bytes([]byte("request")), RequiredCapabilities: []ModelCapability{"b", "a"}, OutputMode: OutputJSON, OutputSchema: json.RawMessage(`{"type":"object"}`), MaxOutputTokens: 100, ThinkingMode: ThinkingDisabled, Deadline: time.Unix(1000, 0).UTC()}
	a, err := ActionDigest(inv)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ActionDigest(inv)
	if err != nil || a != b || len(a) != 64 {
		t.Fatalf("unstable digest %s %s %v", a, b, err)
	}
}

func TestActionDigestRequiresDispatcherAssignmentPin(t *testing.T) {
	inv := Invocation{ID: 1, RequestHash: SHA256Bytes([]byte("request")), ModelEgressPolicyVersionID: int64Pointer(17), ModelEgressPolicyHash: SHA256Bytes([]byte("policy"))}
	if _, err := ActionDigest(inv); err == nil {
		t.Fatal("expected legacy unpinned invocation to reject action digest computation")
	}
}

func TestActionDigestChangesWithPinnedScope(t *testing.T) {
	base := Invocation{ID: 1, RequestHash: SHA256Bytes([]byte("request")), DispatcherAssignmentID: int64Pointer(9), ExecutionPrincipalID: int64Pointer(11), ModelEgressPolicyVersionID: int64Pointer(17), ModelEgressPolicyHash: SHA256Bytes([]byte("policy")), ExecutionIdentityPolicyVersionID: int64Pointer(27), ExecutionIdentityPolicyHash: SHA256Bytes([]byte("identity-policy"))}
	first, err := ActionDigest(base)
	if err != nil {
		t.Fatal(err)
	}
	cases := []Invocation{base, base, base, base, base, base, base, base}
	cases[0].ID++
	cases[1].RequestHash = SHA256Bytes([]byte("other-request"))
	cases[2].ModelEgressPolicyVersionID = int64Pointer(18)
	cases[3].ModelEgressPolicyHash = SHA256Bytes([]byte("other-policy"))
	cases[4].DispatcherAssignmentID = int64Pointer(999)
	cases[5].ExecutionPrincipalID = int64Pointer(999)
	cases[6].ExecutionIdentityPolicyVersionID = int64Pointer(28)
	cases[7].ExecutionIdentityPolicyHash = SHA256Bytes([]byte("other-identity-policy"))
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
