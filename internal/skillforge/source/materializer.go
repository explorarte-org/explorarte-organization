package source

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/contextengine/document"
	"github.com/Mireuz13/explorarte-organization/internal/skillregistry"
)

type LocalMaterializer struct {
	skillsRoot string
	reader     PinnedSourceReader
}

func NewLocalMaterializer(skillsRoot string, reader PinnedSourceReader) (*LocalMaterializer, error) {
	if strings.TrimSpace(skillsRoot) == "" {
		return nil, fmt.Errorf("skills root is required")
	}
	if reader == nil {
		return nil, fmt.Errorf("pinned source reader is required")
	}
	abs, err := filepath.Abs(skillsRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve skills root: %w", err)
	}
	return &LocalMaterializer{skillsRoot: abs, reader: reader}, nil
}

func (m *LocalMaterializer) Materialize(ctx context.Context, req MaterializeRequest) (skillregistry.SourceRecord, error) {
	// 1. Validate pinned GitHub origin
	trimmedOrigin := strings.TrimSpace(req.OriginRef)
	if !githubPinnedPattern.MatchString(trimmedOrigin) {
		return skillregistry.SourceRecord{}, fmt.Errorf("%w: origin %q is not pinned as owner/repo@<40-hex-sha>", ErrMutableOriginRejected, req.OriginRef)
	}

	// 2. Validate RelativePath
	cleanPath := filepath.Clean(strings.TrimSpace(req.RelativePath))
	if filepath.IsAbs(cleanPath) || strings.HasPrefix(cleanPath, ".."+string(filepath.Separator)) || cleanPath == "." || cleanPath == ".." {
		return skillregistry.SourceRecord{}, fmt.Errorf("%w: path %q", ErrPathEscapesRoot, req.RelativePath)
	}
	if filepath.Base(cleanPath) != "SKILL.md" {
		return skillregistry.SourceRecord{}, fmt.Errorf("materialized file must be SKILL.md, got %s", filepath.Base(cleanPath))
	}

	// 3. Read exact bytes from pinned git commit via PinnedSourceReader
	artifact, err := m.reader.ReadPinned(ctx, PinnedSourceRef{
		OriginRef: trimmedOrigin,
		Path:      cleanPath,
	})
	if err != nil {
		return skillregistry.SourceRecord{}, fmt.Errorf("read pinned commit source %s:%s: %w", trimmedOrigin, cleanPath, err)
	}

	// 4. Verify Raw SHA against pinned commit bytes
	rawSum := sha256.Sum256(artifact.Bytes)
	rawSHA := hex.EncodeToString(rawSum[:])
	if req.ExpectedRawSHA != "" && rawSHA != req.ExpectedRawSHA {
		return skillregistry.SourceRecord{}, fmt.Errorf("%w: expected raw %s, got %s", ErrDigestMismatch, req.ExpectedRawSHA, rawSHA)
	}

	// 5. Normalize text and verify Normalized SHA
	normBytes, err := document.NormalizeText(artifact.Bytes)
	if err != nil {
		return skillregistry.SourceRecord{}, fmt.Errorf("normalize materialized text: %w", err)
	}
	normSum := sha256.Sum256(normBytes)
	normSHA := hex.EncodeToString(normSum[:])
	if req.ExpectedNormSHA != "" && normSHA != req.ExpectedNormSHA {
		return skillregistry.SourceRecord{}, fmt.Errorf("%w: expected normalized %s, got %s", ErrDigestMismatch, req.ExpectedNormSHA, normSHA)
	}

	// 6. Write to destination under skillsRoot
	destPath := filepath.Join(m.skillsRoot, cleanPath)
	rel, err := filepath.Rel(m.skillsRoot, destPath)
	if err != nil || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return skillregistry.SourceRecord{}, fmt.Errorf("%w: %s", ErrPathEscapesRoot, destPath)
	}

	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return skillregistry.SourceRecord{}, fmt.Errorf("create materialized directory: %w", err)
	}
	if err := os.WriteFile(destPath, artifact.Bytes, 0644); err != nil {
		return skillregistry.SourceRecord{}, fmt.Errorf("write materialized file: %w", err)
	}

	record := skillregistry.SourceRecord{
		Path:             cleanPath,
		SHA256:           rawSHA,
		NormalizedSHA256: normSHA,
		Origin:           skillregistry.OriginGitHub,
		OriginRef:        trimmedOrigin,
		RecordedBy:       req.RecordedBy,
		RecordRef:        req.RecordRef,
	}

	if err := record.Validate(); err != nil {
		return skillregistry.SourceRecord{}, fmt.Errorf("validate constructed source record: %w", err)
	}

	return record, nil
}
