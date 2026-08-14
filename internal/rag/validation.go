package rag

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/dataclassifier"
)

var (
	idPattern     = regexp.MustCompile(`^[a-z0-9]+(?:[-_][a-z0-9]+)*$`)
	roleIDPattern = regexp.MustCompile(`^[a-z0-9]+(?:[-_][a-z0-9]+)*/[a-z0-9]+(?:[-_][a-z0-9]+)*$`)
	digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

func (d KnowledgeDocument) Validate() error {
	if !idPattern.MatchString(d.ID) || !idPattern.MatchString(d.OrganizationID) {
		return fmt.Errorf("%w: invalid id or organization", ErrInvalidDocument)
	}
	if !d.NamespaceKind.Valid() || strings.TrimSpace(d.NamespaceID) == "" {
		return fmt.Errorf("%w: invalid namespace", ErrInvalidDocument)
	}
	if !roleIDPattern.MatchString(d.CreatedByRole) {
		return fmt.Errorf("%w: invalid creator role", ErrInvalidDocument)
	}
	if d.CreatedAt.IsZero() {
		return fmt.Errorf("%w: created_at is required", ErrInvalidDocument)
	}
	return nil
}

func (a AdmissionAttestation) Validate() error {
	if !a.DataClass.Valid() {
		return fmt.Errorf("%w: invalid data class", ErrInvalidAdmission)
	}
	if !a.DataClass.AllowedInApprovedKnowledge() {
		return fmt.Errorf("%w: data class %q cannot become approved knowledge", ErrForbiddenDataClass, a.DataClass)
	}
	if strings.TrimSpace(a.AttestedBy) == "" || strings.TrimSpace(a.SourceBoundary) == "" || strings.TrimSpace(a.EvidenceRef) == "" || a.AttestedAt.IsZero() {
		return fmt.Errorf("%w: admission attestation is incomplete", ErrInvalidAdmission)
	}
	// RAG-INTEGRITY-001 defense in depth: Service.Propose is the one path
	// that canonicalizes AttestedAt (UTC, microsecond-truncated) before
	// ComputeCanonicalHash ever sees it, but the repository calls
	// Validate() again on every load -- a KnowledgeVersion constructed
	// any other way (a future caller that bypasses Propose, a test
	// fixture) must not be able to persist a value ComputeCanonicalHash
	// would hash one way now and re-hash a different way after a
	// Postgres round-trip. Rejecting non-UTC and sub-microsecond
	// precision here means that can never reach storage in the first
	// place, not just in the one call site that currently constructs it.
	if a.AttestedAt.Location() != time.UTC {
		return fmt.Errorf("%w: admission attested_at must be UTC", ErrInvalidAdmission)
	}
	if a.AttestedAt.Nanosecond()%1000 != 0 {
		return fmt.Errorf("%w: admission attested_at must not carry sub-microsecond precision (would not survive a Postgres timestamptz round-trip)", ErrInvalidAdmission)
	}
	if a.DataClass == DataSanitized && strings.TrimSpace(a.SanitizationEvidenceRef) == "" {
		return fmt.Errorf("%w: sanitized data class requires sanitization evidence", ErrInvalidAdmission)
	}
	if a.DataClass != DataSanitized && strings.TrimSpace(a.SanitizationEvidenceRef) != "" {
		return fmt.Errorf("%w: only sanitized data class may carry sanitization evidence", ErrInvalidAdmission)
	}
	return nil
}

func (v KnowledgeVersion) Validate() error {
	if strings.TrimSpace(v.ID) == "" || !idPattern.MatchString(v.DocumentID) || !idPattern.MatchString(v.OrganizationID) {
		return fmt.Errorf("%w: invalid identity", ErrInvalidVersion)
	}
	if !v.NamespaceKind.Valid() || strings.TrimSpace(v.NamespaceID) == "" {
		return fmt.Errorf("%w: invalid namespace", ErrInvalidVersion)
	}
	if v.Version <= 0 || !v.Lifecycle.Valid() || v.Revision <= 0 {
		return fmt.Errorf("%w: invalid version, lifecycle, or revision", ErrInvalidVersion)
	}
	if n := len(strings.TrimSpace(v.Title)); n < 1 || n > 240 {
		return fmt.Errorf("%w: title must contain 1 to 240 bytes", ErrInvalidVersion)
	}
	if n := len(v.Body); n < 1 || n > 1<<20 {
		return fmt.Errorf("%w: body must contain 1 to 1048576 bytes", ErrInvalidVersion)
	}
	if !v.SourceKind.Valid() || strings.TrimSpace(v.SourceReference) == "" {
		return fmt.Errorf("%w: invalid source", ErrInvalidVersion)
	}
	if !roleIDPattern.MatchString(v.ProposedBy) {
		return fmt.Errorf("%w: invalid proposer role", ErrInvalidVersion)
	}
	if err := v.Admission.Validate(); err != nil {
		return err
	}
	// ARCH-BOUNDARY-001: no clinical cross-check here by design -- see
	// cmd/orgctl/rag.go's runRAGIngestPDF for the full rationale. This
	// package has no clinical data concept; DataClinical remains a valid
	// DataClass value only so AllowedInApprovedKnowledge's existing
	// architectural rejection of it keeps working, not because content
	// is ever expected to be classified as clinical here.
	if finding := dataclassifier.Detect(v.Body); finding.Secret && v.Admission.DataClass != DataSecret {
		return fmt.Errorf("%w: body matches a %s but is declared %q", ErrForbiddenDataClass, finding.SecretReason, v.Admission.DataClass)
	}
	if v.ContentHash != ContentHash(v.Body) {
		return fmt.Errorf("%w: content hash must equal the hash of the normalized body", ErrInvalidVersion)
	}
	if !digestPattern.MatchString(v.ContentHash) || !digestPattern.MatchString(v.CanonicalHash) {
		return fmt.Errorf("%w: invalid canonical digest", ErrInvalidVersion)
	}
	expectedCanonicalHash, err := v.ComputeCanonicalHash()
	if err != nil {
		return err
	}
	if expectedCanonicalHash != v.CanonicalHash {
		return fmt.Errorf("%w: canonical version hash mismatch", ErrSourceDrift)
	}
	if v.SupersedesVersionID == v.ID && v.SupersedesVersionID != "" {
		return fmt.Errorf("%w: version cannot supersede itself", ErrInvalidVersion)
	}
	if v.CreatedAt.IsZero() || v.UpdatedAt.IsZero() || v.UpdatedAt.Before(v.CreatedAt) {
		return fmt.Errorf("%w: invalid timestamps", ErrInvalidVersion)
	}
	seen := map[string]struct{}{}
	for _, ref := range v.EvidenceRefs {
		if strings.TrimSpace(ref.Reference) == "" || strings.TrimSpace(ref.Digest) == "" {
			return fmt.Errorf("%w: invalid evidence reference", ErrInvalidVersion)
		}
		if _, exists := seen[ref.Reference]; exists {
			return fmt.Errorf("%w: duplicate evidence reference %q", ErrInvalidVersion, ref.Reference)
		}
		seen[ref.Reference] = struct{}{}
	}
	switch v.Lifecycle {
	case LifecycleCandidate:
		if v.ReviewerID != "" || v.ReviewedAt != nil {
			return fmt.Errorf("%w: candidate cannot already carry review provenance", ErrInvalidVersion)
		}
	default:
		if !roleIDPattern.MatchString(v.ReviewerID) || v.ReviewedAt == nil || v.ReviewedAt.IsZero() {
			return fmt.Errorf("%w: reviewed knowledge requires reviewer provenance", ErrInvalidReview)
		}
	}
	return nil
}
