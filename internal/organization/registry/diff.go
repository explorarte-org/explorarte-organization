package registry

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
)

type roleHashView struct {
	OrganizationID          string  `json:"organization_id"`
	ID                      string  `json:"id"`
	UnitID                  string  `json:"unit_id"`
	RoleSlug                string  `json:"role_slug"`
	DisplayName             string  `json:"display_name"`
	RuntimeKind             string  `json:"runtime_kind"`
	AuthorityClass          string  `json:"authority_class"`
	CanonicalLeader         bool    `json:"canonical_leader"`
	LegacyFrontmatterLeader bool    `json:"legacy_frontmatter_leader"`
	ModelPolicy             *string `json:"model_policy,omitempty"`
	ProfilePath             *string `json:"profile_path,omitempty"`
	DepartmentAgentPath     *string `json:"department_agent_path,omitempty"`
	MemoryDomain            *string `json:"memory_domain,omitempty"`
	MemoryPath              *string `json:"memory_path,omitempty"`
	Summary                 *string `json:"summary,omitempty"`
	ReportsToSourceText     *string `json:"reports_to_source_text,omitempty"`
	RAGTopicsSourceText     *string `json:"rag_topics_source_text,omitempty"`
	SourceStatus            string  `json:"source_status"`
	Enabled                 bool    `json:"enabled"`
	Executable              bool    `json:"executable"`
}

func roleForHash(role Role) roleHashView {
	return roleHashView{
		OrganizationID: role.OrganizationID, ID: role.ID, UnitID: role.UnitID, RoleSlug: role.RoleSlug,
		DisplayName: role.DisplayName, RuntimeKind: role.RuntimeKind, AuthorityClass: role.AuthorityClass,
		CanonicalLeader: role.CanonicalLeader, LegacyFrontmatterLeader: role.LegacyFrontmatterLeader,
		ModelPolicy: role.ModelPolicy, ProfilePath: role.ProfilePath, DepartmentAgentPath: role.DepartmentAgentPath,
		MemoryDomain: role.MemoryDomain, MemoryPath: role.MemoryPath, Summary: role.Summary,
		ReportsToSourceText: role.ReportsToSourceText, RAGTopicsSourceText: role.RAGTopicsSourceText,
		SourceStatus: role.SourceStatus, Enabled: role.Enabled, Executable: role.Executable,
	}
}

func reportingKey(line ReportingLine) string {
	// Length-prefixed components keep the key deterministic and collision-free
	// without embedding NUL characters. PostgreSQL jsonb rejects \u0000 even
	// when it arrives as an otherwise valid JSON escape.
	parts := [...]string{line.OrganizationID, line.RoleID, line.ReportsToRoleID, line.Relationship}
	var key strings.Builder
	for _, part := range parts {
		key.WriteString(strconv.Itoa(len(part)))
		key.WriteByte(':')
		key.WriteString(part)
	}
	return key.String()
}

func CompareSnapshots(current *Snapshot, canonical Snapshot) Diff {
	if current == nil {
		return Diff{
			Organizations:  EntityChanges{Added: []string{canonical.Organization.ID}},
			Units:          EntityChanges{Added: unitIDs(canonical.Units)},
			Roles:          EntityChanges{Added: roleIDs(canonical.Roles)},
			ReportingLines: EntityChanges{Added: reportingIDs(canonical.ReportingLines)},
		}
	}
	return Diff{
		Organizations: compareEntities(
			map[string]any{current.Organization.ID: organizationComparable(current.Organization)},
			map[string]any{canonical.Organization.ID: organizationComparable(canonical.Organization)},
		),
		Units:          compareEntities(unitComparableMap(current.Units), unitComparableMap(canonical.Units)),
		Roles:          compareEntities(roleComparableMap(current.Roles), roleComparableMap(canonical.Roles)),
		ReportingLines: compareEntities(reportingComparableMap(current.ReportingLines), reportingComparableMap(canonical.ReportingLines)),
	}
}

func compareEntities(current, canonical map[string]any) EntityChanges {
	var changes EntityChanges
	for id, wanted := range canonical {
		existing, ok := current[id]
		if !ok {
			changes.Added = append(changes.Added, id)
			continue
		}
		if !jsonEqual(existing, wanted) {
			changes.Updated = append(changes.Updated, id)
		}
	}
	for id := range current {
		if _, ok := canonical[id]; !ok {
			changes.Retired = append(changes.Retired, id)
		}
	}
	sort.Strings(changes.Added)
	sort.Strings(changes.Updated)
	sort.Strings(changes.Retired)
	return changes
}

func jsonEqual(left, right any) bool {
	lb, _ := json.Marshal(left)
	rb, _ := json.Marshal(right)
	return string(lb) == string(rb)
}

func organizationComparable(value Organization) any {
	return struct {
		ID, DisplayName, OwnerRoleID, CEORoleID, RuntimeTarget, StateStoreTarget, SchemaVersion, DocumentStatus string
		CellsAreExternal                                                                                        bool
	}{value.ID, value.DisplayName, value.OwnerRoleID, value.CEORoleID, value.RuntimeTarget, value.StateStoreTarget, value.SchemaVersion, value.DocumentStatus, value.CellsAreExternal}
}

func unitComparableMap(values []Unit) map[string]any {
	result := make(map[string]any, len(values))
	for _, value := range values {
		result[value.ID] = struct {
			OrganizationID, ID, DisplayName, Kind, CanonicalStatus string
			Operational, Leaderless                                bool
			LeaderRoleID, AgentPath, Schedule, Timezone            *string
		}{value.OrganizationID, value.ID, value.DisplayName, value.Kind, value.CanonicalStatus, value.Operational, value.Leaderless, value.LeaderRoleID, value.AgentPath, value.Schedule, value.Timezone}
	}
	return result
}

func roleComparableMap(values []Role) map[string]any {
	result := make(map[string]any, len(values))
	for _, value := range values {
		result[value.ID] = roleForHash(value)
	}
	return result
}

func reportingComparableMap(values []ReportingLine) map[string]any {
	result := make(map[string]any, len(values))
	for _, value := range values {
		key := reportingKey(value)
		result[key] = struct{ OrganizationID, RoleID, ReportsToRoleID, Relationship string }{value.OrganizationID, value.RoleID, value.ReportsToRoleID, value.Relationship}
	}
	return result
}

func unitIDs(values []Unit) []string {
	result := make([]string, len(values))
	for i := range values {
		result[i] = values[i].ID
	}
	sort.Strings(result)
	return result
}
func roleIDs(values []Role) []string {
	result := make([]string, len(values))
	for i := range values {
		result[i] = values[i].ID
	}
	sort.Strings(result)
	return result
}
func reportingIDs(values []ReportingLine) []string {
	result := make([]string, len(values))
	for i := range values {
		result[i] = reportingKey(values[i])
	}
	sort.Strings(result)
	return result
}
