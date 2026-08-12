package corpuscuration

import (
	"reflect"
	"testing"
)

func TestCollapseDuplicateWorksInClusterMergesExactTitleMatch(t *testing.T) {
	meta := map[string]WorkIdentity{
		"work-01054": {WorkID: "work-01054", Title: "PyramidInfer: Pyramid KV Cache Compression for High-throughput LLM Inference", DOI: "10.48550/arxiv.2405.12532", ArxivID: "2405.12532"},
		"work-00154": {WorkID: "work-00154", Title: "PyramidInfer: Pyramid KV Cache Compression for High-throughput LLM Inference", DOI: "10.18653/v1/2024.findings-acl.195"},
		"work-99999": {WorkID: "work-99999", Title: "A Completely Different Paper"},
	}
	canonical, aliasOf := CollapseDuplicateWorksInCluster([]string{"work-01054", "work-00154", "work-99999"}, meta)
	if len(canonical) != 2 {
		t.Fatalf("expected 2 canonical works after collapsing the duplicate, got %d: %v", len(canonical), canonical)
	}
	// Neither work carries AbstractPresent/TitleVerified signals, so this
	// falls all the way through to the lexicographic tie-break -- same
	// outcome as the original (pre-fix) behavior, preserved here as a
	// backward-compatibility check on the deprecated two-value wrapper.
	if aliasOf["work-01054"] != "work-00154" {
		t.Fatalf("expected work-01054 to alias to work-00154 (lexically smaller), got %q", aliasOf["work-01054"])
	}
}

func TestCollapseDuplicateWorksInClusterLeavesDistinctTitlesAlone(t *testing.T) {
	meta := map[string]WorkIdentity{
		"a": {WorkID: "a", Title: "First Paper"},
		"b": {WorkID: "b", Title: "Second Paper"},
	}
	canonical, aliasOf := CollapseDuplicateWorksInCluster([]string{"a", "b"}, meta)
	if len(canonical) != 2 || len(aliasOf) != 0 {
		t.Fatalf("expected no collapsing, got canonical=%v aliasOf=%v", canonical, aliasOf)
	}
}

// TestCollapseDuplicateWorksInClusterGraphReaderRegression is the exact
// reference case from the real production bug: "GraphReader" harvested
// twice as work-00195 (title only, abstract missing from enrichment) and
// work-01212 (title + abstract present). Because "work-00195" <
// "work-01212" lexicographically, the OLD pure-lexicographic logic picked
// work-00195 as canonical, discarding the richer abstract. The fix must
// now pick work-01212 (abstract present) as canonical.
func TestCollapseDuplicateWorksInClusterGraphReaderRegression(t *testing.T) {
	meta := map[string]WorkIdentity{
		"work-00195": {WorkID: "work-00195", Title: "GraphReader: Building Graph-based Agent to Enhance Long-Context Abilities", DOI: "10.48550/arxiv.2406.14550", AbstractPresent: false},
		"work-01212": {WorkID: "work-01212", Title: "GraphReader: Building Graph-based Agent to Enhance Long-Context Abilities", ArxivID: "2406.14550", AbstractPresent: true},
	}
	result := CollapseDuplicateWorksInClusterWithIdentifiers([]string{"work-00195", "work-01212"}, meta)

	if len(result.Canonical) != 1 || result.Canonical[0] != "work-01212" {
		t.Fatalf("expected work-01212 (abstract present) to win canonical status, got canonical=%v", result.Canonical)
	}
	if result.AliasOf["work-00195"] != "work-01212" {
		t.Fatalf("expected work-00195 to alias to work-01212, got %q", result.AliasOf["work-00195"])
	}
	if got := result.AliasesOf["work-01212"]; len(got) != 1 || got[0] != "work-00195" {
		t.Fatalf("expected AliasesOf[work-01212] = [work-00195], got %v", got)
	}

	merged := result.MergedIdentifiers["work-01212"]
	if !containsString(merged.DOIs, "10.48550/arxiv.2406.14550") {
		t.Fatalf("expected merged DOIs to include work-00195's DOI, got %v", merged.DOIs)
	}
	if !containsString(merged.ArxivIDs, "2406.14550") {
		t.Fatalf("expected merged ArxivIDs to include work-01212's arXiv id, got %v", merged.ArxivIDs)
	}
}

// TestCollapseDuplicateWorksInClusterTitleVerifiedTiebreak checks priority
// level 2: when AbstractPresent ties, a verified-title source wins over an
// unverified/raw one.
func TestCollapseDuplicateWorksInClusterTitleVerifiedTiebreak(t *testing.T) {
	meta := map[string]WorkIdentity{
		"work-a": {WorkID: "work-a", Title: "Some Paper Title", AbstractPresent: true, TitleVerified: false},
		"work-b": {WorkID: "work-b", Title: "Some Paper Title", AbstractPresent: true, TitleVerified: true},
	}
	result := CollapseDuplicateWorksInClusterWithIdentifiers([]string{"work-a", "work-b"}, meta)
	if len(result.Canonical) != 1 || result.Canonical[0] != "work-b" {
		t.Fatalf("expected work-b (title verified) to win canonical status among AbstractPresent ties, got %v", result.Canonical)
	}
}

// TestCollapseDuplicateWorksInClusterIdentifiersUnionedAcrossThreeWayGroup
// checks that DOI/ArxivID/ACLID are unioned across a 3+-way duplicate
// group, not just taken from the canonical winner.
func TestCollapseDuplicateWorksInClusterIdentifiersUnionedAcrossThreeWayGroup(t *testing.T) {
	meta := map[string]WorkIdentity{
		"work-01": {WorkID: "work-01", Title: "Triple Harvested Paper", DOI: "10.1/one", AbstractPresent: true},
		"work-02": {WorkID: "work-02", Title: "Triple Harvested Paper", ArxivID: "2401.00002", AbstractPresent: false},
		"work-03": {WorkID: "work-03", Title: "Triple Harvested Paper", ACLID: "2024.acl-long.3", AbstractPresent: false},
	}
	result := CollapseDuplicateWorksInClusterWithIdentifiers([]string{"work-01", "work-02", "work-03"}, meta)

	if len(result.Canonical) != 1 || result.Canonical[0] != "work-01" {
		t.Fatalf("expected work-01 (only one with AbstractPresent) to win canonical status, got %v", result.Canonical)
	}
	merged := result.MergedIdentifiers["work-01"]
	if !reflect.DeepEqual(merged.DOIs, []string{"10.1/one"}) {
		t.Fatalf("expected DOIs=[10.1/one], got %v", merged.DOIs)
	}
	if !reflect.DeepEqual(merged.ArxivIDs, []string{"2401.00002"}) {
		t.Fatalf("expected ArxivIDs=[2401.00002], got %v", merged.ArxivIDs)
	}
	if !reflect.DeepEqual(merged.ACLIDs, []string{"2024.acl-long.3"}) {
		t.Fatalf("expected ACLIDs=[2024.acl-long.3], got %v", merged.ACLIDs)
	}
	aliases := result.AliasesOf["work-01"]
	if !reflect.DeepEqual(aliases, []string{"work-02", "work-03"}) {
		t.Fatalf("expected AliasesOf[work-01]=[work-02 work-03], got %v", aliases)
	}
}

// TestCollapseDuplicateWorksInClusterProvenancePreserved is a broader
// check that nothing about the alias set is silently dropped: every
// aliased work_id must be reachable both via AliasOf (backward mapping)
// and AliasesOf (forward mapping), and must not appear in Canonical.
func TestCollapseDuplicateWorksInClusterProvenancePreserved(t *testing.T) {
	meta := map[string]WorkIdentity{
		"work-a": {WorkID: "work-a", Title: "Provenance Paper", DOI: "10.1/a", AbstractPresent: true},
		"work-b": {WorkID: "work-b", Title: "Provenance Paper", ArxivID: "2401.00099", AbstractPresent: false},
		"work-c": {WorkID: "work-c", Title: "Provenance Paper", ACLID: "2024.acl-long.9", AbstractPresent: false},
		"work-d": {WorkID: "work-d", Title: "Unrelated Paper"},
	}
	result := CollapseDuplicateWorksInClusterWithIdentifiers([]string{"work-a", "work-b", "work-c", "work-d"}, meta)

	canonicalSet := map[string]bool{}
	for _, id := range result.Canonical {
		canonicalSet[id] = true
	}
	for alias, canonicalID := range result.AliasOf {
		if canonicalSet[alias] {
			t.Fatalf("alias %q must not also appear in Canonical", alias)
		}
		found := false
		for _, a := range result.AliasesOf[canonicalID] {
			if a == alias {
				found = true
			}
		}
		if !found {
			t.Fatalf("alias %q not found in AliasesOf[%q]=%v (forward mapping must match backward mapping)", alias, canonicalID, result.AliasesOf[canonicalID])
		}
	}
	if len(result.AliasOf) != 2 {
		t.Fatalf("expected exactly 2 aliases (work-b, work-c collapsed into work-a), got %v", result.AliasOf)
	}
	if !canonicalSet["work-d"] {
		t.Fatalf("expected unrelated work-d to remain in Canonical, got %v", result.Canonical)
	}
}

// TestCollapseDuplicateWorksInClusterDeterministicOutput proves that the
// same input produces byte-identical output across repeated calls (no
// map-iteration-order non-determinism leaking through).
func TestCollapseDuplicateWorksInClusterDeterministicOutput(t *testing.T) {
	meta := map[string]WorkIdentity{
		"work-05": {WorkID: "work-05", Title: "Deterministic Paper", DOI: "10.1/x", AbstractPresent: true},
		"work-04": {WorkID: "work-04", Title: "Deterministic Paper", ArxivID: "2401.00005", AbstractPresent: true},
		"work-03": {WorkID: "work-03", Title: "Deterministic Paper", ACLID: "2024.acl-long.5", AbstractPresent: false},
		"work-02": {WorkID: "work-02", Title: "Other Paper"},
		"work-01": {WorkID: "work-01", Title: "Other Paper"},
	}
	ids := []string{"work-05", "work-04", "work-03", "work-02", "work-01"}

	first := CollapseDuplicateWorksInClusterWithIdentifiers(ids, meta)
	for i := 0; i < 20; i++ {
		got := CollapseDuplicateWorksInClusterWithIdentifiers(ids, meta)
		if !reflect.DeepEqual(got, first) {
			t.Fatalf("non-deterministic output on iteration %d:\nfirst=%+v\ngot=%+v", i, first, got)
		}
	}
}

// TestCollapseDuplicateWorksInClusterOldLexicographicBehaviorIsGone is the
// explicit regression proving pure lexicographic-smallest-wins is gone: a
// case where the lexicographically smaller work_id has strictly worse
// metadata (no abstract, unverified title) and must NOT win canonical
// status anymore.
func TestCollapseDuplicateWorksInClusterOldLexicographicBehaviorIsGone(t *testing.T) {
	meta := map[string]WorkIdentity{
		"work-00001": {WorkID: "work-00001", Title: "Lexicographic Trap Paper", AbstractPresent: false, TitleVerified: false},
		"work-99999": {WorkID: "work-99999", Title: "Lexicographic Trap Paper", AbstractPresent: true, TitleVerified: true},
	}
	result := CollapseDuplicateWorksInClusterWithIdentifiers([]string{"work-00001", "work-99999"}, meta)
	if len(result.Canonical) != 1 || result.Canonical[0] != "work-99999" {
		t.Fatalf("pure lexicographic behavior detected: expected work-99999 (better metadata) to win despite being lexically larger, got %v", result.Canonical)
	}
	if result.AliasOf["work-00001"] != "work-99999" {
		t.Fatalf("expected work-00001 to alias to work-99999, got %q", result.AliasOf["work-00001"])
	}
}

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
