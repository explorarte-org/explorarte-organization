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
	if out.SchemaVersion == WorkerResultSchemaVersionV2 {
		// One authority, not two.
		//
		// Deriving the flat list from the structured items while still
		// accepting whatever the model put in evidence_refs left the old
		// bag of untyped citations reachable through a side door: an
		// artifact could ground one claim structurally and hand four more
		// references to downstream verification with no relation, no
		// subject and no claim attached. Every citation a v2 artifact
		// offers must be one that some claim rests on, or the topology
		// this version exists to preserve is optional in practice.
		if err := refsAreAllStructured(out.EvidenceRefs, out.Evidence); err != nil {
			return WorkerResult{}, err
		}
		out.EvidenceRefs = structuredRefs(out.Evidence)
	}
	return out, nil
}

// workerResultV2StructureGuidance states worker-result/v2's document
// topology to every producer asked for it, independent of whether this
// round has any EvidenceRequirements at all.
//
// refsAreAllStructured (above) enforces this cross-field rule
// unconditionally -- it never reads EvidenceRequirements, because
// EvidenceRequirements decide WHAT evidence is owed, not whether a v2
// document may contradict its own structured representation. Before
// AUTONOMY-SMOKE-017-R17-V3, a zero-requirement worker was bound by that
// rule without ever being told it existed: evidenceContractGuidance is
// correctly silent when required is empty (AUTONOMY-SMOKE-017-R8), and
// nothing else stated it. The worker guessed across three attempts: a
// real canonical document with no evidence[] entry behind it, then a
// self-citation to its own task, then a repository:// URI composed to
// look like one it had never been shown.
//
// This is deliberately independent of evidenceContractGuidance, which
// states evidence OBLIGATIONS (what must be grounded). This states
// STRUCTURE (how a v2 document must be shaped), and is true whether or
// not any obligation exists.
func workerResultV2StructureGuidance() string {
	return `worker-result/v2 document structure:
- evidence[] is the structured authority: each item states a claim, a subject, a relation and a ref.
- every non-empty evidence_refs entry must also occur as some evidence[].ref.
- if you have no evidence to cite, return evidence_refs: [] and evidence: [].
- never invent a reference (a task ID, a self-citation, a composed repository:// URI, a worker-result citation) merely to populate these arrays; report only evidence that actually grounds a claim.`
}

func refsAreAllStructured(refs []string, evidence []EvidenceItem) error {
	structured := make(map[string]struct{}, len(evidence))
	for _, item := range evidence {
		structured[strings.TrimSpace(item.Ref)] = struct{}{}
	}
	for _, ref := range refs {
		if _, grounded := structured[strings.TrimSpace(ref)]; !grounded {
			return fmt.Errorf("%w: evidence_refs carries %s, which grounds no claim", ErrContractRejected, ref)
		}
	}
	return nil
}

// structuredRefs is what downstream verification sees: exactly the citations
// some claim rests on, in the order they were offered.
func structuredRefs(evidence []EvidenceItem) []string {
	seen := make(map[string]struct{}, len(evidence))
	refs := make([]string, 0, len(evidence))
	for _, item := range evidence {
		ref := strings.TrimSpace(item.Ref)
		if ref == "" {
			continue
		}
		if _, already := seen[ref]; already {
			continue
		}
		seen[ref] = struct{}{}
		refs = append(refs, ref)
	}
	return refs
}
