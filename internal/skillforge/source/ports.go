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

type MaterializeRequest struct {
	OriginRef       string // owner/repo@40-hex-sha
	RelativePath    string // path within SKILLS_ROOT ending in SKILL.md
	ExpectedRawSHA  string
	ExpectedNormSHA string
	SourceBytes     []byte // provided by host fetcher or local repo
	RecordedBy      string
	RecordRef       string
}

type Materializer interface {
	Materialize(ctx context.Context, req MaterializeRequest) (skillregistry.SourceRecord, error)
}
