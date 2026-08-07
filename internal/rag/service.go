package rag

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

type Clock interface{ Now() time.Time }

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

type Service struct {
	clock Clock
}

func NewService(clock Clock) *Service {
	if clock == nil {
		clock = systemClock{}
	}
	return &Service{clock: clock}
}

type ProposeCommand struct {
	ID                  string
	DocumentID          string
	OrganizationID      string
	NamespaceKind       NamespaceKind
	NamespaceID         string
	Version             int64
	Title               string
	Body                string
	SourceKind          SourceKind
	SourceReference     string
	SourceRunRef        string
	EvidenceRefs        []EvidenceRef
	ProposedBy          string
	Admission           AdmissionAttestation
	SupersedesVersionID string
}

func (s *Service) Propose(command ProposeCommand) (KnowledgeVersion, error) {
	if s == nil {
		return KnowledgeVersion{}, errors.New("rag service is nil")
	}
	now := s.clock.Now().UTC()
	contentHash := ContentHash(command.Body)
	version := KnowledgeVersion{
		ID: strings.TrimSpace(command.ID), DocumentID: strings.TrimSpace(command.DocumentID), OrganizationID: strings.TrimSpace(command.OrganizationID),
		NamespaceKind: command.NamespaceKind, NamespaceID: strings.TrimSpace(command.NamespaceID), Version: command.Version,
		Title: strings.TrimSpace(command.Title), Body: command.Body, SourceKind: command.SourceKind, SourceReference: strings.TrimSpace(command.SourceReference),
		SourceRunRef: strings.TrimSpace(command.SourceRunRef), EvidenceRefs: append([]EvidenceRef(nil), command.EvidenceRefs...),
		ProposedBy: strings.TrimSpace(command.ProposedBy), Admission: command.Admission, ContentHash: contentHash,
		SupersedesVersionID: strings.TrimSpace(command.SupersedesVersionID), Lifecycle: LifecycleCandidate, Revision: 1,
		CreatedAt: now, UpdatedAt: now,
	}
	canonicalHash, err := version.ComputeCanonicalHash()
	if err != nil {
		return KnowledgeVersion{}, err
	}
	version.CanonicalHash = canonicalHash
	if err := version.Validate(); err != nil {
		return KnowledgeVersion{}, err
	}
	return version, nil
}

type ReviewOutcome string

const (
	ReviewApprove ReviewOutcome = "approve"
	ReviewReject  ReviewOutcome = "reject"
)

func (o ReviewOutcome) Valid() bool { return o == ReviewApprove || o == ReviewReject }

func (s *Service) Review(version KnowledgeVersion, outcome ReviewOutcome, reviewerID string) (KnowledgeVersion, error) {
	if !outcome.Valid() {
		return KnowledgeVersion{}, fmt.Errorf("%w: invalid review outcome %q", ErrInvalidReview, outcome)
	}
	to := LifecycleApproved
	if outcome == ReviewReject {
		to = LifecycleRejected
	}
	return s.transition(version, to, func(v *KnowledgeVersion) error {
		reviewerID = strings.TrimSpace(reviewerID)
		if reviewerID == "" {
			return fmt.Errorf("%w: reviewer_role_id is required", ErrInvalidReview)
		}
		v.ReviewerID = reviewerID
		return nil
	})
}

func (s *Service) Deprecate(version KnowledgeVersion) (KnowledgeVersion, error) {
	return s.transition(version, LifecycleDeprecated, nil)
}

func (s *Service) Archive(version KnowledgeVersion) (KnowledgeVersion, error) {
	return s.transition(version, LifecycleArchived, nil)
}

func (s *Service) transition(version KnowledgeVersion, to Lifecycle, mutate func(*KnowledgeVersion) error) (KnowledgeVersion, error) {
	if s == nil {
		return KnowledgeVersion{}, errors.New("rag service is nil")
	}
	if err := version.Validate(); err != nil {
		return KnowledgeVersion{}, err
	}
	if err := ValidateTransition(version.Lifecycle, to); err != nil {
		return KnowledgeVersion{}, err
	}
	if mutate != nil {
		if err := mutate(&version); err != nil {
			return KnowledgeVersion{}, err
		}
	}
	version.Lifecycle = to
	version.Revision++
	version.UpdatedAt = s.clock.Now().UTC()
	if version.UpdatedAt.Before(version.CreatedAt) {
		return KnowledgeVersion{}, fmt.Errorf("%w: clock moved backwards", ErrInvalidVersion)
	}
	if version.ReviewedAt == nil {
		reviewedAt := version.UpdatedAt
		version.ReviewedAt = &reviewedAt
	}
	if err := version.Validate(); err != nil {
		return KnowledgeVersion{}, err
	}
	return version, nil
}
