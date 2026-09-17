package modeldispatch

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"
)

type authorizationCall struct {
	role       string
	capability string
	resourceID string
}

type recordingAuthorizer struct {
	calls []authorizationCall
	deny  map[string]bool
}

func (a *recordingAuthorizer) Authorize(_ context.Context, _ string, _ int64, role, capability string) error {
	a.calls = append(a.calls, authorizationCall{role: role, capability: capability})
	if a.deny[role+"|"+capability] {
		return errors.New("denied")
	}
	return nil
}

func (a *recordingAuthorizer) AuthorizeResource(_ context.Context, _ string, _ int64, role, capability, _, resourceID, _ string) error {
	a.calls = append(a.calls, authorizationCall{role: role, capability: capability, resourceID: resourceID})
	if a.deny[role+"|"+capability] {
		return errors.New("denied")
	}
	return nil
}

type lineageReader struct {
	tasks map[int64]TaskLineageRef
}

func (r *lineageReader) GetTaskLineage(_ context.Context, taskID int64) (TaskLineageRef, error) {
	task, ok := r.tasks[taskID]
	if !ok {
		return TaskLineageRef{}, ErrNotFound
	}
	return task, nil
}

// fakeAuthorityReader is the single fake standing in for
// RoleRoutingAuthorityReader: the real Postgres implementation's business
// rules (static-vs-pool derivation, fail-closed conflict/absence,
// candidate materialization) are exercised against real Postgres in
// postgres/integration_test.go, not re-implemented here. This fake just
// hands back whatever result/err the fixture configured, so unit tests can
// focus on what AuthorizedAttemptProvisioner itself does once it has an
// authority: digest/idempotency-key derivation, error propagation, replay.
type fakeAuthorityReader struct {
	result RoleRoutingAuthorityRef
	err    error
}

func (r *fakeAuthorityReader) GetRoleRoutingAuthority(context.Context, string, int64, string) (RoleRoutingAuthorityRef, error) {
	return r.result, r.err
}

type authorizedAttemptFixture struct {
	service    *AuthorizedAttemptProvisioner
	authorizer *recordingAuthorizer
	lineage    *lineageReader
	authority  *fakeAuthorityReader
	store      *fakeAssignmentStore
	now        time.Time
	attempt    TaskAttemptRef
	principal  ExecutionPrincipal
}

func newAuthorizedAttemptFixture(t *testing.T) *authorizedAttemptFixture {
	t.Helper()
	now := mustTime("2026-01-01T00:00:00Z")
	attempt := TaskAttemptRef{
		TaskID: 12, AttemptID: 34, OrganizationID: "explorarte", OrganizationRevisionID: 7,
		AssignedRoleID: "empresa/ceo", TaskStatus: "running", AttemptStatus: "running",
		LeaseHolderID: "41", LeaseExpiresAt: now.Add(30 * time.Minute),
	}
	principal := ExecutionPrincipal{
		ID: 81, OrganizationID: "explorarte", PrincipalKey: "oracle-01/model-runtime-01",
		DispatchActorRoleID: "ingenieria_ia/code-runner", Status: PrincipalActive,
	}
	authorizer := &recordingAuthorizer{}
	catalog := fakeCatalog{revision: 7, roles: map[string]RoleRef{
		"ingenieria_ia/code-runner": {ID: "ingenieria_ia/code-runner", Enabled: true, Executable: true, AuthorityClass: "execution_service"},
	}}
	store := &fakeAssignmentStore{}
	assignments, err := NewAssignmentService(
		"explorarte", authorizer, catalog, fakeTaskReader{ref: attempt},
		fakePrincipalResolver{principal: principal}, store, ClockFunc(func() time.Time { return now }),
		15*time.Minute, time.Hour,
	)
	if err != nil {
		t.Fatal(err)
	}
	lineage := &lineageReader{tasks: map[int64]TaskLineageRef{
		12: {TaskID: 12, OrganizationID: "explorarte", OrganizationRevisionID: 7, RequestedByRoleID: "empresa/ceo", AssignedRoleID: "empresa/ceo", CorrelationID: "executive:campaign", CausationID: "task:4"},
		4:  {TaskID: 4, OrganizationID: "explorarte", OrganizationRevisionID: 7, RequestedByRoleID: "empresa/human", AssignedRoleID: "empresa/ceo", CorrelationID: "executive:campaign", CausationID: "owner:campaign-r17"},
	}}
	authority := &fakeAuthorityReader{result: RoleRoutingAuthorityRef{
		OrganizationID: "explorarte", OrganizationRevisionID: 7, RoleID: "empresa/ceo",
		PolicyID: "ceo-primary", Kind: RoleRoutingStaticBinding,
		ProfileID: "ceo-primary", ModelProfileVersionID: 8,
		AuthorityHash: "bf7b45e7e18cf02ff98a4562537c16b21767fb321bf6a87a48bc2ba5ab24f669",
	}}
	service, err := NewAuthorizedAttemptProvisioner(assignments, lineage, authority, principal.PrincipalKey)
	if err != nil {
		t.Fatal(err)
	}
	return &authorizedAttemptFixture{service: service, authorizer: authorizer, lineage: lineage, authority: authority, store: store, now: now, attempt: attempt, principal: principal}
}

func TestAuthorizedAttemptProvisionerDerivesAndSeparatesAuthorities(t *testing.T) {
	fixture := newAuthorizedAttemptFixture(t)
	result, err := fixture.service.EnsureAuthorizedAssignmentForRunningAttempt(context.Background(), fixture.attempt.TaskID, fixture.attempt.AttemptID)
	if err != nil || result.Reused || result.Assignment.ID == 0 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if len(fixture.authorizer.calls) != 3 {
		t.Fatalf("authorization calls=%+v", fixture.authorizer.calls)
	}
	want := []authorizationCall{
		{role: "ingenieria_ia/code-runner", capability: capabilityProvisionAuthorizedAttempt, resourceID: "task:12/attempt:34"},
		{role: "empresa/human", capability: capabilityAssignmentCreate, resourceID: "task:12/attempt:34"},
		{role: "empresa/human", capability: capabilityAssignmentCreate},
	}
	for i := range want {
		if fixture.authorizer.calls[i] != want[i] {
			t.Fatalf("authorization call %d=%+v want %+v", i, fixture.authorizer.calls[i], want[i])
		}
	}
	if fixture.store.created[0].CreatedByRoleID != "empresa/human" {
		t.Fatalf("created_by=%q, want persisted root requester", fixture.store.created[0].CreatedByRoleID)
	}
}

func TestAuthorizedAttemptProvisionerRejectsBrokenOrForgedAncestry(t *testing.T) {
	tests := map[string]func(*authorizedAttemptFixture){
		"missing parent": func(f *authorizedAttemptFixture) {
			delete(f.lineage.tasks, 4)
		},
		"cross organization parent": func(f *authorizedAttemptFixture) {
			parent := f.lineage.tasks[4]
			parent.OrganizationID = "foreign"
			f.lineage.tasks[4] = parent
		},
		"correlation splice": func(f *authorizedAttemptFixture) {
			parent := f.lineage.tasks[4]
			parent.CorrelationID = "executive:other"
			f.lineage.tasks[4] = parent
		},
		"cycle": func(f *authorizedAttemptFixture) {
			parent := f.lineage.tasks[4]
			parent.CausationID = "task:12"
			f.lineage.tasks[4] = parent
		},
		"forged owner marker without requester": func(f *authorizedAttemptFixture) {
			parent := f.lineage.tasks[4]
			parent.RequestedByRoleID = ""
			f.lineage.tasks[4] = parent
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			fixture := newAuthorizedAttemptFixture(t)
			mutate(fixture)
			_, err := fixture.service.EnsureAuthorizedAssignmentForRunningAttempt(context.Background(), fixture.attempt.TaskID, fixture.attempt.AttemptID)
			if err == nil || (!errors.Is(err, ErrTaskAttemptRejected) && !errors.Is(err, ErrAuthorizationDenied)) {
				t.Fatalf("expected fail-closed ancestry rejection, got %v", err)
			}
			if len(fixture.store.created) != 0 {
				t.Fatal("broken ancestry reached assignment creation")
			}
		})
	}
}

func TestAuthorizedAttemptProvisionerRejectsMissingBinding(t *testing.T) {
	fixture := newAuthorizedAttemptFixture(t)
	fixture.authority.err = ErrNotFound
	_, err := fixture.service.EnsureAuthorizedAssignmentForRunningAttempt(context.Background(), fixture.attempt.TaskID, fixture.attempt.AttemptID)
	if !errors.Is(err, ErrTaskAttemptRejected) {
		t.Fatalf("expected missing binding rejection, got %v", err)
	}
	if len(fixture.store.created) != 0 {
		t.Fatal("missing binding reached assignment creation")
	}
}

func TestAuthorizedAttemptProvisionerReplayRequiresSameEffectiveBinding(t *testing.T) {
	fixture := newAuthorizedAttemptFixture(t)
	first, err := fixture.service.EnsureAuthorizedAssignmentForRunningAttempt(context.Background(), fixture.attempt.TaskID, fixture.attempt.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := fixture.service.EnsureAuthorizedAssignmentForRunningAttempt(context.Background(), fixture.attempt.TaskID, fixture.attempt.AttemptID)
	if err != nil || !second.Reused || second.Assignment.ID != first.Assignment.ID || len(fixture.store.created) != 1 {
		t.Fatalf("exact replay result=%+v err=%v creates=%d", second, err, len(fixture.store.created))
	}

	fixture.authority.result.ModelProfileVersionID++
	fixture.authority.result.AuthorityHash = "af7b45e7e18cf02ff98a4562537c16b21767fb321bf6a87a48bc2ba5ab24f669"
	_, err = fixture.service.EnsureAuthorizedAssignmentForRunningAttempt(context.Background(), fixture.attempt.TaskID, fixture.attempt.AttemptID)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("expected explicit binding replay conflict, got %v", err)
	}
	if len(fixture.store.created) != 1 {
		t.Fatalf("divergent replay created another assignment: %d", len(fixture.store.created))
	}
}

// TestAuthorizedAttemptProvisionerPoolAuthorityWorksWithoutBinding proves
// AuthorizedAttemptProvisioner provisions a running attempt for a
// pool-routed role using only a RoleRoutingAuthorityRef of
// Kind==RoleRoutingPoolPolicy -- no role_model_bindings row anywhere in
// this fixture -- and that the ref it received carries no synthetic
// profile: ProfileID/ModelProfileVersionID stay exactly zero-valued, never
// a "pool:<policy>" stand-in.
func TestAuthorizedAttemptProvisionerPoolAuthorityWorksWithoutBinding(t *testing.T) {
	fixture := newAuthorizedAttemptFixture(t)
	fixture.authority.result = RoleRoutingAuthorityRef{
		OrganizationID: "explorarte", OrganizationRevisionID: 7, RoleID: "empresa/ceo",
		PolicyID: "research.worker.pool", Kind: RoleRoutingPoolPolicy,
		AuthorityHash: "7c9e6679b5b0f7cf8e9a4b3d2c1a0f8e7d6c5b4a3928170695847362514031f",
	}
	if fixture.authority.result.ProfileID != "" || fixture.authority.result.ModelProfileVersionID != 0 {
		t.Fatal("fixture setup error: pool authority must not carry a profile")
	}
	result, err := fixture.service.EnsureAuthorizedAssignmentForRunningAttempt(context.Background(), fixture.attempt.TaskID, fixture.attempt.AttemptID)
	if err != nil || result.Reused || result.Assignment.ID == 0 {
		t.Fatalf("pool authority provisioning failed: result=%+v err=%v", result, err)
	}
}

// TestAuthorizedAttemptStaticDigestsAreByteCompatibleWithThePreUnification
// Format locks authorizedAttemptIdempotencyKey/authorizedAttemptActionDigest
// for Kind==RoleRoutingStaticBinding to the exact byte layout the binding-
// shaped predecessor produced (ProfileID, decimal ModelProfileVersionID,
// hash, NUL-joined, in that order, ahead of the shared
// organization/revision/task/attempt/role/principal/dispatch-actor
// prefix) -- so replay/idempotency for every already-provisioned static
// assignment is unaffected by the pool-authority unification.
func TestAuthorizedAttemptStaticDigestsAreByteCompatibleWithThePreUnificationFormat(t *testing.T) {
	attempt := TaskAttemptRef{TaskID: 12, AttemptID: 34, OrganizationID: "explorarte", OrganizationRevisionID: 7, AssignedRoleID: "empresa/ceo"}
	principal := ExecutionPrincipal{ID: 81, PrincipalKey: "oracle-01/model-runtime-01", DispatchActorRoleID: "ingenieria_ia/code-runner"}
	authority := RoleRoutingAuthorityRef{
		Kind: RoleRoutingStaticBinding, ProfileID: "ceo-primary", ModelProfileVersionID: 8,
		AuthorityHash: "bf7b45e7e18cf02ff98a4562537c16b21767fb321bf6a87a48bc2ba5ab24f669",
	}
	const rootTaskID = int64(4)

	wantIdemBody := strings.Join([]string{
		attempt.OrganizationID, strconv.FormatInt(attempt.OrganizationRevisionID, 10),
		strconv.FormatInt(rootTaskID, 10), strconv.FormatInt(attempt.TaskID, 10), strconv.FormatInt(attempt.AttemptID, 10),
		attempt.AssignedRoleID, strconv.FormatInt(principal.ID, 10), principal.PrincipalKey, principal.DispatchActorRoleID,
		authority.ProfileID, strconv.FormatInt(authority.ModelProfileVersionID, 10), authority.AuthorityHash,
	}, "\x00")
	wantIdem := fmt.Sprintf("authorized-attempt/%d/%d/%s", attempt.TaskID, attempt.AttemptID, sha256Hex([]byte(wantIdemBody))[:32])
	if got := authorizedAttemptIdempotencyKey(rootTaskID, attempt, principal, authority, legacyAuthorizedAttemptMaxInvocations); got != wantIdem {
		t.Fatalf("static idempotency key changed shape: got %q want %q", got, wantIdem)
	}

	const requesterRoleID = "empresa/human"
	wantDigestBody := strings.Join([]string{
		"provision_authorized_attempt", attempt.OrganizationID, strconv.FormatInt(attempt.OrganizationRevisionID, 10),
		strconv.FormatInt(rootTaskID, 10), requesterRoleID, strconv.FormatInt(attempt.TaskID, 10), strconv.FormatInt(attempt.AttemptID, 10),
		attempt.AssignedRoleID, strconv.FormatInt(principal.ID, 10), principal.PrincipalKey, principal.DispatchActorRoleID,
		authority.ProfileID, strconv.FormatInt(authority.ModelProfileVersionID, 10), authority.AuthorityHash,
	}, "\x00")
	wantDigest := sha256Hex([]byte(wantDigestBody))
	if got := authorizedAttemptActionDigest(rootTaskID, attempt, principal, authority, requesterRoleID, legacyAuthorizedAttemptMaxInvocations); got != wantDigest {
		t.Fatalf("static action digest changed shape: got %q want %q", got, wantDigest)
	}
}

// TestAuthorizedAttemptLegacyMax1DigestsAreByteIdenticalToThePreQuotaPolicyFormula
// is CEO_CONVERSATIONAL_DISPATCH_ASSIGNMENT_BOUNDARY_CLOSURE_V1's GAP 1,
// REQUIRED TEST A: a golden/pinned reproduction of the EXACT formula that
// shipped before max_invocations became part of this identity at all (no
// max_invocations field anywhere in the body, "authorized-attempt/..." key
// prefix, "provision_authorized_attempt" digest prefix), reimplemented here
// independently of authorizedAttemptIdempotencyKey/authorizedAttemptActionDigest
// so this test cannot become tautological by construction. Durable
// assignment rows a pre-quota-policy binary already wrote (max_invocations
// was always 1 back then, the only value that could ever be requested) must
// resolve to this exact identity after the upgrade, or a live rollout could
// see a legitimate in-flight attempt's replay rejected as ErrConflict --
// see TestAuthorizedAttemptProvisionerReplaysADurableLegacyMax1Assignment
// for the real-Postgres proof that this pinned formula is what production
// actually needs to match.
func TestAuthorizedAttemptLegacyMax1DigestsAreByteIdenticalToThePreQuotaPolicyFormula(t *testing.T) {
	attempt := TaskAttemptRef{TaskID: 555, AttemptID: 7, OrganizationID: "explorarte", OrganizationRevisionID: 41, AssignedRoleID: "empresa/ceo"}
	principal := ExecutionPrincipal{ID: 202, PrincipalKey: "oracle-org-01/model-runtime-01", DispatchActorRoleID: "ingenieria_ia/code-runner"}
	authority := RoleRoutingAuthorityRef{
		Kind: RoleRoutingStaticBinding, ProfileID: "ceo-primary", ModelProfileVersionID: 3,
		AuthorityHash: "0f1e2d3c4b5a69788796a5b4c3d2e1f00112233445566778899aabbccddeeff",
	}
	const rootTaskID = int64(11)
	const requesterRoleID = "empresa/human"

	legacyIdem := legacyAuthorizedAttemptIdempotencyKeyForTest(rootTaskID, attempt, principal, authority)
	if got := authorizedAttemptIdempotencyKey(rootTaskID, attempt, principal, authority, legacyAuthorizedAttemptMaxInvocations); got != legacyIdem {
		t.Fatalf("legacy max=1 idempotency key diverged from the pinned pre-quota-policy formula: got %q want %q", got, legacyIdem)
	}
	legacyDigest := legacyAuthorizedAttemptActionDigestForTest(rootTaskID, attempt, principal, authority, requesterRoleID)
	if got := authorizedAttemptActionDigest(rootTaskID, attempt, principal, authority, requesterRoleID, legacyAuthorizedAttemptMaxInvocations); got != legacyDigest {
		t.Fatalf("legacy max=1 action digest diverged from the pinned pre-quota-policy formula: got %q want %q", got, legacyDigest)
	}
}

// legacyAuthorizedAttemptIdempotencyKeyForTest and
// legacyAuthorizedAttemptActionDigestForTest are literal, independent
// reimplementations of the formula that shipped before
// CEO_CONVERSATIONAL_DISPATCH_ASSIGNMENT_BOUNDARY_V1 -- copy-pasted as it
// was, never refactored to share code with the current production
// functions. Sharing code with them would make
// TestAuthorizedAttemptLegacyMax1DigestsAreByteIdenticalToThePreQuotaPolicyFormula
// tautological (a change to production's shared helper would silently move
// both sides together); keeping these standalone is what makes that test an
// actual pin.
func legacyAuthorizedAttemptIdempotencyKeyForTest(rootTaskID int64, attempt TaskAttemptRef, principal ExecutionPrincipal, authority RoleRoutingAuthorityRef) string {
	var authorityTail []string
	if authority.Kind == RoleRoutingPoolPolicy {
		authorityTail = []string{"pool_policy", authority.PolicyID, authority.AuthorityHash}
	} else {
		authorityTail = []string{authority.ProfileID, strconv.FormatInt(authority.ModelProfileVersionID, 10), authority.AuthorityHash}
	}
	fields := append([]string{
		attempt.OrganizationID, strconv.FormatInt(attempt.OrganizationRevisionID, 10),
		strconv.FormatInt(rootTaskID, 10), strconv.FormatInt(attempt.TaskID, 10), strconv.FormatInt(attempt.AttemptID, 10),
		attempt.AssignedRoleID, strconv.FormatInt(principal.ID, 10), principal.PrincipalKey, principal.DispatchActorRoleID,
	}, authorityTail...)
	body := strings.Join(fields, "\x00")
	return fmt.Sprintf("authorized-attempt/%d/%d/%s", attempt.TaskID, attempt.AttemptID, sha256Hex([]byte(body))[:32])
}

func legacyAuthorizedAttemptActionDigestForTest(rootTaskID int64, attempt TaskAttemptRef, principal ExecutionPrincipal, authority RoleRoutingAuthorityRef, requesterRoleID string) string {
	var authorityTail []string
	if authority.Kind == RoleRoutingPoolPolicy {
		authorityTail = []string{"pool_policy", authority.PolicyID, authority.AuthorityHash}
	} else {
		authorityTail = []string{authority.ProfileID, strconv.FormatInt(authority.ModelProfileVersionID, 10), authority.AuthorityHash}
	}
	fields := append([]string{
		"provision_authorized_attempt", attempt.OrganizationID, strconv.FormatInt(attempt.OrganizationRevisionID, 10),
		strconv.FormatInt(rootTaskID, 10), requesterRoleID, strconv.FormatInt(attempt.TaskID, 10), strconv.FormatInt(attempt.AttemptID, 10),
		attempt.AssignedRoleID, strconv.FormatInt(principal.ID, 10), principal.PrincipalKey, principal.DispatchActorRoleID,
	}, authorityTail...)
	body := strings.Join(fields, "\x00")
	return sha256Hex([]byte(body))
}

// TestAuthorizedAttemptDigestsAreDomainSeparatedByMaxInvocations proves a
// grant of 1 invocation and a grant of 8 invocations never share an identity
// for the exact same task/attempt/principal/authority/requester tuple --
// required by CEO_CONVERSATIONAL_DISPATCH_ASSIGNMENT_BOUNDARY_V1, since a
// silently-widened quota would otherwise be indistinguishable from the
// original grant at the identity layer.
func TestAuthorizedAttemptDigestsAreDomainSeparatedByMaxInvocations(t *testing.T) {
	attempt := TaskAttemptRef{TaskID: 12, AttemptID: 34, OrganizationID: "explorarte", OrganizationRevisionID: 7, AssignedRoleID: "empresa/ceo"}
	principal := ExecutionPrincipal{ID: 81, PrincipalKey: "oracle-01/model-runtime-01", DispatchActorRoleID: "ingenieria_ia/code-runner"}
	authority := RoleRoutingAuthorityRef{
		Kind: RoleRoutingStaticBinding, ProfileID: "ceo-primary", ModelProfileVersionID: 8,
		AuthorityHash: "bf7b45e7e18cf02ff98a4562537c16b21767fb321bf6a87a48bc2ba5ab24f669",
	}
	const rootTaskID = int64(4)
	const requesterRoleID = "empresa/human"

	idem1 := authorizedAttemptIdempotencyKey(rootTaskID, attempt, principal, authority, 1)
	idem8 := authorizedAttemptIdempotencyKey(rootTaskID, attempt, principal, authority, 8)
	if idem1 == idem8 {
		t.Fatal("idempotency key is identical for max_invocations=1 and max_invocations=8")
	}
	digest1 := authorizedAttemptActionDigest(rootTaskID, attempt, principal, authority, requesterRoleID, 1)
	digest8 := authorizedAttemptActionDigest(rootTaskID, attempt, principal, authority, requesterRoleID, 8)
	if digest1 == digest8 {
		t.Fatal("action digest is identical for max_invocations=1 and max_invocations=8")
	}
}

// TestAuthorizedAttemptPoolDigestsAreDomainSeparatedFromStatic proves a
// pool authority never collides with a static one even when every other
// input (attempt, principal, requester) is identical: the pool body is
// tagged "pool_policy" ahead of PolicyID/AuthorityHash, structurally
// distinct from the static body's ProfileID/ModelProfileVersionID/hash
// layout, and carries no candidate/provider/model.
func TestAuthorizedAttemptPoolDigestsAreDomainSeparatedFromStatic(t *testing.T) {
	attempt := TaskAttemptRef{TaskID: 12, AttemptID: 34, OrganizationID: "explorarte", OrganizationRevisionID: 7, AssignedRoleID: "empresa/ceo"}
	principal := ExecutionPrincipal{ID: 81, PrincipalKey: "oracle-01/model-runtime-01", DispatchActorRoleID: "ingenieria_ia/code-runner"}
	const rootTaskID = int64(4)
	const requesterRoleID = "empresa/human"

	pool := RoleRoutingAuthorityRef{
		Kind: RoleRoutingPoolPolicy, PolicyID: "research.worker.pool",
		AuthorityHash: "7c9e6679b5b0f7cf8e9a4b3d2c1a0f8e7d6c5b4a3928170695847362514031f",
	}
	// The closest a static authority's 3-field tail (ProfileID,
	// ModelProfileVersionID, hash) can get to reusing pool's own field
	// values: the domain-separation tag borrowed as a ProfileID, the same
	// hash. ModelProfileVersionID is int64-typed and PolicyID is free-form
	// text, so the two tails can never be literally identical -- the tag
	// alone is what this test exists to prove matters regardless.
	staticSameFields := RoleRoutingAuthorityRef{
		Kind: RoleRoutingStaticBinding, ProfileID: "pool_policy", ModelProfileVersionID: 0, AuthorityHash: pool.AuthorityHash,
	}

	poolIdem := authorizedAttemptIdempotencyKey(rootTaskID, attempt, principal, pool, 1)
	staticIdem := authorizedAttemptIdempotencyKey(rootTaskID, attempt, principal, staticSameFields, 1)
	if poolIdem == staticIdem {
		t.Fatal("pool and static idempotency keys collided despite the domain-separation tag")
	}
	poolDigest := authorizedAttemptActionDigest(rootTaskID, attempt, principal, pool, requesterRoleID, 1)
	staticDigest := authorizedAttemptActionDigest(rootTaskID, attempt, principal, staticSameFields, requesterRoleID, 1)
	if poolDigest == staticDigest {
		t.Fatal("pool and static action digests collided despite the domain-separation tag")
	}
}

// TestAuthorizedAttemptProvisionerDeniesWhenRequesterLacksCurrentCapability
// covers gap #6 (SECURITY-CRITICAL): a genuinely resolved, unforged root
// provenance is not itself permission. Provisioning must still be denied the
// instant the persisted root's requester role lacks the capability being
// evaluated right now, proving provenance != permission.
func TestAuthorizedAttemptProvisionerDeniesWhenRequesterLacksCurrentCapability(t *testing.T) {
	fixture := newAuthorizedAttemptFixture(t)
	fixture.authorizer.deny = map[string]bool{"empresa/human|" + capabilityAssignmentCreate: true}
	_, err := fixture.service.EnsureAuthorizedAssignmentForRunningAttempt(context.Background(), fixture.attempt.TaskID, fixture.attempt.AttemptID)
	if !errors.Is(err, ErrAuthorizationDenied) {
		t.Fatalf("expected requester authorization denial despite genuine ancestry, got %v", err)
	}
	if len(fixture.store.created) != 0 {
		t.Fatal("denied requester capability reached assignment creation")
	}
	found := false
	for _, call := range fixture.authorizer.calls {
		if call.role == "empresa/human" && call.capability == capabilityAssignmentCreate {
			found = true
		}
	}
	if !found {
		t.Fatal("expected provenance to resolve and the durable requester capability check to actually run before denial")
	}
}

// buildSyntheticAncestryChain rewires the fixture's leaf task (task 12) to
// walk through `hops` additional synthetic ancestor tasks (IDs 9000+) before
// optionally reaching a genuine owner-root marker on the last one. It exists
// to test the ancestry walk's hard depth bound precisely, independent of the
// two real fixture tasks (12, 4) used everywhere else.
//
// Node count including the leaf = hops + 1. resolveTrustedRoot processes one
// node per loop iteration (0-indexed, current<64), so the owner marker is
// only ever reachable on the 64th processed node (hops=63, terminal node is
// node #64) -- one hop more (hops=64, terminal candidate would be node #65)
// is provably unreachable and must fail closed on the depth bound instead.
func buildSyntheticAncestryChain(fixture *authorizedAttemptFixture, hops int, terminateWithOwner bool) {
	const orgID = "explorarte"
	const correlation = "executive:campaign"
	leaf := fixture.lineage.tasks[fixture.attempt.TaskID]
	leaf.CorrelationID = correlation
	if hops == 0 {
		if terminateWithOwner {
			leaf.CausationID = "owner:campaign-r17"
		}
		fixture.lineage.tasks[fixture.attempt.TaskID] = leaf
		return
	}
	leaf.CausationID = "task:9000"
	fixture.lineage.tasks[fixture.attempt.TaskID] = leaf
	for i := 0; i < hops; i++ {
		id := int64(9000 + i)
		node := TaskLineageRef{
			TaskID: id, OrganizationID: orgID, OrganizationRevisionID: 5,
			RequestedByRoleID: "empresa/human", AssignedRoleID: "empresa/ceo",
			CorrelationID: correlation,
		}
		switch {
		case i == hops-1 && terminateWithOwner:
			node.CausationID = "owner:campaign-r17"
		case i == hops-1:
			// One synthetic node beyond the walk's reach. Its own
			// causation is never inspected once the depth bound is
			// hit -- deliberately non-nonsensical to make that
			// explicit, not to encode any real behavior.
			node.CausationID = "owner:unreachable-excess"
		default:
			node.CausationID = fmt.Sprintf("task:%d", 9000+i+1)
		}
		fixture.lineage.tasks[id] = node
	}
}

// TestAuthorizedAttemptProvisionerAcceptsAncestryAtMaximumDepth covers the
// accepted half of gap #10: a chain that reaches the owner root on exactly
// the last node the walk is allowed to process must still succeed.
func TestAuthorizedAttemptProvisionerAcceptsAncestryAtMaximumDepth(t *testing.T) {
	fixture := newAuthorizedAttemptFixture(t)
	buildSyntheticAncestryChain(fixture, maxAuthorizedAttemptAncestryDepth-1, true)
	result, err := fixture.service.EnsureAuthorizedAssignmentForRunningAttempt(context.Background(), fixture.attempt.TaskID, fixture.attempt.AttemptID)
	if err != nil || result.Assignment.ID == 0 {
		t.Fatalf("ancestry at the exact depth bound was rejected: result=%+v err=%v", result, err)
	}
}

// TestAuthorizedAttemptProvisionerRejectsAncestryBeyondMaximumDepth covers
// the rejected half of gap #10: a chain one hop longer than the maximum,
// with a real (never-consulted) node beyond the bound, must fail closed --
// no panic, no partial assignment -- rather than silently succeeding or
// crashing on the extra hop.
func TestAuthorizedAttemptProvisionerRejectsAncestryBeyondMaximumDepth(t *testing.T) {
	fixture := newAuthorizedAttemptFixture(t)
	buildSyntheticAncestryChain(fixture, maxAuthorizedAttemptAncestryDepth, false)
	_, err := fixture.service.EnsureAuthorizedAssignmentForRunningAttempt(context.Background(), fixture.attempt.TaskID, fixture.attempt.AttemptID)
	if !errors.Is(err, ErrTaskAttemptRejected) {
		t.Fatalf("expected fail-closed depth-limit rejection, got %v", err)
	}
	if len(fixture.store.created) != 0 {
		t.Fatal("ancestry beyond the depth bound reached assignment creation")
	}
}

// buildMixedRevisionAncestryChain inserts a genuine three-level lineage --
// root (task 4) at one organization revision, an intermediate task (6) at a
// second, and the running leaf (task 12, already revision-current per the
// base fixture) at a third -- to prove the ancestry walk demonstrates
// provenance without requiring the whole lineage to share one revision.
func buildMixedRevisionAncestryChain(fixture *authorizedAttemptFixture) {
	fixture.lineage.tasks[6] = TaskLineageRef{
		TaskID: 6, OrganizationID: "explorarte", OrganizationRevisionID: 6,
		RequestedByRoleID: "empresa/ceo", AssignedRoleID: "empresa/ceo",
		CorrelationID: "executive:campaign", CausationID: "task:4",
	}
	root := fixture.lineage.tasks[4]
	root.OrganizationRevisionID = 5
	fixture.lineage.tasks[4] = root
	leaf := fixture.lineage.tasks[12]
	leaf.CausationID = "task:6"
	fixture.lineage.tasks[12] = leaf
}

// TestAuthorizedAttemptProvisionerAllowsMixedRevisionAncestry covers gap #20
// (REVISION SEMANTICS GATE): root R5 -> child R6 -> target R7, with valid
// CURRENT authority and CURRENT binding, must provision successfully.
// Ancestry demonstrates provenance; it does not require lineage-wide
// revision uniformity, which the codebase never guaranteed in the first
// place (every task, root or child, always stamps whatever revision is live
// at its own creation instant).
func TestAuthorizedAttemptProvisionerAllowsMixedRevisionAncestry(t *testing.T) {
	fixture := newAuthorizedAttemptFixture(t)
	buildMixedRevisionAncestryChain(fixture)
	result, err := fixture.service.EnsureAuthorizedAssignmentForRunningAttempt(context.Background(), fixture.attempt.TaskID, fixture.attempt.AttemptID)
	if err != nil || result.Reused || result.Assignment.ID == 0 {
		t.Fatalf("mixed-revision ancestry rejected: result=%+v err=%v", result, err)
	}
	if result.Assignment.CreatedByRoleID != "empresa/human" {
		t.Fatalf("created_by=%q, want persisted root requester", result.Assignment.CreatedByRoleID)
	}
}

// TestAuthorizedAttemptProvisionerMixedRevisionDoesNotPreserveRevokedAuthority
// covers gap #21: the exact security property motivating the fix. The root
// requester's authority existed at revision 5, the lineage is structurally
// genuine end to end, but the capability is evaluated at the CURRENT
// revision -- if it was revoked by then, provisioning must deny. Removing
// the per-hop revision-equality check must not let stale authority survive.
func TestAuthorizedAttemptProvisionerMixedRevisionDoesNotPreserveRevokedAuthority(t *testing.T) {
	fixture := newAuthorizedAttemptFixture(t)
	buildMixedRevisionAncestryChain(fixture)
	fixture.authorizer.deny = map[string]bool{"empresa/human|" + capabilityAssignmentCreate: true}
	_, err := fixture.service.EnsureAuthorizedAssignmentForRunningAttempt(context.Background(), fixture.attempt.TaskID, fixture.attempt.AttemptID)
	if !errors.Is(err, ErrAuthorizationDenied) {
		t.Fatalf("expected current-revision authorization denial despite structurally valid mixed-revision ancestry, got %v", err)
	}
	if len(fixture.store.created) != 0 {
		t.Fatal("revoked authority reached assignment creation despite mixed-revision ancestry")
	}
}
