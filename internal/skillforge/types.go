package skillforge

import (
	"errors"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/skillforge/source"
	"github.com/Mireuz13/explorarte-organization/internal/skillregistry"
)

var (
	ErrNeedNotAccepted     = errors.New("procedure need must be in accepted status to start forge run")
	ErrHumanApprovalNeeded = errors.New("skill version is waiting for external owner approval")
	ErrValidationFailed    = errors.New("skill static validation failed")
	ErrAdversarialFailed   = errors.New("adversarial review failed")
	ErrCanaryFailed        = errors.New("canary evaluation failed")
	ErrContractViolation   = errors.New("SKILL.md contract violated")
)

type RunStatus string

const (
	StatusCreated              RunStatus = "created"
	StatusSearching            RunStatus = "searching"
	StatusAuthoring            RunStatus = "authoring"
	StatusPublishing           RunStatus = "publishing"
	StatusMaterializing        RunStatus = "materializing"
	StatusDraftRegistered      RunStatus = "draft_registered"
	StatusWaitingHumanApproval RunStatus = "waiting_human_approval"
	StatusValidating           RunStatus = "validating"
	StatusEvaluating           RunStatus = "evaluating"
	StatusAdversarialReview    RunStatus = "adversarial_review"
	StatusCanary               RunStatus = "canary"
	StatusCandidateReady       RunStatus = "candidate_ready"
	StatusRejected             RunStatus = "rejected"
	StatusFailed               RunStatus = "failed"
)

type Step string

const (
	StepSearch       Step = "search"
	StepAuthor       Step = "author"
	StepPublish      Step = "publish"
	StepMaterialize  Step = "materialize"
	StepDraft        Step = "draft"
	StepWaitApproval Step = "wait_approval"
	StepValidate     Step = "validate"
	StepEvaluate     Step = "evaluate"
	StepAdversarial  Step = "adversarial"
	StepCanary       Step = "canary"
	StepComplete     Step = "complete"
)

type SearchDecision string

const (
	DecisionReuse  SearchDecision = "reuse"
	DecisionAdapt  SearchDecision = "adapt"
	DecisionAuthor SearchDecision = "author"
)

type SearchRecord struct {
	SearchedSources  []string       `json:"searched_sources"`
	CandidateMatches []string       `json:"candidate_matches"`
	Decision         SearchDecision `json:"decision"`
	DecisionReason   string         `json:"decision_reason"`
}

type AuthorRecord struct {
	SkillID          string                 `json:"skill_id"`
	CandidateBytes   []byte                 `json:"candidate_bytes"`
	Manifest         skillregistry.Manifest `json:"manifest"`
	ContentDigest    string                 `json:"content_digest"`
	AuthorProfileRef string                 `json:"author_profile_ref"`
}

type ValidationResult struct {
	Passed               bool     `json:"passed"`
	SchemaValidationRef  string   `json:"schema_validation_ref"`
	CapabilityReviewRef  string   `json:"capability_review_ref"`
	InstructionSafetyRef string   `json:"instruction_safety_ref"`
	Errors               []string `json:"errors,omitempty"`
}

type EvaluationResult struct {
	Passed                 bool               `json:"passed"`
	CandidateVersionID     string             `json:"candidate_version_id"`
	CandidateCanonicalHash string             `json:"candidate_canonical_hash"`
	CandidateSourceHash    string             `json:"candidate_source_hash"`
	BaselineVersionID      string             `json:"baseline_version_id,omitempty"`
	SuiteRef               string             `json:"suite_ref"`
	Metrics                map[string]float64 `json:"metrics,omitempty"`
	AdversarialVerdict     string             `json:"adversarial_verdict"` // pass / fail
	CanaryVerdict          string             `json:"canary_verdict"`      // pass / fail
}

type ForgeRun struct {
	ID                 string                      `json:"id"`
	OrganizationID     string                      `json:"organization_id"`
	NeedID             string                      `json:"need_id"`
	Status             RunStatus                   `json:"status"`
	CurrentStep        Step                        `json:"current_step"`
	InputDigest        string                      `json:"input_digest"`
	Search             *SearchRecord               `json:"search,omitempty"`
	Author             *AuthorRecord               `json:"author,omitempty"`
	PublishedSource    *source.PublishedSource     `json:"published_source,omitempty"`
	MaterializedSource *skillregistry.SourceRecord `json:"materialized_source,omitempty"`
	SkillVersionID     string                      `json:"skill_version_id,omitempty"`
	Validation         *ValidationResult           `json:"validation,omitempty"`
	Evaluation         *EvaluationResult           `json:"evaluation,omitempty"`
	Revision           int64                       `json:"revision"`
	StartedAt          time.Time                   `json:"started_at"`
	FinishedAt         *time.Time                  `json:"finished_at,omitempty"`
}

type Event struct {
	RunID      string            `json:"run_id"`
	Sequence   int64             `json:"sequence"`
	EventType  string            `json:"event_type"`
	Refs       map[string]string `json:"refs,omitempty"`
	Digest     string            `json:"digest"`
	RecordedAt time.Time         `json:"recorded_at"`
}
