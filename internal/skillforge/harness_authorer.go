package skillforge

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Mireuz13/explorarte-organization/internal/executionharness"
	"github.com/Mireuz13/explorarte-organization/internal/skillforge/need"
	"github.com/Mireuz13/explorarte-organization/internal/skillregistry"
)

var (
	ErrProductiveProfileBlocked = errors.New("productive authoring profile activation is blocked by owner decision D-006")
	ErrAuthorOutputSchema       = errors.New("author output schema invalid")
	ErrAuthorOutputLimit        = errors.New("author output size limit exceeded")
	ErrAuthorOutputEncoding     = errors.New("author output contains invalid UTF-8")
	ErrAuthorHarnessFailed      = errors.New("authoring harness run failed")
	ErrAuthorAuthorityEscape    = errors.New("author output attempted authority escalation")
)

const (
	DefaultAuthoringProfileID = "worker/skill-forge/v1"
	MaxAuthorMarkdownBytes    = 65536 // 64KB bounded limit
)

// SkillAuthoringOutput is the strict structured contract that the model invocation must produce.
type SkillAuthoringOutput struct {
	SkillID        string `json:"skill_id"`
	Decision       string `json:"decision"` // reuse | adapt | author
	DecisionReason string `json:"decision_reason"`
	SkillMarkdown  string `json:"skill_markdown"`
}

// AuthoringProfileGate defines the authority boundary for authoring execution profiles.
type AuthoringProfileGate interface {
	AuthorizeProfile(ctx context.Context, organizationID string, profileID string) error
}

// FailClosedProfileGate is the default fail-closed gate that denies unapproved profile usage.
type FailClosedProfileGate struct{}

func (FailClosedProfileGate) AuthorizeProfile(_ context.Context, _ string, _ string) error {
	return ErrProductiveProfileBlocked
}

// CanonicalAuthoringProfileGate authorizes profiles that have been approved by canonical governance.
type CanonicalAuthoringProfileGate struct {
	ApprovedProfiles map[string]bool
	CanonicalDir     string
}

func (c CanonicalAuthoringProfileGate) AuthorizeProfile(_ context.Context, _ string, profileID string) error {
	if c.ApprovedProfiles != nil {
		if c.ApprovedProfiles[profileID] {
			return nil
		}
		return ErrProductiveProfileBlocked
	}

	// Canonical governance verification: check if D-006 is resolved
	canonicalDir := c.CanonicalDir
	if canonicalDir == "" {
		canonicalDir = filepath.Join("docs", "canonical")
		if _, err := os.Stat(canonicalDir); os.IsNotExist(err) {
			canonicalDir = filepath.Join("..", "..", "docs", "canonical")
		}
	}
	decisionsPath := filepath.Join(canonicalDir, "decisions-required.yaml")
	data, err := os.ReadFile(decisionsPath)
	if err != nil {
		return ErrProductiveProfileBlocked
	}

	content := string(data)
	resolvedIdx := strings.Index(content, "resolved:")
	if resolvedIdx == -1 {
		return ErrProductiveProfileBlocked
	}
	resolvedSection := content[resolvedIdx:]
	if strings.Contains(resolvedSection, "- id: D-006") || strings.Contains(resolvedSection, "id: D-006") {
		if profileID == "worker/skill-forge/v1" {
			return nil
		}
	}

	return ErrProductiveProfileBlocked
}

// FakeAuthoringProfileGate is a test-only fixture authority.
type FakeAuthoringProfileGate struct {
	Allowed bool
}

func (f FakeAuthoringProfileGate) AuthorizeProfile(_ context.Context, _ string, _ string) error {
	if f.Allowed {
		return nil
	}
	return ErrProductiveProfileBlocked
}

type HarnessAuthorerConfig struct {
	ExecutionProfileID string               // logical profile, e.g. "worker/skill-forge/v1"
	ModelPolicyRef     string               // model policy reference
	BuildRef           string               // harness build reference
	ProfileGate        AuthoringProfileGate // injected authority gate for profile validation
	MaxTurns           int                  // bounded turns (e.g. 1 or 2)
}

// HarnessAuthorer connects the ProcedureNeed domain to the provider-independent ExecutionHarness.
// It never invokes modelruntime directly and never parses Chain-of-Thought.
type HarnessAuthorer struct {
	harness   *executionharness.Runtime
	validator *ContractValidator
	cfg       HarnessAuthorerConfig
}

func NewHarnessAuthorer(harness *executionharness.Runtime, cfg HarnessAuthorerConfig) (*HarnessAuthorer, error) {
	if harness == nil {
		return nil, fmt.Errorf("execution harness is required")
	}
	if strings.TrimSpace(cfg.ExecutionProfileID) == "" {
		cfg.ExecutionProfileID = DefaultAuthoringProfileID
	}
	if strings.TrimSpace(cfg.ModelPolicyRef) == "" {
		cfg.ModelPolicyRef = "worker.skill_forge"
	}
	if strings.TrimSpace(cfg.BuildRef) == "" {
		cfg.BuildRef = "skillforge/authorer:v1"
	}
	if cfg.MaxTurns <= 0 {
		cfg.MaxTurns = 1
	}
	if cfg.ProfileGate == nil {
		cfg.ProfileGate = FailClosedProfileGate{}
	}

	// Section 1 Invariant: D-006 is an activation gate verified by the AuthoringProfileGate.
	// Production bootstrap refuses enablement if the gate denies the execution profile.
	if err := cfg.ProfileGate.AuthorizeProfile(context.Background(), "explorarte", cfg.ExecutionProfileID); err != nil {
		return nil, ErrProductiveProfileBlocked
	}

	return &HarnessAuthorer{
		harness:   harness,
		validator: NewContractValidator(),
		cfg:       cfg,
	}, nil
}

func (a *HarnessAuthorer) Author(ctx context.Context, n need.ProcedureNeed) (AuthorRecord, error) {
	if n.Status != need.StatusAccepted {
		return AuthorRecord{}, ErrNeedNotAccepted
	}
	if err := a.cfg.ProfileGate.AuthorizeProfile(ctx, n.OrganizationID, a.cfg.ExecutionProfileID); err != nil {
		return AuthorRecord{}, fmt.Errorf("%w: %v", ErrProductiveProfileBlocked, err)
	}

	// Bounded, deterministic prompt for structured authoring
	promptContent := fmt.Sprintf(`You are an authorized skill authorer.
Generate a structured JSON response matching the SkillAuthoringOutput schema.
Organization: %s
Need ID: %s
Role ID: %s
Task Class: %s
Description: %s
Acceptance Decision: %s

Requirements for skill_markdown:
- Must follow SKILL.md format.
- Must include sections: Purpose, Applicability, Non-goals, Inputs, Outputs, Procedure, Stop conditions, Failure modes, Evidence requirements.
- Must NOT attempt to grant capabilities, modify routing, bypass governance, or access secrets.
- The "decision" field MUST be "author".
- The "skill_id" MUST use lowercase alphanumeric and hyphens only (e.g. "skill-smoke-verification").
Respond ONLY with a valid JSON object with keys: "skill_id", "decision", "decision_reason", "skill_markdown".`,
		n.OrganizationID, n.ID, n.RoleID, n.TaskClass, n.ProblemStatement, n.Acceptance.DecisionRef,
	)

	promptDigestBytes := sha256.Sum256([]byte(promptContent))
	promptDigest := hex.EncodeToString(promptDigestBytes[:])

	runID := fmt.Sprintf("harness-author-%s-%d", n.ID, time.Now().UnixNano())

	spec := executionharness.RunSpec{
		Identity: executionharness.RunIdentity{
			RunID:                runID,
			OrganizationID:       n.OrganizationID,
			TaskID:               1,
			AttemptID:            1,
			RoleID:               n.RoleID,
			ExecutionPrincipalID: "skillforge-harness-authorer",
			CorrelationID:        n.ID,
			CausationID:          n.Acceptance.DecisionRef,
		},
		LeaseToken: fmt.Sprintf("lease-%s", runID),
		Context: executionharness.InitialContext{
			ID:      fmt.Sprintf("ctx-author-%s", n.ID),
			Version: "v1",
			Digest:  promptDigest,
			Content: promptContent,
		},
		Tools: nil, // MaxToolCalls = 0: no tools visible to authorer
		Policy: executionharness.RunPolicy{
			MaxTurns:           a.cfg.MaxTurns,
			MaxToolCalls:       0,
			ExecutionProfileID: a.cfg.ExecutionProfileID,
			ModelPolicyRef:     a.cfg.ModelPolicyRef,
			BuildRef:           a.cfg.BuildRef,
		},
	}

	// Execute through real ExecutionHarness
	// This ensures RunDescriptor is frozen & persisted, context snapshot recorded, and events emitted.
	runResult := a.harness.Execute(ctx, spec)

	if runResult.Status != executionharness.StatusCompleted {
		return AuthorRecord{}, fmt.Errorf("%w: status %s, reason: %s", ErrAuthorHarnessFailed, runResult.Status, runResult.TerminationReason)
	}

	// Never parse Chain-of-Thought. Extract only structured output from FinalOutput or LastModelOutput
	rawOutput := strings.TrimSpace(runResult.FinalOutput)
	if rawOutput == "" {
		rawOutput = strings.TrimSpace(runResult.LastModelOutput)
	}
	if rawOutput == "" {
		return AuthorRecord{}, fmt.Errorf("%w: model returned empty output", ErrAuthorOutputSchema)
	}

	// Clean code fence formatting if present
	if strings.HasPrefix(rawOutput, "```json") {
		rawOutput = strings.TrimPrefix(rawOutput, "```json")
		rawOutput = strings.TrimSuffix(rawOutput, "```")
		rawOutput = strings.TrimSpace(rawOutput)
	} else if strings.HasPrefix(rawOutput, "```") {
		rawOutput = strings.TrimPrefix(rawOutput, "```")
		rawOutput = strings.TrimSuffix(rawOutput, "```")
		rawOutput = strings.TrimSpace(rawOutput)
	}

	// Strict JSON schema validation
	decoder := json.NewDecoder(bytes.NewReader([]byte(rawOutput)))
	decoder.DisallowUnknownFields()

	var output SkillAuthoringOutput
	if err := decoder.Decode(&output); err != nil {
		return AuthorRecord{}, fmt.Errorf("%w: decode output: %v", ErrAuthorOutputSchema, err)
	}
	if decoder.More() {
		return AuthorRecord{}, fmt.Errorf("%w: trailing content in model output", ErrAuthorOutputSchema)
	}

	// Validate fields
	if strings.TrimSpace(output.SkillID) == "" {
		return AuthorRecord{}, fmt.Errorf("%w: skill_id is required", ErrAuthorOutputSchema)
	}
	dec := strings.ToLower(strings.TrimSpace(output.Decision))
	if dec == "approved" {
		dec = "author"
	}
	if dec != "reuse" && dec != "adapt" && dec != "author" {
		return AuthorRecord{}, fmt.Errorf("%w: invalid decision %q", ErrAuthorOutputSchema, output.Decision)
	}
	if strings.TrimSpace(output.DecisionReason) == "" {
		return AuthorRecord{}, fmt.Errorf("%w: decision_reason is required", ErrAuthorOutputSchema)
	}
	if strings.TrimSpace(output.SkillMarkdown) == "" {
		return AuthorRecord{}, fmt.Errorf("%w: skill_markdown is empty", ErrAuthorOutputSchema)
	}

	// Size limit
	if len(output.SkillMarkdown) > MaxAuthorMarkdownBytes {
		return AuthorRecord{}, fmt.Errorf("%w: markdown size %d exceeds limit %d", ErrAuthorOutputLimit, len(output.SkillMarkdown), MaxAuthorMarkdownBytes)
	}

	// UTF-8 validation
	if !utf8.ValidString(output.SkillMarkdown) {
		return AuthorRecord{}, ErrAuthorOutputEncoding
	}

	candidateBytes := []byte(output.SkillMarkdown)

	// Static validation of markdown contract: sections and forbidden statements
	if err := a.validator.ValidateContract(candidateBytes); err != nil {
		return AuthorRecord{}, fmt.Errorf("%w: %v", ErrAuthorAuthorityEscape, err)
	}

	// Host chooses repository, path, publication key, metadata
	// Model has NO authority over destination paths or storage
	// Host normalizes skill_id to ensure canonical hyphen-separated alphanumeric format
	rawID := strings.TrimSpace(output.SkillID)
	var b strings.Builder
	for _, r := range strings.ToLower(rawID) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	cleanID := b.String()
	for strings.Contains(cleanID, "--") {
		cleanID = strings.ReplaceAll(cleanID, "--", "-")
	}
	cleanID = strings.Trim(cleanID, "-")
	if !strings.HasPrefix(cleanID, "skill-") {
		cleanID = "skill-" + cleanID
	}
	normSkillID := cleanID

	sum := sha256.Sum256(candidateBytes)
	contentDigest := hex.EncodeToString(sum[:])

	department, _, ok := strings.Cut(n.RoleID, "/")
	if !ok {
		department = "investigacion"
	}

	manifest := skillregistry.Manifest{
		Name:                  normSkillID,
		Description:           fmt.Sprintf("Procedure for %s solving: %s", n.RoleID, n.ProblemStatement),
		Department:            department,
		OwnerRoleID:           n.RoleID,
		MemoryDomain:          department,
		BaseProtocol:          "verificacion_estado",
		VerifierRef:           "internal/verifier:v1",
		RequiredCapabilities:  []string{"verificacion_estado"},
		ManifestSchemaVersion: "skill-manifest/v2",
		ExecutionProfileRef:   a.cfg.ExecutionProfileID,
	}

	return AuthorRecord{
		SkillID:            normSkillID,
		CandidateBytes:     candidateBytes,
		Manifest:           manifest,
		ContentDigest:      contentDigest,
		AuthorProfileRef:   a.cfg.ExecutionProfileID,
		AuthorHarnessRunID: spec.Identity.RunID,
	}, nil
}
