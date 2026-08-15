package questionidentity

import (
	"slices"
	"testing"
)

func TestCanonicalContractAcceptsWithPinnedHash(t *testing.T) {
	decision, err := Evaluate(CanonicalContract())
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if !decision.Accepted() {
		t.Fatalf("canonical contract rejected: %#v", decision)
	}
	if decision.NormalizedContractSHA256 != CanonicalContractSHA256 {
		t.Fatalf("normalized hash = %s, want canonical %s", decision.NormalizedContractSHA256, CanonicalContractSHA256)
	}
	if len(decision.ChangedFields) != 0 || len(decision.Reasons) != 0 {
		t.Fatalf("accepted decision contains drift details: %#v", decision)
	}
}

func TestPositiveNarrowRuntimeEvidenceDetail(t *testing.T) {
	contract := CanonicalContract()
	contract.NarrowingPredicates = []NarrowingPredicate{{ID: PredicateRuntimeEvidenceTimestamps}}

	decision, err := Evaluate(contract)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if !decision.Accepted() {
		t.Fatalf("valid narrowing rejected: %#v", decision)
	}
	if decision.NormalizedContractSHA256 == "" || decision.NormalizedContractSHA256 == CanonicalContractSHA256 {
		t.Fatalf("narrowed contract did not receive its own normalized hash: %#v", decision)
	}
}

func TestPositiveResolveUnclassifiedEntriesNarrowing(t *testing.T) {
	contract := CanonicalContract()
	contract.NarrowingPredicates = []NarrowingPredicate{{ID: PredicateResolveUnclassifiedSourceEntries}}

	decision, err := Evaluate(contract)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if !decision.Accepted() {
		t.Fatalf("real source-space narrowing rejected: %#v", decision)
	}
}

func TestEachProtectedFieldRejectsIndependentDrift(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*RefinementContract)
		field  ContractField
	}{
		{name: "subject drift only", field: FieldSubject, mutate: func(contract *RefinementContract) {
			contract.Subject = "literal_phrase_in_repository"
		}},
		{name: "requested relation drift only", field: FieldRequestedRelation, mutate: func(contract *RefinementContract) {
			contract.RequestedRelation = "repository_declares_literal_count"
		}},
		{name: "measurement universe drift only", field: FieldMeasurementUniverse, mutate: func(contract *RefinementContract) {
			contract.MeasurementUniverse = []string{MeasurementRuntimeEvidence}
		}},
		{name: "required output schema drift only", field: FieldRequiredOutputSchema, mutate: func(contract *RefinementContract) {
			contract.RequiredOutputSchema = slices.DeleteFunc(contract.RequiredOutputSchema, func(value string) bool {
				return value == OutputCompleteness
			})
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			contract := CanonicalContract()
			test.mutate(&contract)
			decision, err := Evaluate(contract)
			if err != nil {
				t.Fatalf("Evaluate() error = %v", err)
			}
			assertRejectedWithFields(t, decision, []ContractField{test.field})
		})
	}
}

func TestEachMissingProtectedFieldRejects(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*RefinementContract)
		field  ContractField
	}{
		{name: "subject", field: FieldSubject, mutate: func(contract *RefinementContract) { contract.Subject = "" }},
		{name: "requested relation", field: FieldRequestedRelation, mutate: func(contract *RefinementContract) { contract.RequestedRelation = "" }},
		{name: "measurement universe", field: FieldMeasurementUniverse, mutate: func(contract *RefinementContract) { contract.MeasurementUniverse = nil }},
		{name: "required output schema", field: FieldRequiredOutputSchema, mutate: func(contract *RefinementContract) { contract.RequiredOutputSchema = nil }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			contract := CanonicalContract()
			test.mutate(&contract)
			decision, err := Evaluate(contract)
			if err != nil {
				t.Fatalf("Evaluate() error = %v", err)
			}
			assertRejectedWithFields(t, decision, []ContractField{test.field})
		})
	}
}

func TestNegativeLiteralNFixtureRejectsAllTargetFields(t *testing.T) {
	// NEGATIVE-LITERAL-N is the structured transformation of:
	// "¿existe literalmente la frase N custom mechanisms?"
	literalPhraseSearch := RefinementContract{
		Subject:              "literal_phrase_in_repository",
		RequestedRelation:    "source_text_contains_literal_phrase",
		MeasurementUniverse:  []string{"repository_text_search_results"},
		RequiredOutputSchema: []string{"literal_phrase_exists"},
	}

	decision, err := Evaluate(literalPhraseSearch)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	assertRejectedWithFields(t, decision, []ContractField{
		FieldSubject,
		FieldRequestedRelation,
		FieldMeasurementUniverse,
		FieldRequiredOutputSchema,
	})
}

func TestNegativeDropCompletenessRejectsOutputDrift(t *testing.T) {
	contract := CanonicalContract()
	contract.RequiredOutputSchema = slices.DeleteFunc(contract.RequiredOutputSchema, func(value string) bool {
		return value == OutputCompleteness
	})

	decision, err := Evaluate(contract)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	assertRejectedWithFields(t, decision, []ContractField{FieldRequiredOutputSchema})
}

func TestNegativeRuntimeOnlyRejectsUniverseDrift(t *testing.T) {
	contract := CanonicalContract()
	contract.MeasurementUniverse = []string{MeasurementRuntimeEvidence}

	decision, err := Evaluate(contract)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	assertRejectedWithFields(t, decision, []ContractField{FieldMeasurementUniverse})
}

func TestSubjectAndRelationMustBeByteExact(t *testing.T) {
	contract := CanonicalContract()
	contract.Subject = CanonicalSubject + " "
	contract.RequestedRelation = "surviving_runtime_evidence<->declared_or_configured_capability"

	decision, err := Evaluate(contract)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	assertRejectedWithFields(t, decision, []ContractField{FieldSubject, FieldRequestedRelation})
}

func TestAdditionalCoreSourceMustBeLabeledSupplement(t *testing.T) {
	contract := CanonicalContract()
	contract.MeasurementUniverse = append(contract.MeasurementUniverse, "manual_notes")

	decision, err := Evaluate(contract)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	assertRejectedWithFields(t, decision, []ContractField{FieldMeasurementUniverse})

	contract = CanonicalContract()
	contract.ObservationSupplements = []ObservationSupplement{{SourceID: "manual_notes", Label: "observation supplement only"}}
	decision, err = Evaluate(contract)
	if err != nil {
		t.Fatalf("Evaluate() supplement error = %v", err)
	}
	if !decision.Accepted() {
		t.Fatalf("explicit supplement rejected: %#v", decision)
	}
}

func TestSupplementCannotRelabelCanonicalSource(t *testing.T) {
	contract := CanonicalContract()
	contract.ObservationSupplements = []ObservationSupplement{{
		SourceID: MeasurementSourceSpace,
		Label:    "supplement",
	}}

	decision, err := Evaluate(contract)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	assertRejectedWithFields(t, decision, []ContractField{FieldObservationSupplements})
}

func TestUnknownNarrowingPredicateFailsClosed(t *testing.T) {
	contract := CanonicalContract()
	contract.NarrowingPredicates = []NarrowingPredicate{{ID: "literal_phrase_exists"}}

	decision, err := Evaluate(contract)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	assertRejectedWithFields(t, decision, []ContractField{FieldNarrowingPredicates})
}

func TestAdditionalOutputIsAllowedAndNormalizationIsOrderIndependent(t *testing.T) {
	left := CanonicalContract()
	left.RequiredOutputSchema = append(left.RequiredOutputSchema, "z_detail", "a_detail")
	left.ObservationSupplements = []ObservationSupplement{
		{SourceID: "z_source", Label: "z label"},
		{SourceID: "a_source", Label: "a label"},
	}

	right := CanonicalContract()
	right.RequiredOutputSchema = append([]string{"a_detail", "z_detail"}, right.RequiredOutputSchema...)
	right.ObservationSupplements = []ObservationSupplement{
		{SourceID: "a_source", Label: "a label"},
		{SourceID: "z_source", Label: "z label"},
	}

	leftDecision, err := Evaluate(left)
	if err != nil {
		t.Fatalf("Evaluate(left) error = %v", err)
	}
	rightDecision, err := Evaluate(right)
	if err != nil {
		t.Fatalf("Evaluate(right) error = %v", err)
	}
	if !leftDecision.Accepted() || !rightDecision.Accepted() {
		t.Fatalf("allowed extensions rejected: left=%#v right=%#v", leftDecision, rightDecision)
	}
	if leftDecision.NormalizedContractSHA256 != rightDecision.NormalizedContractSHA256 {
		t.Fatalf("normalization depends on input order: %s != %s", leftDecision.NormalizedContractSHA256, rightDecision.NormalizedContractSHA256)
	}
}

func TestDuplicateMembersFailClosed(t *testing.T) {
	contract := CanonicalContract()
	contract.MeasurementUniverse = append(contract.MeasurementUniverse, MeasurementSourceSpace)
	contract.RequiredOutputSchema = append(contract.RequiredOutputSchema, OutputRuntimeEvidence)

	decision, err := Evaluate(contract)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	assertRejectedWithFields(t, decision, []ContractField{FieldMeasurementUniverse, FieldRequiredOutputSchema})
}

func TestCanonicalContractReturnsIndependentCopies(t *testing.T) {
	first := CanonicalContract()
	first.MeasurementUniverse[0] = "mutated"
	first.RequiredOutputSchema[0] = "mutated"

	decision, err := Evaluate(CanonicalContract())
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if !decision.Accepted() {
		t.Fatalf("canonical state was mutated through returned slices: %#v", decision)
	}
}

func assertRejectedWithFields(t *testing.T, decision Decision, want []ContractField) {
	t.Helper()
	if decision.Accepted() {
		t.Fatalf("drift accepted: %#v", decision)
	}
	if decision.Status != StatusQuestionTargetDrift || decision.Disposition != DispositionReject {
		t.Fatalf("wrong rejection codes: %#v", decision)
	}
	if !slices.Equal(decision.ChangedFields, want) {
		t.Fatalf("changed fields = %v, want %v; reasons=%v", decision.ChangedFields, want, decision.Reasons)
	}
	if len(decision.Reasons) != len(want) {
		t.Fatalf("reason count = %d, want %d: %v", len(decision.Reasons), len(want), decision.Reasons)
	}
	if decision.NormalizedContractSHA256 != "" {
		t.Fatalf("rejected contract received executable hash %q", decision.NormalizedContractSHA256)
	}
}
