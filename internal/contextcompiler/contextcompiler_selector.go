package contextcompiler

import "fmt"

// SelectorAlgorithmVersion identifies the frozen M1.3 selection algorithm
// implemented by SelectorRegistry.Select -- durable provenance, persisted
// on ExecutionContextView, answering "which algorithm chose this profile"
// after restart. A future change to precedence/matching semantics must
// become a new version, never a silent behavior change under this same
// identity.
const SelectorAlgorithmVersion = "semantic_context_selector/v1"

// SelectionKind is the frozen, durable provenance of HOW a profile was
// selected (M1.3 section 14) -- never fabricated, never inferred after
// the fact.
type SelectionKind string

const (
	SelectionExact            SelectionKind = "exact"
	SelectionTaskClass        SelectionKind = "task_class"
	SelectionExecutionPurpose SelectionKind = "execution_purpose"
	SelectionCanonical        SelectionKind = "canonical"
)

// SemanticSelector is the durable, host-validated selector identity M1.3
// resolves ContextProfiles from. Every field is classification metadata
// only -- none of them, alone or combined, grant authority, capabilities,
// tools, or model/memory access (M1.3 section 17). Built ONLY from
// already-durable contextengine.Snapshot facts -- see BuildSelector.
type SemanticSelector struct {
	TaskClass        string
	ExecutionPurpose string
	ActorRoleID      string
	ActorUnitID      string
}

// ProfileEntry is one registered ContextProfile plus the applicability
// restriction that gates whether a TASK-CLASS/EXECUTION-PURPOSE tier
// match is allowed to apply for a given actor (M1.3 section 11:
// ActorRoleID/ActorUnitID are applicability axes, never substitutes for
// TaskClass).
type ProfileEntry struct {
	Profile ContextProfile
	// ApplicableActorRoleIDs / ApplicableActorUnitIDs: when non-empty,
	// the selector's corresponding value must be present in the list for
	// a TASK-CLASS/EXECUTION-PURPOSE tier match to apply. An empty list
	// means "unrestricted on that axis". Both axes are independently
	// AND-ed -- a profile restricted on both role and unit requires both
	// to match.
	ApplicableActorRoleIDs []string
	ApplicableActorUnitIDs []string
}

func (e ProfileEntry) applicable(selector SemanticSelector) bool {
	if len(e.ApplicableActorRoleIDs) > 0 && !selectorListContains(e.ApplicableActorRoleIDs, selector.ActorRoleID) {
		return false
	}
	if len(e.ApplicableActorUnitIDs) > 0 && !selectorListContains(e.ApplicableActorUnitIDs, selector.ActorUnitID) {
		return false
	}
	return true
}

func selectorListContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

// ExactRegistration additionally registers a ProfileEntry for the EXACT
// selector-identity tier (the full four-axis tuple, M1.3 section 10) --
// only meaningful when Selector is fully populated; a zero-value
// SemanticSelector is never registered as an exact match.
type ExactRegistration struct {
	Selector SemanticSelector
	Entry    ProfileEntry
}

// SelectorRegistry is the deterministic, precedence-ordered profile
// resolver M1.3 replaces `Registry()[TaskClassOf(actorRoleID)]` with.
// Lookup NEVER depends on map iteration order, embeddings, free text, or
// an LLM -- see Select.
type SelectorRegistry struct {
	exact              map[SemanticSelector]ProfileEntry
	byTaskClass        map[string]ProfileEntry
	byExecutionPurpose map[string]ProfileEntry
}

// BuildSelectorRegistry builds a SelectorRegistry from explicit
// registrations, rejecting any duplicate/ambiguous registration
// (M1.3 section 10: "duplicate/ambiguous selector registrations must
// fail tests / initialization, not depend on map iteration order") rather
// than silently letting a later entry overwrite an earlier one.
func BuildSelectorRegistry(byTaskClass []ProfileEntry, byExecutionPurpose []ProfileEntry, exact []ExactRegistration) (*SelectorRegistry, error) {
	registry := &SelectorRegistry{
		exact:              make(map[SemanticSelector]ProfileEntry, len(exact)),
		byTaskClass:        make(map[string]ProfileEntry, len(byTaskClass)),
		byExecutionPurpose: make(map[string]ProfileEntry, len(byExecutionPurpose)),
	}
	for _, entry := range byTaskClass {
		if entry.Profile.TaskClass == "" {
			return nil, fmt.Errorf("contextcompiler: task-class profile registration %q has no TaskClass", entry.Profile.ID)
		}
		if _, duplicate := registry.byTaskClass[entry.Profile.TaskClass]; duplicate {
			return nil, fmt.Errorf("contextcompiler: duplicate task-class selector registration for %q", entry.Profile.TaskClass)
		}
		registry.byTaskClass[entry.Profile.TaskClass] = entry
	}
	for _, entry := range byExecutionPurpose {
		if entry.Profile.ExecutionPurpose == "" {
			return nil, fmt.Errorf("contextcompiler: execution-purpose profile registration %q has no ExecutionPurpose", entry.Profile.ID)
		}
		if _, duplicate := registry.byExecutionPurpose[entry.Profile.ExecutionPurpose]; duplicate {
			return nil, fmt.Errorf("contextcompiler: duplicate execution-purpose selector registration for %q", entry.Profile.ExecutionPurpose)
		}
		registry.byExecutionPurpose[entry.Profile.ExecutionPurpose] = entry
	}
	for _, registration := range exact {
		if registration.Selector == (SemanticSelector{}) {
			return nil, fmt.Errorf("contextcompiler: exact selector registration %q has an empty selector identity", registration.Entry.Profile.ID)
		}
		if _, duplicate := registry.exact[registration.Selector]; duplicate {
			return nil, fmt.Errorf("contextcompiler: duplicate exact selector registration for %+v", registration.Selector)
		}
		registry.exact[registration.Selector] = registration.Entry
	}
	return registry, nil
}

// MustBuildSelectorRegistry panics on a registry construction error. Only
// safe to call at package initialization with a registration set already
// proven correct by tests -- never on caller-influenced input.
func MustBuildSelectorRegistry(byTaskClass []ProfileEntry, byExecutionPurpose []ProfileEntry, exact []ExactRegistration) *SelectorRegistry {
	registry, err := BuildSelectorRegistry(byTaskClass, byExecutionPurpose, exact)
	if err != nil {
		panic(err)
	}
	return registry
}

// SelectionResult is the outcome of resolving a SemanticSelector: either a
// matched ContextProfile with its provenance Kind, or Matched=false
// (canonical fallback -- Kind is still SelectionCanonical for durable
// provenance, never left blank).
type SelectionResult struct {
	Profile ContextProfile
	Kind    SelectionKind
	Matched bool
}

// Select resolves selector against the frozen M1.3 precedence (section
// 10): EXACT, then TASK-CLASS (gated by applicability), then
// EXECUTION-PURPOSE (gated by applicability), then CANONICAL fallback.
// No fuzzy matching, no embeddings, no free-text inspection -- pure
// deterministic map lookups in a fixed order.
func (r *SelectorRegistry) Select(selector SemanticSelector) SelectionResult {
	if r == nil {
		return SelectionResult{Kind: SelectionCanonical}
	}
	if entry, ok := r.exact[selector]; ok {
		return SelectionResult{Profile: entry.Profile, Kind: SelectionExact, Matched: true}
	}
	if selector.TaskClass != "" {
		if entry, ok := r.byTaskClass[selector.TaskClass]; ok && entry.applicable(selector) {
			return SelectionResult{Profile: entry.Profile, Kind: SelectionTaskClass, Matched: true}
		}
	}
	if selector.ExecutionPurpose != "" {
		if entry, ok := r.byExecutionPurpose[selector.ExecutionPurpose]; ok && entry.applicable(selector) {
			return SelectionResult{Profile: entry.Profile, Kind: SelectionExecutionPurpose, Matched: true}
		}
	}
	return SelectionResult{Kind: SelectionCanonical}
}
