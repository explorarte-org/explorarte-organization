package skillforge

import (
	"fmt"
	"strings"
)

var mandatorySections = []string{
	"purpose",
	"applicability",
	"non-goals",
	"inputs",
	"outputs",
	"procedure",
	"stop conditions",
	"failure modes",
	"evidence requirements",
}

var forbiddenStatements = []string{
	"grant yourself",
	"activate automatically",
	"ignore owner",
	"modify routing",
	"read secrets",
	"delegate across departments",
}

type ContractValidator struct{}

func NewContractValidator() *ContractValidator {
	return &ContractValidator{}
}

func (v *ContractValidator) ValidateContract(content []byte) error {
	text := strings.ToLower(string(content))

	// 1. Check for forbidden statements
	for _, forbidden := range forbiddenStatements {
		if strings.Contains(text, forbidden) {
			return fmt.Errorf("%w: contains forbidden statement %q", ErrContractViolation, forbidden)
		}
	}

	// 2. Check for mandatory sections (as markdown headings or sections)
	for _, section := range mandatorySections {
		// Look for either "# Section" or "## Section" or "### Section" or "**Section**"
		h1 := "# " + section
		h2 := "## " + section
		h3 := "### " + section
		bold := "**" + section + "**"
		if !strings.Contains(text, h1) && !strings.Contains(text, h2) && !strings.Contains(text, h3) && !strings.Contains(text, bold) {
			return fmt.Errorf("%w: missing required section %q", ErrContractViolation, section)
		}
	}

	return nil
}
