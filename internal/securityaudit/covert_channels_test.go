package securityaudit

import (
	"strings"
	"testing"
)

// TestCheckViolationsHasNoCriticalAgentMessagingViolations is the
// regression guard this branch (feat/security-agent-communication-
// hardening-v1) owns: CheckViolations() previously reported a "critical"
// messagingTopologyBypass violation on agent_messages_idempotency_collision
// (RoleScope: "bypassable_via_colliding_key") because Store.Send() reached
// the idempotency-key lookup before validating topology/principal/task
// ownership. That gap is now fixed (see
// internal/agentmessaging/postgres/store.go) and the catalog entry was
// updated to reflect it -- this test fails loudly if a NEW critical
// violation on any agent_messages* channel is ever introduced, in either
// the implementation or the catalog drifting out of sync with it again.
//
// Scoped to agent_messages* channels deliberately: CheckViolations() also
// reports a pre-existing critical finding on tasks_instructions (see
// TestCheckViolationsKnownPreExistingOutOfScopeCritical below) that belongs
// to a different subsystem this branch does not touch. A blanket "zero
// critical violations anywhere" assertion would either mask that finding
// (if weakened) or block this branch on a defect it didn't introduce and
// isn't scoped to fix (if left strict) -- neither is honest. Scoping by
// channel prefix keeps this test strict about what this branch owns while
// the finding below keeps the other one visible instead of hidden.
func TestCheckViolationsHasNoCriticalAgentMessagingViolations(t *testing.T) {
	violations := CheckViolations()
	for _, v := range violations {
		if v.Severity == "critical" && strings.HasPrefix(v.ChannelName, "agent_messages") {
			t.Fatalf("unexpected critical violation: channel=%s rule=%s desc=%q", v.ChannelName, v.RuleName, v.Description)
		}
	}
}

// TestCheckViolationsKnownPreExistingOutOfScopeCritical documents, by name,
// the ONE critical finding CheckViolations() currently reports outside
// agent_messages* channels: tasks_instructions/secretSmugglingViaPayload
// (secret/clinical data smuggleable via message/instructions payload). It
// predates this branch, belongs to the tasks subsystem, and was not fixed
// here -- this test does not assert it is fixed, it asserts it is exactly
// this ONE known finding, so a second, unreviewed critical violation
// appearing anywhere else fails the build instead of blending into "well,
// there's already a critical one, one more is fine."
func TestNoUnreviewedCriticalViolationsRemain(t *testing.T) {
	// This replaces TestCheckViolationsKnownPreExistingOutOfScopeCritical,
	// which documented tasks_instructions/secretSmugglingViaPayload as the one
	// known critical finding outside agent_messages*. That finding is now
	// closed: credential material is refused at ingress by
	// ValidateCreateRequest, proved behaviorally by
	// TestCreateRequestRejectsSecretsInEveryAgentVisibleField in
	// internal/tasks, with the opposite direction pinned by
	// TestCreateRequestCarriesSensitiveButNonSecretData so the filter cannot
	// quietly become a censor of clinical or personal data.
	//
	// The assertion is deliberately strict now. A new critical violation
	// anywhere must fail the build rather than blend into a documented
	// backlog, which is how the previous one survived as long as it did.
	var critical []Violation
	for _, violation := range CheckViolations() {
		if violation.Severity == "critical" {
			critical = append(critical, violation)
		}
	}
	if len(critical) != 0 {
		t.Fatalf("unreviewed critical violations: %+v", critical)
	}
}

func TestCheckViolationsKnownMediumSeverityBaseline(t *testing.T) {
	violations := CheckViolations()
	got := make(map[string]int)
	for _, v := range violations {
		if v.Severity != "medium" {
			continue
		}
		got[v.ChannelName+"/"+v.RuleName]++
	}
	want := map[string]int{
		// tasks_instructions/unboundedDurableSurface and
		// tasks_instructions/tasksInstructionsUnbounded were removed from this
		// baseline when the channel started declaring its real 64 KiB bound.
		// The bound is not a declaration: TestCreateRequestKeepsItsSizeBound in
		// internal/tasks proves 65536 bytes pass and 65537 are rejected.
		"task_results/unboundedDurableSurface":                 1,
		"task_evidence/unboundedDurableSurface":                1,
		"organizational_memory_get/unboundedDurableSurface":    1,
		"organizational_memory_list/unboundedDurableSurface":   1,
		"organizational_memory_search/unboundedDurableSurface": 1,
		"context_snapshots/unboundedDurableSurface":            1,
		"decision_graph/unboundedDurableSurface":               1,
		"staging_artifacts/unboundedDurableSurface":            1,
		"model_invocation_result/unboundedDurableSurface":      1,
	}
	if len(got) != len(want) {
		t.Fatalf("medium violation set changed: got %d distinct channel/rule pairs, want %d\ngot=%+v", len(got), len(want), got)
	}
	for key, count := range want {
		if got[key] != count {
			t.Fatalf("violation %q: got %d occurrences, want %d (full set: %+v)", key, got[key], count, got)
		}
	}
}

func TestFormatSize(t *testing.T) {
	cases := []struct {
		bytes int
		want  string
	}{
		{0, "unbounded"},
		{1, "1B"},
		{1023, "1023B"},
		{1024, "1KB"},
		{2048, "2KB"},
		{1048576, "1MB"},
		{5242880, "5MB"},
	}
	for _, tc := range cases {
		if got := formatSize(tc.bytes); got != tc.want {
			t.Errorf("formatSize(%d) = %q, want %q", tc.bytes, got, tc.want)
		}
	}
}

func TestFormatBool(t *testing.T) {
	if formatBool(true) != "true" {
		t.Fatal("formatBool(true) should be \"true\"")
	}
	if formatBool(false) != "false" {
		t.Fatal("formatBool(false) should be \"false\"")
	}
}

// TestRuleMessagingTopologyBypassDetectsForgedOrBypassRoleScope proves the
// detection rule's own logic actually fires on the exact pattern that was
// live in this catalog until this branch fixed it -- not just that
// CheckViolations() runs without panicking.
func TestRuleMessagingTopologyBypassDetectsForgedOrBypassRoleScope(t *testing.T) {
	var rule Rule
	for _, r := range Rules() {
		if r.Name == "messagingTopologyBypass" {
			rule = r
			break
		}
	}
	if rule.Name == "" {
		t.Fatal("messagingTopologyBypass rule not found")
	}

	bypassChannel := Channel{Name: "agent_messages_example", RoleScope: "bypassable_via_something"}
	if !rule.Check(bypassChannel) {
		t.Fatal("expected rule to flag a RoleScope containing 'bypass'")
	}
	forgedChannel := Channel{Name: "agent_messages_example", RoleScope: "forged_role_accepted"}
	if !rule.Check(forgedChannel) {
		t.Fatal("expected rule to flag a RoleScope containing 'forged'")
	}
	cleanChannel := Channel{Name: "agent_messages_example", RoleScope: "sender_role matches dispatch_actor + recipient_role in topology edge"}
	if rule.Check(cleanChannel) {
		t.Fatal("expected rule NOT to flag a clean RoleScope")
	}
	nonMessagingChannel := Channel{Name: "task_results", RoleScope: "bypassable_via_something"}
	if rule.Check(nonMessagingChannel) {
		t.Fatal("expected rule to only apply to agent_messages* channels")
	}
}

// TestCatalogAndRulesAreNonEmpty guards against an accidental empty
// catalog or rule set silently making CheckViolations() report a clean
// bill of health for the wrong reason (nothing to check, rather than
// nothing wrong).
func TestCatalogAndRulesAreNonEmpty(t *testing.T) {
	if len(Catalog()) == 0 {
		t.Fatal("Catalog() must not be empty")
	}
	if len(Rules()) == 0 {
		t.Fatal("Rules() must not be empty")
	}
}
