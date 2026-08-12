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
