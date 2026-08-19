package executive

import (
	"encoding/json"
	"fmt"
	"strings"
)

// implementation-plan/v1 is what a department leader produces AFTER a design
// freeze and BEFORE any mission exists. It is deliberately a contract about
// CONTENT, not authority: the plan says what should change and supplies the
// patch, and the host decides which paths are permitted, which gates must
// pass and which commit the work is based on.
//
// Nothing here lets the model name a provider, a budget, a credential, an
// allowed path, a required gate or a promotion approval. The shared decoder
// rejects unknown fields outright, and the forbidden-key scan rejects the
// authority-shaped ones by name wherever they appear, however deeply nested.

const maxPlannedChanges = 24

type PlannedChange struct {
	Path   string `json:"path"`
	Intent string `json:"intent"`
	Patch  string `json:"patch"`
}

type ImplementationPlan struct {
	SchemaVersion            string          `json:"schema_version"`
	Objective                string          `json:"objective"`
	Changes                  []PlannedChange `json:"changes"`
	VerificationExpectations []string        `json:"verification_expectations"`
	DependencyOrder          []string        `json:"dependency_order"`
	EvidenceRefs             []string        `json:"evidence_refs"`
}

func ParseImplementationPlan(body []byte, limits Limits) (ImplementationPlan, error) {
	var out ImplementationPlan
	if err := decodeStrictModelJSON(body, &out, limits); err != nil {
		return ImplementationPlan{}, err
	}
	if out.SchemaVersion != ImplementationPlanSchemaVersion {
		return ImplementationPlan{}, fmt.Errorf("%w: schema_version", ErrContractRejected)
	}
	if err := validateRequiredString(out.Objective, limits.MaxStringBytes, "objective"); err != nil {
		return ImplementationPlan{}, err
	}
	if len(out.Changes) == 0 || len(out.Changes) > maxPlannedChanges {
		return ImplementationPlan{}, ErrPlanTooLarge
	}
	for name, values := range map[string][]string{
		"verification_expectations": out.VerificationExpectations,
		"dependency_order":          out.DependencyOrder,
		"evidence_refs":             out.EvidenceRefs,
	} {
		if err := validateStrings(values, limits, name); err != nil {
			return ImplementationPlan{}, err
		}
	}
	if len(out.VerificationExpectations) == 0 {
		return ImplementationPlan{}, fmt.Errorf("%w: verification_expectations required", ErrContractRejected)
	}
	seen := make(map[string]struct{}, len(out.Changes))
	for i, change := range out.Changes {
		if err := validateRequiredString(change.Path, 400, "change.path"); err != nil {
			return ImplementationPlan{}, fmt.Errorf("change[%d]: %w", i, err)
		}
		if err := validateRequiredString(change.Intent, limits.MaxStringBytes, "change.intent"); err != nil {
			return ImplementationPlan{}, fmt.Errorf("change[%d]: %w", i, err)
		}
		// The patch is content, and content is allowed to be large; it is
		// bounded here only so one plan cannot exhaust the mission.
		if strings.TrimSpace(change.Patch) == "" || len(change.Patch) > limits.MaxInputBytes {
			return ImplementationPlan{}, fmt.Errorf("%w: change[%d] patch is empty or oversized", ErrContractRejected, i)
		}
		if _, duplicate := seen[change.Path]; duplicate {
			return ImplementationPlan{}, fmt.Errorf("%w: change[%d] repeats path %q", ErrContractRejected, i, change.Path)
		}
		seen[change.Path] = struct{}{}
	}
	return out, nil
}

var implementationPlanOutputSchema = json.RawMessage(`{
  "type":"object",
  "additionalProperties":false,
  "required":[
    "schema_version",
    "objective",
    "changes",
    "verification_expectations",
    "dependency_order",
    "evidence_refs"
  ],
  "properties":{
    "schema_version":{
      "type":"string",
      "enum":["implementation-plan/v1"]
    },
    "objective":{"type":"string"},
    "changes":{
      "type":"array",
      "items":{
        "type":"object",
        "additionalProperties":false,
        "required":["path","intent","patch"],
        "properties":{
          "path":{"type":"string","description":"Repository-relative path. The host decides whether it is permitted; naming it here is a request, not a grant."},
          "intent":{"type":"string","description":"What this change accomplishes and why."},
          "patch":{"type":"string","description":"Unified diff to apply to that path."}
        }
      }
    },
    "verification_expectations":{"type":"array","items":{"type":"string"}},
    "dependency_order":{"type":"array","items":{"type":"string"}},
    "evidence_refs":{"type":"array","items":{"type":"string"}}
  }
}`)

func ImplementationPlanOutputSchema() json.RawMessage {
	return append(json.RawMessage(nil), implementationPlanOutputSchema...)
}
