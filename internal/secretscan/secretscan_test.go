package secretscan

import (
	"strings"
	"testing"
)

// Fake credentials. None of these is real; they are shaped to match the
// detectors so the tests exercise the same paths a genuine leak would.
const (
	fakeOpenAI    = "sk-abcdefghijklmnopqrstuvwxyz012345"
	fakeAnthropic = "sk-ant-api03-AAAABBBBCCCCDDDDEEEEFFFF"
	fakeGitHub    = "ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	fakeSlack     = "xoxb-1234567890-ABCDEFGHIJKLMNOP"
	fakeGoogle    = "AIzaSyABCDEFGHIJKLMNOPQRSTUVWXYZ0123456"
	fakeAWSKeyID  = "AKIAIOSFODNN7EXAMPLE"
	fakeJWT       = "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dBjftJeZ4CVPmB92K27uhbUJU1p1r_wW1gFWFOEjXk"
)

func TestDetectsEveryDeclaredSecretCategory(t *testing.T) {
	cases := []struct {
		name string
		kind Kind
		text string
	}{
		{"openai style token", KindAPIToken, "Use " + fakeOpenAI + " to call the API."},
		{"anthropic token", KindAPIToken, "key: " + fakeAnthropic},
		{"github token", KindAPIToken, "clone with " + fakeGitHub},
		{"slack token", KindAPIToken, fakeSlack},
		{"google api key", KindAPIToken, fakeGoogle},
		{"password assignment", KindPassword, `connect with password=Tr0ub4dor&3xyz`},
		{"password json field", KindPassword, `{"password": "hunter2seventeen"}`},
		{"private key block", KindPrivateKey, "-----BEGIN RSA PRIVATE KEY-----\nMIIE...\n"},
		{"openssh private key", KindPrivateKey, "-----BEGIN OPENSSH PRIVATE KEY-----"},
		{"aws access key id", KindCloudCredential, "export AWS_ACCESS_KEY_ID=" + fakeAWSKeyID},
		{"aws secret access key", KindCloudCredential, "aws_secret_access_key = wJalrXUtnFEMIK7MDENGbPxRfiCYEXAMPLEKEY123"},
		{"azure account key", KindCloudCredential, "DefaultEndpointsProtocol=https;AccountKey=abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGH=="},
		{"postgres url with password", KindDatabaseURLCredential, "postgres://app:s3cr3tpassword@db.internal:5432/orgdb"},
		{"mongodb srv url", KindDatabaseURLCredential, "mongodb+srv://svc:PleaseRotateMe@cluster0.example.net"},
		{"jwt session token", KindSessionToken, "token " + fakeJWT},
		{"bearer header", KindSessionToken, "Authorization: Bearer abcdef0123456789abcdef"},
		{"session cookie", KindSessionToken, "Cookie: sessionid=abcdef0123456789abcdef0123"},
		{"stripe webhook secret", KindWebhookSigningSecret, "whsec_ABCDEFGHIJKLMNOPQRSTUV"},
		{"signing secret assignment", KindWebhookSigningSecret, `signing_secret: "a1b2c3d4e5f6a7b8"`},
		{"oauth client secret", KindOAuthClientSecret, `client_secret=abcdefghijklmnop`},
		{"gcp service account json", KindServiceAccountCredential, `{"type": "service_account", "project_id": "p", "private_key": "-----BEGIN"},`},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			findings := Scan(testCase.text)
			if len(findings) == 0 {
				t.Fatalf("no secret detected in %q", testCase.name)
			}
			var matched bool
			for _, finding := range findings {
				if finding.Kind == testCase.kind {
					matched = true
				}
			}
			if !matched {
				t.Fatalf("detected %v, want a %s finding", Kinds(findings), testCase.kind)
			}
		})
	}
}

// TestOrdinaryInstructionsAreNotRejected is the half that decides whether
// this control survives contact with real users. A credential filter that
// fires on ordinary task text gets routed around, so false positives are
// asserted as strictly as detections.
func TestOrdinaryInstructionsAreNotRejected(t *testing.T) {
	ordinary := []string{
		"Ask the patient to reset their password before the next session.",
		"The password policy requires rotation every 90 days.",
		"Document how the client_secret should be stored, without including it here.",
		"Connect to postgres://reporting.internal:5432/analytics using the shared role.",
		"Set password=<REDACTED> in the example configuration.",
		"Use password=${DB_PASSWORD} from the environment.",
		"password: changeme",
		"api key: your-api-key-here",
		"Store the token in /run/secrets/gemini-embedding-api-key and reference the file.",
		"Review commit 7cd60785683cb197b3941974d1727311447af4fa for the regression.",
		"The SHA-256 digest is 9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08.",
		"Escribe un informe sobre la sesión clínica de Sonia Silva y sus resultados.",
		"El identificador de paciente es TH001-PX-001 y su diagnóstico es TDAH inatento.",
	}
	for _, text := range ordinary {
		t.Run(truncate(text), func(t *testing.T) {
			if findings := Scan(text); len(findings) != 0 {
				t.Fatalf("ordinary text rejected as %v: %q", Kinds(findings), text)
			}
		})
	}
}

// TestSensitiveButNotSecretIsCarried pins the policy boundary: personal and
// clinical material is governed by classification and access control, not
// refused at ingress. Folding it in here would turn a credential filter into
// a censor.
func TestSensitiveButNotSecretIsCarried(t *testing.T) {
	sensitive := "Paciente Sonia Silva, 34 años, diagnóstico TDAH inatento, historia clínica TH001-PX-001. " +
		"Correo: sonia@example.com. Teléfono: +56 9 1234 5678."
	if findings := Scan(sensitive); len(findings) != 0 {
		t.Fatalf("clinical/personal data must not be treated as credential material, got %v", Kinds(findings))
	}
}

// TestFindingsNeverCarryTheSecret is the property that makes this package
// safe to call from an audit path: a detector whose output leaks the value it
// found has moved the secret, not contained it.
func TestFindingsNeverCarryTheSecret(t *testing.T) {
	text := "deploy with " + fakeGitHub + " and " + fakeAWSKeyID
	findings := Scan(text)
	if len(findings) == 0 {
		t.Fatal("expected findings")
	}
	for _, finding := range findings {
		rendered := finding.String()
		if strings.Contains(rendered, fakeGitHub) || strings.Contains(rendered, fakeAWSKeyID) {
			t.Fatalf("finding rendering leaked the secret: %s", rendered)
		}
	}
	for _, kind := range Kinds(findings) {
		if strings.Contains(kind, fakeGitHub) || strings.Contains(kind, fakeAWSKeyID) {
			t.Fatalf("kind rendering leaked the secret: %s", kind)
		}
	}
}

// TestRedactKeepsTheSignalAndDropsTheValue covers the observability half of
// the policy: an operator must learn that a secret was present, and of what
// kind, without the value entering a log they cannot rotate.
func TestRedactKeepsTheSignalAndDropsTheValue(t *testing.T) {
	text := "failed to authenticate using " + fakeGitHub + " against the registry"
	redacted, findings := Redact(text)
	if len(findings) == 0 {
		t.Fatal("expected the secret to be found")
	}
	if strings.Contains(redacted, fakeGitHub) {
		t.Fatalf("redacted text still contains the secret: %q", redacted)
	}
	if !strings.Contains(redacted, "secret_redacted=true") || !strings.Contains(redacted, "secret_type=api_token") {
		t.Fatalf("redacted text lost the audit signal: %q", redacted)
	}
	if !strings.Contains(redacted, "against the registry") {
		t.Fatalf("redaction destroyed surrounding context: %q", redacted)
	}
}

func TestRedactLeavesCleanTextUntouched(t *testing.T) {
	text := "Prepare the quarterly report and share it with the finance department."
	redacted, findings := Redact(text)
	if len(findings) != 0 || redacted != text {
		t.Fatalf("clean text was modified: %q", redacted)
	}
}

// TestOverlappingDetectionsReportOnce guards the audit record against
// double-counting one credential matched by two detectors.
func TestOverlappingDetectionsReportOnce(t *testing.T) {
	findings := Scan("key " + fakeAnthropic)
	if len(findings) != 1 {
		t.Fatalf("expected a single finding for one credential, got %d: %v", len(findings), findings)
	}
}

func truncate(value string) string {
	if len(value) <= 40 {
		return value
	}
	return value[:40]
}
