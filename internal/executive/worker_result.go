package executive

import (
	"fmt"
	"strings"
)

const (
	WorkerResultSchemaVersion   = "worker-result/v1"
	WorkerResultSchemaVersionV2 = "worker-result/v2"
)

// Evidence relations a worker can assert about a citation.
//
// AUTONOMY-SMOKE-017-R5 was told, in its instructions and in its acceptance
// criteria, to give separate references for where each limit is DEFINED and
// where it is APPLIED. It did exactly that -- in prose, inside summary --
// because worker-result/v1 has nowhere else to put it: evidence_refs is a flat
// list of strings, so the relation between a claim and the citation that
// grounds it cannot survive as data. The adjudicator received a bag of
// references, could not tell which was which, and asked for the same thing
// again. Two design rounds were spent on a topology the artifact could not
// express.
const (
	EvidenceDefinition  = "definition"
	EvidenceApplication = "application"
	EvidenceTest        = "test"
	EvidenceContext     = "context"
)

func validEvidenceRelation(relation string) bool {
	switch relation {
	case EvidenceDefinition, EvidenceApplication, EvidenceTest, EvidenceContext:
		return true
	}
	return false
}

// EvidenceItem binds one claim to the citation that grounds it and says what
// role that citation plays. The subject is what the claim is ABOUT -- usually
// the symbol a goal named -- so the host can check coverage per subject
// without interpreting prose.
type EvidenceItem struct {
	Claim    string `json:"claim"`
	Subject  string `json:"subject"`
	Relation string `json:"relation"`
	Ref      string `json:"ref"`
}

type WorkerResult struct {
	SchemaVersion string         `json:"schema_version"`
	Summary       string         `json:"summary"`
	EvidenceRefs  []string       `json:"evidence_refs"`
	Evidence      []EvidenceItem `json:"evidence"`
}

func ParseWorkerResult(body []byte, limits Limits) (WorkerResult, error) {
	var out WorkerResult
	if err := decodeStrictModelJSON(body, &out, limits); err != nil {
		return WorkerResult{}, err
	}
	if out.SchemaVersion != WorkerResultSchemaVersion && out.SchemaVersion != WorkerResultSchemaVersionV2 {
		return WorkerResult{}, fmt.Errorf("%w: schema_version", ErrContractRejected)
	}
	if err := validateRequiredString(out.Summary, limits.MaxStringBytes, "summary"); err != nil {
		return WorkerResult{}, err
	}
	if err := validateStrings(out.EvidenceRefs, limits, "evidence_refs"); err != nil {
		return WorkerResult{}, err
	}
	if out.SchemaVersion == WorkerResultSchemaVersion && len(out.Evidence) > 0 {
		return WorkerResult{}, fmt.Errorf("%w: evidence requires %s", ErrContractRejected, WorkerResultSchemaVersionV2)
	}
	if len(out.Evidence) > limits.MaxArrayItems {
		return WorkerResult{}, fmt.Errorf("%w: evidence", ErrContractRejected)
	}
	for index, item := range out.Evidence {
		if err := validateRequiredString(item.Claim, limits.MaxStringBytes, "evidence.claim"); err != nil {
			return WorkerResult{}, err
		}
		if err := validateRequiredString(item.Subject, limits.MaxStringBytes, "evidence.subject"); err != nil {
			return WorkerResult{}, err
		}
		if err := validateRequiredString(item.Ref, limits.MaxStringBytes, "evidence.ref"); err != nil {
			return WorkerResult{}, err
		}
		if !validEvidenceRelation(item.Relation) {
			return WorkerResult{}, fmt.Errorf("%w: evidence[%d].relation", ErrContractRejected, index)
		}
	}
	// Downstream verification reads evidence_refs, and a v2 artifact must
	// not have to state every citation twice. The structured items are the
	// authority; the flat list is derived from them so nothing that grounds
	// a claim can be missing from what gets verified.
	out.EvidenceRefs = mergeEvidenceRefs(out.EvidenceRefs, out.Evidence)
	return out, nil
}

func mergeEvidenceRefs(refs []string, evidence []EvidenceItem) []string {
	seen := make(map[string]struct{}, len(refs)+len(evidence))
	merged := make([]string, 0, len(refs)+len(evidence))
	for _, ref := range refs {
		if _, already := seen[ref]; already {
			continue
		}
		seen[ref] = struct{}{}
		merged = append(merged, ref)
	}
	for _, item := range evidence {
		ref := strings.TrimSpace(item.Ref)
		if ref == "" {
			continue
		}
		if _, already := seen[ref]; already {
			continue
		}
		seen[ref] = struct{}{}
		merged = append(merged, ref)
	}
	return merged
}
