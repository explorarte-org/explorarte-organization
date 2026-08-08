package sleep

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	VerificationVerified     = "verified"
	VerificationInferred     = "inferred"
	VerificationUnknown      = "unknown"
	VerificationContradicted = "contradicted"

	BodySchemaVersion = "organizational-sleep-consolidation.v1"
	SourceBoundary    = "internal/executive/sleep"
	ProposerRoleID    = "investigacion/auditor_cerebro_empresa"
)

var digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type Experience struct {
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
}

func (e Experience) Validate() error {
	if e.RunID <= 0 || e.TaskID <= 0 || e.AttemptID <= 0 {
		return fmt.Errorf("sleep: experience ids must be positive")
	}
	for name, value := range map[string]string{
		"unit_id": e.UnitID, "role_id": e.RoleID, "provider_id": e.ProviderID, "provider_model_id": e.ProviderModelID,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("sleep: %s is required", name)
		}
	}
	if !validVerificationLabel(e.VerificationLabel) {
		return fmt.Errorf("sleep: invalid verification label %q", e.VerificationLabel)
	}
	if !digestPattern.MatchString(e.EvidenceDigest) {
		return fmt.Errorf("sleep: invalid evidence digest")
	}
	if e.DecisionHash != "" && !digestPattern.MatchString(e.DecisionHash) {
		return fmt.Errorf("sleep: invalid decision hash")
	}
	if e.ObservedAt.IsZero() {
		return fmt.Errorf("sleep: observed_at is required")
	}
	return nil
}

func validVerificationLabel(value string) bool {
	switch value {
	case VerificationVerified, VerificationInferred, VerificationUnknown, VerificationContradicted:
		return true
	default:
		return false
	}
}

func successfulLabel(value string) bool {
	return value == VerificationVerified || value == VerificationInferred
}

type GroupKey struct {
	UnitID          string `json:"unit_id"`
	RoleID          string `json:"role_id"`
	ProviderID      string `json:"provider_id"`
	ProviderModelID string `json:"provider_model_id"`
}

func (k GroupKey) String() string {
	return k.UnitID + "|" + k.RoleID + "|" + k.ProviderID + "|" + k.ProviderModelID
}

type Group struct {
	Key         GroupKey     `json:"key"`
	Experiences []Experience `json:"experiences"`
}

func (g Group) Sorted() Group {
	out := Group{Key: g.Key, Experiences: append([]Experience(nil), g.Experiences...)}
	sort.Slice(out.Experiences, func(i, j int) bool {
		if out.Experiences[i].ObservedAt.Equal(out.Experiences[j].ObservedAt) {
			return out.Experiences[i].RunID < out.Experiences[j].RunID
		}
		return out.Experiences[i].ObservedAt.Before(out.Experiences[j].ObservedAt)
	})
	return out
}

type GroupAnalysis struct {
	Key               GroupKey `json:"key"`
	Total             int      `json:"total"`
	SuccessCount      int      `json:"success_count"`
	FailureCount      int      `json:"failure_count"`
	VerifiedCount     int      `json:"verified_count"`
	InferredCount     int      `json:"inferred_count"`
	ContradictedCount int      `json:"contradicted_count"`
	UnknownCount      int      `json:"unknown_count"`
	PassRate          float64  `json:"pass_rate"`
	Contradiction     bool     `json:"contradiction"`
	Eligible          bool     `json:"eligible"`
}

type ProviderRate struct {
	ProviderID      string  `json:"provider_id"`
	ProviderModelID string  `json:"provider_model_id"`
	PassRate        float64 `json:"pass_rate"`
	Count           int     `json:"count"`
	Band            string  `json:"band"`
}

type Portability struct {
	ProvidersSeen  int            `json:"providers_seen"`
	Classification string         `json:"classification"`
	ProviderRates  []ProviderRate `json:"provider_rates"`
}

type ProposalResult struct {
	Group          GroupKey `json:"group"`
	VersionID      string   `json:"version_id"`
	DocumentID     string   `json:"document_id"`
	Reused         bool     `json:"reused"`
	Confidence     float64  `json:"confidence"`
	EvidenceRunIDs []int64  `json:"evidence_run_ids"`
}

// ProposalFailure records a candidate that failed to build or propose. Each
// group's candidate is consumed independently: one group's failure never
// rolls back, blocks, or hides another group's already-durable proposal, so
// the cycle always reports every group's true outcome instead of aborting
// on the first error.
type ProposalFailure struct {
	Group GroupKey `json:"group"`
	Error string   `json:"error"`
}

type CycleResult struct {
	WindowStart              time.Time         `json:"window_start"`
	WindowEnd                time.Time         `json:"window_end"`
	EligibleExperiences      int               `json:"eligible_experiences"`
	GroupsObserved           int               `json:"groups_observed"`
	RecurringGroups          int               `json:"recurring_groups"`
	MixedContradictionGroups int               `json:"mixed_contradiction_groups"`
	SkippedInsufficientRuns  int               `json:"skipped_insufficient_runs"`
	SkippedLowPassRate       int               `json:"skipped_low_pass_rate"`
	CandidatesProposed       int               `json:"candidates_proposed"`
	CandidatesReused         int               `json:"candidates_reused"`
	Proposals                []ProposalResult  `json:"proposals"`
	Failures                 []ProposalFailure `json:"failures"`
}

type Config struct {
	MinGroupSize     int
	RecurrenceTarget int
	MaxExperiences   int
	MaxWindow        time.Duration
	ProposerRoleID   string
}

func DefaultConfig() Config {
	return Config{
		MinGroupSize: 3, RecurrenceTarget: 8, MaxExperiences: 1024,
		MaxWindow: 365 * 24 * time.Hour, ProposerRoleID: ProposerRoleID,
	}
}

func (c Config) Validate() error {
	if c.MinGroupSize < 2 || c.MinGroupSize > 1000 {
		return fmt.Errorf("sleep: min group size outside allowed range")
	}
	if c.RecurrenceTarget < c.MinGroupSize || c.RecurrenceTarget > 10000 {
		return fmt.Errorf("sleep: recurrence target outside allowed range")
	}
	if c.MaxExperiences < c.MinGroupSize || c.MaxExperiences > 10000 {
		return fmt.Errorf("sleep: max experiences outside allowed range")
	}
	if c.MaxWindow <= 0 || c.MaxWindow > 5*365*24*time.Hour {
		return fmt.Errorf("sleep: max window outside allowed range")
	}
	if strings.TrimSpace(c.ProposerRoleID) == "" {
		return fmt.Errorf("sleep: proposer role id is required")
	}
	return nil
}
