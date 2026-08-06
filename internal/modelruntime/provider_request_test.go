package modelruntime

import (
	"encoding/json"
	"testing"
	"time"
)

func providerRequestFixture() CanonicalRequest {
	temperature := 0.2
	return CanonicalRequest{
		InvocationID: 11, DispatchAttemptID: 12, OrganizationID: "explorarte",
		OrganizationRevisionID: 13, TaskID: 14, AttemptID: 15,
		DispatchActorRoleID: "ingenieria_ia/code-runner", SubjectRoleID: "ingenieria_ia/orquestador",
		ModelProfileID: "department.leader", ModelProfileVersionID: 16,
		ProviderID: "openai_compatible", ProviderModelID: "gpt-compatible",
		ProviderIdempotencyKey: SHA256Bytes([]byte("idempotency")), ContextSnapshotID: 17,
		ContextRenderedHash: SHA256Bytes([]byte("rendered context")), RenderedContext: []byte("never hash raw content into evidence output"),
		RequiredCapabilities: []ModelCapability{"structured.output", "analysis"}, OutputMode: OutputJSON,
		OutputSchema: json.RawMessage(`{"type":"object"}`), MaxOutputTokens: 100, Temperature: &temperature,
		ThinkingMode: ThinkingOpaque, ReasoningEffort: "high",
		Deadline: time.Date(2026, 8, 6, 12, 0, 0, 123456789, time.FixedZone("offset", -4*60*60)),
	}
}

func TestBuildProviderRequestEvidenceIsDeterministicAndContentIndependent(t *testing.T) {
	descriptor := validAdapterDescriptor()
	firstRequest := providerRequestFixture()
	first, err := BuildProviderRequestEvidence(firstRequest, descriptor)
	if err != nil {
		t.Fatal(err)
	}
	secondRequest := firstRequest
	secondRequest.RequiredCapabilities = []ModelCapability{"analysis", "structured.output"}
	secondRequest.Deadline = firstRequest.Deadline.UTC().Truncate(time.Microsecond)
	secondRequest.RenderedContext = []byte("different bytes with the same already-pinned rendered hash")
	second, err := BuildProviderRequestEvidence(secondRequest, descriptor)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("evidence drifted: first=%+v second=%+v", first, second)
	}
	thirdRequest := firstRequest
	thirdRequest.ContextRenderedHash = SHA256Bytes([]byte("different context"))
	third, err := BuildProviderRequestEvidence(thirdRequest, descriptor)
	if err != nil {
		t.Fatal(err)
	}
	if third.RequestHash == first.RequestHash {
		t.Fatal("request hash did not bind rendered context hash")
	}
}
