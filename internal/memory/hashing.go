package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// CanonicalHash identifies the immutable content/provenance of an entry. It
// deliberately excludes lifecycle state, review metadata, timestamps, ID and
// optimistic-concurrency revision so approval/deprecation do not change the
// identity of what was reviewed.
func (e Entry) CanonicalHash() (string, error) {
	refs := append([]EvidenceRef(nil), e.EvidenceRefs...)
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].Reference == refs[j].Reference {
			return refs[i].Digest < refs[j].Digest
		}
		return refs[i].Reference < refs[j].Reference
	})
	body := struct {
		SchemaVersion     string                `json:"schema_version"`
		OrganizationID    string                `json:"organization_id"`
		RoleID            string                `json:"role_id"`
		Category          string                `json:"category"`
		Problem           string                `json:"problem"`
		Correction        string                `json:"correction"`
		SourceRunID       int64                 `json:"source_run_id"`
		EvidenceRefs      []EvidenceRef         `json:"evidence_refs"`
		Classification    ContentClassification `json:"classification"`
		SupersedesEntryID string                `json:"supersedes_entry_id,omitempty"`
	}{
		SchemaVersion:     "organizational-memory.v1",
		OrganizationID:    strings.TrimSpace(e.OrganizationID),
		RoleID:            strings.TrimSpace(e.RoleID),
		Category:          strings.TrimSpace(e.Category),
		Problem:           strings.TrimSpace(e.Problem),
		Correction:        strings.TrimSpace(e.Correction),
		SourceRunID:       e.SourceRunID,
		EvidenceRefs:      refs,
		Classification:    e.Classification,
		SupersedesEntryID: strings.TrimSpace(e.SupersedesEntryID),
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("canonicalize memory entry: %w", err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
