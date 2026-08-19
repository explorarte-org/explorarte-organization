package executive

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

var forbiddenModelKeys = map[string]struct{}{
	"provider": {}, "provider_id": {}, "model": {}, "model_id": {}, "transport": {},
	"endpoint": {}, "credential": {}, "credentials": {}, "api_key": {}, "reasoning_effort": {},
	"reasoning_provider_override": {}, "egress": {}, "egress_override": {}, "shell": {}, "command": {},
	"sql": {}, "capability_grant": {}, "capability_grants": {}, "authority": {}, "approval_decision": {},
	"direct_publish": {}, "direct_promote": {}, "memory_visibility": {}, "rag_visibility": {}, "filesystem": {},
	"network": {}, "deployment_permissions": {}, "leader_role_id": {},
	// Mission authority. These are host decisions in every contract; a
	// model naming one is trying to grant itself reach, not describe work.
	"allowed_paths": {}, "required_gates": {}, "base_sha": {}, "budget": {},
	"max_cost": {}, "promotion_approval": {}, "mission_policy": {}, "scope": {},
}

func ParseExecutivePlan(body []byte, limits Limits) (ExecutivePlan, error) {
	var out ExecutivePlan
	if err := decodeStrictModelJSON(body, &out, limits); err != nil {
		return ExecutivePlan{}, err
	}
	if out.SchemaVersion != ExecutivePlanSchemaVersion {
		return ExecutivePlan{}, fmt.Errorf("%w: schema_version", ErrContractRejected)
	}
	if err := validateExecutivePlanShape(out, limits); err != nil {
		return ExecutivePlan{}, err
	}
	return out, nil
}

func ParseDepartmentPlan(body []byte, limits Limits) (DepartmentPlan, error) {
	var out DepartmentPlan
	if err := decodeStrictModelJSON(body, &out, limits); err != nil {
		return DepartmentPlan{}, err
	}
	if out.SchemaVersion != DepartmentPlanSchemaVersion {
		return DepartmentPlan{}, fmt.Errorf("%w: schema_version", ErrContractRejected)
	}
	if err := validateDepartmentPlanShape(out, limits); err != nil {
		return DepartmentPlan{}, err
	}
	return out, nil
}

func ParseDepartmentReview(body []byte, limits Limits) (DepartmentReview, error) {
	var out DepartmentReview
	if err := decodeStrictModelJSON(body, &out, limits); err != nil {
		return DepartmentReview{}, err
	}
	if out.SchemaVersion != DepartmentReviewSchemaVersion {
		return DepartmentReview{}, fmt.Errorf("%w: schema_version", ErrContractRejected)
	}
	if out.Verdict != ReviewAccept && out.Verdict != ReviewNeedsReplan && out.Verdict != ReviewBlocked && out.Verdict != ReviewFail {
		return DepartmentReview{}, fmt.Errorf("%w: invalid review verdict", ErrContractRejected)
	}
	if len(out.ProposedFollowupTasks) > limits.MaxFollowupTasks {
		return DepartmentReview{}, ErrPlanTooLarge
	}
	if err := validateStrings(out.Findings, limits, "findings"); err != nil {
		return DepartmentReview{}, err
	}
	if err := validateStrings(out.UnsatisfiedCriteria, limits, "unsatisfied_criteria"); err != nil {
		return DepartmentReview{}, err
	}
	if err := validateStrings(out.EvidenceRefs, limits, "evidence_refs"); err != nil {
		return DepartmentReview{}, err
	}
	for i := range out.ProposedFollowupTasks {
		if err := validateWorkerTaskShape(&out.ProposedFollowupTasks[i], limits); err != nil {
			return DepartmentReview{}, fmt.Errorf("followup[%d]: %w", i, err)
		}
	}
	return out, nil
}

func ParseExecutiveClosure(body []byte, limits Limits) (ExecutiveClosure, error) {
	var out ExecutiveClosure
	if err := decodeStrictModelJSON(body, &out, limits); err != nil {
		return ExecutiveClosure{}, err
	}
	if out.SchemaVersion != ExecutiveClosureSchemaVersion {
		return ExecutiveClosure{}, fmt.Errorf("%w: schema_version", ErrContractRejected)
	}
	switch out.Status {
	case ClosureCompleted, ClosurePartial, ClosureBlocked, ClosureFailed:
	default:
		return ExecutiveClosure{}, fmt.Errorf("%w: invalid closure status", ErrContractRejected)
	}
	if err := validateRequiredString(out.AnswerToOwner, limits.MaxStringBytes*2, "answer_to_owner"); err != nil {
		return ExecutiveClosure{}, err
	}
	for name, values := range map[string][]string{"completed_items": out.CompletedItems, "blocked_items": out.BlockedItems, "unresolved_decisions": out.UnresolvedDecisions, "evidence_refs": out.EvidenceRefs} {
		if err := validateStrings(values, limits, name); err != nil {
			return ExecutiveClosure{}, err
		}
	}
	return out, nil
}

func decodeStrictModelJSON(body []byte, target any, limits Limits) error {
	if limits.MaxInputBytes <= 0 {
		limits = DefaultLimits()
	}
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return fmt.Errorf("%w: empty JSON", ErrContractRejected)
	}
	if len(body) > limits.MaxInputBytes {
		return ErrPlanTooLarge
	}
	var raw any
	probe := json.NewDecoder(bytes.NewReader(body))
	probe.UseNumber()
	if err := probe.Decode(&raw); err != nil {
		return fmt.Errorf("%w: %v", ErrContractRejected, err)
	}
	if key := findForbiddenModelKey(raw); key != "" {
		return fmt.Errorf("%w: %s", ErrForbiddenField, key)
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("%w: %v", ErrContractRejected, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("%w: multiple top-level JSON values", ErrContractRejected)
		}
		return fmt.Errorf("%w: trailing JSON: %v", ErrContractRejected, err)
	}
	return nil
}

func findForbiddenModelKey(value any) string {
	switch v := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			n := strings.ToLower(strings.TrimSpace(k))
			if _, bad := forbiddenModelKeys[n]; bad {
				return k
			}
			if nested := findForbiddenModelKey(v[k]); nested != "" {
				return nested
			}
		}
	case []any:
		for _, child := range v {
			if nested := findForbiddenModelKey(child); nested != "" {
				return nested
			}
		}
	}
	return ""
}

func validateExecutivePlanShape(p ExecutivePlan, limits Limits) error {
	if err := validateRequiredString(p.Objective, limits.MaxStringBytes, "objective"); err != nil {
		return err
	}
	if len(p.DepartmentRequests) == 0 || len(p.DepartmentRequests) > limits.MaxDepartments {
		return ErrPlanTooLarge
	}
	if err := validateStrings(p.GlobalConstraints, limits, "global_constraints"); err != nil {
		return err
	}
	if len(p.SuccessCriteria) == 0 {
		return fmt.Errorf("%w: success_criteria required", ErrContractRejected)
	}
	if err := validateStrings(p.SuccessCriteria, limits, "success_criteria"); err != nil {
		return err
	}
	if err := validateStrings(p.OwnerDecisionsRequired, limits, "owner_decisions_required"); err != nil {
		return err
	}
	for i, d := range p.DepartmentRequests {
		if err := validateRequiredString(d.UnitID, 120, "unit_id"); err != nil {
			return fmt.Errorf("department[%d]: %w", i, err)
		}
		if err := validateRequiredString(d.Objective, limits.MaxStringBytes, "objective"); err != nil {
			return fmt.Errorf("department[%d]: %w", i, err)
		}
		if err := validateRequiredString(d.Deliverable, limits.MaxStringBytes, "deliverable"); err != nil {
			return fmt.Errorf("department[%d]: %w", i, err)
		}
		if err := validateStrings(d.Constraints, limits, "constraints"); err != nil {
			return fmt.Errorf("department[%d]: %w", i, err)
		}
	}
	return nil
}

func validateDepartmentPlanShape(p DepartmentPlan, limits Limits) error {
	if err := validateRequiredString(p.DepartmentID, 120, "department_id"); err != nil {
		return err
	}
	if len(p.Tasks) > limits.MaxWorkerTasksPerPlan {
		return ErrPlanTooLarge
	}
	if err := validateStrings(p.ReviewCriteria, limits, "review_criteria"); err != nil {
		return err
	}
	if err := validateStrings(p.Unresolved, limits, "unresolved"); err != nil {
		return err
	}
	for i := range p.Tasks {
		if err := validateWorkerTaskShape(&p.Tasks[i], limits); err != nil {
			return fmt.Errorf("task[%d]: %w", i, err)
		}
	}
	return nil
}

// validateWorkerTaskShape takes t by pointer so it can normalize
// TaskClass in place: a nil/empty proposal (including one recovered from
// pre-M1.3 durable output, which never had this field at all) defaults to
// TaskClassGeneralWork here, never TaskClassOf(role) -- that ActorRoleID
// proxy is not reintroduced to "fix" old outputs (M1.3 section 5). A
// non-empty, syntactically invalid proposal is rejected outright: the
// Leader may PROPOSE a class, but the host validates it before it can
// ever reach CreateTaskCommand.
func validateWorkerTaskShape(t *WorkerTaskProposal, limits Limits) error {
	for name, value := range map[string]string{"client_key": t.ClientKey, "assigned_role_id": t.AssignedRoleID, "title": t.Title} {
		if err := validateRequiredString(value, 240, name); err != nil {
			return err
		}
	}
	if t.TaskClass == "" {
		t.TaskClass = TaskClassGeneralWork
	} else if !ValidTaskClass(t.TaskClass) {
		return fmt.Errorf("%w: task_class is invalid", ErrContractRejected)
	}
	if err := validateRequiredString(t.Instructions, limits.MaxInstructionsBytes, "instructions"); err != nil {
		return err
	}
	if len(t.AcceptanceCriteria) == 0 || len(t.AcceptanceCriteria) > limits.MaxAcceptanceCriteria {
		return ErrPlanTooLarge
	}
	if err := validateStrings(t.AcceptanceCriteria, limits, "acceptance_criteria"); err != nil {
		return err
	}
	if len(t.Dependencies) > limits.MaxWorkerTasksPerPlan || len(t.Requirements) > limits.MaxRequirementsPerTask {
		return ErrPlanTooLarge
	}
	if err := validateStrings(t.Dependencies, limits, "dependencies"); err != nil {
		return err
	}
	for _, r := range t.Requirements {
		if err := validateRequiredString(r.Key, 120, "requirement.key"); err != nil {
			return err
		}
		if !validRequirementType(r.Type) {
			return fmt.Errorf("%w: invalid requirement type %q", ErrContractRejected, r.Type)
		}
		if err := validateRequiredString(r.Description, limits.MaxStringBytes, "requirement.description"); err != nil {
			return err
		}
	}
	return nil
}

func validRequirementType(v string) bool {
	switch v {
	case "artifact", "check", "approval", "condition", "result":
		return true
	default:
		return false
	}
}
func validateStrings(values []string, limits Limits, name string) error {
	if len(values) > limits.MaxArrayItems {
		return ErrPlanTooLarge
	}
	for i, v := range values {
		if err := validateRequiredString(v, limits.MaxStringBytes, name); err != nil {
			return fmt.Errorf("%s[%d]: %w", name, i, err)
		}
	}
	return nil
}
func validateRequiredString(v string, max int, name string) error {
	if strings.TrimSpace(v) == "" || len(v) > max || strings.ContainsRune(v, 0) {
		return fmt.Errorf("%w: invalid %s", ErrContractRejected, name)
	}
	return nil
}
