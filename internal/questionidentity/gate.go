// Package questionidentity implements the fail-closed semantic identity gate
// that must run before a Q3 refinement can collect evidence or spend resources.
package questionidentity

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
)

const (
	// CanonicalContractSHA256 pins the byte-exact canonical JSON frozen by
	// Q3_ONTOLOGY_V1. The JSON has no trailing newline.
	CanonicalContractSHA256 = "5d14179555b1634238ffd197bc56afa42bc5b1dc93e5605f5d2e58f368e48f9c"

	CanonicalSubject           = "organizational_capabilities_implemented_by_this_repository"
	CanonicalRequestedRelation = "declared_or_configured_capability<->surviving_runtime_evidence"

	MeasurementSourceSpace     = "deterministically_registered_accessible_source_space"
	MeasurementRuntimeEvidence = "surviving_runtime_evidence_universe"

	OutputCapabilityInventory = "capability_inventory_under_frozen_ontology"
	OutputDeclarationConfig   = "declaration_configuration_provenance"
	OutputRuntimeEvidence     = "runtime_evidence"
	OutputLimitations         = "observation_limitations"
	OutputCompleteness        = "completeness_accounting"
)

const canonicalContractJSON = `{"measurement_universe":["deterministically_registered_accessible_source_space","surviving_runtime_evidence_universe"],"requested_relation":"declared_or_configured_capability<->surviving_runtime_evidence","required_output_schema":["capability_inventory_under_frozen_ontology","declaration_configuration_provenance","runtime_evidence","observation_limitations","completeness_accounting"],"subject":"organizational_capabilities_implemented_by_this_repository"}`

var (
	canonicalMeasurementUniverse = []string{
		MeasurementSourceSpace,
		MeasurementRuntimeEvidence,
	}
	canonicalRequiredOutputSchema = []string{
		OutputCapabilityInventory,
		OutputDeclarationConfig,
		OutputRuntimeEvidence,
		OutputLimitations,
		OutputCompleteness,
	}
)

// RefinementContract is the only input accepted by the gate. A free-text
// proposal must be transformed into this structure before evaluation. Core
// measurement sources belong in MeasurementUniverse; optional sources must be
// explicitly labeled in ObservationSupplements and cannot replace core ones.
type RefinementContract struct {
	MeasurementUniverse    []string                `json:"measurement_universe"`
	RequestedRelation      string                  `json:"requested_relation"`
	RequiredOutputSchema   []string                `json:"required_output_schema"`
	Subject                string                  `json:"subject"`
	ObservationSupplements []ObservationSupplement `json:"observation_supplements,omitempty"`
	NarrowingPredicates    []NarrowingPredicate    `json:"narrowing_predicates,omitempty"`
}

// ObservationSupplement labels a source that may add observations but never
// replace either member of the canonical measurement universe.
type ObservationSupplement struct {
	SourceID string `json:"source_id"`
	Label    string `json:"label"`
}

// NarrowingPredicateID is a reviewed, typed refinement that is known to reduce
// uncertainty inside one canonical field without changing the measurement
// target. New predicate semantics require an explicit code change and tests.
type NarrowingPredicateID string

const (
	// PredicateRuntimeEvidenceTimestamps requires timestamps where runtime
	// evidence already exposes them. It does not add or remove evidence units.
	PredicateRuntimeEvidenceTimestamps NarrowingPredicateID = "runtime_evidence_observation_timestamps_where_present"
	// PredicateResolveUnclassifiedSourceEntries narrows work to unresolved
	// members of the already registered source space and tests their relation
	// to already proposed capabilities. It changes no protected field.
	PredicateResolveUnclassifiedSourceEntries NarrowingPredicateID = "resolve_unclassified_source_entries_against_proposed_capabilities"
)

// NarrowingPredicate uses an allowlisted ID rather than arbitrary prose. This
// keeps target-preservation mechanically decidable without a model call.
type NarrowingPredicate struct {
	ID NarrowingPredicateID `json:"id"`
}

type GateStatus string

const (
	StatusIdentityPreserved   GateStatus = "ACCEPT_IDENTITY_PRESERVED"
	StatusQuestionTargetDrift GateStatus = "QUESTION_TARGET_DRIFT"
)

type GateDisposition string

const (
	DispositionAccept GateDisposition = "ACCEPT_IDENTITY_PRESERVED"
	DispositionReject GateDisposition = "REJECT_TARGET_DRIFT"
)

type ContractField string

const (
	FieldSubject                ContractField = "subject"
	FieldRequestedRelation      ContractField = "requested_relation"
	FieldMeasurementUniverse    ContractField = "measurement_universe"
	FieldRequiredOutputSchema   ContractField = "required_output_schema"
	FieldObservationSupplements ContractField = "observation_supplements"
	FieldNarrowingPredicates    ContractField = "narrowing_predicates"
	FieldSchemaVersion          ContractField = "schema_version"
	FieldContractHash           ContractField = "canonical_contract_sha256"
	FieldPayload                ContractField = "payload"
)

var fieldOrder = []ContractField{
	FieldPayload,
	FieldSchemaVersion,
	FieldContractHash,
	FieldSubject,
	FieldRequestedRelation,
	FieldMeasurementUniverse,
	FieldRequiredOutputSchema,
	FieldObservationSupplements,
	FieldNarrowingPredicates,
}

// Decision is a deterministic gate result. A rejected result deliberately has
// no normalized contract hash: rejected refinements must never be executed.
type Decision struct {
	Status                   GateStatus      `json:"status"`
	Disposition              GateDisposition `json:"disposition"`
	ChangedFields            []ContractField `json:"changed_fields,omitempty"`
	Reasons                  []string        `json:"reasons,omitempty"`
	NormalizedContractSHA256 string          `json:"normalized_contract_sha256,omitempty"`
}

// Accepted reports whether collection or measurement may proceed. Callers
// should use this method instead of inferring acceptance from missing errors.
func (d Decision) Accepted() bool {
	return d.Status == StatusIdentityPreserved && d.Disposition == DispositionAccept
}

// CanonicalContract returns a fresh copy of the frozen semantic contract.
func CanonicalContract() RefinementContract {
	return RefinementContract{
		MeasurementUniverse:  slices.Clone(canonicalMeasurementUniverse),
		RequestedRelation:    CanonicalRequestedRelation,
		RequiredOutputSchema: slices.Clone(canonicalRequiredOutputSchema),
		Subject:              CanonicalSubject,
	}
}

// Evaluate verifies the canonical contract pin and then checks every protected
// field. Configuration failure is returned as an error; semantic drift is a
// successful, explicit rejection decision.
func Evaluate(candidate RefinementContract) (Decision, error) {
	if err := verifyCanonicalContract(); err != nil {
		return Decision{}, err
	}

	changed := make(map[ContractField]bool)
	reasons := make([]string, 0)
	markChanged := func(field ContractField, reason string) {
		changed[field] = true
		reasons = append(reasons, fmt.Sprintf("%s: %s", field, reason))
	}

	if candidate.Subject != CanonicalSubject {
		markChanged(FieldSubject, "must equal the canonical subject")
	}
	if candidate.RequestedRelation != CanonicalRequestedRelation {
		markChanged(FieldRequestedRelation, "must equal the canonical bidirectional relation")
	}
	if reason := exactSetReason(candidate.MeasurementUniverse, canonicalMeasurementUniverse); reason != "" {
		markChanged(FieldMeasurementUniverse, reason+"; optional sources belong in observation_supplements")
	}
	if reason := exactRequiredOutputReason(candidate.RequiredOutputSchema, canonicalRequiredOutputSchema); reason != "" {
		markChanged(FieldRequiredOutputSchema, reason)
	}
	if reason := validateSupplements(candidate.ObservationSupplements); reason != "" {
		markChanged(FieldObservationSupplements, reason)
	}
	if reason := validatePredicates(candidate.NarrowingPredicates); reason != "" {
		markChanged(FieldNarrowingPredicates, reason)
	}

	if len(changed) != 0 {
		return Decision{
			Status:        StatusQuestionTargetDrift,
			Disposition:   DispositionReject,
			ChangedFields: orderedChangedFields(changed),
			Reasons:       reasons,
		}, nil
	}

	normalized, err := normalize(candidate)
	if err != nil {
		return Decision{}, fmt.Errorf("normalize accepted question identity contract: %w", err)
	}
	sum := sha256.Sum256(normalized)
	return Decision{
		Status:                   StatusIdentityPreserved,
		Disposition:              DispositionAccept,
		NormalizedContractSHA256: hex.EncodeToString(sum[:]),
	}, nil
}

func verifyCanonicalContract() error {
	sum := sha256.Sum256([]byte(canonicalContractJSON))
	actual := hex.EncodeToString(sum[:])
	if actual != CanonicalContractSHA256 {
		return fmt.Errorf("canonical question identity contract hash mismatch: got %s want %s", actual, CanonicalContractSHA256)
	}
	var decoded RefinementContract
	if err := json.Unmarshal([]byte(canonicalContractJSON), &decoded); err != nil {
		return fmt.Errorf("decode canonical question identity contract: %w", err)
	}
	if decoded.Subject != CanonicalSubject || decoded.RequestedRelation != CanonicalRequestedRelation {
		return errors.New("canonical question identity contract constants do not match pinned JSON")
	}
	if !slices.Equal(decoded.MeasurementUniverse, canonicalMeasurementUniverse) ||
		!slices.Equal(decoded.RequiredOutputSchema, canonicalRequiredOutputSchema) {
		return errors.New("canonical question identity contract arrays do not match pinned JSON")
	}
	return nil
}

func exactSetReason(actual, required []string) string {
	if duplicateOrBlank(actual) {
		return "must contain each canonical member exactly once and no blank members"
	}
	if len(actual) != len(required) {
		return "must contain exactly both canonical measurement-universe members"
	}
	for _, item := range required {
		if !slices.Contains(actual, item) {
			return "is missing a canonical measurement-universe member"
		}
	}
	return ""
}

func exactRequiredOutputReason(actual, required []string) string {
	if duplicateOrBlank(actual) {
		return "must contain each canonical output exactly once and no blank members"
	}
	if len(actual) != len(required) {
		return "must contain exactly the canonical output schema"
	}
	for _, item := range required {
		if !slices.Contains(actual, item) {
			return "is missing canonical output member " + item
		}
	}
	return ""
}

func duplicateOrBlank(values []string) bool {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return true
		}
		if _, ok := seen[value]; ok {
			return true
		}
		seen[value] = struct{}{}
	}
	return false
}

func validateSupplements(supplements []ObservationSupplement) string {
	seen := make(map[string]struct{}, len(supplements))
	for _, supplement := range supplements {
		if strings.TrimSpace(supplement.SourceID) == "" || strings.TrimSpace(supplement.Label) == "" {
			return "each supplement requires a nonblank source_id and explicit label"
		}
		if slices.Contains(canonicalMeasurementUniverse, supplement.SourceID) {
			return "canonical measurement sources cannot be relabeled as supplements"
		}
		if _, ok := seen[supplement.SourceID]; ok {
			return "supplement source_id values must be unique"
		}
		seen[supplement.SourceID] = struct{}{}
	}
	return ""
}

func validatePredicates(predicates []NarrowingPredicate) string {
	seen := make(map[NarrowingPredicateID]struct{}, len(predicates))
	for _, predicate := range predicates {
		if _, ok := seen[predicate.ID]; ok {
			return "narrowing predicate IDs must be unique"
		}
		seen[predicate.ID] = struct{}{}
		switch predicate.ID {
		case PredicateRuntimeEvidenceTimestamps, PredicateResolveUnclassifiedSourceEntries:
			// This reviewed predicate only increases detail inside the existing
			// runtime_evidence output field.
		default:
			return "contains an unknown or unreviewed predicate ID"
		}
	}
	return ""
}

func orderedChangedFields(changed map[ContractField]bool) []ContractField {
	result := make([]ContractField, 0, len(changed))
	for _, field := range fieldOrder {
		if changed[field] {
			result = append(result, field)
		}
	}
	return result
}

type normalizedContract struct {
	MeasurementUniverse    []string                `json:"measurement_universe"`
	RequestedRelation      string                  `json:"requested_relation"`
	RequiredOutputSchema   []string                `json:"required_output_schema"`
	Subject                string                  `json:"subject"`
	ObservationSupplements []ObservationSupplement `json:"observation_supplements,omitempty"`
	NarrowingPredicates    []NarrowingPredicate    `json:"narrowing_predicates,omitempty"`
}

func normalize(candidate RefinementContract) ([]byte, error) {
	outputs := append([]string(nil), canonicalRequiredOutputSchema...)

	supplements := slices.Clone(candidate.ObservationSupplements)
	sort.Slice(supplements, func(i, j int) bool {
		if supplements[i].SourceID == supplements[j].SourceID {
			return supplements[i].Label < supplements[j].Label
		}
		return supplements[i].SourceID < supplements[j].SourceID
	})
	predicates := slices.Clone(candidate.NarrowingPredicates)
	sort.Slice(predicates, func(i, j int) bool { return predicates[i].ID < predicates[j].ID })

	return marshalCanonicalJSON(normalizedContract{
		MeasurementUniverse:    slices.Clone(canonicalMeasurementUniverse),
		RequestedRelation:      candidate.RequestedRelation,
		RequiredOutputSchema:   outputs,
		Subject:                candidate.Subject,
		ObservationSupplements: supplements,
		NarrowingPredicates:    predicates,
	})
}

func marshalCanonicalJSON(value normalizedContract) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buffer.Bytes(), []byte{'\n'}), nil
}
