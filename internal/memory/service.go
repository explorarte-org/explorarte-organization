package memory

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

type Clock interface{ Now() time.Time }

type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now() }

type Service struct{ clock Clock }

func NewService(clock Clock) *Service {
	if clock == nil {
		clock = SystemClock{}
	}
	return &Service{clock: clock}
}

type ProposeCommand struct {
	ID                string
	OrganizationID    string
	RoleID            string
	Category          string
	Problem           string
	Correction        string
	SourceKind        SourceKind
	SourceRunID       int64
	EvidenceRefs      []EvidenceRef
	ProposedBy        string
	Admission         AdmissionAttestation
	SupersedesEntryID string
}

func (s *Service) Propose(command ProposeCommand) (Entry, error) {
	if s == nil {
		return Entry{}, errors.New("memory service is nil")
	}
	refs := append([]EvidenceRef(nil), command.EvidenceRefs...)
	for i := range refs {
		refs[i].Reference = strings.TrimSpace(refs[i].Reference)
		refs[i].Digest = strings.TrimSpace(refs[i].Digest)
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].Reference == refs[j].Reference {
			return refs[i].Digest < refs[j].Digest
		}
		return refs[i].Reference < refs[j].Reference
	})
	admission := command.Admission
	admission.AttestedBy = strings.TrimSpace(admission.AttestedBy)
	admission.SourceBoundary = strings.TrimSpace(admission.SourceBoundary)
	admission.EvidenceRef = strings.TrimSpace(admission.EvidenceRef)
	admission.SanitizationEvidenceRef = strings.TrimSpace(admission.SanitizationEvidenceRef)
	if !admission.AttestedAt.IsZero() {
		admission.AttestedAt = admission.AttestedAt.UTC()
	}

	now := s.clock.Now().UTC()
	entry := Entry{
		ID:                strings.TrimSpace(command.ID),
		OrganizationID:    strings.TrimSpace(command.OrganizationID),
		RoleID:            strings.TrimSpace(command.RoleID),
		Category:          strings.TrimSpace(command.Category),
		Problem:           strings.TrimSpace(command.Problem),
		Correction:        strings.TrimSpace(command.Correction),
		SourceKind:        command.SourceKind,
		SourceRunID:       command.SourceRunID,
		EvidenceRefs:      refs,
		Status:            StatusCandidate,
		ProposedBy:        strings.TrimSpace(command.ProposedBy),
		Admission:         admission,
		SupersedesEntryID: strings.TrimSpace(command.SupersedesEntryID),
		Revision:          1,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if err := entry.Validate(); err != nil {
		return Entry{}, err
	}
	return entry, nil
}

func (s *Service) transition(entry Entry, to Status) (Entry, error) {
	if s == nil {
		return Entry{}, errors.New("memory service is nil")
	}
	if err := entry.Validate(); err != nil {
		return Entry{}, err
	}
	if err := ValidateTransition(entry.Status, to); err != nil {
		return Entry{}, err
	}
	entry.Status = to
	entry.Revision++
	entry.UpdatedAt = s.clock.Now().UTC()
	return entry, nil
}

func (s *Service) Review(entry Entry, review Review) (Entry, error) {
	if err := review.Validate(); err != nil {
		return Entry{}, err
	}
	// Independent of capability-matrix.yaml (G4-001): even a role holding
	// memory.approve must never be the same role that proposed this entry.
	if strings.TrimSpace(review.ReviewerID) == strings.TrimSpace(entry.ProposedBy) {
		return Entry{}, fmt.Errorf("%w: role %q", ErrSelfReview, review.ReviewerID)
	}
	var to Status
	switch review.Outcome {
	case ReviewApprove:
		to = StatusApproved
	case ReviewReject:
		to = StatusRejected
	default:
		return Entry{}, fmt.Errorf("%w: unknown outcome %q", ErrInvalidReview, review.Outcome)
	}
	updated, err := s.transition(entry, to)
	if err != nil {
		return Entry{}, err
	}
	now := updated.UpdatedAt
	updated.ReviewerID = strings.TrimSpace(review.ReviewerID)
	updated.ReviewedAt = &now
	if err := updated.Validate(); err != nil {
		return Entry{}, err
	}
	return updated, nil
}

func (s *Service) Deprecate(entry Entry) (Entry, error) {
	updated, err := s.transition(entry, StatusDeprecated)
	if err != nil {
		return Entry{}, err
	}
	if err := updated.Validate(); err != nil {
		return Entry{}, err
	}
	return updated, nil
}

func (s *Service) Archive(entry Entry) (Entry, error) {
	updated, err := s.transition(entry, StatusArchived)
	if err != nil {
		return Entry{}, err
	}
	if err := updated.Validate(); err != nil {
		return Entry{}, err
	}
	return updated, nil
}
