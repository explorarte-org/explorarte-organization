package contextengine

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
)

type ServiceConfig struct {
	OrganizationAgentPath string
	MaxTotalBytes         int
	MaxSegmentBytes       int
	MaxSegments           int
	MaxSkills             int
	MaxMemorySegments     int
	MaxRAGSegments        int
}

type contextService struct {
	config    ServiceConfig
	registry  RegistryReader
	documents DocumentLoader
	canonical CanonicalPolicyProvider
	owner     OwnerConstraintProvider
	memory    MemoryProvider
	skills    SkillProvider
	projects  ProjectContextProvider
	tasks     TaskContextProvider
	rag       RAGEvidenceProvider
	assembler Assembler
	renderer  Renderer
	store     SnapshotStore
	clock     Clock
}

func NewService(
	config ServiceConfig,
	registryReader RegistryReader,
	documents DocumentLoader,
	canonical CanonicalPolicyProvider,
	owner OwnerConstraintProvider,
	memory MemoryProvider,
	skills SkillProvider,
	projects ProjectContextProvider,
	tasks TaskContextProvider,
	rag RAGEvidenceProvider,
	assembler Assembler,
	renderer Renderer,
	store SnapshotStore,
	clock Clock,
) (Service, error) {
	if strings.TrimSpace(config.OrganizationAgentPath) == "" {
		return nil, errors.New("organization agent path is required")
	}
	if config.MaxTotalBytes <= 0 || config.MaxSegmentBytes <= 0 || config.MaxSegments <= 0 {
		return nil, errors.New("context limits must be positive")
	}
	if registryReader == nil || documents == nil || canonical == nil || owner == nil || memory == nil || skills == nil || projects == nil || tasks == nil || rag == nil || assembler == nil || renderer == nil || store == nil {
		return nil, errors.New("context service dependencies cannot be nil")
	}
	if clock == nil {
		clock = systemClock{}
	}
	return &contextService{config: config, registry: registryReader, documents: documents, canonical: canonical, owner: owner, memory: memory, skills: skills, projects: projects, tasks: tasks, rag: rag, assembler: assembler, renderer: renderer, store: store, clock: clock}, nil
}

func (s *contextService) Build(ctx context.Context, request BuildRequest) (BuildResult, error) {
	if err := ValidateBuildRequest(request); err != nil {
		return BuildResult{}, err
	}
	organization, revision, role, unit, bundle, sources, resolvedSkills, err := s.resolve(ctx, request)
	if err != nil {
		return s.rejectBuild(ctx, request, err)
	}
	_ = organization
	_ = unit
	request.OrganizationRevisionID = revision.ID
	assembly, err := s.assembler.Assemble(ctx, AssemblyInput{
		Sources: sources, MaxTotalBytes: s.config.MaxTotalBytes, MaxSegmentBytes: s.config.MaxSegmentBytes,
		MaxSegments: s.config.MaxSegments, MaxSkills: s.config.MaxSkills, MaxMemorySegments: s.config.MaxMemorySegments,
		MaxRAGSegments: s.config.MaxRAGSegments,
	})
	if err != nil {
		return s.rejectBuild(ctx, request, err)
	}
	canonicalRequest := CanonicalBuildRequest{
		Request: request, PrecedenceHash: bundle.PrecedenceHash, CanonicalBundleHash: bundle.BundleHash, Sources: sources,
		MaxTotalBytes: s.config.MaxTotalBytes, MaxSegmentBytes: s.config.MaxSegmentBytes, MaxSegments: s.config.MaxSegments,
		MaxSkills: s.config.MaxSkills, MaxMemorySegments: s.config.MaxMemorySegments, MaxRAGSegments: s.config.MaxRAGSegments,
	}
	requestHash := DigestBuildRequest(canonicalRequest)
	if existing, getErr := s.store.GetByIdempotency(ctx, request.OrganizationID, request.IdempotencyKey, true); getErr == nil {
		if !requestHashCompatible(existing.RequestHash, requestHash, canonicalRequest) {
			return BuildResult{}, ErrIdempotencyConflict
		}
		if err = s.verifySnapshotIntegrity(ctx, existing); err != nil {
			return BuildResult{}, err
		}
		return BuildResult{Snapshot: existing, Reused: true}, nil
	} else if !errors.Is(getErr, ErrSnapshotNotFound) {
		return BuildResult{}, getErr
	}
	if err = s.revalidateResolved(ctx, request, revision.ID, bundle, role, sources, resolvedSkills); err != nil {
		return s.rejectBuild(ctx, request, err)
	}
	id, err := s.store.AllocateID(ctx)
	if err != nil {
		return BuildResult{}, err
	}
	now := s.clock.Now().UTC()
	snapshot := Snapshot{
		ID: id, OrganizationID: request.OrganizationID, OrganizationRevisionID: revision.ID, ActorRoleID: request.ActorRoleID,
		Purpose: request.Purpose, ProjectRef: request.ProjectRef, TaskRef: request.TaskRef,
		TaskClass: request.TaskClass, ExecutionPurpose: request.ExecutionPurpose, ActorUnitID: request.ActorUnitID,
		IdempotencyKey: request.IdempotencyKey,
		Status:         SnapshotReady, Version: 1, RequestHash: requestHash, PrecedenceHash: bundle.PrecedenceHash,
		CanonicalBundleHash: bundle.BundleHash, SegmentCount: assembly.SegmentCount,
		IncludedSegmentCount: assembly.IncludedSegmentCount, OmittedSegmentCount: assembly.OmittedSegmentCount,
		TotalBytes: assembly.TotalBytes, CorrelationID: request.CorrelationID, CausationID: request.CausationID,
		Segments: assembly.Segments, CreatedAt: now,
	}
	rendered, err := s.renderer.Render(ctx, snapshot)
	if err != nil {
		return BuildResult{}, fmt.Errorf("render context snapshot: %w", err)
	}
	snapshot.RenderedHash = DigestCanonicalBytes(rendered)
	result, err := s.store.Create(ctx, CreateSnapshotCommand{Snapshot: snapshot, Now: now})
	if err != nil {
		return BuildResult{}, err
	}
	if result.Reused {
		if !requestHashCompatible(result.Snapshot.RequestHash, requestHash, canonicalRequest) {
			return BuildResult{}, ErrIdempotencyConflict
		}
		if err = s.verifySnapshotIntegrity(ctx, result.Snapshot); err != nil {
			return BuildResult{}, err
		}
	}
	return result, nil
}

// requestHashCompatible decides whether an already-durable snapshot's
// stored hash represents the SAME logical request as freshHash (M1.3
// section 9). existingHash may have been computed before
// TaskClass/ExecutionPurpose/ActorUnitID existed at all (a pre-M1.3 row):
// in that case existingHash matches digestBuildRequestLegacy(canonical),
// never freshHash, even though the two requests are logically identical.
// Accepting either computation is what keeps a legitimate pre-M1.3
// idempotent resume working after the upgrade, while a snapshot whose
// stored hash matches NEITHER computation is a genuine contradictory
// identity under a reused idempotency key and must still fail closed.
func requestHashCompatible(existingHash, freshHash string, canonical CanonicalBuildRequest) bool {
	if existingHash == freshHash {
		return true
	}
	return existingHash == digestBuildRequestLegacy(canonical)
}

func (s *contextService) rejectBuild(ctx context.Context, request BuildRequest, cause error) (BuildResult, error) {
	reason := ReasonOf(cause)
	if !isForbiddenSourceReason(reason) {
		return BuildResult{}, cause
	}
	recorder, ok := s.store.(ForbiddenSourceEventRecorder)
	if !ok {
		return BuildResult{}, cause
	}
	if err := recorder.RecordForbiddenSourceRejection(ctx, request, reason, s.clock.Now().UTC()); err != nil {
		return BuildResult{}, errors.Join(cause, fmt.Errorf("record forbidden source rejection: %w", err))
	}
	return BuildResult{}, cause
}

func isForbiddenSourceReason(reason ReasonCode) bool {
	switch reason {
	case ReasonForbiddenDataClass, ReasonSecretDataRejected, ReasonClinicalDataRejected,
		ReasonUnsafeInstructionSource, ReasonSourcePathEscape, ReasonSourceSymlinkEscape:
		return true
	default:
		return false
	}
}

func (s *contextService) resolve(ctx context.Context, request BuildRequest) (registry.Organization, *registry.Revision, registry.Role, registry.Unit, CanonicalBundle, []SourceRecord, []SkillRecord, error) {
	organization, err := s.registry.GetOrganization(ctx, request.OrganizationID)
	if err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			return registry.Organization{}, nil, registry.Role{}, registry.Unit{}, CanonicalBundle{}, nil, nil, Reject(ReasonOrganizationMismatch, request.OrganizationID, "organization does not exist")
		}
		return registry.Organization{}, nil, registry.Role{}, registry.Unit{}, CanonicalBundle{}, nil, nil, fmt.Errorf("get organization: %w", err)
	}
	if organization.RetiredAt != nil {
		return registry.Organization{}, nil, registry.Role{}, registry.Unit{}, CanonicalBundle{}, nil, nil, Reject(ReasonOrganizationMismatch, request.OrganizationID, "organization is retired")
	}
	revision, err := s.registry.GetCurrentRevision(ctx, request.OrganizationID)
	if err != nil {
		return registry.Organization{}, nil, registry.Role{}, registry.Unit{}, CanonicalBundle{}, nil, nil, fmt.Errorf("get active registry revision: %w", err)
	}
	if revision == nil || revision.ID != request.OrganizationRevisionID {
		return registry.Organization{}, nil, registry.Role{}, registry.Unit{}, CanonicalBundle{}, nil, nil, Reject(ReasonRevisionMismatch, request.OrganizationID, "requested organization revision is not active")
	}
	role, err := s.registry.GetRole(ctx, request.OrganizationID, request.ActorRoleID)
	if err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			return registry.Organization{}, nil, registry.Role{}, registry.Unit{}, CanonicalBundle{}, nil, nil, Reject(ReasonRoleNotFound, request.ActorRoleID, "actor role does not exist")
		}
		return registry.Organization{}, nil, registry.Role{}, registry.Unit{}, CanonicalBundle{}, nil, nil, fmt.Errorf("get actor role: %w", err)
	}
	if role.RetiredAt != nil {
		return registry.Organization{}, nil, registry.Role{}, registry.Unit{}, CanonicalBundle{}, nil, nil, Reject(ReasonRoleRetired, role.ID, "actor role is retired")
	}
	if !role.Enabled {
		return registry.Organization{}, nil, registry.Role{}, registry.Unit{}, CanonicalBundle{}, nil, nil, Reject(ReasonRoleDisabled, role.ID, "actor role is disabled")
	}
	if !role.Executable && role.ID != organization.OwnerRoleID {
		return registry.Organization{}, nil, registry.Role{}, registry.Unit{}, CanonicalBundle{}, nil, nil, Reject(ReasonRoleNotExecutable, role.ID, "actor role is not executable")
	}
	unit, err := s.registry.GetUnit(ctx, request.OrganizationID, role.UnitID)
	if err != nil {
		return registry.Organization{}, nil, registry.Role{}, registry.Unit{}, CanonicalBundle{}, nil, nil, fmt.Errorf("get actor department: %w", err)
	}
	bundle, err := s.canonical.Load(ctx)
	if err != nil {
		return registry.Organization{}, nil, registry.Role{}, registry.Unit{}, CanonicalBundle{}, nil, nil, err
	}
	sources := canonicalRecords(bundle)
	ownerSources, err := s.owner.ListApplicable(ctx, request)
	if err != nil {
		return registry.Organization{}, nil, registry.Role{}, registry.Unit{}, CanonicalBundle{}, nil, nil, fmt.Errorf("resolve owner constraints: %w", err)
	}
	for _, source := range ownerSources {
		sources = append(sources, normalizeSource(source, TierOwnerDecisions, SourceOwnerConstraint, InstructionOwnerConstraint, TrustAuthoritative, false))
	}

	organizationAgent, err := s.documents.Load(ctx, s.config.OrganizationAgentPath, int64(s.config.MaxSegmentBytes))
	if err != nil {
		return registry.Organization{}, nil, registry.Role{}, registry.Unit{}, CanonicalBundle{}, nil, nil, err
	}
	if err = ValidateAgent(organizationAgent); err != nil {
		return registry.Organization{}, nil, registry.Role{}, registry.Unit{}, CanonicalBundle{}, nil, nil, err
	}
	sources = append(sources, documentRecord(organizationAgent, TierOrganizationAgent, SourceOrganizationAgent, InstructionOrganizational, TrustAuthoritative, DataOrganizational))

	departmentPath := ""
	if role.DepartmentAgentPath != nil {
		departmentPath = *role.DepartmentAgentPath
	}
	if departmentPath == "" && unit.AgentPath != nil {
		departmentPath = *unit.AgentPath
	}
	if departmentPath == "" {
		return registry.Organization{}, nil, registry.Role{}, registry.Unit{}, CanonicalBundle{}, nil, nil, Reject(ReasonSourceNotFound, role.UnitID, "department AGENT path is missing from registry")
	}
	departmentAgent, err := s.documents.Load(ctx, departmentPath, int64(s.config.MaxSegmentBytes))
	if err != nil {
		return registry.Organization{}, nil, registry.Role{}, registry.Unit{}, CanonicalBundle{}, nil, nil, err
	}
	if err = ValidateAgent(departmentAgent); err != nil {
		return registry.Organization{}, nil, registry.Role{}, registry.Unit{}, CanonicalBundle{}, nil, nil, err
	}
	sources = append(sources, documentRecord(departmentAgent, TierDepartmentAgent, SourceDepartmentAgent, InstructionOrganizational, TrustAuthoritative, DataOrganizational))

	if role.ProfilePath == nil || strings.TrimSpace(*role.ProfilePath) == "" {
		return registry.Organization{}, nil, registry.Role{}, registry.Unit{}, CanonicalBundle{}, nil, nil, Reject(ReasonSourceNotFound, role.ID, "role profile path is missing")
	}
	profile, err := s.documents.Load(ctx, *role.ProfilePath, int64(s.config.MaxSegmentBytes))
	if err != nil {
		return registry.Organization{}, nil, registry.Role{}, registry.Unit{}, CanonicalBundle{}, nil, nil, err
	}
	memoryDomain := ""
	if role.MemoryDomain != nil {
		memoryDomain = *role.MemoryDomain
	}
	if err = ValidateProfile(profile, role.UnitID, role.RoleSlug, memoryDomain); err != nil {
		return registry.Organization{}, nil, registry.Role{}, registry.Unit{}, CanonicalBundle{}, nil, nil, err
	}
	sources = append(sources, documentRecord(profile, TierRoleProfile, SourceRoleProfile, InstructionRole, TrustAuthoritative, DataOrganizational))

	memories, err := s.memory.ListApproved(ctx, request)
	if err != nil && !errors.Is(err, ErrSourceProviderUnavailable) {
		return registry.Organization{}, nil, registry.Role{}, registry.Unit{}, CanonicalBundle{}, nil, nil, fmt.Errorf("resolve approved memory: %w", err)
	}
	for _, source := range memories {
		sources = append(sources, normalizeSource(source, TierApprovedMemory, SourceApprovedMemory, InstructionData, TrustUntrusted, false))
	}

	resolvedSkills, err := s.resolveSkills(ctx, request, role)
	if err != nil {
		return registry.Organization{}, nil, registry.Role{}, registry.Unit{}, CanonicalBundle{}, nil, nil, err
	}
	for _, skill := range resolvedSkills {
		documentValue, loadErr := s.documents.Load(ctx, skill.Path, int64(s.config.MaxSegmentBytes))
		if loadErr != nil {
			return registry.Organization{}, nil, registry.Role{}, registry.Unit{}, CanonicalBundle{}, nil, nil, loadErr
		}
		if _, loadErr = ValidateSkill(documentValue, skill); loadErr != nil {
			return registry.Organization{}, nil, registry.Role{}, registry.Unit{}, CanonicalBundle{}, nil, nil, loadErr
		}
		if skill.SourceHash != "" && skill.SourceHash != documentValue.Hash {
			return registry.Organization{}, nil, registry.Role{}, registry.Unit{}, CanonicalBundle{}, nil, nil, Reject(ReasonSkillSourceHashMismatch, skill.ID, "skill file hash differs from canonical provider")
		}
		record := documentRecord(documentValue, TierApprovedSkill, SourceApprovedSkill, InstructionProcedure, TrustApproved, DataOrganizational)
		record.Reference = skill.ID
		record.Version = skill.Version
		record.Relevance = skill.Relevance
		record.ProviderPriority = skill.ProviderPriority
		sources = append(sources, record)
	}

	project, err := s.projects.GetProjectContext(ctx, request)
	if err != nil {
		return registry.Organization{}, nil, registry.Role{}, registry.Unit{}, CanonicalBundle{}, nil, nil, fmt.Errorf("resolve project context: %w", err)
	}
	if project != nil {
		sources = append(sources, normalizeSource(*project, TierProject, SourceProjectContext, InstructionScoped, TrustScoped, false))
	}
	task, err := s.tasks.GetTaskContext(ctx, request)
	if err != nil {
		return registry.Organization{}, nil, registry.Role{}, registry.Unit{}, CanonicalBundle{}, nil, nil, fmt.Errorf("resolve task context: %w", err)
	}
	if task != nil {
		sources = append(sources, normalizeSource(*task, TierTask, SourceTaskContext, InstructionScoped, TrustScoped, false))
	}
	evidence, err := s.rag.ListApprovedEvidence(ctx, request)
	if err != nil && !errors.Is(err, ErrSourceProviderUnavailable) {
		return registry.Organization{}, nil, registry.Role{}, registry.Unit{}, CanonicalBundle{}, nil, nil, fmt.Errorf("resolve RAG evidence: %w", err)
	}
	for _, source := range evidence {
		sources = append(sources, normalizeSource(source, TierRAGEvidence, SourceRAGEvidence, InstructionData, TrustUntrusted, false))
	}
	return organization, revision, role, unit, bundle, sources, resolvedSkills, nil
}

func (s *contextService) resolveSkills(ctx context.Context, request BuildRequest, role registry.Role) ([]SkillRecord, error) {
	active, err := s.skills.ListActiveForRole(ctx, request.OrganizationID, role.ID)
	if err != nil && !errors.Is(err, ErrSourceProviderUnavailable) {
		return nil, fmt.Errorf("list active skills: %w", err)
	}
	byID := make(map[string]SkillRecord, len(active)+len(request.RequestedSkillIDs))
	for _, record := range active {
		if err = ValidateSkillLifecycle(record); err != nil {
			return nil, err
		}
		if record.RoleID != role.ID {
			return nil, Reject(ReasonSkillNotAssigned, record.ID, "provider returned skill for another role")
		}
		byID[record.ID] = record
	}
	for _, id := range request.RequestedSkillIDs {
		record, getErr := s.skills.GetActiveForRole(ctx, request.OrganizationID, role.ID, id)
		if getErr != nil {
			return nil, getErr
		}
		if getErr = ValidateSkillLifecycle(record); getErr != nil {
			return nil, getErr
		}
		byID[id] = record
	}
	if len(byID) > s.config.MaxSkills {
		return nil, Reject(ReasonContextBudgetExceeded, "max_skills", "active skill count exceeds configured maximum")
	}
	values := make([]SkillRecord, 0, len(byID))
	for _, record := range byID {
		values = append(values, record)
	}
	sort.Slice(values, func(i, j int) bool { return values[i].ID < values[j].ID })
	return values, nil
}

func (s *contextService) revalidateResolved(ctx context.Context, request BuildRequest, revisionID int64, bundle CanonicalBundle, role registry.Role, sources []SourceRecord, skills []SkillRecord) error {
	current, err := s.registry.GetCurrentRevision(ctx, request.OrganizationID)
	if err != nil {
		return fmt.Errorf("revalidate active revision: %w", err)
	}
	if current == nil || current.ID != revisionID {
		return Reject(ReasonRevisionMismatch, request.OrganizationID, "organization revision changed during context build")
	}
	currentRole, err := s.registry.GetRole(ctx, request.OrganizationID, role.ID)
	if err != nil {
		return fmt.Errorf("revalidate actor role: %w", err)
	}
	if currentRole.RetiredAt != nil || !currentRole.Enabled || currentRole.SourceRevision != role.SourceRevision {
		return Reject(ReasonRoleRetired, role.ID, "actor role changed during context build")
	}
	if err = s.canonical.Validate(ctx, bundle.PrecedenceHash, bundle.BundleHash); err != nil {
		return err
	}
	for _, source := range sources {
		switch source.Kind {
		case SourceOrganizationAgent, SourceDepartmentAgent, SourceRoleProfile:
			doc, loadErr := s.documents.Load(ctx, source.Reference, int64(s.config.MaxSegmentBytes))
			if loadErr != nil {
				return loadErr
			}
			if doc.Hash != source.ContentHash {
				return Reject(driftForKind(source.Kind), source.Reference, "document changed during context build")
			}
		case SourceOwnerConstraint:
			if err = s.owner.ValidateVersion(ctx, request.ActorRoleID, source); err != nil {
				return err
			}
		case SourceApprovedMemory:
			if err = s.memory.ValidateVersion(ctx, request.ActorRoleID, source); err != nil {
				return err
			}
		case SourceProjectContext:
			if err = s.projects.ValidateVersion(ctx, request.ActorRoleID, source); err != nil {
				return err
			}
		case SourceTaskContext:
			if err = s.tasks.ValidateVersion(ctx, request.ActorRoleID, source); err != nil {
				return err
			}
		case SourceRAGEvidence:
			if err = s.rag.ValidateVersion(ctx, request.ActorRoleID, source); err != nil {
				return err
			}
		}
	}
	for _, skill := range skills {
		if err = s.skills.ValidateVersion(ctx, skill); err != nil {
			return err
		}
	}
	return nil
}

func (s *contextService) Get(ctx context.Context, id int64, includeSegments bool) (Snapshot, error) {
	return s.store.Get(ctx, id, includeSegments)
}
func (s *contextService) List(ctx context.Context, filter ListFilter) ([]Snapshot, error) {
	return s.store.List(ctx, filter)
}

func (s *contextService) Render(ctx context.Context, id int64) ([]byte, error) {
	snapshot, err := s.store.Get(ctx, id, true)
	if err != nil {
		return nil, err
	}
	if snapshot.Status == SnapshotInvalidated {
		return nil, ErrSnapshotInvalidated
	}
	validation, err := s.Validate(ctx, id)
	if err != nil {
		return nil, err
	}
	if !validation.Valid {
		return nil, ErrSnapshotStale
	}
	return s.renderer.Render(ctx, snapshot)
}

func (s *contextService) Validate(ctx context.Context, id int64) (SnapshotValidation, error) {
	snapshot, err := s.store.Get(ctx, id, true)
	if err != nil {
		return SnapshotValidation{}, err
	}
	if snapshot.Status == SnapshotInvalidated {
		return SnapshotValidation{Valid: false, ReasonCode: string(ReasonSnapshotInvalidated)}, nil
	}
	drift := []DriftFinding{}
	organization, err := s.registry.GetOrganization(ctx, snapshot.OrganizationID)
	if err != nil {
		return SnapshotValidation{}, fmt.Errorf("validate organization: %w", err)
	}
	revision, err := s.registry.GetCurrentRevision(ctx, snapshot.OrganizationID)
	if err != nil {
		return SnapshotValidation{}, fmt.Errorf("validate revision: %w", err)
	}
	if revision == nil || revision.ID != snapshot.OrganizationRevisionID {
		drift = append(drift, DriftFinding{ReasonCode: string(ReasonRevisionMismatch), Expected: fmt.Sprint(snapshot.OrganizationRevisionID), Actual: revisionID(revision)})
	}
	role, err := s.registry.GetRole(ctx, snapshot.OrganizationID, snapshot.ActorRoleID)
	if err != nil {
		if !errors.Is(err, registry.ErrNotFound) {
			return SnapshotValidation{}, fmt.Errorf("validate actor role: %w", err)
		}
		drift = append(drift, DriftFinding{ReasonCode: string(ReasonRoleNotFound), Reference: snapshot.ActorRoleID})
	} else if role.RetiredAt != nil || !role.Enabled || (!role.Executable && role.ID != organization.OwnerRoleID) {
		drift = append(drift, DriftFinding{ReasonCode: string(ReasonRoleRetired), Reference: role.ID})
	}
	if err = s.canonical.Validate(ctx, snapshot.PrecedenceHash, snapshot.CanonicalBundleHash); err != nil {
		code := ReasonOf(err)
		if code == "" {
			return SnapshotValidation{}, err
		}
		drift = append(drift, DriftFinding{ReasonCode: string(code), Reference: "docs/canonical"})
	}
	for _, segment := range snapshot.Segments {
		switch segment.SourceKind {
		case SourceOrganizationAgent, SourceDepartmentAgent, SourceRoleProfile:
			doc, loadErr := s.documents.Load(ctx, segment.SourceReference, int64(s.config.MaxSegmentBytes))
			if loadErr != nil {
				if IsOperational(loadErr) {
					return SnapshotValidation{}, fmt.Errorf("validate source %s: %w", segment.SourceReference, loadErr)
				}
				drift = append(drift, DriftFinding{ReasonCode: string(driftForKind(segment.SourceKind)), SourceKind: segment.SourceKind, Reference: segment.SourceReference})
				continue
			}
			if doc.Hash != segment.ContentHash {
				drift = append(drift, DriftFinding{ReasonCode: string(driftForKind(segment.SourceKind)), SourceKind: segment.SourceKind, Reference: segment.SourceReference, Expected: segment.ContentHash, Actual: doc.Hash})
			}
		case SourceApprovedSkill:
			record, getErr := s.skills.GetActiveForRole(ctx, snapshot.OrganizationID, snapshot.ActorRoleID, segment.SourceReference)
			if getErr != nil {
				if IsOperational(getErr) {
					return SnapshotValidation{}, fmt.Errorf("validate skill state %s: %w", segment.SourceReference, getErr)
				}
				drift = append(drift, DriftFinding{ReasonCode: string(ReasonSkillStateDrift), SourceKind: segment.SourceKind, Reference: segment.SourceReference})
				continue
			}
			if getErr = s.skills.ValidateVersion(ctx, record); getErr != nil {
				if IsOperational(getErr) {
					return SnapshotValidation{}, fmt.Errorf("validate skill source %s: %w", segment.SourceReference, getErr)
				}
				drift = append(drift, DriftFinding{ReasonCode: string(ReasonSkillSourceDrift), SourceKind: segment.SourceKind, Reference: segment.SourceReference})
			} else if record.SourceHash != segment.ContentHash {
				drift = append(drift, DriftFinding{ReasonCode: string(ReasonSkillSourceDrift), SourceKind: segment.SourceKind, Reference: segment.SourceReference, Expected: segment.ContentHash, Actual: record.SourceHash})
			}
		case SourceApprovedMemory:
			if validateErr := s.memory.ValidateVersion(ctx, snapshot.ActorRoleID, segmentToRecord(segment)); validateErr != nil {
				if IsOperational(validateErr) {
					return SnapshotValidation{}, fmt.Errorf("validate memory %s: %w", segment.SourceReference, validateErr)
				}
				drift = append(drift, DriftFinding{ReasonCode: string(ReasonMemoryVersionDrift), SourceKind: segment.SourceKind, Reference: segment.SourceReference})
			}
		case SourceProjectContext:
			if validateErr := s.projects.ValidateVersion(ctx, snapshot.ActorRoleID, segmentToRecord(segment)); validateErr != nil {
				if IsOperational(validateErr) {
					return SnapshotValidation{}, fmt.Errorf("validate project %s: %w", segment.SourceReference, validateErr)
				}
				drift = append(drift, DriftFinding{ReasonCode: string(ReasonProjectVersionDrift), SourceKind: segment.SourceKind, Reference: segment.SourceReference})
			}
		case SourceTaskContext:
			if validateErr := s.tasks.ValidateVersion(ctx, snapshot.ActorRoleID, segmentToRecord(segment)); validateErr != nil {
				if IsOperational(validateErr) {
					return SnapshotValidation{}, fmt.Errorf("validate task %s: %w", segment.SourceReference, validateErr)
				}
				drift = append(drift, DriftFinding{ReasonCode: string(ReasonTaskVersionDrift), SourceKind: segment.SourceKind, Reference: segment.SourceReference})
			}
		case SourceRAGEvidence:
			if validateErr := s.rag.ValidateVersion(ctx, snapshot.ActorRoleID, segmentToRecord(segment)); validateErr != nil {
				if IsOperational(validateErr) {
					return SnapshotValidation{}, fmt.Errorf("validate RAG evidence %s: %w", segment.SourceReference, validateErr)
				}
				drift = append(drift, DriftFinding{ReasonCode: string(ReasonRAGVersionDrift), SourceKind: segment.SourceKind, Reference: segment.SourceReference})
			}
		}
	}
	if err = s.verifySnapshotIntegrity(ctx, snapshot); err != nil {
		drift = append(drift, DriftFinding{ReasonCode: string(ReasonRenderedHashMismatch), Expected: snapshot.RenderedHash})
	}
	if len(drift) > 0 {
		validation := SnapshotValidation{Valid: false, ReasonCode: string(ReasonSnapshotStale), Drift: drift}
		if recorder, ok := s.store.(ValidationEventRecorder); ok {
			if recordErr := recorder.RecordValidationFailure(ctx, snapshot, validation, s.clock.Now().UTC()); recordErr != nil {
				return SnapshotValidation{}, fmt.Errorf("record snapshot validation failure: %w", recordErr)
			}
		}
		return validation, nil
	}
	return SnapshotValidation{Valid: true}, nil
}

func (s *contextService) Invalidate(ctx context.Context, command InvalidateCommand) (Snapshot, error) {
	if command.SnapshotID <= 0 || !roleID.MatchString(command.ActorRoleID) || len(strings.TrimSpace(command.Reason)) < 1 || len(command.Reason) > 2000 {
		return Snapshot{}, Reject(ReasonInvalidRequest, "invalidate", "snapshot ID, actor role, and bounded reason are required")
	}
	snapshot, err := s.store.Get(ctx, command.SnapshotID, false)
	if err != nil {
		return Snapshot{}, err
	}
	organization, err := s.registry.GetOrganization(ctx, snapshot.OrganizationID)
	if err != nil {
		return Snapshot{}, fmt.Errorf("load organization for invalidation: %w", err)
	}
	if command.ActorRoleID != snapshot.ActorRoleID && command.ActorRoleID != organization.OwnerRoleID {
		return Snapshot{}, Reject(ReasonUnsafeInstructionSource, command.ActorRoleID, "only snapshot actor or owner can invalidate")
	}
	value, _, err := s.store.Invalidate(ctx, command, s.clock.Now().UTC())
	return value, err
}

func (s *contextService) verifySnapshotIntegrity(ctx context.Context, snapshot Snapshot) error {
	rendered, err := s.renderer.Render(ctx, snapshot)
	if err != nil {
		return err
	}
	if DigestCanonicalBytes(rendered) != snapshot.RenderedHash {
		return Reject(ReasonRenderedHashMismatch, fmt.Sprint(snapshot.ID), "rendered snapshot hash mismatch")
	}
	return nil
}

func canonicalRecords(bundle CanonicalBundle) []SourceRecord {
	values := make([]SourceRecord, 0, len(bundle.Sources))
	for _, source := range bundle.Sources {
		priority, _ := AuthorityPriority(source.Tier)
		values = append(values, SourceRecord{Kind: SourceCanonicalDocument, Reference: source.LogicalName, Version: source.Version, AuthorityTier: source.Tier, AuthorityPriority: priority, InstructionClass: source.InstructionClass, TrustClass: source.TrustClass, DataClass: source.DataClass, MayGrantCapabilities: source.MayGrant, Content: append([]byte(nil), source.Content...), ContentHash: source.ContentHash, Included: true})
	}
	return values
}

func documentRecord(document LoadedDocument, tier AuthorityTier, kind SourceKind, class InstructionClass, trust TrustClass, data DataClass) SourceRecord {
	priority, _ := AuthorityPriority(tier)
	return SourceRecord{Kind: kind, Reference: document.Path, Version: "sha256:" + document.Hash, AuthorityTier: tier, AuthorityPriority: priority, InstructionClass: class, TrustClass: trust, DataClass: data, Content: append([]byte(nil), document.Normalized...), ContentHash: document.Hash, Included: true}
}

func normalizeSource(source SourceRecord, tier AuthorityTier, kind SourceKind, class InstructionClass, trust TrustClass, mayGrant bool) SourceRecord {
	source.Kind = kind
	source.AuthorityTier = tier
	source.AuthorityPriority, _ = AuthorityPriority(tier)
	source.InstructionClass = class
	source.TrustClass = trust
	source.MayGrantCapabilities = mayGrant
	source.Included = true
	source.OmissionReason = ""
	if source.ContentHash == "" {
		source.ContentHash = DigestMarkdown(source.Content)
	}
	return source
}

func segmentToRecord(segment Segment) SourceRecord {
	return SourceRecord{Kind: segment.SourceKind, Reference: segment.SourceReference, Version: segment.SourceVersion, AuthorityTier: segment.AuthorityTier, AuthorityPriority: segment.AuthorityPriority, InstructionClass: segment.InstructionClass, TrustClass: segment.TrustClass, DataClass: segment.DataClass, MayGrantCapabilities: segment.MayGrantCapabilities, ContentHash: segment.ContentHash, Included: segment.Included}
}

func driftForKind(kind SourceKind) ReasonCode {
	if kind == SourceRoleProfile {
		return ReasonProfileDrift
	}
	return ReasonAgentDrift
}

func revisionID(revision *registry.Revision) string {
	if revision == nil {
		return "none"
	}
	return fmt.Sprint(revision.ID)
}
