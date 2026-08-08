package modelruntime

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/modelegress"
)

func r24CreateCommand(now time.Time) CreateInvocationCommand {
	return CreateInvocationCommand{
		OrganizationID: "explorarte", TaskID: 3, AttemptID: 4,
		SubjectRoleID: "ingenieria_ia/code-runner", ContextSnapshotID: 5,
		Purpose: "department_worker", RequiredCapabilities: []ModelCapability{"structured.output"},
		OutputMode: OutputJSON, OutputSchema: json.RawMessage(`{"type":"object"}`),
		MaxOutputTokens: 100, ThinkingMode: ThinkingDisabled,
		IdempotencyKey: "r24-scope-create", CorrelationID: "executive:test", Deadline: now.Add(time.Hour),
	}
}

func TestInvocationServiceRejectsAlibabaWithoutCEOScopeBeforeMaterialization(t *testing.T) {
	store, catalog, task, contexts, assignments, _, now := serviceFixture()
	store.binding.Version.ProviderID = "alibaba_token_plan_via_claude_code"
	store.binding.Version.Transport = TransportCLI
	store.binding.Provider.ID = "alibaba_token_plan_via_claude_code"
	store.binding.Provider.Transport = TransportCLI
	contexts.ref.DataClasses = []string{"organizational"}
	contexts.ref.ExecutiveScope = ""
	service, err := NewInvocationService("explorarte", catalog, task, contexts, store, store, store, assignments, ClockFunc(func() time.Time { return now }), 10, false)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Create(context.Background(), r24CreateCommand(now))
	if !errors.Is(err, ErrEgressDenied) {
		t.Fatalf("error=%v", err)
	}
	if store.created {
		t.Fatal("invocation was materialized without CEO executive scope")
	}
}

func TestInvocationServiceAcceptsMatchingAlibabaCEOScope(t *testing.T) {
	store, catalog, task, contexts, assignments, _, now := serviceFixture()
	store.binding.Version.ProviderID = "alibaba_token_plan_via_claude_code"
	store.binding.Version.Transport = TransportCLI
	store.binding.Provider.ID = "alibaba_token_plan_via_claude_code"
	store.binding.Provider.Transport = TransportCLI
	contexts.ref.DataClasses = []string{"organizational", "public"}
	contexts.ref.ExecutiveScope = modelegress.ScopeExecutiveCEO
	service, err := NewInvocationService("explorarte", catalog, task, contexts, store, store, store, assignments, ClockFunc(func() time.Time { return now }), 10, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Create(context.Background(), r24CreateCommand(now)); err != nil {
		t.Fatal(err)
	}
	if !store.created {
		t.Fatal("matching CEO scoped invocation was not materialized")
	}
}

func TestInvocationServiceOpenAIOrganizationalRequiresLeaderScopeButPublicDoesNot(t *testing.T) {
	for _, tc := range []struct {
		name    string
		classes []string
		scope   string
		wantErr bool
	}{
		{name: "organizational no scope", classes: []string{"organizational"}, wantErr: true},
		{name: "organizational leader scope", classes: []string{"organizational"}, scope: modelegress.ScopeDepartmentLeader},
		{name: "public no scope", classes: []string{"public"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, catalog, task, contexts, assignments, _, now := serviceFixture()
			store.binding.Version.ProviderID = "openai_compatible"
			store.binding.Version.Transport = TransportHTTP
			store.binding.Provider.ID = "openai_compatible"
			store.binding.Provider.Transport = TransportHTTP
			contexts.ref.DataClasses = tc.classes
			contexts.ref.ExecutiveScope = tc.scope
			service, err := NewInvocationService("explorarte", catalog, task, contexts, store, store, store, assignments, ClockFunc(func() time.Time { return now }), 10, false)
			if err != nil {
				t.Fatal(err)
			}
			_, err = service.Create(context.Background(), r24CreateCommand(now))
			if tc.wantErr {
				if !errors.Is(err, ErrEgressDenied) || store.created {
					t.Fatalf("err=%v created=%v", err, store.created)
				}
				return
			}
			if err != nil || !store.created {
				t.Fatalf("err=%v created=%v", err, store.created)
			}
		})
	}
}
