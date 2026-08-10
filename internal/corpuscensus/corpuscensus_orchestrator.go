package corpuscensus

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/pdfingest"
)

// OrchestratorConfig bounds concurrency (owner decision, section 12: "NO
// crear miles de procesos Poppler simultaneos") and where state lives.
type OrchestratorConfig struct {
	Concurrency         int // 0 means runtime.NumCPU(), capped at 8
	Validation          ValidationConfig
	SelfImprovingSHA256 map[string]bool // cross-corpus dedup check, owner decision section 17; nil is valid (skips the check)
	// DiscoveryCatalogCount is a caller-supplied count of discovery
	// catalog directories (Awesome-list git clones) seen alongside the
	// harvester's own `papers` table -- this package never reads
	// catalogs/ itself (owner decision, section 7: catalogs are
	// discovery, never a corpus source), so the count is opaque to it
	// and simply passed through to the Census for reporting.
	DiscoveryCatalogCount int
}

func (c OrchestratorConfig) resolvedConcurrency() int {
	if c.Concurrency > 0 {
		return c.Concurrency
	}
	n := runtime.NumCPU()
	if n > 8 {
		return 8
	}
	if n < 1 {
		return 1
	}
	return n
}

// Orchestrator ties Bronze (harvester reader), the dedup/classify pure
// functions, PDF validation (Poppler, reused), and the StateStore
// together into one resumable run.
type Orchestrator struct {
	Reader    HarvesterReader
	Processor pdfingest.Processor
	Store     *StateStore
	Config    OrchestratorConfig
}

// Run processes every "downloaded" bronze row not already terminal in the
// StateStore, bounded by Config.Concurrency, and returns the full Census
// computed from ALL records now in the StateStore (including ones
// resumed from a prior run) plus whatever bronze rows never became
// SilverRecords at all (duplicate/failed/unresolved/pending -- counted in
// the Census but never validated with Poppler, since they have no usable
// local PDF).
func (o *Orchestrator) Run(ctx context.Context) (Census, error) {
	papers, err := o.Reader.ListPapers(ctx)
	if err != nil {
		return Census{}, err
	}
	sources, err := o.Reader.ListSources(ctx)
	if err != nil {
		return Census{}, err
	}
	sourcesByID := make(map[string][]BronzeSource)
	for _, s := range sources {
		sourcesByID[s.CanonicalID] = append(sourcesByID[s.CanonicalID], s)
	}

	groups := GroupWorks(papers)
	workIDOf := make(map[string]string, len(papers)) // canonical_id -> work_id
	canonicalArtifactOf := make(map[string]string)   // work_id -> canonical artifact's canonical_id
	canonicalReasonOf := make(map[string]string)
	workGroupSize := make(map[string]int) // work_id -> number of artifacts in the group
	workIndex := 0
	// Deterministic work_id assignment: sort group keys so re-runs assign
	// the same work_id to the same group every time.
	groupKeys := make([]string, 0, len(groups))
	for k := range groups {
		groupKeys = append(groupKeys, k)
	}
	sort.Strings(groupKeys)
	for _, key := range groupKeys {
		group := groups[key]
		sort.Slice(group, func(i, j int) bool { return group[i].CanonicalID < group[j].CanonicalID })
		workID := fmt.Sprintf("work-%05d", workIndex)
		workIndex++
		canonical, reason := SelectCanonicalArtifact(group)
		canonicalArtifactOf[workID] = canonical.CanonicalID
		canonicalReasonOf[workID] = reason
		workGroupSize[workID] = len(group)
		for _, p := range group {
			workIDOf[p.CanonicalID] = workID
		}
	}

	type job struct {
		paper BronzePaper
	}
	var toProcess []job
	for _, p := range papers {
		if p.Status != "downloaded" {
			continue // duplicate/failed/unresolved/pending rows are counted in Census.Total but have no local PDF to validate
		}
		if o.Store.isTerminal(p.CanonicalID) {
			continue // resumed from a prior run
		}
		toProcess = append(toProcess, job{paper: p})
	}

	sem := make(chan struct{}, o.Config.resolvedConcurrency())
	var wg sync.WaitGroup
	var mu sync.Mutex
	for _, j := range toProcess {
		wg.Add(1)
		sem <- struct{}{}
		go func(p BronzePaper) {
			defer wg.Done()
			defer func() { <-sem }()
			record := o.processOne(ctx, p, sourcesByID[p.CanonicalID], workIDOf, canonicalArtifactOf, canonicalReasonOf, workGroupSize)
			mu.Lock()
			o.Store.Put(record)
			mu.Unlock()
		}(j.paper)
	}
	wg.Wait()

	if err := o.Store.Flush(); err != nil {
		return Census{}, err
	}

	census := BuildCensus(papers, o.Store.All())
	census.DiscoveryCatalogs = o.Config.DiscoveryCatalogCount
	return census, nil
}

func (o *Orchestrator) processOne(ctx context.Context, p BronzePaper, srcs []BronzeSource, workIDOf, canonicalArtifactOf, canonicalReasonOf map[string]string, workGroupSize map[string]int) SilverRecord {
	workID := workIDOf[p.CanonicalID]
	isCanonical := canonicalArtifactOf[workID] == p.CanonicalID
	groupSize := workGroupSize[workID]

	discovered := make([]SourceRef, 0, len(srcs))
	collection := ""
	for _, s := range srcs {
		discovered = append(discovered, SourceRef{Collection: s.Collection, RepoName: s.RepoName, SourceFile: s.SourceFile, RawURL: s.RawURL})
		if collection == "" {
			collection = s.Collection
		}
	}

	sourceType := ClassifySourceType(p.Title, p.str(p.Venue))
	tier := ClassifyAuthorityTier(p)

	record := SilverRecord{
		WorkID:              workID,
		ArtifactID:          p.CanonicalID,
		SHA256:              p.str(p.SHA256),
		Title:               p.Title,
		DOI:                 p.str(p.DOI),
		ArxivID:             p.str(p.ArxivID),
		ACLID:               p.str(p.ACLID),
		OpenReviewID:        p.str(p.OpenReviewID),
		Language:            "en", // harvester's catalogs are English-language sources; not independently detected in V1, documented gap
		SourceType:          sourceType,
		Collection:          collection,
		DiscoveredVia:       discovered,
		CanonicalSource:     p.str(p.Venue),
		Topics:              TopicsForCollection(collection),
		AuthorityTier:       tier,
		Quality:             DeterministicQuality(tier, groupSize),
		IsCanonicalArtifact: isCanonical,
		ProcessedAt:         time.Now().UTC(),
	}
	if p.Year != nil {
		record.Year = *p.Year
	}
	if o.Config.SelfImprovingSHA256 != nil && record.SHA256 != "" {
		record.SeenInSelfImprovingAgentsSeed = o.Config.SelfImprovingSHA256[strings.ToLower(record.SHA256)]
	}

	if sourceType == "evaluation" {
		record.Decision = DecisionLowRelevance
		record.DecisionReason = "matches a known evaluation/benchmark dataset name -- excluded from Knowledge to avoid evaluation leakage"
		return record
	}

	if !isCanonical {
		record.Decision = DecisionSuperseded
		record.SupersededBy = canonicalArtifactOf[workID]
		record.DecisionReason = canonicalReasonOf[workID]
		return record
	}

	if p.LocalPath == nil || p.str(p.LocalPath) == "" {
		record.Decision = DecisionInvalid
		record.DecisionReason = "downloaded status but no local_path recorded"
		return record
	}

	cleanPath := filepath.Clean(p.str(p.LocalPath))
	if info, err := os.Stat(cleanPath); err == nil {
		record.ArtifactBytes = info.Size()
	}
	validation, decision, reason := ValidatePDF(ctx, o.Processor, cleanPath, o.Config.Validation)
	record.PDF = validation
	record.Decision = decision
	record.DecisionReason = reason
	if record.SeenInSelfImprovingAgentsSeed && decision == DecisionAccepted {
		record.Decision = DecisionDuplicate
		record.DecisionReason = "SHA-256 matches an artifact already present in the self-improving-agents seed (Object Storage) -- not re-ingested by this corpus, per owner instruction"
	}
	return record
}
