package memory

import (
	"fmt"
	"strings"
	"time"
)

type Status string

const (
	StatusCandidate  Status = "candidate"
	StatusApproved   Status = "approved"
	StatusDeprecated Status = "deprecated"
	StatusArchived   Status = "archived"
	StatusRejected   Status = "rejected"
)

func (s Status) Valid() bool {
	switch s {
	case StatusCandidate, StatusApproved, StatusDeprecated, StatusArchived, StatusRejected:
		return true
	default:
		return false
	}
}

type DataClass string

const (
	DataPublic         DataClass = "public"
	DataOrganizational DataClass = "organizational"
	DataSanitized      DataClass = "sanitized"
	DataSecret         DataClass = "secret"
	DataClinical       DataClass = "clinical"
)

func (c DataClass) Valid() bool {
	switch c {
	case DataPublic, DataOrganizational, DataSanitized, DataSecret, DataClinical:
		return true
	default:
		return false
	}
}

func (c DataClass) AllowedInOrganizationalMemory() bool {
	switch c {
	case DataPublic, DataOrganizational, DataSanitized:
		return true
	default:
		return false
	}
}

type EvidenceRef struct {
	Reference string `json:"reference"`
	Digest    string `json:"digest"`
}

func (e EvidenceRef) Validate() error {
	if strings.TrimSpace(e.Reference) == "" {
		return fmt.Errorf("%w: reference is required", ErrInvalidEvidenceRef)
	}
	if strings.TrimSpace(e.Digest) == "" {
		return fmt.Errorf("%w: digest is required", ErrInvalidEvidenceRef)
	}
	return nil
}

// ContentClassification is the publication-boundary attestation that the
// proposed memory payload is safe to retain as organizational memory. The
// durable adapter must verify its EvidenceRef against an authoritative source;
// the pure domain only validates its shape and fail-closed data-class policy.
type ContentClassification struct {
	DataClass    DataClass `json:"data_class"`
	ClassifierID string    `json:"classifier_id"`
	EvidenceRef  string    `json:"evidence_ref"`
	ClassifiedAt time.Time `json:"classified_at"`
}

func (c ContentClassification) Validate() error {
	if !c.DataClass.Valid() {
		return fmt.Errorf("%w: unknown data class %q", ErrInvalidClassification, c.DataClass)
	}
	if !c.DataClass.AllowedInOrganizationalMemory() {
		return fmt.Errorf("%w: %s", ErrForbiddenDataClass, c.DataClass)
	}
	if strings.TrimSpace(c.ClassifierID) == "" {
		return fmt.Errorf("%w: classifier_id is required", ErrInvalidClassification)
	}
	if strings.TrimSpace(c.EvidenceRef) == "" {
		return fmt.Errorf("%w: evidence_ref is required", ErrInvalidClassification)
	}
	if c.ClassifiedAt.IsZero() {
		return fmt.Errorf("%w: classified_at is required", ErrInvalidClassification)
	}
	return nil
}

type Entry struct {
	ID                string                `json:"id"`
	OrganizationID    string                `json:"organization_id"`
	RoleID            string                `json:"role_id"`
	Category          string                `json:"category"`
	Problem           string                `json:"problem"`
	Correction        string                `json:"correction"`
	SourceRunID       int64                 `json:"source_run_id"`
	EvidenceRefs      []EvidenceRef         `json:"evidence_refs"`
	Status            Status                `json:"status"`
	ProposedBy        string                `json:"proposed_by"`
	ReviewerID        string                `json:"reviewer_id,omitempty"`
	Classification    ContentClassification `json:"classification"`
	SupersedesEntryID string                `json:"supersedes_entry_id,omitempty"`
	Revision          int64                 `json:"revision"`
	CreatedAt         time.Time             `json:"created_at"`
	UpdatedAt         time.Time             `json:"updated_at"`
	ReviewedAt        *time.Time            `json:"reviewed_at,omitempty"`
}

func (e Entry) Validate() error {
	if strings.TrimSpace(e.ID) == "" || strings.TrimSpace(e.OrganizationID) == "" || strings.TrimSpace(e.RoleID) == "" {
		return fmt.Errorf("%w: id, organization_id, and role_id are required", ErrInvalidEntry)
	}
	if strings.TrimSpace(e.Category) == "" || strings.TrimSpace(e.Problem) == "" || strings.TrimSpace(e.Correction) == "" {
		return fmt.Errorf("%w: category, problem, and correction are required", ErrInvalidEntry)
	}
	if e.SourceRunID <= 0 {
		return fmt.Errorf("%w: source_run_id must be positive", ErrInvalidEntry)
	}
	if len(e.EvidenceRefs) == 0 {
		return fmt.Errorf("%w: at least one evidence reference is required", ErrInvalidEntry)
	}
	seen := make(map[string]struct{}, len(e.EvidenceRefs))
	for _, ref := range e.EvidenceRefs {
		if err := ref.Validate(); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidEntry, err)
		}
		key := strings.TrimSpace(ref.Reference)
		if _, exists := seen[key]; exists {
			return fmt.Errorf("%w: duplicate evidence reference %q", ErrInvalidEntry, key)
		}
		seen[key] = struct{}{}
	}
	if !e.Status.Valid() {
		return fmt.Errorf("%w: invalid status %q", ErrInvalidEntry, e.Status)
	}
	if strings.TrimSpace(e.ProposedBy) == "" {
		return fmt.Errorf("%w: proposed_by is required", ErrInvalidEntry)
	}
	if err := e.Classification.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidEntry, err)
	}
	if e.SupersedesEntryID == e.ID && e.SupersedesEntryID != "" {
		return fmt.Errorf("%w: an entry cannot supersede itself", ErrInvalidEntry)
	}
	if e.Revision <= 0 {
		return fmt.Errorf("%w: revision must be positive", ErrInvalidEntry)
	}
	if e.CreatedAt.IsZero() || e.UpdatedAt.IsZero() || e.UpdatedAt.Before(e.CreatedAt) {
		return fmt.Errorf("%w: created_at and a nondecreasing updated_at are required", ErrInvalidEntry)
	}
	if e.Classification.ClassifiedAt.After(e.CreatedAt) {
		return fmt.Errorf("%w: classification must exist before candidate creation", ErrInvalidEntry)
	}

	reviewed := e.Status != StatusCandidate
	if reviewed {
		if strings.TrimSpace(e.ReviewerID) == "" || e.ReviewedAt == nil || e.ReviewedAt.IsZero() {
			return fmt.Errorf("%w: reviewed states require reviewer_id and reviewed_at", ErrInvalidEntry)
		}
		if e.ReviewedAt.Before(e.CreatedAt) || e.ReviewedAt.After(e.UpdatedAt) {
			return fmt.Errorf("%w: reviewed_at must be within the entry lifetime", ErrInvalidEntry)
		}
	} else if e.ReviewerID != "" || e.ReviewedAt != nil {
		return fmt.Errorf("%w: candidate cannot already contain review metadata", ErrInvalidEntry)
	}
	return nil
}

type ReviewOutcome string

const (
	ReviewApprove ReviewOutcome = "approve"
	ReviewReject  ReviewOutcome = "reject"
)

func (o ReviewOutcome) Valid() bool { return o == ReviewApprove || o == ReviewReject }

type Review struct {
	Outcome    ReviewOutcome `json:"outcome"`
	ReviewerID string        `json:"reviewer_id"`
}

func (r Review) Validate() error {
	if !r.Outcome.Valid() {
		return fmt.Errorf("%w: invalid outcome %q", ErrInvalidReview, r.Outcome)
	}
	if strings.TrimSpace(r.ReviewerID) == "" {
		return fmt.Errorf("%w: reviewer_id is required", ErrInvalidReview)
	}
	return nil
}
