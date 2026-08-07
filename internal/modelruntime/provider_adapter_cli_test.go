package modelruntime

import "testing"

func TestProviderOutcomeCLISuccess(t *testing.T) {
	zero := 0
	outcome := ProviderOutcome{
		OutcomeClassification: ProviderOutcomeResponseReceived,
		Transport:             TransportCLI,
		ProcessExitCode:       &zero,
		ResponseHash:          SHA256Bytes([]byte("response")),
		ResponseSchemaVersion: "claude.code.print.response.v1",
	}
	if err := outcome.Validate(); err != nil {
		t.Fatalf("validate CLI success: %v", err)
	}
	outcome.HTTPStatus = 200
	if err := outcome.Validate(); err == nil {
		t.Fatal("CLI outcome accepted an HTTP status")
	}
}

func TestLegacyProviderOutcomeStillDefaultsToHTTP(t *testing.T) {
	outcome := ProviderOutcome{
		OutcomeClassification: ProviderOutcomeResponseReceived,
		HTTPStatus:            200,
		ResponseHash:          SHA256Bytes([]byte("response")),
		ResponseSchemaVersion: "openai.chat.completions.response.v1",
	}
	if err := outcome.Validate(); err != nil {
		t.Fatalf("legacy HTTP outcome: %v", err)
	}
}

func TestProviderOutcomeCLIAmbiguousMayCarryExitCode(t *testing.T) {
	code := 143
	outcome := ProviderOutcome{
		OutcomeClassification: ProviderOutcomeAmbiguous,
		Transport:             TransportCLI,
		ProcessExitCode:       &code,
		ErrorClass:            "cli_transport",
		ErrorCode:             "process_cancelled_ambiguous",
		ResponseSchemaVersion: "claude.code.print.response.v1",
	}
	if err := outcome.Validate(); err != nil {
		t.Fatalf("validate CLI ambiguity: %v", err)
	}
}
