package authorization

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
)

type fakePolicyReader struct {
	organization registry.Organization
	revision     *registry.Revision
	roles        map[string]registry.Role
	err          error
}

func (f *fakePolicyReader) GetOrganization(context.Context, string) (registry.Organization, error) {
	if f.err != nil {
		return registry.Organization{}, f.err
	}
	return f.organization, nil
}
func (f *fakePolicyReader) GetCurrentRevision(context.Context, string) (*registry.Revision, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.revision, nil
}
func (f *fakePolicyReader) GetAuthorizationRole(_ context.Context, _ string, id string) (registry.Role, error) {
	if f.err != nil {
		return registry.Role{}, f.err
	}
	role, ok := f.roles[id]
	if !ok {
		return registry.Role{}, ErrRoleNotFound
	}
	return role, nil
}

func testMatrix() Matrix {
	return Matrix{
		DefaultPolicy: "deny",
		Capabilities: []Capability{
			{ID: "code.commit", Risk: "high"},
			{ID: "project.create", Risk: "medium"},
			{ID: "organization.activate_skill", Risk: "high", Approval: "owner"},
			{ID: "rag.publish_approved", Risk: "high", Approval: "policy_or_human"},
			{ID: "deployment.request", Risk: "high", Approval: "owner_or_cell_policy"},
			{ID: "cell.read_clinical_data", Risk: "forbidden"},
		},
		Grants: map[string][]string{
			"owner": {"*"}, "execution_service": {"code.commit"}, "specialist": {"project.create"},
		},
		HardDenies: map[string][]string{
			"*": {"cell.read_clinical_data"}, "specialist": {"project.create"},
		},
	}
}

func testAuthorizer(t *testing.T) (*Authorizer, *fakePolicyReader) {
	t.Helper()
	hash := strings.Repeat("a", 64)
	reader := &fakePolicyReader{
		organization: registry.Organization{ID: "explorarte", OwnerRoleID: "empresa/human", CurrentRevision: 7},
		revision:     &registry.Revision{ID: 7, DocumentHashes: map[string]string{"capability-matrix.yaml": hash}},
		roles: map[string]registry.Role{
			"empresa/human":             {OrganizationID: "explorarte", ID: "empresa/human", AuthorityClass: "owner", Enabled: true, Executable: false},
			"ingenieria_ia/code-runner": {OrganizationID: "explorarte", ID: "ingenieria_ia/code-runner", AuthorityClass: "execution_service", Enabled: true, Executable: true},
			"creativo/copywriter":       {OrganizationID: "explorarte", ID: "creativo/copywriter", AuthorityClass: "specialist", Enabled: true, Executable: true},
		},
	}
	a, err := newAuthorizer(reader, "explorarte", testMatrix(), hash)
	if err != nil {
		t.Fatal(err)
	}
	return a, reader
}

func evalRequest(role, capability string) EvaluationRequest {
	return EvaluationRequest{OrganizationID: "explorarte", OrganizationRevisionID: 7, ActorRoleID: role, CapabilityID: capability, ResourceType: "task", ResourceID: "42", ActionDigest: DigestAction([]byte("task:42"))}
}

func TestPolicyDefaultDenyOwnerWildcardAndHardDenies(t *testing.T) {
	a, _ := testAuthorizer(t)
	cases := []struct {
		name, role, capability string
		effect                 Effect
		reason                 ReasonCode
	}{
		{"grant", "ingenieria_ia/code-runner", "code.commit", EffectAllow, ReasonAllowedByGrant},
		{"default deny", "ingenieria_ia/code-runner", "project.create", EffectDeny, ReasonGrantMissing},
		{"owner wildcard", "empresa/human", "code.commit", EffectAllow, ReasonAllowedByGrant},
		{"global hard deny", "empresa/human", "cell.read_clinical_data", EffectDeny, ReasonHardDeny},
		{"authority hard deny", "creativo/copywriter", "project.create", EffectDeny, ReasonHardDeny},
		{"unknown capability", "empresa/human", "missing.capability", EffectDeny, ReasonUnknownCapability},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := a.Evaluate(context.Background(), evalRequest(tc.role, tc.capability))
			if err != nil {
				t.Fatal(err)
			}
			if got.Effect != tc.effect || got.ReasonCode != tc.reason {
				t.Fatalf("got %+v", got)
			}
		})
	}
}

func TestPolicyRoleAndRevisionFailures(t *testing.T) {
	a, reader := testAuthorizer(t)
	role := reader.roles["ingenieria_ia/code-runner"]
	role.Enabled = false
	reader.roles[role.ID] = role
	got, _ := a.Evaluate(context.Background(), evalRequest(role.ID, "code.commit"))
	if got.ReasonCode != ReasonRoleDisabled {
		t.Fatalf("disabled: %+v", got)
	}
	role.Enabled = true
	now := time.Now()
	role.RetiredAt = &now
	reader.roles[role.ID] = role
	got, _ = a.Evaluate(context.Background(), evalRequest(role.ID, "code.commit"))
	if got.ReasonCode != ReasonRoleRetired {
		t.Fatalf("retired: %+v", got)
	}
	role.RetiredAt = nil
	role.Executable = false
	reader.roles[role.ID] = role
	got, _ = a.Evaluate(context.Background(), evalRequest(role.ID, "code.commit"))
	if got.ReasonCode != ReasonRoleNotExecutable {
		t.Fatalf("not executable: %+v", got)
	}
	got, _ = a.Evaluate(context.Background(), evalRequest("empresa/human", "code.commit"))
	if got.Effect != EffectAllow {
		t.Fatalf("human owner must be allowed: %+v", got)
	}
	request := evalRequest("empresa/human", "code.commit")
	request.OrganizationRevisionID = 6
	got, _ = a.Evaluate(context.Background(), request)
	if got.ReasonCode != ReasonRevisionMismatch {
		t.Fatalf("revision: %+v", got)
	}
	reader.revision.DocumentHashes["capability-matrix.yaml"] = strings.Repeat("b", 64)
	request.OrganizationRevisionID = 7
	got, _ = a.Evaluate(context.Background(), request)
	if got.ReasonCode != ReasonMatrixHashMismatch {
		t.Fatalf("matrix: %+v", got)
	}
}

func TestUnknownAuthorityAndOrganizationMismatch(t *testing.T) {
	a, reader := testAuthorizer(t)
	reader.roles["x/y"] = registry.Role{OrganizationID: "explorarte", ID: "x/y", AuthorityClass: "mystery", Enabled: true, Executable: true}
	got, _ := a.Evaluate(context.Background(), evalRequest("x/y", "code.commit"))
	if got.ReasonCode != ReasonUnknownAuthorityClass {
		t.Fatalf("got %+v", got)
	}
	request := evalRequest("empresa/human", "code.commit")
	request.OrganizationID = "other"
	got, _ = a.Evaluate(context.Background(), request)
	if got.ReasonCode != ReasonOrganizationMismatch {
		t.Fatalf("got %+v", got)
	}
}

func TestApprovalModesAndRiskDoNotInventApproval(t *testing.T) {
	a, _ := testAuthorizer(t)
	got, _ := a.Evaluate(context.Background(), evalRequest("ingenieria_ia/code-runner", "code.commit"))
	if got.Risk != "high" || got.Effect != EffectAllow {
		t.Fatalf("high risk without approval: %+v", got)
	}
	for _, capability := range []string{"organization.activate_skill", "rag.publish_approved", "deployment.request"} {
		got, err := a.Evaluate(context.Background(), evalRequest("empresa/human", capability))
		if err != nil {
			t.Fatal(err)
		}
		if got.Effect != EffectApprovalRequired || got.ReasonCode != ReasonApprovalMissing || got.ApprovalMode == "" {
			t.Fatalf("%s: %+v", capability, got)
		}
	}
	got, _ = a.Evaluate(context.Background(), evalRequest("creativo/copywriter", "rag.publish_approved"))
	if got.Effect != EffectApprovalRequired {
		t.Fatalf("approval may grant without static grant: %+v", got)
	}
}

func TestLegacyAuthorizeCompatibility(t *testing.T) {
	a, _ := testAuthorizer(t)
	if err := a.Authorize(context.Background(), "explorarte", 7, "ingenieria_ia/code-runner", "code.commit"); err != nil {
		t.Fatal(err)
	}
	if err := a.Authorize(context.Background(), "explorarte", 7, "ingenieria_ia/code-runner", "project.create"); !errors.Is(err, ErrCapabilityDenied) {
		t.Fatalf("deny=%v", err)
	}
	if err := a.Authorize(context.Background(), "explorarte", 7, "empresa/human", "organization.activate_skill"); !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("approval=%v", err)
	}
	if err := a.Authorize(context.Background(), "explorarte", 7, "empresa/human", "missing.capability"); !errors.Is(err, ErrUnknownCapability) {
		t.Fatalf("unknown=%v", err)
	}
}

func TestOperationalReaderErrorIsNotConvertedToDeny(t *testing.T) {
	a, reader := testAuthorizer(t)
	reader.err = context.DeadlineExceeded
	result, err := a.Evaluate(context.Background(), evalRequest("empresa/human", "code.commit"))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error=%v", err)
	}
	if result.Effect != "" || result.ReasonCode != "" {
		t.Fatalf("operational error became a policy decision: %+v", result)
	}
}

func TestPolicyOrHumanAndOwnerOrCellPolicyDoNotInventAutomaticApproval(t *testing.T) {
	a, _ := testAuthorizer(t)
	for _, capability := range []string{"rag.publish_approved", "deployment.request"} {
		result, err := a.Evaluate(context.Background(), evalRequest("creativo/copywriter", capability))
		if err != nil {
			t.Fatal(err)
		}
		if result.Effect != EffectApprovalRequired || result.ReasonCode != ReasonApprovalMissing {
			t.Fatalf("%s unexpectedly used an automatic policy: %+v", capability, result)
		}
	}
}
