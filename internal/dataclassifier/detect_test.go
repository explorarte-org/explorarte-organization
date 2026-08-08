package dataclassifier_test

import (
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/dataclassifier"
)

func TestDetectFindsKnownSecretShapes(t *testing.T) {
	cases := []string{
		"-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA\n-----END RSA PRIVATE KEY-----",
		"aws access key id AKIAABCDEFGHIJKLMNOP in this ticket",
		"token: ghp_" + "abcdefghijklmnopqrstuvwxyz0123456789AB",
		"slack token xoxb-1234567890-abcdefghij",
		"authorization: eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dGhpc2lzYXNpZ25hdHVyZQ",
		"Authorization: Bearer abcdefghijklmnopqrstuvwx1234",
		`api_key: "sk_live_abcdefghijklmnop"`,
	}
	for _, body := range cases {
		f := dataclassifier.Detect(body)
		if !f.Secret {
			t.Fatalf("expected secret detection for %q", body)
		}
		if f.SecretReason == "" {
			t.Fatalf("expected a secret reason for %q", body)
		}
	}
}

func TestDetectFindsClinicalMarkers(t *testing.T) {
	cases := []string{
		"El paciente presenta síntomas de fiebre alta.",
		"Adjuntamos la historia clínica completa para revisión.",
		"The patient's diagnosis was confirmed after review.",
		"Se actualizó la receta médica del tratamiento.",
	}
	for _, body := range cases {
		f := dataclassifier.Detect(body)
		if !f.Clinical {
			t.Fatalf("expected clinical detection for %q", body)
		}
		if f.ClinicalReason == "" {
			t.Fatalf("expected a clinical reason for %q", body)
		}
	}
}

func TestDetectIgnoresOrdinaryOperationalText(t *testing.T) {
	cases := []string{
		"Antes de desplegar un modelo nuevo, valida la política de egress y el owner del dataset.",
		"Registra la evidencia de validación en el ticket de staging.",
		"The deployment pipeline retries failed model invocations up to three times.",
		"Gestión de riesgos en despliegues de modelos: revisar el catálogo de roles.",
	}
	for _, body := range cases {
		f := dataclassifier.Detect(body)
		if f.Any() {
			t.Fatalf("expected no detection for %q, got %+v", body, f)
		}
	}
}

func TestFindingAny(t *testing.T) {
	if (dataclassifier.Finding{}).Any() {
		t.Fatal("zero-value Finding must report Any()==false")
	}
	if !(dataclassifier.Finding{Secret: true}).Any() {
		t.Fatal("Secret finding must report Any()==true")
	}
	if !(dataclassifier.Finding{Clinical: true}).Any() {
		t.Fatal("Clinical finding must report Any()==true")
	}
}
