package questionidentity

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const RefinementEnvelopeSchemaV1 = "INSTRUMENT_V4_REFINEMENT_CONTRACT_V1"

// RefinementEnvelope binds a candidate to the input schema and the frozen
// semantic contract. The hash is a required compatibility assertion, not a
// value supplied by a model as evidence of correctness.
type RefinementEnvelope struct {
	SchemaVersion           string             `json:"schema_version"`
	CanonicalContractSHA256 string             `json:"canonical_contract_sha256"`
	Contract                RefinementContract `json:"contract"`
}

func CanonicalEnvelope() RefinementEnvelope {
	return RefinementEnvelope{
		SchemaVersion:           RefinementEnvelopeSchemaV1,
		CanonicalContractSHA256: CanonicalContractSHA256,
		Contract:                CanonicalContract(),
	}
}

// RefinementSource supplies one serialized candidate. Keeping this boundary
// byte-oriented ensures malformed or unknown schemas are rejected before any
// model adapter can see them.
type RefinementSource interface {
	NextRefinement(context.Context) ([]byte, error)
}

// ModelAdapter executes an already authorized refinement. Implementations must
// not be passed to any code path that can bypass ExecutionPath.RunNext.
type ModelAdapter interface {
	ExecuteRefinement(context.Context, RefinementContract, string) error
}

// DriftRecorder persists or emits a deterministic rejection record. It
// receives a payload hash rather than raw candidate prose.
type DriftRecorder interface {
	RecordQuestionTargetDrift(context.Context, DriftRecord) error
}

type DriftRecord struct {
	Decision               Decision `json:"decision"`
	CandidatePayloadSHA256 string   `json:"candidate_payload_sha256"`
}

type ExecutionOutcome struct {
	Decision           Decision `json:"decision"`
	ModelCallAllowed   bool     `json:"model_call_allowed"`
	ModelCallCompleted bool     `json:"model_call_completed"`
}

// ExecutionPath is the only component in this package that can invoke a model
// adapter. Its ordering is fixed: source -> strict decode -> identity gate ->
// drift record or model call.
type ExecutionPath struct {
	source   RefinementSource
	model    ModelAdapter
	recorder DriftRecorder
}

func NewExecutionPath(source RefinementSource, model ModelAdapter, recorder DriftRecorder) (*ExecutionPath, error) {
	if source == nil {
		return nil, errors.New("question identity execution path requires a refinement source")
	}
	if model == nil {
		return nil, errors.New("question identity execution path requires a model adapter")
	}
	if recorder == nil {
		return nil, errors.New("question identity execution path requires a drift recorder")
	}
	return &ExecutionPath{source: source, model: model, recorder: recorder}, nil
}

func (path *ExecutionPath) RunNext(ctx context.Context) (ExecutionOutcome, error) {
	if path == nil {
		return ExecutionOutcome{}, errors.New("question identity execution path is nil")
	}
	payload, err := path.source.NextRefinement(ctx)
	if err != nil {
		return ExecutionOutcome{}, fmt.Errorf("read candidate refinement: %w", err)
	}
	decision, contract, err := EvaluatePayload(payload)
	if err != nil {
		return ExecutionOutcome{}, fmt.Errorf("evaluate candidate refinement: %w", err)
	}
	outcome := ExecutionOutcome{Decision: decision}
	if !decision.Accepted() {
		sum := sha256.Sum256(payload)
		record := DriftRecord{
			Decision:               decision,
			CandidatePayloadSHA256: hex.EncodeToString(sum[:]),
		}
		if err := path.recorder.RecordQuestionTargetDrift(ctx, record); err != nil {
			return outcome, fmt.Errorf("record rejected question refinement: %w", err)
		}
		return outcome, nil
	}

	outcome.ModelCallAllowed = true
	if err := path.model.ExecuteRefinement(ctx, contract, decision.NormalizedContractSHA256); err != nil {
		return outcome, fmt.Errorf("execute accepted question refinement: %w", err)
	}
	outcome.ModelCallCompleted = true
	return outcome, nil
}

// EvaluatePayload strictly decodes one envelope and evaluates all protected
// fields. Parse failures, unknown fields, trailing JSON, unknown versions and
// contract-hash mismatches are semantic rejections, never bypasses.
func EvaluatePayload(payload []byte) (Decision, RefinementContract, error) {
	envelope, err := decodeEnvelope(payload)
	if err != nil {
		return rejectFields([]ContractField{FieldPayload}, "payload: "+err.Error()), RefinementContract{}, nil
	}

	decision, err := Evaluate(envelope.Contract)
	if err != nil {
		return Decision{}, RefinementContract{}, err
	}
	changed := make([]ContractField, 0, len(decision.ChangedFields)+2)
	reasons := make([]string, 0, len(decision.Reasons)+2)
	if envelope.SchemaVersion != RefinementEnvelopeSchemaV1 {
		changed = append(changed, FieldSchemaVersion)
		reasons = append(reasons, "schema_version: must equal the supported INSTRUMENT_V4 refinement schema")
	}
	if envelope.CanonicalContractSHA256 != CanonicalContractSHA256 {
		changed = append(changed, FieldContractHash)
		reasons = append(reasons, "canonical_contract_sha256: must equal the frozen question identity contract hash")
	}
	changed = append(changed, decision.ChangedFields...)
	reasons = append(reasons, decision.Reasons...)
	if len(changed) != 0 {
		return rejectFields(changed, reasons...), RefinementContract{}, nil
	}
	return decision, envelope.Contract, nil
}

func decodeEnvelope(payload []byte) (RefinementEnvelope, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var envelope RefinementEnvelope
	if err := decoder.Decode(&envelope); err != nil {
		return RefinementEnvelope{}, fmt.Errorf("decode structured refinement envelope: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return RefinementEnvelope{}, errors.New("refinement envelope contains trailing JSON")
		}
		return RefinementEnvelope{}, fmt.Errorf("decode trailing refinement data: %w", err)
	}
	return envelope, nil
}

func rejectFields(fields []ContractField, reasons ...string) Decision {
	changed := make(map[ContractField]bool, len(fields))
	for _, field := range fields {
		changed[field] = true
	}
	return Decision{
		Status:        StatusQuestionTargetDrift,
		Disposition:   DispositionReject,
		ChangedFields: orderedChangedFields(changed),
		Reasons:       reasons,
	}
}
