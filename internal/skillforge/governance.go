package skillforge

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
)

var (
	ErrGovernanceDenied = errors.New("skill forge governance authorization denied")
)

const (
	canonicalSkillForgeOrganizationID = "explorarte"
	skillForgeDecisionID              = "D-006"
	skillForgeAuthorizationScope      = "skill_authoring_evaluation"
)

// AuthoringAuthorization records the structural decision facts that permit
// an execution profile and role to operate under Skill Forge.
type AuthoringAuthorization struct {
	Approved           bool                           `json:"approved"`
	OrganizationID     string                         `json:"organization_id"`
	RoleID             string                         `json:"role_id"`
	ExecutionProfileID string                         `json:"execution_profile_id"`
	Scope              string                         `json:"scope"`
	ResolvedDecisionID string                         `json:"resolved_decision_id"`
	Authorization      registry.DecisionAuthorization `json:"authorization"`
	DecisionText       string                         `json:"decision_text"`
}

// SkillForgeGovernanceReader resolves authoring authorization through structural
// canonical inspection rather than ad-hoc string searching.
type SkillForgeGovernanceReader interface {
	ResolveAuthoringAuthorization(
		ctx context.Context,
		organizationID string,
		roleID string,
		executionProfileID string,
	) (AuthoringAuthorization, error)
}

// CanonicalSkillForgeGovernanceReader inspects the validated canonical registry snapshot
// and resolved owner decisions to authorize Skill Forge roles and profiles.
type CanonicalSkillForgeGovernanceReader struct {
	canonicalDir string
	loader       *registry.Loader
}

// NewCanonicalSkillForgeGovernanceReader constructs a governance reader bound to a canonical directory.
func NewCanonicalSkillForgeGovernanceReader(canonicalDir string) (*CanonicalSkillForgeGovernanceReader, error) {
	loader, err := registry.NewLoader(canonicalDir)
	if err != nil {
		return nil, fmt.Errorf("validate canonical dir %q: %w", canonicalDir, err)
	}
	return &CanonicalSkillForgeGovernanceReader{
		canonicalDir: canonicalDir,
		loader:       loader,
	}, nil
}

func (r *CanonicalSkillForgeGovernanceReader) ResolveAuthoringAuthorization(
	ctx context.Context,
	organizationID string,
	roleID string,
	executionProfileID string,
) (AuthoringAuthorization, error) {
	if err := ctx.Err(); err != nil {
		return AuthoringAuthorization{}, err
	}
	if r == nil || r.loader == nil {
		return AuthoringAuthorization{}, deny("canonical governance reader is not configured")
	}

	// 1. Organization boundary check: only the authoritative organization is governed.
	if organizationID != canonicalSkillForgeOrganizationID {
		return AuthoringAuthorization{}, deny("unauthorized organization %q", organizationID)
	}

	// 2. Canonical snapshot load & validation (fail closed on any validation error).
	snapshot, report, err := r.loader.Load()
	if err != nil {
		return AuthoringAuthorization{}, deny("canonical registry load failure: %v", err)
	}
	if !report.Valid() {
		return AuthoringAuthorization{}, deny("canonical registry validation report invalid")
	}
	if snapshot.Organization.ID != canonicalSkillForgeOrganizationID || snapshot.Organization.ID != organizationID {
		return AuthoringAuthorization{}, deny("canonical organization %q does not match requested organization %q", snapshot.Organization.ID, organizationID)
	}

	// 3. Role eligibility & authority scope check.
	var matchedRole *registry.Role
	for i := range snapshot.Roles {
		if snapshot.Roles[i].ID == roleID {
			matchedRole = &snapshot.Roles[i]
			break
		}
	}
	if matchedRole == nil {
		return AuthoringAuthorization{}, deny("role %q not found in canonical catalog", roleID)
	}
	if matchedRole.OrganizationID != snapshot.Organization.ID {
		return AuthoringAuthorization{}, deny("role %q belongs to organization %q", roleID, matchedRole.OrganizationID)
	}
	if matchedRole.SourceStatus != "imported_source" {
		return AuthoringAuthorization{}, deny("role %q status %q is not imported_source", roleID, matchedRole.SourceStatus)
	}
	if !matchedRole.Enabled || !matchedRole.Executable {
		return AuthoringAuthorization{}, deny("role %q is not enabled/executable (enabled=%v, executable=%v)", roleID, matchedRole.Enabled, matchedRole.Executable)
	}
	// Verify authority scope: must be a specialist worker, never executive/root authority.
	if matchedRole.AuthorityClass != "specialist" || matchedRole.RuntimeKind != "worker_agent" {
		return AuthoringAuthorization{}, deny("role %q has disallowed worker authority (class=%q runtime=%q)", roleID, matchedRole.AuthorityClass, matchedRole.RuntimeKind)
	}

	// 4. Decision D-006 is read from the same validated snapshot as the role.
	d006, err := resolvedD006(snapshot)
	if err != nil {
		return AuthoringAuthorization{}, err
	}

	// 5. D-006's typed scope is the only source of approval. Its prose remains
	// an audit record and is never interpreted as an authorization primitive.
	authorization, err := validatedD006Authorization(d006)
	if err != nil {
		return AuthoringAuthorization{}, err
	}
	if !containsExact(authorization.OrganizationIDs, organizationID) {
		return AuthoringAuthorization{}, deny("organization %q is absent from D-006 authorization", organizationID)
	}
	if !containsExact(authorization.RoleIDs, roleID) {
		return AuthoringAuthorization{}, deny("role %q is absent from D-006 authorization", roleID)
	}
	if !containsExact(authorization.ExecutionProfileIDs, executionProfileID) {
		return AuthoringAuthorization{}, deny("execution profile %q is absent from D-006 authorization", executionProfileID)
	}

	return AuthoringAuthorization{
		Approved:           true,
		OrganizationID:     organizationID,
		RoleID:             roleID,
		ExecutionProfileID: executionProfileID,
		Scope:              authorization.Scope,
		ResolvedDecisionID: d006.ID,
		Authorization:      authorization,
		DecisionText:       d006.Decision,
	}, nil
}

func resolvedD006(snapshot registry.Snapshot) (*registry.DecisionResolved, error) {
	for _, openID := range snapshot.OpenDecisionIDs {
		if openID == skillForgeDecisionID {
			return nil, deny("D-006 is still open")
		}
	}

	var d006 *registry.DecisionResolved
	for index := range snapshot.ResolvedDecisions {
		decision := &snapshot.ResolvedDecisions[index]
		if decision.ID != skillForgeDecisionID {
			continue
		}
		if d006 != nil {
			return nil, deny("D-006 appears more than once in resolved decisions")
		}
		d006 = decision
	}
	if d006 == nil {
		return nil, deny("resolved decision D-006 not found")
	}
	if strings.TrimSpace(d006.Question) == "" || strings.TrimSpace(d006.Decision) == "" || strings.TrimSpace(d006.DecidedIn) == "" {
		return nil, deny("resolved decision D-006 is malformed")
	}
	return d006, nil
}

func validatedD006Authorization(decision *registry.DecisionResolved) (registry.DecisionAuthorization, error) {
	if decision == nil || decision.Authorization == nil {
		return registry.DecisionAuthorization{}, deny("resolved decision D-006 has no typed authorization")
	}
	authorization := *decision.Authorization
	if authorization.Scope != skillForgeAuthorizationScope {
		return registry.DecisionAuthorization{}, deny("D-006 authorization scope %q is not Skill Forge authoring/evaluation", authorization.Scope)
	}
	if err := validateAuthorizationList("organization_ids", authorization.OrganizationIDs, func(value string) bool {
		return value == canonicalSkillForgeOrganizationID
	}); err != nil {
		return registry.DecisionAuthorization{}, err
	}
	if err := validateAuthorizationList("role_ids", authorization.RoleIDs, func(value string) bool {
		return registry.ValidateRoleID(value) == nil
	}); err != nil {
		return registry.DecisionAuthorization{}, err
	}
	if err := validateAuthorizationList("execution_profile_ids", authorization.ExecutionProfileIDs, func(value string) bool {
		return value != "" && !strings.ContainsAny(value, " \t\r\n")
	}); err != nil {
		return registry.DecisionAuthorization{}, err
	}
	authorization.OrganizationIDs = append([]string(nil), authorization.OrganizationIDs...)
	authorization.RoleIDs = append([]string(nil), authorization.RoleIDs...)
	authorization.ExecutionProfileIDs = append([]string(nil), authorization.ExecutionProfileIDs...)
	return authorization, nil
}

func validateAuthorizationList(name string, values []string, valid func(string) bool) error {
	if len(values) == 0 {
		return deny("D-006 authorization %s is empty", name)
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != value || !valid(value) {
			return deny("D-006 authorization %s contains malformed value %q", name, value)
		}
		if _, exists := seen[value]; exists {
			return deny("D-006 authorization %s contains duplicate value %q", name, value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func containsExact(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func deny(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrGovernanceDenied, fmt.Sprintf(format, args...))
}
