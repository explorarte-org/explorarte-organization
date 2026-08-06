package modelruntime

import (
	"context"
	"crypto/ed25519"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/modeldispatch"
	"github.com/Mireuz13/explorarte-organization/internal/modelegress"
	"github.com/Mireuz13/explorarte-organization/internal/modelidentity"
)

type OrganizationRef struct {
	ID                    string
	RevisionID            int64
	ModelRoutingHash      string
	ModelEgressPolicyHash string
	CapabilityMatrixHash  string
}
type RoleRef struct {
	ID             string
	ModelPolicy    string
	Enabled        bool
	Executable     bool
	AuthorityClass string
	UnitID         string
}
type TaskAttemptRef struct {
	TaskID                 int64
	AttemptID              int64
	OrganizationID         string
	OrganizationRevisionID int64
	AssignedRoleID         string
	TaskStatus             string
	AttemptStatus          string
	LeaseHolderID          string
	LeaseExpiresAt         time.Time
}
type ContextSnapshotRef struct {
	ID                     int64
	OrganizationID         string
	OrganizationRevisionID int64
	ActorRoleID            string
	TaskRef                string
	Status                 string
	RenderedHash           string
	DataClasses            []string
}
type AuthorizationDecision struct {
	Effect     modelegress.AuthorizationEffect
	Allowed    bool
	ReasonCode string
	MatrixHash string
}

type OrganizationCatalog interface {
	CurrentOrganization(context.Context, string) (OrganizationRef, error)
	GetRole(context.Context, string, string) (RoleRef, error)
	ListRoles(context.Context, string) ([]RoleRef, error)
}

type TaskAttemptReader interface {
	GetTaskAttempt(context.Context, int64, int64) (TaskAttemptRef, error)
}
type ContextReader interface {
	GetContextSnapshot(context.Context, int64) (ContextSnapshotRef, error)
	ValidateContextSnapshot(context.Context, int64) error
	RenderContextSnapshot(context.Context, int64) ([]byte, error)
}
type CapabilityEvaluator interface {
	EvaluateDispatch(context.Context, string, int64, string, string, string) (AuthorizationDecision, error)
}

type ExecutionIdentityPolicyCatalog interface {
	ResolveActive(context.Context, string) (modelidentity.ResolvedPolicy, error)
	ResolveByID(context.Context, string, int64) (modelidentity.ResolvedPolicy, error)
}

type ExecutionPrivateKeyLoader interface {
	LoadExecutionPrivateKey(string) (ed25519.PrivateKey, error)
}

type ExecutionIdentityService interface {
	ResolvePolicy(context.Context, string) (modelidentity.ResolvedPolicy, error)
	ResolvePolicyByID(context.Context, string, int64) (modelidentity.ResolvedPolicy, error)
	ResolveActiveKeyByFingerprint(context.Context, string, int64, string) (modelidentity.ExecutionIdentityKey, error)
	Issue(context.Context, modelidentity.ChallengeScope, modelidentity.ResolvedPolicy) (modelidentity.IssuedChallenge, error)
	Verify(modelidentity.ExecutionIdentityKey, modelidentity.IssuedChallenge, []byte, modelidentity.ResolvedPolicy) (modelidentity.VerifiedAssertion, error)
}

type EgressPolicyCatalog interface {
	ResolveForRevision(context.Context, string, int64) (modelegress.ResolvedPolicy, error)
}

type EgressDecisionEvaluator interface {
	Evaluate(modelegress.EvaluationRequest) (modelegress.Decision, error)
}

type EgressEvaluationStore interface {
	PersistPreSendAllowAndMarkSendStarted(context.Context, modelegress.PersistAllowCommand) error
	PersistPreSendDenyAndFail(context.Context, modelegress.PersistDenyCommand) error
}

type ProviderAdapter interface {
	ProviderID() string
	Dispatch(context.Context, CanonicalRequest) (RawResponse, error)
}
type AdapterRegistry interface {
	Get(string) (ProviderAdapter, bool)
}

type RegistryStore interface {
	RecordRegistryValidated(context.Context, string, string) error
	RegistryStatus(context.Context, string, int64, string) (RegistryStatus, error)
	ApplyRegistry(context.Context, RegistryPlan, int) (RegistrySyncResult, error)
	GetBinding(context.Context, string, int64, string) (ResolvedBinding, error)
}

type InvocationStore interface {
	CreateInvocation(context.Context, PreparedInvocation, int) (CreateInvocationResult, error)
	GetInvocation(context.Context, int64) (Invocation, error)
	ListInvocations(context.Context, string, int) ([]Invocation, error)
	ClaimInvocation(context.Context, ClaimCommand, RuntimeConfig) (ClaimedInvocation, error)
	ClaimInvocationAuthenticated(context.Context, AuthenticatedClaimCommand, RuntimeConfig) (ClaimedInvocation, error)
	MarkResponseReceived(context.Context, int64, int64, string, string) (Invocation, error)
	CompleteInvocation(context.Context, CompletionCommand, int) (DispatchResult, error)
	FailBeforeSend(context.Context, FailureCommand, int) (Invocation, error)
	FailAfterResponse(context.Context, FailureCommand, int) (Invocation, error)
	MarkAmbiguous(context.Context, FailureCommand, string, int) (Invocation, error)
	RequestCancellation(context.Context, int64, string, string, int) (CancelResult, error)
	CancellationRequested(context.Context, int64) (bool, error)
	MarkCancelled(context.Context, FailureCommand, int) (Invocation, error)
	Reconcile(context.Context, string, int, int) (ReconcileResult, error)
	WatchCancellation(context.Context, int64) error
}

type Store interface {
	RegistryStore
	InvocationStore
}

type PreparedInvocation struct {
	Command                CreateInvocationCommand
	OrganizationRevisionID int64
	Binding                ResolvedBinding
	RequestHash            string
	RequiredCapabilities   []ModelCapability
	OutputSchema           []byte
	EgressPolicy           modelegress.ResolvedPolicy
	IdentityPolicy         modelidentity.ResolvedPolicy
	Assignment             modeldispatch.ResolvedAssignment
}
type ClaimCommand struct {
	InvocationID         int64
	ClaimedBy            string
	ExecutionPrincipalID int64
}
type AuthenticatedClaimCommand struct {
	InvocationID         int64
	ClaimedBy            string
	ExecutionPrincipalID int64
	IdentityKeyID        int64
	ChallengeID          int64
	ChallengeNonce       string
	Signature            []byte
}

type CompletionCommand struct {
	InvocationID      int64
	DispatchAttemptID int64
	ClaimToken        string
	Response          NormalizedResponse
}
type FailureCommand struct {
	InvocationID          int64
	DispatchAttemptID     int64
	ClaimToken            string
	ErrorCode             string
	OutcomeClassification string
	EventType             string
}

type Clock interface{ Now() time.Time }
type ClockFunc func() time.Time

func (f ClockFunc) Now() time.Time { return f() }
