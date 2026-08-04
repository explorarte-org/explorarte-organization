package registry

import (
	"errors"
	"time"
)

var ErrNotFound = errors.New("organization registry entity not found")

type Organization struct {
	ID               string     `json:"id"`
	DisplayName      string     `json:"display_name"`
	OwnerRoleID      string     `json:"owner_role_id"`
	CEORoleID        string     `json:"ceo_role_id"`
	RuntimeTarget    string     `json:"runtime_target"`
	StateStoreTarget string     `json:"state_store_target"`
	CellsAreExternal bool       `json:"cells_are_external"`
	SchemaVersion    string     `json:"schema_version"`
	DocumentStatus   string     `json:"document_status"`
	CurrentRevision  int64      `json:"current_revision_id,omitempty"`
	CreatedAt        time.Time  `json:"created_at,omitempty"`
	UpdatedAt        time.Time  `json:"updated_at,omitempty"`
	RetiredAt        *time.Time `json:"retired_at,omitempty"`
}

type Unit struct {
	OrganizationID  string     `json:"organization_id"`
	ID              string     `json:"id"`
	DisplayName     string     `json:"display_name"`
	Kind            string     `json:"kind"`
	Operational     bool       `json:"operational"`
	Leaderless      bool       `json:"leaderless"`
	LeaderRoleID    *string    `json:"leader_role_id,omitempty"`
	AgentPath       *string    `json:"agent_path,omitempty"`
	Schedule        *string    `json:"schedule,omitempty"`
	Timezone        *string    `json:"timezone,omitempty"`
	CanonicalStatus string     `json:"canonical_status"`
	SourceRevision  int64      `json:"source_revision_id,omitempty"`
	CreatedAt       time.Time  `json:"created_at,omitempty"`
	UpdatedAt       time.Time  `json:"updated_at,omitempty"`
	RetiredAt       *time.Time `json:"retired_at,omitempty"`
}

type Role struct {
	OrganizationID          string     `json:"organization_id"`
	ID                      string     `json:"id"`
	UnitID                  string     `json:"unit_id"`
	RoleSlug                string     `json:"role_slug"`
	DisplayName             string     `json:"display_name"`
	RuntimeKind             string     `json:"runtime_kind"`
	AuthorityClass          string     `json:"authority_class"`
	CanonicalLeader         bool       `json:"canonical_leader"`
	LegacyFrontmatterLeader bool       `json:"legacy_frontmatter_leader"`
	ModelPolicy             *string    `json:"model_policy,omitempty"`
	ProfilePath             *string    `json:"profile_path,omitempty"`
	DepartmentAgentPath     *string    `json:"department_agent_path,omitempty"`
	MemoryDomain            *string    `json:"memory_domain,omitempty"`
	MemoryPath              *string    `json:"memory_path,omitempty"`
	Summary                 *string    `json:"summary,omitempty"`
	ReportsToSourceText     *string    `json:"reports_to_source_text,omitempty"`
	RAGTopicsSourceText     *string    `json:"rag_topics_source_text,omitempty"`
	SourceStatus            string     `json:"source_status"`
	Enabled                 bool       `json:"enabled"`
	Executable              bool       `json:"executable"`
	SourceHash              string     `json:"source_hash"`
	SourceRevision          int64      `json:"source_revision_id,omitempty"`
	CreatedAt               time.Time  `json:"created_at,omitempty"`
	UpdatedAt               time.Time  `json:"updated_at,omitempty"`
	RetiredAt               *time.Time `json:"retired_at,omitempty"`
}

type ReportingLine struct {
	OrganizationID  string    `json:"organization_id"`
	RoleID          string    `json:"role_id"`
	ReportsToRoleID string    `json:"reports_to_role_id"`
	Relationship    string    `json:"relationship"`
	RevisionID      int64     `json:"revision_id,omitempty"`
	RecordedAt      time.Time `json:"recorded_at,omitempty"`
}

type DocumentDigest struct {
	Path           string `json:"path"`
	SchemaVersion  string `json:"schema_version,omitempty"`
	DocumentStatus string `json:"document_status,omitempty"`
	SemanticHash   string `json:"semantic_hash"`
}

type Counts struct {
	Organizations          int `json:"organizations"`
	Units                  int `json:"units"`
	OperationalUnits       int `json:"operational_units"`
	TransversalUnits       int `json:"transversal_units"`
	Roles                  int `json:"roles"`
	ImportedRoles          int `json:"imported_roles"`
	ProposedRoles          int `json:"proposed_roles"`
	ReportingRelationships int `json:"reporting_relationships"`
}

type Snapshot struct {
	Organization   Organization     `json:"organization"`
	Units          []Unit           `json:"units"`
	Roles          []Role           `json:"roles"`
	ReportingLines []ReportingLine  `json:"reporting_lines"`
	Documents      []DocumentDigest `json:"documents,omitempty"`
	CanonicalHash  string           `json:"canonical_hash,omitempty"`
	Counts         Counts           `json:"counts"`
}

type Revision struct {
	ID                 int64             `json:"id"`
	CanonicalHash      string            `json:"canonical_hash"`
	PreviousRevisionID *int64            `json:"previous_revision_id,omitempty"`
	PreviousHash       string            `json:"previous_hash,omitempty"`
	Status             string            `json:"status"`
	SchemaVersions     map[string]string `json:"schema_versions"`
	DocumentHashes     map[string]string `json:"document_hashes"`
	Counts             Counts            `json:"counts"`
	Diff               Diff              `json:"diff"`
	AppliedAt          time.Time         `json:"applied_at"`
}

type EntityChanges struct {
	Added   []string `json:"added,omitempty"`
	Updated []string `json:"updated,omitempty"`
	Retired []string `json:"retired,omitempty"`
}

type Diff struct {
	Organizations  EntityChanges `json:"organizations"`
	Units          EntityChanges `json:"units"`
	Roles          EntityChanges `json:"roles"`
	ReportingLines EntityChanges `json:"reporting_lines"`
}

func (d Diff) HasChanges() bool {
	return len(d.Organizations.Added)+len(d.Organizations.Updated)+len(d.Organizations.Retired)+
		len(d.Units.Added)+len(d.Units.Updated)+len(d.Units.Retired)+
		len(d.Roles.Added)+len(d.Roles.Updated)+len(d.Roles.Retired)+
		len(d.ReportingLines.Added)+len(d.ReportingLines.Updated)+len(d.ReportingLines.Retired) > 0
}

func (d Diff) Totals() (added, updated, retired int) {
	added = len(d.Organizations.Added) + len(d.Units.Added) + len(d.Roles.Added) + len(d.ReportingLines.Added)
	updated = len(d.Organizations.Updated) + len(d.Units.Updated) + len(d.Roles.Updated) + len(d.ReportingLines.Updated)
	retired = len(d.Organizations.Retired) + len(d.Units.Retired) + len(d.Roles.Retired) + len(d.ReportingLines.Retired)
	return
}

type ValidationIssue struct {
	Code    string `json:"code"`
	Path    string `json:"path,omitempty"`
	Message string `json:"message"`
}

type ValidationReport struct {
	Errors   []ValidationIssue `json:"errors,omitempty"`
	Warnings []ValidationIssue `json:"warnings,omitempty"`
}

func (r ValidationReport) Valid() bool { return len(r.Errors) == 0 }

type ValidationError struct{ Report ValidationReport }

func (e *ValidationError) Error() string {
	if len(e.Report.Errors) == 0 {
		return "canonical registry validation failed"
	}
	return e.Report.Errors[0].Message
}

type CompareResult struct {
	Canonical       Snapshot  `json:"canonical"`
	CurrentRevision *Revision `json:"current_revision,omitempty"`
	Diff            Diff      `json:"diff"`
	Synchronized    bool      `json:"synchronized"`
}

type SyncResult struct {
	Applied       bool      `json:"applied"`
	NoOp          bool      `json:"no_op"`
	CanonicalHash string    `json:"canonical_hash"`
	PreviousHash  string    `json:"previous_hash,omitempty"`
	Revision      *Revision `json:"revision,omitempty"`
	Diff          Diff      `json:"diff"`
}

type RoleFilter struct {
	UnitID      string
	EnabledOnly bool
}
