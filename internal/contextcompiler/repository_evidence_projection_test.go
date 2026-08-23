package contextcompiler_test

import (
	"context"
	stdBase64 "encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/contextcompiler"
	"github.com/Mireuz13/explorarte-organization/internal/contextengine"
)

// B1, the half that was missing: the excerpt must survive the projection into
// the bytes the Harness actually sends.
//
// The earlier witness followed the excerpt into the durable snapshot and the
// canonical render, which proves the assembler. P5 claims the AGENT receives
// it, and between the snapshot and the agent sits this compiler. If it stopped
// projecting repository_evidence tomorrow, every test written so far would
// stay green while the design went back to guessing -- the same locally-green
// false chain this whole subsystem exists to prevent.
func TestRepositoryEvidenceSurvivesProjectionToTheProviderBytes(t *testing.T) {
	const excerpt = "func (o *Orchestrator) driveDepartments() { /* observed */ }"
	const reference = "repository://explorarte-organization@c30328eda491241fccb81b8c83feb8a5b1e6cc35/internal/executive/orchestrator.go#L12-L48"
	const commit = "c30328eda491241fccb81b8c83feb8a5b1e6cc35"

	priority, ok := contextengine.AuthorityPriority(contextengine.TierRAGEvidence)
	if !ok {
		t.Fatal("no authority priority for the evidence tier")
	}
	payload := []byte(reference + "\n" + excerpt)
	snapshot := contextengine.Snapshot{
		ID: 7, OrganizationID: "explorarte", OrganizationRevisionID: 6,
		ActorRoleID: "ingenieria_ia/qa", Purpose: "department_worker",
		TaskClass: "engineering.design", ExecutionPurpose: "department-worker",
		Status: contextengine.SnapshotReady,
		Segments: []contextengine.Segment{{
			Ordinal: 1, RenderOrdinal: 1,
			AuthorityPriority: priority, AuthorityTier: contextengine.TierRAGEvidence,
			SourceKind: contextengine.SourceRepositoryEvidence, SourceReference: reference,
			SourceVersion:    commit,
			InstructionClass: contextengine.InstructionData, TrustClass: contextengine.TrustUntrusted,
			DataClass: contextengine.DataOrganizational, Included: true,
			Content: payload, ByteCount: len(payload),
			ContentHash: contextengine.DigestMarkdown(payload),
		}},
	}

	resolved, err := contextcompiler.ResolveProviderContext(context.Background(), snapshot)
	if err != nil {
		t.Fatalf("resolving the provider-visible context: %v", err)
	}
	provider := string(resolved.Bytes)

	// The bytes the Harness sends must contain the excerpt itself...
	if !strings.Contains(provider, excerpt) && !containsEncoded(provider, excerpt) {
		t.Fatal("the excerpt reached the snapshot and not the bytes the model is sent")
	}
	// ...and the citation, which is what a claim about it has to name.
	if !strings.Contains(provider, reference) && !containsEncoded(provider, reference) {
		t.Fatal("the citation did not survive projection, so no claim could be grounded in it")
	}
	if !strings.Contains(provider, commit) && !containsEncoded(provider, commit) {
		t.Fatal("the commit did not survive projection: the world the excerpt describes is unknown to the model")
	}
}

// containsEncoded also looks for the value base64-encoded, because segment
// content is []byte and a JSON envelope encodes it. Searching only the raw
// bytes once made an audit report zero occurrences of fields that were
// present, and the same mistake here would report a working projection as
// broken.
func containsEncoded(haystack, needle string) bool {
	return strings.Contains(base64Of(haystack), needle)
}

func base64Of(haystack string) string {
	// Decode every base64-looking run and search the concatenation.
	var out strings.Builder
	for _, field := range strings.FieldsFunc(haystack, func(r rune) bool {
		return !((r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '+' || r == '/' || r == '=')
	}) {
		if len(field) < 24 {
			continue
		}
		if decoded, err := decodeBase64(field); err == nil {
			out.WriteString(decoded)
		}
	}
	return out.String()
}

func decodeBase64(value string) (string, error) {
	for _, padding := range []string{"", "=", "=="} {
		if raw, err := stdBase64.StdEncoding.DecodeString(value + padding); err == nil {
			return string(raw), nil
		}
	}
	return "", errNotBase64
}

var errNotBase64 = errors.New("not base64")
