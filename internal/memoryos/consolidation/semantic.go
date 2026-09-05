package consolidation

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/memoryos/episode"
	"github.com/Mireuz13/explorarte-organization/internal/rag"
)

type SemanticSource struct {
	HarnessRunID   string `json:"harness_run_id"`
	DecisionRunID  int64  `json:"decision_run_id"`
	Verification   string `json:"verification"`
	EvidenceDigest string `json:"evidence_digest"`
	ObservedAt     string `json:"observed_at"`
}

type SemanticCandidateBody struct {
	SchemaVersion   string           `json:"schema_version"`
	Group           SemanticGroupKey `json:"group"`
	RecurrenceCount int              `json:"recurrence_count"`
	SuccessCount    int              `json:"success_count"`
	FailureCount    int              `json:"failure_count"`
	BindingMode     string           `json:"binding_mode"`
	Claim           string           `json:"claim"`
	Applicability   []string         `json:"applicability_conditions"`
	Sources         []SemanticSource `json:"sources"`
}

// BuildSemanticCandidate creates a metadata-only RAG candidate from positive,
// obligation-verified episodes. It never includes context/prompt bytes or
// provider claims; mixed provider/model bindings are represented explicitly as
// mixed and therefore cannot be attributed to one provider.
func BuildSemanticCandidate(organizationID string, key SemanticGroupKey, episodes []episode.Episode) (rag.ProposeRequest, []int64, error) {
	if strings.TrimSpace(organizationID) == "" || len(episodes) == 0 {
		return rag.ProposeRequest{}, nil, fmt.Errorf("memoryos: semantic candidate requires organization and episodes")
	}
	members := uniqueEpisodes(episodes)
	if len(members) == 0 {
		return rag.ProposeRequest{}, nil, fmt.Errorf("memoryos: semantic candidate has no unique episodes")
	}
	for _, current := range members {
		if !positive(current) || current.DecisionRunID == nil || *current.DecisionRunID <= 0 {
			return rag.ProposeRequest{}, nil, fmt.Errorf("memoryos: semantic candidate contains an ineligible episode %s", current.HarnessRunID)
		}
		if evidenceDigest(current) == "" {
			return rag.ProposeRequest{}, nil, fmt.Errorf("memoryos: semantic episode %s has no evidence digest", current.HarnessRunID)
		}
	}
	sort.Slice(members, func(i, j int) bool { return members[i].HarnessRunID < members[j].HarnessRunID })
	binding := BindingModeHomogeneous
	for _, current := range members {
		mode := bindingMode(current)
		if mode == BindingModeMixed {
			binding = BindingModeMixed
			break
		}
		if mode == BindingModeUnknown && binding != BindingModeMixed {
			binding = BindingModeUnknown
		}
	}
	sources := make([]SemanticSource, 0, len(members))
	refs := make([]rag.EvidenceRef, 0, len(members))
	decisionIDs := make([]int64, 0, len(members))
	latest := members[0]
	for _, current := range members {
		decisionID := *current.DecisionRunID
		digest := evidenceDigest(current)
		label := "pass"
		if current.Verification != nil {
			label = current.Verification.Verdict
		}
		sources = append(sources, SemanticSource{HarnessRunID: current.HarnessRunID, DecisionRunID: decisionID, Verification: label, EvidenceDigest: digest, ObservedAt: observedAt(current).UTC().Format("2006-01-02T15:04:05.999999Z07:00")})
		refs = append(refs, rag.EvidenceRef{Reference: fmt.Sprintf("decisiongraph:run:%d", decisionID), Digest: digest})
		decisionIDs = append(decisionIDs, decisionID)
		if newer(current, latest) {
			latest = current
		}
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].DecisionRunID < sources[j].DecisionRunID })
	sort.Slice(refs, func(i, j int) bool { return refs[i].Reference < refs[j].Reference })
	sort.Slice(decisionIDs, func(i, j int) bool { return decisionIDs[i] < decisionIDs[j] })
	evidenceHash := digestJSON(struct {
		Group SemanticGroupKey `json:"group"`
		Runs  []int64          `json:"runs"`
	}{key, decisionIDs})
	groupHash := digestJSON(struct {
		OrganizationID string           `json:"organization_id"`
		Group          SemanticGroupKey `json:"group"`
	}{organizationID, key})
	id := "memoryos-semantic-" + groupHash[:24] + "-" + evidenceHash[:24]
	body := SemanticCandidateBody{
		SchemaVersion:   SemanticSchemaVersion,
		Group:           key,
		RecurrenceCount: len(members),
		SuccessCount:    len(members),
		FailureCount:    0,
		BindingMode:     binding,
		Claim:           fmt.Sprintf("Observed verified completion pattern for role %s, task class %s, and execution profile %s across %d Harness runs.", key.RoleID, key.TaskClass, key.ExecutionProfileID, len(members)),
		Applicability:   []string{"role_id=" + key.RoleID, "task_class=" + key.TaskClass, "execution_profile_id=" + key.ExecutionProfileID, "binding_mode=" + binding},
		Sources:         sources,
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return rag.ProposeRequest{}, nil, err
	}
	firstRef := refs[0].Reference
	proposer := key.RoleID
	request := rag.ProposeRequest{
		Command: rag.ProposeCommand{
			ID: id, DocumentID: id, OrganizationID: organizationID,
			NamespaceKind: rag.NamespaceOwn, NamespaceID: key.RoleID, Version: 1,
			Title: "MemoryOS observed semantic pattern " + evidenceHash[:16], Body: string(bodyBytes),
			SourceKind: rag.SourceOperational, SourceReference: "memoryos:semantic:" + groupHash,
			SourceRunRef: fmt.Sprintf("decisiongraph:run:%d", *latest.DecisionRunID), EvidenceRefs: refs,
			ProposedBy: proposer,
			Admission:  rag.AdmissionAttestation{DataClass: rag.DataOrganizational, AttestedBy: proposer, SourceBoundary: SourceBoundary, EvidenceRef: firstRef, AttestedAt: observedAt(latest)},
		},
		IdempotencyKey: "memoryos:semantic:" + groupHash + ":" + evidenceHash,
	}
	return request, decisionIDs, nil
}

var _ = episode.BindingModeHomogeneous
