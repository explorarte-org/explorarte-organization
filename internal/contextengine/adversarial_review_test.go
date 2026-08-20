package contextengine

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/designfreeze"
	"github.com/Mireuz13/explorarte-organization/internal/designreview"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
)

// The six canonical documents named below are the ones that actually reached
// the provider and got the first real Grok invocation denied at egress. They
// are named literally so a future change that reintroduces any of them fails
// with the name of the document it let through.

func TestAdversarialSelectorRequiresEveryDurableFact(t *testing.T) {
	complete := adversarialRequest()
	if err := validateAdversarialSelector(complete); err != nil {
		t.Fatalf("the exact selector must be accepted: %v", err)
	}
	for _, tc := range []struct {
		name    string
		mutate  func(*BuildRequest)
		missing string
	}{
		{"purpose absent", func(r *BuildRequest) { r.Purpose = "" }, "purpose"},
		{"purpose wrong", func(r *BuildRequest) { r.Purpose = "adversarial-review" }, "purpose"},
		{"task class absent", func(r *BuildRequest) { r.TaskClass = "" }, "task_class"},
		{"task class wrong", func(r *BuildRequest) { r.TaskClass = "coordination.department_review" }, "task_class"},
		{"execution purpose absent", func(r *BuildRequest) { r.ExecutionPurpose = "" }, "execution_purpose"},
		{"execution purpose underscored", func(r *BuildRequest) { r.ExecutionPurpose = "adversarial_review" }, "execution_purpose"},
		{"actor role absent", func(r *BuildRequest) { r.ActorRoleID = "" }, "actor_role_id"},
		{"actor role is another auditor", func(r *BuildRequest) { r.ActorRoleID = "investigacion/analista" }, "actor_role_id"},
		{"actor unit absent", func(r *BuildRequest) { r.ActorUnitID = "" }, "actor_unit_id"},
		{"actor unit wrong", func(r *BuildRequest) { r.ActorUnitID = "ingenieria_ia" }, "actor_unit_id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := adversarialRequest()
			tc.mutate(&request)
			if !adversarialReviewRequested(request) {
				t.Fatal("a request carrying part of the selector must still commit to strict validation, never fall back to the ordinary assembly")
			}
			err := validateAdversarialSelector(request)
			if err == nil {
				t.Fatal("partial selector was accepted")
			}
			if !strings.Contains(err.Error(), tc.missing) {
				t.Fatalf("rejection must name the offending fact, got %v", err)
			}
		})
	}
}

func TestOrdinaryRequestsAreUntouchedByTheAdversarialMode(t *testing.T) {
	for _, request := range []BuildRequest{
		{ActorRoleID: "ingenieria_ia/qa", Purpose: "department_plan", TaskClass: "department.plan"},
		{ActorRoleID: "empresa/ceo", Purpose: "executive_plan"},
		{ActorRoleID: "investigacion/analista", Purpose: "research", ActorUnitID: "investigacion"},
	} {
		if adversarialReviewRequested(request) {
			t.Fatalf("ordinary request %+v was captured by the adversarial mode", request)
		}
	}
}

// TestAdversarialBuildProducesRestrictedDurableSnapshot exercises the real
// Service.Build, not the helpers, because the property that matters is what
// ends up in the durable Snapshot the Harness will send.
func TestAdversarialBuildProducesRestrictedDurableSnapshot(t *testing.T) {
	f := newAdversarialFixture(t, reviewTaskPayload(t))
	result, err := f.service.Build(context.Background(), adversarialRequest())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	kinds := map[SourceKind]int{}
	for _, segment := range result.Snapshot.Segments {
		kinds[segment.SourceKind]++
		if segment.DataClass != DataPublic && segment.DataClass != DataSanitized {
			t.Fatalf("segment %s carries %q into an adversarial snapshot", segment.SourceKind, segment.DataClass)
		}
		for _, forbidden := range []string{
			"role-catalog.yaml", "capability-matrix.yaml", "model-routing.yaml",
			"decisions-required.yaml", "cell-boundaries.yaml", "AGENT.md",
		} {
			if strings.Contains(segment.SourceReference, forbidden) {
				t.Fatalf("canonical document %q reached the adversarial snapshot", forbidden)
			}
		}
	}
	if kinds[SourceRoleProfile] != 1 {
		t.Fatalf("reviewer contract must be present exactly once, got %d", kinds[SourceRoleProfile])
	}
	if kinds[SourceTaskContext] != 1 {
		t.Fatalf("review bundle must be present exactly once, got %d", kinds[SourceTaskContext])
	}
	if len(kinds) != 2 {
		t.Fatalf("the admissible set is the contract and the bundle, got %v", kinds)
	}

	bundleSegment := segmentByKind(t, result.Snapshot, SourceTaskContext)
	if !strings.Contains(string(bundleSegment.Content), "a candidate design under review") {
		t.Fatalf("the real bundle is missing from the snapshot: %s", bundleSegment.Content)
	}
	// The rendered task record is read for facts and never forwarded.
	for _, leaked := range []string{"acceptance_criteria", "attempts", "task_id", "Review this sanitized"} {
		if strings.Contains(string(bundleSegment.Content), leaked) {
			t.Fatalf("task record field %q survived into the sanitized source", leaked)
		}
	}

	again, err := f.service.Build(context.Background(), adversarialRequest())
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if again.Snapshot.ID != result.Snapshot.ID || again.Snapshot.RenderedHash != result.Snapshot.RenderedHash {
		t.Fatalf("the same request must resolve to the same durable snapshot: %d/%s vs %d/%s",
			again.Snapshot.ID, again.Snapshot.RenderedHash, result.Snapshot.ID, result.Snapshot.RenderedHash)
	}
}

// TestAdversarialBuildRejectsATaskThatIsNotTheReview is the regression that
// matters most. GetTaskContext only proves the task is assigned to the actor,
// so a task assigned to the reviewer for any other reason must not become an
// egress-safe source merely by being asked for under this purpose.
func TestAdversarialBuildRejectsATaskThatIsNotTheReview(t *testing.T) {
	const secret = "pxid TH001-PX-001 attends on thursdays"
	payload, err := json.Marshal(map[string]any{
		"schema_version":   "task-context.v1",
		"task_id":          99,
		"assigned_role_id": AdversarialReviewerRoleID,
		"assigned_unit_id": AdversarialReviewerUnitID,
		"title":            "Summarise the clinical backlog",
		"instructions":     "Summarise the backlog.\n\n{\"clinical_notes\":\"" + secret + "\"}",
		"status":           "ready",
	})
	if err != nil {
		t.Fatal(err)
	}
	f := newAdversarialFixture(t, payload)
	result, err := f.service.Build(context.Background(), adversarialRequest())
	if err == nil {
		t.Fatalf("a task that is not the adversarial review must be refused, built snapshot %d", result.Snapshot.ID)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatal("the rejection must not quote the content it refused")
	}
	for _, snapshot := range f.store.byID {
		for _, segment := range snapshot.Segments {
			if strings.Contains(string(segment.Content), secret) {
				t.Fatal("organizational content reached a DataSanitized segment")
			}
		}
	}
}

func TestAdversarialEgressSafeRefusesInadmissibleClasses(t *testing.T) {
	for _, class := range []DataClass{DataOrganizational, DataSecret, DataClinical, DataClass("")} {
		err := assertAdversarialEgressSafe([]SourceRecord{{Kind: SourceRAGEvidence, Reference: "evidence:1", DataClass: class}})
		if err == nil {
			t.Fatalf("class %q was admitted", class)
		}
		if !errors.Is(err, ErrRejected) {
			t.Fatalf("want a rejection, got %T: %v", err, err)
		}
	}
}

// ---- fixture ---------------------------------------------------------

func adversarialRequest() BuildRequest {
	return BuildRequest{
		OrganizationID:         "explorarte",
		OrganizationRevisionID: 1,
		ActorRoleID:            AdversarialReviewerRoleID,
		ActorUnitID:            AdversarialReviewerUnitID,
		Purpose:                AdversarialReviewPurpose,
		TaskClass:              AdversarialReviewTaskClass,
		ExecutionPurpose:       AdversarialReviewExecutionPurpose,
		TaskRef:                "task:41",
		IdempotencyKey:         "adversarial-review-41",
	}
}

func reviewTaskPayload(t *testing.T) []byte {
	t.Helper()
	bundle, err := designreview.Bundle{
		OwnerRequirements:       []string{"the organization rewrites its own next steps"},
		CandidateDesign:         "a candidate design under review",
		ArchitectureConstraints: []string{"one state machine"},
		AuthorityConstraints:    []string{"the reviewer publishes findings only"},
		UnresolvedDecisions:     []string{},
		EvidenceRefs:            []string{},
		Design:                  designfreeze.Design{ID: "design:root:41", Version: "v1", Digest: "sha256:deadbeef"},
	}.Encode()
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]any{
		"schema_version":      "task-context.v1",
		"task_id":             41,
		"assigned_role_id":    AdversarialReviewerRoleID,
		"assigned_unit_id":    AdversarialReviewerUnitID,
		"title":               "Adversarial design review: design:root:41 v1",
		"instructions":        "Review this sanitized candidate design adversarially and return AdversarialReview JSON.\n\n" + string(bundle),
		"acceptance_criteria": []string{"Return strict AdversarialReview JSON"},
		"status":              "leased",
		"attempts":            1,
	})
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

type adversarialFixture struct {
	service Service
	store   *memoryStore
}

func newAdversarialFixture(t *testing.T, taskPayload []byte) *adversarialFixture {
	t.Helper()
	profilePath := "investigacion/revisor_adversarial/PERFIL.md"
	agentPath := "investigacion/AGENT.md"
	memory := "investigacion"
	reg := &fakeRegistry{
		organization: registry.Organization{ID: "explorarte", OwnerRoleID: "empresa/human", CurrentRevision: 1},
		revision:     &registry.Revision{ID: 1, CanonicalHash: DigestMarkdown([]byte("registry"))},
		unit:         registry.Unit{OrganizationID: "explorarte", ID: AdversarialReviewerUnitID, AgentPath: &agentPath},
		role: registry.Role{
			OrganizationID: "explorarte", ID: AdversarialReviewerRoleID, UnitID: AdversarialReviewerUnitID,
			RoleSlug: "revisor_adversarial", ProfilePath: &profilePath, DepartmentAgentPath: &agentPath,
			MemoryDomain: &memory, Enabled: true, Executable: true, SourceRevision: 1,
		},
	}
	docs := &fakeDocuments{docs: map[string]LoadedDocument{
		"AGENT.md":  agentDoc("AGENT.md"),
		agentPath:   agentDoc(agentPath),
		profilePath: profileDoc(profilePath, AdversarialReviewerUnitID, "revisor_adversarial", memory),
	}}
	// The canonical bundle is fully populated on purpose: the test asserts
	// these documents are never resolved, which is only meaningful if they
	// were available to be resolved.
	sources := make([]CanonicalSource, 0, 6)
	for _, name := range []string{
		"docs/canonical/role-catalog.yaml", "docs/canonical/capability-matrix.yaml",
		"docs/canonical/model-routing.yaml", "docs/canonical/decisions-required.yaml",
		"docs/canonical/cell-boundaries.yaml", "AGENT.md",
	} {
		body := []byte("organizational body of " + name)
		sources = append(sources, CanonicalSource{
			LogicalName: name, Version: "0.1", Tier: TierImmutableSafety,
			InstructionClass: InstructionImmutableConstraint, TrustClass: TrustImmutable,
			DataClass: DataOrganizational, Content: body,
			ContentHash: DigestMarkdown(body), SemanticHash: DigestMarkdown(body),
		})
	}
	canonical := &fakeCanonical{bundle: CanonicalBundle{
		PrecedenceHash: DigestMarkdown([]byte("precedence")),
		BundleHash:     DigestMarkdown([]byte("bundle")),
		Sources:        sources,
	}}
	store := newMemoryStore()
	service, err := NewService(
		ServiceConfig{OrganizationAgentPath: "AGENT.md", MaxTotalBytes: 65536, MaxSegmentBytes: 8192, MaxSegments: 64, MaxSkills: 16, MaxMemorySegments: 32, MaxRAGSegments: 20},
		reg, docs, canonical, NoopOwnerConstraintProvider{}, UnavailableMemoryProvider{}, emptySkillProvider{},
		UnavailableProjectProvider{}, fakeTaskProvider{payload: taskPayload}, UnavailableRAGProvider{},
		NewAssembler(), NewRenderer(), store, fixedClock{time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)},
	)
	if err != nil {
		t.Fatal(err)
	}
	return &adversarialFixture{service: service, store: store}
}

type fakeTaskProvider struct{ payload []byte }

func (f fakeTaskProvider) GetTaskContext(ctx context.Context, request BuildRequest) (*SourceRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if request.TaskRef == "" {
		return nil, nil
	}
	return &SourceRecord{
		Kind: SourceTaskContext, Reference: request.TaskRef, Version: "task.v1:1:hash",
		AuthorityTier: TierTask, InstructionClass: InstructionScoped, TrustClass: TrustUntrusted,
		DataClass: DataOrganizational, Content: append([]byte(nil), f.payload...),
		ContentHash: DigestCanonicalBytes(f.payload), Included: true, Relevance: 1, ProviderPriority: 1,
	}, nil
}

func segmentByKind(t *testing.T, snapshot Snapshot, kind SourceKind) Segment {
	t.Helper()
	for _, segment := range snapshot.Segments {
		if segment.SourceKind == kind {
			return segment
		}
	}
	t.Fatalf("snapshot carries no %s segment", kind)
	return Segment{}
}

func (f fakeTaskProvider) ValidateVersion(context.Context, string, SourceRecord) error { return nil }
