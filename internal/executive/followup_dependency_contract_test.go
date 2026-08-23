package executive

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// The host refuses a follow-up task whose dependency is not a client_key
// declared in the same response. That rule lives in validator.go, and the only
// surface a provider can read is the output schema -- so if the schema does
// not state the rule, the rule is unknowable to the party expected to obey it.
//
// It was unstated, and AUTONOMY-SMOKE-013 is what that cost: a department
// review asked for a follow-up task and expressed its relationship to an
// already-completed task the way the system itself names tasks in the prompt,
// as "task:106594". The schema declared dependencies as a bare array of
// strings with no description, and the prompt never used the words
// "dependencies" or "client_key" at all -- so the only surface carrying the
// rule was the one that said nothing about it. The host refused the plan on
// every attempt and the campaign stopped.
//
// Two details are worth stating precisely, because an earlier version of this
// comment got them wrong and the decoded record corrected it. The attempts
// were NOT identical: the model returned different plans each time (two, two
// and one follow-up task), and it did receive the previous refusal in its
// attempt history. So retrying was not futile in principle -- it was futile
// in practice, because the refusal said "missing dependency task:106594" about
// a task the model had just watched complete. And "evidence_refs" DOES appear
// in the prompt, seven times, but only as data: once in canonical memory
// documentation and otherwise inside the previous round's recorded evidence.
// Seeing a field name used is not being told what it means.
//
// This test is the comparison nobody was making between the two sides.
func TestTheDependencyRuleIsStatedWhereTheModelCanReadIt(t *testing.T) {
	var schema map[string]any
	if err := json.Unmarshal([]byte(taskOutputSchemaJSON), &schema); err != nil {
		t.Fatalf("the task schema must be valid JSON: %v", err)
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("task schema has no properties")
	}
	dependencies, ok := properties["dependencies"].(map[string]any)
	if !ok {
		t.Fatal("task schema declares no dependencies field")
	}
	description, _ := dependencies["description"].(string)
	if strings.TrimSpace(description) == "" {
		t.Fatal("dependencies is enforced against a rule the schema never states")
	}
	// The rule has two halves, and the incident needed both: what IS legal,
	// and what looks legal but is not.
	if !strings.Contains(description, "client_key") {
		t.Fatalf("the description must name what a dependency may contain, got %q", description)
	}
	if !strings.Contains(description, "task:") {
		t.Fatalf("the description must warn against the form the host rejects, got %q", description)
	}
	if clientKey, ok := properties["client_key"].(map[string]any); ok {
		if description, _ := clientKey["description"].(string); strings.TrimSpace(description) == "" {
			t.Fatal("dependencies point at client_key, so client_key must say what it is")
		}
	}
}

// The other half of the seam: the host really does refuse what the schema now
// warns about, and accepts what it permits. A description documenting a rule
// nobody enforced would be as broken as an enforced rule nobody documented.
//
// This calls the host's own rule rather than restating it. The first draft of
// this test reimplemented the check and passed against itself, which would
// have proved nothing about the code that actually runs.
func TestTheHostEnforcesTheDependencyRuleItNowStates(t *testing.T) {
	// Exactly the shape AUTONOMY-SMOKE-013 produced.
	incident := []WorkerTaskProposal{{
		ClientKey:    "task.106612.followup.qa.verify.001",
		Dependencies: []string{"task:106594"},
	}}
	err := ValidatePlanDependencies(incident)
	if !errors.Is(err, ErrContractRejected) {
		t.Fatalf("a dependency naming an already-existing task must be refused, got %v", err)
	}
	// The refusal is read by the model on its next attempt: a failed
	// attempt's summary travels in the task's own history, which is how the
	// provider learns what happened. The original message said "missing
	// dependency task:106594" about a task that had just completed in front
	// of it -- false, and with nowhere to go. A retry can only do better if
	// the refusal says what the rule is and what would satisfy it.
	for _, needed := range []string{"client_key", "evidence_refs", "task.106612.followup.qa.verify.001"} {
		if !strings.Contains(err.Error(), needed) {
			t.Fatalf("the refusal must mention %q so a retry has somewhere to go, got %q", needed, err.Error())
		}
	}
	if strings.Contains(err.Error(), "missing dependency") {
		t.Fatalf("the refusal must not claim an existing task is missing, got %q", err.Error())
	}

	// The legal form the schema now describes.
	legal := []WorkerTaskProposal{
		{ClientKey: "draft"},
		{ClientKey: "verify", Dependencies: []string{"draft"}},
	}
	if err := ValidatePlanDependencies(legal); err != nil {
		t.Fatalf("a dependency naming a client_key in the same plan must be accepted, got %v", err)
	}

	// An empty dependency list is how a task says it can start immediately,
	// which is what the schema tells the model to send.
	if err := ValidatePlanDependencies([]WorkerTaskProposal{{ClientKey: "solo"}}); err != nil {
		t.Fatalf("a task with no dependencies must be accepted, got %v", err)
	}
}
