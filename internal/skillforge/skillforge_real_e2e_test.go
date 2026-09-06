package skillforge

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/executionharness"
	"github.com/Mireuz13/explorarte-organization/internal/memoryos/episode"
	"github.com/Mireuz13/explorarte-organization/internal/platform/skillpublisher"
	"github.com/Mireuz13/explorarte-organization/internal/skillforge/need"
	"github.com/Mireuz13/explorarte-organization/internal/skillforge/source"
	"github.com/Mireuz13/explorarte-organization/internal/skillregistry"
	"github.com/Mireuz13/explorarte-organization/internal/skillregistry/contextprovider"
)

// Section 7 Cost Bounds
const (
	MaxAuthoringCost   = 0.10
	MaxBaselineCost    = 0.05
	MaxCandidateCost   = 0.05
	MaxAdversarialCost = 0.15
	MaxCanaryCost      = 0.05
	MaxTotalCost       = 0.40
)

type RealRunEvidence struct {
	HarnessRunID       string
	RoleID             string
	ExecutionProfileID string
	ModelPolicyRef     string
	Provider           string
	Model              string
	InputTokens        int64
	OutputTokens       int64
	ActualCost         float64
	TerminalStatus     executionharness.RunStatus
	ContextSnapshotID  string
	PromptDigest       string
}

type RealHarnessModelExecutor struct {
	geminiKey  string
	grokKey    string
	httpClient *http.Client
	totalCost  float64
	runs       map[string]RealRunEvidence
	mu         sync.Mutex
}

func NewRealHarnessModelExecutor() (*RealHarnessModelExecutor, error) {
	geminiKey, err := readSecret("gemini-embedding-api-key", "ORG_MODEL_PROVIDER_GEMINI_CREDENTIAL_FILE")
	if err != nil {
		return nil, fmt.Errorf("read gemini secret: %w", err)
	}
	grokKey, err := readSecret("grok-api-key", "ORG_MODEL_PROVIDER_XAI_CREDENTIAL_FILE")
	if err != nil {
		return nil, fmt.Errorf("read grok secret: %w", err)
	}

	return &RealHarnessModelExecutor{
		geminiKey: strings.TrimSpace(geminiKey),
		grokKey:   strings.TrimSpace(grokKey),
		httpClient: &http.Client{
			Timeout: 90 * time.Second,
		},
		runs: make(map[string]RealRunEvidence),
	}, nil
}

func readSecret(name, envVar string) (string, error) {
	if path := os.Getenv(envVar); path != "" {
		if data, err := os.ReadFile(path); err == nil {
			return string(data), nil
		}
	}
	paths := []string{
		filepath.Join("/etc/explorarte/secrets", name),
		filepath.Join("/run/secrets", name),
	}
	for _, p := range paths {
		if data, err := os.ReadFile(p); err == nil {
			return string(data), nil
		}
	}
	return "", fmt.Errorf("secret %s not found in standard paths", name)
}

func (e *RealHarnessModelExecutor) Invoke(ctx context.Context, id executionharness.RunIdentity, req executionharness.NormalizedModelRequest) (executionharness.ModelResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	var prefix struct {
		Context executionharness.InitialContext `json:"initial_context"`
		Policy  executionharness.RunPolicy      `json:"policy"`
	}
	if err := json.Unmarshal(req.StablePrefix, &prefix); err != nil {
		return executionharness.ModelResult{}, fmt.Errorf("unmarshal stable prefix: %w", err)
	}

	promptContent := prefix.Context.Content
	modelPolicy := prefix.Policy.ModelPolicyRef
	profileID := prefix.Policy.ExecutionProfileID

	// Determine purpose and bound
	var purpose string
	var maxCost float64
	switch {
	case strings.Contains(id.RunID, "author"):
		purpose = "authoring"
		maxCost = MaxAuthoringCost
	case strings.Contains(id.RunID, "base"):
		purpose = "baseline"
		maxCost = MaxBaselineCost
	case strings.Contains(id.RunID, "cand"):
		purpose = "candidate"
		maxCost = MaxCandidateCost
	case strings.Contains(id.RunID, "adv"):
		purpose = "adversarial"
		maxCost = MaxAdversarialCost
	case strings.Contains(id.RunID, "canary"):
		purpose = "canary"
		maxCost = MaxCanaryCost
	default:
		purpose = "general"
		maxCost = 0.05
	}

	var endpoint string
	var apiKey string
	var provider string
	var modelName string
	var payload map[string]any

	if modelPolicy == "research.adversarial_review" {
		// xAI Grok
		provider = "xai"
		modelName = "grok-4.6"
		endpoint = "https://api.x.ai/v1/chat/completions"
		apiKey = e.grokKey
		payload = map[string]any{
			"model": modelName,
			"messages": []map[string]string{
				{"role": "user", "content": promptContent},
			},
		}
	} else {
		// Gemini (worker.skill_forge or worker.skill_forge_evaluator)
		provider = "gemini"
		modelName = "gemini-3.5-flash-lite"
		endpoint = "https://generativelanguage.googleapis.com/v1beta/openai/chat/completions"
		apiKey = e.geminiKey
		payload = map[string]any{
			"model": modelName,
			"messages": []map[string]string{
				{"role": "user", "content": promptContent},
			},
		}
	}

	reqBody, err := json.Marshal(payload)
	if err != nil {
		return executionharness.ModelResult{}, fmt.Errorf("marshal payload: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(reqBody))
	if err != nil {
		return executionharness.ModelResult{}, fmt.Errorf("new http request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := e.httpClient.Do(httpReq)
	if err != nil {
		return executionharness.ModelResult{}, fmt.Errorf("%s provider call failed: %w", provider, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return executionharness.ModelResult{}, fmt.Errorf("read %s response: %w", provider, err)
	}

	if resp.StatusCode != http.StatusOK {
		return executionharness.ModelResult{}, fmt.Errorf("%s provider returned status %d: %s", provider, resp.StatusCode, string(respBody))
	}

	var chatResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int64 `json:"prompt_tokens"`
			CompletionTokens int64 `json:"completion_tokens"`
			CostInUsdTicks   int64 `json:"cost_in_usd_ticks,omitempty"`
		} `json:"usage"`
	}

	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return executionharness.ModelResult{}, fmt.Errorf("unmarshal %s chat response: %w", provider, err)
	}

	if len(chatResp.Choices) == 0 {
		return executionharness.ModelResult{}, fmt.Errorf("%s returned zero choices", provider)
	}

	outputContent := chatResp.Choices[0].Message.Content
	inTokens := chatResp.Usage.PromptTokens
	outTokens := chatResp.Usage.CompletionTokens

	// Calculate actual cost
	var callCost float64
	if provider == "xai" {
		if chatResp.Usage.CostInUsdTicks > 0 {
			callCost = float64(chatResp.Usage.CostInUsdTicks) / 1e9
		} else {
			callCost = (float64(inTokens)*3.00 + float64(outTokens)*15.00) / 1000000.0
		}
	} else {
		// gemini-3.5-flash-lite pricing: $0.075 / 1M prompt, $0.30 / 1M completion
		callCost = (float64(inTokens)*0.075 + float64(outTokens)*0.30) / 1000000.0
	}

	// Section 7 Cost Bound Checks
	if callCost > maxCost {
		return executionharness.ModelResult{}, fmt.Errorf("cost bound exceeded for %s: cost $%.6f exceeds limit $%.4f", purpose, callCost, maxCost)
	}
	if e.totalCost+callCost > MaxTotalCost {
		return executionharness.ModelResult{}, fmt.Errorf("total cost bound exceeded: $%.6f exceeds MAX_TOTAL_COST $%.2f", e.totalCost+callCost, MaxTotalCost)
	}
	e.totalCost += callCost

	// Section 8 Durable Evidence Recording (No CoT, No secrets stored)
	promptSum := sha256.Sum256([]byte(promptContent))
	evidence := RealRunEvidence{
		HarnessRunID:       id.RunID,
		RoleID:             id.RoleID,
		ExecutionProfileID: profileID,
		ModelPolicyRef:     modelPolicy,
		Provider:           provider,
		Model:              modelName,
		InputTokens:        inTokens,
		OutputTokens:       outTokens,
		ActualCost:         callCost,
		TerminalStatus:     executionharness.StatusCompleted,
		ContextSnapshotID:  prefix.Context.ID,
		PromptDigest:       hex.EncodeToString(promptSum[:]),
	}
	e.runs[id.RunID] = evidence

	return executionharness.ModelResult{
		FinalOutput:  outputContent,
		FinishReason: executionharness.FinishFinal,
		Usage: executionharness.Usage{
			InputTokens:  &inTokens,
			OutputTokens: &outTokens,
		},
		InvocationRef: fmt.Sprintf("inv-%s", id.RunID),
	}, nil
}

func TestRealSkillForgeBoundedE2E(t *testing.T) {
	ctx := context.Background()
	orgID := "explorarte"

	// Section 7: Print Cost Bounds before execution
	fmt.Printf("\n=== SKILL FORGE BOUNDED E2E COST LIMITS ===\n")
	fmt.Printf("MAX_AUTHORING_COST:   $%.2f\n", MaxAuthoringCost)
	fmt.Printf("MAX_BASELINE_COST:    $%.2f\n", MaxBaselineCost)
	fmt.Printf("MAX_CANDIDATE_COST:   $%.2f\n", MaxCandidateCost)
	fmt.Printf("MAX_ADVERSARIAL_COST: $%.2f\n", MaxAdversarialCost)
	fmt.Printf("MAX_CANARY_COST:      $%.2f\n", MaxCanaryCost)
	fmt.Printf("MAX_TOTAL_COST:       $%.2f\n", MaxTotalCost)
	fmt.Printf("===========================================\n\n")

	// 1. Setup Real Model Executor
	realExecutor, err := NewRealHarnessModelExecutor()
	if err != nil {
		t.Skipf("skipping real external model E2E test: secrets unavailable: %v", err)
	}

	// 2. Setup ExecutionHarness with memory history and descriptor stores
	history := executionharness.NewMemoryHistoryStore()
	descriptors := executionharness.NewMemoryRunDescriptorStore()
	harness, err := executionharness.NewWithDescriptorStore(
		harnessTestAuthority{},
		realExecutor,
		harnessTestCatalog{},
		harnessTestTools{},
		history,
		descriptors,
	)
	if err != nil {
		t.Fatalf("create execution harness: %v", err)
	}

	// 3. Setup disposable Git Publisher and remote
	bareRemoteDir := filepath.Join(t.TempDir(), "explorarte-org", "skills.git")
	testInitBareRepo(t, bareRemoteDir)

	publisherDir := t.TempDir()
	testInitGitRepo(t, publisherDir)
	testRunCmd(t, publisherDir, "git", "remote", "add", "origin", bareRemoteDir)
	testRunCmd(t, publisherDir, "git", "push", "-u", "origin", "main")

	publisher, err := skillpublisher.NewGitPublisher(skillpublisher.GitPublisherConfig{
		RepoDir:           publisherDir,
		RemoteName:        "origin",
		ExpectedRemoteURL: bareRemoteDir,
		Branch:            "main",
		Owner:             "explorarte-org",
		Repo:              "skills",
		RequireRemote:     true,
	})
	if err != nil {
		t.Fatalf("create GitPublisher: %v", err)
	}

	// 4. Setup separate runtime root and PinnedSourceReader
	runtimeRoot := t.TempDir()
	reader, err := skillpublisher.NewGitPinnedSourceReader(publisherDir)
	if err != nil {
		t.Fatalf("create GitPinnedSourceReader: %v", err)
	}
	materializer, err := source.NewLocalMaterializer(runtimeRoot, reader)
	if err != nil {
		t.Fatalf("create LocalMaterializer: %v", err)
	}

	// 5. Setup Authorer with Section 1 ProfileGate and D-006 Resolution
	authorer, err := NewHarnessAuthorer(harness, HarnessAuthorerConfig{
		ExecutionProfileID: "worker/skill-forge/v1",
		ModelPolicyRef:     "worker.skill_forge",
		BuildRef:           "skillforge/authorer:v1",
		ProfileGate:        CanonicalAuthoringProfileGate{},
	})
	if err != nil {
		t.Fatalf("create HarnessAuthorer: %v", err)
	}

	// 6. Setup Evaluator with real model harness and adversarial review
	evaluator := NewForgeEvaluatorWithHarness(runtimeRoot, harness, ForgeEvaluatorConfig{
		ExecutionProfileID:   "worker/skill-forge/v1",
		ModelPolicyRef:       "worker.skill_forge_evaluator",
		AdversarialProfileID: "worker/skill-forge/v1",
		AdversarialPolicyRef: "research.adversarial_review",
		BuildRef:             "skillforge/evaluator:v1",
		SuiteRef:             "skillforge/smoke-suite:v1",
	})

	needRepo := need.NewMemoryRepository()
	regRepo := newMemoryRegistryRepo()
	domainService := skillregistry.NewService(nil)
	manager, err := skillregistry.NewManager(domainService, regRepo, noopGate{})
	if err != nil {
		t.Fatalf("create manager: %v", err)
	}
	validator := NewStaticValidator(runtimeRoot)

	engine := NewEngine(
		needRepo,
		regRepo,
		manager,
		publisher,
		materializer,
		authorer,
		validator,
		evaluator,
	)

	// Step 1: Operator ProcedureNeed accepted by Owner
	pNeed := need.ProcedureNeed{
		ID:               "need-real-e2e-1",
		OrganizationID:   orgID,
		RoleID:           "recursos_agenticos/disenador_skills",
		TaskClass:        "smoke_verification",
		ProblemStatement: "Procedure for automated smoke verification of agentic skills",
		Status:           need.StatusAccepted,
		Revision:         1,
		CreatedAt:        time.Now().UTC(),
		UpdatedAt:        time.Now().UTC(),
		Acceptance: &need.Acceptance{
			DecisionRef: "decision-d006-accepted",
			AcceptedBy:  "empresa/owner",
			AcceptedAt:  time.Now().UTC(),
		},
	}
	_, err = needRepo.CreateNeed(ctx, pNeed)
	if err != nil {
		t.Fatalf("create need: %v", err)
	}

	// Section 10: Inspect initial production assignments (MUST be 0)
	initialAssignments := regRepo.assignments[pNeed.RoleID]
	if len(initialAssignments) != 0 {
		t.Fatalf("precondition failed: expected 0 assignments, got %d", len(initialAssignments))
	}

	// Step 2: Forge Run 1 -> Real Authoring Model Invocation -> halts at waiting_human_approval
	fmt.Printf("[E2E STEP 1/6] Invoking real authoring model via ExecutionHarness...\n")
	run1, err := engine.Run(ctx, orgID, pNeed.ID)
	if err != nil {
		t.Fatalf("initial run failed: %v", err)
	}
	if run1.Status != StatusWaitingHumanApproval {
		t.Fatalf("expected status %s, got %s", StatusWaitingHumanApproval, run1.Status)
	}
	fmt.Printf("✓ Authoring completed: SkillID=%s, DraftVersionID=%s\n", run1.Author.SkillID, run1.SkillVersionID)

	// Verify Section 8 Authoring Proof
	ver, err := regRepo.GetVersion(ctx, orgID, run1.SkillVersionID)
	if err != nil {
		t.Fatalf("get version: %v", err)
	}
	if ver.Lifecycle != skillregistry.LifecycleDraft {
		t.Fatalf("expected draft lifecycle, got %s", ver.Lifecycle)
	}

	// Step 3: Human Approval OUTSIDE Forge (Durable Governance Gate)
	fmt.Printf("[E2E STEP 2/6] Recording human approval outside Forge...\n")
	approvedVer, err := manager.HumanApprove(ctx, skillregistry.LifecycleMutationRequest{
		OrganizationID:   orgID,
		VersionID:        ver.ID,
		ExpectedRevision: ver.Revision,
		ActorRoleID:      "empresa/owner",
	}, skillregistry.ApprovalEvidence{
		DecisionRef: "decision-owner-approve-draft-001",
		ApprovedBy:  "empresa/owner",
		ApprovedAt:  time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("human approval failed: %v", err)
	}
	if approvedVer.Lifecycle != skillregistry.LifecycleHumanApproved {
		t.Fatalf("expected candidate lifecycle after approval, got %s", approvedVer.Lifecycle)
	}
	fmt.Printf("✓ Human approved: VersionID=%s promoted to Candidate\n", approvedVer.ID)

	// Step 4: Resume Forge Run -> Evaluates (Baseline, Candidate, Adversarial Model Review, Canary)
	fmt.Printf("[E2E STEP 3/6] Resuming Forge run (Real Baseline, Candidate, Model Adversarial Review, Canary)...\n")
	run2, err := engine.Run(ctx, orgID, pNeed.ID)
	if err != nil {
		t.Fatalf("resume run failed: %v", err)
	}
	if run2.Status != StatusCandidateReady {
		t.Fatalf("expected candidate_ready, got %s", run2.Status)
	}
	fmt.Printf("✓ Evaluation and Canary completed: Forge Status=%s\n", run2.Status)

	// ==========================================
	// POST-EXECUTION VERIFICATIONS
	// ==========================================

	// Section 10 & 13: CANDIDATE_REMAINS_CANDIDATE & ZERO_PRODUCTION_ASSIGNMENTS
	fmt.Printf("[E2E STEP 4/6] Verifying Lifecycle and Zero Production Assignments...\n")
	finalVer, err := regRepo.GetVersion(ctx, orgID, run2.SkillVersionID)
	if err != nil {
		t.Fatalf("get final version: %v", err)
	}
	if finalVer.Lifecycle != skillregistry.LifecycleCandidate {
		t.Fatalf("FATAL: candidate lifecycle modified! expected %s, got %s", skillregistry.LifecycleCandidate, finalVer.Lifecycle)
	}

	postAssignments := regRepo.assignments[pNeed.RoleID]
	if len(postAssignments) != 0 {
		t.Fatalf("FATAL: candidate gained production assignments! count=%d", len(postAssignments))
	}

	// Verify candidate NOT visible in ContextEngine resolution
	contextProvider, err := contextprovider.New(regRepo, orgID)
	if err != nil {
		t.Fatalf("create contextprovider: %v", err)
	}
	activeSkills, err := contextProvider.ListActiveForRole(ctx, orgID, pNeed.RoleID)
	if err != nil {
		t.Fatalf("list active skills: %v", err)
	}
	if len(activeSkills) != 0 {
		t.Fatalf("FATAL: candidate skill resolved in active context! count=%d", len(activeSkills))
	}
	fmt.Printf("✓ Lifecycle verified: Lifecycle=%s, ActiveAssignments=0\n", finalVer.Lifecycle)

	// Section 9: REAL EVALUATION PROOF
	eval := run2.Evaluation
	if eval == nil {
		t.Fatal("evaluation result is nil")
	}
	if !eval.Passed {
		t.Fatalf("evaluation did not pass: %+v", eval)
	}
	if eval.AdversarialVerdict != "pass" {
		t.Fatalf("adversarial review did not pass: %s", eval.AdversarialVerdict)
	}
	if eval.CanaryVerdict != "pass" {
		t.Fatalf("canary did not pass: %s", eval.CanaryVerdict)
	}
	if eval.BaselineHarnessRunID == "" || eval.CandidateHarnessRunID == "" || eval.AdversarialHarnessRunID == "" || eval.CanaryHarnessRunID == "" {
		t.Fatalf("evaluation missing harness run IDs: %+v", eval)
	}
	if eval.AdversarialReview == nil {
		t.Fatal("adversarial review structured data is nil")
	}
	if eval.AdversarialReview.PromptInjectionRisk || eval.AdversarialReview.AuthorityEscalation {
		t.Fatalf("adversarial review flagged security violations: %+v", eval.AdversarialReview)
	}

	// Section 11: MEMORYOS EPISODES PROJECTION
	fmt.Printf("[E2E STEP 5/6] Projecting MemoryOS Episodes for all 5 real runs...\n")
	runsToProject := []struct {
		Name  string
		RunID string
	}{
		{"authoring", run1.Author.AuthorHarnessRunID},
		{"baseline", eval.BaselineHarnessRunID},
		{"candidate", eval.CandidateHarnessRunID},
		{"adversarial", eval.AdversarialHarnessRunID},
		{"canary", eval.CanaryHarnessRunID},
	}

	for _, r := range runsToProject {
		evidence, ok := realExecutor.runs[r.RunID]
		if !ok {
			t.Fatalf("evidence for %s run %s not found in executor runs", r.Name, r.RunID)
		}

		// Read durable descriptor from descriptor store
		desc, err := descriptors.ReadRunDescriptor(ctx, orgID, r.RunID)
		if err != nil {
			t.Fatalf("read run descriptor for %s: %v", r.RunID, err)
		}

		// Read events from history
		events, err := history.Read(ctx, r.RunID)
		if err != nil {
			t.Fatalf("read history events for %s: %v", r.RunID, err)
		}

		var eventFacts []episode.EventFact
		for _, ev := range events {
			eventFacts = append(eventFacts, episode.EventFact{
				Sequence:       ev.Sequence,
				Type:           string(ev.Type),
				TerminalStatus: string(ev.TerminalStatus),
				RecordedAt:     time.Now().UTC(),
			})
		}

		nanos := int64(evidence.ActualCost * 1e9)
		inTokens := evidence.InputTokens
		outTokens := evidence.OutputTokens

		// Project MemoryOS episode
		input := episode.ProjectionInput{
			Descriptor: episode.RunDescriptor{
				RunID:                desc.RunID,
				OrganizationID:       desc.OrganizationID,
				TaskID:               desc.TaskID,
				AttemptID:            desc.AttemptID,
				RoleID:               desc.RoleID,
				ExecutionPrincipalID: desc.ExecutionPrincipalID,
				ContextID:            desc.ContextID,
				ContextVersion:       desc.ContextVersion,
				ContextDigest:        desc.ContextDigest,
				ExecutionProfileID:   desc.ExecutionProfileID,
				ModelPolicyRef:       desc.ModelPolicyRef,
				BuildRef:             desc.BuildRef,
				MaxTurns:             desc.MaxTurns,
				MaxToolCalls:         desc.MaxToolCalls,
				IdentityDigest:       desc.IdentityDigest,
			},
			TaskClass: "skill_forge_workflow",
			Context: episode.ContextUse{
				SnapshotID:            desc.ContextID,
				SnapshotVersion:       desc.ContextVersion,
				SnapshotDigest:        desc.ContextDigest,
				ProviderVisibleDigest: desc.ContextDigest,
				TaskClass:             "skill_forge_workflow",
				ExecutionPurpose:      "skillforge_e2e_evaluation",
			},
			Events: eventFacts,
			Invocations: []episode.InvocationFact{
				{
					InvocationID:    1,
					ProviderID:      evidence.Provider,
					ProviderModelID: evidence.Model,
					InputTokens:     &inTokens,
					OutputTokens:    &outTokens,
					Status:          "succeeded",
					CreatedAt:       time.Now().UTC(),
				},
			},
			Costs: []episode.CostFact{
				{
					InvocationID:   1,
					ActualUSDNanos: &nanos,
				},
			},
		}

		ep, err := episode.Project(input)
		if err != nil {
			t.Fatalf("MemoryOS episode projection for %s (%s) failed: %v", r.Name, r.RunID, err)
		}

		if ep.HarnessRunID != r.RunID {
			t.Fatalf("episode HarnessRunID mismatch: %s != %s", ep.HarnessRunID, r.RunID)
		}
		if ep.RoleID != desc.RoleID {
			t.Fatalf("episode RoleID mismatch: %s != %s", ep.RoleID, desc.RoleID)
		}
		if ep.ExecutionProfileID != desc.ExecutionProfileID {
			t.Fatalf("episode ExecutionProfileID mismatch: %s != %s", ep.ExecutionProfileID, desc.ExecutionProfileID)
		}
		fmt.Printf("✓ MemoryOS Episode projected for %-12s: EpisodeID=%s, Cost=$%.6f\n", r.Name, ep.ID, evidence.ActualCost)
	}

	// Section 7 & Cost Bounds Verification
	fmt.Printf("[E2E STEP 6/6] Verifying total cost within bound...\n")
	if realExecutor.totalCost > MaxTotalCost {
		t.Fatalf("TOTAL COST EXCEEDED: $%.6f > $%.2f", realExecutor.totalCost, MaxTotalCost)
	}
	fmt.Printf("✓ Total Cost: $%.6f (limit $%.2f) - WITHIN BOUNDS!\n", realExecutor.totalCost, MaxTotalCost)

	// Summary Report
	fmt.Printf("\n=======================================================\n")
	fmt.Printf("SKILL FORGE BOUNDED E2E EXECUTION REPORT\n")
	fmt.Printf("=======================================================\n")
	fmt.Printf("Candidate Skill ID:      %s\n", finalVer.SkillID)
	fmt.Printf("Candidate Version ID:    %s\n", finalVer.ID)
	fmt.Printf("Candidate Lifecycle:     %s\n", finalVer.Lifecycle)
	fmt.Printf("Active Assignments:      %d\n", len(postAssignments))
	fmt.Printf("Origin Ref:              %s\n", finalVer.Source.OriginRef)
	fmt.Printf("Publication Tag:         %s\n", run1.PublishedSource.PublicationRef)
	fmt.Printf("Canonical Hash:          %s\n", finalVer.CanonicalHash)
	fmt.Printf("Source Hash:             %s\n", finalVer.Source.SHA256)
	fmt.Printf("Adversarial Verdict:     %s\n", eval.AdversarialVerdict)
	fmt.Printf("Canary Verdict:          %s\n", eval.CanaryVerdict)
	fmt.Printf("Total Actual Cost:       $%.6f USD\n", realExecutor.totalCost)
	fmt.Printf("Harness Runs:\n")
	for id, ev := range realExecutor.runs {
		fmt.Printf("  - RunID: %-36s | Role: %-35s | Provider: %-6s | Model: %-22s | In/Out: %4d/%4d | Cost: $%.6f\n",
			id, ev.RoleID, ev.Provider, ev.Model, ev.InputTokens, ev.OutputTokens, ev.ActualCost)
	}
	fmt.Printf("=======================================================\n\n")
}
