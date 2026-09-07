package episode_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/memoryos/episode"
)

func sampleDescriptor(runID, profileID string) episode.RunDescriptor {
	return episode.RunDescriptor{
		RunID:                runID,
		OrganizationID:       "explorarte",
		TaskID:               101,
		AttemptID:            1,
		RoleID:               "ingenieria_ia/qa",
		ExecutionPrincipalID: "principal-1",
		ContextID:            "snap-1",
		ContextVersion:       "v1",
		ContextDigest:        strings.Repeat("a", 64),
		ExecutionProfileID:   profileID,
		ModelPolicyRef:       "policy/standard",
		BuildRef:             "build/v1",
		MaxTurns:             5,
		MaxToolCalls:         2,
		FrozenTools: []episode.FrozenToolRef{
			{Name: "search", DefinitionDigest: strings.Repeat("b", 64)},
		},
		IdentityDigest: strings.Repeat("c", 64),
	}
}

func sampleContext(purpose string) episode.ContextUse {
	return episode.ContextUse{
		SnapshotID:            "snap-1",
		SnapshotVersion:       "v1",
		SnapshotDigest:        strings.Repeat("a", 64),
		ProviderVisibleDigest: strings.Repeat("a", 64),
		TaskClass:             "qa_verification",
		ExecutionPurpose:      purpose,
		Status:                "active",
	}
}

func sampleEvents(now time.Time) []episode.EventFact {
	return []episode.EventFact{
		{
			Sequence:   1,
			Type:       "run_started",
			RecordedAt: now,
		},
		{
			Sequence:      2,
			Type:          "model_request_prepared",
			InvocationRef: "inv-1",
			RecordedAt:    now.Add(100 * time.Millisecond),
		},
		{
			Sequence:      3,
			Type:          "model_response_received",
			InvocationRef: "inv-1",
			RecordedAt:    now.Add(200 * time.Millisecond),
		},
		{
			Sequence:       4,
			Type:           "run_finished",
			TerminalStatus: "success",
			RecordedAt:     now.Add(300 * time.Millisecond),
		},
	}
}

func TestEpisodeGrainIsHarnessRun(t *testing.T) {
	now := time.Date(2026, 9, 5, 1, 0, 0, 0, time.UTC)

	// Two Harness runs within the same task and attempt
	descPlan := sampleDescriptor("run-plan-1", "profile/planner")
	descExec := sampleDescriptor("run-exec-1", "profile/executor")

	inputPlan := episode.ProjectionInput{
		Descriptor: descPlan,
		TaskClass:  "qa_verification",
		Context:    sampleContext("planning"),
		Events:     sampleEvents(now),
	}
	inputExec := episode.ProjectionInput{
		Descriptor: descExec,
		TaskClass:  "qa_verification",
		Context:    sampleContext("execution"),
		Events:     sampleEvents(now.Add(time.Second)),
	}

	epPlan, err := episode.Project(inputPlan)
	if err != nil {
		t.Fatalf("Project plan run: %v", err)
	}
	epExec, err := episode.Project(inputExec)
	if err != nil {
		t.Fatalf("Project exec run: %v", err)
	}

	if epPlan.ID == epExec.ID {
		t.Fatalf("Episodes must have distinct IDs: plan=%s exec=%s", epPlan.ID, epExec.ID)
	}
	if epPlan.HarnessRunID != "run-plan-1" || epExec.HarnessRunID != "run-exec-1" {
		t.Fatalf("Episodes must preserve their HarnessRunID")
	}
	if epPlan.TaskID != epExec.TaskID || epPlan.AttemptID != epExec.AttemptID {
		t.Fatalf("Episodes must preserve same TaskID (%d) and AttemptID (%d)", epPlan.TaskID, epPlan.AttemptID)
	}
	if epPlan.ExecutionProfileID != "profile/planner" || epExec.ExecutionProfileID != "profile/executor" {
		t.Fatalf("Episodes must retain distinct execution profiles")
	}
}

func TestEpisodeOptionalDecisionRunIDAndVerification(t *testing.T) {
	now := time.Date(2026, 9, 5, 1, 0, 0, 0, time.UTC)
	desc := sampleDescriptor("run-fail-1", "profile/planner")

	// Episode that halted on model_error before any decision graph was formed
	events := []episode.EventFact{
		{Sequence: 1, Type: "run_started", RecordedAt: now},
		{Sequence: 2, Type: "model_error", ErrorCode: "provider_overloaded", RecordedAt: now.Add(100 * time.Millisecond)},
		{Sequence: 3, Type: "run_finished", TerminalStatus: "model_error", RecordedAt: now.Add(200 * time.Millisecond)},
	}

	input := episode.ProjectionInput{
		Descriptor:   desc,
		TaskClass:    "qa_verification",
		Context:      sampleContext("planning"),
		Events:       events,
		Verification: nil, // no verification
	}

	ep, err := episode.Project(input)
	if err != nil {
		t.Fatalf("Project failed run: %v", err)
	}

	if ep.DecisionRunID != nil {
		t.Fatalf("Expected nil DecisionRunID, got %v", *ep.DecisionRunID)
	}
	if ep.Verification != nil {
		t.Fatalf("Expected nil Verification, got %+v", ep.Verification)
	}
	if ep.TerminalStatus != "model_error" {
		t.Fatalf("Expected terminal status model_error, got %s", ep.TerminalStatus)
	}
	if ep.ID == "" || ep.CanonicalDigest == "" {
		t.Fatalf("Episode without decision graph must still produce stable ID and canonical digest")
	}
}

func TestEpisodeSkillsIncludedVsOmitted(t *testing.T) {
	now := time.Date(2026, 9, 5, 1, 0, 0, 0, time.UTC)
	desc := sampleDescriptor("run-skills-1", "profile/planner")

	tTrue := true
	tFalse := false

	skills := []episode.SkillFact{
		{
			SkillID:     "skill-audit",
			Version:     "v1.0.0",
			ContentHash: strings.Repeat("d", 64),
			Available:   &tTrue,
			Requested:   &tTrue,
			Resolved:    &tTrue,
			Included:    true,
		},
		{
			SkillID:     "skill-forge",
			Version:     "v0.1.0",
			ContentHash: strings.Repeat("e", 64),
			Available:   &tTrue,
			Requested:   &tFalse,
			Resolved:    &tFalse,
			Included:    false,
		},
	}

	input := episode.ProjectionInput{
		Descriptor: desc,
		TaskClass:  "qa_verification",
		Context:    sampleContext("planning"),
		Events:     sampleEvents(now),
		Skills:     skills,
	}

	ep, err := episode.Project(input)
	if err != nil {
		t.Fatalf("Project with skills: %v", err)
	}

	if len(ep.Skills) != 2 {
		t.Fatalf("Expected 2 skills, got %d", len(ep.Skills))
	}

	var foundIncluded, foundOmitted bool
	for _, sk := range ep.Skills {
		if sk.SkillID == "skill-audit" {
			foundIncluded = true
			if !sk.Included {
				t.Errorf("Expected skill-audit to be included")
			}
		}
		if sk.SkillID == "skill-forge" {
			foundOmitted = true
			if sk.Included {
				t.Errorf("Expected skill-forge to be omitted (included=false)")
			}
		}
	}
	if !foundIncluded || !foundOmitted {
		t.Fatalf("Missing expected skill in projected skills")
	}
}

func TestEpisodeBindingModeHomogeneousAndMixed(t *testing.T) {
	now := time.Date(2026, 9, 5, 1, 0, 0, 0, time.UTC)
	tokens := int64(100)

	// 1. Homogeneous invocations: both from same provider and model
	desc1 := sampleDescriptor("run-homo-1", "profile/standard")
	inputHomo := episode.ProjectionInput{
		Descriptor: desc1,
		TaskClass:  "qa_verification",
		Context:    sampleContext("exec"),
		Events:     sampleEvents(now),
		Invocations: []episode.InvocationFact{
			{InvocationID: 1, ProviderID: "anthropic", ProviderModelID: "claude-3-5-sonnet", InputTokens: &tokens, OutputTokens: &tokens, Status: "completed", CreatedAt: now},
			{InvocationID: 2, ProviderID: "anthropic", ProviderModelID: "claude-3-5-sonnet", InputTokens: &tokens, OutputTokens: &tokens, Status: "completed", CreatedAt: now.Add(time.Second)},
		},
	}
	epHomo, err := episode.Project(inputHomo)
	if err != nil {
		t.Fatalf("Project homogeneous: %v", err)
	}
	if epHomo.BindingMode != episode.BindingModeHomogeneous {
		t.Fatalf("Expected BindingModeHomogeneous, got %s", epHomo.BindingMode)
	}

	// 2. Mixed invocations: different provider and/or model
	desc2 := sampleDescriptor("run-mixed-1", "profile/standard")
	inputMixed := episode.ProjectionInput{
		Descriptor: desc2,
		TaskClass:  "qa_verification",
		Context:    sampleContext("exec"),
		Events:     sampleEvents(now),
		Invocations: []episode.InvocationFact{
			{InvocationID: 1, ProviderID: "anthropic", ProviderModelID: "claude-3-5-sonnet", InputTokens: &tokens, OutputTokens: &tokens, Status: "completed", CreatedAt: now},
			{InvocationID: 2, ProviderID: "google", ProviderModelID: "gemini-1.5-pro", InputTokens: &tokens, OutputTokens: &tokens, Status: "completed", CreatedAt: now.Add(time.Second)},
		},
	}
	epMixed, err := episode.Project(inputMixed)
	if err != nil {
		t.Fatalf("Project mixed: %v", err)
	}
	if epMixed.BindingMode != episode.BindingModeMixed {
		t.Fatalf("Expected BindingModeMixed, got %s", epMixed.BindingMode)
	}
}

func TestEpisodeToolsNullableLatency(t *testing.T) {
	now := time.Date(2026, 9, 5, 1, 0, 0, 0, time.UTC)
	desc := sampleDescriptor("run-tools-1", "profile/standard")

	events := []episode.EventFact{
		{Sequence: 1, Type: "run_started", RecordedAt: now},
		{Sequence: 2, Type: "tool_call_requested", ToolCallID: "tc-1", ToolName: "search", RecordedAt: now.Add(10 * time.Millisecond)},
		{Sequence: 3, Type: "tool_result_recorded", ToolCallID: "tc-1", ToolName: "search", RecordedAt: now.Add(50 * time.Millisecond)},
		{Sequence: 4, Type: "tool_call_requested", ToolCallID: "tc-2", ToolName: "search", RecordedAt: now.Add(60 * time.Millisecond)},
		{Sequence: 5, Type: "tool_call_denied", ToolCallID: "tc-2", ToolName: "search", RecordedAt: now.Add(70 * time.Millisecond)},
		{Sequence: 6, Type: "run_finished", TerminalStatus: "success", RecordedAt: now.Add(100 * time.Millisecond)},
	}

	input := episode.ProjectionInput{
		Descriptor: desc,
		TaskClass:  "qa_verification",
		Context:    sampleContext("exec"),
		Events:     events,
	}

	ep, err := episode.Project(input)
	if err != nil {
		t.Fatalf("Project tools: %v", err)
	}

	if len(ep.Tools) != 2 {
		t.Fatalf("Expected 2 projected tools, got %d", len(ep.Tools))
	}

	for _, tool := range ep.Tools {
		if tool.ToolCallID == "tc-1" {
			if tool.Outcome != episode.ToolOutcomeOK {
				t.Errorf("tc-1 outcome expected ok, got %s", tool.Outcome)
			}
			if tool.LatencyMS == nil {
				t.Errorf("tc-1 latency expected non-nil (calculated from timestamps)")
			} else if *tool.LatencyMS != 40 {
				t.Errorf("tc-1 latency expected 40ms, got %d", *tool.LatencyMS)
			}
		}
		if tool.ToolCallID == "tc-2" {
			if tool.Outcome != episode.ToolOutcomeDenied {
				t.Errorf("tc-2 outcome expected denied, got %s", tool.Outcome)
			}
		}
	}
}

func TestEpisodeDeterministicHashAndReordering(t *testing.T) {
	now := time.Date(2026, 9, 5, 1, 0, 0, 0, time.UTC)
	desc := sampleDescriptor("run-det-1", "profile/standard")
	tokens := int64(150)
	cost1 := int64(3000)
	cost2 := int64(4500)

	events1 := []episode.EventFact{
		{Sequence: 1, Type: "run_started", RecordedAt: now},
		{Sequence: 2, Type: "model_request_prepared", InvocationRef: "inv-1", RecordedAt: now.Add(10 * time.Millisecond)},
		{Sequence: 3, Type: "model_response_received", InvocationRef: "inv-1", RecordedAt: now.Add(20 * time.Millisecond)},
		{Sequence: 4, Type: "run_finished", TerminalStatus: "success", RecordedAt: now.Add(30 * time.Millisecond)},
	}
	// Permuted events slice (different order)
	events2 := []episode.EventFact{
		events1[2],
		events1[0],
		events1[3],
		events1[1],
	}

	invocations1 := []episode.InvocationFact{
		{InvocationID: 10, ProviderID: "anthropic", ProviderModelID: "claude-3-5-sonnet", InputTokens: &tokens, OutputTokens: &tokens, Status: "completed", CreatedAt: now},
		{InvocationID: 20, ProviderID: "anthropic", ProviderModelID: "claude-3-5-sonnet", InputTokens: &tokens, OutputTokens: &tokens, Status: "completed", CreatedAt: now.Add(time.Second)},
	}
	invocations2 := []episode.InvocationFact{
		invocations1[1],
		invocations1[0],
	}

	costs1 := []episode.CostFact{
		{InvocationID: 10, ActualUSDNanos: &cost1},
		{InvocationID: 20, ActualUSDNanos: &cost2},
	}
	costs2 := []episode.CostFact{
		costs1[1],
		costs1[0],
	}

	skills1 := []episode.SkillFact{
		{SkillID: "skill-a", Version: "v1", ContentHash: strings.Repeat("a", 64), Included: true},
		{SkillID: "skill-b", Version: "v2", ContentHash: strings.Repeat("b", 64), Included: false},
	}
	skills2 := []episode.SkillFact{
		skills1[1],
		skills1[0],
	}

	input1 := episode.ProjectionInput{
		Descriptor:  desc,
		TaskClass:   "qa_verification",
		Context:     sampleContext("exec"),
		Events:      events1,
		Invocations: invocations1,
		Costs:       costs1,
		Skills:      skills1,
	}
	input2 := episode.ProjectionInput{
		Descriptor:  desc,
		TaskClass:   "qa_verification",
		Context:     sampleContext("exec"),
		Events:      events2,
		Invocations: invocations2,
		Costs:       costs2,
		Skills:      skills2,
	}

	ep1, err := episode.Project(input1)
	if err != nil {
		t.Fatalf("Project 1: %v", err)
	}
	ep2, err := episode.Project(input2)
	if err != nil {
		t.Fatalf("Project 2: %v", err)
	}

	if ep1.CanonicalDigest != ep2.CanonicalDigest {
		t.Fatalf("Expected identical CanonicalDigest despite reordering:\n1: %s\n2: %s", ep1.CanonicalDigest, ep2.CanonicalDigest)
	}
	if ep1.ID != ep2.ID {
		t.Fatalf("Expected identical Episode ID")
	}
	if ep1.Observability.SourceFactsDigest != ep2.Observability.SourceFactsDigest {
		t.Fatalf("Expected identical SourceFactsDigest")
	}
}

func TestEpisodeNoSecretsOrClinicalContent(t *testing.T) {
	now := time.Date(2026, 9, 5, 1, 0, 0, 0, time.UTC)
	desc := sampleDescriptor("run-privacy-1", "profile/standard")

	input := episode.ProjectionInput{
		Descriptor: desc,
		TaskClass:  "qa_verification",
		Context:    sampleContext("exec"),
		Events:     sampleEvents(now),
	}

	ep, err := episode.Project(input)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}

	rawBytes, err := json.Marshal(ep)
	if err != nil {
		t.Fatalf("Marshal episode: %v", err)
	}
	raw := string(rawBytes)

	// Invariants: No raw tokens, private instructions, or prompt content
	forbidden := []string{
		"descriptor-lease-token",
		"clinical",
		"prompt",
		"private description",
		"chain_of_thought",
	}
	for _, term := range forbidden {
		if strings.Contains(strings.ToLower(raw), term) {
			t.Errorf("Episode contains forbidden term %q in serialized state", term)
		}
	}
}

func TestModelInvocationAttributionZeroOneMany(t *testing.T) {
	now := time.Date(2026, 9, 5, 1, 0, 0, 0, time.UTC)
	desc := sampleDescriptor("run-inv-attr", "profile/standard")
	ctxUse := sampleContext("exec")
	events := sampleEvents(now)

	// 1. Zero invocations: empty invocations, BindingModeHomogeneous, NO fabricated provider or model anywhere
	inputZero := episode.ProjectionInput{
		Descriptor:  desc,
		TaskClass:   "qa_verification",
		Context:     ctxUse,
		Events:      events,
		Invocations: []episode.InvocationFact{},
	}
	epZero, err := episode.Project(inputZero)
	if err != nil {
		t.Fatalf("Project zero invocations: %v", err)
	}
	if len(epZero.Invocations) != 0 {
		t.Fatalf("expected 0 invocations, got %d", len(epZero.Invocations))
	}
	if epZero.BindingMode != episode.BindingModeHomogeneous {
		t.Fatalf("zero invocations binding mode=%s want homogeneous", epZero.BindingMode)
	}

	// 2. Exactly one invocation -> BindingModeHomogeneous
	inputOne := episode.ProjectionInput{
		Descriptor: desc,
		TaskClass:  "qa_verification",
		Context:    ctxUse,
		Events:     events,
		Invocations: []episode.InvocationFact{
			{InvocationID: 1, ProviderID: "anthropic", ProviderModelID: "claude-3-5-sonnet", Status: "succeeded"},
		},
	}
	epOne, err := episode.Project(inputOne)
	if err != nil {
		t.Fatalf("Project one invocation: %v", err)
	}
	if epOne.BindingMode != episode.BindingModeHomogeneous {
		t.Fatalf("one invocation binding mode=%s want homogeneous", epOne.BindingMode)
	}
	if len(epOne.Invocations) != 1 {
		t.Fatalf("expected 1 invocation, got %d", len(epOne.Invocations))
	}

	// 3. N same invocations -> BindingModeHomogeneous
	inputSame := episode.ProjectionInput{
		Descriptor: desc,
		TaskClass:  "qa_verification",
		Context:    ctxUse,
		Events:     events,
		Invocations: []episode.InvocationFact{
			{InvocationID: 1, ProviderID: "anthropic", ProviderModelID: "claude-3-5-sonnet", Status: "succeeded"},
			{InvocationID: 2, ProviderID: "anthropic", ProviderModelID: "claude-3-5-sonnet", Status: "succeeded"},
			{InvocationID: 3, ProviderID: "anthropic", ProviderModelID: "claude-3-5-sonnet", Status: "succeeded"},
		},
	}
	epSame, err := episode.Project(inputSame)
	if err != nil {
		t.Fatalf("Project N same invocations: %v", err)
	}
	if epSame.BindingMode != episode.BindingModeHomogeneous {
		t.Fatalf("N same invocations binding mode=%s want homogeneous", epSame.BindingMode)
	}

	// 4. N mixed invocations -> BindingModeMixed
	inputMixed := episode.ProjectionInput{
		Descriptor: desc,
		TaskClass:  "qa_verification",
		Context:    ctxUse,
		Events:     events,
		Invocations: []episode.InvocationFact{
			{InvocationID: 1, ProviderID: "anthropic", ProviderModelID: "claude-3-5-sonnet", Status: "succeeded"},
			{InvocationID: 2, ProviderID: "google", ProviderModelID: "gemini-1.5-pro", Status: "succeeded"},
		},
	}
	epMixed, err := episode.Project(inputMixed)
	if err != nil {
		t.Fatalf("Project N mixed invocations: %v", err)
	}
	if epMixed.BindingMode != episode.BindingModeMixed {
		t.Fatalf("N mixed invocations binding mode=%s want mixed", epMixed.BindingMode)
	}
}
