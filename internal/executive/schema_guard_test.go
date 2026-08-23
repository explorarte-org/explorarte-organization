package executive

import (
	"encoding/json"
	"testing"
)

// Under strict structured outputs a provider rejects any object schema whose
// properties are not all listed in required. The rejection happens before a
// model ever sees the request, so it does not look like a contract failure --
// it looks like the provider being broken, and it costs a whole campaign at
// the last phase.
//
// This is the seventh-copy guard for a mistake already made once: dropping
// three fields from required to stop demanding an echo, while leaving them in
// properties.
func TestEveryStrictSchemaRequiresEveryPropertyItDeclares(t *testing.T) {
	for name, raw := range map[string]json.RawMessage{
		"adversarial-review/v1":  AdversarialReviewOutputSchema(),
		"design-adjudication/v1": DesignAdjudicationOutputSchema(),
	} {
		t.Run(name, func(t *testing.T) {
			assertStrictObject(t, name, raw)
		})
	}
}

func assertStrictObject(t *testing.T, path string, raw json.RawMessage) {
	t.Helper()
	var node struct {
		Type                 string                     `json:"type"`
		AdditionalProperties *bool                      `json:"additionalProperties"`
		Required             []string                   `json:"required"`
		Properties           map[string]json.RawMessage `json:"properties"`
		Items                json.RawMessage            `json:"items"`
	}
	if err := json.Unmarshal(raw, &node); err != nil {
		t.Fatalf("%s: schema does not parse: %v", path, err)
	}
	if node.Type == "array" && len(node.Items) > 0 {
		assertStrictObject(t, path+"[]", node.Items)
		return
	}
	if node.Type != "object" {
		return
	}
	if node.AdditionalProperties == nil || *node.AdditionalProperties {
		t.Errorf("%s: a strict object schema must set additionalProperties:false", path)
	}
	required := make(map[string]bool, len(node.Required))
	for _, name := range node.Required {
		required[name] = true
	}
	for name := range node.Properties {
		if !required[name] {
			t.Errorf("%s: property %q is declared but not required; strict mode rejects the whole schema", path, name)
		}
	}
	for _, name := range node.Required {
		if _, declared := node.Properties[name]; !declared {
			t.Errorf("%s: %q is required but never declared as a property", path, name)
		}
	}
	for name, child := range node.Properties {
		assertStrictObject(t, path+"."+name, child)
	}
}

// The host owns the design identity, so the model must not be able to send it
// at all. A field the model cannot supply is a field it cannot get wrong.
func TestTheAdjudicationSchemaDoesNotAskTheModelForTheDesignIdentity(t *testing.T) {
	var node struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(DesignAdjudicationOutputSchema(), &node); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"design_id", "design_version", "design_digest"} {
		if _, present := node.Properties[name]; present {
			t.Errorf("%q must not be in the schema: the host binds it, and an echo the model can get wrong only loses well-formed verdicts", name)
		}
	}
}
