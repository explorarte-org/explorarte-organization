package modelegress

import "time"

type Effect string

const (
	EffectAllow        Effect = "allow"
	EffectDeny         Effect = "deny"
	EffectError        Effect = "error"
	EffectNotEvaluated Effect = "not_evaluated"
)

type DataClassification string

const (
	ClassificationPublic         DataClassification = "public"
	ClassificationSanitized      DataClassification = "sanitized"
	ClassificationOrganizational DataClassification = "organizational"
	ClassificationSecret         DataClassification = "secret"
	ClassificationClinical       DataClassification = "clinical"
	ClassificationUnknown        DataClassification = "unknown"
)

type HardDeny struct {
	DataClassification DataClassification `yaml:"data_classification" json:"data_classification"`
	ReasonCode         string             `yaml:"reason_code" json:"reason_code"`
}

type Rule struct {
	ProviderID         string             `yaml:"provider_id" json:"provider_id"`
	DataClassification DataClassification `yaml:"data_classification" json:"data_classification"`
	Effect             Effect             `yaml:"effect" json:"effect"`
	ReasonCode         string             `yaml:"reason_code" json:"reason_code"`
	HardDeny           bool               `yaml:"-" json:"hard_deny"`
}

type CanonicalPolicy struct {
	SchemaVersion  string     `json:"schema_version"`
	DocumentStatus string     `json:"document_status"`
	PolicyID       string     `json:"policy_id"`
	PolicyVersion  int        `json:"policy_version"`
	DefaultAction  Effect     `json:"default_action"`
	HardDenies     []HardDeny `json:"hard_denies"`
	Rules          []Rule     `json:"rules"`
	CanonicalHash  string     `json:"canonical_hash"`
	Path           string     `json:"path"`
}

type PolicyVersion struct {
	ID                                 int64     `json:"id"`
	OrganizationID                     string    `json:"organization_id"`
	PolicyID                           string    `json:"policy_id"`
	PolicyVersion                      int       `json:"policy_version"`
	CanonicalHash                      string    `json:"canonical_hash"`
	IntroducedByOrganizationRevisionID int64     `json:"introduced_by_organization_revision_id"`
	Status                             string    `json:"status"`
	CreatedAt                          time.Time `json:"created_at"`
}

type ResolvedPolicy struct {
	Version                PolicyVersion `json:"version"`
	OrganizationRevisionID int64         `json:"organization_revision_id"`
	CanonicalHash          string        `json:"canonical_hash"`
	DefaultAction          Effect        `json:"default_action"`
	Rules                  []Rule        `json:"rules"`
}

type EvaluationRequest struct {
	ProviderID             string
	ProviderTransport      string
	OrganizationRevisionID int64
	Policy                 ResolvedPolicy
	ContextClassifications []string
}

type Decision struct {
	Effect              Effect   `json:"effect"`
	ReasonCodes         []string `json:"reason_codes"`
	Classifications     []string `json:"classifications"`
	ClassificationsHash string   `json:"classifications_hash"`
}

type RegistryPlan struct {
	OrganizationID         string
	OrganizationRevisionID int64
	CanonicalHash          string
	Policy                 CanonicalPolicy
}

type RegistryStatus struct {
	OrganizationID         string `json:"organization_id"`
	OrganizationRevisionID int64  `json:"organization_revision_id"`
	PolicyID               string `json:"policy_id"`
	PolicyVersion          int    `json:"policy_version"`
	CanonicalHash          string `json:"canonical_hash"`
	MaterializedHash       string `json:"materialized_hash,omitempty"`
	PolicyVersionID        int64  `json:"policy_version_id,omitempty"`
	Rules                  int    `json:"rules"`
	Synchronized           bool   `json:"synchronized"`
}

type RegistryDiff = RegistryStatus

type RegistrySyncResult struct {
	Applied                bool   `json:"applied"`
	NoOp                   bool   `json:"no_op"`
	OrganizationRevisionID int64  `json:"organization_revision_id"`
	PolicyVersionID        int64  `json:"policy_version_id"`
	PolicyID               string `json:"policy_id"`
	PolicyVersion          int    `json:"policy_version"`
	CanonicalHash          string `json:"canonical_hash"`
	Rules                  int    `json:"rules"`
}

type AuthorizationEffect string

const (
	AuthorizationAllow AuthorizationEffect = "allow"
	AuthorizationDeny  AuthorizationEffect = "deny"
	AuthorizationError AuthorizationEffect = "error"
)

type PreSendEvaluation struct {
	InvocationID               int64
	DispatchAttemptID          int64
	PolicyVersionID            int64
	PolicyHash                 string
	OrganizationID             string
	OrganizationRevisionID     int64
	DispatchActorRoleID        string
	SubjectRoleID              string
	ModelProfileVersionID      int64
	ProviderID                 string
	ProviderTransport          string
	ActionDigest               string
	CapabilityMatrixHash       string
	ContextClassifications     []string
	ContextClassificationsHash string
	AuthorizationEffect        AuthorizationEffect
	AuthorizationReasonCode    string
	EgressEffect               Effect
	EgressReasonCodes          []string
	DecisionHash               string
	CorrelationID              string
	CausationID                string
}

type PersistAllowCommand struct {
	Evaluation                 PreSendEvaluation
	ClaimToken                 string
	ProviderIdempotencyKeyHash string
	Deadline                   time.Time
}

type PersistDenyCommand struct {
	Evaluation        PreSendEvaluation
	ClaimToken        string
	ErrorCode         string
	OutboxMaxAttempts int
}
