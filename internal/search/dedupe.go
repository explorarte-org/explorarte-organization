package search

import (
	"sort"
	"sync/atomic"
)

var dedupeCallCount int64

func nextDedupCall() int64 {
	return atomic.AddInt64(&dedupeCallCount, 1)
}

type Deduplicator struct {
	opts        DedupeOptions
	seenOrder   map[string]int64
	globalOrder int64
}

type DedupeOptions struct {
	PreferFirstSeen bool
}

func NewDeduplicator(opts DedupeOptions) *Deduplicator {
	return &Deduplicator{
		opts:      opts,
		seenOrder: make(map[string]int64),
	}
}

func (d *Deduplicator) MergeWithResults(chosen []SearchResult, remaining ...[]SearchResult) ([][]SearchResult, map[string]struct{}) {
	if len(chosen) == 0 && len(remaining) == 0 {
		return nil, nil
	}

	// Start with chosen results, assigning order numbers
	allResults := make([]SearchResult, 0, len(chosen))
	allResults = append(allResults, chosen...)
	for _, batch := range remaining {
		allResults = append(allResults, batch...)
	}

	if len(allResults) == 0 {
		return nil, nil
	}

	// Build deduplicated map with order tracking
	deduped := make(map[string]*SearchResult)
	for i := range allResults {
		cid := d.canonicalID(&allResults[i])
		if cid == "" {
			continue
		}
		if existing, ok := deduped[cid]; ok {
			// Already seen: merge metadata but preserve first-seen if PreferFirstSeen
			if d.opts.PreferFirstSeen {
				mergeMetadata(existing, &allResults[i])
			} else {
				mergeMetadata(existing, &allResults[i])
			}
		} else {
			// First time seeing this canonical ID
			order := d.nextOrder()
			allResults[i].dedupOrder = order
			deduped[cid] = &allResults[i]
		}
	}

	// Collect and sort by order to ensure deterministic output
	out := make([]SearchResult, 0, len(deduped))
	for _, r := range deduped {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].dedupOrder < out[j].dedupOrder
	})

	return [][]SearchResult{out}, nil
}

func (d *Deduplicator) nextOrder() int64 {
	return atomic.AddInt64(&d.globalOrder, 1)
}

func (d *Deduplicator) canonicalID(r *SearchResult) string {
	if r.DOI != "" {
		return "doi:" + r.DOI
	}
	if r.ArxivID != "" {
		return "arxiv:" + r.ArxivID
	}
	if r.ISBN != "" {
		return "isbn:" + r.ISBN
	}
	if r.GutenbergID != "" {
		return "gutenberg:" + r.GutenbergID
	}
	if r.CanonicalURL != "" {
		return "url:" + r.CanonicalURL
	}
	// Fallback to URL if no other identifier
	if r.URL != "" {
		return "url:" + r.URL
	}
	return ""
}

func mergeMetadata(target, source *SearchResult) {
	if target.DOI == "" && source.DOI != "" {
		target.DOI = source.DOI
	}
	if target.ISBN == "" && source.ISBN != "" {
		target.ISBN = source.ISBN
	}
	if target.ArxivID == "" && source.ArxivID != "" {
		target.ArxivID = source.ArxivID
	}
	if target.GutenbergID == "" && source.GutenbergID != "" {
		target.GutenbergID = source.GutenbergID
	}
	if len(source.Authors) > 0 {
		if target.Authors == nil {
			target.Authors = make([]string, len(source.Authors))
			copy(target.Authors, source.Authors)
		} else {
			// Merge unique authors
			existing := make(map[string]struct{})
			for _, a := range target.Authors {
				existing[a] = struct{}{}
			}
			for _, a := range source.Authors {
				if _, ok := existing[a]; !ok {
					target.Authors = append(target.Authors, a)
					existing[a] = struct{}{}
				}
			}
		}
	}
	if target.Publisher == "" && source.Publisher != "" {
		target.Publisher = source.Publisher
	}
	if target.Year == 0 && source.Year != 0 {
		target.Year = source.Year
	}
	if target.OpenAccess && !target.OpenAccess {
		target.OpenAccess = source.OpenAccess
	}
	if target.License == "" && source.License != "" {
		target.License = source.License
	}
	if target.Abstract == "" && source.Abstract != "" {
		target.Abstract = source.Abstract
	}
	if target.Citations == 0 && source.Citations != 0 {
		target.Citations = source.Citations
	}
	// Merge keywords
	if len(source.Keywords) > 0 {
		existing := make(map[string]struct{})
		for _, k := range target.Keywords {
			existing[k] = struct{}{}
		}
		for _, k := range source.Keywords {
			if _, ok := existing[k]; !ok {
				target.Keywords = append(target.Keywords, k)
			}
		}
	}
}
