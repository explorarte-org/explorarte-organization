package consolidation

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/memory"
	"github.com/Mireuz13/explorarte-organization/internal/memoryos/episode"
	"github.com/Mireuz13/explorarte-organization/internal/rag"
)

type Service struct {
	reader     EpisodeReader
	clusters   ClusterStore
	semantic   SemanticProposer
	corrective CorrectiveProposer
	config     Config
}

func NewService(reader EpisodeReader, clusters ClusterStore, semantic SemanticProposer, corrective CorrectiveProposer, config Config) (*Service, error) {
	if reader == nil || clusters == nil {
		return nil, errors.New("memoryos: episode reader and cluster store are required")
	}
	if semantic == nil && corrective == nil {
		return nil, errors.New("memoryos: at least one candidate proposer is required")
	}
	if config.SemanticOwner == "" {
		config.SemanticOwner = SemanticOwnerSleep
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return &Service{reader: reader, clusters: clusters, semantic: semantic, corrective: corrective, config: config}, nil
}

// Consolidate performs one bounded, manually-invoked consolidation pass. It
// reads durable Episode facts and emits candidates only. Approval, RAG
// publication/reindexing, and memory review remain separate host operations.
func (s *Service) Consolidate(ctx context.Context, organizationID string, from, to time.Time) (Result, error) {
	organizationID = strings.TrimSpace(organizationID)
	if organizationID == "" {
		return Result{}, errors.New("memoryos: organization id is required")
	}
	if from.IsZero() || to.IsZero() || !from.Before(to) || to.Sub(from) > s.config.MaxWindow {
		return Result{}, fmt.Errorf("memoryos: invalid consolidation window (maximum %s)", s.config.MaxWindow)
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	result := Result{WindowStart: from.UTC(), WindowEnd: to.UTC(), Failures: []Failure{}}
	episodes, err := s.reader.List(ctx, organizationID, from.UTC(), to.UTC(), s.config.MaxEpisodes)
	if err != nil {
		return result, fmt.Errorf("memoryos: list episodes: %w", err)
	}
	result.EpisodesSeen = len(episodes)
	valid, failures := canonicalEpisodes(organizationID, episodes)
	result.Failures = append(result.Failures, failures...)
	// This service consumes already materialized episodes. Projection/reuse
	// counters belong to the episode projector and remain zero here; invalid
	// source rows are reported as failures, never as reuse.
	for _, current := range valid {
		if bindingMode(current) == BindingModeMixed {
			result.MixedBindingEpisodes++
		}
		if current.Verification == nil {
			result.EpisodesWithoutVerification++
		}
	}
	result.SemanticOwner = s.config.SemanticOwner
	if s.semantic != nil {
		s.semanticPass(ctx, organizationID, valid, &result)
	}
	if s.corrective != nil {
		s.correctivePass(ctx, organizationID, valid, &result)
	}
	return result, nil
}

func (s *Service) Run(ctx context.Context, organizationID string, from, to time.Time) (Result, error) {
	return s.Consolidate(ctx, organizationID, from, to)
}

func validateEpisode(e episode.Episode) error {
	if strings.TrimSpace(e.ID) == "" || strings.TrimSpace(e.OrganizationID) == "" || strings.TrimSpace(e.HarnessRunID) == "" {
		return errors.New("episode id, organization_id, and harness_run_id are required")
	}
	if e.TaskID <= 0 || e.AttemptID <= 0 || strings.TrimSpace(e.RoleID) == "" || strings.TrimSpace(e.TaskClass) == "" || strings.TrimSpace(e.ExecutionPurpose) == "" || strings.TrimSpace(e.ExecutionProfileID) == "" {
		return errors.New("episode task, attempt, role, task class, purpose, and profile are required")
	}
	if observedAt(e).IsZero() {
		return errors.New("episode has no durable observed timestamp")
	}
	if e.TurnsUsed < 0 || e.ToolCallsUsed < 0 {
		return errors.New("episode metrics cannot be negative")
	}
	if e.ActualCostUSDNanos != nil && *e.ActualCostUSDNanos < 0 || e.EstimatedCostUSDNanos != nil && *e.EstimatedCostUSDNanos < 0 {
		return errors.New("episode cost cannot be negative")
	}
	for _, invocation := range e.Invocations {
		if invocation.InvocationID <= 0 || strings.TrimSpace(invocation.ProviderID) == "" || strings.TrimSpace(invocation.ProviderModelID) == "" {
			return errors.New("invocation identity is incomplete")
		}
		for _, value := range []*int64{invocation.InputTokens, invocation.OutputTokens, invocation.ReasoningTokens, invocation.CostUSDNanos, invocation.EstimatedUSDNanos} {
			if value != nil && *value < 0 {
				return errors.New("invocation metrics cannot be negative")
			}
		}
	}
	return nil
}

func canonicalEpisodes(organizationID string, input []episode.Episode) ([]episode.Episode, []Failure) {
	byID := make(map[string]episode.Episode, len(input))
	failures := make([]Failure, 0)
	for _, current := range input {
		if err := validateEpisode(current); err != nil {
			failures = append(failures, Failure{Phase: "episode", Key: current.HarnessRunID, Error: err.Error()})
			continue
		}
		if current.OrganizationID != organizationID {
			failures = append(failures, Failure{Phase: "episode", Key: current.HarnessRunID, Error: "episode organization scope does not match request"})
			continue
		}
		if existing, ok := byID[current.ID]; ok {
			if digestJSON(existing) != digestJSON(current) {
				failures = append(failures, Failure{Phase: "episode", Key: current.HarnessRunID, Error: "duplicate episode has divergent facts"})
				continue
			}
			continue
		}
		byID[current.ID] = current
	}
	out := make([]episode.Episode, 0, len(byID))
	for _, current := range byID {
		out = append(out, current)
	}
	sort.Slice(out, func(i, j int) bool {
		if observedAt(out[i]).Equal(observedAt(out[j])) {
			return out[i].HarnessRunID < out[j].HarnessRunID
		}
		return observedAt(out[i]).Before(observedAt(out[j]))
	})
	return out, failures
}

func (s *Service) semanticPass(ctx context.Context, organizationID string, episodes []episode.Episode, result *Result) {
	owner := s.config.SemanticOwner
	if owner == "" {
		owner = SemanticOwnerSleep
	}
	result.SemanticOwner = owner

	groups := make(map[SemanticGroupKey][]episode.Episode)
	for _, current := range episodes {
		// Positive semantic consolidation requires an actual completion pass,
		// a decision run and a durable digest. Other Episodes remain visible but
		// cannot be converted into positive knowledge.
		if !positive(current) || current.DecisionRunID == nil || *current.DecisionRunID <= 0 || evidenceDigest(current) == "" {
			continue
		}
		key := SemanticGroupKey{RoleID: current.RoleID, TaskClass: current.TaskClass, ExecutionPurpose: current.ExecutionPurpose, ExecutionProfileID: current.ExecutionProfileID}
		groups[key] = append(groups[key], current)
	}
	keys := make([]SemanticGroupKey, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
	result.SemanticGroups = len(keys)

	// Structural single-owner check:
	// If MemoryOS is not designated as the active semantic owner (Sleep remains
	// the active owner for Phase 1), observe and report semantic groups, but do
	// not emit candidate proposals into RAG staging.
	if owner != SemanticOwnerMemoryOS {
		result.SemanticSkippedNotOwner = true
		result.SemanticSkipReason = SemanticSkipReasonNotOwner
		return
	}

	for _, key := range keys {
		members := uniqueEpisodes(groups[key])
		if len(members) < s.config.MinSemanticRecurrence {
			continue
		}
		request, _, err := BuildSemanticCandidate(organizationID, key, members)
		if err != nil {
			result.Failures = append(result.Failures, Failure{Phase: "semantic", Key: key.String(), Error: err.Error()})
			continue
		}
		if _, reused, err := s.semantic.Propose(ctx, request); err != nil {
			result.Failures = append(result.Failures, Failure{Phase: "semantic", Key: key.String(), Error: err.Error()})
		} else if reused {
			result.SemanticReused++
		} else {
			result.SemanticCandidates++
		}
	}
}

func (s *Service) correctivePass(ctx context.Context, organizationID string, episodes []episode.Episode, result *Result) {
	groups := make(map[CorrectiveClusterKey][]CorrectiveObservation)
	for _, current := range episodes {
		if current.DecisionRunID == nil || *current.DecisionRunID <= 0 || strings.TrimSpace(current.ExecutionProfileID) == "" || evidenceDigest(current) == "" {
			continue
		}
		for _, obligation := range allObligationObservations(current) {
			key := CorrectiveClusterKey{OrganizationID: organizationID, RoleID: current.RoleID, TaskClass: current.TaskClass, ExecutionProfileID: current.ExecutionProfileID, ObligationKey: obligation.Key, ObligationKind: obligation.Kind}
			groups[key] = append(groups[key], CorrectiveObservation{Episode: current, Obligation: obligation})
		}
	}
	keys := make([]CorrectiveClusterKey, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
	for _, key := range keys {
		members := sortObservations(groups[key])
		if countContradictedRuns(members) < s.config.MinCorrectiveRecurrence {
			continue
		}
		cluster, err := BuildCorrectiveCluster(key, members)
		if err != nil {
			result.Failures = append(result.Failures, Failure{Phase: "corrective_cluster", Key: key.String(), Error: err.Error()})
			continue
		}
		saved, _, err := s.clusters.SaveCluster(ctx, cluster)
		if err != nil {
			result.Failures = append(result.Failures, Failure{Phase: "corrective_cluster", Key: key.String(), Error: err.Error()})
			continue
		}
		result.CorrectiveClusters++
		request, err := BuildCorrectiveCandidate(organizationID, saved, members, s.config)
		if err != nil {
			result.Failures = append(result.Failures, Failure{Phase: "corrective", Key: key.String(), Error: err.Error()})
			continue
		}
		if _, reused, err := s.corrective.Propose(ctx, request); err != nil {
			result.Failures = append(result.Failures, Failure{Phase: "corrective", Key: key.String(), Error: err.Error()})
			continue
		} else if reused {
			result.CorrectiveReused++
		} else {
			result.CorrectiveCandidates++
		}
		cluster.Status = ClusterStatusCandidateEmitted
		cluster.CanonicalDigest = ClusterDigest(cluster)
		if _, _, err := s.clusters.SaveCluster(ctx, cluster); err != nil {
			result.Failures = append(result.Failures, Failure{Phase: "corrective_cluster_status", Key: key.String(), Error: err.Error()})
		}
	}
}

func allObligationObservations(current episode.Episode) []episode.ObligationObservation {
	if current.Verification == nil {
		return nil
	}
	out := make([]episode.ObligationObservation, 0, len(current.Verification.Obligations))
	for _, obligation := range current.Verification.Obligations {
		if strings.TrimSpace(obligation.Key) == "" || strings.TrimSpace(obligation.Kind) == "" {
			continue
		}
		out = append(out, obligation)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Key != out[j].Key {
			return out[i].Key < out[j].Key
		}
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Label < out[j].Label
	})
	return out
}

func sortObservations(input []CorrectiveObservation) []CorrectiveObservation {
	out := append([]CorrectiveObservation(nil), input...)
	sort.SliceStable(out, func(i, j int) bool {
		if observedAt(out[i].Episode).Equal(observedAt(out[j].Episode)) {
			if *out[i].Episode.DecisionRunID != *out[j].Episode.DecisionRunID {
				return *out[i].Episode.DecisionRunID < *out[j].Episode.DecisionRunID
			}
			return out[i].Episode.ID < out[j].Episode.ID
		}
		return observedAt(out[i].Episode).Before(observedAt(out[j].Episode))
	})
	return out
}

func countContradictedRuns(input []CorrectiveObservation) int {
	seen := make(map[int64]struct{})
	for _, observation := range input {
		if observation.Obligation.Label != VerificationContradicted || observation.Episode.DecisionRunID == nil {
			continue
		}
		seen[*observation.Episode.DecisionRunID] = struct{}{}
	}
	return len(seen)
}

func uniqueEpisodes(input []episode.Episode) []episode.Episode {
	byID := make(map[string]episode.Episode, len(input))
	for _, current := range input {
		byID[current.ID] = current
	}
	out := make([]episode.Episode, 0, len(byID))
	for _, current := range byID {
		out = append(out, current)
	}
	sort.Slice(out, func(i, j int) bool {
		if observedAt(out[i]).Equal(observedAt(out[j])) {
			return out[i].HarnessRunID < out[j].HarnessRunID
		}
		return observedAt(out[i]).Before(observedAt(out[j]))
	})
	return out
}

func newer(a, b episode.Episode) bool {
	if !observedAt(a).Equal(observedAt(b)) {
		return observedAt(a).After(observedAt(b))
	}
	return a.HarnessRunID > b.HarnessRunID
}

var _ SemanticProposer = (interface {
	Propose(context.Context, rag.ProposeRequest) (rag.KnowledgeVersion, bool, error)
})(nil)
var _ CorrectiveProposer = (interface {
	Propose(context.Context, memory.ProposeRequest) (memory.Entry, bool, error)
})(nil)
