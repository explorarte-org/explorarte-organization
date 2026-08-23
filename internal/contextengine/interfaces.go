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
	ValidateVersion(context.Context, string, SourceRecord) error
}

type MemoryProvider interface {
	ListApproved(context.Context, BuildRequest) ([]SourceRecord, error)
	ValidateVersion(context.Context, string, SourceRecord) error
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
	ValidateVersion(context.Context, string, SourceRecord) error
}

type TaskContextProvider interface {
	GetTaskContext(context.Context, BuildRequest) (*SourceRecord, error)
	ValidateVersion(context.Context, string, SourceRecord) error
}

type RAGEvidenceProvider interface {
	ListApprovedEvidence(context.Context, BuildRequest) ([]SourceRecord, error)
	ValidateVersion(context.Context, string, SourceRecord) error
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
	// BindSelectorFacts durably fills in whichever of
	// TaskClass/ExecutionPurpose/ActorUnitID this snapshot does not yet
	// have (NULL/empty), from the resumed caller's proposed values --
	// and NEVER overwrites a field that already has one (implementations
	// must make this atomic and race-free, e.g. via SQL COALESCE against
	// the row's current value at UPDATE time, not a separate read-then-
	// write). This is what turns "a pre-M1.3 snapshot with no selector
	// identity accepts any resumed proposal" into a ONE-TIME binding: the
	// first legitimate resume durably records what the snapshot's
	// selector identity actually is, so a LATER, DIFFERENT resumed
	// proposal under the same idempotency key is compared against a
	// now-concrete value instead of remaining permanently unbound.
	// Passing an empty string for any field is a no-op for that field.
	BindSelectorFacts(ctx context.Context, snapshotID int64, taskClass, executionPurpose, actorUnitID string) (Snapshot, error)
}

type ValidationEventRecorder interface {
	RecordValidationFailure(context.Context, Snapshot, SnapshotValidation, time.Time) error
}

type ForbiddenSourceEventRecorder interface {
	RecordForbiddenSourceRejection(context.Context, BuildRequest, ReasonCode, time.Time) error
}

type Service interface {
	Build(context.Context, BuildRequest) (BuildResult, error)
	Get(context.Context, int64, bool) (Snapshot, error)
	List(context.Context, ListFilter) ([]Snapshot, error)
	Render(context.Context, int64) ([]byte, error)
	Validate(context.Context, int64) (SnapshotValidation, error)
	Invalidate(context.Context, InvalidateCommand) (Snapshot, error)
}

// RepositoryEvidenceProvider supplies bounded excerpts of the repository an
// execution is allowed to observe, at one exact commit.
//
// It is asked only when a BuildRequest carries a pinned commit, and it is
// given that commit rather than choosing one: a provider free to decide which
// version to read could hand a design a repository nobody decided to reason
// about.
type RepositoryEvidenceProvider interface {
	ListRepositoryEvidence(ctx context.Context, request BuildRequest) ([]SourceRecord, error)
	ValidateVersion(ctx context.Context, actorRoleID string, source SourceRecord) error
}
