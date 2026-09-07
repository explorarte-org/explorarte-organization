package source

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/contextengine/document"
)

type fakePinnedSourceReader struct {
	files map[string][]byte // key: originRef + ":" + path
}

func newFakePinnedSourceReader() *fakePinnedSourceReader {
	return &fakePinnedSourceReader{files: make(map[string][]byte)}
}

func (f *fakePinnedSourceReader) addFile(originRef, path string, content []byte) {
	f.files[originRef+":"+filepath.Clean(path)] = content
}

func (f *fakePinnedSourceReader) ReadPinned(_ context.Context, ref PinnedSourceRef) (PinnedSourceArtifact, error) {
	parts := strings.Split(ref.OriginRef, "@")
	if len(parts) != 2 || len(parts[1]) != 40 {
		return PinnedSourceArtifact{}, fmt.Errorf("invalid origin ref: %s", ref.OriginRef)
	}
	clean := filepath.Clean(ref.Path)
	content, found := f.files[ref.OriginRef+":"+clean]
	if !found {
		return PinnedSourceArtifact{}, fmt.Errorf("%w: %s:%s", ErrPinnedPathNotFound, ref.OriginRef, clean)
	}
	return PinnedSourceArtifact{
		CommitSHA: parts[1],
		Path:      clean,
		Bytes:     content,
	}, nil
}

func TestMutableOriginRejected(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	reader := newFakePinnedSourceReader()
	mat, err := NewLocalMaterializer(tmpDir, reader)
	if err != nil {
		t.Fatal(err)
	}

	invalidOrigins := []string{
		"explorarte-org/skills@main",
		"explorarte-org/skills@master",
		"explorarte-org/skills@latest",
		"explorarte-org/skills@v1.0.0",
		"explorarte-org/skills",
		"https://github.com/explorarte-org/skills.git",
		"explorarte-org/skills@12345", // too short, not 40 chars
	}

	for _, origin := range invalidOrigins {
		_, err := mat.Materialize(ctx, MaterializeRequest{
			OriginRef:    origin,
			RelativePath: "skills/test/SKILL.md",
		})
		if err == nil {
			t.Fatalf("expected error for mutable origin %q, got nil", origin)
		}
		if !errors.Is(err, ErrMutableOriginRejected) {
			t.Fatalf("expected ErrMutableOriginRejected for %q, got %v", origin, err)
		}
	}
}

func TestPathSecurityEnforcement(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	reader := newFakePinnedSourceReader()
	mat, _ := NewLocalMaterializer(tmpDir, reader)
	validOrigin := "explorarte-org/skills@aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	badPaths := []string{
		"/absolute/path/SKILL.md",
		"../escape/SKILL.md",
		"skills/../../escape/SKILL.md",
		"skills/test/other.txt", // not SKILL.md
	}

	for _, path := range badPaths {
		_, err := mat.Materialize(ctx, MaterializeRequest{
			OriginRef:    validOrigin,
			RelativePath: path,
		})
		if err == nil {
			t.Fatalf("expected error for path %q, got nil", path)
		}
	}
}

func TestMaterializationAndDigestVerification(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	reader := newFakePinnedSourceReader()
	mat, _ := NewLocalMaterializer(tmpDir, reader)
	validOrigin := "explorarte-org/skills@aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	skillPath := "skills/pinned-skill/SKILL.md"

	rawContent := []byte("# Pinned Skill\r\n\r\nProcedure.\r\n")
	rawSum := sha256.Sum256(rawContent)
	rawHex := hex.EncodeToString(rawSum[:])

	normText, _ := document.NormalizeText(rawContent)
	normSum := sha256.Sum256(normText)
	normHex := hex.EncodeToString(normSum[:])

	// Register file in pinned reader
	reader.addFile(validOrigin, skillPath, rawContent)

	// 1. Successful materialization
	rec, err := mat.Materialize(ctx, MaterializeRequest{
		OriginRef:       validOrigin,
		RelativePath:    skillPath,
		ExpectedRawSHA:  rawHex,
		ExpectedNormSHA: normHex,
		RecordedBy:      "empresa/human",
		RecordRef:       "mat-rec-1",
	})
	if err != nil {
		t.Fatalf("materialize failed: %v", err)
	}

	if rec.SHA256 != rawHex || rec.NormalizedSHA256 != normHex {
		t.Fatalf("unexpected record hashes: %+v", rec)
	}

	// Verify file was written to disk
	diskBytes, err := os.ReadFile(filepath.Join(tmpDir, "skills", "pinned-skill", "SKILL.md"))
	if err != nil {
		t.Fatalf("read materialized file: %v", err)
	}
	if string(diskBytes) != string(rawContent) {
		t.Fatal("materialized file content mismatch")
	}

	// 2. Digest mismatch rejects
	_, err = mat.Materialize(ctx, MaterializeRequest{
		OriginRef:       validOrigin,
		RelativePath:    skillPath,
		ExpectedRawSHA:  "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
		ExpectedNormSHA: normHex,
		RecordedBy:      "empresa/human",
		RecordRef:       "mat-rec-2",
	})
	if err == nil || !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("expected ErrDigestMismatch, got %v", err)
	}
}

type fakeInMemoryPublisher struct {
	Owner string
	Repo  string
}

func (f *fakeInMemoryPublisher) Publish(_ context.Context, req PublishRequest) (PublishedSource, error) {
	if strings.TrimSpace(req.SkillID) == "" {
		return PublishedSource{}, errors.New("skill id is required")
	}
	if len(req.CandidateSourceBytes) == 0 {
		return PublishedSource{}, errors.New("candidate bytes cannot be empty")
	}

	rawSum := sha256.Sum256(req.CandidateSourceBytes)
	rawSHA := hex.EncodeToString(rawSum[:])

	normBytes, err := document.NormalizeText(req.CandidateSourceBytes)
	if err != nil {
		return PublishedSource{}, fmt.Errorf("normalize candidate text: %w", err)
	}
	normSum := sha256.Sum256(normBytes)
	normSHA := hex.EncodeToString(normSum[:])

	fakeSHA := "abcdef0123456789abcdef0123456789abcdef01"
	originRef := fmt.Sprintf("%s/%s@%s", f.Owner, f.Repo, fakeSHA)

	return PublishedSource{
		OriginRef:        originRef,
		Path:             filepath.Join("skills", req.SkillID, "SKILL.md"),
		RawSHA256:        rawSHA,
		NormalizedSHA256: normSHA,
		PublicationRef:   fmt.Sprintf("fake-git:%s:%s", req.SkillID, fakeSHA),
	}, nil
}

func TestFakeSourcePublisherWithoutGit(t *testing.T) {
	ctx := context.Background()
	pub := &fakeInMemoryPublisher{
		Owner: "explorarte-org",
		Repo:  "skills",
	}

	content := []byte("# Disposable Skill\n\nContent.\n")
	res, err := pub.Publish(ctx, PublishRequest{
		SkillID:              "disposable-skill",
		CandidateSourceBytes: content,
	})
	if err != nil {
		t.Fatalf("Publish failed: %v", err)
	}

	if !githubPinnedPattern.MatchString(res.OriginRef) {
		t.Fatalf("published origin ref is not pinned: %q", res.OriginRef)
	}
	expectedPath := filepath.Join("skills", "disposable-skill", "SKILL.md")
	if res.Path != expectedPath {
		t.Fatalf("unexpected path: expected %s, got %q", expectedPath, res.Path)
	}
}
