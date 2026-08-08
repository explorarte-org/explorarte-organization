package sleep

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/rag"
)

type CandidateBody struct {
	SchemaVersion           string            `json:"schema_version"`
	Claim                   string            `json:"claim"`
	ApplicabilityConditions []string          `json:"applicability_conditions"`
	Group                   GroupKey          `json:"group"`
	RecurrenceCount         int               `json:"recurrence_count"`
	SuccessCount            int               `json:"success_count"`
	FailureCount            int               `json:"failure_count"`
	VerifiedCount           int               `json:"verified_count"`
	InferredCount           int               `json:"inferred_count"`
	ContradictedCount       int               `json:"contradicted_count"`
	UnknownCount            int               `json:"unknown_count"`
	PassRate                float64           `json:"pass_rate"`
	Confidence              float64           `json:"confidence"`
	ConfidenceTerms         ConfidenceTerms   `json:"confidence_terms"`
	Contradiction           bool              `json:"contradiction"`
	Portability             Portability       `json:"portability"`
	Sources                 []CandidateSource `json:"sources"`
}

type ConfidenceTerms struct {
	RecurrenceFactor     float64 `json:"recurrence_factor"`
	PassRate             float64 `json:"pass_rate"`
	ProviderMultiplier   float64 `json:"provider_multiplier"`
	ContradictionPenalty float64 `json:"contradiction_penalty"`
}

type CandidateSource struct {
	RunID             int64     `json:"run_id"`
	TaskID            int64     `json:"task_id"`
	AttemptID         int64     `json:"attempt_id"`
	UnitID            string    `json:"unit_id"`
	RoleID            string    `json:"role_id"`
	ProviderID        string    `json:"provider_id"`
	ProviderModelID   string    `json:"provider_model_id"`
	VerificationLabel string    `json:"verification_label"`
	EvidenceDigest    string    `json:"evidence_digest"`
	DecisionHash      string    `json:"decision_hash,omitempty"`
	ObservedAt        time.Time `json:"observed_at"`
	PrimaryGroup      bool      `json:"primary_group"`
}

type BuiltCandidate struct {
	Request        rag.ProposeRequest
	Confidence     float64
	EvidenceRunIDs []int64
}

func BuildCandidate(primary Group, recurring []Group, analysis GroupAnalysis, config Config) (BuiltCandidate, error) {
	if !analysis.Eligible {
		return BuiltCandidate{}, fmt.Errorf("sleep: group %s is not eligible for consolidation", primary.Key.String())
	}
	portability, evidence := PortabilityFor(primary, recurring, config.MinGroupSize)
	if len(evidence) == 0 {
		evidence = primary.Sorted().Experiences
		portability = Portability{
			ProvidersSeen: 1, Classification: "single_provider_observation",
			ProviderRates: []ProviderRate{{ProviderID: primary.Key.ProviderID, PassRate: analysis.PassRate, Count: analysis.Total, Band: passBand(analysis.PassRate)}},
		}
	}

	confidence := Confidence(analysis.Total, config.RecurrenceTarget, analysis.PassRate, portability.ProvidersSeen, analysis.Contradiction)
	recurrenceFactor := round6(float64(analysis.Total) / float64(config.RecurrenceTarget))
	if recurrenceFactor > 1 {
		recurrenceFactor = 1
	}
	providerMultiplier := round6(1 + 0.1*float64(minInt(maxInt(portability.ProvidersSeen-1, 0), 3)))
	penalty := 0.0
	if analysis.Contradiction {
		penalty = 0.15
	}

	sources := make([]CandidateSource, 0, len(evidence))
	refs := make([]rag.EvidenceRef, 0, len(evidence))
	runIDs := make([]int64, 0, len(evidence))
	latestObserved := time.Time{}
	for _, experience := range evidence {
		primaryMember := experience.UnitID == primary.Key.UnitID && experience.RoleID == primary.Key.RoleID && experience.ProviderID == primary.Key.ProviderID
		sources = append(sources, CandidateSource{
			RunID: experience.RunID, TaskID: experience.TaskID, AttemptID: experience.AttemptID,
			UnitID: experience.UnitID, RoleID: experience.RoleID, ProviderID: experience.ProviderID, ProviderModelID: experience.ProviderModelID,
			VerificationLabel: experience.VerificationLabel, EvidenceDigest: experience.EvidenceDigest, DecisionHash: experience.DecisionHash,
			ObservedAt: experience.ObservedAt.UTC(), PrimaryGroup: primaryMember,
		})
		refs = append(refs, rag.EvidenceRef{Reference: fmt.Sprintf("decisiongraph:run:%d", experience.RunID), Digest: experience.EvidenceDigest})
		runIDs = append(runIDs, experience.RunID)
		if latestObserved.IsZero() || experience.ObservedAt.After(latestObserved) {
			latestObserved = experience.ObservedAt.UTC()
		}
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].RunID < sources[j].RunID })
	sort.Slice(refs, func(i, j int) bool { return refs[i].Reference < refs[j].Reference })
	sort.Slice(runIDs, func(i, j int) bool { return runIDs[i] < runIDs[j] })

	claim := fmt.Sprintf("Observed completion pattern for %s in %s via %s: %d/%d successful verified-or-inferred outcomes (pass_rate=%.6f).",
		primary.Key.RoleID, primary.Key.UnitID, primary.Key.ProviderID, analysis.SuccessCount, analysis.Total, analysis.PassRate)
	conditions := []string{
		"unit_id=" + primary.Key.UnitID,
		"role_id=" + primary.Key.RoleID,
		"provider_id=" + primary.Key.ProviderID,
	}
	if analysis.Contradiction {
		conditions = append(conditions, "mixed_outcomes_observed=true; this is not an unconditional success claim")
	}
	switch portability.Classification {
	case "provider_dependent":
		conditions = append(conditions, "provider_dependent=true; do not generalize across providers")
	case "consistent_eligibility_band_across_providers":
		conditions = append(conditions, fmt.Sprintf("recurrent_provider_bands_consistent=%d; transferability is observational, not causal", portability.ProvidersSeen))
	}

	body := CandidateBody{
		SchemaVersion: BodySchemaVersion, Claim: claim, ApplicabilityConditions: conditions, Group: primary.Key,
		RecurrenceCount: analysis.Total, SuccessCount: analysis.SuccessCount, FailureCount: analysis.FailureCount,
		VerifiedCount: analysis.VerifiedCount, InferredCount: analysis.InferredCount, ContradictedCount: analysis.ContradictedCount, UnknownCount: analysis.UnknownCount,
		PassRate: analysis.PassRate, Confidence: confidence,
		ConfidenceTerms: ConfidenceTerms{RecurrenceFactor: recurrenceFactor, PassRate: analysis.PassRate, ProviderMultiplier: providerMultiplier, ContradictionPenalty: penalty},
		Contradiction:   analysis.Contradiction, Portability: portability, Sources: sources,
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return BuiltCandidate{}, fmt.Errorf("sleep: marshal candidate body: %w", err)
	}
	if len(bodyBytes) > 1<<20 {
		return BuiltCandidate{}, fmt.Errorf("sleep: candidate body exceeds RAG body limit")
	}

	groupHash := sha256Hex(primary.Key.String())
	evidenceHash := evidenceSetHash(evidence)
	documentID := "sleep-" + groupHash[:16] + "-" + evidenceHash[:16]
	versionID := documentID + "-v1"
	title := compactTitle("Observed completion pattern: "+primary.Key.RoleID+" via "+primary.Key.ProviderID, groupHash)
	sourceReference := "organizational-sleep:evidence-set:" + evidenceHash
	evidenceRef := "decisiongraph:evidence-set:" + evidenceHash

	request := rag.ProposeRequest{
		Command: rag.ProposeCommand{
			ID: versionID, DocumentID: documentID, OrganizationID: "",
			NamespaceKind: rag.NamespaceDepartment, NamespaceID: primary.Key.UnitID, Version: 1,
			Title: title, Body: string(bodyBytes), SourceKind: rag.SourceOperational,
			SourceReference: sourceReference, SourceRunRef: evidenceRef, EvidenceRefs: refs,
			ProposedBy: strings.TrimSpace(config.ProposerRoleID),
			Admission: rag.AdmissionAttestation{
				DataClass: rag.DataOrganizational, AttestedBy: strings.TrimSpace(config.ProposerRoleID),
				SourceBoundary: SourceBoundary, EvidenceRef: evidenceRef, AttestedAt: latestObserved,
			},
		},
		// Cross-provider portability can make two primary groups share the same
		// evidence set. Commit both the primary group and evidence set to the
		// idempotency key so different claims cannot collide under one key.
		IdempotencyKey: "sleep:" + groupHash + ":" + evidenceHash,
	}
	return BuiltCandidate{Request: request, Confidence: confidence, EvidenceRunIDs: runIDs}, nil
}

func evidenceSetHash(experiences []Experience) string {
	values := append([]Experience(nil), experiences...)
	sort.Slice(values, func(i, j int) bool { return values[i].RunID < values[j].RunID })
	var b strings.Builder
	b.WriteString("organizational-sleep-evidence.v1\n")
	for _, experience := range values {
		fmt.Fprintf(&b, "%d|%d|%d|%s|%s|%s|%s|%s\n", experience.RunID, experience.TaskID, experience.AttemptID,
			experience.UnitID, experience.RoleID, experience.ProviderID, experience.VerificationLabel, experience.EvidenceDigest)
	}
	return sha256Hex(b.String())
}

func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func compactTitle(value, fallbackHash string) string {
	value = strings.TrimSpace(value)
	if len(value) <= 240 {
		return value
	}
	return "Observed completion pattern " + fallbackHash[:24]
}
