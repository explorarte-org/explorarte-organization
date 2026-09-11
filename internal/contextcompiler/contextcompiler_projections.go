package contextcompiler

import (
	"fmt"

	"github.com/Mireuz13/explorarte-organization/internal/contextengine"
	"gopkg.in/yaml.v3"
)

// RoleCatalogSourceReference is the exact source_reference this
// projection is registered against (see R10_DESIGN_AUDIT.md section E:
// role-catalog.yaml measured at 50,034 of 82,506 total context bytes in
// r9/r9.1, ~61% -- the single largest contributor by far, and the only
// segment this V1 profile projects).
const RoleCatalogSourceReference = "docs/canonical/role-catalog.yaml"

type roleCatalogDocument struct {
	SchemaVersion  string                   `yaml:"schema_version"`
	DocumentStatus string                   `yaml:"document_status"`
	Roles          []map[string]interface{} `yaml:"roles"`
}

// RoleCatalogSelfEntry projects role-catalog.yaml down to exactly the
// requesting actor's own entry -- identity/authority/responsibilities
// the actor's role already carries in full, per R10_DESIGN_AUDIT.md
// section E. It removes every OTHER role's entry (identity of roles the
// task has no relationship to), never the actor's own. Determinism: for
// a fixed segment content + actorRoleID, output is byte-identical,
// because yaml.Marshal on a map built from a single found entry (whose
// key order came from yaml.v3's deterministic unmarshal-then-remarshal)
// is stable across calls in the same process/Go version.
func RoleCatalogSelfEntry(segment contextengine.Segment, actorRoleID string) ([]byte, string, error) {
	var doc roleCatalogDocument
	if err := yaml.Unmarshal(segment.Content, &doc); err != nil {
		return nil, "", fmt.Errorf("contextcompiler: parse role-catalog.yaml: %w", err)
	}

	var self map[string]interface{}
	for _, role := range doc.Roles {
		id, _ := role["id"].(string)
		if id == actorRoleID {
			self = role
			break
		}
	}
	if self == nil {
		// Fail closed: if the actor's own role entry cannot be found,
		// this is NOT a safe projection -- pass the full catalog
		// through unchanged rather than silently dropping the actor's
		// own identity. Compile treats an empty projectedContent as
		// "projection declined, use original".
		return nil, "role_catalog_self_entry_not_found_fell_back_to_full_catalog", nil
	}

	projected := roleCatalogDocument{
		SchemaVersion:  doc.SchemaVersion,
		DocumentStatus: doc.DocumentStatus + "_projected_self_entry_only",
		Roles:          []map[string]interface{}{self},
	}
	out, err := yaml.Marshal(projected)
	if err != nil {
		return nil, "", fmt.Errorf("contextcompiler: marshal projected role-catalog.yaml: %w", err)
	}
	return out, "projected_subset:role_catalog_self_entry", nil
}

// findRoleByID returns the entry whose id field equals target, or nil.
// Identity comes from role-catalog.yaml's own `id` field -- the same
// durable field RoleCatalogSelfEntry already matches against -- never
// from splitting or pattern-matching the role ID string.
func findRoleByID(roles []map[string]interface{}, target string) map[string]interface{} {
	for _, role := range roles {
		if id, _ := role["id"].(string); id == target {
			return role
		}
	}
	return nil
}

// roleDepartment reads role-catalog.yaml's own structured `department`
// field off one entry (e.g. "ingenieria_ia" on "ingenieria_ia/qa") --
// never derived from the role ID string.
func roleDepartment(role map[string]interface{}) (string, bool) {
	department, ok := role["department"].(string)
	return department, ok && department != ""
}

// roleIsCanonicalLeader reads role-catalog.yaml's own structured
// `canonical_leader` field -- the same field that already marks exactly
// one entry per operational department as its leader (see
// leader-worker-map.yaml/organization.yaml's leader_role_id, which this
// field agrees with by construction).
func roleIsCanonicalLeader(role map[string]interface{}) bool {
	leader, _ := role["canonical_leader"].(bool)
	return leader
}

// marshalProjectedRoles renders a role-catalog.yaml subset in the exact
// shape RoleCatalogSelfEntry already produces, differing only in which
// roles and which document_status suffix. selected's order is whatever
// the caller built it in; every caller here iterates doc.Roles in its own
// already-deterministic order, so output stays byte-identical for a fixed
// segment + actorRoleID, per ProjectionFunc's determinism contract.
func marshalProjectedRoles(doc roleCatalogDocument, statusSuffix string, selected []map[string]interface{}) ([]byte, error) {
	projected := roleCatalogDocument{
		SchemaVersion:  doc.SchemaVersion,
		DocumentStatus: doc.DocumentStatus + statusSuffix,
		Roles:          selected,
	}
	out, err := yaml.Marshal(projected)
	if err != nil {
		return nil, fmt.Errorf("contextcompiler: marshal projected role-catalog.yaml: %w", err)
	}
	return out, nil
}

// RoleCatalogSelfAndOwnLeaderEntries projects role-catalog.yaml down to the
// requesting actor's own entry plus the canonical_leader entry sharing the
// actor's own department (a harmless no-op addition when the actor already
// IS that leader, since the loop below skips the actor's own id).
// Used by department_worker/v1.
func RoleCatalogSelfAndOwnLeaderEntries(segment contextengine.Segment, actorRoleID string) ([]byte, string, error) {
	var doc roleCatalogDocument
	if err := yaml.Unmarshal(segment.Content, &doc); err != nil {
		return nil, "", fmt.Errorf("contextcompiler: parse role-catalog.yaml: %w", err)
	}
	self := findRoleByID(doc.Roles, actorRoleID)
	if self == nil {
		return nil, "role_catalog_self_entry_not_found_fell_back_to_full_catalog", nil
	}
	selected := []map[string]interface{}{self}
	if department, ok := roleDepartment(self); ok {
		for _, role := range doc.Roles {
			if id, _ := role["id"].(string); id == actorRoleID {
				continue
			}
			if dept, _ := roleDepartment(role); dept == department && roleIsCanonicalLeader(role) {
				selected = append(selected, role)
			}
		}
	}
	out, err := marshalProjectedRoles(doc, "_projected_self_and_own_leader", selected)
	if err != nil {
		return nil, "", err
	}
	return out, "projected_subset:role_catalog_self_and_own_leader", nil
}

// RoleCatalogOwnDepartment projects role-catalog.yaml down to every entry
// sharing the requesting actor's own department (its leader and every
// worker of that one unit) -- the conservative, unit-scoped fallback used
// by department_plan/v1 and department_review/v1.
func RoleCatalogOwnDepartment(segment contextengine.Segment, actorRoleID string) ([]byte, string, error) {
	var doc roleCatalogDocument
	if err := yaml.Unmarshal(segment.Content, &doc); err != nil {
		return nil, "", fmt.Errorf("contextcompiler: parse role-catalog.yaml: %w", err)
	}
	self := findRoleByID(doc.Roles, actorRoleID)
	if self == nil {
		return nil, "role_catalog_self_entry_not_found_fell_back_to_full_catalog", nil
	}
	department, ok := roleDepartment(self)
	if !ok {
		return nil, "role_catalog_self_entry_department_missing_fell_back_to_full_catalog", nil
	}
	selected := make([]map[string]interface{}, 0, 8)
	for _, role := range doc.Roles {
		if dept, _ := roleDepartment(role); dept == department {
			selected = append(selected, role)
		}
	}
	out, err := marshalProjectedRoles(doc, "_projected_own_department", selected)
	if err != nil {
		return nil, "", err
	}
	return out, "projected_subset:role_catalog_own_department", nil
}

// RoleCatalogSelfAndDepartmentLeaders projects role-catalog.yaml down to
// the requesting actor's own entry plus every entry marked
// canonical_leader:true -- executive_ceo_plan/v1, the one purpose that
// decomposes a goal across department leaders it must be able to name.
func RoleCatalogSelfAndDepartmentLeaders(segment contextengine.Segment, actorRoleID string) ([]byte, string, error) {
	var doc roleCatalogDocument
	if err := yaml.Unmarshal(segment.Content, &doc); err != nil {
		return nil, "", fmt.Errorf("contextcompiler: parse role-catalog.yaml: %w", err)
	}
	self := findRoleByID(doc.Roles, actorRoleID)
	if self == nil {
		return nil, "role_catalog_self_entry_not_found_fell_back_to_full_catalog", nil
	}
	selected := []map[string]interface{}{self}
	for _, role := range doc.Roles {
		if id, _ := role["id"].(string); id == actorRoleID {
			continue
		}
		if roleIsCanonicalLeader(role) {
			selected = append(selected, role)
		}
	}
	out, err := marshalProjectedRoles(doc, "_projected_self_and_department_leaders", selected)
	if err != nil {
		return nil, "", err
	}
	return out, "projected_subset:role_catalog_self_and_department_leaders", nil
}
