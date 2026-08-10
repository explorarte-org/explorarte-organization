package corpuscensus

import "sort"

// Census is the aggregate report this package produces (owner decision,
// Part B section 1 and the re-run's 46-point final report format). It is
// computed purely from in-memory data (all Bronze rows plus all
// SilverRecords in the StateStore, including ones resumed from a prior
// run) -- BuildCensus never touches the network, the filesystem, or a
// subprocess itself.
type Census struct {
	TotalReferencesDiscovered int
	TotalPDFFilesPresent      int // bronze rows with status=downloaded
	TotalBytesKnown           int64

	UniqueWorks     int
	UniqueArtifacts int // = len(SilverRecords), one per downloaded bronze row

	UniqueSHA256    int
	DuplicateSHA256 int // SHA-256 values seen on more than one artifact

	UniqueDOI             int
	UniqueArxiv           int
	UniqueACL             int
	UniqueOpenReview      int
	UniqueTitleYear       int
	NoCanonicalIdentifier int // artifacts with none of DOI/arXiv/ACL/OpenReview

	ParserHealth  ParserHealthCensus
	Metadata      MetadataCensus
	Topics        map[string]int
	TopicCoverage []TopicCoverage

	Decisions map[Decision]int
	// FinalDecisionSum and ArtifactCountForSumCheck let a report reader
	// verify SUM(final_decision counts) == total Artifacts directly from
	// the JSON (owner decision, re-run section 7) instead of re-summing
	// the Decisions map by hand.
	FinalDecisionSum         int
	ArtifactCountForSumCheck int
	FinalDecisionSumMatches  bool

	AuthorityTiers AuthorityTierCensus

	AcceptedHighConfidence int // accepted, no metadata gaps
	AcceptedWithGaps       int // accepted, HasMetadataGaps=true

	SelfImprovingAgentsOverlap int // artifacts whose SHA-256 matched the separate seed corpus

	DiscoveryCatalogs int // count of catalog directories seen (not documents -- see Orchestrator caller)
}

type ParserHealthCensus struct {
	Valid              int
	Malformed          int
	Encrypted          int
	InitialTimeout     int // documents whose FIRST parse attempt timed out (= RetrySuccess + PersistentTimeout)
	RetrySuccess       int // timed out once, succeeded on the bounded retry
	PersistentTimeout  int // timed out on both the default and retry bounds -- still not "corrupt" (owner decision)
	EmptyTextDocuments int // ALL pages empty-text
	SomeEmptyTextPages int // some but not all pages empty-text
	ReviewRequired     int
}

type MetadataCensus struct {
	YearDistribution       map[int]int
	CollectionDistribution map[string]int
	LanguageDistribution   map[string]int // keyed by LanguageDetection.Language
}

type AuthorityTierCensus struct {
	TierA   int
	TierB   int
	TierC   int // conceptual only -- catalogs are discovery, never become SilverRecords, so this is always 0 from this package
	TierD   int // conceptual only -- same as above
	Unknown int
}

// TopicCoverage is a per-topic coverage-gap assessment (owner decision,
// re-run section 12: do not infer coverage from count alone). Tier is a
// deterministic, disclosed heuristic -- NOT a claim of true corpus
// completeness, which requires a human/domain judgment this package does
// not make.
type TopicCoverage struct {
	Topic           string
	WorkCount       int
	DistinctSources int    // distinct (repo_name or venue) contributing to this topic
	Tier            string // "strong" | "adequate" | "weak" | "missing"
}

func classifyCoverageTier(workCount, distinctSources int) string {
	switch {
	case workCount == 0:
		return "missing"
	case workCount >= 40 && distinctSources >= 5:
		return "strong"
	case workCount >= 15 && distinctSources >= 3:
		return "adequate"
	default:
		return "weak"
	}
}

func BuildCensus(papers []BronzePaper, records []SilverRecord) Census {
	census := Census{
		TotalReferencesDiscovered: len(papers),
		Decisions:                 make(map[Decision]int),
		Topics:                    make(map[string]int),
		Metadata: MetadataCensus{
			YearDistribution:       make(map[int]int),
			CollectionDistribution: make(map[string]int),
			LanguageDistribution:   make(map[string]int),
		},
	}

	shaCounts := make(map[string]int)
	doiSet := make(map[string]bool)
	arxivSet := make(map[string]bool)
	aclSet := make(map[string]bool)
	openReviewSet := make(map[string]bool)
	workSet := make(map[string]bool)
	titleYearSet := make(map[string]bool)

	for _, p := range papers {
		if p.Status == "downloaded" {
			census.TotalPDFFilesPresent++
		}
		if p.DOI != nil && *p.DOI != "" {
			doiSet[*p.DOI] = true
		}
		if p.ArxivID != nil && *p.ArxivID != "" {
			arxivSet[*p.ArxivID] = true
		}
		if p.ACLID != nil && *p.ACLID != "" {
			aclSet[*p.ACLID] = true
		}
		if p.OpenReviewID != nil && *p.OpenReviewID != "" {
			openReviewSet[*p.OpenReviewID] = true
		}
		if kind, key := ResolveWorkIdentity(p); kind == IdentityTitleYear {
			titleYearSet[key] = true
		}
	}
	census.UniqueDOI = len(doiSet)
	census.UniqueArxiv = len(arxivSet)
	census.UniqueACL = len(aclSet)
	census.UniqueOpenReview = len(openReviewSet)
	census.UniqueTitleYear = len(titleYearSet)

	topicSources := make(map[string]map[string]bool) // topic -> set of distinct (repo_name|venue)

	for _, r := range records {
		census.UniqueArtifacts++
		census.TotalBytesKnown += r.ArtifactBytes
		workSet[r.WorkID] = true
		census.Decisions[r.Decision]++
		census.Metadata.CollectionDistribution[r.Collection]++
		census.Metadata.LanguageDistribution[r.Language.Language]++
		if r.Year > 0 {
			census.Metadata.YearDistribution[r.Year]++
		}
		for _, topic := range r.Topics {
			census.Topics[topic]++
			if topicSources[topic] == nil {
				topicSources[topic] = make(map[string]bool)
			}
			sourceKey := r.CanonicalSource
			if sourceKey == "" {
				for _, dv := range r.DiscoveredVia {
					sourceKey = dv.RepoName
					break
				}
			}
			if sourceKey != "" {
				topicSources[topic][sourceKey] = true
			}
		}
		if r.SHA256 != "" {
			shaCounts[r.SHA256]++
		}
		if r.DOI == "" && r.ArxivID == "" && r.ACLID == "" && r.OpenReviewID == "" {
			census.NoCanonicalIdentifier++
		}
		if r.SeenInSelfImprovingAgentsSeed {
			census.SelfImprovingAgentsOverlap++
		}

		switch r.AuthorityTier {
		case TierA:
			census.AuthorityTiers.TierA++
		case TierB:
			census.AuthorityTiers.TierB++
		case TierC:
			census.AuthorityTiers.TierC++
		case TierD:
			census.AuthorityTiers.TierD++
		default:
			census.AuthorityTiers.Unknown++
		}

		if r.Decision == DecisionAccepted {
			if r.HasMetadataGaps {
				census.AcceptedWithGaps++
			} else {
				census.AcceptedHighConfidence++
			}
		}

		switch {
		case r.Decision == DecisionEncrypted:
			census.ParserHealth.Encrypted++
		case r.Decision == DecisionTimeout:
			switch r.PDF.TimeoutPolicy {
			case TimeoutRetryable:
				census.ParserHealth.RetrySuccess++
			case TimeoutHard:
				census.ParserHealth.PersistentTimeout++
			}
		case r.Decision == DecisionInvalid && r.PDF.QuarantineReason == "malformed":
			census.ParserHealth.Malformed++
		case r.Decision == DecisionReviewRequired:
			census.ParserHealth.ReviewRequired++
			census.ParserHealth.EmptyTextDocuments++
		}
		if r.PDF.Valid {
			census.ParserHealth.Valid++
			if r.PDF.EmptyTextPages > 0 && r.PDF.EmptyTextPages < r.PDF.Pages {
				census.ParserHealth.SomeEmptyTextPages++
			}
		}
	}
	census.ParserHealth.InitialTimeout = census.ParserHealth.RetrySuccess + census.ParserHealth.PersistentTimeout

	census.UniqueWorks = len(workSet)
	for _, count := range shaCounts {
		census.UniqueSHA256++
		if count > 1 {
			census.DuplicateSHA256++
		}
	}

	for _, topic := range AllTopics {
		count := census.Topics[topic]
		distinct := len(topicSources[topic])
		census.TopicCoverage = append(census.TopicCoverage, TopicCoverage{
			Topic: topic, WorkCount: count, DistinctSources: distinct,
			Tier: classifyCoverageTier(count, distinct),
		})
	}
	sort.Slice(census.TopicCoverage, func(i, j int) bool { return census.TopicCoverage[i].Topic < census.TopicCoverage[j].Topic })

	for _, decision := range FinalDecisions {
		census.FinalDecisionSum += census.Decisions[decision]
	}
	census.ArtifactCountForSumCheck = census.UniqueArtifacts
	census.FinalDecisionSumMatches = census.FinalDecisionSum == census.UniqueArtifacts

	return census
}

// SortedTopics returns Census.Topics as a stable, count-descending slice
// for report rendering.
func (c Census) SortedTopics() []struct {
	Topic string
	Count int
} {
	type row struct {
		Topic string
		Count int
	}
	rows := make([]row, 0, len(c.Topics))
	for topic, count := range c.Topics {
		rows = append(rows, row{topic, count})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Count != rows[j].Count {
			return rows[i].Count > rows[j].Count
		}
		return rows[i].Topic < rows[j].Topic
	})
	out := make([]struct {
		Topic string
		Count int
	}, len(rows))
	for i, r := range rows {
		out[i] = struct {
			Topic string
			Count int
		}{r.Topic, r.Count}
	}
	return out
}
