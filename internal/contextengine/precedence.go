package contextengine

import "fmt"

type PrecedenceDefinition struct {
	AuthorityTiers []PrecedenceAuthorityTier `yaml:"authority_tiers"`
	AssemblyOrder  []string                  `yaml:"prompt_assembly_order"`
}

type PrecedenceAuthorityTier struct {
	Priority             int      `yaml:"priority"`
	ID                   string   `yaml:"id"`
	Content              []string `yaml:"content"`
	MayGrantCapabilities bool     `yaml:"may_grant_capabilities"`
	TreatedAsUntrusted   bool     `yaml:"treated_as_untrusted_data"`
}

func ValidatePrecedence(definition PrecedenceDefinition) error {
	expectedTiers := []struct {
		priority int
		id       string
		mayGrant bool
	}{
		{0, "immutable_safety", false},
		{1, "owner_decisions", true},
		{2, "canonical_registry_and_policies", true},
		{3, "organization_and_department_agents", false},
		{4, "role_and_approved_skills", false},
		{5, "project_and_task", false},
		{6, "memory_and_rag", false},
	}
	if len(definition.AuthorityTiers) != len(expectedTiers) {
		return Reject(ReasonPrecedenceInvalid, "instruction-precedence.yaml", "authority tier count differs from canonical contract")
	}
	for index, expected := range expectedTiers {
		actual := definition.AuthorityTiers[index]
		if actual.Priority != expected.priority || actual.ID != expected.id || actual.MayGrantCapabilities != expected.mayGrant {
			return Reject(ReasonPrecedenceInvalid, actual.ID, fmt.Sprintf("authority tier at index %d differs from canonical contract", index))
		}
		if actual.Priority == 6 && !actual.TreatedAsUntrusted {
			return Reject(ReasonPrecedenceInvalid, actual.ID, "memory_and_rag must be treated as untrusted data")
		}
	}
	expectedOrder := []string{
		"immutable_safety", "owner_decisions", "canonical_registry_and_policies",
		"organization_agent", "department_agent", "role_profile", "approved_memory",
		"matched_approved_skills", "project_context", "task_context", "rag_evidence",
	}
	if len(definition.AssemblyOrder) != len(expectedOrder) {
		return Reject(ReasonPrecedenceInvalid, "prompt_assembly_order", "render order length differs from canonical contract")
	}
	for index := range expectedOrder {
		if definition.AssemblyOrder[index] != expectedOrder[index] {
			return Reject(ReasonPrecedenceInvalid, definition.AssemblyOrder[index], fmt.Sprintf("render order differs at position %d", index+1))
		}
	}
	return nil
}
