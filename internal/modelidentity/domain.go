package modelidentity

import "time"

type Algorithm string

const AlgorithmEd25519 Algorithm = "ed25519"

type KeyStatus string

const (
	KeyActive   KeyStatus = "active"
	KeyRetiring KeyStatus = "retiring"
	KeyRetired  KeyStatus = "retired"
	KeyRevoked  KeyStatus = "revoked"
)

type PolicyStatus string

const (
	PolicyActive     PolicyStatus = "active"
	PolicySuperseded PolicyStatus = "superseded"
)

type CanonicalPolicy struct {
	SchemaVersion       string    `json:"schema_version"`
	DocumentStatus      string    `json:"document_status"`
	PolicyID            string    `json:"policy_id"`
	PolicyVersion       int       `json:"policy_version"`
	DefaultAction       string    `json:"default_action"`
	Algorithm           Algorithm `json:"algorithm"`
	ChallengeTTLSeconds int       `json:"challenge_ttl_seconds"`
	ClockSkewSeconds    int       `json:"clock_skew_seconds"`
	CanonicalHash       string    `json:"canonical_hash"`
	Path                string    `json:"path"`
}

type PolicyVersion struct {
	ID                  int64        `json:"id"`
	OrganizationID      string       `json:"organization_id"`
	PolicyID            string       `json:"policy_id"`
	PolicyVersion       int          `json:"policy_version"`
	CanonicalHash       string       `json:"canonical_hash"`
	Algorithm           Algorithm    `json:"algorithm"`
	ChallengeTTLSeconds int          `json:"challenge_ttl_seconds"`
	ClockSkewSeconds    int          `json:"clock_skew_seconds"`
	Status              PolicyStatus `json:"status"`
	CreatedAt           time.Time    `json:"created_at"`
	SupersededAt        *time.Time   `json:"superseded_at,omitempty"`
}

type ResolvedPolicy struct {
	Version PolicyVersion `json:"version"`
}

type RegistryStatus struct {
	OrganizationID   string `json:"organization_id"`
	PolicyID         string `json:"policy_id"`
	PolicyVersion    int    `json:"policy_version"`
	CanonicalHash    string `json:"canonical_hash"`
	MaterializedHash string `json:"materialized_hash,omitempty"`
	PolicyVersionID  int64  `json:"policy_version_id,omitempty"`
	Synchronized     bool   `json:"synchronized"`
}

type RegistrySyncResult struct {
	Applied         bool   `json:"applied"`
	NoOp            bool   `json:"no_op"`
	PolicyVersionID int64  `json:"policy_version_id"`
	PolicyID        string `json:"policy_id"`
	PolicyVersion   int    `json:"policy_version"`
	CanonicalHash   string `json:"canonical_hash"`
}

type ExecutionIdentityKey struct {
	ID                   int64      `json:"id"`
	OrganizationID       string     `json:"organization_id"`
	ExecutionPrincipalID int64      `json:"execution_principal_id"`
	KeyVersion           int        `json:"key_version"`
	Algorithm            Algorithm  `json:"algorithm"`
	PublicKey            []byte     `json:"-"`
	PublicKeyFingerprint string     `json:"public_key_fingerprint"`
	SecretRef            string     `json:"-"`
	Status               KeyStatus  `json:"status"`
	IdempotencyKey       string     `json:"idempotency_key"`
	RequestHash          string     `json:"request_hash"`
	CreatedByRoleID      string     `json:"created_by_role_id"`
	ValidFrom            time.Time  `json:"valid_from"`
	ValidUntil           *time.Time `json:"valid_until,omitempty"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
	RetiredAt            *time.Time `json:"retired_at,omitempty"`
	RevokedAt            *time.Time `json:"revoked_at,omitempty"`
	RevokedByRoleID      string     `json:"revoked_by_role_id,omitempty"`
	RevocationReasonCode string     `json:"revocation_reason_code,omitempty"`
}

type RegisterKeyCommand struct {
	OrganizationID        string     `json:"organization_id"`
	ExecutionPrincipalKey string     `json:"execution_principal_key"`
	PublicKeyBase64       string     `json:"public_key_base64"`
	SecretRef             string     `json:"secret_ref"`
	ValidUntil            *time.Time `json:"valid_until,omitempty"`
	IdempotencyKey        string     `json:"idempotency_key"`
}

type RegisterKeyResult struct {
	Key    ExecutionIdentityKey `json:"key"`
	Reused bool                 `json:"reused"`
}

type RotateKeyCommand = RegisterKeyCommand

type PreparedKey struct {
	OrganizationID       string
	ExecutionPrincipalID int64
	PublicKey            []byte
	PublicKeyFingerprint string
	SecretRef            string
	ValidUntil           *time.Time
	IdempotencyKey       string
	RequestHash          string
	CreatedByRoleID      string
}

type ChallengeScope struct {
	OrganizationID                   string
	OrganizationRevisionID           int64
	InvocationID                     int64
	DispatcherAssignmentID           int64
	ExecutionPrincipalID             int64
	ExecutionIdentityPolicyVersionID int64
	ExecutionIdentityPolicyHash      string
	ExecutionIdentityKeyID           int64
	ActionDigest                     string
	RequestHash                      string
}

type AssertionPayload struct {
	SchemaVersion                    int    `json:"schema_version"`
	OrganizationID                   string `json:"organization_id"`
	OrganizationRevisionID           int64  `json:"organization_revision_id"`
	InvocationID                     int64  `json:"invocation_id"`
	DispatcherAssignmentID           int64  `json:"dispatcher_assignment_id"`
	ExecutionPrincipalID             int64  `json:"execution_principal_id"`
	ExecutionIdentityPolicyVersionID int64  `json:"execution_identity_policy_version_id"`
	ExecutionIdentityPolicyHash      string `json:"execution_identity_policy_hash"`
	ExecutionIdentityKeyID           int64  `json:"execution_identity_key_id"`
	ChallengeID                      int64  `json:"challenge_id"`
	ChallengeNonce                   string `json:"challenge_nonce"`
	ActionDigest                     string `json:"action_digest"`
	RequestHash                      string `json:"request_hash"`
	IssuedAt                         string `json:"issued_at"`
	ExpiresAt                        string `json:"expires_at"`
}

type Challenge struct {
	ID                               int64      `json:"id"`
	OrganizationID                   string     `json:"organization_id"`
	OrganizationRevisionID           int64      `json:"organization_revision_id"`
	InvocationID                     int64      `json:"invocation_id"`
	DispatcherAssignmentID           int64      `json:"dispatcher_assignment_id"`
	ExecutionPrincipalID             int64      `json:"execution_principal_id"`
	ExecutionIdentityPolicyVersionID int64      `json:"execution_identity_policy_version_id"`
	ExecutionIdentityPolicyHash      string     `json:"execution_identity_policy_hash"`
	ExecutionIdentityKeyID           int64      `json:"execution_identity_key_id"`
	NonceHash                        string     `json:"nonce_hash"`
	PayloadHash                      string     `json:"payload_hash"`
	ActionDigest                     string     `json:"action_digest"`
	RequestHash                      string     `json:"request_hash"`
	IssuedAt                         time.Time  `json:"issued_at"`
	ExpiresAt                        time.Time  `json:"expires_at"`
	ConsumedAt                       *time.Time `json:"consumed_at,omitempty"`
	InvalidatedAt                    *time.Time `json:"invalidated_at,omitempty"`
	CreatedAt                        time.Time  `json:"created_at"`
}

type PreparedChallenge struct {
	Scope       ChallengeScope
	Nonce       string
	NonceHash   string
	PayloadHash string
	IssuedAt    time.Time
	ExpiresAt   time.Time
}

type IssuedChallenge struct {
	Challenge Challenge `json:"challenge"`
	Nonce     string    `json:"challenge_nonce"`
	Payload   []byte    `json:"-"`
}

type VerifiedAssertion struct {
	ChallengeID   int64     `json:"challenge_id"`
	KeyID         int64     `json:"execution_identity_key_id"`
	PayloadHash   string    `json:"payload_hash"`
	SignatureHash string    `json:"signature_hash"`
	AssertionHash string    `json:"assertion_hash"`
	VerifiedAt    time.Time `json:"verified_at"`
}
