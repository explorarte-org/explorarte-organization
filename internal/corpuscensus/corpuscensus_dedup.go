package corpuscensus

import (
	"regexp"
	"strconv"
	"strings"
)

// ResolveWorkIdentity implements the owner's preference order (Part B
// section 4): DOI, then arXiv ID, then ACL/OpenReview, then normalized
// title+year, and only SHA-256 as a last resort when nothing else is
// present -- SHA-256 identifies an Artifact, never a Work, so a row that
// only has a hash is, at best, a Work of one unknown-identity Artifact.
func ResolveWorkIdentity(p BronzePaper) (IdentityKind, string) {
	if doi := strings.TrimSpace(p.str(p.DOI)); doi != "" {
		return IdentityDOI, "doi:" + strings.ToLower(doi)
	}
	if arxiv := strings.TrimSpace(p.str(p.ArxivID)); arxiv != "" {
		return IdentityArxiv, "arxiv:" + strings.ToLower(arxiv)
	}
	if acl := strings.TrimSpace(p.str(p.ACLID)); acl != "" {
		return IdentityACL, "acl:" + strings.ToLower(acl)
	}
	if or := strings.TrimSpace(p.str(p.OpenReviewID)); or != "" {
		return IdentityOpenReview, "openreview:" + strings.ToLower(or)
	}
	if title := normalizeTitle(p.Title); title != "" {
		year := 0
		if p.Year != nil {
			year = *p.Year
		}
		return IdentityTitleYear, titleYearKey(title, year)
	}
	if sha := strings.TrimSpace(p.str(p.SHA256)); sha != "" {
		return IdentitySHA256, "sha256:" + strings.ToLower(sha)
	}
	return "", ""
}

var normalizeTitleNonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

func normalizeTitle(title string) string {
	lower := strings.ToLower(strings.TrimSpace(title))
	if lower == "" {
		return ""
	}
	return strings.Trim(normalizeTitleNonAlnum.ReplaceAllString(lower, " "), " ")
}

func titleYearKey(normalizedTitle string, year int) string {
	if year > 0 {
		return "title_year:" + normalizedTitle + "|" + strconv.Itoa(year)
	}
	return "title_year:" + normalizedTitle + "|unknown"
}

// unionFind is a minimal disjoint-set used to group BronzePaper rows into
// Works. Two rows merge into the same Work when they share ANY non-empty
// scholarly identifier (DOI, arXiv, ACL, OpenReview) -- the harvester's
// own canonical_id is per-artifact, not per-work, so two separately
// discovered rows for the same underlying paper (e.g. one keyed by DOI,
// one by arXiv ID, before the harvester itself cross-resolved them) need
// this explicit merge step, or they would silently count as two Works.
type unionFind struct {
	parent map[string]string
}

func newUnionFind() *unionFind {
	return &unionFind{parent: make(map[string]string)}
}

func (u *unionFind) find(x string) string {
	if _, ok := u.parent[x]; !ok {
		u.parent[x] = x
	}
	root := x
	for u.parent[root] != root {
		root = u.parent[root]
	}
	for u.parent[x] != root {
		u.parent[x], x = root, u.parent[x]
	}
	return root
}

func (u *unionFind) union(a, b string) {
	ra, rb := u.find(a), u.find(b)
	if ra != rb {
		u.parent[ra] = rb
	}
}

// GroupWorks partitions bronze rows into Works by scholarly-identifier
// overlap, falling back to normalized title+year for rows with none of
// the four scholarly IDs. Rows sharing a canonical_id are already the
// same row (harvester's own primary key); this only merges DISTINCT rows
// that turn out to describe the same underlying Work.
func GroupWorks(papers []BronzePaper) map[string][]BronzePaper {
	uf := newUnionFind()
	rowKey := func(p BronzePaper) string { return "row:" + p.CanonicalID }

	for _, p := range papers {
		root := rowKey(p)
		uf.find(root)
		if doi := strings.TrimSpace(p.str(p.DOI)); doi != "" {
			uf.union(root, "id:doi:"+strings.ToLower(doi))
		}
		if arxiv := strings.TrimSpace(p.str(p.ArxivID)); arxiv != "" {
			uf.union(root, "id:arxiv:"+strings.ToLower(arxiv))
		}
		if acl := strings.TrimSpace(p.str(p.ACLID)); acl != "" {
			uf.union(root, "id:acl:"+strings.ToLower(acl))
		}
		if or := strings.TrimSpace(p.str(p.OpenReviewID)); or != "" {
			uf.union(root, "id:or:"+strings.ToLower(or))
		}
		hasScholarID := p.str(p.DOI) != "" || p.str(p.ArxivID) != "" || p.str(p.ACLID) != "" || p.str(p.OpenReviewID) != ""
		if !hasScholarID {
			if title := normalizeTitle(p.Title); title != "" {
				year := 0
				if p.Year != nil {
					year = *p.Year
				}
				uf.union(root, "id:"+titleYearKey(title, year))
			}
		}
	}

	groups := make(map[string][]BronzePaper)
	for _, p := range papers {
		root := uf.find(rowKey(p))
		groups[root] = append(groups[root], p)
	}
	return groups
}

// SelectCanonicalArtifact implements the owner's version-selection order
// (Part B section 5): publisher/venue-confirmed > highest year (proxy for
// latest revision, since the harvester does not expose arXiv version
// numbers directly) > stable order (canonical_id, lexical) as a final
// deterministic tiebreaker. Every choice's reason is recorded so it is
// auditable, not asserted.
func SelectCanonicalArtifact(group []BronzePaper) (canonical BronzePaper, reason string) {
	best := group[0]
	bestReason := "only artifact in this work group"
	for _, candidate := range group[1:] {
		candidateHasVenue := strings.TrimSpace(candidate.str(candidate.Venue)) != ""
		bestHasVenue := strings.TrimSpace(best.str(best.Venue)) != ""
		switch {
		case candidateHasVenue && !bestHasVenue:
			best, bestReason = candidate, "has a confirmed venue/publication; prior candidate did not"
		case candidateHasVenue == bestHasVenue && yearOf(candidate) > yearOf(best):
			best, bestReason = candidate, "higher publication year (proxy for a later revision)"
		case candidateHasVenue == bestHasVenue && yearOf(candidate) == yearOf(best) && candidate.CanonicalID < best.CanonicalID:
			best, bestReason = candidate, "deterministic tiebreak (lexically smaller canonical_id)"
		}
	}
	return best, bestReason
}

func yearOf(p BronzePaper) int {
	if p.Year == nil {
		return 0
	}
	return *p.Year
}
