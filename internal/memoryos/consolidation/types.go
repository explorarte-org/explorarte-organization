package consolidation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/memory"
	"github.com/Mireuz13/explorarte-organization/internal/memoryos/episode"
	"github.com/Mireuz13/explorarte-organization/internal/rag"
)

const (
	BindingModeHomogeneous = string(episode.BindingModeHomogeneous)
	BindingModeMixed       = string(episode.BindingModeMixed)
	BindingModeUnknown     = "unknown"

	VerificationVerified     = "verified"
	VerificationInferred     = "inferred"
	VerificationUnknown      = "unknown"
	VerificationContradicted = "contradicted"

	ClusterKindCorrective         = "corrective"
	ClusterStatusObserved         = "observed"
	ClusterStatusCandidateEmitted = "candidate_emitted"
	ClusterStatusSuperseded       = "superseded"

	SemanticSchemaVersion = "memoryos-semantic-consolidation.v1"
	CorrectiveCategory    = "memoryos_corrective_recurrence"
	SourceBoundary        = "internal/memoryos/consolidation"
)

// These aliases keep consolidation coupled to the canonical Episode domain,
// so a consumer cannot accidentally project a second, divergent episode
// schema. Bodies remain absent from the domain types by construction.
type Episode = episode.Episode
type ContextUse = episode.ContextUse
type SkillUse = episode.SkillUse
type ToolUse = episode.ToolUse
type InvocationUse = episode.InvocationUse
type ObligationObservation = episode.ObligationObservation
type VerificationSummary = episode.VerificationSummary

type EpisodeReader interface {
	List(context.Context, string, time.Time, time.Time, int) ([]episode.Episode, error)
	Get(context.Context, string, string) (episode.Episode, error)
}

type SemanticProposer interface {
	Propose(context.Context, rag.ProposeRequest) (rag.KnowledgeVersion, bool, error)
}

type CorrectiveProposer interface {
	Propose(context.Context, memory.ProposeRequest) (memory.Entry, bool, error)
}

type ClusterStore interface {
	SaveCluster(context.Context, Cluster) (Cluster, bool, error)
}

type SemanticGroupKey struct {
	RoleID             string `json:"role_id"`
	TaskClass          string `json:"task_class"`
	ExecutionPurpose   string `json:"execution_purpose"`
	ExecutionProfileID string `json:"execution_profile_id"`
}

func (k SemanticGroupKey) String() string {
	return strings.Join([]string{k.RoleID, k.TaskClass, k.ExecutionPurpose, k.ExecutionProfileID}, "|")
}

type CorrectiveClusterKey struct {
	OrganizationID     string `json:"organization_id"`
	RoleID             string `json:"role_id"`
	TaskClass          string `json:"task_class"`
	ExecutionProfileID string `json:"execution_profile_id"`
	ObligationKey      string `json:"obligation_key"`
	ObligationKind     string `json:"obligation_kind"`
}

func (k CorrectiveClusterKey) String() string {
	return strings.Join([]string{k.OrganizationID, k.RoleID, k.TaskClass, k.ExecutionProfileID, k.ObligationKey, k.ObligationKind}, "|")
}

type Cluster struct {
	ID                 string    `json:"cluster_id"`
	OrganizationID     string    `json:"organization_id"`
	Kind               string    `json:"cluster_kind"`
	RoleID             string    `json:"role_id"`
	TaskClass          string    `json:"task_class"`
	ExecutionProfileID string    `json:"execution_profile_id"`
	ObligationKey      string    `json:"obligation_key"`
	ObligationKind     string    `json:"obligation_kind"`
	EpisodeIDs         []string  `json:"episode_ids"`
	DecisionRunRefs    []int64   `json:"decision_run_refs"`
	PassCount          int       `json:"pass_count"`
	FailCount          int       `json:"fail_count"`
	FirstObserved      time.Time `json:"first_observed"`
	LastObserved       time.Time `json:"last_observed"`
	CanonicalDigest    string    `json:"canonical_digest"`
	Status             string    `json:"status"`
	Revision           int64     `json:"revision"`
	CreatedAt          time.Time `json:"created_at,omitempty"`
}

func (c Cluster) Validate() error {
	if !isDigest(c.CanonicalDigest) || strings.TrimSpace(c.ID) == "" || strings.TrimSpace(c.OrganizationID) == "" {
		return errors.New("memoryos: cluster id, organization, and canonical digest are required")
	}
	if c.Kind != ClusterKindCorrective || strings.TrimSpace(c.RoleID) == "" || strings.TrimSpace(c.TaskClass) == "" || strings.TrimSpace(c.ExecutionProfileID) == "" {
		return errors.New("memoryos: corrective cluster identity is incomplete")
	}
	if strings.TrimSpace(c.ObligationKey) == "" || strings.TrimSpace(c.ObligationKind) == "" || len(c.EpisodeIDs) == 0 || len(c.DecisionRunRefs) == 0 {
		return errors.New("memoryos: corrective cluster evidence is incomplete")
	}
	if c.PassCount < 0 || c.FailCount < 0 || c.FirstObserved.IsZero() || c.LastObserved.IsZero() || c.LastObserved.Before(c.FirstObserved) {
		return errors.New("memoryos: invalid cluster metrics or timestamps")
	}
	if c.Status != ClusterStatusObserved && c.Status != ClusterStatusCandidateEmitted && c.Status != ClusterStatusSuperseded {
		return fmt.Errorf("memoryos: invalid cluster status %q", c.Status)
	}
	return nil
}

type Failure struct {
	Phase string `json:"phase"`
	Key   string `json:"key"`
	Error string `json:"error"`
}

type Result struct {
	WindowStart                 time.Time `json:"window_start"`
	WindowEnd                   time.Time `json:"window_end"`
	EpisodesSeen                int       `json:"episodes_seen"`
	EpisodesProjected           int       `json:"episodes_projected"`
	EpisodesReused              int       `json:"episodes_reused"`
	SemanticGroups              int       `json:"semantic_groups"`
	SemanticCandidates          int       `json:"semantic_candidates"`
	SemanticReused              int       `json:"semantic_reused"`
	CorrectiveClusters          int       `json:"corrective_clusters"`
	CorrectiveCandidates        int       `json:"corrective_candidates"`
	CorrectiveReused            int       `json:"corrective_reused"`
	MixedBindingEpisodes        int       `json:"mixed_binding_episodes"`
	EpisodesWithoutVerification int       `json:"episodes_without_verification"`
	Failures                    []Failure `json:"failures"`
}

type Config struct {
	MinSemanticRecurrence   int
	MinCorrectiveRecurrence int
	MaxEpisodes             int
	MaxWindow               time.Duration
	ProposerRoleID          string
	CorrectionPending       string
}

func DefaultConfig() Config {
	return Config{
		MinSemanticRecurrence:   3,
		MinCorrectiveRecurrence: 3,
		MaxEpisodes:             2048,
		MaxWindow:               365 * 24 * time.Hour,
		CorrectionPending:       "Pendiente de revisión humana: evaluar la obligación recurrente y decidir la corrección autorizada.",
	}
}

func (c Config) Validate() error {
	if c.MinSemanticRecurrence < 2 || c.MinSemanticRecurrence > 1000 || c.MinCorrectiveRecurrence < 2 || c.MinCorrectiveRecurrence > 1000 {
		return errors.New("memoryos: recurrence thresholds are outside allowed range")
	}
	if c.MaxEpisodes < c.MinSemanticRecurrence || c.MaxEpisodes > 10000 || c.MaxWindow <= 0 || c.MaxWindow > 5*365*24*time.Hour {
		return errors.New("memoryos: episode window limits are invalid")
	}
	if strings.TrimSpace(c.CorrectionPending) == "" {
		return errors.New("memoryos: correction placeholder is required")
	}
	return nil
}

func observedAt(e episode.Episode) time.Time {
	if e.FinishedAt != nil && !e.FinishedAt.IsZero() {
		return e.FinishedAt.UTC()
	}
	if e.StartedAt != nil && !e.StartedAt.IsZero() {
		return e.StartedAt.UTC()
	}
	if e.Verification != nil && e.Verification.VerifiedAt != nil {
		return e.Verification.VerifiedAt.UTC()
	}
	return time.Time{}
}

func bindingMode(e episode.Episode) string {
	if e.BindingMode != "" {
		return string(e.BindingMode)
	}
	if len(e.Invocations) == 0 {
		return BindingModeUnknown
	}
	provider, model := e.Invocations[0].ProviderID, e.Invocations[0].ProviderModelID
	for _, invocation := range e.Invocations[1:] {
		if invocation.ProviderID != provider || invocation.ProviderModelID != model {
			return BindingModeMixed
		}
	}
	return BindingModeHomogeneous
}

func evidenceDigest(e episode.Episode) string {
	if e.Verification != nil {
		for _, obligation := range e.Verification.Obligations {
			if isDigest(obligation.EvidenceDigest) {
				return obligation.EvidenceDigest
			}
		}
		for _, ref := range e.Verification.EvidenceRefs {
			if isDigest(ref) {
				return ref
			}
		}
	}
	if isDigest(e.Observability.SourceFactsDigest) {
		return e.Observability.SourceFactsDigest
	}
	return ""
}

func positive(e episode.Episode) bool {
	if e.Verification == nil || e.Verification.Verdict != "pass" {
		return false
	}
	for _, obligation := range e.Verification.Obligations {
		if obligation.Label != VerificationVerified && obligation.Label != VerificationInferred {
			return false
		}
	}
	return true
}

func correctiveObligations(e episode.Episode) []episode.ObligationObservation {
	if e.Verification == nil {
		return nil
	}
	out := make([]episode.ObligationObservation, 0, len(e.Verification.Obligations))
	for _, obligation := range e.Verification.Obligations {
		if strings.TrimSpace(obligation.Key) == "" || strings.TrimSpace(obligation.Kind) == "" || obligation.Label != VerificationContradicted {
			continue
		}
		out = append(out, obligation)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Key != out[j].Key {
			return out[i].Key < out[j].Key
		}
		return out[i].Kind < out[j].Kind
	})
	return out
}

func isDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, r := range value {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return false
		}
	}
	return true
}

func digestJSON(value any) string {
	body, _ := json.Marshal(value)
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
