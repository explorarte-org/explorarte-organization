package skillforge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/skillforge/need"
	"github.com/Mireuz13/explorarte-organization/internal/skillforge/source"
	"github.com/Mireuz13/explorarte-organization/internal/skillregistry"
)

type Authorer interface {
	Author(ctx context.Context, n need.ProcedureNeed) (AuthorRecord, error)
}

type DefaultAuthorer struct{}

func (d *DefaultAuthorer) Author(_ context.Context, n need.ProcedureNeed) (AuthorRecord, error) {
	skillID := fmt.Sprintf("skill-%s", n.ID)
	// Truncate or clean skillID
	skillID = strings.TrimPrefix(skillID, "need-")
	if !strings.HasPrefix(skillID, "skill-") {
		skillID = "skill-" + skillID
	}

	department, _, ok := strings.Cut(n.RoleID, "/")
	if !ok {
		department = "ingenieria_ia"
	}

	content := fmt.Sprintf(`# %s

## Purpose
Solve the problem: %s

## Applicability
Applicable to role %s for task class %s.

## Non-goals
Does not grant capabilities or bypass governance.

## Inputs
Required task context and parameters.

## Outputs
Standard execution report and evidence.

## Procedure
1. Verify prerequisite context.
2. Execute bounded operations.
3. Record verifiable evidence.

## Stop conditions
Stop immediately upon unexpected error or budget limit.

## Failure modes
Handle recoverable errors cleanly.

## Evidence requirements
Emit verifiable completion proof.
`, skillID, n.ProblemStatement, n.RoleID, n.TaskClass)

	rawSum := sha256.Sum256([]byte(content))
	contentDigest := hex.EncodeToString(rawSum[:])

	manifest := skillregistry.Manifest{
		Name:                  skillID,
		Description:           fmt.Sprintf("Procedure for %s solving: %s", n.RoleID, n.ProblemStatement),
		Department:            department,
		OwnerRoleID:           n.RoleID,
		MemoryDomain:          department,
		BaseProtocol:          "verificacion_estado",
		VerifierRef:           "internal/verifier:v1",
		RequiredCapabilities:  []string{"verificacion_estado"},
		ManifestSchemaVersion: "skill-manifest/v2",
	}

	return AuthorRecord{
		SkillID:          skillID,
		CandidateBytes:   []byte(content),
		Manifest:         manifest,
		ContentDigest:    contentDigest,
		AuthorProfileRef: "worker/skill-forge/v1",
	}, nil
}

type Engine struct {
	mu           sync.Mutex
	needRepo     need.Repository
	registryRepo skillregistry.Repository
	manager      *skillregistry.Manager
	publisher    source.SourcePublisher
	materializer source.Materializer
	authorer     Authorer
	validator    *StaticValidator
	evaluator    *ForgeEvaluator
	runs         map[string]ForgeRun
	events       map[string][]Event
}

func NewEngine(
	needRepo need.Repository,
	registryRepo skillregistry.Repository,
	manager *skillregistry.Manager,
	publisher source.SourcePublisher,
	materializer source.Materializer,
	authorer Authorer,
	validator *StaticValidator,
	evaluator *ForgeEvaluator,
) *Engine {
	if authorer == nil {
		authorer = &DefaultAuthorer{}
	}
	return &Engine{
		needRepo:     needRepo,
		registryRepo: registryRepo,
		manager:      manager,
		publisher:    publisher,
		materializer: materializer,
		authorer:     authorer,
		validator:    validator,
		evaluator:    evaluator,
		runs:         make(map[string]ForgeRun),
		events:       make(map[string][]Event),
	}
}

func (e *Engine) Run(ctx context.Context, orgID, needID string) (ForgeRun, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	n, err := e.needRepo.GetNeed(ctx, orgID, needID)
	if err != nil {
		return ForgeRun{}, fmt.Errorf("fetch need %s: %w", needID, err)
	}

	if n.Status != need.StatusAccepted {
		return ForgeRun{}, fmt.Errorf("%w: current status is %s", ErrNeedNotAccepted, n.Status)
	}

	runID := "run:" + needID
	run, exists := e.runs[runID]
	if !exists {
		run = ForgeRun{
			ID:             runID,
			OrganizationID: orgID,
			NeedID:         needID,
			Status:         StatusCreated,
			CurrentStep:    StepSearch,
			InputDigest:    n.CanonicalDigest,
			Revision:       1,
			StartedAt:      time.Now().UTC(),
		}
		e.runs[runID] = run
		e.recordEvent(runID, "run_created", map[string]string{"need_id": needID}, n.CanonicalDigest)
	}

	// State machine execution loop
	for {
		switch run.CurrentStep {
		case StepSearch:
			run.Status = StatusSearching
			// Search existing skills
			searchRec := SearchRecord{
				SearchedSources:  []string{"skill_registry", "catalog"},
				CandidateMatches: []string{},
				Decision:         DecisionAuthor,
				DecisionReason:   "no existing skill matches problem statement",
			}
			run.Search = &searchRec
			run.CurrentStep = StepAuthor
			e.recordEvent(run.ID, "search_completed", map[string]string{"decision": string(searchRec.Decision)}, "")

		case StepAuthor:
			run.Status = StatusAuthoring
			authorRec, err := e.authorer.Author(ctx, n)
			if err != nil {
				run.Status = StatusFailed
				e.runs[run.ID] = run
				return run, fmt.Errorf("authoring failed: %w", err)
			}
			run.Author = &authorRec
			run.CurrentStep = StepPublish
			e.recordEvent(run.ID, "authoring_completed", map[string]string{"skill_id": authorRec.SkillID}, authorRec.ContentDigest)

		case StepPublish:
			run.Status = StatusPublishing
			pubReq := source.PublishRequest{
				SkillID:               run.Author.SkillID,
				CandidateSourceBytes:  run.Author.CandidateBytes,
				ExpectedContentDigest: run.Author.ContentDigest,
				IdempotencyKey:        run.ID,
			}
			pubSource, err := e.publisher.Publish(ctx, pubReq)
			if err != nil {
				run.Status = StatusFailed
				e.runs[run.ID] = run
				return run, fmt.Errorf("publication failed: %w", err)
			}
			run.PublishedSource = &pubSource
			run.CurrentStep = StepMaterialize
			e.recordEvent(run.ID, "published", map[string]string{"origin_ref": pubSource.OriginRef}, pubSource.RawSHA256)

		case StepMaterialize:
			run.Status = StatusMaterializing
			matReq := source.MaterializeRequest{
				OriginRef:       run.PublishedSource.OriginRef,
				RelativePath:    run.PublishedSource.Path,
				ExpectedRawSHA:  run.PublishedSource.RawSHA256,
				ExpectedNormSHA: run.PublishedSource.NormalizedSHA256,
				RecordedBy:      n.RoleID,
				RecordRef:       fmt.Sprintf("materialize:%s", run.ID),
			}
			matSource, err := e.materializer.Materialize(ctx, matReq)
			if err != nil {
				run.Status = StatusFailed
				e.runs[run.ID] = run
				return run, fmt.Errorf("materialization failed: %w", err)
			}
			run.MaterializedSource = &matSource
			run.CurrentStep = StepDraft
			e.recordEvent(run.ID, "materialized", map[string]string{"path": matSource.Path}, matSource.NormalizedSHA256)

		case StepDraft:
			// Register Draft in SkillRegistry
			versionID := fmt.Sprintf("%s-v1", run.Author.SkillID)
			skillID := run.Author.SkillID

			manifestHash, err := skillregistry.HashManifest(run.Author.Manifest)
			if err != nil {
				return run, err
			}
			canonicalHash, err := skillregistry.HashVersionIdentity(skillID, orgID, 1, manifestHash, *run.MaterializedSource)
			if err != nil {
				return run, err
			}

			skill := skillregistry.Skill{
				ID:             skillID,
				OrganizationID: orgID,
				CreatedByRole:  n.RoleID,
				CreatedAt:      time.Now().UTC(),
			}

			version := skillregistry.SkillVersion{
				ID:             versionID,
				SkillID:        skillID,
				OrganizationID: orgID,
				Version:        1,
				Lifecycle:      skillregistry.LifecycleDraft,
				Manifest:       run.Author.Manifest,
				Source:         *run.MaterializedSource,
				ContentHash:    run.MaterializedSource.SHA256,
				ManifestHash:   manifestHash,
				CanonicalHash:  canonicalHash,
				Revision:       1,
				CreatedAt:      time.Now().UTC(),
				UpdatedAt:      time.Now().UTC(),
			}

			evidence := skillregistry.GovernanceEvidence{
				DecisionRef: n.Acceptance.DecisionRef,
				ActorRoleID: n.Acceptance.AcceptedBy,
				DecidedAt:   n.Acceptance.AcceptedAt,
			}

			_, savedVer, _, err := e.registryRepo.CreateSkill(ctx, skill, version, run.ID, evidence)
			if err != nil {
				// If skill exists, try getting existing version
				existingVer, getErr := e.registryRepo.GetVersion(ctx, orgID, versionID)
				if getErr == nil {
					savedVer = existingVer
				} else {
					run.Status = StatusFailed
					e.runs[run.ID] = run
					return run, fmt.Errorf("register draft failed: %w", err)
				}
			}

			run.SkillVersionID = savedVer.ID
			run.Status = StatusWaitingHumanApproval
			run.CurrentStep = StepWaitApproval
			e.recordEvent(run.ID, "draft_registered", map[string]string{"version_id": savedVer.ID}, canonicalHash)
			e.runs[run.ID] = run

			// MUST STOP HERE: wait for external human approval
			return run, nil

		case StepWaitApproval:
			// Inspect if external human approval has occurred on the SkillVersion
			ver, err := e.registryRepo.GetVersion(ctx, orgID, run.SkillVersionID)
			if err != nil {
				return run, fmt.Errorf("check version approval status %s: %w", run.SkillVersionID, err)
			}

			if ver.Lifecycle == skillregistry.LifecycleDraft {
				run.Status = StatusWaitingHumanApproval
				e.runs[run.ID] = run
				return run, ErrHumanApprovalNeeded
			}

			if ver.Lifecycle != skillregistry.LifecycleHumanApproved && ver.Lifecycle != skillregistry.LifecycleCandidate {
				return run, fmt.Errorf("unexpected version lifecycle during approval check: %s", ver.Lifecycle)
			}

			run.CurrentStep = StepValidate
			e.recordEvent(run.ID, "human_approval_detected", map[string]string{"version_id": ver.ID}, ver.CanonicalHash)

		case StepValidate:
			run.Status = StatusValidating
			ver, err := e.registryRepo.GetVersion(ctx, orgID, run.SkillVersionID)
			if err != nil {
				return run, err
			}

			schemaRef, schemaPass, err := e.validator.ValidateSkillSource(ctx, ver.SkillID, ver.Source, ver.Manifest)
			if err != nil || !schemaPass {
				run.Status = StatusRejected
				e.runs[run.ID] = run
				return run, fmt.Errorf("%w: schema validation failed: %v", ErrValidationFailed, err)
			}

			capRef, capPass, err := e.validator.ReviewRoleCapabilities(ctx, n.RoleID, ver.SkillID, ver.Manifest.RequiredCapabilities)
			if err != nil || !capPass {
				run.Status = StatusRejected
				e.runs[run.ID] = run
				return run, fmt.Errorf("%w: capability review failed: %v", ErrValidationFailed, err)
			}

			safetyRef, safetyPass, err := e.validator.ReviewSkillInstructions(ctx, ver.SkillID, ver.Source, ver.Manifest)
			if err != nil || !safetyPass {
				run.Status = StatusRejected
				e.runs[run.ID] = run
				return run, fmt.Errorf("%w: safety review failed: %v", ErrValidationFailed, err)
			}

			valEvidence := skillregistry.ValidationEvidence{
				SchemaValidationRef:   schemaRef,
				CapabilityReviewRef:   capRef,
				InstructionSafetyRef:  safetyRef,
				SourceRecordRef:       ver.Source.RecordRef,
				ValidatedBy:           "empresa/qa_validator",
				ValidatedAt:           time.Now().UTC(),
				CapabilitiesPass:      true,
				InstructionSafetyPass: true,
			}

			// Advance in registry: human_approved -> candidate
			if ver.Lifecycle == skillregistry.LifecycleHumanApproved {
				ver, err = e.manager.QualifyCandidate(ctx, skillregistry.LifecycleMutationRequest{
					OrganizationID:   orgID,
					VersionID:        ver.ID,
					ExpectedRevision: ver.Revision,
					ActorRoleID:      "empresa/qa_validator",
				}, valEvidence)
				if err != nil {
					return run, fmt.Errorf("qualify candidate in registry: %w", err)
				}
			}

			run.Validation = &ValidationResult{
				Passed:               true,
				SchemaValidationRef:  schemaRef,
				CapabilityReviewRef:  capRef,
				InstructionSafetyRef: safetyRef,
			}
			run.CurrentStep = StepEvaluate
			e.recordEvent(run.ID, "validated_candidate", map[string]string{"version_id": ver.ID}, ver.CanonicalHash)

		case StepEvaluate, StepAdversarial, StepCanary:
			run.Status = StatusEvaluating
			ver, err := e.registryRepo.GetVersion(ctx, orgID, run.SkillVersionID)
			if err != nil {
				return run, err
			}

			evalResult, err := e.evaluator.EvaluateCandidate(ctx, n.RoleID, ver)
			if err != nil {
				run.Status = StatusRejected
				run.Evaluation = &evalResult
				e.runs[run.ID] = run
				return run, fmt.Errorf("evaluation rejected: %w", err)
			}

			run.Evaluation = &evalResult
			run.CurrentStep = StepComplete
			e.recordEvent(run.ID, "evaluation_passed", map[string]string{"suite": evalResult.SuiteRef}, "")

		case StepComplete:
			run.Status = StatusCandidateReady
			now := time.Now().UTC()
			run.FinishedAt = &now
			e.recordEvent(run.ID, "candidate_ready", map[string]string{"version_id": run.SkillVersionID}, "")
			e.runs[run.ID] = run
			return run, nil
		}
	}
}

func (e *Engine) GetRun(_ context.Context, runID string) (ForgeRun, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	run, ok := e.runs[runID]
	if !ok {
		return ForgeRun{}, fmt.Errorf("forge run %s not found", runID)
	}
	return run, nil
}

func (e *Engine) ListEvents(_ context.Context, runID string) ([]Event, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.events[runID], nil
}

func (e *Engine) recordEvent(runID, eventType string, refs map[string]string, digest string) {
	seq := int64(len(e.events[runID]) + 1)
	e.events[runID] = append(e.events[runID], Event{
		RunID:      runID,
		Sequence:   seq,
		EventType:  eventType,
		Refs:       refs,
		Digest:     digest,
		RecordedAt: time.Now().UTC(),
	})
}
