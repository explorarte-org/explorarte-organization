package modelruntime

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/modeldispatch"
	"github.com/Mireuz13/explorarte-organization/internal/modelegress"
	"github.com/Mireuz13/explorarte-organization/internal/modelidentity"
)

type fakeAssignmentResolver struct {
	resolved modeldispatch.ResolvedAssignment
	err      error
}

func (f fakeAssignmentResolver) ResolveActive(context.Context, string, int64, int64, string) (modeldispatch.ResolvedAssignment, error) {
	return f.resolved, f.err
}
func (f fakeAssignmentResolver) GetByID(context.Context, string, int64) (modeldispatch.ResolvedAssignment, error) {
	return f.resolved, f.err
}

type fakePrincipalResolver struct {
	principal modeldispatch.ExecutionPrincipal
	err       error
}

func (f fakePrincipalResolver) ResolveByKey(context.Context, string, string) (modeldispatch.ExecutionPrincipal, error) {
	return f.principal, f.err
}

type fakeCatalog struct {
	org  OrganizationRef
	role RoleRef
}

func (f fakeCatalog) CurrentOrganization(context.Context, string) (OrganizationRef, error) {
	return f.org, nil
}
func (f fakeCatalog) GetRole(context.Context, string, string) (RoleRef, error) { return f.role, nil }
func (f fakeCatalog) ListRoles(context.Context, string) ([]RoleRef, error) {
	return []RoleRef{f.role}, nil
}

type fakeTaskReader struct {
	ref TaskAttemptRef
	err error
}

func (f fakeTaskReader) GetTaskAttempt(context.Context, int64, int64) (TaskAttemptRef, error) {
	return f.ref, f.err
}

type fakeContextReader struct {
	ref           ContextSnapshotRef
	rendered      []byte
	validationErr error
	renderErr     error
	renderCalls   int
}

func (f *fakeContextReader) GetContextSnapshot(context.Context, int64) (ContextSnapshotRef, error) {
	return f.ref, nil
}
func (f *fakeContextReader) ValidateContextSnapshot(context.Context, int64) error {
	return f.validationErr
}
func (f *fakeContextReader) RenderContextSnapshot(context.Context, int64) ([]byte, error) {
	f.renderCalls++
	if f.renderErr != nil {
		return nil, f.renderErr
	}
	return append([]byte(nil), f.rendered...), nil
}

type fakeEvaluator struct {
	allow bool
	err   error
}

func (f fakeEvaluator) EvaluateDispatch(context.Context, string, int64, string, string, string) (AuthorizationDecision, error) {
	if f.err != nil {
		return AuthorizationDecision{}, f.err
	}
	if f.allow {
		return AuthorizationDecision{Effect: modelegress.AuthorizationAllow, Allowed: true, ReasonCode: "allowed_by_grant", MatrixHash: SHA256Bytes([]byte("matrix"))}, nil
	}
	return AuthorizationDecision{Effect: modelegress.AuthorizationDeny, ReasonCode: "grant_missing", MatrixHash: SHA256Bytes([]byte("matrix"))}, nil
}

type failingEgressEvaluator struct{ err error }

func (f failingEgressEvaluator) Evaluate(modelegress.EvaluationRequest) (modelegress.Decision, error) {
	return modelegress.Decision{Effect: modelegress.EffectError, ReasonCodes: []string{"egress_operational_error"}}, f.err
}

type fakeAdapterRegistry struct{ value ProviderAdapter }

func (f fakeAdapterRegistry) Get(id string) (ProviderAdapter, bool) {
	return f.value, f.value != nil && f.value.ProviderID() == id
}

type deterministicAdapter struct {
	preflightErr error
	err          error
	response     *RawResponse
	calls        int
}

func (d *deterministicAdapter) ProviderID() string { return "test.fake" }
func (d *deterministicAdapter) Descriptor() AdapterDescriptor {
	return AdapterDescriptor{
		ProviderID: "test.fake", AdapterID: "deterministic-test", AdapterVersion: 1,
		Transport: TransportFake, RequestSchemaVersion: "test.fake.request.v1",
		ResponseSchemaVersion: "test.fake.response.v1",
		EndpointFingerprint:   SHA256Bytes([]byte("test.fake:endpoint")),
		CredentialRefHash:     SHA256Bytes([]byte("test.fake:credential")),
	}
}
func (d *deterministicAdapter) Preflight(ctx context.Context, request ProviderPreflightRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if d.preflightErr != nil {
		return d.preflightErr
	}
	if request.ProviderID != "test.fake" || request.ProviderModelID == "" || request.Deadline.IsZero() {
		return ErrInvalidRequest
	}
	return nil
}
func (d *deterministicAdapter) Dispatch(_ context.Context, request CanonicalRequest) (RawResponse, error) {
	d.calls++
	if d.response != nil {
		return *d.response, d.err
	}
	if d.err != nil {
		return RawResponse{}, d.err
	}
	return RawResponse{Content: []byte(`{"ok":true}`), HiddenReasoning: []byte("do not persist"), ProviderRequestID: "fake-1", InputTokens: 2, OutputTokens: 3}, nil
}

type fakeStore struct {
	binding            ResolvedBinding
	invocation         Invocation
	claimed            ClaimedInvocation
	result             DispatchResult
	failed             bool
	providerRejected   bool
	providerNotSent    bool
	ambiguous          bool
	completed          bool
	created            bool
	cancelled          bool
	prepared           PreparedInvocation
	modelInput         PreparedModelInput
	allowEvaluation    *modelegress.PreSendEvaluation
	denyEvaluation     *modelegress.PreSendEvaluation
	egressPolicy       *modelegress.ResolvedPolicy
	egressPolicyErr    error
	identityPolicy     modelidentity.ResolvedPolicy
	identityKey        modelidentity.ExecutionIdentityKey
	identityPrivateKey ed25519.PrivateKey
	// lastFailureUsage captures whatever FailureCommand.Usage the service
	// last passed to RejectProviderResponse or FailAfterResponse -- the P0
	// usage-survives-failure mechanism (see recoveredUsage in
	// dispatch_service.go). nil means the call passed no recovered usage.
	lastFailureUsage *Usage
	retryableFailure bool
}

// fakeCostBudgetGate is a deterministic CostBudgetGate double used to
// assert the P0 financial state machine: exactly one of
// Reconcile/Release/MarkPendingReconciliation must fire per dispatch that
// applied a reservation, and never more than one (settledCalls catches a
// double-settle, which would mean the deferred auto-release in
// dispatch_service.go's Dispatch fired on top of an explicit settle).
type fakeCostBudgetGate struct {
	reserveErr     error
	reconcileErr   error
	releaseErr     error
	pendingErr     error
	reserveCalls   int
	releaseCalls   int
	pendingCalls   int
	reconcileCalls []reconcileCall
}
type reconcileCall struct{ inputTokens, cachedInputTokens, outputTokens int64 }

func (f *fakeCostBudgetGate) settledCalls() int {
	return f.releaseCalls + f.pendingCalls + len(f.reconcileCalls)
}
func (f *fakeCostBudgetGate) Reserve(_ context.Context, request CostReservationRequest, _ time.Time) (CostReservation, error) {
	f.reserveCalls++
	if f.reserveErr != nil {
		return CostReservation{}, f.reserveErr
	}
	return CostReservation{ProviderID: request.ProviderID, ProviderModelID: request.ProviderModelID, InvocationID: request.InvocationID, WalletApplied: true}, nil
}
func (f *fakeCostBudgetGate) Reconcile(_ context.Context, _ CostReservation, inputTokens, cachedInputTokens, outputTokens int64, _ time.Time) error {
	f.reconcileCalls = append(f.reconcileCalls, reconcileCall{inputTokens, cachedInputTokens, outputTokens})
	return f.reconcileErr
}
func (f *fakeCostBudgetGate) Release(context.Context, CostReservation, time.Time) error {
	f.releaseCalls++
	return f.releaseErr
}
func (f *fakeCostBudgetGate) MarkPendingReconciliation(context.Context, CostReservation, time.Time) error {
	f.pendingCalls++
	return f.pendingErr
}

func (f *fakeStore) RecordRegistryValidated(context.Context, string, string) error { return nil }
func (f *fakeStore) RegistryStatus(context.Context, string, int64, string) (RegistryStatus, error) {
	return RegistryStatus{}, nil
}
func (f *fakeStore) ApplyRegistry(context.Context, RegistryPlan, int) (RegistrySyncResult, error) {
	return RegistrySyncResult{}, nil
}
func (f *fakeStore) GetBinding(context.Context, string, int64, string) (ResolvedBinding, error) {
	return f.binding, nil
}
func fixtureEgressPolicy() modelegress.ResolvedPolicy {
	return modelegress.ResolvedPolicy{Version: modelegress.PolicyVersion{ID: 17, Status: "materialized", OrganizationID: "explorarte", PolicyID: "model-egress", PolicyVersion: 1, CanonicalHash: SHA256Bytes([]byte("egress"))}, OrganizationRevisionID: 7, CanonicalHash: SHA256Bytes([]byte("egress")), DefaultAction: modelegress.EffectDeny, Rules: []modelegress.Rule{{ProviderID: "*", DataClassification: modelegress.ClassificationSecret, Effect: modelegress.EffectDeny, ReasonCode: "secret_egress_forbidden", HardDeny: true}, {ProviderID: "*", DataClassification: modelegress.ClassificationClinical, Effect: modelegress.EffectDeny, ReasonCode: "clinical_egress_forbidden", HardDeny: true}, {ProviderID: "test.fake", DataClassification: modelegress.ClassificationOrganizational, Effect: modelegress.EffectAllow, ReasonCode: "fixture_allow"}}}
}
func (f *fakeStore) ResolveForRevision(context.Context, string, int64) (modelegress.ResolvedPolicy, error) {
	if f.egressPolicyErr != nil {
		return modelegress.ResolvedPolicy{}, f.egressPolicyErr
	}
	if f.egressPolicy != nil {
		return *f.egressPolicy, nil
	}
	return fixtureEgressPolicy(), nil
}
func (f *fakeStore) PersistPreSendAllowAndMarkSendStarted(_ context.Context, command modelegress.PersistAllowCommand) error {
	evaluation := command.Evaluation
	f.allowEvaluation = &evaluation
	f.invocation.Status = InvocationSendStarted
	return nil
}
func (f *fakeStore) PersistPreSendDenyAndFail(_ context.Context, command modelegress.PersistDenyCommand) error {
	evaluation := command.Evaluation
	f.denyEvaluation = &evaluation
	f.failed = true
	f.invocation.Status = InvocationFailed
	f.invocation.ErrorCode = command.ErrorCode
	return nil
}
func (f *fakeStore) ResolveActive(context.Context, string) (modelidentity.ResolvedPolicy, error) {
	return f.identityPolicy, nil
}
func (f *fakeStore) ResolveByID(context.Context, string, int64) (modelidentity.ResolvedPolicy, error) {
	return f.identityPolicy, nil
}
func (f *fakeStore) ResolvePolicy(context.Context, string) (modelidentity.ResolvedPolicy, error) {
	return f.identityPolicy, nil
}
func (f *fakeStore) ResolvePolicyByID(context.Context, string, int64) (modelidentity.ResolvedPolicy, error) {
	return f.identityPolicy, nil
}
func (f *fakeStore) ResolveActiveKeyByFingerprint(_ context.Context, _ string, _ int64, fingerprint string) (modelidentity.ExecutionIdentityKey, error) {
	if fingerprint != f.identityKey.PublicKeyFingerprint {
		return modelidentity.ExecutionIdentityKey{}, modelidentity.ErrKeyNotFound
	}
	return f.identityKey, nil
}
func (f *fakeStore) Issue(_ context.Context, scope modelidentity.ChallengeScope, policy modelidentity.ResolvedPolicy) (modelidentity.IssuedChallenge, error) {
	now := mustTime("2026-01-01T00:00:00Z")
	nonce := "unit-test-nonce"
	_, payload, err := modelidentity.BuildAssertionPayload(scope, 61, nonce, now, now.Add(time.Minute))
	if err != nil {
		return modelidentity.IssuedChallenge{}, err
	}
	challenge := modelidentity.Challenge{ID: 61, OrganizationID: scope.OrganizationID, OrganizationRevisionID: scope.OrganizationRevisionID, InvocationID: scope.InvocationID, DispatcherAssignmentID: scope.DispatcherAssignmentID, ExecutionPrincipalID: scope.ExecutionPrincipalID, ExecutionIdentityPolicyVersionID: scope.ExecutionIdentityPolicyVersionID, ExecutionIdentityPolicyHash: scope.ExecutionIdentityPolicyHash, ExecutionIdentityKeyID: scope.ExecutionIdentityKeyID, NonceHash: modelidentity.SHA256Bytes([]byte(nonce)), PayloadHash: modelidentity.SHA256Bytes(payload), ActionDigest: scope.ActionDigest, RequestHash: scope.RequestHash, IssuedAt: now, ExpiresAt: now.Add(time.Minute), CreatedAt: now}
	_ = policy
	return modelidentity.IssuedChallenge{Challenge: challenge, Nonce: nonce, Payload: payload}, nil
}
func (f *fakeStore) Verify(key modelidentity.ExecutionIdentityKey, issued modelidentity.IssuedChallenge, signature []byte, policy modelidentity.ResolvedPolicy) (modelidentity.VerifiedAssertion, error) {
	return modelidentity.VerifyAssertion(key, issued, signature, issued.Challenge.IssuedAt.Add(time.Second), time.Duration(policy.Version.ClockSkewSeconds)*time.Second)
}
func (f *fakeStore) LoadExecutionPrivateKey(string) (ed25519.PrivateKey, error) {
	return append(ed25519.PrivateKey(nil), f.identityPrivateKey...), nil
}

// ProviderFailureRetryable defaults to false: a fixture that has recorded no
// provider outcome has nothing to call transient.
func (f *fakeStore) ProviderFailureRetryable(context.Context, int64) (bool, error) {
	return f.retryableFailure, nil
}

func (f *fakeStore) CreateInvocation(_ context.Context, p PreparedInvocation, _ int) (CreateInvocationResult, error) {
	f.created = true
	f.prepared = p
	f.modelInput = p.ModelInput
	v := f.invocation
	v.RequestHash = p.RequestHash
	return CreateInvocationResult{Invocation: v}, nil
}
func (f *fakeStore) GetModelInput(context.Context, int64) (PreparedModelInput, error) {
	return f.modelInput, nil
}
func (f *fakeStore) GetInvocation(context.Context, int64) (Invocation, error) {
	return f.invocation, nil
}
func (f *fakeStore) ListInvocations(context.Context, string, int) ([]Invocation, error) {
	return []Invocation{f.invocation}, nil
}
func (f *fakeStore) ClaimInvocation(context.Context, ClaimCommand, RuntimeConfig) (ClaimedInvocation, error) {
	return f.claimed, nil
}
func (f *fakeStore) ClaimInvocationAuthenticated(_ context.Context, command AuthenticatedClaimCommand, _ RuntimeConfig) (ClaimedInvocation, error) {
	if command.ChallengeID != 61 || command.ChallengeNonce != "unit-test-nonce" || len(command.Signature) != ed25519.SignatureSize {
		return ClaimedInvocation{}, modelidentity.ErrAssertionInvalid
	}
	claimed := f.claimed
	claimed.Invocation = f.invocation
	claimed.DispatchAttempt.ExecutionPrincipalID = int64Pointer(command.ExecutionPrincipalID)
	claimed.DispatchAttempt.ExecutionIdentityKeyID = int64Pointer(command.IdentityKeyID)
	assertionID := int64(71)
	verifiedAt := mustTime("2026-01-01T00:00:01Z")
	claimed.DispatchAttempt.IdentityAssertionID = &assertionID
	claimed.DispatchAttempt.IdentityVerifiedAt = &verifiedAt
	f.claimed = claimed
	return claimed, nil
}
func (f *fakeStore) MarkResponseReceived(context.Context, int64, int64, string, ProviderOutcome) (Invocation, error) {
	v := f.invocation
	v.Status = InvocationResponseReceived
	f.invocation = v
	return v, nil
}
func (f *fakeStore) RejectProviderResponse(_ context.Context, command FailureCommand, _ ProviderOutcome, _ int) (Invocation, error) {
	f.failed = true
	f.providerRejected = true
	f.lastFailureUsage = command.Usage
	v := f.invocation
	v.Status = InvocationFailed
	f.invocation = v
	return v, nil
}
func (f *fakeStore) FailCommittedBeforeRequest(context.Context, FailureCommand, ProviderOutcome, int) (Invocation, error) {
	f.failed = true
	f.providerNotSent = true
	v := f.invocation
	v.Status = InvocationFailed
	f.invocation = v
	return v, nil
}
func (f *fakeStore) CompleteInvocation(_ context.Context, c CompletionCommand, _ int) (DispatchResult, error) {
	f.completed = true
	v := f.invocation
	v.Status = InvocationSucceeded
	r := c.Response.Result
	u := c.Response.Usage
	f.result = DispatchResult{Invocation: v, Result: &r, Usage: &u}
	return f.result, nil
}
func (f *fakeStore) FailBeforeSend(context.Context, FailureCommand, int) (Invocation, error) {
	f.failed = true
	v := f.invocation
	v.Status = InvocationFailed
	return v, nil
}
func (f *fakeStore) FailAfterResponse(_ context.Context, command FailureCommand, _ int) (Invocation, error) {
	f.failed = true
	f.lastFailureUsage = command.Usage
	v := f.invocation
	v.Status = InvocationFailed
	return v, nil
}
func (f *fakeStore) MarkAmbiguous(context.Context, FailureCommand, string, int) (Invocation, error) {
	f.ambiguous = true
	v := f.invocation
	v.Status = InvocationAmbiguous
	return v, nil
}
func (f *fakeStore) RequestCancellation(context.Context, int64, string, string, int) (CancelResult, error) {
	return CancelResult{}, nil
}
func (f *fakeStore) CancellationRequested(context.Context, int64) (bool, error) {
	return f.invocation.CancelRequestedAt != nil, nil
}
func (f *fakeStore) MarkCancelled(context.Context, FailureCommand, int) (Invocation, error) {
	f.cancelled = true
	v := f.invocation
	v.Status = InvocationCancelled
	return v, nil
}
func (f *fakeStore) Reconcile(context.Context, string, int, int) (ReconcileResult, error) {
	return ReconcileResult{}, nil
}
func (f *fakeStore) WatchCancellation(ctx context.Context, _ int64) error {
	<-ctx.Done()
	return ctx.Err()
}

func fixturePrincipal() modeldispatch.ExecutionPrincipal {
	return modeldispatch.ExecutionPrincipal{ID: 21, OrganizationID: "explorarte", PrincipalKey: "test-principal", DispatchActorRoleID: "ingenieria_ia/code-runner", PrincipalKind: modeldispatch.PrincipalLocalProcess, Status: modeldispatch.PrincipalActive}
}
func fixtureAssignment(now time.Time) modeldispatch.DispatcherAssignment {
	return modeldispatch.DispatcherAssignment{ID: 31, OrganizationID: "explorarte", OrganizationRevisionID: 7, TaskID: 3, AttemptID: 4, SubjectRoleID: "ingenieria_ia/code-runner", DispatchActorRoleID: "ingenieria_ia/code-runner", ExecutionPrincipalID: 21, Status: modeldispatch.AssignmentActive, ValidFrom: now.Add(-time.Minute), ValidUntil: now.Add(time.Hour), MaxInvocations: 5, UsedInvocations: 0, AssignmentHash: SHA256Bytes([]byte("assignment"))}
}

func serviceFixture() (*fakeStore, fakeCatalog, fakeTaskReader, *fakeContextReader, fakeAssignmentResolver, fakePrincipalResolver, time.Time) {
	now := mustTime("2026-01-01T00:00:00Z")
	rendered := []byte("safe context")
	principal := fixturePrincipal()
	assignment := fixtureAssignment(now)
	resolved := modeldispatch.ResolvedAssignment{Assignment: assignment, Principal: principal}
	binding := ResolvedBinding{Binding: RoleBinding{Active: true}, Profile: Profile{ID: "worker-default", PolicyID: "department.worker"}, Version: ProfileVersion{ID: 9, ProfileID: "worker-default", ProviderID: "test.fake", ProviderModelID: "v1", Transport: TransportFake, AdapterStatus: AdapterAvailable, DispatchEnabled: true}, Provider: Provider{ID: "test.fake", AdapterStatus: AdapterAvailable, DispatchEnabled: true}, Capabilities: CapabilitySnapshot{Capabilities: []ModelCapability{"structured.output"}}}
	identityPrivateKey := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	identityPublicKey := identityPrivateKey.Public().(ed25519.PublicKey)
	identityPolicyHash := SHA256Bytes([]byte("identity-policy"))
	identityPolicy := modelidentity.ResolvedPolicy{Version: modelidentity.PolicyVersion{ID: 41, OrganizationID: "explorarte", PolicyID: "model-execution-identity", PolicyVersion: 1, CanonicalHash: identityPolicyHash, Algorithm: modelidentity.AlgorithmEd25519, ChallengeTTLSeconds: 60, ClockSkewSeconds: 15, Status: modelidentity.PolicyActive}}
	identityKey := modelidentity.ExecutionIdentityKey{ID: 51, OrganizationID: "explorarte", ExecutionPrincipalID: 21, KeyVersion: 1, Algorithm: modelidentity.AlgorithmEd25519, PublicKey: identityPublicKey, PublicKeyFingerprint: modelidentity.PublicKeyFingerprint(identityPublicKey), Status: modelidentity.KeyActive, ValidFrom: now.Add(-time.Hour)}
	inv := Invocation{ID: 11, OrganizationID: "explorarte", OrganizationRevisionID: 7, TaskID: 3, AttemptID: 4, DispatchActorRoleID: "ingenieria_ia/code-runner", SubjectRoleID: "ingenieria_ia/code-runner", DispatcherAssignmentID: int64Pointer(31), ExecutionPrincipalID: int64Pointer(21), ContextSnapshotID: 5, Purpose: "test", ModelProfileID: "worker-default", ModelProfileVersionID: 9, ProviderID: "test.fake", ProviderModelID: "v1", RequiredCapabilities: []ModelCapability{"structured.output"}, OutputMode: OutputJSON, OutputSchema: json.RawMessage(`{"type":"object","required":["ok"],"properties":{"ok":{"type":"boolean"}},"additionalProperties":false}`), MaxOutputTokens: 100, ThinkingMode: ThinkingDisabled, Deadline: now.Add(time.Hour), ModelEgressPolicyVersionID: int64Pointer(17), ModelEgressPolicyHash: SHA256Bytes([]byte("egress")), ExecutionIdentityPolicyVersionID: int64Pointer(41), ExecutionIdentityPolicyHash: identityPolicyHash, RequestHash: SHA256Bytes([]byte("request")), Status: InvocationRequested}
	store := &fakeStore{binding: binding, invocation: inv, identityPolicy: identityPolicy, identityKey: identityKey, identityPrivateKey: identityPrivateKey, claimed: ClaimedInvocation{Invocation: inv, DispatchAttempt: DispatchAttempt{ID: 13, InvocationID: 11, Status: DispatchClaimed, ClaimedBy: principal.PrincipalKey, ExecutionPrincipalID: int64Pointer(21)}, ClaimToken: "raw-token"}}
	catalog := fakeCatalog{org: OrganizationRef{ID: "explorarte", RevisionID: 7, ModelEgressPolicyHash: SHA256Bytes([]byte("egress")), CapabilityMatrixHash: SHA256Bytes([]byte("matrix"))}, role: RoleRef{ID: "ingenieria_ia/code-runner", ModelPolicy: "department.worker", Enabled: true, Executable: true, AuthorityClass: "execution_service"}}
	task := fakeTaskReader{ref: TaskAttemptRef{TaskID: 3, AttemptID: 4, OrganizationID: "explorarte", OrganizationRevisionID: 7, AssignedRoleID: "ingenieria_ia/code-runner", TaskStatus: "running", AttemptStatus: "running", LeaseHolderID: "test-worker", LeaseExpiresAt: now.Add(time.Hour)}}
	contexts := &fakeContextReader{ref: ContextSnapshotRef{ID: 5, OrganizationID: "explorarte", OrganizationRevisionID: 7, ActorRoleID: "ingenieria_ia/code-runner", TaskRef: "3", Status: "ready", RenderedHash: SHA256Bytes(rendered), DataClasses: []string{"organizational"}}, rendered: rendered}
	modelInput, err := PrepareModelInput(nil, contexts.ref, rendered)
	if err != nil {
		panic(err)
	}
	store.modelInput = modelInput
	assignments := fakeAssignmentResolver{resolved: resolved}
	principals := fakePrincipalResolver{principal: principal}
	return store, catalog, task, contexts, assignments, principals, now
}

func TestInvocationServiceCreatesFrozenInvocation(t *testing.T) {
	store, catalog, task, contexts, assignments, _, now := serviceFixture()
	service, err := NewInvocationService("explorarte", catalog, task, contexts, store, store, store, assignments, ClockFunc(func() time.Time { return now }), 10, false)
	if err != nil {
		t.Fatal(err)
	}
	command := CreateInvocationCommand{OrganizationID: "explorarte", TaskID: 3, AttemptID: 4, SubjectRoleID: "ingenieria_ia/code-runner", ContextSnapshotID: 5, Purpose: "test", RequiredCapabilities: []ModelCapability{"structured.output"}, OutputMode: OutputJSON, OutputSchema: json.RawMessage(`{"type":"object"}`), MaxOutputTokens: 100, ThinkingMode: ThinkingDisabled, IdempotencyKey: "create-1", Deadline: now.Add(time.Hour)}
	got, err := service.Create(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	if !store.created || got.Invocation.RequestHash == "" {
		t.Fatalf("invocation not prepared: %#v", got)
	}
	if store.prepared.EgressPolicy.Version.ID != 17 || store.prepared.EgressPolicy.CanonicalHash != SHA256Bytes([]byte("egress")) {
		t.Fatalf("egress policy was not pinned: %#v", store.prepared.EgressPolicy)
	}
}

func TestInvocationServicePersistsSuppliedModelInputBeforeDispatch(t *testing.T) {
	store, catalog, task, contexts, assignments, _, now := serviceFixture()
	service, err := NewInvocationService("explorarte", catalog, task, contexts, store, store, store, assignments, ClockFunc(func() time.Time { return now }), 10, false)
	if err != nil {
		t.Fatal(err)
	}
	input := &ModelInputEnvelope{
		SchemaVersion: ModelInputEnvelopeSchemaV1, ContextSnapshotID: contexts.ref.ID,
		CanonicalProjectionDigest: SHA256Bytes([]byte("run-1-turn-2")),
		StablePrefix:              []ModelInputMessage{{Role: ModelInputRoleUser, Content: string(contexts.rendered)}},
		VisibleHistory: []ModelInputMessage{
			{Role: ModelInputRoleAssistant, ToolCalls: []ModelInputToolCall{{ID: "call-1", Name: "lookup_fixture", Arguments: json.RawMessage(`{"id":"fixture"}`)}}},
			{Role: ModelInputRoleTool, ToolCallID: "call-1", ToolName: "lookup_fixture", Content: `{"value":"safe"}`},
		},
		ToolDefinitions: []ModelInputToolDefinition{{Name: "lookup_fixture", InputSchema: json.RawMessage(`{"type":"object"}`)}},
	}
	command := CreateInvocationCommand{OrganizationID: "explorarte", TaskID: 3, AttemptID: 4, SubjectRoleID: "ingenieria_ia/code-runner", ContextSnapshotID: 5, ModelInput: input, Purpose: "harness turn", RequiredCapabilities: []ModelCapability{"structured.output"}, OutputMode: OutputJSON, OutputSchema: json.RawMessage(`{"type":"object"}`), MaxOutputTokens: 100, ThinkingMode: ThinkingDisabled, IdempotencyKey: "harness-turn-2", Deadline: now.Add(time.Hour)}
	created, err := service.Create(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	if !store.created || store.prepared.ModelInput.Envelope.CanonicalProjectionDigest != input.CanonicalProjectionDigest || len(store.prepared.ModelInput.CanonicalBytes) == 0 {
		t.Fatalf("model input was not durably prepared with invocation: %+v", store.prepared.ModelInput)
	}
	legacyCommand := command
	legacyCommand.ModelInput = nil
	legacyCommand.IdempotencyKey = "legacy-turn"
	legacy, err := service.Create(context.Background(), legacyCommand)
	if err != nil {
		t.Fatal(err)
	}
	if created.Invocation.RequestHash == legacy.Invocation.RequestHash {
		t.Fatal("invocation request hash did not bind supplied model input")
	}
}

func TestInvocationServiceRejectsCredentialBearingModelInputBeforePersistence(t *testing.T) {
	secret := "sk-abcdefghijklmnopqrstuvwxyz123456"
	cases := []struct {
		name         string
		mutateInput  func(*ModelInputEnvelope)
		outputSchema json.RawMessage
	}{
		{name: "assistant content", mutateInput: func(input *ModelInputEnvelope) {
			input.VisibleHistory = []ModelInputMessage{{Role: ModelInputRoleAssistant, Content: "API_KEY=" + secret}}
		}},
		{name: "tool result", mutateInput: func(input *ModelInputEnvelope) {
			input.VisibleHistory = []ModelInputMessage{
				{Role: ModelInputRoleAssistant, ToolCalls: []ModelInputToolCall{{ID: "call-1", Name: "lookup_fixture", Arguments: json.RawMessage(`{"id":"fixture"}`)}}},
				{Role: ModelInputRoleTool, ToolCallID: "call-1", ToolName: "lookup_fixture", Content: "API_KEY=" + secret},
			}
		}},
		{name: "tool call arguments", mutateInput: func(input *ModelInputEnvelope) {
			input.VisibleHistory = []ModelInputMessage{{Role: ModelInputRoleAssistant, ToolCalls: []ModelInputToolCall{{ID: "call-1", Name: "lookup_fixture", Arguments: json.RawMessage(`{"api_key":"` + secret + `"}`)}}}}
		}},
		{name: "tool definition schema", mutateInput: func(input *ModelInputEnvelope) {
			input.ToolDefinitions = []ModelInputToolDefinition{{Name: "lookup_fixture", InputSchema: json.RawMessage(`{"type":"object","description":"API_KEY=` + secret + `"}`)}}
		}},
		{name: "explicit secret classification", mutateInput: func(input *ModelInputEnvelope) {
			input.InputClassifications = []string{string(modelegress.ClassificationSecret)}
		}},
		{name: "provider visible output schema", outputSchema: json.RawMessage(`{"type":"object","description":"API_KEY=` + secret + `"}`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, catalog, task, contexts, assignments, _, now := serviceFixture()
			service, err := NewInvocationService("explorarte", catalog, task, contexts, store, store, store, assignments, ClockFunc(func() time.Time { return now }), 10, false)
			if err != nil {
				t.Fatal(err)
			}
			input := &ModelInputEnvelope{
				SchemaVersion: ModelInputEnvelopeSchemaV1, ContextSnapshotID: contexts.ref.ID,
				CanonicalProjectionDigest: SHA256Bytes([]byte("credential-admission-" + tc.name)),
				StablePrefix:              []ModelInputMessage{{Role: ModelInputRoleUser, Content: string(contexts.rendered)}},
			}
			if tc.mutateInput != nil {
				tc.mutateInput(input)
			}
			outputSchema := tc.outputSchema
			if len(outputSchema) == 0 {
				outputSchema = json.RawMessage(`{"type":"object"}`)
			}
			command := CreateInvocationCommand{OrganizationID: "explorarte", TaskID: 3, AttemptID: 4, SubjectRoleID: "ingenieria_ia/code-runner", ContextSnapshotID: 5, ModelInput: input, Purpose: "credential admission", RequiredCapabilities: []ModelCapability{"structured.output"}, OutputMode: OutputJSON, OutputSchema: outputSchema, MaxOutputTokens: 100, ThinkingMode: ThinkingDisabled, IdempotencyKey: "credential-admission-" + tc.name, Deadline: now.Add(time.Hour)}
			provider := &deterministicAdapter{}
			_, err = service.Create(context.Background(), command)
			if !errors.Is(err, ErrModelInputSecretRejected) {
				t.Fatalf("credential-bearing input error=%v", err)
			}
			if store.created || len(store.prepared.ModelInput.CanonicalBytes) != 0 {
				t.Fatalf("credential-bearing input crossed durable admission: created=%t prepared_bytes=%d", store.created, len(store.prepared.ModelInput.CanonicalBytes))
			}
			if provider.calls != 0 {
				t.Fatalf("credential-bearing input reached provider: calls=%d", provider.calls)
			}
		})
	}
}

func TestDispatchDeniesCredentialIntroducedByDynamicModelInput(t *testing.T) {
	store, catalog, task, contexts, assignments, principals, now := serviceFixture()
	input := ModelInputEnvelope{
		SchemaVersion: ModelInputEnvelopeSchemaV1, ContextSnapshotID: contexts.ref.ID,
		CanonicalProjectionDigest: SHA256Bytes([]byte("turn-2-projection")),
		StablePrefix:              []ModelInputMessage{{Role: ModelInputRoleUser, Content: string(contexts.rendered)}},
		VisibleHistory:            []ModelInputMessage{{Role: ModelInputRoleAssistant, Content: "API_KEY=sk-abcdefghijklmnopqrstuvwxyz123456"}},
	}
	prepared, err := PrepareModelInput(&input, contexts.ref, contexts.rendered)
	if err != nil {
		t.Fatal(err)
	}
	store.modelInput = prepared
	provider := &deterministicAdapter{}
	service, err := NewDispatchService("explorarte", dispatchCfgWithCostGate(), catalog, task, contexts, fakeEvaluator{allow: true}, store, modelegress.NewEvaluator(), store, principals, assignments, store, store, fakeAdapterRegistry{value: provider}, ClockFunc(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Dispatch(context.Background(), 11)
	if !errors.Is(err, ErrEgressDenied) {
		t.Fatalf("dynamic credential was not denied: %v", err)
	}
	if provider.calls != 0 || contexts.renderCalls != 0 {
		t.Fatalf("credential denial crossed pre-send boundary: provider_calls=%d render_calls=%d", provider.calls, contexts.renderCalls)
	}
	if store.denyEvaluation == nil || !containsModelInputClass(store.denyEvaluation.ContextClassifications, string(modelegress.ClassificationSecret)) {
		t.Fatalf("effective egress evidence omitted dynamic secret: %+v", store.denyEvaluation)
	}
}

func TestDispatchDeniesCredentialInProviderVisibleOutputContract(t *testing.T) {
	store, catalog, task, contexts, assignments, principals, now := serviceFixture()
	store.invocation.OutputSchema = json.RawMessage(`{"type":"object","description":"API_KEY=sk-abcdefghijklmnopqrstuvwxyz123456"}`)
	provider := &deterministicAdapter{}
	service, err := NewDispatchService("explorarte", dispatchCfgWithCostGate(), catalog, task, contexts, fakeEvaluator{allow: true}, store, modelegress.NewEvaluator(), store, principals, assignments, store, store, fakeAdapterRegistry{value: provider}, ClockFunc(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Dispatch(context.Background(), 11)
	if !errors.Is(err, ErrEgressDenied) || provider.calls != 0 || contexts.renderCalls != 0 {
		t.Fatalf("provider-visible output contract bypassed secret egress: err=%v provider_calls=%d render_calls=%d", err, provider.calls, contexts.renderCalls)
	}
}

func TestInvocationServiceRejectsMalformedEgressPolicyMaterialization(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*modelegress.ResolvedPolicy)
	}{
		{name: "unmaterialized", mutate: func(policy *modelegress.ResolvedPolicy) { policy.Version.Status = "materializing" }},
		{name: "wrong organization", mutate: func(policy *modelegress.ResolvedPolicy) { policy.Version.OrganizationID = "other" }},
		{name: "wrong revision", mutate: func(policy *modelegress.ResolvedPolicy) { policy.OrganizationRevisionID = 8 }},
		{name: "version hash mismatch", mutate: func(policy *modelegress.ResolvedPolicy) { policy.Version.CanonicalHash = SHA256Bytes([]byte("other")) }},
		{name: "allow default", mutate: func(policy *modelegress.ResolvedPolicy) { policy.DefaultAction = modelegress.EffectAllow }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			store, catalog, task, contexts, assignments, _, now := serviceFixture()
			policy := fixtureEgressPolicy()
			test.mutate(&policy)
			store.egressPolicy = &policy
			service, err := NewInvocationService("explorarte", catalog, task, contexts, store, store, store, assignments, ClockFunc(func() time.Time { return now }), 10, false)
			if err != nil {
				t.Fatal(err)
			}
			command := CreateInvocationCommand{OrganizationID: "explorarte", TaskID: 3, AttemptID: 4, SubjectRoleID: "ingenieria_ia/code-runner", ContextSnapshotID: 5, Purpose: "test", RequiredCapabilities: []ModelCapability{"structured.output"}, OutputMode: OutputJSON, OutputSchema: json.RawMessage(`{"type":"object"}`), MaxOutputTokens: 100, ThinkingMode: ThinkingDisabled, IdempotencyKey: "malformed-egress", Deadline: now.Add(time.Hour)}
			if _, err = service.Create(context.Background(), command); !errors.Is(err, ErrEgressPolicyUnpinned) {
				t.Fatalf("error=%v", err)
			}
			if store.created {
				t.Fatal("invocation was created with malformed egress materialization")
			}
		})
	}
}

func TestDispatchFakeSuccessDoesNotCompleteTask(t *testing.T) {
	store, catalog, task, contexts, assignments, principals, now := serviceFixture()
	cfg := RuntimeConfig{Enabled: true, CommandTimeout: time.Minute, GlobalConcurrency: 1, MaxResponseBytes: 1024, MaxToolIntents: 2, ClaimTTL: time.Minute, ReconcileBatchSize: 10, OutboxMaxAttempts: 10, ExecutionPrincipalKey: "test-principal", ExecutionIdentityEnabled: true, ExecutionIdentityKeyFile: "test://identity"}
	providerAdapter := &deterministicAdapter{}
	service, err := NewDispatchService("explorarte", cfg, catalog, task, contexts, fakeEvaluator{allow: true}, store, modelegress.NewEvaluator(), store, principals, assignments, store, store, fakeAdapterRegistry{value: providerAdapter}, ClockFunc(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	got, err := service.Dispatch(context.Background(), 11)
	if err != nil {
		t.Fatal(err)
	}
	if !store.completed || got.Invocation.Status != InvocationSucceeded || providerAdapter.calls != 1 {
		t.Fatalf("dispatch did not complete exactly once: result=%#v calls=%d", got, providerAdapter.calls)
	}
	if store.allowEvaluation == nil || store.allowEvaluation.AuthorizationEffect != modelegress.AuthorizationAllow || store.allowEvaluation.EgressEffect != modelegress.EffectAllow {
		t.Fatalf("allow evaluation was not persisted: %#v", store.allowEvaluation)
	}
	body, _ := json.Marshal(got)
	if bytesContains(body, []byte("do not persist")) {
		t.Fatal("hidden reasoning leaked")
	}
}
func TestDispatchAuthorizationDenyFailsBeforeSend(t *testing.T) {
	store, catalog, task, contexts, assignments, principals, now := serviceFixture()
	cfg := RuntimeConfig{Enabled: true, CommandTimeout: time.Minute, GlobalConcurrency: 1, MaxResponseBytes: 1024, MaxToolIntents: 2, ClaimTTL: time.Minute, ReconcileBatchSize: 10, OutboxMaxAttempts: 10, ExecutionPrincipalKey: "test-principal", ExecutionIdentityEnabled: true, ExecutionIdentityKeyFile: "test://identity"}
	service, _ := NewDispatchService("explorarte", cfg, catalog, task, contexts, fakeEvaluator{allow: false}, store, modelegress.NewEvaluator(), store, principals, assignments, store, store, fakeAdapterRegistry{value: &deterministicAdapter{}}, ClockFunc(func() time.Time { return now }))
	_, err := service.Dispatch(context.Background(), 11)
	if !errors.Is(err, ErrAuthorizationDenied) || !store.failed || contexts.renderCalls != 0 {
		t.Fatalf("expected denied pre-send failure without render, err=%v render_calls=%d", err, contexts.renderCalls)
	}
	if store.denyEvaluation == nil || store.denyEvaluation.AuthorizationEffect != modelegress.AuthorizationDeny || store.denyEvaluation.EgressEffect != modelegress.EffectNotEvaluated {
		t.Fatalf("authorization denial was not durably classified: %#v", store.denyEvaluation)
	}
}
func TestDispatchAdapterFailureIsAmbiguous(t *testing.T) {
	store, catalog, task, contexts, assignments, principals, now := serviceFixture()
	cfg := RuntimeConfig{Enabled: true, CommandTimeout: time.Minute, GlobalConcurrency: 1, MaxResponseBytes: 1024, MaxToolIntents: 2, ClaimTTL: time.Minute, ReconcileBatchSize: 10, OutboxMaxAttempts: 10, ExecutionPrincipalKey: "test-principal", ExecutionIdentityEnabled: true, ExecutionIdentityKeyFile: "test://identity"}
	service, _ := NewDispatchService("explorarte", cfg, catalog, task, contexts, fakeEvaluator{allow: true}, store, modelegress.NewEvaluator(), store, principals, assignments, store, store, fakeAdapterRegistry{value: &deterministicAdapter{err: errors.New("unknown after send")}}, ClockFunc(func() time.Time { return now }))
	_, err := service.Dispatch(context.Background(), 11)
	if !errors.Is(err, ErrAmbiguousOutcome) || !store.ambiguous {
		t.Fatalf("expected ambiguous outcome, got %v", err)
	}
}

func TestDispatchAdapterPreflightFailureDoesNotRenderOrCommitSend(t *testing.T) {
	store, catalog, task, contexts, assignments, principals, now := serviceFixture()
	outcome := ProviderOutcome{OutcomeClassification: ProviderOutcomeNotSent, ErrorClass: "circuit_breaker", ErrorCode: "circuit_open", ResponseSchemaVersion: "test.fake.response.v1"}
	provider := &deterministicAdapter{preflightErr: &AdapterError{Phase: AdapterFailureBeforeRequest, Outcome: outcome, Cause: ErrProviderUnavailable}}
	cfg := RuntimeConfig{Enabled: true, CommandTimeout: time.Minute, GlobalConcurrency: 1, MaxResponseBytes: 1024, MaxToolIntents: 2, ClaimTTL: time.Minute, ReconcileBatchSize: 10, OutboxMaxAttempts: 10, ExecutionPrincipalKey: "test-principal", ExecutionIdentityEnabled: true, ExecutionIdentityKeyFile: "test://identity"}
	service, _ := NewDispatchService("explorarte", cfg, catalog, task, contexts, fakeEvaluator{allow: true}, store, modelegress.NewEvaluator(), store, principals, assignments, store, store, fakeAdapterRegistry{value: provider}, ClockFunc(func() time.Time { return now }))
	_, err := service.Dispatch(context.Background(), 11)
	if !errors.Is(err, ErrProviderUnavailable) || contexts.renderCalls != 0 || provider.calls != 0 || !store.failed || store.allowEvaluation != nil {
		t.Fatalf("preflight failure err=%v render=%d calls=%d failed=%v allow=%+v", err, contexts.renderCalls, provider.calls, store.failed, store.allowEvaluation)
	}
}

func TestDispatchClassifiedProviderFailuresUseKnownTerminalPaths(t *testing.T) {
	cases := []struct {
		name          string
		adapterError  *AdapterError
		wantRejected  bool
		wantNotSent   bool
		wantAmbiguous bool
		wantError     error
	}{
		{name: "request not sent", adapterError: &AdapterError{Phase: AdapterFailureBeforeRequest, Outcome: ProviderOutcome{OutcomeClassification: ProviderOutcomeNotSent, ErrorClass: "credential", ErrorCode: "credential_unavailable", ResponseSchemaVersion: "test.fake.response.v1"}, Cause: errors.New("credential unavailable")}, wantNotSent: true},
		{name: "provider rejection", adapterError: &AdapterError{Phase: AdapterFailureResponseReceived, Outcome: ProviderOutcome{OutcomeClassification: ProviderOutcomeRejected, ProviderRequestID: "provider-1", HTTPStatus: 429, ErrorClass: "rate_limit", ErrorCode: "rate_limited", Retryable: true, ResponseHash: SHA256Bytes([]byte("rejection")), ResponseSchemaVersion: "test.fake.response.v1"}, Cause: ErrResponseRejected}, wantRejected: true, wantError: ErrResponseRejected},
		{name: "ambiguous transport", adapterError: &AdapterError{Phase: AdapterFailureAmbiguous, Outcome: ProviderOutcome{OutcomeClassification: ProviderOutcomeAmbiguous, ErrorClass: "transport", ErrorCode: "transport_timeout", Retryable: true, ResponseSchemaVersion: "test.fake.response.v1"}, Cause: context.DeadlineExceeded}, wantAmbiguous: true, wantError: ErrAmbiguousOutcome},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			store, catalog, task, contexts, assignments, principals, now := serviceFixture()
			provider := &deterministicAdapter{err: test.adapterError}
			cfg := RuntimeConfig{Enabled: true, CommandTimeout: time.Minute, GlobalConcurrency: 1, MaxResponseBytes: 1024, MaxToolIntents: 2, ClaimTTL: time.Minute, ReconcileBatchSize: 10, OutboxMaxAttempts: 10, ExecutionPrincipalKey: "test-principal", ExecutionIdentityEnabled: true, ExecutionIdentityKeyFile: "test://identity"}
			service, _ := NewDispatchService("explorarte", cfg, catalog, task, contexts, fakeEvaluator{allow: true}, store, modelegress.NewEvaluator(), store, principals, assignments, store, store, fakeAdapterRegistry{value: provider}, ClockFunc(func() time.Time { return now }))
			_, err := service.Dispatch(context.Background(), 11)
			if test.wantError != nil && !errors.Is(err, test.wantError) {
				t.Fatalf("error=%v want=%v", err, test.wantError)
			}
			if provider.calls != 1 || store.allowEvaluation == nil || store.providerRejected != test.wantRejected || store.providerNotSent != test.wantNotSent || store.ambiguous != test.wantAmbiguous {
				t.Fatalf("calls=%d allow=%+v rejected=%v not_sent=%v ambiguous=%v err=%v", provider.calls, store.allowEvaluation, store.providerRejected, store.providerNotSent, store.ambiguous, err)
			}
		})
	}
}

func TestDispatchAuthorizationOperationalErrorDoesNotBecomePolicyDeny(t *testing.T) {
	store, catalog, task, contexts, assignments, principals, now := serviceFixture()
	cfg := RuntimeConfig{Enabled: true, CommandTimeout: time.Minute, GlobalConcurrency: 1, MaxResponseBytes: 1024, MaxToolIntents: 2, ClaimTTL: time.Minute, ReconcileBatchSize: 10, OutboxMaxAttempts: 10, ExecutionPrincipalKey: "test-principal", ExecutionIdentityEnabled: true, ExecutionIdentityKeyFile: "test://identity"}
	service, _ := NewDispatchService("explorarte", cfg, catalog, task, contexts, fakeEvaluator{err: context.DeadlineExceeded}, store, modelegress.NewEvaluator(), store, principals, assignments, store, store, fakeAdapterRegistry{value: &deterministicAdapter{}}, ClockFunc(func() time.Time { return now }))
	_, err := service.Dispatch(context.Background(), 11)
	if !errors.Is(err, context.DeadlineExceeded) || contexts.renderCalls != 0 || store.denyEvaluation == nil {
		t.Fatalf("operational error handling err=%v render_calls=%d evaluation=%#v", err, contexts.renderCalls, store.denyEvaluation)
	}
	if store.denyEvaluation.AuthorizationEffect != modelegress.AuthorizationError || store.denyEvaluation.EgressEffect != modelegress.EffectNotEvaluated {
		t.Fatalf("operational error lost classification: %#v", store.denyEvaluation)
	}
}

func TestDispatchEgressOperationalErrorIsDurableAndDoesNotRender(t *testing.T) {
	store, catalog, task, contexts, assignments, principals, now := serviceFixture()
	providerAdapter := &deterministicAdapter{}
	cfg := RuntimeConfig{Enabled: true, CommandTimeout: time.Minute, GlobalConcurrency: 1, MaxResponseBytes: 1024, MaxToolIntents: 2, ClaimTTL: time.Minute, ReconcileBatchSize: 10, OutboxMaxAttempts: 10, ExecutionPrincipalKey: "test-principal", ExecutionIdentityEnabled: true, ExecutionIdentityKeyFile: "test://identity"}
	service, err := NewDispatchService("explorarte", cfg, catalog, task, contexts, fakeEvaluator{allow: true}, store, failingEgressEvaluator{err: errors.New("egress store unavailable")}, store, principals, assignments, store, store, fakeAdapterRegistry{value: providerAdapter}, ClockFunc(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Dispatch(context.Background(), 11)
	if !errors.Is(err, ErrEgressEvaluation) || contexts.renderCalls != 0 || providerAdapter.calls != 0 {
		t.Fatalf("egress error leaked to render/adapter: err=%v render=%d calls=%d", err, contexts.renderCalls, providerAdapter.calls)
	}
	if store.denyEvaluation == nil || store.denyEvaluation.AuthorizationEffect != modelegress.AuthorizationAllow || store.denyEvaluation.EgressEffect != modelegress.EffectError {
		t.Fatalf("egress error not durable: %#v", store.denyEvaluation)
	}
}

func TestDispatchEgressDenyDoesNotRenderOrCallAdapter(t *testing.T) {
	store, catalog, task, contexts, assignments, principals, now := serviceFixture()
	contexts.ref.DataClasses = []string{"public", "clinical"}
	providerAdapter := &deterministicAdapter{}
	cfg := RuntimeConfig{Enabled: true, CommandTimeout: time.Minute, GlobalConcurrency: 1, MaxResponseBytes: 1024, MaxToolIntents: 2, ClaimTTL: time.Minute, ReconcileBatchSize: 10, OutboxMaxAttempts: 10, ExecutionPrincipalKey: "test-principal", ExecutionIdentityEnabled: true, ExecutionIdentityKeyFile: "test://identity"}
	service, _ := NewDispatchService("explorarte", cfg, catalog, task, contexts, fakeEvaluator{allow: true}, store, modelegress.NewEvaluator(), store, principals, assignments, store, store, fakeAdapterRegistry{value: providerAdapter}, ClockFunc(func() time.Time { return now }))
	_, err := service.Dispatch(context.Background(), 11)
	if !errors.Is(err, ErrEgressDenied) || contexts.renderCalls != 0 || providerAdapter.calls != 0 {
		t.Fatalf("egress denial leaked to render/adapter: err=%v render=%d calls=%d", err, contexts.renderCalls, providerAdapter.calls)
	}
	if store.denyEvaluation == nil || store.denyEvaluation.EgressEffect != modelegress.EffectDeny {
		t.Fatalf("egress denial not durable: %#v", store.denyEvaluation)
	}
}

func TestDispatchAdapterUnavailableDoesNotRender(t *testing.T) {
	store, catalog, task, contexts, assignments, principals, now := serviceFixture()
	cfg := RuntimeConfig{Enabled: true, CommandTimeout: time.Minute, GlobalConcurrency: 1, MaxResponseBytes: 1024, MaxToolIntents: 2, ClaimTTL: time.Minute, ReconcileBatchSize: 10, OutboxMaxAttempts: 10, ExecutionPrincipalKey: "test-principal", ExecutionIdentityEnabled: true, ExecutionIdentityKeyFile: "test://identity"}
	service, _ := NewDispatchService("explorarte", cfg, catalog, task, contexts, fakeEvaluator{allow: true}, store, modelegress.NewEvaluator(), store, principals, assignments, store, store, fakeAdapterRegistry{}, ClockFunc(func() time.Time { return now }))
	_, err := service.Dispatch(context.Background(), 11)
	if !errors.Is(err, ErrProviderUnavailable) || contexts.renderCalls != 0 {
		t.Fatalf("adapter unavailable rendered context: err=%v render=%d", err, contexts.renderCalls)
	}
	if store.allowEvaluation != nil || store.denyEvaluation != nil {
		t.Fatalf("adapter failure persisted a decision outside the send_started transaction: allow=%#v deny=%#v", store.allowEvaluation, store.denyEvaluation)
	}
}

func TestDispatchRenderFailureOccursAfterInMemoryDecisionsAndBeforeAdapter(t *testing.T) {
	store, catalog, task, contexts, assignments, principals, now := serviceFixture()
	contexts.renderErr = errors.New("render unavailable")
	provider := &deterministicAdapter{}
	cfg := RuntimeConfig{Enabled: true, CommandTimeout: time.Minute, GlobalConcurrency: 1, MaxResponseBytes: 1024, MaxToolIntents: 2, ClaimTTL: time.Minute, ReconcileBatchSize: 10, OutboxMaxAttempts: 10, ExecutionPrincipalKey: "test-principal", ExecutionIdentityEnabled: true, ExecutionIdentityKeyFile: "test://identity"}
	service, _ := NewDispatchService("explorarte", cfg, catalog, task, contexts, fakeEvaluator{allow: true}, store, modelegress.NewEvaluator(), store, principals, assignments, store, store, fakeAdapterRegistry{value: provider}, ClockFunc(func() time.Time { return now }))
	_, err := service.Dispatch(context.Background(), 11)
	if err == nil || contexts.renderCalls != 1 || provider.calls != 0 || !store.failed {
		t.Fatalf("render failure err=%v render=%d adapter=%d failed=%v", err, contexts.renderCalls, provider.calls, store.failed)
	}
	if store.allowEvaluation != nil || store.denyEvaluation != nil {
		t.Fatalf("render failure persisted allow outside send_started: allow=%#v deny=%#v", store.allowEvaluation, store.denyEvaluation)
	}
}

func TestDispatchLegacyUnpinnedDoesNotRenderOrCallAdapter(t *testing.T) {
	store, catalog, task, contexts, assignments, principals, now := serviceFixture()
	store.invocation.ModelEgressPolicyVersionID = nil
	store.invocation.ModelEgressPolicyHash = ""
	// A genuine pre-Branch-11 row has no execution-identity policy pin. Keep
	// the fixture internally consistent so the legacy terminalization path does
	// not exercise the authenticated-claim boundary.
	store.invocation.ExecutionIdentityPolicyVersionID = nil
	store.invocation.ExecutionIdentityPolicyHash = ""
	store.claimed.Invocation = store.invocation
	providerAdapter := &deterministicAdapter{}
	cfg := RuntimeConfig{Enabled: true, CommandTimeout: time.Minute, GlobalConcurrency: 1, MaxResponseBytes: 1024, MaxToolIntents: 2, ClaimTTL: time.Minute, ReconcileBatchSize: 10, OutboxMaxAttempts: 10, ExecutionPrincipalKey: "test-principal", ExecutionIdentityEnabled: true, ExecutionIdentityKeyFile: "test://identity"}
	service, _ := NewDispatchService("explorarte", cfg, catalog, task, contexts, fakeEvaluator{allow: true}, store, modelegress.NewEvaluator(), store, principals, assignments, store, store, fakeAdapterRegistry{value: providerAdapter}, ClockFunc(func() time.Time { return now }))
	_, err := service.Dispatch(context.Background(), 11)
	if !errors.Is(err, ErrEgressPolicyUnpinned) || contexts.renderCalls != 0 || providerAdapter.calls != 0 || !store.failed {
		t.Fatalf("legacy unpinned dispatch was not blocked: err=%v render=%d calls=%d failed=%v", err, contexts.renderCalls, providerAdapter.calls, store.failed)
	}
}

func TestDispatchStillRequiresAssignmentAndActiveLease(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*fakeTaskReader, time.Time)
	}{
		{"foreign assignment", func(task *fakeTaskReader, _ time.Time) { task.ref.AssignedRoleID = "ingenieria_ia/frontend" }},
		{"expired lease", func(task *fakeTaskReader, now time.Time) { task.ref.LeaseExpiresAt = now.Add(-time.Second) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, catalog, task, contexts, assignments, principals, now := serviceFixture()
			tc.mutate(&task, now)
			adapter := &deterministicAdapter{}
			cfg := RuntimeConfig{Enabled: true, CommandTimeout: time.Minute, GlobalConcurrency: 1, MaxResponseBytes: 1024, MaxToolIntents: 2, ClaimTTL: time.Minute, ReconcileBatchSize: 10, OutboxMaxAttempts: 10, ExecutionPrincipalKey: "test-principal", ExecutionIdentityEnabled: true, ExecutionIdentityKeyFile: "test://identity"}
			service, _ := NewDispatchService("explorarte", cfg, catalog, task, contexts, fakeEvaluator{allow: true}, store, modelegress.NewEvaluator(), store, principals, assignments, store, store, fakeAdapterRegistry{value: adapter}, ClockFunc(func() time.Time { return now }))
			_, err := service.Dispatch(context.Background(), 11)
			if !errors.Is(err, ErrTaskAttemptRejected) || contexts.renderCalls != 0 || adapter.calls != 0 {
				t.Fatalf("task invariant failure err=%v render=%d adapter=%d", err, contexts.renderCalls, adapter.calls)
			}
		})
	}
}

func bytesContains(body, needle []byte) bool {
	for i := 0; i+len(needle) <= len(body); i++ {
		match := true
		for j := range needle {
			if body[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func TestDispatchMalformedResponseFailsAfterKnownResponse(t *testing.T) {
	store, catalog, task, contexts, assignments, principals, now := serviceFixture()
	cfg := RuntimeConfig{Enabled: true, CommandTimeout: time.Minute, GlobalConcurrency: 1, MaxResponseBytes: 1024, MaxToolIntents: 2, ClaimTTL: time.Minute, ReconcileBatchSize: 10, OutboxMaxAttempts: 10, ExecutionPrincipalKey: "test-principal", ExecutionIdentityEnabled: true, ExecutionIdentityKeyFile: "test://identity"}
	response := RawResponse{Content: []byte(`{"ok":"wrong"}`), ProviderRequestID: "fake-bad"}
	service, _ := NewDispatchService("explorarte", cfg, catalog, task, contexts, fakeEvaluator{allow: true}, store, modelegress.NewEvaluator(), store, principals, assignments, store, store, fakeAdapterRegistry{value: &deterministicAdapter{response: &response}}, ClockFunc(func() time.Time { return now }))
	_, err := service.Dispatch(context.Background(), 11)
	if !errors.Is(err, ErrResponseRejected) || !store.failed || store.ambiguous {
		t.Fatalf("expected terminal known-response failure, got err=%v failed=%v ambiguous=%v", err, store.failed, store.ambiguous)
	}
}

func TestDispatchCancellationRequiresDurableRequest(t *testing.T) {
	store, catalog, task, contexts, assignments, principals, now := serviceFixture()
	cfg := RuntimeConfig{Enabled: true, CommandTimeout: time.Minute, GlobalConcurrency: 1, MaxResponseBytes: 1024, MaxToolIntents: 2, ClaimTTL: time.Minute, ReconcileBatchSize: 10, OutboxMaxAttempts: 10, ExecutionPrincipalKey: "test-principal", ExecutionIdentityEnabled: true, ExecutionIdentityKeyFile: "test://identity"}
	response := RawResponse{CancellationConfirmed: true}
	service, _ := NewDispatchService("explorarte", cfg, catalog, task, contexts, fakeEvaluator{allow: true}, store, modelegress.NewEvaluator(), store, principals, assignments, store, store, fakeAdapterRegistry{value: &deterministicAdapter{response: &response, err: context.Canceled}}, ClockFunc(func() time.Time { return now }))
	_, err := service.Dispatch(context.Background(), 11)
	if !errors.Is(err, ErrAmbiguousOutcome) || store.cancelled {
		t.Fatalf("cancellation without durable request was accepted: err=%v", err)
	}

	requestedAt := now
	store, catalog, task, contexts, assignments, principals, now = serviceFixture()
	store.invocation.CancelRequestedAt = &requestedAt
	store.claimed.Invocation.CancelRequestedAt = &requestedAt
	service, _ = NewDispatchService("explorarte", cfg, catalog, task, contexts, fakeEvaluator{allow: true}, store, modelegress.NewEvaluator(), store, principals, assignments, store, store, fakeAdapterRegistry{value: &deterministicAdapter{response: &response, err: context.Canceled}}, ClockFunc(func() time.Time { return now }))
	_, err = service.Dispatch(context.Background(), 11)
	if !errors.Is(err, ErrCancellationRequested) || !store.cancelled {
		t.Fatalf("durable confirmed cancellation not persisted: err=%v", err)
	}
}

func TestInvocationCancellationRejectsDifferentActor(t *testing.T) {
	store, catalog, task, contexts, assignments, _, now := serviceFixture()
	service, err := NewInvocationService("explorarte", catalog, task, contexts, store, store, store, assignments, ClockFunc(func() time.Time { return now }), 10, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Cancel(context.Background(), 11, "ingenieria_ia/frontend", "stop"); !errors.Is(err, ErrAuthorizationDenied) {
		t.Fatalf("expected actor denial, got %v", err)
	}
}

func TestDispatchDisabledDoesNotClaim(t *testing.T) {
	store, catalog, task, contexts, assignments, principals, now := serviceFixture()
	cfg := RuntimeConfig{Enabled: false, CommandTimeout: time.Minute, GlobalConcurrency: 1, MaxResponseBytes: 1024, MaxToolIntents: 2, ClaimTTL: time.Minute, ReconcileBatchSize: 10, OutboxMaxAttempts: 10, ExecutionPrincipalKey: "test-principal", ExecutionIdentityEnabled: true, ExecutionIdentityKeyFile: "test://identity"}
	service, err := NewDispatchService("explorarte", cfg, catalog, task, contexts, fakeEvaluator{allow: true}, store, modelegress.NewEvaluator(), store, principals, assignments, store, store, fakeAdapterRegistry{value: &deterministicAdapter{}}, ClockFunc(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Dispatch(context.Background(), 11); !errors.Is(err, ErrDisabled) {
		t.Fatalf("expected disabled dispatch, got %v", err)
	}
}

// --- P0 financial state machine tests -------------------------------------
//
// These cover the AdapterFailureResponseReceived release bug (the deferred
// Release in Dispatch fired unconditionally for this phase because
// reservationSettled was never set) and the design each case now maps to:
// exactly one of Reconcile/Release/MarkPendingReconciliation must fire, and
// a `failed` invocation with real recovered usage must still be committed
// as `actual`, not released as `released_not_sent` and not left unexplained
// as `estimated_pending_reconciliation`.

func dispatchCfgWithCostGate() RuntimeConfig {
	return RuntimeConfig{Enabled: true, CommandTimeout: time.Minute, GlobalConcurrency: 1, MaxResponseBytes: 1024, MaxToolIntents: 2, ClaimTTL: time.Minute, ReconcileBatchSize: 10, OutboxMaxAttempts: 10, ExecutionPrincipalKey: "test-principal", ExecutionIdentityEnabled: true, ExecutionIdentityKeyFile: "test://identity"}
}

func TestDispatchBeforeRequestFailureReleasesReservation(t *testing.T) {
	store, catalog, task, contexts, assignments, principals, now := serviceFixture()
	gate := &fakeCostBudgetGate{}
	outcome := ProviderOutcome{OutcomeClassification: ProviderOutcomeNotSent, ErrorClass: "request_encoding", ErrorCode: "request_encoding_failed", ResponseSchemaVersion: "test.fake.response.v1"}
	provider := &deterministicAdapter{err: &AdapterError{Phase: AdapterFailureBeforeRequest, Outcome: outcome, Cause: ErrProviderUnavailable}}
	service, _ := NewDispatchService("explorarte", dispatchCfgWithCostGate(), catalog, task, contexts, fakeEvaluator{allow: true}, store, modelegress.NewEvaluator(), store, principals, assignments, store, store, fakeAdapterRegistry{value: provider}, ClockFunc(func() time.Time { return now }), WithCostBudgetGate(gate))
	_, err := service.Dispatch(context.Background(), 11)
	if err == nil || !store.providerNotSent {
		t.Fatalf("expected before-request failure, err=%v not_sent=%v", err, store.providerNotSent)
	}
	if gate.releaseCalls != 1 || gate.pendingCalls != 0 || len(gate.reconcileCalls) != 0 {
		t.Fatalf("expected exactly one Release, got release=%d pending=%d reconcile=%d", gate.releaseCalls, gate.pendingCalls, len(gate.reconcileCalls))
	}
}

func TestDispatchSuccessCommitsActualUsageRegression(t *testing.T) {
	store, catalog, task, contexts, assignments, principals, now := serviceFixture()
	gate := &fakeCostBudgetGate{}
	provider := &deterministicAdapter{}
	service, _ := NewDispatchService("explorarte", dispatchCfgWithCostGate(), catalog, task, contexts, fakeEvaluator{allow: true}, store, modelegress.NewEvaluator(), store, principals, assignments, store, store, fakeAdapterRegistry{value: provider}, ClockFunc(func() time.Time { return now }), WithCostBudgetGate(gate))
	if _, err := service.Dispatch(context.Background(), 11); err != nil {
		t.Fatal(err)
	}
	if !store.completed {
		t.Fatal("expected the invocation to complete")
	}
	if len(gate.reconcileCalls) != 1 || gate.reconcileCalls[0].inputTokens != 2 || gate.reconcileCalls[0].outputTokens != 3 {
		t.Fatalf("expected one Reconcile(2,_,3), got %+v", gate.reconcileCalls)
	}
	if gate.releaseCalls != 0 || gate.pendingCalls != 0 {
		t.Fatalf("success must never release or park a reservation: release=%d pending=%d", gate.releaseCalls, gate.pendingCalls)
	}
}

func TestDispatchResponseReceivedWithRecoveredUsageCommitsActualEvenThoughFailed(t *testing.T) {
	store, catalog, task, contexts, assignments, principals, now := serviceFixture()
	gate := &fakeCostBudgetGate{}
	response := RawResponse{ProviderReported: true, InputTokens: 41, OutputTokens: 7, ProviderRequestID: "fake-partial"}
	outcome := ProviderOutcome{OutcomeClassification: ProviderOutcomeRejected, ProviderRequestID: "fake-partial", HTTPStatus: 200, ErrorClass: "response", ErrorCode: "response_content_invalid", ResponseHash: SHA256Bytes([]byte("x")), ResponseSchemaVersion: "test.fake.response.v1"}
	provider := &deterministicAdapter{response: &response, err: &AdapterError{Phase: AdapterFailureResponseReceived, Outcome: outcome, Cause: ErrResponseRejected}}
	service, _ := NewDispatchService("explorarte", dispatchCfgWithCostGate(), catalog, task, contexts, fakeEvaluator{allow: true}, store, modelegress.NewEvaluator(), store, principals, assignments, store, store, fakeAdapterRegistry{value: provider}, ClockFunc(func() time.Time { return now }), WithCostBudgetGate(gate))
	_, err := service.Dispatch(context.Background(), 11)
	if err == nil || !store.providerRejected {
		t.Fatalf("expected a rejected-provider-response failure, err=%v rejected=%v", err, store.providerRejected)
	}
	if len(gate.reconcileCalls) != 1 || gate.reconcileCalls[0].inputTokens != 41 || gate.reconcileCalls[0].outputTokens != 7 {
		t.Fatalf("expected the recovered usage to be committed as actual, got reconcile=%+v", gate.reconcileCalls)
	}
	if gate.releaseCalls != 0 || gate.pendingCalls != 0 {
		t.Fatalf("a call with recovered usage must be committed, never released or merely parked: release=%d pending=%d", gate.releaseCalls, gate.pendingCalls)
	}
	if store.lastFailureUsage == nil || store.lastFailureUsage.InputTokens != 41 || store.lastFailureUsage.OutputTokens != 7 {
		t.Fatalf("expected the failed invocation's usage row to be threaded through, got %+v", store.lastFailureUsage)
	}
}

func TestDispatchResponseReceivedWithoutUsageIsParkedNotReleased(t *testing.T) {
	store, catalog, task, contexts, assignments, principals, now := serviceFixture()
	gate := &fakeCostBudgetGate{}
	// ProviderReported=false: this is what the deepseek adapter now returns
	// for response_read_failed/response_json_invalid -- no usage object was
	// ever decoded, so nothing is recoverable.
	response := RawResponse{ProviderReported: false, ProviderRequestID: "fake-unreadable"}
	outcome := ProviderOutcome{OutcomeClassification: ProviderOutcomeRejected, ProviderRequestID: "fake-unreadable", HTTPStatus: 200, ErrorClass: "response", ErrorCode: "response_json_invalid", ResponseHash: SHA256Bytes([]byte("x")), ResponseSchemaVersion: "test.fake.response.v1"}
	provider := &deterministicAdapter{response: &response, err: &AdapterError{Phase: AdapterFailureResponseReceived, Outcome: outcome, Cause: ErrResponseRejected}}
	service, _ := NewDispatchService("explorarte", dispatchCfgWithCostGate(), catalog, task, contexts, fakeEvaluator{allow: true}, store, modelegress.NewEvaluator(), store, principals, assignments, store, store, fakeAdapterRegistry{value: provider}, ClockFunc(func() time.Time { return now }), WithCostBudgetGate(gate))
	_, err := service.Dispatch(context.Background(), 11)
	if err == nil || !store.providerRejected {
		t.Fatalf("expected a rejected-provider-response failure, err=%v rejected=%v", err, store.providerRejected)
	}
	if gate.pendingCalls != 1 {
		t.Fatalf("expected the reservation to be marked pending reconciliation exactly once, got %d", gate.pendingCalls)
	}
	if gate.releaseCalls != 0 || len(gate.reconcileCalls) != 0 {
		// This is the exact bug this change fixes: response_received must
		// never be silently released as if the call were free.
		t.Fatalf("response_received without usage must never be released or committed: release=%d reconcile=%+v", gate.releaseCalls, gate.reconcileCalls)
	}
	if store.lastFailureUsage != nil {
		t.Fatalf("expected no usage to be threaded through when none was recoverable, got %+v", store.lastFailureUsage)
	}
}

func TestDispatchNormalizationFailureCommitsActualFromAdapterUsage(t *testing.T) {
	store, catalog, task, contexts, assignments, principals, now := serviceFixture()
	gate := &fakeCostBudgetGate{}
	// The adapter itself succeeded (decoded the envelope fine, usage known)
	// -- only the service's own JSON-schema normalization rejects the
	// content. response_normalization_failed must commit this as actual
	// usage, not leave it reserved forever.
	response := RawResponse{Content: []byte(`{"ok":"wrong"}`), ProviderRequestID: "fake-bad", ProviderReported: true, InputTokens: 19, OutputTokens: 4}
	service, _ := NewDispatchService("explorarte", dispatchCfgWithCostGate(), catalog, task, contexts, fakeEvaluator{allow: true}, store, modelegress.NewEvaluator(), store, principals, assignments, store, store, fakeAdapterRegistry{value: &deterministicAdapter{response: &response}}, ClockFunc(func() time.Time { return now }), WithCostBudgetGate(gate))
	_, err := service.Dispatch(context.Background(), 11)
	if !errors.Is(err, ErrResponseRejected) || !store.failed {
		t.Fatalf("expected terminal known-response failure, got err=%v failed=%v", err, store.failed)
	}
	if len(gate.reconcileCalls) != 1 || gate.reconcileCalls[0].inputTokens != 19 || gate.reconcileCalls[0].outputTokens != 4 {
		t.Fatalf("expected normalization failure to commit actual usage, got reconcile=%+v", gate.reconcileCalls)
	}
	if gate.releaseCalls != 0 || gate.pendingCalls != 0 {
		t.Fatalf("normalization failure with known usage must not release or merely park: release=%d pending=%d", gate.releaseCalls, gate.pendingCalls)
	}
	if store.lastFailureUsage == nil || store.lastFailureUsage.InputTokens != 19 {
		t.Fatalf("expected usage to be threaded through FailAfterResponse, got %+v", store.lastFailureUsage)
	}
}

func TestDispatchAmbiguousTransportIsParkedNotReleased(t *testing.T) {
	store, catalog, task, contexts, assignments, principals, now := serviceFixture()
	gate := &fakeCostBudgetGate{}
	service, _ := NewDispatchService("explorarte", dispatchCfgWithCostGate(), catalog, task, contexts, fakeEvaluator{allow: true}, store, modelegress.NewEvaluator(), store, principals, assignments, store, store, fakeAdapterRegistry{value: &deterministicAdapter{err: errors.New("unknown after send")}}, ClockFunc(func() time.Time { return now }), WithCostBudgetGate(gate))
	_, err := service.Dispatch(context.Background(), 11)
	if !errors.Is(err, ErrAmbiguousOutcome) || !store.ambiguous {
		t.Fatalf("expected ambiguous outcome, got %v", err)
	}
	if gate.pendingCalls != 1 || gate.releaseCalls != 0 || len(gate.reconcileCalls) != 0 {
		t.Fatalf("ambiguous transport must be parked, never released or committed: pending=%d release=%d reconcile=%+v", gate.pendingCalls, gate.releaseCalls, gate.reconcileCalls)
	}
}

// TestDispatchEverySettlementPathSettlesExactlyOnce guards against a
// reservation reaching a terminal invocation state with no explanation at
// all (neither committed, released, nor parked) -- and against the inverse
// bug this change fixes elsewhere: a phase settling explicitly AND the
// deferred Release firing on top of it.
func TestDispatchEverySettlementPathSettlesExactlyOnce(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*deterministicAdapter)
		wantErr bool
	}{
		{name: "success", mutate: func(*deterministicAdapter) {}},
		{name: "before request", mutate: func(a *deterministicAdapter) {
			a.err = &AdapterError{Phase: AdapterFailureBeforeRequest, Outcome: ProviderOutcome{OutcomeClassification: ProviderOutcomeNotSent, ErrorClass: "credential", ErrorCode: "credential_unavailable", ResponseSchemaVersion: "test.fake.response.v1"}, Cause: errors.New("no credential")}
		}, wantErr: true},
		{name: "response received rejected without usage", mutate: func(a *deterministicAdapter) {
			a.response = &RawResponse{ProviderRequestID: "r"}
			a.err = &AdapterError{Phase: AdapterFailureResponseReceived, Outcome: ProviderOutcome{OutcomeClassification: ProviderOutcomeRejected, ProviderRequestID: "r", HTTPStatus: 500, ErrorClass: "response", ErrorCode: "response_read_failed", ResponseHash: SHA256Bytes([]byte("x")), ResponseSchemaVersion: "test.fake.response.v1", Retryable: true}, Cause: ErrResponseRejected}
		}, wantErr: true},
		{name: "ambiguous transport", mutate: func(a *deterministicAdapter) { a.err = errors.New("connection reset") }, wantErr: true},
		{name: "normalization failed", mutate: func(a *deterministicAdapter) {
			a.response = &RawResponse{Content: []byte(`{"ok":"wrong"}`), ProviderRequestID: "fake-bad", ProviderReported: true, InputTokens: 1, OutputTokens: 1}
		}, wantErr: true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			store, catalog, task, contexts, assignments, principals, now := serviceFixture()
			gate := &fakeCostBudgetGate{}
			provider := &deterministicAdapter{}
			test.mutate(provider)
			service, _ := NewDispatchService("explorarte", dispatchCfgWithCostGate(), catalog, task, contexts, fakeEvaluator{allow: true}, store, modelegress.NewEvaluator(), store, principals, assignments, store, store, fakeAdapterRegistry{value: provider}, ClockFunc(func() time.Time { return now }), WithCostBudgetGate(gate))
			_, err := service.Dispatch(context.Background(), 11)
			if test.wantErr && err == nil {
				t.Fatal("expected an error")
			}
			if gate.settledCalls() != 1 {
				t.Fatalf("expected the reservation to settle exactly once, got release=%d pending=%d reconcile=%d (total=%d)", gate.releaseCalls, gate.pendingCalls, len(gate.reconcileCalls), gate.settledCalls())
			}
		})
	}
}

func int64Pointer(value int64) *int64 { return &value }
