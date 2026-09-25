package designreview

import (
	"errors"
	"strings"
	"testing"
)

// The bundle's credential scan is internal/contentpolicy, not a list of substrings. Root 1315
// (2026-09-25) was blocked before its adversarial review because a worker wrote "task-supplied
// facts" and "ta[sk-]supplied" contains the substring "sk-" the old list refused.
//
// Tokens are assembled at run time so this file carries no literal with the shape of a
// credential (a secret scanner cannot tell a test fixture from a leak).

func fakeToken(prefix string, count int) string {
	return prefix + strings.Repeat("aB3", count/3+1)[:count]
}

func TestOrdinaryDesignLanguageIsNotCredentialMaterial(t *testing.T) {
	for _, text := range []string{
		"task-supplied facts are taken as given",
		"a risk-based, disk-backed, mask-value ask-for-review flow",
		"task-supplied", "risk-based", "disk-backed", "mask-value", "ask-for-review",
		"the password policy requires rotation",
		"API key rotation policy",
		"the secret_key field is prohibited in the schema",
		"use the api_key from the environment",
		"the Authorization header carries the caller's identity",
		"the desk-side task-list for the sk- prefix convention is described in prose",
		"password=changeme",
		"api_key=<your-api-key>",
	} {
		if err := AssertNoCredentialMaterial("body", []byte(text)); err != nil {
			t.Errorf("%q was refused: %v", text, err)
		}
	}
}

func TestCredentialMaterialIsRefused(t *testing.T) {
	for name, text := range map[string]string{
		"openai style key":      "call it with " + fakeToken("sk-", 24),
		"anthropic style key":   "key " + fakeToken("sk-ant-", 20),
		"github token":          "token " + fakeToken("ghp_", 36),
		"github fine-grained":   fakeToken("github_pat_", 40),
		"gitlab token":          fakeToken("glpat-", 20),
		"slack token":           fakeToken("xoxb-", 14),
		"google api key":        fakeToken("AIza", 35),
		"private key header":    "-----BEGIN " + "PRIVATE KEY-----",
		"bearer authorization":  "Authorization: Bearer " + fakeToken("", 24),
		"password assignment":   "password=hunter2secret",
		"api key assignment":    "api_key=" + fakeToken("", 20),
		"secret key assignment": "secret_key: " + fakeToken("", 20),
	} {
		t.Run(name, func(t *testing.T) {
			err := AssertNoCredentialMaterial("body", []byte(text))
			if !errors.Is(err, ErrBundleContaminated) {
				t.Fatalf("%q was not refused: %v", text, err)
			}
		})
	}
}

// The error is safe to log and persist: category and byte offsets, never the value.
func TestTheRefusalNeverContainsTheMatchedValue(t *testing.T) {
	token := fakeToken("sk-", 30)
	err := AssertNoCredentialMaterial("bundle", []byte("prefix "+token+" suffix"))
	if err == nil {
		t.Fatal("not refused")
	}
	if strings.Contains(err.Error(), token) || strings.Contains(err.Error(), token[3:]) {
		t.Fatalf("the error carries the matched value: %v", err)
	}
	for _, want := range []string{"bundle", "credential/api_token", "bytes"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
}

// A value quoted inside a JSON string is escaped there; the scan must see through that.
func TestAnEscapedQuotedAssignmentInsideJSONIsRefused(t *testing.T) {
	_, err := Bundle{CandidateDesign: `set password="hunter2secret" in the config`}.Encode()
	if !errors.Is(err, ErrBundleContaminated) {
		t.Fatalf("a quoted password assignment inside a bundle was encoded: %v", err)
	}
}

// The exact root-1315 pair, through the real Encode.
func TestRoot1315TheWordThatBlockedTheRunNoLongerDoes(t *testing.T) {
	ok := bundleWithTarget("add one case")
	ok.CandidateDesign = "The values are taken as task-supplied facts, not as claims from evidence."
	if _, err := ok.Encode(); err != nil {
		t.Fatalf("root 1315's candidate design was refused: %v", err)
	}
	bad := bundleWithTarget("add one case")
	bad.CandidateDesign = "use " + fakeToken("sk-", 21) + " here"
	if _, err := bad.Encode(); !errors.Is(err, ErrBundleContaminated) {
		t.Fatalf("a candidate design carrying an sk- token was encoded: %v", err)
	}
}

// Every free-text field is scanned, not only the candidate, and a stored bundle that carries a
// credential is refused on the way back in.
func TestEveryFreeTextFieldAndTheDecodePathAreScanned(t *testing.T) {
	token := fakeToken("ghp_", 36)
	for name, mutate := range map[string]func(*Bundle){
		"owner requirement": func(b *Bundle) { b.OwnerRequirements = []string{"see " + token} },
		"campaign target":   func(b *Bundle) { b.CampaignTarget = "see " + token },
		"candidate design":  func(b *Bundle) { b.CandidateDesign = "see " + token },
	} {
		t.Run(name, func(t *testing.T) {
			bundle := bundleWithTarget("t")
			mutate(&bundle)
			if _, err := bundle.Encode(); !errors.Is(err, ErrBundleContaminated) {
				t.Fatalf("not refused: %v", err)
			}
		})
	}
	clean, err := bundleWithTarget("t").Encode()
	if err != nil {
		t.Fatal(err)
	}
	tainted := strings.Replace(string(clean), "department: one case proposed", "token "+token, 1)
	if _, _, err := DecodeBundle([]byte(tainted)); !errors.Is(err, ErrBundleContaminated) {
		t.Fatalf("a stored bundle carrying a credential was decoded: %v", err)
	}
}
