package contentpolicy

import (
	"strings"
	"testing"
)

const (
	fakeGitHub = "ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	fakeAWS    = "AKIAIOSFODNN7EXAMPLE"
)

func TestAnalyzeClassifiesCredentialAndClinicalSignalsIndependently(t *testing.T) {
	assessment := Analyze("Patient record references " + fakeGitHub)
	if !assessment.Has(RiskCredential) || !assessment.Has(RiskClinical) {
		t.Fatalf("expected both typed risks, got %+v", assessment.Findings)
	}
	if got := assessment.Kinds(RiskCredential); len(got) != 1 || got[0] != string(KindAPIToken) {
		t.Fatalf("credential kinds = %v", got)
	}
	if got := assessment.Kinds(RiskClinical); len(got) != 1 || got[0] != string(KindClinicalTerminology) {
		t.Fatalf("clinical kinds = %v", got)
	}
}

func TestAnalyzeCoversDeclaredCredentialKinds(t *testing.T) {
	cases := []struct {
		name string
		kind Kind
		text string
	}{
		{"api token", KindAPIToken, fakeGitHub},
		{"password", KindPassword, `password=Tr0ub4dor&3xyz`},
		{"private key", KindPrivateKey, "-----BEGIN OPENSSH PRIVATE KEY-----"},
		{"cloud", KindCloudCredential, fakeAWS},
		{"database url", KindDatabaseURLCredential, "postgres://app:s3cr3tpassword@db.internal:5432/orgdb"},
		{"session", KindSessionToken, "Authorization: Bearer abcdef0123456789abcdef"},
		{"webhook", KindWebhookSigningSecret, "whsec_ABCDEFGHIJKLMNOPQRSTUV"},
		{"oauth", KindOAuthClientSecret, `client_secret=abcdefghijklmnop`},
		{"service account", KindServiceAccountCredential, `{"type":"service_account","private_key":"-----BEGIN"}`},
		{"generic assignment", KindCredentialAssignment, `api_key: "abcdefgh12345678"`},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			assessment := Analyze(testCase.text)
			finding, ok := assessment.First(RiskCredential)
			if !ok {
				t.Fatalf("no credential finding for %q", testCase.text)
			}
			if finding.Kind != testCase.kind {
				t.Fatalf("kind = %s, want %s; all=%+v", finding.Kind, testCase.kind, assessment.Findings)
			}
		})
	}
}

func TestAnalyzeLeavesPlaceholdersAndOrdinaryTextAlone(t *testing.T) {
	clean := []string{
		"Ask the patient to reset their password before the next session.",
		"Set password=<REDACTED> in the example configuration.",
		"Use password=${DB_PASSWORD} from the environment.",
		"api_key: your-api-key-here",
		"Review commit 7cd60785683cb197b3941974d1727311447af4fa.",
	}
	for _, text := range clean {
		if assessment := Analyze(text); assessment.Has(RiskCredential) {
			t.Fatalf("ordinary text classified as credential: %q -> %+v", text, assessment.Findings)
		}
	}
}

func TestRedactCredentialsPreservesClinicalTextAndRemovesValues(t *testing.T) {
	text := "patient history used " + fakeGitHub + " against the registry"
	redacted, findings := RedactCredentials(text)
	if len(findings) != 1 {
		t.Fatalf("credential findings = %+v", findings)
	}
	if strings.Contains(redacted, fakeGitHub) {
		t.Fatalf("redacted text leaked credential: %q", redacted)
	}
	if !strings.Contains(redacted, "patient history") || !strings.Contains(redacted, "credential_type=api_token") {
		t.Fatalf("redaction lost safe context or category: %q", redacted)
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

func TestOverlappingCredentialDetectorsReportOnce(t *testing.T) {
	assessment := Analyze("key sk-ant-api03-AAAABBBBCCCCDDDDEEEEFFFF")
	if got := len(assessment.For(RiskCredential)); got != 1 {
		t.Fatalf("credential findings = %d, want 1: %+v", got, assessment.Findings)
	}
}
