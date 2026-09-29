package modelegress

import "testing"

// The campaign finance review runs on a task correlated to the CEO chat turn that asked for it.
// Routed to DeepSeek (2026-09-28), it must earn a scope, and only it may.
func TestFinanceReviewScopeBindsToTheFinanceReviewerAlone(t *testing.T) {
	if got := ExecutiveScopeMarker(FinanceReviewerRoleID, "department_worker", "ceochat:6", "task:2183"); got != ScopeFinanceReview {
		t.Fatalf("finance reviewer scope = %q, want %q", got, ScopeFinanceReview)
	}
	for name, marker := range map[string][4]string{
		"another role in a chat turn":   {"negocio/director_negocio", "department_worker", "ceochat:6", "task:1"},
		"the CEO in its own chat turn":  {"empresa/ceo", "department_worker", "ceochat:6", "task:1"},
		"the reviewer, other purpose":   {FinanceReviewerRoleID, "department_plan", "ceochat:6", "task:1"},
		"the reviewer, no task":         {FinanceReviewerRoleID, "department_worker", "ceochat:6", ""},
		"the reviewer, other correlate": {FinanceReviewerRoleID, "department_worker", "other:6", "task:1"},
	} {
		if got := ExecutiveScopeMarker(marker[0], marker[1], marker[2], marker[3]); got != "" {
			t.Errorf("%s earned scope %q", name, got)
		}
	}
	// Inside an executive correlation the reviewer is an ordinary department worker, as before.
	if got := ExecutiveScopeMarker(FinanceReviewerRoleID, "department_worker", "executive:abc", "task:1"); got != ScopeDepartmentWorker {
		t.Fatalf("executive correlation scope = %q, want the department worker scope", got)
	}
}

func TestDeepSeekAcceptsTheFinanceReviewScopeAndOnlyDeepSeek(t *testing.T) {
	if reason, ok := ValidateExecutiveScope("deepseek", "http_adapter", []string{"organizational"}, ScopeFinanceReview, false); !ok || reason != "executive_scope_verified_finance_review" {
		t.Fatalf("deepseek refused the finance review scope: %q", reason)
	}
	for _, provider := range []string{"xai", "openai_compatible", "alibaba_token_plan_via_claude_code"} {
		if _, ok := ValidateExecutiveScope(provider, "http_adapter", []string{"organizational"}, ScopeFinanceReview, false); ok {
			t.Errorf("%s accepted the finance review scope", provider)
		}
	}
}
