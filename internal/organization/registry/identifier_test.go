package registry

import "testing"

func TestValidateIdentifiers(t *testing.T) {
	validRoles := []string{"ingenieria_ia/code-runner", "empresa/ceo_observer", "investigacion/research_worker_hourly"}
	for _, value := range validRoles {
		if err := ValidateRoleID(value); err != nil {
			t.Errorf("ValidateRoleID(%q): %v", value, err)
		}
	}
	invalid := []string{"Empresa/ceo", "empresa/ceo observer", "empresa//ceo", "empresa/../ceo", "empresa/ceo/extra", "empresa/"}
	for _, value := range invalid {
		if err := ValidateRoleID(value); err == nil {
			t.Errorf("ValidateRoleID(%q) = nil", value)
		}
	}
	for _, value := range []string{"ingenieria_ia", "recursos-agenticos"} {
		if err := ValidateUnitID(value); err != nil {
			t.Errorf("ValidateUnitID(%q): %v", value, err)
		}
	}
}

func TestValidateReferencePath(t *testing.T) {
	for _, value := range []string{"ingenieria_ia/AGENT.md", "empresa/ceo/memoria/MEMORY.md"} {
		if err := ValidateReferencePath(value); err != nil {
			t.Errorf("valid path %q: %v", value, err)
		}
	}
	for _, value := range []string{"/etc/passwd", "../secret", "a/../../b", `a\\b`, "."} {
		if err := ValidateReferencePath(value); err == nil {
			t.Errorf("invalid path %q accepted", value)
		}
	}
}
