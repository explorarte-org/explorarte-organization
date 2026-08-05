package authorization

import "time"

type Effect string

const (
	EffectAllow            Effect = "allow"
	EffectDeny             Effect = "deny"
	EffectApprovalRequired Effect = "approval_required"
)

type ReasonCode string

const (
	ReasonAllowedByGrant         ReasonCode = "allowed_by_grant"
	ReasonAllowedByApproval      ReasonCode = "allowed_by_approval"
	ReasonHardDeny               ReasonCode = "hard_deny"
	ReasonGrantMissing           ReasonCode = "grant_missing"
	ReasonApprovalMissing        ReasonCode = "approval_missing"
	ReasonApprovalPending        ReasonCode = "approval_pending"
	ReasonApprovalRejected       ReasonCode = "approval_rejected"
	ReasonApprovalExpired        ReasonCode = "approval_expired"
	ReasonApprovalConsumed       ReasonCode = "approval_consumed"
	ReasonApprovalScopeMismatch  ReasonCode = "approval_scope_mismatch"
	ReasonApprovalPolicyDrift    ReasonCode = "approval_policy_drift"
	ReasonUnknownCapability      ReasonCode = "unknown_capability"
	ReasonUnknownAuthorityClass  ReasonCode = "unknown_authority_class"
	ReasonRoleNotFound           ReasonCode = "role_not_found"
	ReasonRoleDisabled           ReasonCode = "role_disabled"
	ReasonRoleRetired            ReasonCode = "role_retired"
	ReasonRoleNotExecutable      ReasonCode = "role_not_executable"
	ReasonOrganizationMismatch   ReasonCode = "organization_mismatch"
	ReasonRevisionMismatch       ReasonCode = "revision_mismatch"
	ReasonMatrixHashMismatch     ReasonCode = "matrix_hash_mismatch"
	ReasonApprovalCancelled      ReasonCode = "approval_cancelled"
	ReasonAutomaticPolicyMissing ReasonCode = "automatic_policy_not_implemented"
)

type EvaluationRequest struct {
	OrganizationID         string
	OrganizationRevisionID int64
	ActorRoleID            string
	CapabilityID           string
	ResourceType           string
	ResourceID             string
	ActionDigest           string
	ApprovalRequestID      *int64
}

type Evaluation struct {
	Effect            Effect     `json:"effect"`
	ReasonCode        ReasonCode `json:"reason_code"`
	CapabilityID      string     `json:"capability_id"`
	Risk              string     `json:"risk"`
	ApprovalMode      string     `json:"approval_mode,omitempty"`
	AuthorityClass    string     `json:"authority_class,omitempty"`
	MatrixHash        string     `json:"matrix_hash"`
	ApprovalRequestID *int64     `json:"approval_request_id,omitempty"`
}

type RequestStatus string

const (
	RequestPending   RequestStatus = "pending"
	RequestApproved  RequestStatus = "approved"
	RequestRejected  RequestStatus = "rejected"
	RequestCancelled RequestStatus = "cancelled"
	RequestExpired   RequestStatus = "expired"
	RequestConsumed  RequestStatus = "consumed"
)

func (s RequestStatus) Valid() bool {
	switch s {
	case RequestPending, RequestApproved, RequestRejected, RequestCancelled, RequestExpired, RequestConsumed:
		return true
	default:
		return false
	}
}

func (s RequestStatus) Terminal() bool {
	switch s {
	case RequestRejected, RequestCancelled, RequestExpired, RequestConsumed:
		return true
	default:
		return false
	}
}

type Decision string

const (
	DecisionApprove Decision = "approve"
	DecisionReject  Decision = "reject"
)

func (d Decision) Valid() bool { return d == DecisionApprove || d == DecisionReject }

type ApprovalRequest struct {
	ID                     int64         `json:"id"`
	OrganizationID         string        `json:"organization_id"`
	OrganizationRevisionID int64         `json:"organization_revision_id"`
	CapabilityMatrixHash   string        `json:"capability_matrix_hash"`
	RequesterRoleID        string        `json:"requester_role_id"`
	CapabilityID           string        `json:"capability_id"`
	Risk                   string        `json:"risk"`
	ApprovalMode           string        `json:"approval_mode"`
	ResourceType           string        `json:"resource_type"`
	ResourceID             string        `json:"resource_id"`
	ActionDigest           string        `json:"action_digest"`
	IdempotencyKey         string        `json:"idempotency_key"`
	RequestHash            string        `json:"request_hash"`
	Status                 RequestStatus `json:"status"`
	Reason                 string        `json:"reason"`
	Version                int64         `json:"version"`
	CorrelationID          *string       `json:"correlation_id,omitempty"`
	CausationID            *string       `json:"causation_id,omitempty"`
	CreatedAt              time.Time     `json:"created_at"`
	UpdatedAt              time.Time     `json:"updated_at"`
	ExpiresAt              time.Time     `json:"expires_at"`
	ApprovedAt             *time.Time    `json:"approved_at,omitempty"`
	RejectedAt             *time.Time    `json:"rejected_at,omitempty"`
	CancelledAt            *time.Time    `json:"cancelled_at,omitempty"`
	ExpiredAt              *time.Time    `json:"expired_at,omitempty"`
	ConsumedAt             *time.Time    `json:"consumed_at,omitempty"`
}

type ApprovalDecision struct {
	ID              int64     `json:"id"`
	RequestID       int64     `json:"request_id"`
	OrganizationID  string    `json:"organization_id"`
	Decision        Decision  `json:"decision"`
	DecidedByRoleID string    `json:"decided_by_role_id"`
	Reason          string    `json:"reason"`
	Reference       *string   `json:"reference,omitempty"`
	Digest          *string   `json:"digest,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

type ApprovalUse struct {
	ID               int64     `json:"id"`
	RequestID        int64     `json:"request_id"`
	OrganizationID   string    `json:"organization_id"`
	ConsumedByRoleID string    `json:"consumed_by_role_id"`
	ActionDigest     string    `json:"action_digest"`
	CorrelationID    *string   `json:"correlation_id,omitempty"`
	CausationID      *string   `json:"causation_id,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
}

type RequestApprovalCommand struct {
	ActorRoleID    string
	CapabilityID   string
	ResourceType   string
	ResourceID     string
	ActionDigest   string
	IdempotencyKey string
	Reason         string
	TTL            time.Duration
	CorrelationID  string
	CausationID    string
}

type RequestApprovalResult struct {
	Request ApprovalRequest `json:"request"`
	Reused  bool            `json:"reused"`
}

type DecideRequestCommand struct {
	RequestID      int64
	OrganizationID string
	Decision       Decision
	ActorRoleID    string
	Reason         string
	Reference      string
	Digest         string
}

type ConsumeApprovalCommand struct {
	RequestID              int64
	OrganizationID         string
	OrganizationRevisionID int64
	CapabilityMatrixHash   string
	ActorRoleID            string
	CapabilityID           string
	ResourceType           string
	ResourceID             string
	ApprovalMode           string
	ActionDigest           string
	CorrelationID          string
	CausationID            string
}

type ConsumeApprovalResult struct {
	Request    ApprovalRequest `json:"request"`
	Use        *ApprovalUse    `json:"use,omitempty"`
	Reused     bool            `json:"reused"`
	ReasonCode ReasonCode      `json:"reason_code,omitempty"`
}

type CancelRequestCommand struct {
	RequestID      int64
	OrganizationID string
	ActorRoleID    string
	Reason         string
}

type ListRequestsFilter struct {
	OrganizationID  string
	Status          RequestStatus
	RequesterRoleID string
	CapabilityID    string
	Limit           int
}

type ExpireResult struct {
	Expired int `json:"expired"`
}
