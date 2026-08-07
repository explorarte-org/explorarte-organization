package modelegress

import "strings"

const (
	ScopeExecutiveCEO      = "scope.executive.ceo"
	ScopeDepartmentLeader = "scope.executive.department_leader"
	ScopeDepartmentWorker = "scope.executive.department_worker"
)

// ExecutiveScopeMarker derives an internal egress scope marker exclusively
// from durable Context Engine metadata. It is never rendered into model
// context and never accepted from model output.
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

func splitScopeClassifications(values []string) (data []string, scopes []string, invalidScope bool) {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if strings.HasPrefix(value, "scope.") {
			switch value {
			case ScopeExecutiveCEO, ScopeDepartmentLeader, ScopeDepartmentWorker:
				scopes = append(scopes, value)
			default:
				invalidScope = true
			}
			continue
		}
		data = append(data, value)
	}
	return data, scopes, invalidScope
}

func scopeRequired(provider string, dataClasses []string) bool {
	switch provider {
	case "alibaba_token_plan_via_claude_code", "deepseek":
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

func scopeAllows(provider, transport string, scopes []string) bool {
	if len(scopes) != 1 {
		return false
	}
	switch provider {
	case "alibaba_token_plan_via_claude_code":
		return transport == "cli_adapter" && scopes[0] == ScopeExecutiveCEO
	case "openai_compatible":
		return transport == "http_adapter" && scopes[0] == ScopeDepartmentLeader
	case "deepseek":
		return transport == "http_adapter" && scopes[0] == ScopeDepartmentWorker
	default:
		return false
	}
}
