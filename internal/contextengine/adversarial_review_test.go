package contextengine

import (
	"bytes"
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

// TestAdversarialSanitizedSourceIsNotValidatedAsATaskRecord is the regression
// for the defect the permissive fake used to hide. The sanitized bundle and
// the rendered task record have deliberately different hashes, so routing the
// restricted source set through TaskContextProvider.ValidateVersion would fail
// EVERY adversarial review with task_version_drift. The build must succeed.
func TestAdversarialSanitizedSourceIsNotValidatedAsATaskRecord(t *testing.T) {
	f := newAdversarialFixture(t, reviewTaskPayload(t))
	result, err := f.service.Build(context.Background(), adversarialRequest())
	if err != nil {
		t.Fatalf("the sanitized representation must have its own validation rule, got %v", err)
	}
	bundle := segmentByKind(t, result.Snapshot, SourceTaskContext)
	rendered := fakeTaskProvider{payload: f.payload}.record("task:41")
	if bundle.ContentHash == rendered.ContentHash {
		t.Fatal("the sanitized bundle must not hash equal to the rendered organizational record; if it does, the record leaked")
	}
}

// TestAdversarialValidationDetectsBundleDrift proves the replacement rule is a
// real check and not merely a way to skip validation. What must drift is the
// BUNDLE changing, because the bundle is the entire content of this source.
func TestAdversarialValidationDetectsBundleDrift(t *testing.T) {
	f := newAdversarialFixture(t, reviewTaskPayload(t))
	f.service.(*contextService).tasks = &driftingTaskProvider{inner: fakeTaskProvider{payload: f.payload}, mutate: f.payload}
	if _, err := f.service.Build(context.Background(), adversarialRequest()); err == nil {
		t.Fatal("a review bundle that changed mid-build must drift")
	} else if ReasonOf(err) != ReasonTaskVersionDrift {
		t.Fatalf("want task_version_drift, got %s (%v)", ReasonOf(err), err)
	}
}

// TestTaskChurnThatLeavesTheBundleIdenticalIsNotDrift is the production
// regression. The durable task row's version increments on things this source
// deliberately does not contain -- status transitions, attempt counts -- and
// an earlier revision compared that version, so every adversarial review died
// with "task context N version drift" the moment its task started running.
func TestTaskChurnThatLeavesTheBundleIdenticalIsNotDrift(t *testing.T) {
	f := newAdversarialFixture(t, reviewTaskPayload(t))
	f.service.(*contextService).tasks = &churningTaskProvider{inner: fakeTaskProvider{payload: f.payload}}
	if _, err := f.service.Build(context.Background(), adversarialRequest()); err != nil {
		t.Fatalf("a task whose status moved but whose bundle is byte-identical must not drift: %v", err)
	}
}

// TestDurableAdversarialSnapshotRevalidates covers the SECOND validation path.
// Service.Build revalidates what it just resolved; Service.Validate
// revalidates a stored snapshot later, and the Executive calls it before every
// dispatch. Routing only the first one left the second handing this snapshot
// to the generic Tasks provider, which is what actually blocked root 138.
func TestDurableAdversarialSnapshotRevalidates(t *testing.T) {
	f := newAdversarialFixture(t, reviewTaskPayload(t))
	built, err := f.service.Build(context.Background(), adversarialRequest())
	if err != nil {
		t.Fatal(err)
	}
	// Between build and revalidation the task starts running, exactly as it
	// does in production between the snapshot being created and the dispatch
	// that consumes it.
	f.service.(*contextService).tasks = &churningTaskProvider{inner: fakeTaskProvider{payload: f.payload}}
	validation, err := f.service.Validate(context.Background(), built.Snapshot.ID)
	if err != nil {
		t.Fatalf("revalidating a durable adversarial snapshot must not error: %v", err)
	}
	if !validation.Valid {
		t.Fatalf("a durable adversarial snapshot must revalidate, got drift %+v", validation.Drift)
	}
}

// TestAdversarialValidationDetectsReviewerContractDrift covers the other half:
// the profile is reloaded and compared, which is why pinning its PATH is
// enough and pinning a literal content hash in Go would be redundant.
func TestAdversarialValidationDetectsReviewerContractDrift(t *testing.T) {
	f := newAdversarialFixture(t, reviewTaskPayload(t))
	service := f.service.(*contextService)
	service.documents = &driftingDocuments{inner: f.docs}
	if _, err := f.service.Build(context.Background(), adversarialRequest()); err == nil {
		t.Fatal("a reviewer contract edited mid-build must drift")
	} else if ReasonOf(err) != ReasonProfileDrift {
		t.Fatalf("want profile_drift, got %s (%v)", ReasonOf(err), err)
	}
}

// TestAdversarialRejectsAForeignReviewerContract covers the pinned path: the
// classification decision is about the document at that path, so a registry
// that points the reviewer somewhere else must not get its bytes sanitized.
func TestAdversarialRejectsAForeignReviewerContract(t *testing.T) {
	f := newAdversarialFixture(t, reviewTaskPayload(t))
	service := f.service.(*contextService)
	other := "investigacion/analista/PERFIL.md"
	f.docs.docs[other] = profileDoc(other, AdversarialReviewerUnitID, "revisor_adversarial", AdversarialReviewerUnitID)
	service.registry.(*fakeRegistry).role.ProfilePath = &other
	if _, err := f.service.Build(context.Background(), adversarialRequest()); err == nil {
		t.Fatal("only the pinned reviewer contract may be classified sanitized")
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
	docs    *fakeDocuments
	payload *[]byte
}

func newAdversarialFixture(t *testing.T, taskPayload []byte) *adversarialFixture {
	t.Helper()
	profilePath := adversarialReviewerProfilePath
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
		UnavailableProjectProvider{}, fakeTaskProvider{payload: &taskPayload}, UnavailableRAGProvider{},
		NewAssembler(), NewRenderer(), store, fixedClock{time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)},
	)
	if err != nil {
		t.Fatal(err)
	}
	return &adversarialFixture{service: service, store: store, docs: docs, payload: &taskPayload}
}

// fakeTaskProvider reproduces the PRODUCTION provider's semantics, including
// the part that matters most here: ValidateVersion rebuilds the full
// organizational task record and demands an identical ContentHash. An earlier
// revision of this fake returned nil unconditionally, which is exactly why a
// real defect -- the generic validator reporting drift on every adversarial
// review, because the sanitized bundle's hash can never equal the rendered
// record's -- passed a green suite.
type fakeTaskProvider struct{ payload *[]byte }

func (f fakeTaskProvider) record(reference string) SourceRecord {
	payload := *f.payload
	return SourceRecord{
		Kind: SourceTaskContext, Reference: reference, Version: "task.v1:" + DigestCanonicalBytes(payload)[:8],
		AuthorityTier: TierTask, InstructionClass: InstructionScoped, TrustClass: TrustUntrusted,
		DataClass: DataOrganizational, Content: append([]byte(nil), payload...),
		ContentHash: DigestCanonicalBytes(payload), Included: true, Relevance: 1, ProviderPriority: 1,
	}
}

func (f fakeTaskProvider) GetTaskContext(ctx context.Context, request BuildRequest) (*SourceRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if request.TaskRef == "" {
		return nil, nil
	}
	record := f.record(request.TaskRef)
	return &record, nil
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

func (f fakeTaskProvider) ValidateVersion(_ context.Context, _ string, source SourceRecord) error {
	current := f.record(source.Reference)
	if current.ContentHash != source.ContentHash || current.Version != source.Version {
		return Reject(ReasonTaskVersionDrift, source.Reference, "task context changed during context build")
	}
	return nil
}

// driftingTaskProvider changes the review bundle after the first read, which
// is what a design genuinely changing mid-build looks like from here.
type driftingTaskProvider struct {
	inner  fakeTaskProvider
	mutate *[]byte
	read   bool
}

func (d *driftingTaskProvider) GetTaskContext(ctx context.Context, request BuildRequest) (*SourceRecord, error) {
	record, err := d.inner.GetTaskContext(ctx, request)
	if err != nil || record == nil {
		return record, err
	}
	if !d.read {
		d.read = true
		// Change the CANDIDATE DESIGN itself, not the envelope. Perturbing
		// the payload's bytes would leave the decoded bundle identical, and
		// correctly would not be drift.
		moved := bytes.Replace(*d.mutate, []byte("a candidate design under review"), []byte("a DIFFERENT candidate design"), 1)
		if bytes.Equal(moved, *d.mutate) {
			panic("drift fixture no longer changes the bundle; it would assert nothing")
		}
		*d.mutate = moved
	}
	return record, nil
}

func (d *driftingTaskProvider) ValidateVersion(ctx context.Context, actor string, source SourceRecord) error {
	return d.inner.ValidateVersion(ctx, actor, source)
}

// driftingDocuments edits the document AFTER the first read, so Build sees the
// original and Validate sees the edit. Mutating it up front would prove
// nothing: both halves would agree.
type driftingDocuments struct {
	inner *fakeDocuments
	read  bool
}

func (d *driftingDocuments) Load(ctx context.Context, path string, limit int64) (LoadedDocument, error) {
	doc, err := d.inner.Load(ctx, path, limit)
	if err != nil {
		return doc, err
	}
	if !d.read {
		d.read = true
		return doc, nil
	}
	normalized := append(append([]byte(nil), doc.Normalized...), 0x0a)
	return LoadedDocument{Path: doc.Path, Normalized: normalized, Body: doc.Body, Hash: DigestMarkdown(normalized), Frontmatter: doc.Frontmatter}, nil
}

// churningTaskProvider advances the durable task the way execution does --
// status and attempts -- while leaving the review bundle in the instructions
// byte-identical.
type churningTaskProvider struct {
	inner fakeTaskProvider
	reads int
}

func (c *churningTaskProvider) GetTaskContext(ctx context.Context, request BuildRequest) (*SourceRecord, error) {
	record, err := c.inner.GetTaskContext(ctx, request)
	if err != nil || record == nil {
		return record, err
	}
	c.reads++
	if c.reads > 1 {
		moved := bytes.Replace(record.Content, []byte(`"status":"leased"`), []byte(`"status":"running"`), 1)
		moved = bytes.Replace(moved, []byte(`"attempts":1`), []byte(`"attempts":2`), 1)
		record.Content = moved
		record.ContentHash = DigestCanonicalBytes(moved)
		record.Version = "task.v1:" + DigestCanonicalBytes(moved)[:8]
	}
	return record, nil
}

func (c *churningTaskProvider) ValidateVersion(ctx context.Context, actor string, source SourceRecord) error {
	return c.inner.ValidateVersion(ctx, actor, source)
}
