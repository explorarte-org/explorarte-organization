package corpuscensus

import "sort"

// Census is the aggregate report this package produces (owner decision,
// Part B section 1 and the 35-point final report format). It is computed
// purely from in-memory data (all Bronze rows plus all SilverRecords in
// the StateStore, including ones resumed from a prior run) -- BuildCensus
// never touches the network, the filesystem, or a subprocess itself.
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

	ParserHealth ParserHealthCensus
	Metadata     MetadataCensus
	Topics       map[string]int

	Decisions map[Decision]int

	SelfImprovingAgentsOverlap int // artifacts whose SHA-256 matched the separate seed corpus

	DiscoveryCatalogs int // count of catalog directories seen (not documents -- see Orchestrator caller)
}

type ParserHealthCensus struct {
	Valid              int
	Malformed          int
	Encrypted          int
	Timeout            int
	RetryableTimeout   int
	HardTimeout        int
	EmptyTextDocuments int // ALL pages empty-text
	SomeEmptyTextPages int // some but not all pages empty-text
	ReviewRequired     int
}

type MetadataCensus struct {
	YearDistribution       map[int]int
	CollectionDistribution map[string]int
	LanguageDistribution   map[string]int
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

	for _, r := range records {
		census.UniqueArtifacts++
		census.TotalBytesKnown += r.ArtifactBytes
		workSet[r.WorkID] = true
		census.Decisions[r.Decision]++
		census.Metadata.CollectionDistribution[r.Collection]++
		census.Metadata.LanguageDistribution[r.Language]++
		if r.Year > 0 {
			census.Metadata.YearDistribution[r.Year]++
		}
		for _, topic := range r.Topics {
			census.Topics[topic]++
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

		switch {
		case r.Decision == DecisionEncrypted:
			census.ParserHealth.Encrypted++
		case r.Decision == DecisionTimeout:
			census.ParserHealth.Timeout++
			switch r.PDF.TimeoutPolicy {
			case TimeoutRetryable:
				census.ParserHealth.RetryableTimeout++
			case TimeoutHard:
				census.ParserHealth.HardTimeout++
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

	census.UniqueWorks = len(workSet)
	for _, count := range shaCounts {
		census.UniqueSHA256++
		if count > 1 {
			census.DuplicateSHA256++
		}
	}

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
