package tasks

import (
	"errors"
	"strings"
	"testing"
)

// These are the behavioral tests WB-3 is required to close on: the policy is
// "secrets are rejected at ingress", so the evidence has to be a rejection
// produced by the real validation path, not a claim in the covert-channel
// catalog. Deleting rejectSecrets from ValidateCreateRequest fails these.

func validRequest() CreateRequest {
	return NormalizeCreateRequest(CreateRequest{
		OrganizationID:     "explorarte",
		RequestedByRoleID:  "ingenieria/lead",
		AssignedRoleID:     "ingenieria/worker_a",
		IdempotencyKey:     "idem-1",
		Title:              "Revisar el pipeline de ingesta",
		Instructions:       "Revisa el pipeline y documenta los hallazgos en el informe semanal.",
		AcceptanceCriteria: []string{"El informe incluye los hallazgos"},
		MaxAttempts:        5,
	})
}

func TestCreateRequestAcceptsOrdinaryInstructions(t *testing.T) {
	if err := ValidateCreateRequest(validRequest()); err != nil {
		t.Fatalf("an ordinary request must validate, got: %v", err)
	}
}

func TestCreateRequestRejectsSecretsInEveryAgentVisibleField(t *testing.T) {
	const githubToken = "ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

	cases := []struct {
		name   string
		mutate func(*CreateRequest)
	}{
		{"instructions", func(r *CreateRequest) { r.Instructions = "Despliega usando " + githubToken }},
		{"title", func(r *CreateRequest) { r.Title = "Rotar " + githubToken }},
		{"acceptance criteria", func(r *CreateRequest) {
			r.AcceptanceCriteria = []string{"Confirmar que " + githubToken + " sigue activo"}
		}},
		{"requirement description", func(r *CreateRequest) {
			r.Requirements = []RequirementSpec{{Key: "cred", Description: "usar " + githubToken}}
		}},
		{"database url with password", func(r *CreateRequest) {
			r.Instructions = "Conecta a postgres://app:s3cr3tpassword@db.internal:5432/orgdb y migra."
		}},
		{"private key pasted inline", func(r *CreateRequest) {
			r.Instructions = "Usa esta clave:\n-----BEGIN OPENSSH PRIVATE KEY-----\nAAAA\n"
		}},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			request := validRequest()
			testCase.mutate(&request)
			err := ValidateCreateRequest(NormalizeCreateRequest(request))
			if err == nil {
				t.Fatal("credential material was accepted at ingress")
			}
			if !errors.Is(err, ErrSecretRejected) {
				t.Fatalf("rejected with the wrong error class: %v", err)
			}
			if strings.Contains(err.Error(), githubToken) || strings.Contains(err.Error(), "s3cr3tpassword") {
				t.Fatalf("the rejection error leaked the secret it rejected: %v", err)
			}
		})
	}
}

// TestCreateRequestAllowsHealthcareVocabulary pins the organization/cell
// boundary: words about patients or diagnosis are not proof of a clinical
// record and must not be heuristically rejected by the organization.
func TestCreateRequestAllowsHealthcareVocabulary(t *testing.T) {
	request := validRequest()
	request.Instructions = "Analiza cómo una empresa de salud organiza el historial del paciente en ejemplos de workflow."
	if err := ValidateCreateRequest(NormalizeCreateRequest(request)); err != nil {
		t.Fatalf("ordinary healthcare vocabulary must not be rejected: %v", err)
	}
}

// TestCreateRequestKeepsItsSizeBound documents the 64 KiB limit as behavior.
// The covert-channel catalog reports SizeBoundBytes for this channel, and
// that number is only trustworthy if something fails when it is exceeded.
func TestCreateRequestKeepsItsSizeBound(t *testing.T) {
	request := validRequest()
	request.Instructions = strings.Repeat("a", 65536)
	if err := ValidateCreateRequest(NormalizeCreateRequest(request)); err != nil {
		t.Fatalf("instructions of exactly 65536 bytes must be accepted: %v", err)
	}
	request.Instructions = strings.Repeat("a", 65537)
	if err := ValidateCreateRequest(NormalizeCreateRequest(request)); err == nil {
		t.Fatal("instructions beyond 65536 bytes must be rejected; large content belongs in an artifact reference")
	}
}
