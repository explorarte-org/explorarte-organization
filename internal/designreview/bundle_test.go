package designreview

import (
	"errors"
	"strings"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/designfreeze"
)

func bundleWithTarget(target string) Bundle {
	return Bundle{
		OwnerRequirements: []string{"the design proposes exactly one new table case"},
		CampaignTarget:    target,
		CandidateDesign:   "department: one case proposed",
		Design:            designfreeze.Design{ID: "design:root:1", Version: "v1", Digest: "abc"},
	}
}

// The target is part of the closed field list: it is encoded, and it survives the strict
// decode-and-re-encode every consumer of a stored bundle goes through.
func TestTheCampaignTargetTravelsInTheBundle(t *testing.T) {
	const target = `add one case named "digits adjacent to letters", text "abc123def45", want []string{"123","45"}`
	encoded, err := bundleWithTarget(target).Encode()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"campaign_target"`) || !strings.Contains(string(encoded), "abc123def45") {
		t.Fatalf("the target is not in the encoded bundle: %s", encoded)
	}
	decoded, reencoded, err := DecodeBundle(encoded)
	if err != nil {
		t.Fatalf("a bundle with a target was refused: %v", err)
	}
	if decoded.CampaignTarget != target {
		t.Fatalf("decoded target = %q", decoded.CampaignTarget)
	}
	if string(reencoded) != string(encoded) {
		t.Fatalf("the bundle is not stable under decode/encode:\n%s\n%s", encoded, reencoded)
	}
}

// A bundle without one is byte-for-byte what it was before the field existed, so bundles
// already stored in review tasks decode and re-encode unchanged.
func TestABundleWithoutATargetIsUnchanged(t *testing.T) {
	encoded, err := bundleWithTarget("").Encode()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "campaign_target") {
		t.Fatalf("an empty target must not appear at all: %s", encoded)
	}
	if _, reencoded, err := DecodeBundle(encoded); err != nil || string(reencoded) != string(encoded) {
		t.Fatalf("a bundle without a target changed under decode: %v", err)
	}
}

// The target is free text from the owner, so it is under the same credential scan as every
// other free-text field: it cannot be the field a secret rides in on.
func TestTheCampaignTargetIsUnderTheCredentialScan(t *testing.T) {
	_, err := bundleWithTarget("use the api_key from the environment").Encode()
	if !errors.Is(err, ErrBundleContaminated) {
		t.Fatalf("a target carrying credential material was encoded: %v", err)
	}
}

// Unknown fields are still refused: adding this one did not open the contract.
func TestTheBundleContractStaysClosed(t *testing.T) {
	encoded, err := bundleWithTarget("t").Encode()
	if err != nil {
		t.Fatal(err)
	}
	widened := strings.Replace(string(encoded), `"campaign_target"`, `"campaign_target_extra":"x","campaign_target"`, 1)
	if _, _, err := DecodeBundle([]byte(widened)); !errors.Is(err, ErrBundleContaminated) {
		t.Fatalf("a bundle with an unknown field was accepted: %v", err)
	}
}
