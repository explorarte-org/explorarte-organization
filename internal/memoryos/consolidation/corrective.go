package consolidation

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/memory"
	"github.com/Mireuz13/explorarte-organization/internal/memoryos/episode"
)

// CorrectiveObservation is an obligation-level observation from one Episode.
// The type is exported so deterministic fixtures can build clusters without
// importing an implementation-specific adapter.
type CorrectiveObservation struct {
	Episode    episode.Episode
	Obligation episode.ObligationObservation
}

// BuildCorrectiveCluster groups stable obligation identity. Detail text is
// retained in the originating Episode but never participates in the key.
// Distinct decision runs determine recurrence, while all Episode IDs remain in
// the cluster for complete provenance.
func BuildCorrectiveCluster(key CorrectiveClusterKey, observations []CorrectiveObservation) (Cluster, error) {
	if strings.TrimSpace(key.OrganizationID) == "" || strings.TrimSpace(key.RoleID) == "" || strings.TrimSpace(key.TaskClass) == "" || strings.TrimSpace(key.ExecutionProfileID) == "" || strings.TrimSpace(key.ObligationKey) == "" || strings.TrimSpace(key.ObligationKind) == "" {
		return Cluster{}, fmt.Errorf("memoryos: corrective cluster key is incomplete")
	}
	if len(observations) == 0 {
		return Cluster{}, fmt.Errorf("memoryos: corrective cluster has no observations")
	}
	observations = sortObservations(observations)
	episodeIDs := make([]string, 0, len(observations))
	runIDs := make([]int64, 0, len(observations))
	seenEpisodes := make(map[string]struct{}, len(observations))
	seenRuns := make(map[int64]struct{}, len(observations))
	first, last := observedAt(observations[0].Episode), observedAt(observations[0].Episode)
	passCount, failCount := 0, 0
	for _, observation := range observations {
		current := observation.Episode
		if current.DecisionRunID == nil || *current.DecisionRunID <= 0 || evidenceDigest(current) == "" {
			continue
		}
		if _, ok := seenEpisodes[current.ID]; !ok {
			episodeIDs = append(episodeIDs, current.ID)
			seenEpisodes[current.ID] = struct{}{}
		}
		if _, ok := seenRuns[*current.DecisionRunID]; !ok {
			runIDs = append(runIDs, *current.DecisionRunID)
			seenRuns[*current.DecisionRunID] = struct{}{}
		}
		when := observedAt(current)
		if when.Before(first) {
			first = when
		}
		if when.After(last) {
			last = when
		}
		if observation.Obligation.Label == VerificationContradicted {
			failCount++
		} else if observation.Obligation.Label == VerificationVerified || observation.Obligation.Label == VerificationInferred {
			passCount++
		}
	}
	if len(runIDs) == 0 || len(episodeIDs) == 0 {
		return Cluster{}, fmt.Errorf("memoryos: corrective cluster has no grounded evidence")
	}
	sort.Strings(episodeIDs)
	sort.Slice(runIDs, func(i, j int) bool { return runIDs[i] < runIDs[j] })
	cluster := Cluster{
		ID: "memoryos-corrective-" + digestJSON(key)[:32], OrganizationID: key.OrganizationID,
		Kind: ClusterKindCorrective, RoleID: key.RoleID, TaskClass: key.TaskClass,
		ExecutionProfileID: key.ExecutionProfileID, ObligationKey: key.ObligationKey,
		ObligationKind: key.ObligationKind, EpisodeIDs: episodeIDs, DecisionRunRefs: runIDs,
		PassCount: passCount, FailCount: failCount, FirstObserved: first.UTC(), LastObserved: last.UTC(),
		Status: ClusterStatusObserved, Revision: 1,
	}
	cluster.CanonicalDigest = ClusterDigest(cluster)
	if err := cluster.Validate(); err != nil {
		return Cluster{}, err
	}
	return cluster, nil
}

// ClusterDigest hashes immutable facts and lifecycle state in canonical sort
// order. Storage timestamps and database revision do not affect identity.
func ClusterDigest(cluster Cluster) string {
	copyCluster := cluster
	copyCluster.EpisodeIDs = append([]string(nil), cluster.EpisodeIDs...)
	copyCluster.DecisionRunRefs = append([]int64(nil), cluster.DecisionRunRefs...)
	sort.Strings(copyCluster.EpisodeIDs)
	sort.Slice(copyCluster.DecisionRunRefs, func(i, j int) bool { return copyCluster.DecisionRunRefs[i] < copyCluster.DecisionRunRefs[j] })
	copyCluster.CanonicalDigest = ""
	copyCluster.CreatedAt = time.Time{}
	copyCluster.Revision = 0
	copyCluster.Status = ""
	return digestJSON(copyCluster)
}

func BuildCorrectiveCandidate(organizationID string, cluster Cluster, observations []CorrectiveObservation, config Config) (memory.ProposeRequest, error) {
	if err := cluster.Validate(); err != nil {
		return memory.ProposeRequest{}, err
	}
	if cluster.OrganizationID != organizationID {
		return memory.ProposeRequest{}, fmt.Errorf("memoryos: cluster organization mismatch")
	}
	// Only contradicted observations contribute recurrence and evidence. A
	// healthy observation of the same obligation can enrich cluster counters,
	// but it cannot make a correction candidate appear.
	byRun := make(map[int64]CorrectiveObservation)
	for _, observation := range observations {
		if observation.Obligation.Label != VerificationContradicted || observation.Episode.DecisionRunID == nil || *observation.Episode.DecisionRunID <= 0 || evidenceDigest(observation.Episode) == "" {
			continue
		}
		runID := *observation.Episode.DecisionRunID
		if current, ok := byRun[runID]; !ok || newer(observation.Episode, current.Episode) {
			byRun[runID] = observation
		}
	}
	contradicted := make([]CorrectiveObservation, 0, len(byRun))
	for _, observation := range byRun {
		contradicted = append(contradicted, observation)
	}
	contradicted = sortObservations(contradicted)
	if len(contradicted) < config.MinCorrectiveRecurrence {
		return memory.ProposeRequest{}, fmt.Errorf("memoryos: recurrence threshold not met")
	}
	refs := make([]memory.EvidenceRef, 0, len(contradicted))
	for _, observation := range contradicted {
		refs = append(refs, memory.EvidenceRef{Reference: fmt.Sprintf("decisiongraph:run:%d", *observation.Episode.DecisionRunID), Digest: evidenceDigest(observation.Episode)})
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].Reference < refs[j].Reference })
	latest := contradicted[0]
	for _, observation := range contradicted[1:] {
		if newer(observation.Episode, latest.Episode) {
			latest = observation
		}
	}
	proposer := cluster.RoleID
	if strings.TrimSpace(config.ProposerRoleID) != "" {
		proposer = strings.TrimSpace(config.ProposerRoleID)
	}
	sourceRunID := *latest.Episode.DecisionRunID
	problem := fmt.Sprintf("MemoryOS observó recurrencia de una obligación contradicha: key=%s kind=%s; role=%s task_class=%s execution_profile=%s; decision runs=%d. Requiere revisión humana.", cluster.ObligationKey, cluster.ObligationKind, cluster.RoleID, cluster.TaskClass, cluster.ExecutionProfileID, len(refs))
	entryID := "memoryos-corrective-" + cluster.ID + "-" + cluster.CanonicalDigest[:24]
	return memory.ProposeRequest{
		Command: memory.ProposeCommand{
			ID: entryID, OrganizationID: organizationID, RoleID: cluster.RoleID,
			Category: CorrectiveCategory, Problem: problem, Correction: config.CorrectionPending,
			SourceKind: memory.SourceOperational, SourceRunID: sourceRunID, EvidenceRefs: refs,
			ProposedBy: proposer,
			Admission:  memory.AdmissionAttestation{DataClass: memory.DataOrganizational, AttestedBy: proposer, SourceBoundary: SourceBoundary, EvidenceRef: refs[0].Reference, AttestedAt: observedAt(latest.Episode)},
		},
		IdempotencyKey: "memoryos:corrective:" + cluster.ID + ":" + cluster.CanonicalDigest,
	}, nil
}
