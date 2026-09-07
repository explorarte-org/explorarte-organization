package source

import (
	"context"
	"errors"
	"regexp"

	"github.com/Mireuz13/explorarte-organization/internal/skillregistry"
)

var (
	ErrMutableOriginRejected = errors.New("mutable origin rejected: github origin must be pinned to exact 40-character commit sha")
	ErrPathEscapesRoot       = errors.New("materialized path escapes root")
	ErrDigestMismatch        = errors.New("source materialization digest mismatch")
	ErrInvalidOrigin         = errors.New("invalid origin specification")
	ErrPinnedCommitNotFound  = errors.New("pinned commit not found in repository")
	ErrPinnedPathNotFound    = errors.New("path not found in pinned commit")
	ErrInvalidRuntimeRoot    = errors.New("runtime root cannot contain git authority or overlap with source repo root")

	githubPinnedPattern = regexp.MustCompile(`^[a-zA-Z0-9_.-]+/[a-zA-Z0-9_.-]+@[0-9a-f]{40}$`)
)

type PublishRequest struct {
	SkillID               string
	CandidateSourceBytes  []byte
	ExpectedContentDigest string
	Metadata              map[string]string
	IdempotencyKey        string
}

type PublishedSource struct {
	OriginRef        string // owner/repo@40-char-commit-sha
	Path             string // e.g. "skills/<skill-id>/SKILL.md"
	RawSHA256        string
	NormalizedSHA256 string
	PublicationRef   string
}

// SourcePublisher is a narrow port. Skill Forge does NOT hold git credentials or authority.
// The publisher boundary is host-owned and completely decoupled from internal/staging/gitexec.
type SourcePublisher interface {
	Publish(ctx context.Context, request PublishRequest) (PublishedSource, error)
}

// PinnedSourceRef specifies an immutable source target within a pinned commit.
type PinnedSourceRef struct {
	OriginRef string // owner/repo@40hex
	Path      string // relative path ending in SKILL.md
}

// PinnedSourceArtifact represents the immutable bytes retrieved directly from a pinned commit.
type PinnedSourceArtifact struct {
	CommitSHA string
	Path      string
	Bytes     []byte
}

// PinnedSourceReader is a host-owned read boundary that extracts bytes directly
// from an exact pinned commit object without consulting mutable working trees or live network.
type PinnedSourceReader interface {
	ReadPinned(ctx context.Context, ref PinnedSourceRef) (PinnedSourceArtifact, error)
}

// RepoRootHolder is an optional interface implemented by repository-backed source readers
// to allow the materializer to enforce isolation between source repo root and runtime root.
type RepoRootHolder interface {
	SourceRepoRoot() string
}

type MaterializeRequest struct {
	OriginRef       string // owner/repo@40-hex-sha
	RelativePath    string // path within SKILLS_ROOT ending in SKILL.md
	ExpectedRawSHA  string
	ExpectedNormSHA string
	RecordedBy      string
	RecordRef       string
}

type Materializer interface {
	Materialize(ctx context.Context, req MaterializeRequest) (skillregistry.SourceRecord, error)
}
