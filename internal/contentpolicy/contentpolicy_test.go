package contentpolicy

import (
	"reflect"
	"strings"
	"testing"
)

const (
	fakeGitHub = "ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	fakeAWS    = "AKIAIOSFODNN7EXAMPLE"
	fakeJWT    = "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dBjftJeZ4CVPmB92K27uhbUJU1p1r_wW1gFWFOEjXk"
)

func TestAnalyzeDetectsCredentialAlongsideOrdinaryPatientVocabulary(t *testing.T) {
	assessment := Analyze("Patient workflow reference uses " + fakeGitHub)
	if !assessment.HasCredentials() {
		t.Fatal("credential was not detected")
	}
	if len(assessment.Findings) != 1 || assessment.Findings[0].Kind != KindAPIToken {
		t.Fatalf("findings = %+v, want exactly one credential finding", assessment.Findings)
	}
}

func TestAnalyzeDoesNotInferClinicalDataFromVocabulary(t *testing.T) {
	text := "The patient model is used in healthcare workflow examples"
	if assessment := Analyze(text); assessment.HasCredentials() || len(assessment.Findings) != 0 {
		t.Fatalf("ordinary organizational knowledge produced findings: %+v", assessment.Findings)
	}
}

func TestAnalyzeCoversDeclaredCredentialKinds(t *testing.T) {
	cases := []struct {
		name string
		kind Kind
		text string
	}{
		{"openai style token", KindAPIToken, "sk-abcdefghijklmnopqrstuvwxyz012345"},
		{"anthropic token", KindAPIToken, "sk-ant-api03-AAAABBBBCCCCDDDDEEEEFFFF"},
		{"github token", KindAPIToken, fakeGitHub},
		{"slack token", KindAPIToken, "xoxb-1234567890-ABCDEFGHIJKLMNOP"},
		{"google api key", KindAPIToken, "AIzaSyABCDEFGHIJKLMNOPQRSTUVWXYZ0123456"},
		{"password", KindPassword, `password=Tr0ub4dor&3xyz`},
		{"private key", KindPrivateKey, "-----BEGIN OPENSSH PRIVATE KEY-----"},
		{"cloud", KindCloudCredential, fakeAWS},
		{"aws secret", KindCloudCredential, "aws_secret_access_key=wJalrXUtnFEMIK7MDENGbPxRfiCYEXAMPLEKEY123"},
		{"azure account key", KindCloudCredential, "AccountKey=abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGH=="},
		{"database url", KindDatabaseURLCredential, "postgres://app:s3cr3tpassword@db.internal:5432/orgdb"},
		{"mongodb url", KindDatabaseURLCredential, "mongodb+srv://svc:PleaseRotateMe@cluster0.example.net"},
		{"session", KindSessionToken, "Authorization: Bearer abcdef0123456789abcdef"},
		{"jwt", KindSessionToken, fakeJWT},
		{"session cookie", KindSessionToken, "sessionid=abcdef0123456789abcdef0123"},
		{"webhook", KindWebhookSigningSecret, "whsec_ABCDEFGHIJKLMNOPQRSTUV"},
		{"assigned webhook", KindWebhookSigningSecret, `signing_secret: "a1b2c3d4e5f6a7b8"`},
		{"oauth", KindOAuthClientSecret, `client_secret=abcdefghijklmnop`},
		{"service account", KindServiceAccountCredential, `{"type":"service_account","private_key":"-----BEGIN"}`},
		{"generic assignment", KindCredentialAssignment, `api_key: "abcdefgh12345678"`},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			assessment := Analyze(testCase.text)
			finding, ok := assessment.First()
			if !ok {
				t.Fatalf("no credential finding for %q", testCase.text)
			}
			if finding.Kind != testCase.kind {
				t.Fatalf("kind = %s, want %s; all=%+v", finding.Kind, testCase.kind, assessment.Findings)
			}
		})
	}
}

func TestAnalyzeAllowsDocumentedPlaceholders(t *testing.T) {
	placeholders := []string{
		"api_key: your-api-key-here",
		"api_key: your_api_key",
		"password=<REDACTED>",
		"password=${DB_PASSWORD}",
		"access_token=$TOKEN",
		"password=changeme",
		"password=example",
		"password=dummy",
		"password=placeholder",
	}
	for _, text := range placeholders {
		if assessment := Analyze(text); assessment.HasCredentials() {
			t.Fatalf("documented placeholder classified as credential: %q -> %+v", text, assessment.Findings)
		}
	}
}

func TestPlaceholderPrefixDoesNotCreateBypass(t *testing.T) {
	for _, text := range []string{
		"api_key: your-production-token-ABCD1234",
		"api_key: my-real-secret-123456",
	} {
		if assessment := Analyze(text); !assessment.HasCredentials() {
			t.Fatalf("credential-shaped value bypassed through placeholder prefix: %q", text)
		}
	}
}

func TestFindingsNeverContainMatchedContent(t *testing.T) {
	for _, finding := range Analyze("keys: " + fakeGitHub + " " + fakeAWS).Findings {
		rendered := finding.String()
		if strings.Contains(rendered, fakeGitHub) || strings.Contains(rendered, fakeAWS) {
			t.Fatalf("finding leaked matched content: %s", rendered)
		}
	}
}

func TestRedactCredentialsRemovesValuePreservesContextAndIsDeterministic(t *testing.T) {
	text := "patient workflow used " + fakeGitHub + " against the registry"
	first, findings := RedactCredentials(text)
	second, secondFindings := RedactCredentials(text)
	if strings.Contains(first, fakeGitHub) {
		t.Fatalf("redacted text leaked credential: %q", first)
	}
	if !strings.Contains(first, "patient workflow used ") || !strings.Contains(first, " against the registry") {
		t.Fatalf("redaction lost surrounding text: %q", first)
	}
	if first != second || !reflect.DeepEqual(findings, secondFindings) {
		t.Fatalf("redaction is not deterministic: %q/%+v != %q/%+v", first, findings, second, secondFindings)
	}
}

func TestCollapseUnionsOverlapsKeepsAdjacentFindingsAndSortsStably(t *testing.T) {
	input := []Finding{
		{Kind: KindPassword, Start: 100, End: 120}, // adjacent to the union
		{Kind: KindAPIToken, Start: 50, End: 100},  // partial overlap extends it
		{Kind: KindCloudCredential, Start: 15, End: 30},
		{Kind: KindPrivateKey, Start: 0, End: 60},
		{Kind: KindSessionToken, Start: 20, End: 25}, // contained
	}
	want := []Finding{
		{Kind: KindPrivateKey, Start: 0, End: 100},
		{Kind: KindPassword, Start: 100, End: 120},
	}
	if got := collapse(append([]Finding(nil), input...)); !reflect.DeepEqual(got, want) {
		t.Fatalf("collapse = %+v, want %+v", got, want)
	}
	if got := collapse(append([]Finding(nil), input...)); !reflect.DeepEqual(got, want) {
		t.Fatalf("second collapse changed output: %+v", got)
	}
}

func TestOverlappingCredentialDetectorsProduceOneRedactionSpanWithoutPanic(t *testing.T) {
	text := "Authorization: Bearer " + fakeJWT
	assessment := Analyze(text)
	if len(assessment.Findings) != 1 {
		t.Fatalf("overlapping detectors produced %+v, want one safe span", assessment.Findings)
	}
	redacted, findings := RedactCredentials(text)
	if len(findings) != 1 || strings.Contains(redacted, fakeJWT) {
		t.Fatalf("unsafe overlapping redaction: text=%q findings=%+v", redacted, findings)
	}
}
