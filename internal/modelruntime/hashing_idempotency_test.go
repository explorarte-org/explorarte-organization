package modelruntime

import (
	"testing"
	"time"
)

func fixtureHashInputs() (CreateInvocationCommand, ResolvedBinding, []ModelCapability, []byte, string, int64, string, int64, string) {
	deadline := time.Unix(3000, 0).UTC()
	command := CreateInvocationCommand{
		OrganizationID: "explorarte", TaskID: 3, AttemptID: 4,
		SubjectRoleID:     "investigacion/research_worker_hourly",
		ContextSnapshotID: 5, Purpose: "fixture", OutputMode: OutputJSON,
		MaxOutputTokens: 100, ThinkingMode: ThinkingDisabled, Deadline: deadline,
	}
	binding := ResolvedBinding{
		Profile: Profile{ID: "research.worker~pool~0"},
		Version: ProfileVersion{ID: 100, ProviderID: "cloudflare_workers_ai", ProviderModelID: "@cf/zai-org/glm-4.7-flash"},
	}
	caps := []ModelCapability{"structured.output"}
	schema := []byte(`{"type":"object"}`)
	inputDigest := SHA256Bytes([]byte("model-input"))
	policyHash := SHA256Bytes([]byte("policy"))
	identityPolicyHash := SHA256Bytes([]byte("identity-policy"))
	return command, binding, caps, schema, inputDigest, 17, policyHash, 27, identityPolicyHash
}

// TestInvocationRequestHashChangesWithRoute is the regression test the
// corrective round names explicitly: changing ONLY ProviderID/
// ProviderModelID/ModelProfileVersionID (nothing else) must change
// RequestHash -- the full materialized identity.
func TestInvocationRequestHashChangesWithRoute(t *testing.T) {
	command, binding, caps, schema, inputDigest, policyVersionID, policyHash, identityPolicyVersionID, identityPolicyHash := fixtureHashInputs()
	assignment := fixtureResolvedAssignment()

	cloudflareHash, err := invocationRequestHash(command, 7, binding, caps, schema, inputDigest, policyVersionID, policyHash, identityPolicyVersionID, identityPolicyHash, assignment)
	if err != nil {
		t.Fatal(err)
	}

	// Profile.ID also legitimately differs per candidate in this design
	// (each candidate has its OWN profile), but even holding it fixed and
	// changing only the three route fields the corrective round names is
	// enough to prove the point:
	mistralOnlyRouteFields := binding
	mistralOnlyRouteFields.Version.ID = 101
	mistralOnlyRouteFields.Version.ProviderID = "mistral"
	mistralOnlyRouteFields.Version.ProviderModelID = "ministral-8b-2512"

	mistralHash, err := invocationRequestHash(command, 7, mistralOnlyRouteFields, caps, schema, inputDigest, policyVersionID, policyHash, identityPolicyVersionID, identityPolicyHash, assignment)
	if err != nil {
		t.Fatal(err)
	}
	if cloudflareHash == mistralHash {
		t.Fatal("RequestHash must change when ProviderID/ProviderModelID/ModelProfileVersionID change -- it is the invocation's COMPLETE materialized identity")
	}
}

// TestIdempotencyIntentHashStableAcrossRouteChange is the other half of
// the same regression: the SAME route change above must NOT change
// IdempotencyIntentHash -- it is computed before route resolution and
// never sees Binding at all.
func TestIdempotencyIntentHashStableAcrossRouteChange(t *testing.T) {
	command, _, caps, schema, inputDigest, policyVersionID, policyHash, identityPolicyVersionID, identityPolicyHash := fixtureHashInputs()
	assignment := fixtureResolvedAssignment()

	// idempotencyIntentHash does not take a binding parameter at all --
	// this is provable by construction, not just by equal output. Calling
	// it twice with the identical non-route inputs must be identical
	// (determinism), and there is no way to make it see a route change
	// because it has no such input.
	first, err := idempotencyIntentHash(command, 7, caps, schema, inputDigest, policyVersionID, policyHash, identityPolicyVersionID, identityPolicyHash, assignment)
	if err != nil {
		t.Fatal(err)
	}
	second, err := idempotencyIntentHash(command, 7, caps, schema, inputDigest, policyVersionID, policyHash, identityPolicyVersionID, identityPolicyHash, assignment)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("idempotencyIntentHash must be deterministic for identical non-route inputs: %s vs %s", first, second)
	}
}

// TestIdempotencyIntentHashReflectsLogicalRequestChanges proves the
// converse of the above: changing purpose, model input digest, or the
// output contract (things idempotencyIntentHash DOES take as input) must
// change it -- it is not a hash that ignores everything, only the route.
func TestIdempotencyIntentHashReflectsLogicalRequestChanges(t *testing.T) {
	command, _, caps, schema, inputDigest, policyVersionID, policyHash, identityPolicyVersionID, identityPolicyHash := fixtureHashInputs()
	assignment := fixtureResolvedAssignment()

	base, err := idempotencyIntentHash(command, 7, caps, schema, inputDigest, policyVersionID, policyHash, identityPolicyVersionID, identityPolicyHash, assignment)
	if err != nil {
		t.Fatal(err)
	}

	changedPurpose := command
	changedPurpose.Purpose = "a genuinely different logical request"
	withPurpose, err := idempotencyIntentHash(changedPurpose, 7, caps, schema, inputDigest, policyVersionID, policyHash, identityPolicyVersionID, identityPolicyHash, assignment)
	if err != nil {
		t.Fatal(err)
	}

	withInput, err := idempotencyIntentHash(command, 7, caps, schema, SHA256Bytes([]byte("a different model input")), policyVersionID, policyHash, identityPolicyVersionID, identityPolicyHash, assignment)
	if err != nil {
		t.Fatal(err)
	}

	withSchema, err := idempotencyIntentHash(command, 7, caps, []byte(`{"type":"object","required":["ok"]}`), inputDigest, policyVersionID, policyHash, identityPolicyVersionID, identityPolicyHash, assignment)
	if err != nil {
		t.Fatal(err)
	}

	if base == withPurpose || base == withInput || base == withSchema {
		t.Fatalf("idempotencyIntentHash must change when purpose/model-input/output-contract change: base=%s purpose=%s input=%s schema=%s", base, withPurpose, withInput, withSchema)
	}
}

// TestActionDigestChangesWithRoute: ActionDigest reads Invocation.RequestHash
// directly, so restoring the route fields into RequestHash automatically
// keeps ActionDigest bound to the exact dispatched route -- proven, not
// just asserted by comment.
func TestActionDigestChangesWithRoute(t *testing.T) {
	base := Invocation{
		ID: 1, RequestHash: SHA256Bytes([]byte("cloudflare-route")),
		DispatcherAssignmentID: int64Pointer(9), ExecutionPrincipalID: int64Pointer(11),
		ModelEgressPolicyVersionID: int64Pointer(17), ModelEgressPolicyHash: "egress",
		ExecutionIdentityPolicyVersionID: int64Pointer(27), ExecutionIdentityPolicyHash: "identity",
	}
	other := base
	other.RequestHash = SHA256Bytes([]byte("mistral-route"))

	baseDigest, err := ActionDigest(base)
	if err != nil {
		t.Fatal(err)
	}
	otherDigest, err := ActionDigest(other)
	if err != nil {
		t.Fatal(err)
	}
	if baseDigest == otherDigest {
		t.Fatal("ActionDigest must change when RequestHash (and therefore the route it now includes) changes")
	}
}
