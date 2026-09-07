package executionharness

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

func testRunDescriptorSpec() RunSpec {
	content := "approved context for descriptor tests"
	digest := sha256.Sum256([]byte(content))
	return RunSpec{
		Identity: RunIdentity{
			RunID: "harness-run-1", OrganizationID: "explorarte", TaskID: 42,
			AttemptID: 7, RoleID: "ingenieria_ia/qa", ExecutionPrincipalID: "principal-1",
			CorrelationID: "corr-1", CausationID: "cause-1",
		},
		LeaseToken: "lease-token-is-never-persisted",
		Context:    InitialContext{ID: "snapshot-9", Version: "v3", Digest: hex.EncodeToString(digest[:]), Content: content},
		Tools: []ToolDefinition{
			{Name: "search", Description: "search approved sources", InputSchema: []byte(`{"type":"object","properties":{"q":{"type":"string"}}}`)},
			{Name: "read", Description: "read one source", InputSchema: []byte(`{"type":"object"}`)},
		},
		Policy: RunPolicy{MaxTurns: 3, MaxToolCalls: 5, ExecutionProfileID: "profile/v1", ModelPolicyRef: "policy/v1", BuildRef: "build-abc"},
	}
}

func TestRunDescriptorCanonicalToolOrderIsDeterministic(t *testing.T) {
	first, err := BuildRunDescriptor(testRunDescriptorSpec())
	if err != nil {
		t.Fatal(err)
	}
	spec := testRunDescriptorSpec()
	spec.Tools[0], spec.Tools[1] = spec.Tools[1], spec.Tools[0]
	second, err := BuildRunDescriptor(spec)
	if err != nil {
		t.Fatal(err)
	}
	firstDigest, err := first.CanonicalDigest()
	if err != nil {
		t.Fatal(err)
	}
	secondDigest, err := second.CanonicalDigest()
	if err != nil {
		t.Fatal(err)
	}
	if firstDigest != secondDigest || first.IdentityDigest != second.IdentityDigest {
		t.Fatalf("reordered tools changed descriptor identity: first=%s/%s second=%s/%s", firstDigest, first.IdentityDigest, secondDigest, second.IdentityDigest)
	}
}

func TestRunDescriptorWithoutToolsUsesAnEmptyArray(t *testing.T) {
	spec := testRunDescriptorSpec()
	spec.Tools = nil
	descriptor, err := BuildRunDescriptor(spec)
	if err != nil {
		t.Fatal(err)
	}
	body, err := descriptor.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"frozen_tools":[]`) {
		t.Fatalf("canonical no-tool descriptor=%s want frozen_tools=[]", body)
	}
	store := NewMemoryRunDescriptorStore()
	if err = store.EnsureRunDescriptor(context.Background(), descriptor); err != nil {
		t.Fatal(err)
	}
}

func TestToolDefinitionDigestChangesWithDescriptionOrSchema(t *testing.T) {
	tool := testRunDescriptorSpec().Tools[0]
	base, err := ToolDefinitionDigest(tool)
	if err != nil {
		t.Fatal(err)
	}
	description := tool
	description.Description = "different description"
	changedDescription, err := ToolDefinitionDigest(description)
	if err != nil {
		t.Fatal(err)
	}
	if changedDescription == base {
		t.Fatal("description change did not change tool definition digest")
	}
	schema := tool
	schema.InputSchema = []byte(`{"type":"object","required":["q"]}`)
	changedSchema, err := ToolDefinitionDigest(schema)
	if err != nil {
		t.Fatal(err)
	}
	if changedSchema == base {
		t.Fatal("schema change did not change tool definition digest")
	}
}

func TestRunDescriptorStoreRejectsIdentityDrift(t *testing.T) {
	store := NewMemoryRunDescriptorStore()
	base, err := BuildRunDescriptor(testRunDescriptorSpec())
	if err != nil {
		t.Fatal(err)
	}
	if err = store.EnsureRunDescriptor(context.Background(), base); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*RunSpec){
		"execution profile": func(spec *RunSpec) { spec.Policy.ExecutionProfileID = "profile/v2" },
		"model policy":      func(spec *RunSpec) { spec.Policy.ModelPolicyRef = "policy/v2" },
		"build ref":         func(spec *RunSpec) { spec.Policy.BuildRef = "build/v2" },
	} {
		t.Run(name, func(t *testing.T) {
			spec := testRunDescriptorSpec()
			mutate(&spec)
			drifted, buildErr := BuildRunDescriptor(spec)
			if buildErr != nil {
				t.Fatal(buildErr)
			}
			if err := store.EnsureRunDescriptor(context.Background(), drifted); !errors.Is(err, ErrRunDescriptorConflict) {
				t.Fatalf("drift accepted: %v", err)
			}
		})
	}
}

func TestRunDescriptorDoesNotContainLeaseToken(t *testing.T) {
	descriptor, err := BuildRunDescriptor(testRunDescriptorSpec())
	if err != nil {
		t.Fatal(err)
	}
	body, err := descriptor.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "lease-token-is-never-persisted") || strings.Contains(string(body), "lease_token") {
		t.Fatalf("descriptor canonical representation contains lease material: %s", body)
	}
	store := NewMemoryRunDescriptorStore()
	if err := store.EnsureRunDescriptor(context.Background(), descriptor); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.ReadRunDescriptor(context.Background(), descriptor.OrganizationID, descriptor.RunID)
	if err != nil {
		t.Fatal(err)
	}
	loadedBody, err := loaded.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(loadedBody), "lease-token-is-never-persisted") {
		t.Fatal("loaded descriptor contains lease material")
	}
}

type descriptorTestAuthority struct{}

func (descriptorTestAuthority) AuthorizeExecution(context.Context, AuthorityRequest) error {
	return ErrAuthorityUnavailable
}

type descriptorTestModels struct{}

func (descriptorTestModels) Invoke(context.Context, RunIdentity, NormalizedModelRequest) (ModelResult, error) {
	return ModelResult{FinishReason: FinishFinal, FinalOutput: "unused"}, nil
}

type descriptorTestCatalog struct{}

func (descriptorTestCatalog) Lookup(context.Context, string) (ToolDefinition, bool) {
	return ToolDefinition{}, false
}

func (descriptorTestCatalog) ValidateArguments(context.Context, ToolDefinition, jsonRaw) error {
	return nil
}

type descriptorTestTools struct{}

func (descriptorTestTools) Execute(context.Context, RunIdentity, ToolRequest) (ToolExecutionResult, error) {
	return ToolExecutionResult{}, nil
}

type descriptorOrderedHistory struct {
	base  *MemoryHistoryStore
	order *[]string
}

func (h descriptorOrderedHistory) Append(ctx context.Context, runID string, expectedSequence uint64, event Event) (Event, error) {
	if event.Type == EventRunStarted {
		*h.order = append(*h.order, "run_started")
	}
	return h.base.Append(ctx, runID, expectedSequence, event)
}

func (h descriptorOrderedHistory) Read(ctx context.Context, runID string) ([]Event, error) {
	return h.base.Read(ctx, runID)
}

type descriptorOrderedStore struct {
	order *[]string
	base  *MemoryRunDescriptorStore
}

func (s descriptorOrderedStore) EnsureRunDescriptor(ctx context.Context, descriptor RunDescriptor) error {
	*s.order = append(*s.order, "descriptor")
	return s.base.EnsureRunDescriptor(ctx, descriptor)
}

func (s descriptorOrderedStore) ReadRunDescriptor(ctx context.Context, organizationID, runID string) (RunDescriptor, error) {
	return s.base.ReadRunDescriptor(ctx, organizationID, runID)
}

func TestRuntimePersistsDescriptorBeforeRunStarted(t *testing.T) {
	order := []string{}
	history := descriptorOrderedHistory{base: NewMemoryHistoryStore(), order: &order}
	descriptors := descriptorOrderedStore{order: &order, base: NewMemoryRunDescriptorStore()}
	runtime, err := NewWithDescriptorStore(descriptorTestAuthority{}, descriptorTestModels{}, descriptorTestCatalog{}, descriptorTestTools{}, history, descriptors)
	if err != nil {
		t.Fatal(err)
	}
	result := runtime.Execute(context.Background(), testRunDescriptorSpec())
	if result.Status != StatusAuthorityUnavailable {
		t.Fatalf("status=%s want authority_unavailable", result.Status)
	}
	if len(order) != 2 || order[0] != "descriptor" || order[1] != "run_started" {
		t.Fatalf("durability order=%v want [descriptor run_started]", order)
	}
}

func TestRuntimeBackfillsLegacyHistoryWithoutPoisoningDescriptor(t *testing.T) {
	spec := testRunDescriptorSpec()
	_, identityDigest, err := validateSpec(spec)
	if err != nil {
		t.Fatal(err)
	}
	history := NewMemoryHistoryStore()
	if _, err = history.Append(context.Background(), spec.Identity.RunID, 0, Event{
		RunID: spec.Identity.RunID, Type: EventRunStarted, IdentityDigest: identityDigest,
	}); err != nil {
		t.Fatal(err)
	}
	store := NewMemoryRunDescriptorStore()
	runtime, err := NewWithDescriptorStore(descriptorTestAuthority{}, descriptorTestModels{}, descriptorTestCatalog{}, descriptorTestTools{}, history, store)
	if err != nil {
		t.Fatal(err)
	}
	drifted := spec
	drifted.Policy.ExecutionProfileID = "profile/drifted"
	if result := runtime.Execute(context.Background(), drifted); result.Status != StatusIdentityDrift {
		t.Fatalf("drifted legacy re-entry status=%s want identity_drift", result.Status)
	}
	if result := runtime.Execute(context.Background(), spec); result.Status != StatusAuthorityUnavailable {
		t.Fatalf("original legacy re-entry status=%s want authority_unavailable", result.Status)
	}
	if _, err = store.ReadRunDescriptor(context.Background(), spec.Identity.OrganizationID, spec.Identity.RunID); err != nil {
		t.Fatalf("original identity did not backfill descriptor: %v", err)
	}
}

type descriptorCountingModels struct {
	invocations int
}

func (m *descriptorCountingModels) Invoke(context.Context, RunIdentity, NormalizedModelRequest) (ModelResult, error) {
	m.invocations++
	return ModelResult{FinishReason: FinishFinal, FinalOutput: "output"}, nil
}

type descriptorCountingTools struct {
	executions int
}

func (t *descriptorCountingTools) Execute(context.Context, RunIdentity, ToolRequest) (ToolExecutionResult, error) {
	t.executions++
	return ToolExecutionResult{}, nil
}

type descriptorFailingStore struct {
	err error
}

func (s descriptorFailingStore) EnsureRunDescriptor(context.Context, RunDescriptor) error {
	return s.err
}

func (s descriptorFailingStore) ReadRunDescriptor(context.Context, string, string) (RunDescriptor, error) {
	return RunDescriptor{}, s.err
}

// Test A: descriptor persist FAIL -> 0 model invocations, 0 tool executions
func TestRuntimeDescriptorPersistFailurePreventsExecution(t *testing.T) {
	models := &descriptorCountingModels{}
	tools := &descriptorCountingTools{}
	history := NewMemoryHistoryStore()
	store := descriptorFailingStore{err: errors.New("simulated database failure")}
	runtime, err := NewWithDescriptorStore(descriptorTestAuthority{}, models, descriptorTestCatalog{}, tools, history, store)
	if err != nil {
		t.Fatal(err)
	}
	result := runtime.Execute(context.Background(), testRunDescriptorSpec())
	if result.Status != StatusHistoryError && result.Status != StatusIdentityDrift {
		t.Fatalf("status=%s want history_error or identity_drift", result.Status)
	}
	if models.invocations != 0 {
		t.Fatalf("model invocations=%d want 0", models.invocations)
	}
	if tools.executions != 0 {
		t.Fatalf("tool executions=%d want 0", tools.executions)
	}
}

// Test B: descriptor persisted -> process dies before run_started (events empty) -> restart same RunSpec -> resumes safely without identity conflict
func TestRuntimeResumeSameSpecWhenDiedBeforeRunStarted(t *testing.T) {
	spec := testRunDescriptorSpec()
	desc, err := BuildRunDescriptor(spec)
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemoryRunDescriptorStore()
	if err := store.EnsureRunDescriptor(context.Background(), desc); err != nil {
		t.Fatal(err)
	}
	history := NewMemoryHistoryStore() // empty history: died before EventRunStarted
	runtime, err := NewWithDescriptorStore(descriptorTestAuthority{}, descriptorTestModels{}, descriptorTestCatalog{}, descriptorTestTools{}, history, store)
	if err != nil {
		t.Fatal(err)
	}
	result := runtime.Execute(context.Background(), spec)
	// Must reach authority check and NOT fail with descriptor conflict
	if result.Status != StatusAuthorityUnavailable {
		t.Fatalf("status=%s want authority_unavailable", result.Status)
	}
	events, err := history.Read(context.Background(), spec.Identity.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 || events[0].Type != EventRunStarted {
		t.Fatalf("expected EventRunStarted to be recorded on resume, got events=%v", events)
	}
}

// Test C: descriptor persisted -> no run events -> restart different RunSpec under same RunID -> DENY
func TestRuntimeRejectDriftedSpecWhenDiedBeforeRunStarted(t *testing.T) {
	spec := testRunDescriptorSpec()
	desc, err := BuildRunDescriptor(spec)
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemoryRunDescriptorStore()
	if err := store.EnsureRunDescriptor(context.Background(), desc); err != nil {
		t.Fatal(err)
	}
	history := NewMemoryHistoryStore() // empty history

	drifted := spec
	drifted.Policy.ExecutionProfileID = "profile/drifted-restart"

	runtime, err := NewWithDescriptorStore(descriptorTestAuthority{}, descriptorTestModels{}, descriptorTestCatalog{}, descriptorTestTools{}, history, store)
	if err != nil {
		t.Fatal(err)
	}
	result := runtime.Execute(context.Background(), drifted)
	if result.Status != StatusIdentityDrift {
		t.Fatalf("drifted restart status=%s want identity_drift", result.Status)
	}
	events, _ := history.Read(context.Background(), spec.Identity.RunID)
	if len(events) != 0 {
		t.Fatalf("expected 0 events written for drifted spec, got %d", len(events))
	}
}

// Test D: descriptor persisted -> authority later denied -> define state, verify MemoryOS does not interpret mere existence of descriptor as successful execution
func TestRuntimeAuthorityDeniedAfterDescriptorPersisted(t *testing.T) {
	spec := testRunDescriptorSpec()
	store := NewMemoryRunDescriptorStore()
	history := NewMemoryHistoryStore()
	runtime, err := NewWithDescriptorStore(descriptorTestAuthority{}, descriptorTestModels{}, descriptorTestCatalog{}, descriptorTestTools{}, history, store)
	if err != nil {
		t.Fatal(err)
	}
	result := runtime.Execute(context.Background(), spec)
	if result.Status != StatusAuthorityUnavailable {
		t.Fatalf("status=%s want authority_unavailable", result.Status)
	}
	// Verify descriptor exists
	if _, err := store.ReadRunDescriptor(context.Background(), spec.Identity.OrganizationID, spec.Identity.RunID); err != nil {
		t.Fatalf("read descriptor: %v", err)
	}
	// Events recorded
	events, err := history.Read(context.Background(), spec.Identity.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 {
		t.Fatal("expected events to be written")
	}
}
