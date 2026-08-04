package registry

type organizationDocument struct {
	SchemaVersion          string                  `yaml:"schema_version" json:"schema_version"`
	DocumentStatus         string                  `yaml:"document_status" json:"document_status"`
	Organization           organizationDefinition  `yaml:"organization" json:"organization"`
	Executive              executiveDefinition     `yaml:"executive" json:"executive"`
	OperationalDepartments []departmentDefinition  `yaml:"operational_departments" json:"operational_departments"`
	TransversalUnits       []transversalDefinition `yaml:"transversal_units" json:"transversal_units"`
	DailyCycle             dailyCycleDefinition    `yaml:"daily_cycle" json:"daily_cycle"`
	Counts                 organizationCounts      `yaml:"counts" json:"counts"`
}

type organizationDefinition struct {
	ID               string `yaml:"id" json:"id"`
	DisplayName      string `yaml:"display_name" json:"display_name"`
	OwnerRoleID      string `yaml:"owner_role_id" json:"owner_role_id"`
	CanonicalSource  string `yaml:"canonical_source" json:"canonical_source"`
	RuntimeTarget    string `yaml:"runtime_target" json:"runtime_target"`
	StateStoreTarget string `yaml:"state_store_target" json:"state_store_target"`
	CellsAreExternal bool   `yaml:"cells_are_external" json:"cells_are_external"`
}

type executiveDefinition struct {
	CEORoleID string             `yaml:"ceo_role_id" json:"ceo_role_id"`
	Observer  observerDefinition `yaml:"observer" json:"observer"`
}

type observerDefinition struct {
	ID                string `yaml:"id" json:"id"`
	RuntimeKind       string `yaml:"runtime_kind" json:"runtime_kind"`
	MayReplyToOwner   bool   `yaml:"may_reply_to_owner" json:"may_reply_to_owner"`
	MayDelegate       bool   `yaml:"may_delegate" json:"may_delegate"`
	MayMutateProjects bool   `yaml:"may_mutate_projects" json:"may_mutate_projects"`
	LateResultPolicy  string `yaml:"late_result_policy" json:"late_result_policy"`
	ModelPolicy       string `yaml:"model_policy" json:"model_policy"`
	DecisionStatus    string `yaml:"decision_status" json:"decision_status"`
}

type departmentDefinition struct {
	ID           string `yaml:"id" json:"id"`
	LeaderRoleID string `yaml:"leader_role_id" json:"leader_role_id"`
	AgentPath    string `yaml:"agent_path" json:"agent_path"`
}

type transversalDefinition struct {
	ID                             string   `yaml:"id" json:"id"`
	Kind                           string   `yaml:"kind" json:"kind"`
	Leaderless                     bool     `yaml:"leaderless" json:"leaderless"`
	Roles                          []string `yaml:"roles" json:"roles"`
	HourlySchedule                 string   `yaml:"hourly_schedule" json:"hourly_schedule,omitempty"`
	Timezone                       string   `yaml:"timezone" json:"timezone,omitempty"`
	PublishesDirectlyToApprovedRAG *bool    `yaml:"publishes_directly_to_approved_rag" json:"publishes_directly_to_approved_rag,omitempty"`
}

type dailyCycleDefinition struct {
	Enabled        bool    `yaml:"enabled" json:"enabled"`
	Schedule       *string `yaml:"schedule" json:"schedule"`
	Action         *string `yaml:"action" json:"action"`
	DecisionStatus string  `yaml:"decision_status" json:"decision_status"`
}

type organizationCounts struct {
	OperationalDepartments int `yaml:"operational_departments" json:"operational_departments"`
	ImportedProfiles       int `yaml:"imported_profiles" json:"imported_profiles"`
	ProposedProfiles       int `yaml:"proposed_profiles" json:"proposed_profiles"`
}

type roleCatalogDocument struct {
	SchemaVersion  string           `yaml:"schema_version" json:"schema_version"`
	DocumentStatus string           `yaml:"document_status" json:"document_status"`
	Roles          []roleDefinition `yaml:"roles" json:"roles"`
}

type roleDefinition struct {
	ID                      string   `yaml:"id" json:"id"`
	Department              string   `yaml:"department" json:"department"`
	Role                    string   `yaml:"role" json:"role"`
	DisplayName             string   `yaml:"display_name" json:"display_name"`
	RuntimeKind             string   `yaml:"runtime_kind" json:"runtime_kind"`
	AuthorityClass          string   `yaml:"authority_class" json:"authority_class"`
	CanonicalLeader         bool     `yaml:"canonical_leader" json:"canonical_leader"`
	LegacyFrontmatterLeader bool     `yaml:"legacy_frontmatter_leader" json:"legacy_frontmatter_leader"`
	ModelPolicy             *string  `yaml:"model_policy" json:"model_policy"`
	ProfilePath             *string  `yaml:"profile_path" json:"profile_path"`
	DepartmentAgentPath     *string  `yaml:"department_agent_path" json:"department_agent_path"`
	MemoryDomain            *string  `yaml:"memory_domain" json:"memory_domain"`
	MemoryPath              *string  `yaml:"memory_path" json:"memory_path"`
	Summary                 *string  `yaml:"summary" json:"summary"`
	ReportsToSourceText     *string  `yaml:"reports_to_source_text" json:"reports_to_source_text"`
	RAGTopicsSourceText     *string  `yaml:"rag_topics_source_text" json:"rag_topics_source_text"`
	Status                  string   `yaml:"status" json:"status"`
	ReportsTo               []string `yaml:"reports_to" json:"reports_to"`
}

type leaderWorkerDocument struct {
	SchemaVersion  string                   `yaml:"schema_version" json:"schema_version"`
	DocumentStatus string                   `yaml:"document_status" json:"document_status"`
	Departments    []leaderWorkerDepartment `yaml:"departments" json:"departments"`
	Transversal    map[string][]string      `yaml:"transversal" json:"transversal"`
}

type leaderWorkerDepartment struct {
	ID      string   `yaml:"id" json:"id"`
	Leader  string   `yaml:"leader" json:"leader"`
	Workers []string `yaml:"workers" json:"workers"`
}

type modelRoutingDocument struct {
	SchemaVersion     string                    `yaml:"schema_version" json:"schema_version"`
	DocumentStatus    string                    `yaml:"document_status" json:"document_status"`
	Policies          map[string]modelPolicyDoc `yaml:"policies" json:"policies"`
	RoutingInvariants []string                  `yaml:"routing_invariants" json:"routing_invariants"`
}

type modelPolicyDoc struct {
	Provider            string   `yaml:"provider" json:"provider"`
	Model               string   `yaml:"model" json:"model"`
	Transport           string   `yaml:"transport" json:"transport"`
	DirectHTTPForbidden *bool    `yaml:"direct_http_forbidden" json:"direct_http_forbidden,omitempty"`
	SourceOfDecision    string   `yaml:"source_of_decision" json:"source_of_decision,omitempty"`
	LegacyConflicts     []string `yaml:"legacy_conflicts" json:"legacy_conflicts,omitempty"`
	DecisionStatus      string   `yaml:"decision_status" json:"decision_status,omitempty"`
	ReasoningEffort     string   `yaml:"reasoning_effort" json:"reasoning_effort,omitempty"`
	Schedule            string   `yaml:"schedule" json:"schedule,omitempty"`
}

type capabilityMatrixDocument struct {
	SchemaVersion  string                    `yaml:"schema_version" json:"schema_version"`
	DocumentStatus string                    `yaml:"document_status" json:"document_status"`
	DefaultPolicy  string                    `yaml:"default_policy" json:"default_policy"`
	Capabilities   []capabilityDefinition    `yaml:"capabilities" json:"capabilities"`
	Grants         map[string][]string       `yaml:"grants" json:"grants"`
	HardDenies     map[string][]string       `yaml:"hard_denies" json:"hard_denies"`
	SkillLifecycle skillLifecycleDefinition  `yaml:"skill_lifecycle" json:"skill_lifecycle"`
	ImportedSkills []importedSkillDefinition `yaml:"imported_skills" json:"imported_skills"`
}

type capabilityDefinition struct {
	ID       string `yaml:"id" json:"id"`
	Risk     string `yaml:"risk" json:"risk"`
	Approval string `yaml:"approval" json:"approval,omitempty"`
}

type skillLifecycleDefinition struct {
	States             []string `yaml:"states" json:"states"`
	ActivationRequires []string `yaml:"activation_requires" json:"activation_requires"`
}

type importedSkillDefinition struct {
	ID           string  `yaml:"id" json:"id"`
	OwnerRoleID  string  `yaml:"owner_role_id" json:"owner_role_id"`
	MemoryDomain string  `yaml:"memory_domain" json:"memory_domain"`
	Origin       string  `yaml:"origin" json:"origin"`
	BaseProtocol string  `yaml:"base_protocol" json:"base_protocol"`
	Verifier     *string `yaml:"verifier" json:"verifier"`
	Status       string  `yaml:"status" json:"status"`
	SourceFile   string  `yaml:"source_file" json:"source_file"`
	SourceSHA256 string  `yaml:"source_sha256" json:"source_sha256"`
	Description  string  `yaml:"description" json:"description"`
}

type instructionPrecedenceDocument struct {
	SchemaVersion       string                    `yaml:"schema_version" json:"schema_version"`
	DocumentStatus      string                    `yaml:"document_status" json:"document_status"`
	AuthorityTiers      []authorityTierDefinition `yaml:"authority_tiers" json:"authority_tiers"`
	PromptAssemblyOrder []string                  `yaml:"prompt_assembly_order" json:"prompt_assembly_order"`
	ConflictRules       []string                  `yaml:"conflict_rules" json:"conflict_rules"`
}

type authorityTierDefinition struct {
	Priority               int      `yaml:"priority" json:"priority"`
	ID                     string   `yaml:"id" json:"id"`
	Content                []string `yaml:"content" json:"content"`
	MayGrantCapabilities   bool     `yaml:"may_grant_capabilities" json:"may_grant_capabilities"`
	TreatedAsUntrustedData *bool    `yaml:"treated_as_untrusted_data" json:"treated_as_untrusted_data,omitempty"`
}

type decisionsRequiredDocument struct {
	Branch            string             `yaml:"branch" json:"branch"`
	Status            string             `yaml:"status" json:"status"`
	AcceptedFromOwner []string           `yaml:"accepted_from_owner" json:"accepted_from_owner"`
	Open              []decisionRequired `yaml:"open" json:"open"`
}

type decisionRequired struct {
	ID       string `yaml:"id" json:"id"`
	Question string `yaml:"question" json:"question"`
	Blocks   string `yaml:"blocks" json:"blocks"`
}

type sourceManifestDocument struct {
	SchemaVersion string                `yaml:"schema_version" json:"schema_version"`
	GeneratedAt   string                `yaml:"generated_at" json:"-"`
	Files         []sourceManifestEntry `yaml:"files" json:"files"`
}

type sourceManifestEntry struct {
	Path   string `yaml:"path" json:"path"`
	SHA256 string `yaml:"sha256" json:"sha256"`
	Bytes  int64  `yaml:"bytes" json:"bytes"`
}

type parsedDocuments struct {
	Organization          organizationDocument
	Roles                 roleCatalogDocument
	LeaderWorker          leaderWorkerDocument
	ModelRouting          modelRoutingDocument
	Capabilities          capabilityMatrixDocument
	InstructionPrecedence instructionPrecedenceDocument
	Decisions             decisionsRequiredDocument
	SourceManifest        sourceManifestDocument
}
