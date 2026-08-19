package modelegress

import "strings"

const (
	ScopeExecutiveCEO      = "scope.executive.ceo"
	ScopeDepartmentLeader  = "scope.executive.department_leader"
	ScopeDepartmentWorker  = "scope.executive.department_worker"
	ScopeAdversarialReview = "scope.executive.adversarial_review"
)

// AdversarialReviewerRoleID is the only role whose durable context may derive
// ScopeAdversarialReview. Naming the role here mirrors how ScopeExecutiveCEO
// is already pinned to empresa/ceo: the scope is a statement about WHO ran,
// and a scope that any role could earn would not be one.
const AdversarialReviewerRoleID = "investigacion/revisor_adversarial"

// ExecutiveScopeMarker derives an internal egress scope exclusively from
// durable Context Engine metadata. It is never rendered into model context,
// never stored as a data classification, and never accepted from model output.
func ExecutiveScopeMarker(actorRoleID, purpose, correlationID, taskRef string) string {
	actorRoleID = strings.TrimSpace(actorRoleID)
	purpose = strings.TrimSpace(purpose)
	correlationID = strings.TrimSpace(correlationID)
	taskRef = strings.TrimSpace(taskRef)
	if !strings.HasPrefix(correlationID, "executive:") || !strings.HasPrefix(taskRef, "task:") {
		return ""
	}
	switch purpose {
	case "executive_ceo_plan", "executive_ceo_closure":
		if actorRoleID == "empresa/ceo" {
			return ScopeExecutiveCEO
		}
	case "department_plan", "department_review":
		if scopedDepartmentRole(actorRoleID) {
			return ScopeDepartmentLeader
		}
	case "department_worker":
		if scopedDepartmentRole(actorRoleID) {
			return ScopeDepartmentWorker
		}
	case "adversarial_review":
		if actorRoleID == AdversarialReviewerRoleID {
			return ScopeAdversarialReview
		}
	}
	return ""
}

func scopedDepartmentRole(roleID string) bool {
	if roleID == "" || roleID == "empresa/human" || roleID == "empresa/ceo" || roleID == "empresa/ceo_observer" {
		return false
	}
	parts := strings.Split(roleID, "/")
	return len(parts) == 2 && parts[0] != "" && parts[1] != ""
}

func scopeRequired(provider string, dataClasses []string) bool {
	switch provider {
	case "alibaba_token_plan_via_claude_code", "deepseek", "xai":
		// xAI always requires a durable scope, for every classification
		// including public. Making the requirement unconditional is what
		// stops a new provider from being reachable by default the moment
		// a policy allow exists for it.
		return true
	case "openai_compatible":
		for _, class := range dataClasses {
			if class == string(ClassificationOrganizational) {
				return true
			}
		}
	}
	return false
}

func scopeAllows(provider, transport, scope string, singleProviderTest bool) bool {
	switch provider {
	case "alibaba_token_plan_via_claude_code":
		// Retired provider: even a stale policy allow plus a valid historical
		// CEO marker must remain fail-closed.
		return false
	case "openai_compatible":
		if transport != "http_adapter" {
			return false
		}
		if singleProviderTest {
			return scope == ScopeExecutiveCEO || scope == ScopeDepartmentLeader || scope == ScopeDepartmentWorker
		}
		return scope == ScopeExecutiveCEO || scope == ScopeDepartmentLeader
	case "deepseek":
		// Canonical routing assigns department.leader to DeepSeek Pro and
		// department.worker to DeepSeek Flash. Both remain constrained to a
		// durable executive department scope; CEO scope is deliberately not
		// accepted here.
		return transport == "http_adapter" &&
			(scope == ScopeDepartmentLeader || scope == ScopeDepartmentWorker)
	case "xai":
		// The adversarial reviewer's scope and nothing else. Executive and
		// department scopes are refused here, and singleProviderTest does not
		// widen this the way it widens openai_compatible: a test mode that
		// could route ordinary department work to the reviewer's provider
		// would defeat the independence the reviewer exists to provide.
		return transport == "http_adapter" && scope == ScopeAdversarialReview
	default:
		return false
	}
}

func scopeVerifiedReason(scope string) string {
	switch scope {
	case ScopeExecutiveCEO:
		return "executive_scope_verified_ceo"
	case ScopeDepartmentLeader:
		return "executive_scope_verified_department_leader"
	case ScopeDepartmentWorker:
		return "executive_scope_verified_department_worker"
	case ScopeAdversarialReview:
		return "executive_scope_verified_adversarial_review"
	default:
		return ""
	}
}

// ValidateExecutiveScope is the R24 backend gate. It is evaluated after model
// routing has resolved provider/transport and after Context Engine has resolved
// data classes. A provider/classification combination that requires executive
// scope is rejected unless the durable context-derived marker matches exactly.
func ValidateExecutiveScope(provider, transport string, dataClasses []string, scope string, singleProviderTest bool) (string, bool) {
	classes, _ := NormalizeClassifications(dataClasses)
	if !scopeRequired(strings.TrimSpace(provider), classes) {
		return "executive_scope_not_required", true
	}
	scope = strings.TrimSpace(scope)
	if !scopeAllows(strings.TrimSpace(provider), strings.TrimSpace(transport), scope, singleProviderTest) {
		return "executive_scope_required", false
	}
	reason := scopeVerifiedReason(scope)
	if reason == "" {
		return "executive_scope_invalid", false
	}
	return reason, true
}
