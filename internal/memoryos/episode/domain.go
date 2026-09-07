// Package episode contains the metadata-only, deterministic MemoryOS episode
// projection. An episode is one Harness run; it is deliberately independent
// of Executive's sleep cycle and of any authority or consolidation service.
package episode

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

type BindingMode string

const (
	BindingModeHomogeneous BindingMode = "homogeneous"
	BindingModeMixed       BindingMode = "mixed"
)

const (
	ToolOutcomeRequested     = "requested"
	ToolOutcomeDenied        = "denied"
	ToolOutcomeOK            = "ok"
	ToolOutcomeError         = "error"
	ToolOutcomeIndeterminate = "indeterminate"
	VerificationScopeAttempt = "task_attempt"
	EpisodeStatusObserved    = "observed"
	EpisodeStatusIncomplete  = "incomplete"
)

// RunDescriptor is the secret-free identity frozen for one Harness run. The
// PostgreSQL adapter maps executionharness.RunDescriptor into this type so the
// domain package remains usable by deterministic/unit-test projections.
type RunDescriptor struct {
	RunID                string
	OrganizationID       string
	TaskID               int64
	AttemptID            int64
	RoleID               string
	ExecutionPrincipalID string

	ContextID      string
	ContextVersion string
	ContextDigest  string

	ExecutionProfileID string
	ModelPolicyRef     string
	BuildRef           string

	MaxTurns     int
	MaxToolCalls int

	FrozenTools []FrozenToolRef

	IdentityDigest string
}

type FrozenToolRef struct {
	Name             string `json:"name"`
	DefinitionDigest string `json:"definition_digest"`
}

// ContextUse identifies the exact canonical snapshot and provider-visible
// view. Bodies are intentionally absent: Context Engine remains the owner of
// context bytes and MemoryOS records only identity/provenance.
type ContextUse struct {
	SnapshotID             string `json:"snapshot_id"`
	SnapshotVersion        string `json:"snapshot_version"`
	SnapshotDigest         string `json:"snapshot_digest"`
	ProviderVisibleDigest  string `json:"provider_visible_digest"`
	ExecutionContextViewID *int64 `json:"execution_context_view_id,omitempty"`
	TaskClass              string `json:"task_class,omitempty"`
	ExecutionPurpose       string `json:"execution_purpose,omitempty"`
	Status                 string `json:"status,omitempty"`
}

// SkillUse represents a skill segment that was actually materialized for the
// context. The pointer state fields stay nil when the durable source did not
// expose that state; false must never be fabricated from absence.
type SkillUse struct {
	SkillID     string `json:"skill_id"`
	Version     string `json:"version"`
	ContentHash string `json:"content_hash"`
	Available   *bool  `json:"available,omitempty"`
	Requested   *bool  `json:"requested,omitempty"`
	Resolved    *bool  `json:"resolved,omitempty"`
	Included    bool   `json:"included"`
}

type ToolUse struct {
	ToolCallID       string `json:"tool_call_id"`
	ToolName         string `json:"tool_name"`
	DefinitionDigest string `json:"definition_digest,omitempty"`
	Outcome          string `json:"outcome"`
	LatencyMS        *int64 `json:"latency_ms,omitempty"`
	Provenance       string `json:"provenance,omitempty"`
}

type InvocationUse struct {
	InvocationID      int64      `json:"invocation_id"`
	ProviderID        string     `json:"provider_id"`
	ProviderModelID   string     `json:"provider_model_id"`
	InputTokens       *int64     `json:"input_tokens,omitempty"`
	OutputTokens      *int64     `json:"output_tokens,omitempty"`
	ReasoningTokens   *int64     `json:"reasoning_tokens,omitempty"`
	CostUSDNanos      *int64     `json:"cost_usd_nanos,omitempty"`
	EstimatedUSDNanos *int64     `json:"estimated_usd_nanos,omitempty"`
	Status            string     `json:"status"`
	CreatedAt         time.Time  `json:"created_at,omitempty"`
	TerminalAt        *time.Time `json:"terminal_at,omitempty"`
}

type ObligationObservation struct {
	Key             string   `json:"key"`
	Kind            string   `json:"kind"`
	Label           string   `json:"label"`
	VerifierRef     string   `json:"verifier_ref"`
	VerifierVersion string   `json:"verifier_version"`
	EvidenceDigest  string   `json:"evidence_digest,omitempty"`
	EvidenceRefs    []string `json:"evidence_refs,omitempty"`
}

type VerificationSummary struct {
	Verdict       string                  `json:"verdict"`
	VerifiedAt    *time.Time              `json:"verified_at,omitempty"`
	Scope         string                  `json:"scope"`
	DecisionRunID *int64                  `json:"decision_run_id,omitempty"`
	EvidenceRefs  []string                `json:"evidence_refs,omitempty"`
	Obligations   []ObligationObservation `json:"obligations"`
}

// CompletionObservation is the host-owned, metadata-only handoff produced at
// verification time. It is intentionally separate from Episode because a
// completion check is scoped to a task attempt and may predate a projection or
// be associated with more than one Harness run of that attempt.
type CompletionObservation struct {
	OrganizationID    string                  `json:"organization_id"`
	TaskID            int64                   `json:"task_id"`
	AttemptID         int64                   `json:"attempt_id"`
	ObservationDigest string                  `json:"observation_digest"`
	Verdict           string                  `json:"verdict"`
	VerifiedAt        time.Time               `json:"verified_at"`
	Obligations       []ObligationObservation `json:"obligations"`
}

// NewCompletionObservation computes an idempotency digest from the verified
// obligation facts. Detail text is intentionally not accepted and therefore
// cannot be persisted accidentally.
func NewCompletionObservation(organizationID string, taskID, attemptID int64, verdict string, verifiedAt time.Time, obligations []ObligationObservation) (CompletionObservation, error) {
	value := CompletionObservation{OrganizationID: organizationID, TaskID: taskID, AttemptID: attemptID, Verdict: verdict, VerifiedAt: verifiedAt.UTC(), Obligations: append([]ObligationObservation(nil), obligations...)}
	value.Obligations = cloneVerification(&VerificationSummary{Obligations: value.Obligations}).Obligations
	body, err := json.Marshal(struct {
		OrganizationID string                  `json:"organization_id"`
		TaskID         int64                   `json:"task_id"`
		AttemptID      int64                   `json:"attempt_id"`
		Verdict        string                  `json:"verdict"`
		VerifiedAt     time.Time               `json:"verified_at"`
		Obligations    []ObligationObservation `json:"obligations"`
	}{value.OrganizationID, value.TaskID, value.AttemptID, value.Verdict, value.VerifiedAt, value.Obligations})
	if err != nil {
		return CompletionObservation{}, err
	}
	sum := sha256.Sum256(body)
	value.ObservationDigest = hex.EncodeToString(sum[:])
	return value, nil
}

type EpisodeObservability struct {
	EventCount        int      `json:"event_count"`
	SourceFactsDigest string   `json:"source_facts_digest"`
	Incomplete        bool     `json:"incomplete"`
	IncompleteReasons []string `json:"incomplete_reasons,omitempty"`
}

type Episode struct {
	ID             string `json:"id"`
	OrganizationID string `json:"organization_id"`
	HarnessRunID   string `json:"harness_run_id"`
	TaskID         int64  `json:"task_id"`
	AttemptID      int64  `json:"attempt_id"`
	DecisionRunID  *int64 `json:"decision_run_id,omitempty"`

	RoleID               string `json:"role_id"`
	ExecutionPrincipalID string `json:"execution_principal_id"`
	TaskClass            string `json:"task_class"`
	ExecutionPurpose     string `json:"execution_purpose"`
	ExecutionProfileID   string `json:"execution_profile_id"`

	Context      ContextUse           `json:"context"`
	Skills       []SkillUse           `json:"skills"`
	Tools        []ToolUse            `json:"tools"`
	Invocations  []InvocationUse      `json:"invocations"`
	Verification *VerificationSummary `json:"verification,omitempty"`

	TurnsUsed             int    `json:"turns_used"`
	ToolCallsUsed         int    `json:"tool_calls_used"`
	ActualCostUSDNanos    *int64 `json:"actual_cost_usd_nanos,omitempty"`
	EstimatedCostUSDNanos *int64 `json:"estimated_cost_usd_nanos,omitempty"`

	BindingMode    BindingMode `json:"binding_mode"`
	TerminalStatus string      `json:"terminal_status"`
	Status         string      `json:"status"`
	StartedAt      *time.Time  `json:"started_at,omitempty"`
	FinishedAt     *time.Time  `json:"finished_at,omitempty"`

	Observability   EpisodeObservability `json:"observability"`
	CanonicalDigest string               `json:"canonical_digest"`
}

// EventFact is the event whitelist accepted by the pure projector. It has no
// payload, arguments, result body, reason or model output by construction.
type EventFact struct {
	Sequence       uint64
	Type           string
	InvocationRef  string
	ToolCallID     string
	ToolName       string
	ToolProvenance string
	ErrorCode      string
	TerminalStatus string
	RecordedAt     time.Time
}

type SkillFact struct {
	SkillID     string
	Version     string
	ContentHash string
	Available   *bool
	Requested   *bool
	Resolved    *bool
	Included    bool
}

type InvocationFact struct {
	InvocationID    int64
	ProviderID      string
	ProviderModelID string
	InputTokens     *int64
	OutputTokens    *int64
	ReasoningTokens *int64
	Status          string
	CreatedAt       time.Time
	TerminalAt      *time.Time
}

type CostFact struct {
	InvocationID      int64
	ActualUSDNanos    *int64
	EstimatedUSDNanos *int64
}

type ProjectionInput struct {
	Descriptor   RunDescriptor
	TaskClass    string
	Context      ContextUse
	Events       []EventFact
	Skills       []SkillFact
	Invocations  []InvocationFact
	Costs        []CostFact
	Verification *VerificationSummary
}

// EpisodeIDFor is the stable logical identity of one organization-scoped
// Harness run. It intentionally does not include mutable/late facts.
func EpisodeIDFor(organizationID, harnessRunID string) string {
	body, _ := json.Marshal(struct {
		OrganizationID string `json:"organization_id"`
		HarnessRunID   string `json:"harness_run_id"`
	}{organizationID, harnessRunID})
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func (e Episode) CanonicalBytes() ([]byte, error) {
	canonical := e
	canonical.CanonicalDigest = ""
	return json.Marshal(canonical)
}

func (e Episode) Digest() (string, error) {
	body, err := e.CanonicalBytes()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}

// Project deterministically converts durable, metadata-only facts into one
// Episode. No clock or current-state lookup participates in this function.
func Project(input ProjectionInput) (Episode, error) {
	d := input.Descriptor
	if strings.TrimSpace(d.RunID) == "" || strings.TrimSpace(d.OrganizationID) == "" || d.TaskID <= 0 || d.AttemptID <= 0 {
		return Episode{}, fmt.Errorf("episode: descriptor identity is incomplete")
	}
	if d.ContextID == "" || d.ContextVersion == "" || d.ContextDigest == "" {
		return Episode{}, fmt.Errorf("episode: descriptor context identity is incomplete")
	}
	if input.Context.SnapshotID == "" {
		input.Context.SnapshotID = d.ContextID
	}
	if input.Context.SnapshotVersion == "" {
		input.Context.SnapshotVersion = d.ContextVersion
	}
	if input.Context.ProviderVisibleDigest == "" {
		input.Context.ProviderVisibleDigest = d.ContextDigest
	}
	if input.TaskClass == "" {
		return Episode{}, fmt.Errorf("episode: task class is not a durable fact")
	}
	if input.Context.ExecutionPurpose == "" {
		return Episode{}, fmt.Errorf("episode: execution purpose is not a durable context fact")
	}

	events := append([]EventFact(nil), input.Events...)
	sort.SliceStable(events, func(i, j int) bool {
		if events[i].Sequence != events[j].Sequence {
			return events[i].Sequence < events[j].Sequence
		}
		return eventSortKey(events[i]) < eventSortKey(events[j])
	})
	reasons := make([]string, 0)
	for i, event := range events {
		if event.Sequence == 0 {
			reasons = append(reasons, "event_sequence_missing")
		}
		if i > 0 && event.Sequence == events[i-1].Sequence {
			reasons = append(reasons, "duplicate_event_sequence")
		}
		if i > 0 && event.Sequence != events[i-1].Sequence+1 {
			reasons = append(reasons, "event_sequence_gap")
		}
	}
	if len(events) == 0 {
		reasons = append(reasons, "events_missing")
	}

	tools := projectTools(events, d.FrozenTools, &reasons)
	invocations := projectInvocations(input.Invocations, input.Costs, &reasons)
	skills := projectSkills(input.Skills)
	verification := cloneVerification(input.Verification)

	var startedAt, finishedAt *time.Time
	terminalStatus := ""
	for _, event := range events {
		if !event.RecordedAt.IsZero() && startedAt == nil {
			value := event.RecordedAt.UTC()
			startedAt = &value
		}
		if event.TerminalStatus != "" {
			terminalStatus = event.TerminalStatus
			if !event.RecordedAt.IsZero() {
				value := event.RecordedAt.UTC()
				finishedAt = &value
			}
		}
	}
	if terminalStatus == "" {
		reasons = append(reasons, "terminal_event_missing")
	}
	if input.Context.Status == "invalidated" {
		reasons = append(reasons, "context_snapshot_invalidated")
	}

	actual, estimated := aggregateCosts(input.Costs, invocations, &reasons)
	binding := bindingMode(invocations)
	if binding == BindingModeMixed {
		// Mixed is a valid episode. It only tells consumers that provider/model
		// attribution is ambiguous; it is not a projection failure.
	}

	episode := Episode{
		ID: EpisodeIDFor(d.OrganizationID, d.RunID), OrganizationID: d.OrganizationID, HarnessRunID: d.RunID,
		TaskID: d.TaskID, AttemptID: d.AttemptID, RoleID: d.RoleID, ExecutionPrincipalID: d.ExecutionPrincipalID,
		TaskClass: input.TaskClass, ExecutionPurpose: input.Context.ExecutionPurpose, ExecutionProfileID: d.ExecutionProfileID,
		Context: input.Context, Skills: skills, Tools: tools, Invocations: invocations, Verification: verification,
		TurnsUsed: countTurns(events), ToolCallsUsed: countToolCalls(events),
		ActualCostUSDNanos: actual, EstimatedCostUSDNanos: estimated,
		BindingMode: binding, TerminalStatus: terminalStatus, Status: EpisodeStatusObserved,
		StartedAt: startedAt, FinishedAt: finishedAt,
		Observability: EpisodeObservability{EventCount: len(events), Incomplete: len(reasons) > 0, IncompleteReasons: uniqueStrings(reasons)},
	}
	if episode.Observability.Incomplete {
		episode.Status = EpisodeStatusIncomplete
	}
	sourceDigest, err := digestProjectionFacts(d, input, events)
	if err != nil {
		return Episode{}, err
	}
	episode.Observability.SourceFactsDigest = sourceDigest
	if verification != nil {
		episode.DecisionRunID = verification.DecisionRunID
	}
	digest, err := episode.Digest()
	if err != nil {
		return Episode{}, err
	}
	episode.CanonicalDigest = digest
	return episode, nil
}

func eventSortKey(e EventFact) string {
	return e.Type + "\x00" + e.InvocationRef + "\x00" + e.ToolCallID + "\x00" + e.ToolName + "\x00" + e.ErrorCode
}

func projectSkills(facts []SkillFact) []SkillUse {
	out := make([]SkillUse, 0, len(facts))
	for _, fact := range facts {
		out = append(out, SkillUse{SkillID: fact.SkillID, Version: fact.Version, ContentHash: fact.ContentHash, Available: cloneBool(fact.Available), Requested: cloneBool(fact.Requested), Resolved: cloneBool(fact.Resolved), Included: fact.Included})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return skillSortKey(out[i]) < skillSortKey(out[j])
	})
	return out
}

func skillSortKey(s SkillUse) string {
	return fmt.Sprintf("%s\x00%s\x00%s\x00%t", s.SkillID, s.Version, s.ContentHash, s.Included)
}

func projectInvocations(facts []InvocationFact, costs []CostFact, reasons *[]string) []InvocationUse {
	out := make([]InvocationUse, 0, len(facts))
	seen := make(map[int64]struct{}, len(facts))
	costByInvocation := make(map[int64]CostFact, len(costs))
	for _, cost := range costs {
		if _, exists := costByInvocation[cost.InvocationID]; !exists {
			costByInvocation[cost.InvocationID] = cost
		}
	}
	for _, fact := range facts {
		if fact.InvocationID <= 0 {
			*reasons = append(*reasons, "invocation_id_missing")
			continue
		}
		if _, exists := seen[fact.InvocationID]; exists {
			*reasons = append(*reasons, "duplicate_invocation")
			continue
		}
		seen[fact.InvocationID] = struct{}{}
		value := InvocationUse{InvocationID: fact.InvocationID, ProviderID: fact.ProviderID, ProviderModelID: fact.ProviderModelID, InputTokens: cloneInt64(fact.InputTokens), OutputTokens: cloneInt64(fact.OutputTokens), ReasoningTokens: cloneInt64(fact.ReasoningTokens), Status: fact.Status, CreatedAt: utcTime(fact.CreatedAt), TerminalAt: utcTimePtr(fact.TerminalAt)}
		if cost, exists := costByInvocation[fact.InvocationID]; exists {
			value.CostUSDNanos = cloneInt64(cost.ActualUSDNanos)
			value.EstimatedUSDNanos = cloneInt64(cost.EstimatedUSDNanos)
		}
		out = append(out, value)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].InvocationID != out[j].InvocationID {
			return out[i].InvocationID < out[j].InvocationID
		}
		return out[i].ProviderID+"\x00"+out[i].ProviderModelID < out[j].ProviderID+"\x00"+out[j].ProviderModelID
	})
	return out
}

func projectTools(events []EventFact, descriptors []FrozenToolRef, reasons *[]string) []ToolUse {
	definitions := make(map[string]string, len(descriptors))
	for _, descriptor := range descriptors {
		if _, exists := definitions[descriptor.Name]; exists {
			*reasons = append(*reasons, "duplicate_frozen_tool")
		}
		definitions[descriptor.Name] = descriptor.DefinitionDigest
	}
	type trajectory struct {
		ToolUse
		requestedAt time.Time
		resolved    bool
	}
	items := make(map[string]*trajectory)
	order := make([]string, 0)
	for _, event := range events {
		if event.Type != "tool_call_requested" && event.Type != "tool_call_denied" && event.Type != "tool_result_recorded" {
			continue
		}
		key := event.ToolCallID
		if key == "" {
			key = fmt.Sprintf("sequence:%d", event.Sequence)
			*reasons = append(*reasons, "tool_call_id_missing")
		}
		item := items[key]
		if item == nil {
			item = &trajectory{ToolUse: ToolUse{ToolCallID: event.ToolCallID, ToolName: event.ToolName, Outcome: ToolOutcomeRequested, DefinitionDigest: definitions[event.ToolName]}, requestedAt: event.RecordedAt}
			items[key] = item
			order = append(order, key)
		} else if event.ToolName != "" && item.ToolName != "" && event.ToolName != item.ToolName {
			*reasons = append(*reasons, "tool_call_name_drift")
		}
		if item.ToolName == "" {
			item.ToolName = event.ToolName
			item.DefinitionDigest = definitions[event.ToolName]
		}
		switch event.Type {
		case "tool_call_requested":
			if item.resolved {
				*reasons = append(*reasons, "tool_call_replayed")
			}
			item.Outcome = ToolOutcomeRequested
			item.requestedAt = event.RecordedAt
		case "tool_call_denied":
			item.Outcome = ToolOutcomeDenied
			item.resolved = true
			item.Provenance = event.ToolProvenance
			setLatency(&item.ToolUse, item.requestedAt, event.RecordedAt)
		case "tool_result_recorded":
			if event.ErrorCode != "" {
				item.Outcome = ToolOutcomeError
			} else {
				item.Outcome = ToolOutcomeOK
			}
			item.resolved = true
			item.Provenance = event.ToolProvenance
			setLatency(&item.ToolUse, item.requestedAt, event.RecordedAt)
		}
	}
	for _, event := range events {
		if event.TerminalStatus != "" && (event.TerminalStatus == "indeterminate_tool_execution" || event.TerminalStatus == "tool_error") {
			for _, item := range items {
				if !item.resolved {
					if event.TerminalStatus == "indeterminate_tool_execution" {
						item.Outcome = ToolOutcomeIndeterminate
					} else {
						item.Outcome = ToolOutcomeError
					}
				}
			}
		}
	}
	out := make([]ToolUse, 0, len(order))
	for _, key := range order {
		out = append(out, items[key].ToolUse)
	}
	sort.SliceStable(out, func(i, j int) bool { return toolSortKey(out[i]) < toolSortKey(out[j]) })
	return out
}

func toolSortKey(t ToolUse) string {
	latency := ""
	if t.LatencyMS != nil {
		latency = fmt.Sprintf("%d", *t.LatencyMS)
	}
	return t.ToolCallID + "\x00" + t.ToolName + "\x00" + t.DefinitionDigest + "\x00" + t.Outcome + "\x00" + latency
}

func setLatency(tool *ToolUse, requested, resolved time.Time) {
	if requested.IsZero() || resolved.IsZero() {
		return
	}
	delta := resolved.Sub(requested)
	if delta < 0 {
		return
	}
	millis := delta.Milliseconds()
	tool.LatencyMS = &millis
}

func aggregateCosts(costs []CostFact, invocations []InvocationUse, reasons *[]string) (*int64, *int64) {
	known := make(map[int64]struct{}, len(invocations))
	for _, invocation := range invocations {
		known[invocation.InvocationID] = struct{}{}
	}
	actualTotal, estimatedTotal := int64(0), int64(0)
	actualSeen, estimatedSeen := false, false
	seen := make(map[int64]struct{}, len(costs))
	for _, cost := range costs {
		if _, ok := known[cost.InvocationID]; !ok {
			*reasons = append(*reasons, "cost_for_unreferenced_invocation")
			continue
		}
		if _, ok := seen[cost.InvocationID]; ok {
			*reasons = append(*reasons, "duplicate_cost_fact")
			continue
		}
		seen[cost.InvocationID] = struct{}{}
		if cost.ActualUSDNanos != nil {
			if *cost.ActualUSDNanos < 0 {
				*reasons = append(*reasons, "negative_actual_cost")
			} else {
				actualTotal += *cost.ActualUSDNanos
				actualSeen = true
			}
		}
		if cost.EstimatedUSDNanos != nil {
			if *cost.EstimatedUSDNanos < 0 {
				*reasons = append(*reasons, "negative_estimated_cost")
			} else {
				estimatedTotal += *cost.EstimatedUSDNanos
				estimatedSeen = true
			}
		}
	}
	var actual, estimated *int64
	if actualSeen {
		actual = &actualTotal
	}
	if estimatedSeen {
		estimated = &estimatedTotal
	}
	return actual, estimated
}

func bindingMode(invocations []InvocationUse) BindingMode {
	if len(invocations) < 2 {
		return BindingModeHomogeneous
	}
	provider, model := invocations[0].ProviderID, invocations[0].ProviderModelID
	for _, invocation := range invocations[1:] {
		if invocation.ProviderID != provider || invocation.ProviderModelID != model {
			return BindingModeMixed
		}
	}
	return BindingModeHomogeneous
}

func countTurns(events []EventFact) int {
	requests := countEvents(events, "model_request_prepared")
	responses := countEvents(events, "model_response_recorded")
	if requests > responses {
		return requests
	}
	return responses
}

func countToolCalls(events []EventFact) int {
	requested := countEvents(events, "tool_call_requested")
	recorded := countEvents(events, "tool_result_recorded") + countEvents(events, "tool_call_denied")
	if requested > recorded {
		return requested
	}
	return recorded
}

func countEvents(events []EventFact, eventType string) int {
	count := 0
	for _, event := range events {
		if event.Type == eventType {
			count++
		}
	}
	return count
}

func cloneVerification(input *VerificationSummary) *VerificationSummary {
	if input == nil {
		return nil
	}
	result := *input
	result.EvidenceRefs = append([]string(nil), input.EvidenceRefs...)
	sort.Strings(result.EvidenceRefs)
	result.Obligations = append([]ObligationObservation(nil), input.Obligations...)
	for index := range result.Obligations {
		result.Obligations[index].EvidenceRefs = append([]string(nil), result.Obligations[index].EvidenceRefs...)
		sort.Strings(result.Obligations[index].EvidenceRefs)
	}
	sort.SliceStable(result.Obligations, func(i, j int) bool {
		return obligationSortKey(result.Obligations[i]) < obligationSortKey(result.Obligations[j])
	})
	return &result
}

func obligationSortKey(o ObligationObservation) string {
	return o.Key + "\x00" + o.Kind + "\x00" + o.Label + "\x00" + o.VerifierRef + "\x00" + o.VerifierVersion + "\x00" + o.EvidenceDigest + "\x00" + strings.Join(o.EvidenceRefs, "\x00")
}

func digestProjectionFacts(d RunDescriptor, input ProjectionInput, events []EventFact) (string, error) {
	skills := append([]SkillFact(nil), input.Skills...)
	sort.SliceStable(skills, func(i, j int) bool {
		if skills[i].SkillID != skills[j].SkillID {
			return skills[i].SkillID < skills[j].SkillID
		}
		if skills[i].Version != skills[j].Version {
			return skills[i].Version < skills[j].Version
		}
		return skills[i].ContentHash < skills[j].ContentHash
	})
	invocations := append([]InvocationFact(nil), input.Invocations...)
	sort.SliceStable(invocations, func(i, j int) bool {
		return invocations[i].InvocationID < invocations[j].InvocationID
	})
	costs := append([]CostFact(nil), input.Costs...)
	sort.SliceStable(costs, func(i, j int) bool {
		return costs[i].InvocationID < costs[j].InvocationID
	})
	var verification *VerificationSummary
	if input.Verification != nil {
		verification = cloneVerification(input.Verification)
	}
	body, err := json.Marshal(struct {
		Descriptor   RunDescriptor        `json:"descriptor"`
		TaskClass    string               `json:"task_class"`
		Context      ContextUse           `json:"context"`
		Events       []EventFact          `json:"events"`
		Skills       []SkillFact          `json:"skills"`
		Invocations  []InvocationFact     `json:"invocations"`
		Costs        []CostFact           `json:"costs"`
		Verification *VerificationSummary `json:"verification,omitempty"`
	}{d, input.TaskClass, input.Context, events, skills, invocations, costs, verification})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value != "" {
			seen[value] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen))
	for value := range seen {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func cloneBool(value *bool) *bool {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}

func cloneInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}

func utcTime(value time.Time) time.Time {
	if value.IsZero() {
		return time.Time{}
	}
	return value.UTC()
}

func utcTimePtr(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	result := value.UTC()
	return &result
}
