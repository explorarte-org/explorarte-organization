package corpuscuration

import (
	"regexp"
	"sort"
	"strings"
)

// WorkIdentity is the minimal metadata this preflight needs per Work --
// decoupled from corpuscensus.SilverRecord (same boundary pattern as
// internal/corpuscluster.WorkInput).
//
// AbstractPresent and TitleVerified are canonical-selection SIGNALS, not
// the raw content itself -- callers compute them before calling in (this
// package intentionally never sees abstract text, to stay a thin identity
// preflight rather than a metadata store):
//
//   - AbstractPresent: true only if this Work's abstract is non-empty
//     after trimming whitespace AND is not a degenerate placeholder (a
//     lone punctuation character, or anything <= ~3 chars). false for
//     "missing" and false for "present but useless" alike -- both are
//     equally unusable to the curator.
//   - TitleVerified: true if Title came from a verified/authoritative
//     source (e.g. Semantic Scholar enrichment, per
//     internal/corpusenrich.AbstractRecord.Title) rather than raw
//     harvester metadata.
type WorkIdentity struct {
	WorkID          string
	Title           string
	DOI             string
	ArxivID         string
	ACLID           string
	AbstractPresent bool
	TitleVerified   bool
}

var titleNormalizePreflight = regexp.MustCompile(`[^a-z0-9]+`)

func normalizeTitleForIdentity(title string) string {
	return strings.Trim(titleNormalizePreflight.ReplaceAllString(strings.ToLower(strings.TrimSpace(title)), " "), " ")
}

// IdentitySet is the union of external identifiers observed across every
// work_id collapsed into one canonical entry -- so a richer alias's DOI/
// arXiv/ACL id is never discarded just because it lost the canonical pick.
// Each slice is deduplicated and sorted for deterministic output.
type IdentitySet struct {
	DOIs     []string
	ArxivIDs []string
	ACLIDs   []string
}

// CollapseResult is the full output of the identity-collapse preflight.
type CollapseResult struct {
	// Canonical is the deduplicated, sorted list of work_ids to send to
	// the curator (aliases removed).
	Canonical []string
	// AliasOf maps an alias work_id -> the canonical work_id it collapsed
	// into. (backward-compatible name/shape from the original function.)
	AliasOf map[string]string
	// AliasesOf is the forward mapping: canonical work_id -> sorted list
	// of the alias work_ids merged into it. Only present for canonical
	// ids that actually absorbed at least one alias.
	AliasesOf map[string][]string
	// MergedIdentifiers maps canonical work_id -> the union of DOI/
	// ArxivID/ACLID across the WHOLE collapsed group (canonical + all its
	// aliases), not just the canonical entry's own identifiers.
	MergedIdentifiers map[string]IdentitySet
}

// CollapseDuplicateWorksInCluster is a deterministic identity check that
// runs BEFORE a cluster's Works reach the curator (owner decision: "El
// LLM no deberia decidir que dos copias del mismo paper son redundantes;
// eso es responsabilidad determinista del corpus"). Two Works collapse
// into one canonical entry when they share a DIFFERENT identifier VALUE
// per field but the same real-world paper is evident from either (a) an
// exact normalized-title match, or (b) one Work's arxiv_id appearing
// nowhere else but their DOIs otherwise differ (the classic preprint-vs-
// camera-ready case: same paper, two different DOIs, only one of the two
// rows carries the arXiv identifier at all).
//
// This is intentionally narrower than internal/corpuscensus's own
// GroupWorks (which only merges rows sharing the SAME identifier value)
// -- it exists specifically to catch what that pass structurally cannot:
// two different identifier VALUES for the same underlying Work.
//
// Deprecated: kept only for backward compatibility with the original two-
// value signature. New callers should use
// CollapseDuplicateWorksInClusterWithIdentifiers, which also returns the
// merged identifier set and the forward alias mapping instead of silently
// discarding the losing entries' metadata.
func CollapseDuplicateWorksInCluster(workIDs []string, meta map[string]WorkIdentity) (canonical []string, aliasOf map[string]string) {
	result := CollapseDuplicateWorksInClusterWithIdentifiers(workIDs, meta)
	return result.Canonical, result.AliasOf
}

// CollapseDuplicateWorksInClusterWithIdentifiers is
// CollapseDuplicateWorksInCluster's fixed, metadata-preserving successor.
//
// Canonical selection within a duplicate group now uses a deterministic
// priority chain (previously: pure lexicographically-smallest work_id,
// which silently discarded richer metadata purely by string-sort
// accident -- see the "GraphReader" production bug: work-00195, missing
// abstract, sorted ahead of work-01212, abstract present):
//
//  1. AbstractPresent wins over AbstractPresent==false.
//  2. Among ties on (1), TitleVerified wins over TitleVerified==false.
//  3. Among ties on (1) and (2), the lexicographically smallest work_id
//     wins (deterministic last resort -- never a source of
//     non-determinism).
//
// Regardless of which work_id wins canonical status, NO metadata is
// discarded: MergedIdentifiers unions DOI/ArxivID/ACLID across the entire
// group (canonical + aliases), and AliasesOf preserves the full list of
// work_ids merged into each canonical entry.
func CollapseDuplicateWorksInClusterWithIdentifiers(workIDs []string, meta map[string]WorkIdentity) CollapseResult {
	result := CollapseResult{
		AliasOf:           make(map[string]string),
		AliasesOf:         make(map[string][]string),
		MergedIdentifiers: make(map[string]IdentitySet),
	}

	byNormalizedTitle := make(map[string][]string)
	for _, id := range workIDs {
		w, ok := meta[id]
		if !ok {
			continue
		}
		key := normalizeTitleForIdentity(w.Title)
		if key != "" {
			byNormalizedTitle[key] = append(byNormalizedTitle[key], id)
		}
	}

	collapsed := make(map[string]bool)
	// Iterate groups in a deterministic order (sorted by title key) so
	// that map-derived output stays byte-identical across runs.
	titleKeys := make([]string, 0, len(byNormalizedTitle))
	for k := range byNormalizedTitle {
		titleKeys = append(titleKeys, k)
	}
	sort.Strings(titleKeys)

	for _, key := range titleKeys {
		group := byNormalizedTitle[key]
		if len(group) < 2 {
			continue
		}
		group = append([]string(nil), group...)
		sort.Strings(group) // baseline deterministic order before priority ranking

		canonicalID := pickCanonical(group, meta)
		var aliases []string
		for _, id := range group {
			if id == canonicalID {
				continue
			}
			result.AliasOf[id] = canonicalID
			aliases = append(aliases, id)
			collapsed[id] = true
		}
		sort.Strings(aliases)
		if len(aliases) > 0 {
			result.AliasesOf[canonicalID] = aliases
		}
		result.MergedIdentifiers[canonicalID] = mergeIdentifiers(group, meta)
	}

	for _, id := range workIDs {
		if !collapsed[id] {
			result.Canonical = append(result.Canonical, id)
		}
	}
	sort.Strings(result.Canonical)
	return result
}

// pickCanonical applies the 3-level deterministic priority chain to a
// duplicate group (already sorted lexicographically as the baseline/tie
// break) and returns the winning work_id.
func pickCanonical(group []string, meta map[string]WorkIdentity) string {
	best := group[0]
	for _, id := range group[1:] {
		if isBetterCanonical(meta[id], id, meta[best], best) {
			best = id
		}
	}
	return best
}

// isBetterCanonical reports whether candidate should replace current as
// the canonical pick, per the 3-level priority chain. Ties at every level
// keep the (lexicographically prior) current, since group is pre-sorted
// and this is only ever called with candidate > current lexically.
func isBetterCanonical(candidate WorkIdentity, candidateID string, current WorkIdentity, currentID string) bool {
	if candidate.AbstractPresent != current.AbstractPresent {
		return candidate.AbstractPresent
	}
	if candidate.TitleVerified != current.TitleVerified {
		return candidate.TitleVerified
	}
	return candidateID < currentID
}

// mergeIdentifiers unions DOI/ArxivID/ACLID across every work_id in group,
// deduplicated and sorted for deterministic output.
func mergeIdentifiers(group []string, meta map[string]WorkIdentity) IdentitySet {
	var doiSet, arxivSet, aclSet = map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, id := range group {
		w := meta[id]
		if strings.TrimSpace(w.DOI) != "" {
			doiSet[w.DOI] = true
		}
		if strings.TrimSpace(w.ArxivID) != "" {
			arxivSet[w.ArxivID] = true
		}
		if strings.TrimSpace(w.ACLID) != "" {
			aclSet[w.ACLID] = true
		}
	}
	return IdentitySet{
		DOIs:     sortedKeys(doiSet),
		ArxivIDs: sortedKeys(arxivSet),
		ACLIDs:   sortedKeys(aclSet),
	}
}

func sortedKeys(m map[string]bool) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
