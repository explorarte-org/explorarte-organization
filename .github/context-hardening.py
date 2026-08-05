from pathlib import Path

path = Path("internal/contextengine/service.go")
text = path.read_text()

replacements = [
    (
        '''\trole, err := s.registry.GetRole(ctx, snapshot.OrganizationID, snapshot.ActorRoleID)
\tif err != nil {
\t\tdrift = append(drift, DriftFinding{ReasonCode: string(ReasonRoleNotFound), Reference: snapshot.ActorRoleID})
\t} else if role.RetiredAt != nil || !role.Enabled || (!role.Executable && role.ID != organization.OwnerRoleID) {
\t\tdrift = append(drift, DriftFinding{ReasonCode: string(ReasonRoleRetired), Reference: role.ID})
\t}
''',
        '''\trole, err := s.registry.GetRole(ctx, snapshot.OrganizationID, snapshot.ActorRoleID)
\tif err != nil {
\t\tif !errors.Is(err, registry.ErrNotFound) {
\t\t\treturn SnapshotValidation{}, fmt.Errorf("validate actor role: %w", err)
\t\t}
\t\tdrift = append(drift, DriftFinding{ReasonCode: string(ReasonRoleNotFound), Reference: snapshot.ActorRoleID})
\t} else if role.RetiredAt != nil || !role.Enabled || (!role.Executable && role.ID != organization.OwnerRoleID) {
\t\tdrift = append(drift, DriftFinding{ReasonCode: string(ReasonRoleRetired), Reference: role.ID})
\t}
''',
    ),
    (
        '''\t\tcase SourceOrganizationAgent, SourceDepartmentAgent, SourceRoleProfile:
\t\t\tdoc, loadErr := s.documents.Load(ctx, segment.SourceReference, int64(s.config.MaxSegmentBytes))
\t\t\tif loadErr != nil {
\t\t\t\tdrift = append(drift, DriftFinding{ReasonCode: string(driftForKind(segment.SourceKind)), SourceKind: segment.SourceKind, Reference: segment.SourceReference})
\t\t\t\tcontinue
\t\t\t}
''',
        '''\t\tcase SourceOrganizationAgent, SourceDepartmentAgent, SourceRoleProfile:
\t\t\tdoc, loadErr := s.documents.Load(ctx, segment.SourceReference, int64(s.config.MaxSegmentBytes))
\t\t\tif loadErr != nil {
\t\t\t\tif IsOperational(loadErr) {
\t\t\t\t\treturn SnapshotValidation{}, fmt.Errorf("validate source %s: %w", segment.SourceReference, loadErr)
\t\t\t\t}
\t\t\t\tdrift = append(drift, DriftFinding{ReasonCode: string(driftForKind(segment.SourceKind)), SourceKind: segment.SourceKind, Reference: segment.SourceReference})
\t\t\t\tcontinue
\t\t\t}
''',
    ),
    (
        '''\t\tcase SourceApprovedSkill:
\t\t\trecord, getErr := s.skills.GetActiveForRole(ctx, snapshot.OrganizationID, snapshot.ActorRoleID, segment.SourceReference)
\t\t\tif getErr != nil {
\t\t\t\tdrift = append(drift, DriftFinding{ReasonCode: string(ReasonSkillStateDrift), SourceKind: segment.SourceKind, Reference: segment.SourceReference})
\t\t\t\tcontinue
\t\t\t}
\t\t\tif getErr = s.skills.ValidateVersion(ctx, record); getErr != nil || record.SourceHash != segment.ContentHash {
\t\t\t\tdrift = append(drift, DriftFinding{ReasonCode: string(ReasonSkillSourceDrift), SourceKind: segment.SourceKind, Reference: segment.SourceReference})
\t\t\t}
''',
        '''\t\tcase SourceApprovedSkill:
\t\t\trecord, getErr := s.skills.GetActiveForRole(ctx, snapshot.OrganizationID, snapshot.ActorRoleID, segment.SourceReference)
\t\t\tif getErr != nil {
\t\t\t\tif IsOperational(getErr) {
\t\t\t\t\treturn SnapshotValidation{}, fmt.Errorf("validate skill state %s: %w", segment.SourceReference, getErr)
\t\t\t\t}
\t\t\t\tdrift = append(drift, DriftFinding{ReasonCode: string(ReasonSkillStateDrift), SourceKind: segment.SourceKind, Reference: segment.SourceReference})
\t\t\t\tcontinue
\t\t\t}
\t\t\tif getErr = s.skills.ValidateVersion(ctx, record); getErr != nil {
\t\t\t\tif IsOperational(getErr) {
\t\t\t\t\treturn SnapshotValidation{}, fmt.Errorf("validate skill source %s: %w", segment.SourceReference, getErr)
\t\t\t\t}
\t\t\t\tdrift = append(drift, DriftFinding{ReasonCode: string(ReasonSkillSourceDrift), SourceKind: segment.SourceKind, Reference: segment.SourceReference})
\t\t\t} else if record.SourceHash != segment.ContentHash {
\t\t\t\tdrift = append(drift, DriftFinding{ReasonCode: string(ReasonSkillSourceDrift), SourceKind: segment.SourceKind, Reference: segment.SourceReference, Expected: segment.ContentHash, Actual: record.SourceHash})
\t\t\t}
''',
    ),
]

dynamic = [
    ("SourceApprovedMemory", "s.memory", "ReasonMemoryVersionDrift", "memory"),
    ("SourceProjectContext", "s.projects", "ReasonProjectVersionDrift", "project"),
    ("SourceTaskContext", "s.tasks", "ReasonTaskVersionDrift", "task"),
    ("SourceRAGEvidence", "s.rag", "ReasonRAGVersionDrift", "RAG evidence"),
]
for kind, provider, reason, label in dynamic:
    old = f'''\t\tcase {kind}:
\t\t\tif validateErr := {provider}.ValidateVersion(ctx, segmentToRecord(segment)); validateErr != nil {{
\t\t\t\tdrift = append(drift, DriftFinding{{ReasonCode: string({reason}), SourceKind: segment.SourceKind, Reference: segment.SourceReference}})
\t\t\t}}
'''
    new = f'''\t\tcase {kind}:
\t\t\tif validateErr := {provider}.ValidateVersion(ctx, segmentToRecord(segment)); validateErr != nil {{
\t\t\t\tif IsOperational(validateErr) {{
\t\t\t\t\treturn SnapshotValidation{{}}, fmt.Errorf("validate {label} %s: %w", segment.SourceReference, validateErr)
\t\t\t\t}}
\t\t\t\tdrift = append(drift, DriftFinding{{ReasonCode: string({reason}), SourceKind: segment.SourceKind, Reference: segment.SourceReference}})
\t\t\t}}
'''
    replacements.append((old, new))

for old, new in replacements:
    if old not in text:
        raise SystemExit("expected service validation block not found")
    text = text.replace(old, new, 1)
path.write_text(text)

Path("internal/contextengine/service_operational_test.go").write_text(r'''package contextengine

import (
    "context"
    "errors"
    "testing"
)

type switchableDocumentLoader struct {
    delegate DocumentLoader
    failPath string
    err      error
}

func (l *switchableDocumentLoader) Load(ctx context.Context, path string, maxBytes int64) (LoadedDocument, error) {
    if path == l.failPath && l.err != nil {
        return LoadedDocument{}, l.err
    }
    return l.delegate.Load(ctx, path, maxBytes)
}

func TestServiceValidatePreservesOperationalDocumentError(t *testing.T) {
    fixture := newServiceFixture(t)
    documents := &switchableDocumentLoader{delegate: fixture.docs}
    service, err := NewService(
        ServiceConfig{OrganizationAgentPath: "AGENT.md", MaxTotalBytes: 65536, MaxSegmentBytes: 8192, MaxSegments: 64, MaxSkills: 16, MaxMemorySegments: 32, MaxRAGSegments: 20},
        fixture.registry,
        documents,
        fixture.canonical,
        NoopOwnerConstraintProvider{},
        UnavailableMemoryProvider{},
        emptySkillProvider{},
        UnavailableProjectProvider{},
        UnavailableTaskProvider{},
        UnavailableRAGProvider{},
        NewAssembler(),
        NewRenderer(),
        fixture.store,
        fixedClock{},
    )
    if err != nil {
        t.Fatal(err)
    }
    result, err := service.Build(t.Context(), fixture.request("operational-validation"))
    if err != nil {
        t.Fatal(err)
    }
    sentinel := errors.New("filesystem unavailable")
    documents.failPath = "ingenieria_ia/qa/PERFIL.md"
    documents.err = sentinel
    validation, err := service.Validate(t.Context(), result.Snapshot.ID)
    if !errors.Is(err, sentinel) {
        t.Fatalf("validation=%+v error=%v, want wrapped operational error", validation, err)
    }
}
''')
