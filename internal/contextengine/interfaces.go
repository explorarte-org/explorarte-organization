package contextengine

import (
	"context"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
)

type Clock interface{ Now() time.Time }

type RegistryReader interface {
	GetOrganization(context.Context, string) (registry.Organization, error)
	GetUnit(context.Context, string, string) (registry.Unit, error)
	GetRole(context.Context, string, string) (registry.Role, error)
	GetCurrentRevision(context.Context, string) (*registry.Revision, error)
}

type LoadedDocument struct {
	Path        string
	Version     string
	Content     []byte
	Normalized  []byte
	Hash        string
	Frontmatter map[string]any
	Body        []byte
	Warnings    []string
}

type DocumentLoader interface {
	Load(context.Context, string, int64) (LoadedDocument, error)
}

type CanonicalSource struct {
	LogicalName      string
	Version          string
	Tier             AuthorityTier
	InstructionClass InstructionClass
	TrustClass       TrustClass
	DataClass        DataClass
	MayGrant         bool
	Content          []byte
	ContentHash      string
	SemanticHash     string
}

type CanonicalBundle struct {
	PrecedenceHash string
	BundleHash     string
	Sources        []CanonicalSource
}

type CanonicalPolicyProvider interface {
	Load(context.Context) (CanonicalBundle, error)
	Validate(context.Context, string, string) error
}

type OwnerConstraintProvider interface {
	ListApplicable(context.Context, BuildRequest) ([]SourceRecord, error)
	ValidateVersion(context.Context, SourceRecord) error
}

type MemoryProvider interface {
	ListApproved(context.Context, BuildRequest) ([]SourceRecord, error)
	ValidateVersion(context.Context, SourceRecord) error
}

type SkillRecord struct {
	ID               string
	RoleID           string
	Department       string
	MemoryDomain     string
	Lifecycle        SkillLifecycle
	Assigned         bool
	Path             string
	Version          string
	SourceHash       string
	Relevance        int
	ProviderPriority int
}

type SkillProvider interface {
	ListActiveForRole(context.Context, string, string) ([]SkillRecord, error)
	GetActiveForRole(context.Context, string, string, string) (SkillRecord, error)
	ValidateVersion(context.Context, SkillRecord) error
}

type ProjectContextProvider interface {
	GetProjectContext(context.Context, BuildRequest) (*SourceRecord, error)
	ValidateVersion(context.Context, SourceRecord) error
}

type TaskContextProvider interface {
	GetTaskContext(context.Context, BuildRequest) (*SourceRecord, error)
	ValidateVersion(context.Context, SourceRecord) error
}

type RAGEvidenceProvider interface {
	ListApprovedEvidence(context.Context, BuildRequest) ([]SourceRecord, error)
	ValidateVersion(context.Context, SourceRecord) error
}

type AssemblyInput struct {
	Sources           []SourceRecord
	MaxTotalBytes     int
	MaxSegmentBytes   int
	MaxSegments       int
	MaxSkills         int
	MaxMemorySegments int
	MaxRAGSegments    int
}

type Assembler interface {
	Assemble(context.Context, AssemblyInput) (Assembly, error)
}

type Renderer interface {
	Render(context.Context, Snapshot) ([]byte, error)
}

type CreateSnapshotCommand struct {
	Snapshot   Snapshot
	Now        time.Time
	ReasonCode ReasonCode
}

type SnapshotStore interface {
	AllocateID(context.Context) (int64, error)
	Create(context.Context, CreateSnapshotCommand) (BuildResult, error)
	Get(context.Context, int64, bool) (Snapshot, error)
	GetByIdempotency(context.Context, string, string, bool) (Snapshot, error)
	List(context.Context, ListFilter) ([]Snapshot, error)
	Invalidate(context.Context, InvalidateCommand, time.Time) (Snapshot, bool, error)
}

type ValidationEventRecorder interface {
	RecordValidationFailure(context.Context, Snapshot, SnapshotValidation, time.Time) error
}

type Service interface {
	Build(context.Context, BuildRequest) (BuildResult, error)
	Get(context.Context, int64, bool) (Snapshot, error)
	List(context.Context, ListFilter) ([]Snapshot, error)
	Render(context.Context, int64) ([]byte, error)
	Validate(context.Context, int64) (SnapshotValidation, error)
	Invalidate(context.Context, InvalidateCommand) (Snapshot, error)
}
