package source

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/contextengine/document"
)

func TestMutableOriginRejected(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	mat, err := NewLocalMaterializer(tmpDir)
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
			SourceBytes:  []byte("# Skill"),
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
	mat, _ := NewLocalMaterializer(tmpDir)
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
			SourceBytes:  []byte("# Skill"),
		})
		if err == nil {
			t.Fatalf("expected error for path %q, got nil", path)
		}
	}
}

func TestMaterializationAndDigestVerification(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	mat, _ := NewLocalMaterializer(tmpDir)
	validOrigin := "explorarte-org/skills@aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	rawContent := []byte("# Pinned Skill\r\n\r\nProcedure.\r\n")
	rawSum := sha256.Sum256(rawContent)
	rawHex := hex.EncodeToString(rawSum[:])

	normText, _ := document.NormalizeText(rawContent)
	normSum := sha256.Sum256(normText)
	normHex := hex.EncodeToString(normSum[:])

	// 1. Successful materialization
	rec, err := mat.Materialize(ctx, MaterializeRequest{
		OriginRef:       validOrigin,
		RelativePath:    "skills/pinned-skill/SKILL.md",
		ExpectedRawSHA:  rawHex,
		ExpectedNormSHA: normHex,
		SourceBytes:     rawContent,
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
		RelativePath:    "skills/pinned-skill/SKILL.md",
		ExpectedRawSHA:  "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
		ExpectedNormSHA: normHex,
		SourceBytes:     rawContent,
		RecordedBy:      "empresa/human",
		RecordRef:       "mat-rec-2",
	})
	if err == nil || !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("expected ErrDigestMismatch, got %v", err)
	}
}

func TestLocalGitPublisherInDisposableRepo(t *testing.T) {
	ctx := context.Background()
	repoDir := t.TempDir()

	// Initialize disposable git repo
	initCmd := exec.CommandContext(ctx, "git", "init")
	initCmd.Dir = repoDir
	if out, err := initCmd.CombinedOutput(); err != nil {
		t.Fatalf("git init failed: %v, output: %s", err, string(out))
	}
	configCmd := exec.CommandContext(ctx, "git", "config", "user.email", "test@explorarte.org")
	configCmd.Dir = repoDir
	_ = configCmd.Run()
	configCmd = exec.CommandContext(ctx, "git", "config", "user.name", "Test Runner")
	configCmd.Dir = repoDir
	_ = configCmd.Run()

	pub, err := NewLocalGitPublisher(repoDir, "explorarte-org", "skills")
	if err != nil {
		t.Fatalf("NewLocalGitPublisher: %v", err)
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
	if res.Path != "skills/disposable-skill/SKILL.md" {
		t.Fatalf("unexpected path: %q", res.Path)
	}
}
