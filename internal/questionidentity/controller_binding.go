package questionidentity

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const ControllerBindingSchemaV1 = "INSTRUMENT_V4_CONTROLLER_BINDING_V1"

// ControllerRefinementRequest is the complete language a controller-side
// refinement source may use. Free-form assignments and unknown fields fail
// closed; accepted tasks are generated below rather than copied from a model.
type ControllerRefinementRequest struct {
	SchemaVersion           string               `json:"schema_version"`
	CanonicalContractSHA256 string               `json:"canonical_contract_sha256"`
	QuestionID              string               `json:"question_id"`
	RefinementProfile       NarrowingPredicateID `json:"refinement_profile"`
}

type AuthorizedRefinementScope struct {
	Rules []string `json:"rules"`
}

// AuthorizedRefinementTask is the only task representation returned to the
// historical controller. Every behavioral field is owned by this versioned
// component, not by candidate prose.
type AuthorizedRefinementTask struct {
	AssignmentID   string                    `json:"assignment_id"`
	QuestionID     string                    `json:"question_id"`
	Owner          string                    `json:"owner"`
	Priority       string                    `json:"priority"`
	Objective      string                    `json:"objective"`
	Scope          AuthorizedRefinementScope `json:"scope"`
	Deliverable    []string                  `json:"deliverable"`
	AcceptanceTest string                    `json:"acceptance_test"`
}

type ControllerBindingOutcome struct {
	Decision                Decision                  `json:"decision"`
	ProviderCallAllowed     bool                      `json:"provider_call_allowed"`
	AuthorizedTask          *AuthorizedRefinementTask `json:"authorized_task,omitempty"`
	CandidatePayloadSHA256  string                    `json:"candidate_payload_sha256"`
	CanonicalContractSHA256 string                    `json:"canonical_contract_sha256"`
	ControllerBindingSchema string                    `json:"controller_binding_schema"`
}

// BindControllerPayload validates a candidate and, only for an accepted
// allowlisted profile, returns a deterministic task that the controller may
// pass to its existing provider path.
func BindControllerPayload(payload []byte) (ControllerBindingOutcome, error) {
	sum := sha256.Sum256(payload)
	outcome := ControllerBindingOutcome{
		CandidatePayloadSHA256:  hex.EncodeToString(sum[:]),
		CanonicalContractSHA256: CanonicalContractSHA256,
		ControllerBindingSchema: ControllerBindingSchemaV1,
	}

	request, err := decodeControllerRequest(payload)
	if err != nil {
		outcome.Decision = rejectFields([]ContractField{FieldPayload}, "payload: "+err.Error())
		return outcome, nil
	}

	contract := CanonicalContract()
	contract.NarrowingPredicates = []NarrowingPredicate{{ID: request.RefinementProfile}}
	decision, err := Evaluate(contract)
	if err != nil {
		return ControllerBindingOutcome{}, err
	}
	changed := append([]ContractField(nil), decision.ChangedFields...)
	reasons := append([]string(nil), decision.Reasons...)
	if request.SchemaVersion != ControllerBindingSchemaV1 {
		changed = append(changed, FieldSchemaVersion)
		reasons = append(reasons, "schema_version: must equal the supported controller-binding schema")
	}
	if request.CanonicalContractSHA256 != CanonicalContractSHA256 {
		changed = append(changed, FieldContractHash)
		reasons = append(reasons, "canonical_contract_sha256: must equal the frozen question identity contract hash")
	}
	if strings.TrimSpace(request.QuestionID) == "" {
		changed = append(changed, FieldPayload)
		reasons = append(reasons, "question_id: must be nonblank")
	}
	if len(changed) != 0 {
		outcome.Decision = rejectFields(changed, reasons...)
		return outcome, nil
	}

	task, ok := authorizedTask(request.QuestionID, request.RefinementProfile)
	if !ok {
		outcome.Decision = rejectFields(
			[]ContractField{FieldNarrowingPredicates},
			"narrowing_predicates: profile has no controller-owned task template",
		)
		return outcome, nil
	}
	outcome.Decision = decision
	outcome.ProviderCallAllowed = true
	outcome.AuthorizedTask = &task
	return outcome, nil
}

func decodeControllerRequest(payload []byte) (ControllerRefinementRequest, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var request ControllerRefinementRequest
	if err := decoder.Decode(&request); err != nil {
		return ControllerRefinementRequest{}, fmt.Errorf("decode controller refinement request: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return ControllerRefinementRequest{}, errors.New("controller refinement request contains trailing JSON")
		}
		return ControllerRefinementRequest{}, fmt.Errorf("decode trailing controller refinement data: %w", err)
	}
	return request, nil
}

func authorizedTask(questionID string, profile NarrowingPredicateID) (AuthorizedRefinementTask, bool) {
	switch profile {
	case PredicateResolveUnclassifiedSourceEntries:
		return AuthorizedRefinementTask{
			AssignmentID: "Q3-002-RESOLVE-UNCLASSIFIED-SOURCE-ENTRIES",
			QuestionID:   questionID,
			Owner:        "RUNNER",
			Priority:     "P1",
			Objective:    "Resolve unclassified entries in the deterministically registered source space against already proposed organizational capabilities under the frozen ontology.",
			Scope: AuthorizedRefinementScope{Rules: []string{
				"Inspect only source-space entries already registered by the measurement procedure.",
				"Relate each entry to an already proposed capability, prove it irrelevant, or leave it unresolved.",
				"Do not introduce a mechanism count, literal-phrase test, new subject, new relation, new universe, or new output field.",
			}},
			Deliverable: []string{
				"Deterministic classifications for the selected unresolved source-space entries.",
				"File and symbol provenance for every resolved or irrelevant classification.",
			},
			AcceptanceTest: "Every selected entry remains inside the frozen source space and ends as relevant capability evidence, irrelevant-with-proof, or unresolved without changing the canonical output schema.",
		}, true
	case PredicateRuntimeEvidenceTimestamps:
		return AuthorizedRefinementTask{
			AssignmentID: "Q3-002-RUNTIME-EVIDENCE-TIMESTAMPS",
			QuestionID:   questionID,
			Owner:        "RUNNER",
			Priority:     "P1",
			Objective:    "Record observation timestamps where already selected surviving runtime evidence exposes them.",
			Scope: AuthorizedRefinementScope{Rules: []string{
				"Inspect only runtime evidence already inside the frozen measurement universe.",
				"Add timestamps only where present; absence of a timestamp is an observation limitation.",
				"Do not add evidence sources or alter capability identity.",
			}},
			Deliverable: []string{
				"Timestamp annotations for already selected runtime evidence.",
				"Explicit limitations where timestamps are unavailable.",
			},
			AcceptanceTest: "Every timestamp annotates an existing runtime-evidence record and no source, capability, or output field is added.",
		}, true
	default:
		return AuthorizedRefinementTask{}, false
	}
}
