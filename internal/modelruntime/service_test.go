package modelruntime

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

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
}

func (f fakeContextReader) GetContextSnapshot(context.Context, int64) (ContextSnapshotRef, error) {
	return f.ref, nil
}
func (f fakeContextReader) ValidateContextSnapshot(context.Context, int64) error {
	return f.validationErr
}
func (f fakeContextReader) RenderContextSnapshot(context.Context, int64) ([]byte, error) {
	return append([]byte(nil), f.rendered...), nil
}

type fakeEvaluator struct{ allow bool }

func (f fakeEvaluator) EvaluateDispatch(context.Context, string, int64, string, string, string) (AuthorizationDecision, error) {
	if f.allow {
		return AuthorizationDecision{Allowed: true, ReasonCode: "allowed_by_grant"}, nil
	}
	return AuthorizationDecision{ReasonCode: "grant_missing"}, nil
}

type fakeAdapterRegistry struct{ value ProviderAdapter }

func (f fakeAdapterRegistry) Get(id string) (ProviderAdapter, bool) {
	return f.value, f.value != nil && f.value.ProviderID() == id
}

type deterministicAdapter struct {
	err      error
	response *RawResponse
}

func (d deterministicAdapter) ProviderID() string { return "test.fake" }
func (d deterministicAdapter) Dispatch(_ context.Context, request CanonicalRequest) (RawResponse, error) {
	if d.response != nil {
		return *d.response, d.err
	}
	if d.err != nil {
		return RawResponse{}, d.err
	}
	return RawResponse{Content: []byte(`{"ok":true}`), HiddenReasoning: []byte("do not persist"), ProviderRequestID: "fake-1", InputTokens: 2, OutputTokens: 3}, nil
}

type fakeStore struct {
	binding    ResolvedBinding
	invocation Invocation
	claimed    ClaimedInvocation
	result     DispatchResult
	failed     bool
	ambiguous  bool
	completed  bool
	created    bool
	cancelled  bool
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
func (f *fakeStore) CreateInvocation(_ context.Context, p PreparedInvocation, _ int) (CreateInvocationResult, error) {
	f.created = true
	v := f.invocation
	v.RequestHash = p.RequestHash
	return CreateInvocationResult{Invocation: v}, nil
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
func (f *fakeStore) MarkSendStarted(context.Context, int64, int64, string, string) (Invocation, error) {
	v := f.invocation
	v.Status = InvocationSendStarted
	return v, nil
}
func (f *fakeStore) MarkResponseReceived(context.Context, int64, int64, string, string) (Invocation, error) {
	v := f.invocation
	v.Status = InvocationResponseReceived
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
func (f *fakeStore) FailAfterResponse(context.Context, FailureCommand, int) (Invocation, error) {
	f.failed = true
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

func serviceFixture() (*fakeStore, fakeCatalog, fakeTaskReader, fakeContextReader, time.Time) {
	now := mustTime("2026-01-01T00:00:00Z")
	rendered := []byte("safe context")
	binding := ResolvedBinding{Binding: RoleBinding{Active: true}, Profile: Profile{ID: "worker-default", PolicyID: "department.worker"}, Version: ProfileVersion{ID: 9, ProfileID: "worker-default", ProviderID: "test.fake", ProviderModelID: "v1", AdapterStatus: AdapterAvailable, DispatchEnabled: true}, Provider: Provider{ID: "test.fake", AdapterStatus: AdapterAvailable, DispatchEnabled: true}, Capabilities: CapabilitySnapshot{Capabilities: []ModelCapability{"structured.output"}}}
	inv := Invocation{ID: 11, OrganizationID: "explorarte", OrganizationRevisionID: 7, TaskID: 3, AttemptID: 4, DispatchActorRoleID: "ingenieria_ia/qa", SubjectRoleID: "ingenieria_ia/qa", ContextSnapshotID: 5, Purpose: "test", ModelProfileID: "worker-default", ModelProfileVersionID: 9, ProviderID: "test.fake", ProviderModelID: "v1", RequiredCapabilities: []ModelCapability{"structured.output"}, OutputMode: OutputJSON, OutputSchema: json.RawMessage(`{"type":"object","required":["ok"],"properties":{"ok":{"type":"boolean"}},"additionalProperties":false}`), MaxOutputTokens: 100, ThinkingMode: ThinkingDisabled, Deadline: now.Add(time.Hour), Status: InvocationRequested}
	store := &fakeStore{binding: binding, invocation: inv, claimed: ClaimedInvocation{Invocation: inv, DispatchAttempt: DispatchAttempt{ID: 13, InvocationID: 11, Status: DispatchClaimed, ClaimedBy: "test"}, ClaimToken: "raw-token"}}
	catalog := fakeCatalog{org: OrganizationRef{ID: "explorarte", RevisionID: 7}, role: RoleRef{ID: "ingenieria_ia/qa", ModelPolicy: "department.worker", Enabled: true, Executable: true, AuthorityClass: "specialist"}}
	task := fakeTaskReader{ref: TaskAttemptRef{TaskID: 3, AttemptID: 4, OrganizationID: "explorarte", OrganizationRevisionID: 7, AssignedRoleID: "ingenieria_ia/qa", TaskStatus: "running", AttemptStatus: "running", LeaseExpiresAt: now.Add(time.Hour)}}
	contexts := fakeContextReader{ref: ContextSnapshotRef{ID: 5, OrganizationID: "explorarte", OrganizationRevisionID: 7, ActorRoleID: "ingenieria_ia/qa", TaskRef: "3", Status: "ready", RenderedHash: SHA256Bytes(rendered), DataClasses: []string{"organizational"}}, rendered: rendered}
	return store, catalog, task, contexts, now
}

func TestInvocationServiceCreatesFrozenInvocation(t *testing.T) {
	store, catalog, task, contexts, now := serviceFixture()
	service, err := NewInvocationService("explorarte", catalog, task, contexts, store, ClockFunc(func() time.Time { return now }), 10)
	if err != nil {
		t.Fatal(err)
	}
	command := CreateInvocationCommand{OrganizationID: "explorarte", TaskID: 3, AttemptID: 4, DispatchActorRoleID: "ingenieria_ia/qa", SubjectRoleID: "ingenieria_ia/qa", ContextSnapshotID: 5, Purpose: "test", RequiredCapabilities: []ModelCapability{"structured.output"}, OutputMode: OutputJSON, OutputSchema: json.RawMessage(`{"type":"object"}`), MaxOutputTokens: 100, ThinkingMode: ThinkingDisabled, IdempotencyKey: "create-1", Deadline: now.Add(time.Hour)}
	got, err := service.Create(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	if !store.created || got.Invocation.RequestHash == "" {
		t.Fatalf("invocation not prepared: %#v", got)
	}
}
func TestDispatchFakeSuccessDoesNotCompleteTask(t *testing.T) {
	store, catalog, task, contexts, now := serviceFixture()
	cfg := RuntimeConfig{Enabled: true, CommandTimeout: time.Minute, GlobalConcurrency: 1, MaxResponseBytes: 1024, MaxToolIntents: 2, ClaimTTL: time.Minute, ReconcileBatchSize: 10, OutboxMaxAttempts: 10}
	service, err := NewDispatchService(cfg, catalog, task, contexts, fakeEvaluator{allow: true}, store, fakeAdapterRegistry{value: deterministicAdapter{}}, ClockFunc(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	got, err := service.Dispatch(context.Background(), 11, "test")
	if err != nil {
		t.Fatal(err)
	}
	if !store.completed || got.Invocation.Status != InvocationSucceeded {
		t.Fatalf("dispatch did not complete: %#v", got)
	}
	body, _ := json.Marshal(got)
	if bytesContains(body, []byte("do not persist")) {
		t.Fatal("hidden reasoning leaked")
	}
}
func TestDispatchAuthorizationDenyFailsBeforeSend(t *testing.T) {
	store, catalog, task, contexts, now := serviceFixture()
	cfg := RuntimeConfig{Enabled: true, CommandTimeout: time.Minute, GlobalConcurrency: 1, MaxResponseBytes: 1024, MaxToolIntents: 2, ClaimTTL: time.Minute, ReconcileBatchSize: 10, OutboxMaxAttempts: 10}
	service, _ := NewDispatchService(cfg, catalog, task, contexts, fakeEvaluator{allow: false}, store, fakeAdapterRegistry{value: deterministicAdapter{}}, ClockFunc(func() time.Time { return now }))
	_, err := service.Dispatch(context.Background(), 11, "test")
	if !errors.Is(err, ErrAuthorizationDenied) || !store.failed {
		t.Fatalf("expected denied pre-send failure, got %v", err)
	}
}
func TestDispatchAdapterFailureIsAmbiguous(t *testing.T) {
	store, catalog, task, contexts, now := serviceFixture()
	cfg := RuntimeConfig{Enabled: true, CommandTimeout: time.Minute, GlobalConcurrency: 1, MaxResponseBytes: 1024, MaxToolIntents: 2, ClaimTTL: time.Minute, ReconcileBatchSize: 10, OutboxMaxAttempts: 10}
	service, _ := NewDispatchService(cfg, catalog, task, contexts, fakeEvaluator{allow: true}, store, fakeAdapterRegistry{value: deterministicAdapter{err: errors.New("unknown after send")}}, ClockFunc(func() time.Time { return now }))
	_, err := service.Dispatch(context.Background(), 11, "test")
	if !errors.Is(err, ErrAmbiguousOutcome) || !store.ambiguous {
		t.Fatalf("expected ambiguous outcome, got %v", err)
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
	store, catalog, task, contexts, now := serviceFixture()
	cfg := RuntimeConfig{Enabled: true, CommandTimeout: time.Minute, GlobalConcurrency: 1, MaxResponseBytes: 1024, MaxToolIntents: 2, ClaimTTL: time.Minute, ReconcileBatchSize: 10, OutboxMaxAttempts: 10}
	response := RawResponse{Content: []byte(`{"ok":"wrong"}`), ProviderRequestID: "fake-bad"}
	service, _ := NewDispatchService(cfg, catalog, task, contexts, fakeEvaluator{allow: true}, store, fakeAdapterRegistry{value: deterministicAdapter{response: &response}}, ClockFunc(func() time.Time { return now }))
	_, err := service.Dispatch(context.Background(), 11, "test")
	if !errors.Is(err, ErrResponseRejected) || !store.failed || store.ambiguous {
		t.Fatalf("expected terminal known-response failure, got err=%v failed=%v ambiguous=%v", err, store.failed, store.ambiguous)
	}
}

func TestDispatchCancellationRequiresDurableRequest(t *testing.T) {
	store, catalog, task, contexts, now := serviceFixture()
	cfg := RuntimeConfig{Enabled: true, CommandTimeout: time.Minute, GlobalConcurrency: 1, MaxResponseBytes: 1024, MaxToolIntents: 2, ClaimTTL: time.Minute, ReconcileBatchSize: 10, OutboxMaxAttempts: 10}
	response := RawResponse{CancellationConfirmed: true}
	service, _ := NewDispatchService(cfg, catalog, task, contexts, fakeEvaluator{allow: true}, store, fakeAdapterRegistry{value: deterministicAdapter{response: &response, err: context.Canceled}}, ClockFunc(func() time.Time { return now }))
	_, err := service.Dispatch(context.Background(), 11, "test")
	if !errors.Is(err, ErrAmbiguousOutcome) || store.cancelled {
		t.Fatalf("cancellation without durable request was accepted: err=%v", err)
	}

	requestedAt := now
	store, catalog, task, contexts, now = serviceFixture()
	store.invocation.CancelRequestedAt = &requestedAt
	store.claimed.Invocation.CancelRequestedAt = &requestedAt
	service, _ = NewDispatchService(cfg, catalog, task, contexts, fakeEvaluator{allow: true}, store, fakeAdapterRegistry{value: deterministicAdapter{response: &response, err: context.Canceled}}, ClockFunc(func() time.Time { return now }))
	_, err = service.Dispatch(context.Background(), 11, "test")
	if !errors.Is(err, ErrCancellationRequested) || !store.cancelled {
		t.Fatalf("durable confirmed cancellation not persisted: err=%v", err)
	}
}

func TestInvocationCancellationRejectsDifferentActor(t *testing.T) {
	store, catalog, task, contexts, now := serviceFixture()
	service, err := NewInvocationService("explorarte", catalog, task, contexts, store, ClockFunc(func() time.Time { return now }), 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Cancel(context.Background(), 11, "ingenieria_ia/frontend", "stop"); !errors.Is(err, ErrAuthorizationDenied) {
		t.Fatalf("expected actor denial, got %v", err)
	}
}

func TestDispatchDisabledDoesNotClaim(t *testing.T) {
	store, catalog, task, contexts, now := serviceFixture()
	cfg := RuntimeConfig{Enabled: false, CommandTimeout: time.Minute, GlobalConcurrency: 1, MaxResponseBytes: 1024, MaxToolIntents: 2, ClaimTTL: time.Minute, ReconcileBatchSize: 10, OutboxMaxAttempts: 10}
	service, err := NewDispatchService(cfg, catalog, task, contexts, fakeEvaluator{allow: true}, store, fakeAdapterRegistry{value: deterministicAdapter{}}, ClockFunc(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Dispatch(context.Background(), 11, "test"); !errors.Is(err, ErrDisabled) {
		t.Fatalf("expected disabled dispatch, got %v", err)
	}
}
