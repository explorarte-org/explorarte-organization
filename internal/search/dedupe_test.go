package search

import (
	"testing"
	"time"
)

func TestDedupe_SameDOI_OneResult(t *testing.T) {
	dedupe := NewDeduplicator(DedupeOptions{PreferFirstSeen: true})
	now := time.Now()

	results := []SearchResult{
		{
			Provider:   "provider1",
			Title:      "Paper A",
			URL:        "http://provider1.com/paper",
			DOI:        "10.1234/test",
			Provenance: Provenance{Provider: "provider1", RetrievedAt: now},
		},
		{
			Provider:   "provider2",
			Title:      "Paper A",
			URL:        "http://provider2.com/paper",
			DOI:        "10.1234/test",
			Provenance: Provenance{Provider: "provider2", RetrievedAt: now},
		},
	}

	merged, _ := dedupe.MergeWithResults(results)
	if len(merged) != 1 {
		t.Fatalf("expected 1 batch, got %d", len(merged))
	}
	if len(merged[0]) != 1 {
		t.Errorf("expected 1 result after dedup by DOI, got %d", len(merged[0]))
	}
}

func TestDedupe_SameArxivID_OneResult(t *testing.T) {
	dedupe := NewDeduplicator(DedupeOptions{PreferFirstSeen: true})
	now := time.Now()

	results := []SearchResult{
		{
			Provider:   "provider1",
			Title:      "Paper A",
			URL:        "http://provider1.com/paper",
			ArxivID:    "2101.12345",
			Provenance: Provenance{Provider: "provider1", RetrievedAt: now},
		},
		{
			Provider:   "provider2",
			Title:      "Paper A",
			URL:        "http://provider2.com/paper",
			ArxivID:    "2101.12345",
			Provenance: Provenance{Provider: "provider2", RetrievedAt: now},
		},
	}

	merged, _ := dedupe.MergeWithResults(results)
	if len(merged) != 1 {
		t.Fatalf("expected 1 batch, got %d", len(merged))
	}
	if len(merged[0]) != 1 {
		t.Errorf("expected 1 result after dedup by arXiv ID, got %d", len(merged[0]))
	}
}

func TestDedupe_SameISBN_OneResult(t *testing.T) {
	dedupe := NewDeduplicator(DedupeOptions{PreferFirstSeen: true})
	now := time.Now()

	results := []SearchResult{
		{
			Provider:   "provider1",
			Title:      "Book A",
			URL:        "http://provider1.com/book",
			ISBN:       "978-0-123456-78-9",
			Provenance: Provenance{Provider: "provider1", RetrievedAt: now},
		},
		{
			Provider:   "provider2",
			Title:      "Book A",
			URL:        "http://provider2.com/book",
			ISBN:       "978-0-123456-78-9",
			Provenance: Provenance{Provider: "provider2", RetrievedAt: now},
		},
	}

	merged, _ := dedupe.MergeWithResults(results)
	if len(merged) != 1 {
		t.Fatalf("expected 1 batch, got %d", len(merged))
	}
	if len(merged[0]) != 1 {
		t.Errorf("expected 1 result after dedup by ISBN, got %d", len(merged[0]))
	}
}

func TestDedupe_SameGutenbergID_OneResult(t *testing.T) {
	dedupe := NewDeduplicator(DedupeOptions{PreferFirstSeen: true})
	now := time.Now()

	results := []SearchResult{
		{
			Provider:    "provider1",
			Title:       "Public Domain Book",
			URL:         "http://provider1.com/book",
			GutenbergID: "12345",
			Provenance:  Provenance{Provider: "provider1", RetrievedAt: now},
		},
		{
			Provider:    "provider2",
			Title:       "Public Domain Book",
			URL:         "http://provider2.com/book",
			GutenbergID: "12345",
			Provenance:  Provenance{Provider: "provider2", RetrievedAt: now},
		},
	}

	merged, _ := dedupe.MergeWithResults(results)
	if len(merged) != 1 {
		t.Fatalf("expected 1 batch, got %d", len(merged))
	}
	if len(merged[0]) != 1 {
		t.Errorf("expected 1 result after dedup by Gutenberg ID, got %d", len(merged[0]))
	}
}

func TestDedupe_SameCanonicalURL_OneResult(t *testing.T) {
	dedupe := NewDeduplicator(DedupeOptions{PreferFirstSeen: true})
	now := time.Now()

	results := []SearchResult{
		{
			Provider:   "provider1",
			Title:      "Same Page",
			URL:        "http://example.com/article",
			Provenance: Provenance{Provider: "provider1", RetrievedAt: now},
		},
		{
			Provider:   "provider2",
			Title:      "Same Page",
			URL:        "http://example.com/article",
			Provenance: Provenance{Provider: "provider2", RetrievedAt: now},
		},
	}

	merged, _ := dedupe.MergeWithResults(results)
	if len(merged) != 1 {
		t.Fatalf("expected 1 batch, got %d", len(merged))
	}
	if len(merged[0]) != 1 {
		t.Errorf("expected 1 result after dedup by URL, got %d", len(merged[0]))
	}
}

func TestDedupe_MetadataMergedWithoutLosingProvenance(t *testing.T) {
	dedupe := NewDeduplicator(DedupeOptions{PreferFirstSeen: true})
	now := time.Now()

	results := []SearchResult{
		{
			Provider:   "provider1",
			Title:      "Paper A",
			URL:        "http://provider1.com/paper",
			DOI:        "10.1234/test",
			Authors:    []string{"Smith"},
			Metadata:   map[string]any{"citations": 42},
			Provenance: Provenance{Provider: "provider1", RetrievedAt: now},
		},
		{
			Provider:   "provider2",
			Title:      "Paper A",
			URL:        "http://provider2.com/paper",
			DOI:        "10.1234/test",
			Authors:    []string{"Smith", "Jones"},
			Metadata:   map[string]any{"altmetric": "high"},
			Provenance: Provenance{Provider: "provider2", RetrievedAt: now},
		},
	}

	merged, _ := dedupe.MergeWithResults(results)
	if len(merged) != 1 {
		t.Fatalf("expected 1 batch, got %d", len(merged))
	}
	if len(merged[0]) != 1 {
		t.Fatalf("expected 1 result, got %d", len(merged[0]))
	}

	// Metadata should be merged
	if len(merged[0][0].Authors) != 2 {
		t.Errorf("expected 2 authors after merge, got %d", len(merged[0][0].Authors))
	}

	// With PreferFirstSeen: true, provider1 should be primary
	if merged[0][0].Provenance.Provider != "provider1" {
		t.Errorf("expected provider1 (first seen) to be primary, got %s", merged[0][0].Provenance.Provider)
	}
}
