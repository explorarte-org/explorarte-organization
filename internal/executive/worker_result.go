package executive

import "fmt"

const WorkerResultSchemaVersion = "worker-result/v1"

type WorkerResult struct {
	SchemaVersion string   `json:"schema_version"`
	Summary       string   `json:"summary"`
	EvidenceRefs  []string `json:"evidence_refs"`
}

func ParseWorkerResult(body []byte, limits Limits) (WorkerResult, error) {
	var out WorkerResult
	if err := decodeStrictModelJSON(body, &out, limits); err != nil {
		return WorkerResult{}, err
	}
	if out.SchemaVersion != WorkerResultSchemaVersion {
		return WorkerResult{}, fmt.Errorf("%w: schema_version", ErrContractRejected)
	}
	if err := validateRequiredString(out.Summary, limits.MaxStringBytes, "summary"); err != nil {
		return WorkerResult{}, err
	}
	if err := validateStrings(out.EvidenceRefs, limits, "evidence_refs"); err != nil {
		return WorkerResult{}, err
	}
	return out, nil
}
