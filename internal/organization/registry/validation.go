package registry

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var (
	sha256Pattern     = regexp.MustCompile(`^[0-9a-f]{64}$`)
	reasonCodePattern = regexp.MustCompile(`^[a-z0-9]+([._-][a-z0-9]+)*$`)

	// productiveEgressAllowRules mirrors internal/modelegress.ProductiveLoadOptions:
	// R24 compiles the provider/classification surface needed by executive
	// orchestration, gated by a backend-derived executive scope
	// (ValidateExecutiveScope) rather than by this allowlist alone. DeepSeek
	// has no compiled ProviderAdapter (no working HTTP transport) yet —
	// its presence here is policy/scope-gate readiness only, not a
	// productive dispatch claim. Any other allow rule in the productive
	// policy is still rejected. Keep these two allowlists in sync when a
	// future branch adds or removes a provider.
	productiveEgressAllowRules = map[string]map[string]struct{}{
		"alibaba_token_plan_via_claude_code": {"public": {}, "sanitized": {}, "organizational": {}},
		"deepseek":                           {"public": {}, "sanitized": {}, "organizational": {}},
		"openai_compatible":                  {"public": {}, "sanitized": {}, "organizational": {}},
		"gemini":                             {"public": {}, "sanitized": {}, "organizational": {}},
	}
)

func productiveEgressAllowApproved(providerID, classification string) bool {
	classifications, ok := productiveEgressAllowRules[providerID]
	if !ok {
		return false
	}
	_, ok = classifications[classification]
	return ok
}

func validateDocuments(documents parsedDocuments, snapshot Snapshot) ValidationReport {
	validator := validator{documents: documents, snapshot: snapshot}
	validator.run()
	return validator.report
}

type validator struct {
	documents parsedDocuments
	snapshot  Snapshot
	report    ValidationReport
}

func (v *validator) run() {
	v.validateDocumentMetadata()
	v.validateOrganization()
	v.validateIdentifiersAndPaths()
	v.validateRoleReferences()
	v.validateLeaderWorkerMap()
	v.validateReportingGraph()
	v.validateModelsAndAuthorities()
	v.validateModelEgressPolicy()
	v.validateModelInvokeCapability()
	v.validateCounts()
	v.validateProposedRoles()
	v.validateSourceManifest()
}

func (v *validator) addError(code, path, format string, args ...any) {
	v.report.Errors = append(v.report.Errors, ValidationIssue{Code: code, Path: path, Message: fmt.Sprintf(format, args...)})
}

func (v *validator) addWarning(code, path, format string, args ...any) {
	v.report.Warnings = append(v.report.Warnings, ValidationIssue{Code: code, Path: path, Message: fmt.Sprintf(format, args...)})
}

func (v *validator) validateDocumentMetadata() {
	documents := []struct{ path, schema, status string }{
		{"organization.yaml", v.documents.Organization.SchemaVersion, v.documents.Organization.DocumentStatus},
		{"role-catalog.yaml", v.documents.Roles.SchemaVersion, v.documents.Roles.DocumentStatus},
		{"leader-worker-map.yaml", v.documents.LeaderWorker.SchemaVersion, v.documents.LeaderWorker.DocumentStatus},
		{"model-routing.yaml", v.documents.ModelRouting.SchemaVersion, v.documents.ModelRouting.DocumentStatus},
		{"model-egress-policy.yaml", v.documents.ModelEgress.SchemaVersion, v.documents.ModelEgress.DocumentStatus},
		{"capability-matrix.yaml", v.documents.Capabilities.SchemaVersion, v.documents.Capabilities.DocumentStatus},
		{"instruction-precedence.yaml", v.documents.InstructionPrecedence.SchemaVersion, v.documents.InstructionPrecedence.DocumentStatus},
	}
	for _, document := range documents {
		if strings.TrimSpace(document.schema) == "" {
			v.addError("document.schema_version_missing", document.path, "%s has no schema_version", document.path)
		}
		if strings.TrimSpace(document.status) == "" {
			v.addError("document.status_missing", document.path, "%s has no document_status", document.path)
		}
	}
	if v.documents.SourceManifest.SchemaVersion == "" {
		v.addError("document.schema_version_missing", "source-manifest.yaml", "source-manifest.yaml has no schema_version")
	}
}

func (v *validator) validateOrganization() {
	organization := v.documents.Organization
	if err := ValidateUnitID(organization.Organization.ID); err != nil {
		v.addError("organization.id_invalid", "organization.yaml:organization.id", "%v", err)
	}
	if len(organization.OperationalDepartments) != 7 {
		v.addError("organization.operational_department_count", "organization.yaml:operational_departments", "expected exactly 7 operational departments, got %d", len(organization.OperationalDepartments))
	}
	units := make(map[string]string)
	for _, department := range organization.OperationalDepartments {
		if _, exists := units[department.ID]; exists {
			v.addError("unit.duplicate", "organization.yaml:operational_departments", "unit %q is declared more than once", department.ID)
		}
		units[department.ID] = "operational"
		if department.LeaderRoleID == "" {
			v.addError("unit.leader_missing", department.ID, "operational department %q has no leader_role_id", department.ID)
		}
	}
	for _, unit := range organization.TransversalUnits {
		if _, exists := units[unit.ID]; exists {
			v.addError("unit.duplicate", "organization.yaml:transversal_units", "unit %q is declared more than once", unit.ID)
		}
		units[unit.ID] = "transversal"
		if !unit.Leaderless {
			v.addError("unit.transversal_not_leaderless", unit.ID, "transversal unit %q must remain leaderless", unit.ID)
		}
	}
	for _, required := range []string{"empresa", "investigacion"} {
		kind, exists := units[required]
		if !exists || kind != "transversal" {
			v.addError("unit.required_transversal_missing", required, "%q must exist as a transversal unit", required)
		}
	}
}

func (v *validator) validateIdentifiersAndPaths() {
	for _, unit := range v.snapshot.Units {
		if err := ValidateUnitID(unit.ID); err != nil {
			v.addError("unit.id_invalid", unit.ID, "%v", err)
		}
		if unit.AgentPath != nil {
			if err := ValidateReferencePath(*unit.AgentPath); err != nil {
				v.addError("unit.agent_path_invalid", unit.ID, "%v", err)
			}
		}
		if unit.LeaderRoleID != nil {
			if err := ValidateRoleID(*unit.LeaderRoleID); err != nil {
				v.addError("unit.leader_id_invalid", unit.ID, "%v", err)
			}
		}
	}
	seen := make(map[string]struct{})
	for _, role := range v.snapshot.Roles {
		if err := ValidateRoleID(role.ID); err != nil {
			v.addError("role.id_invalid", role.ID, "%v", err)
		}
		if role.ID != role.UnitID+"/"+role.RoleSlug {
			v.addError("role.id_components_mismatch", role.ID, "role id %q must equal unit %q plus role slug %q", role.ID, role.UnitID, role.RoleSlug)
		}
		if err := ValidateUnitID(role.UnitID); err != nil {
			v.addError("role.unit_id_invalid", role.ID, "%v", err)
		}
		if _, exists := seen[role.ID]; exists {
			v.addError("role.duplicate", role.ID, "role %q is declared more than once", role.ID)
		}
		seen[role.ID] = struct{}{}
		for name, value := range map[string]*string{"profile_path": role.ProfilePath, "department_agent_path": role.DepartmentAgentPath, "memory_path": role.MemoryPath} {
			if value != nil {
				if err := ValidateReferencePath(*value); err != nil {
					v.addError("role.path_invalid", role.ID+":"+name, "%v", err)
				}
			}
		}
	}
}

func (v *validator) validateRoleReferences() {
	units := make(map[string]Unit, len(v.snapshot.Units))
	for _, unit := range v.snapshot.Units {
		units[unit.ID] = unit
	}
	roles := make(map[string]Role, len(v.snapshot.Roles))
	for _, role := range v.snapshot.Roles {
		roles[role.ID] = role
		if _, exists := units[role.UnitID]; !exists {
			v.addError("role.unit_missing", role.ID, "role %q belongs to unknown unit %q", role.ID, role.UnitID)
		}
	}
	for _, roleID := range []string{v.snapshot.Organization.OwnerRoleID, v.snapshot.Organization.CEORoleID} {
		if _, exists := roles[roleID]; !exists {
			v.addError("organization.role_missing", roleID, "organization role %q does not exist", roleID)
		}
	}
	leaderCount := make(map[string]int)
	for _, role := range v.snapshot.Roles {
		if role.CanonicalLeader {
			leaderCount[role.UnitID]++
		}
	}
	for _, unit := range v.snapshot.Units {
		if !unit.Operational {
			continue
		}
		if leaderCount[unit.ID] != 1 {
			v.addError("unit.leader_count", unit.ID, "operational department %q must have exactly one canonical leader, got %d", unit.ID, leaderCount[unit.ID])
		}
		if unit.LeaderRoleID == nil {
			continue
		}
		leader, exists := roles[*unit.LeaderRoleID]
		if !exists {
			v.addError("unit.leader_role_missing", unit.ID, "leader %q does not exist", *unit.LeaderRoleID)
			continue
		}
		if leader.UnitID != unit.ID {
			v.addError("unit.leader_wrong_unit", unit.ID, "leader %q belongs to %q, not %q", leader.ID, leader.UnitID, unit.ID)
		}
		if !leader.CanonicalLeader {
			v.addError("unit.leader_not_canonical", unit.ID, "leader %q is not marked canonical_leader", leader.ID)
		}
	}
	if role, exists := roles["investigacion/auditor_cerebro_empresa"]; exists && role.CanonicalLeader {
		v.addError("research.auditor_became_leader", role.ID, "investigacion/auditor_cerebro_empresa must not be a canonical department leader")
	}
}

func (v *validator) validateLeaderWorkerMap() {
	roles := make(map[string]Role, len(v.snapshot.Roles))
	for _, role := range v.snapshot.Roles {
		roles[role.ID] = role
	}
	organizationLeaders := make(map[string]string)
	for _, department := range v.documents.Organization.OperationalDepartments {
		organizationLeaders[department.ID] = department.LeaderRoleID
	}
	seenDepartments := make(map[string]struct{})
	for _, department := range v.documents.LeaderWorker.Departments {
		if _, duplicate := seenDepartments[department.ID]; duplicate {
			v.addError("leader_map.department_duplicate", department.ID, "leader-worker-map contains department %q more than once", department.ID)
		}
		seenDepartments[department.ID] = struct{}{}
		expected, exists := organizationLeaders[department.ID]
		if !exists {
			v.addError("leader_map.department_unknown", department.ID, "leader-worker-map contains unknown operational department %q", department.ID)
		} else if expected != department.Leader {
			v.addError("leader_map.leader_mismatch", department.ID, "organization.yaml leader %q does not match leader-worker-map leader %q", expected, department.Leader)
		}
		for _, workerID := range department.Workers {
			worker, exists := roles[workerID]
			if !exists {
				v.addError("leader_map.worker_missing", workerID, "worker %q does not exist", workerID)
				continue
			}
			if workerID == department.Leader || worker.CanonicalLeader {
				v.addError("leader_map.worker_is_leader", workerID, "worker %q cannot be the canonical leader of %q", workerID, department.ID)
			}
			if worker.UnitID != department.ID {
				v.addError("leader_map.worker_wrong_unit", workerID, "worker %q belongs to %q, not %q", workerID, worker.UnitID, department.ID)
			}
		}
	}
	if len(seenDepartments) != len(organizationLeaders) {
		v.addError("leader_map.department_count", "leader-worker-map.yaml:departments", "leader-worker-map must describe all %d operational departments, got %d", len(organizationLeaders), len(seenDepartments))
	}
	for unitID, roleIDs := range v.documents.LeaderWorker.Transversal {
		for _, roleID := range roleIDs {
			role, exists := roles[roleID]
			if !exists {
				v.addError("leader_map.transversal_role_missing", roleID, "transversal role %q does not exist", roleID)
			} else if role.UnitID != unitID {
				v.addError("leader_map.transversal_role_wrong_unit", roleID, "transversal role %q belongs to %q, not %q", roleID, role.UnitID, unitID)
			}
		}
	}
}

func (v *validator) validateReportingGraph() {
	roles := make(map[string]Role, len(v.snapshot.Roles))
	for _, role := range v.snapshot.Roles {
		roles[role.ID] = role
	}
	graph := make(map[string][]string)
	for _, line := range v.snapshot.ReportingLines {
		if _, exists := roles[line.ReportsToRoleID]; !exists {
			v.addError("reporting.target_missing", line.RoleID, "role %q reports to unknown role %q", line.RoleID, line.ReportsToRoleID)
		}
		if line.RoleID == line.ReportsToRoleID {
			v.addError("reporting.self_reference", line.RoleID, "role %q cannot report to itself", line.RoleID)
		}
		graph[line.RoleID] = append(graph[line.RoleID], line.ReportsToRoleID)
	}
	state := make(map[string]uint8)
	var visit func(string, []string)
	visit = func(node string, stack []string) {
		if state[node] == 2 {
			return
		}
		if state[node] == 1 {
			cycle := append(stack, node)
			v.addError("reporting.cycle", node, "reports_to graph contains a cycle: %s", strings.Join(cycle, " -> "))
			return
		}
		state[node] = 1
		for _, next := range graph[node] {
			visit(next, append(stack, node))
		}
		state[node] = 2
	}
	keys := make([]string, 0, len(roles))
	for roleID := range roles {
		keys = append(keys, roleID)
	}
	sort.Strings(keys)
	for _, roleID := range keys {
		visit(roleID, nil)
	}
}

func (v *validator) validateModelsAndAuthorities() {
	policies := v.documents.ModelRouting.Policies
	grants := v.documents.Capabilities.Grants
	for _, role := range v.snapshot.Roles {
		if role.ModelPolicy == nil || *role.ModelPolicy == "" {
			if role.RuntimeKind != "human" && role.RuntimeKind != "deterministic_executor" {
				v.addError("role.model_policy_missing", role.ID, "role %q requires a model_policy", role.ID)
			}
		} else if _, exists := policies[*role.ModelPolicy]; !exists {
			v.addError("role.model_policy_unknown", role.ID, "role %q references unknown model policy %q", role.ID, *role.ModelPolicy)
		}
		if _, exists := grants[role.AuthorityClass]; !exists {
			if role.SourceStatus == "proposed_profile_required" && !role.Enabled && !role.Executable {
				v.addWarning("role.proposed_authority_unresolved", role.ID, "disabled proposed role %q uses unresolved authority class %q; default deny remains in force", role.ID, role.AuthorityClass)
			} else {
				v.addError("role.authority_class_unknown", role.ID, "role %q references unknown authority class %q", role.ID, role.AuthorityClass)
			}
		}
	}
}

func (v *validator) validateCounts() {
	declared := v.documents.Organization.Counts
	actual := v.snapshot.Counts
	if declared.OperationalDepartments != actual.OperationalUnits {
		v.addError("counts.operational_departments", "organization.yaml:counts", "declared operational department count %d does not match %d", declared.OperationalDepartments, actual.OperationalUnits)
	}
	if declared.ImportedProfiles != actual.ImportedRoles {
		v.addError("counts.imported_profiles", "organization.yaml:counts", "declared imported profile count %d does not match %d", declared.ImportedProfiles, actual.ImportedRoles)
	}
	if declared.ProposedProfiles != actual.ProposedRoles {
		v.addError("counts.proposed_profiles", "organization.yaml:counts", "declared proposed profile count %d does not match %d", declared.ProposedProfiles, actual.ProposedRoles)
	}
}

func (v *validator) validateProposedRoles() {
	for _, role := range v.snapshot.Roles {
		if role.SourceStatus == "proposed_profile_required" && (role.Enabled || role.Executable) {
			v.addError("role.proposed_enabled", role.ID, "proposed role %q must remain disabled and non-executable", role.ID)
		}
	}
}

func (v *validator) validateSourceManifest() {
	seen := make(map[string]struct{})
	for index, entry := range v.documents.SourceManifest.Files {
		path := fmt.Sprintf("source-manifest.yaml:files[%d]", index)
		if strings.TrimSpace(entry.Path) == "" {
			v.addError("source_manifest.path_empty", path, "source manifest path cannot be empty")
		}
		if !sha256Pattern.MatchString(entry.SHA256) {
			v.addError("source_manifest.hash_invalid", path, "source manifest hash for %q is invalid", entry.Path)
		}
		if entry.Bytes < 0 {
			v.addError("source_manifest.bytes_invalid", path, "source manifest byte count for %q cannot be negative", entry.Path)
		}
		key := entry.Path + "\x00" + entry.SHA256
		if _, exists := seen[key]; exists {
			v.addError("source_manifest.duplicate", path, "source manifest contains duplicate entry %q", entry.Path)
		}
		seen[key] = struct{}{}
	}
}

func (v *validator) validateModelEgressPolicy() {
	document := v.documents.ModelEgress
	if strings.TrimSpace(document.PolicyID) == "" {
		v.addError("model_egress.policy_id_missing", "model-egress-policy.yaml:policy_id", "model egress policy_id is required")
	}
	if document.PolicyVersion <= 0 {
		v.addError("model_egress.policy_version_invalid", "model-egress-policy.yaml:policy_version", "model egress policy_version must be positive")
	}
	if document.DefaultAction != "deny" {
		v.addError("model_egress.default_action_invalid", "model-egress-policy.yaml:default_action", "model egress default_action must be deny")
	}
	providers := make(map[string]struct{})
	for _, policy := range v.documents.ModelRouting.Policies {
		providers[policy.Provider] = struct{}{}
	}
	hard := make(map[string]struct{})
	for index, deny := range document.HardDenies {
		path := fmt.Sprintf("model-egress-policy.yaml:hard_denies[%d]", index)
		if deny.DataClassification != "secret" && deny.DataClassification != "clinical" {
			v.addError("model_egress.hard_deny_invalid", path, "only secret and clinical may be global hard denies")
		}
		if !reasonCodePattern.MatchString(deny.ReasonCode) || len(deny.ReasonCode) > 120 {
			v.addError("model_egress.reason_code_invalid", path, "hard deny reason_code must be a bounded lowercase code")
		}
		if _, exists := hard[deny.DataClassification]; exists {
			v.addError("model_egress.hard_deny_duplicate", path, "duplicate hard deny %q", deny.DataClassification)
		}
		hard[deny.DataClassification] = struct{}{}
	}
	for _, required := range []string{"secret", "clinical"} {
		if _, exists := hard[required]; !exists {
			v.addError("model_egress.hard_deny_missing", "model-egress-policy.yaml:hard_denies", "%s must be a hard deny", required)
		}
	}
	seen := make(map[string]struct{})
	for index, rule := range document.Rules {
		path := fmt.Sprintf("model-egress-policy.yaml:rules[%d]", index)
		if _, exists := providers[rule.ProviderID]; !exists {
			v.addError("model_egress.provider_unknown", path, "provider %q does not exist in model-routing.yaml", rule.ProviderID)
		}
		switch rule.DataClassification {
		case "public", "sanitized", "organizational":
		case "secret", "clinical":
			v.addError("model_egress.hard_deny_rule_conflict", path, "%s is governed exclusively by hard_denies", rule.DataClassification)
		default:
			v.addError("model_egress.classification_unknown", path, "classification %q is unknown", rule.DataClassification)
		}
		if rule.Effect != "deny" && !productiveEgressAllowApproved(rule.ProviderID, rule.DataClassification) {
			v.addError("model_egress.productive_allow_forbidden", path, "productive model egress policy allow rule %s/%s is not compiled into this adapter release", rule.ProviderID, rule.DataClassification)
		}
		if !reasonCodePattern.MatchString(rule.ReasonCode) || len(rule.ReasonCode) > 120 {
			v.addError("model_egress.reason_code_invalid", path, "rule reason_code must be a bounded lowercase code")
		}
		key := rule.ProviderID + "\x00" + rule.DataClassification
		if _, exists := seen[key]; exists {
			v.addError("model_egress.rule_duplicate", path, "duplicate provider/classification rule")
		}
		seen[key] = struct{}{}
	}
}

func (v *validator) validateModelInvokeCapability() {
	capabilities := make(map[string]capabilityDefinition)
	for _, capability := range v.documents.Capabilities.Capabilities {
		capabilities[capability.ID] = capability
	}
	capability, exists := capabilities["model.invoke"]
	if !exists {
		v.addError("capability.model_invoke_missing", "capability-matrix.yaml", "model.invoke capability is required")
		return
	}
	if capability.Risk != "high" || capability.Approval != "" {
		v.addError("capability.model_invoke_invalid", "capability-matrix.yaml", "model.invoke must have high risk and no approval mode")
	}
	for authority, grants := range v.documents.Capabilities.Grants {
		has := false
		for _, grant := range grants {
			if grant == "model.invoke" {
				has = true
				break
			}
		}
		if has && authority != "execution_service" {
			v.addError("capability.model_invoke_grant_invalid", "capability-matrix.yaml:grants", "model.invoke may only be granted to execution_service, found %q", authority)
		}
	}
	if !containsString(v.documents.Capabilities.Grants["execution_service"], "model.invoke") {
		v.addError("capability.model_invoke_execution_service_missing", "capability-matrix.yaml:grants.execution_service", "execution_service must receive model.invoke")
	}
	if !containsString(v.documents.Capabilities.HardDenies["owner"], "model.invoke") {
		v.addError("capability.model_invoke_owner_deny_missing", "capability-matrix.yaml:hard_denies.owner", "owner must have hard deny for model.invoke")
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
