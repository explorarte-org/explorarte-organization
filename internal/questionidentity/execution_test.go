package questionidentity

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

type fakeRefinementSource struct {
	payload []byte
	calls   int
}

func (source *fakeRefinementSource) NextRefinement(context.Context) ([]byte, error) {
	source.calls++
	return append([]byte(nil), source.payload...), nil
}

type countingModelAdapter struct {
	calls        int
	contractHash string
}

func (adapter *countingModelAdapter) ExecuteRefinement(_ context.Context, _ RefinementContract, contractHash string) error {
	adapter.calls++
	adapter.contractHash = contractHash
	return nil
}

type recordingDriftRecorder struct {
	records []DriftRecord
}

func (recorder *recordingDriftRecorder) RecordQuestionTargetDrift(_ context.Context, record DriftRecord) error {
	recorder.records = append(recorder.records, record)
	return nil
}

func TestExecutionPathRejectsLiteralNBeforeModelCall(t *testing.T) {
	envelope := CanonicalEnvelope()
	envelope.Contract = RefinementContract{
		Subject:              "literal_phrase_in_repository",
		RequestedRelation:    "source_text_contains_literal_phrase",
		MeasurementUniverse:  []string{"repository_text_search_results"},
		RequiredOutputSchema: []string{"literal_phrase_exists"},
	}
	source := &fakeRefinementSource{payload: mustJSON(t, envelope)}
	model := &countingModelAdapter{}
	recorder := &recordingDriftRecorder{}
	path := mustExecutionPath(t, source, model, recorder)

	modelCallsBefore := model.calls
	outcome, err := path.RunNext(context.Background())
	if err != nil {
		t.Fatalf("RunNext() error = %v", err)
	}
	modelCallsAfter := model.calls
	if modelCallsBefore != 0 || modelCallsAfter != 0 {
		t.Fatalf("rejected drift reached model: before=%d after=%d", modelCallsBefore, modelCallsAfter)
	}
	if outcome.ModelCallAllowed || outcome.ModelCallCompleted {
		t.Fatalf("rejected drift was marked callable: %#v", outcome)
	}
	assertRejectedWithFields(t, outcome.Decision, []ContractField{
		FieldSubject,
		FieldRequestedRelation,
		FieldMeasurementUniverse,
		FieldRequiredOutputSchema,
	})
	if len(recorder.records) != 1 || recorder.records[0].CandidatePayloadSHA256 == "" {
		t.Fatalf("drift was not recorded exactly once: %#v", recorder.records)
	}
}

func TestExecutionPathAllowsRealNarrowingThenCallsModel(t *testing.T) {
	envelope := CanonicalEnvelope()
	envelope.Contract.NarrowingPredicates = []NarrowingPredicate{{ID: PredicateResolveUnclassifiedSourceEntries}}
	source := &fakeRefinementSource{payload: mustJSON(t, envelope)}
	model := &countingModelAdapter{}
	recorder := &recordingDriftRecorder{}
	path := mustExecutionPath(t, source, model, recorder)

	outcome, err := path.RunNext(context.Background())
	if err != nil {
		t.Fatalf("RunNext() error = %v", err)
	}
	if !outcome.Decision.Accepted() || !outcome.ModelCallAllowed || !outcome.ModelCallCompleted {
		t.Fatalf("valid narrowing did not complete execution path: %#v", outcome)
	}
	if model.calls != 1 || model.contractHash != outcome.Decision.NormalizedContractSHA256 {
		t.Fatalf("model calls=%d hash=%q outcome=%#v", model.calls, model.contractHash, outcome)
	}
	if len(recorder.records) != 0 {
		t.Fatalf("accepted narrowing recorded drift: %#v", recorder.records)
	}
}

func TestExecutionPathAcceptsAllFourPreserved(t *testing.T) {
	source := &fakeRefinementSource{payload: mustJSON(t, CanonicalEnvelope())}
	model := &countingModelAdapter{}
	recorder := &recordingDriftRecorder{}
	path := mustExecutionPath(t, source, model, recorder)

	outcome, err := path.RunNext(context.Background())
	if err != nil {
		t.Fatalf("RunNext() error = %v", err)
	}
	if !outcome.Decision.Accepted() || model.calls != 1 {
		t.Fatalf("canonical execution rejected: outcome=%#v model_calls=%d", outcome, model.calls)
	}
}

func TestExecutionPathSingleFieldDriftNeverCallsModel(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*RefinementContract)
		field  ContractField
	}{
		{name: "subject", field: FieldSubject, mutate: func(contract *RefinementContract) {
			contract.Subject = "literal_phrase_in_repository"
		}},
		{name: "requested relation", field: FieldRequestedRelation, mutate: func(contract *RefinementContract) {
			contract.RequestedRelation = "literal_count_declaration"
		}},
		{name: "measurement universe", field: FieldMeasurementUniverse, mutate: func(contract *RefinementContract) {
			contract.MeasurementUniverse = []string{MeasurementRuntimeEvidence}
		}},
		{name: "required output schema", field: FieldRequiredOutputSchema, mutate: func(contract *RefinementContract) {
			contract.RequiredOutputSchema = []string{OutputCapabilityInventory}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			envelope := CanonicalEnvelope()
			test.mutate(&envelope.Contract)
			source := &fakeRefinementSource{payload: mustJSON(t, envelope)}
			model := &countingModelAdapter{}
			recorder := &recordingDriftRecorder{}
			path := mustExecutionPath(t, source, model, recorder)

			outcome, err := path.RunNext(context.Background())
			if err != nil {
				t.Fatalf("RunNext() error = %v", err)
			}
			if model.calls != 0 {
				t.Fatalf("single-field drift reached model %d time(s)", model.calls)
			}
			assertRejectedWithFields(t, outcome.Decision, []ContractField{test.field})
		})
	}
}

func TestExecutionPathMalformedOrUnknownInputFailsClosed(t *testing.T) {
	unknownSchema := CanonicalEnvelope()
	unknownSchema.SchemaVersion = "INSTRUMENT_V5"
	missingSchema := CanonicalEnvelope()
	missingSchema.SchemaVersion = ""
	wrongHash := CanonicalEnvelope()
	wrongHash.CanonicalContractSHA256 = "unexpected"
	missingHash := CanonicalEnvelope()
	missingHash.CanonicalContractSHA256 = ""
	canonicalPayload := mustJSON(t, CanonicalEnvelope())

	tests := []struct {
		name    string
		payload []byte
		field   ContractField
	}{
		{name: "nonparseable", payload: []byte(`{"schema_version":`), field: FieldPayload},
		{name: "unknown JSON field and model self-assertion", payload: append(append([]byte(nil), canonicalPayload[:len(canonicalPayload)-1]...), []byte(`,"model_claim":"identity preserved"}`)...), field: FieldPayload},
		{name: "trailing JSON", payload: append(append([]byte(nil), canonicalPayload...), []byte(` {}`)...), field: FieldPayload},
		{name: "missing schema", payload: mustJSON(t, missingSchema), field: FieldSchemaVersion},
		{name: "unknown schema", payload: mustJSON(t, unknownSchema), field: FieldSchemaVersion},
		{name: "missing contract hash", payload: mustJSON(t, missingHash), field: FieldContractHash},
		{name: "unexpected contract hash", payload: mustJSON(t, wrongHash), field: FieldContractHash},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := &fakeRefinementSource{payload: test.payload}
			model := &countingModelAdapter{}
			recorder := &recordingDriftRecorder{}
			path := mustExecutionPath(t, source, model, recorder)

			outcome, err := path.RunNext(context.Background())
			if err != nil {
				t.Fatalf("RunNext() error = %v", err)
			}
			if model.calls != 0 {
				t.Fatalf("invalid input reached model %d time(s)", model.calls)
			}
			assertRejectedWithFields(t, outcome.Decision, []ContractField{test.field})
			if len(recorder.records) != 1 {
				t.Fatalf("invalid input drift records = %d, want 1", len(recorder.records))
			}
		})
	}
}

func TestExecutionPathRequiresEveryDependency(t *testing.T) {
	source := &fakeRefinementSource{}
	model := &countingModelAdapter{}
	recorder := &recordingDriftRecorder{}
	for _, test := range []struct {
		name     string
		source   RefinementSource
		model    ModelAdapter
		recorder DriftRecorder
	}{
		{name: "source", model: model, recorder: recorder},
		{name: "model", source: source, recorder: recorder},
		{name: "recorder", source: source, model: model},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewExecutionPath(test.source, test.model, test.recorder); err == nil {
				t.Fatal("NewExecutionPath() accepted missing dependency")
			}
		})
	}
}

func TestExecutionPathDoesNotCallModelWhenDriftRecordingFails(t *testing.T) {
	source := &fakeRefinementSource{payload: []byte("not-json")}
	model := &countingModelAdapter{}
	recorder := failingDriftRecorder{}
	path := mustExecutionPath(t, source, model, recorder)

	if _, err := path.RunNext(context.Background()); err == nil {
		t.Fatal("RunNext() accepted drift-recording failure")
	}
	if model.calls != 0 {
		t.Fatalf("drift-recording failure reached model %d time(s)", model.calls)
	}
}

type failingDriftRecorder struct{}

func (failingDriftRecorder) RecordQuestionTargetDrift(context.Context, DriftRecord) error {
	return errors.New("record unavailable")
}

func mustExecutionPath(t *testing.T, source RefinementSource, model ModelAdapter, recorder DriftRecorder) *ExecutionPath {
	t.Helper()
	path, err := NewExecutionPath(source, model, recorder)
	if err != nil {
		t.Fatalf("NewExecutionPath() error = %v", err)
	}
	return path
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return payload
}
