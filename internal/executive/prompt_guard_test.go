package executive

import (
	"encoding/json"
	"strings"
	"testing"
)

// A prompt may not ask for a field the schema has no place for.
//
// Removing design_id, design_version and design_digest from the output schema
// while leaving "Echo design_id, design_version and design_digest exactly as
// supplied" in the instructions is the shape of this bug: the request is valid,
// the model obeys, and it writes the identity into whichever array will take a
// string. A campaign died that way with the identity as prose inside a
// findings array.
//
// The two halves live in different files, which is why they came apart, and
// why the guard has to compare them rather than inspect either alone.
func TestTheAdjudicationPromptAsksOnlyForFieldsTheSchemaDeclares(t *testing.T) {
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(DesignAdjudicationOutputSchema(), &schema); err != nil {
		t.Fatal(err)
	}
	for _, absent := range []string{"design_id", "design_version", "design_digest"} {
		if _, declared := schema.Properties[absent]; declared {
			continue // the schema owns it again; the prompt may name it
		}
		if strings.Contains(designAdjudicationPreamble, absent) {
			t.Errorf("the preamble names %q, which the schema does not declare; the model will return it somewhere it does not belong", absent)
		}
	}
	if strings.Contains(strings.ToLower(designAdjudicationPreamble), "echo") {
		t.Error("the preamble must not ask the adjudicator to echo anything: the host binds the identity, and an echo it can get wrong only loses well-formed verdicts")
	}
}
