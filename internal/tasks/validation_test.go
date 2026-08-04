package tasks

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func validCreateRequest() CreateRequest {
	return CreateRequest{
		OrganizationID: "explorarte", AssignedRoleID: "ingenieria_ia/qa", IdempotencyKey: "task-001",
		Title: "Run durable checks", Instructions: "Execute the approved verification plan.",
		AcceptanceCriteria: []string{"tests pass"}, MaxAttempts: 5,
	}
}

func TestDecodeCreateRequestStrict(t *testing.T) {
	tests := []struct {
		name string
		body string
		want error
	}{
		{"valid", `{"assigned_role_id":"ingenieria_ia/qa","idempotency_key":"k","title":"t","instructions":"i","acceptance_criteria":[]}`, nil},
		{"unknown", `{"assigned_role_id":"ingenieria_ia/qa","idempotency_key":"k","title":"t","instructions":"i","unknown":1}`, ErrInvalidInput},
		{"forbidden_authority", `{"assigned_role_id":"ingenieria_ia/qa","idempotency_key":"k","title":"t","instructions":"i","authority":"admin"}`, ErrForbiddenField},
		{"forbidden_model_nested", `{"assigned_role_id":"ingenieria_ia/qa","idempotency_key":"k","title":"t","instructions":"i","requirements":[{"key":"x","type":"check","description":"d","model":"x"}]}`, ErrForbiddenField},
		{"forbidden_shell", `{"assigned_role_id":"ingenieria_ia/qa","idempotency_key":"k","title":"t","instructions":"i","shell":"bash"}`, ErrForbiddenField},
		{"forbidden_k8s", `{"assigned_role_id":"ingenieria_ia/qa","idempotency_key":"k","title":"t","instructions":"i","k8s":{}}`, ErrForbiddenField},
		{"multiple_values", `{} {}`, ErrInvalidInput},
		{"empty", ``, ErrInvalidInput},
		{"malformed", `{`, ErrInvalidInput},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := DecodeCreateRequest(strings.NewReader(test.body))
			if test.want == nil && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if test.want != nil && !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestValidateCreateRequestCases(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*CreateRequest)
	}{
		{"invalid_org", func(r *CreateRequest) { r.OrganizationID = "../bad" }},
		{"invalid_assignee", func(r *CreateRequest) { r.AssignedRoleID = "qa" }},
		{"empty_key", func(r *CreateRequest) { r.IdempotencyKey = "" }},
		{"long_key", func(r *CreateRequest) { r.IdempotencyKey = strings.Repeat("x", 201) }},
		{"empty_title", func(r *CreateRequest) { r.Title = "" }},
		{"long_title", func(r *CreateRequest) { r.Title = strings.Repeat("x", 241) }},
		{"empty_instructions", func(r *CreateRequest) { r.Instructions = "" }},
		{"priority_low", func(r *CreateRequest) { r.Priority = -100001 }},
		{"priority_high", func(r *CreateRequest) { r.Priority = 100001 }},
		{"attempts_zero", func(r *CreateRequest) { r.MaxAttempts = 0 }},
		{"attempts_high", func(r *CreateRequest) { r.MaxAttempts = 101 }},
		{"empty_criterion", func(r *CreateRequest) { r.AcceptanceCriteria = []string{""} }},
		{"negative_dependency", func(r *CreateRequest) { r.Dependencies = []int64{-1} }},
		{"duplicate_dependency", func(r *CreateRequest) { r.Dependencies = []int64{1, 1} }},
		{"duplicate_requirement", func(r *CreateRequest) {
			r.Requirements = []RequirementSpec{{Key: "qa", Type: RequirementCheck, Description: "a"}, {Key: "qa", Type: RequirementCheck, Description: "b"}}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := validCreateRequest()
			test.mutate(&request)
			if err := ValidateCreateRequest(request); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("error = %v, want ErrInvalidInput", err)
			}
		})
	}
}

func TestNormalizeAndHashCreateRequest(t *testing.T) {
	request := validCreateRequest()
	request.Title = "  Run durable checks  "
	request.AcceptanceCriteria = []string{"  tests pass "}
	normalized := NormalizeCreateRequest(request)
	if normalized.Title != "Run durable checks" || normalized.AcceptanceCriteria[0] != "tests pass" {
		t.Fatalf("normalization failed: %+v", normalized)
	}
	nilCriteria := NormalizeCreateRequest(CreateRequest{})
	if nilCriteria.AcceptanceCriteria == nil || len(nilCriteria.AcceptanceCriteria) != 0 {
		t.Fatalf("nil acceptance criteria were not normalized to an empty array: %#v", nilCriteria.AcceptanceCriteria)
	}
	first, err := HashCreateRequest(normalized)
	if err != nil {
		t.Fatal(err)
	}
	second, err := HashCreateRequest(normalized)
	if err != nil {
		t.Fatal(err)
	}
	if first != second || len(first) != 64 {
		t.Fatalf("hashes = %q and %q", first, second)
	}
	normalized.Title = "different"
	third, _ := HashCreateRequest(normalized)
	if third == first {
		t.Fatal("different request produced the same request hash")
	}
}

func TestRequirementAndEvidenceValidation(t *testing.T) {
	if err := ValidateRequirementSpec(RequirementSpec{Key: "build-check", Type: RequirementCheck, Description: "build passes"}); err != nil {
		t.Fatalf("valid requirement: %v", err)
	}
	for _, spec := range []RequirementSpec{
		{Key: "Bad Key", Type: RequirementCheck, Description: "x"},
		{Key: "ok", Type: "unknown", Description: "x"},
		{Key: "ok", Type: RequirementCheck, Description: ""},
	} {
		if err := ValidateRequirementSpec(spec); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("spec %+v error = %v", spec, err)
		}
	}
	id := int64(1)
	valid := RecordEvidenceCommand{TaskID: 1, RequirementID: &id, Type: RequirementCheck, Reference: "artifact://checks/1", Digest: strings.Repeat("a", 64), RecordedBy: "worker-1"}
	if err := ValidateEvidence(valid); err != nil {
		t.Fatalf("valid evidence: %v", err)
	}
	valid.Digest = "bad"
	if err := ValidateEvidence(valid); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("digest error = %v", err)
	}
}

func TestNormalizeAvailableAtUTC(t *testing.T) {
	value := time.Date(2026, 8, 4, 12, 0, 0, 123, time.FixedZone("x", -4*60*60))
	normalized := NormalizeAvailableAt(&value)
	if normalized == nil || normalized.Location() != time.UTC || normalized.Nanosecond() != 123 {
		t.Fatalf("normalized = %v", normalized)
	}
}
