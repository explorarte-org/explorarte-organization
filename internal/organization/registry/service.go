package registry

import (
	"context"
	"errors"
	"time"
)

type Service struct {
	loader       *Loader
	repository   Repository
	organization string
	timeout      time.Duration
}

func NewService(loader *Loader, repository Repository, organizationID string, timeout time.Duration) (*Service, error) {
	if loader == nil {
		return nil, errors.New("registry service requires a canonical loader")
	}
	if organizationID == "" {
		return nil, errors.New("registry service requires an organization ID")
	}
	if timeout <= 0 {
		return nil, errors.New("registry service timeout must be greater than zero")
	}
	return &Service{loader: loader, repository: repository, organization: organizationID, timeout: timeout}, nil
}

func (s *Service) ValidateCanonical() (Snapshot, ValidationReport, error) { return s.loader.Load() }

func (s *Service) CompareCanonical(ctx context.Context) (CompareResult, error) {
	canonical, report, err := s.loader.Load()
	if err != nil {
		return CompareResult{}, err
	}
	if !report.Valid() {
		return CompareResult{}, &ValidationError{Report: report}
	}
	if s.repository == nil {
		return CompareResult{Canonical: canonical, Diff: CompareSnapshots(nil, canonical), Synchronized: false}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	current, err := s.repository.LoadCurrentSnapshot(ctx, s.organization)
	if err != nil {
		return CompareResult{}, err
	}
	diff := CompareSnapshots(current, canonical)
	var revision *Revision
	if current != nil {
		revision, err = s.repository.GetCurrentRevision(ctx, s.organization)
		if err != nil {
			return CompareResult{}, err
		}
	}
	return CompareResult{Canonical: canonical, CurrentRevision: revision, Diff: diff, Synchronized: current != nil && current.CanonicalHash == canonical.CanonicalHash && !diff.HasChanges()}, nil
}

func (s *Service) SynchronizeCanonical(ctx context.Context, apply bool) (SyncResult, error) {
	comparison, err := s.CompareCanonical(ctx)
	if err != nil {
		return SyncResult{}, err
	}
	result := SyncResult{CanonicalHash: comparison.Canonical.CanonicalHash, Diff: comparison.Diff}
	if comparison.CurrentRevision != nil {
		result.PreviousHash = comparison.CurrentRevision.CanonicalHash
	}
	if comparison.Synchronized {
		result.NoOp = true
		return result, nil
	}
	if !apply {
		return result, nil
	}
	if s.repository == nil {
		return SyncResult{}, errors.New("registry synchronization requires a repository")
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	return s.repository.Apply(ctx, comparison.Canonical)
}

func (s *Service) GetOrganization(ctx context.Context) (Organization, error) {
	if s.repository == nil {
		return Organization{}, errors.New("registry repository is not configured")
	}
	return s.repository.GetOrganization(ctx, s.organization)
}
func (s *Service) ListUnits(ctx context.Context) ([]Unit, error) {
	if s.repository == nil {
		return nil, errors.New("registry repository is not configured")
	}
	return s.repository.ListUnits(ctx, s.organization)
}
func (s *Service) GetUnit(ctx context.Context, id string) (Unit, error) {
	if s.repository == nil {
		return Unit{}, errors.New("registry repository is not configured")
	}
	return s.repository.GetUnit(ctx, s.organization, id)
}
func (s *Service) GetRole(ctx context.Context, id string) (Role, error) {
	if s.repository == nil {
		return Role{}, errors.New("registry repository is not configured")
	}
	return s.repository.GetRole(ctx, s.organization, id)
}
func (s *Service) ListRoles(ctx context.Context, filter RoleFilter) ([]Role, error) {
	if s.repository == nil {
		return nil, errors.New("registry repository is not configured")
	}
	return s.repository.ListRoles(ctx, s.organization, filter)
}
func (s *Service) GetLeader(ctx context.Context, unit string) (Role, error) {
	if s.repository == nil {
		return Role{}, errors.New("registry repository is not configured")
	}
	return s.repository.GetLeader(ctx, s.organization, unit)
}
func (s *Service) ListWorkers(ctx context.Context, unit string) ([]Role, error) {
	if s.repository == nil {
		return nil, errors.New("registry repository is not configured")
	}
	return s.repository.ListWorkers(ctx, s.organization, unit)
}
func (s *Service) GetCurrentRevision(ctx context.Context) (*Revision, error) {
	if s.repository == nil {
		return nil, errors.New("registry repository is not configured")
	}
	return s.repository.GetCurrentRevision(ctx, s.organization)
}
