package skillpublisher

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/skillforge/source"
)

func TestLocalGitPublisherInDisposableRepo(t *testing.T) {
	ctx := context.Background()
	tmpDir, err := os.MkdirTemp("", "git-publisher-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Init disposable git repo
	cmd := exec.CommandContext(ctx, "git", "init")
	cmd.Dir = tmpDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init failed: %v, output: %s", err, string(out))
	}
	_ = exec.CommandContext(ctx, "git", "-C", tmpDir, "config", "user.email", "test@explorarte.org").Run()
	_ = exec.CommandContext(ctx, "git", "-C", tmpDir, "config", "user.name", "Test Runner").Run()

	publisher, err := NewLocalGitPublisher(tmpDir, "explorarte-org", "skills")
	if err != nil {
		t.Fatalf("NewLocalGitPublisher error: %v", err)
	}

	content := []byte("# Procedure\nDo something useful.\n")
	published, err := publisher.Publish(ctx, source.PublishRequest{
		SkillID:              "skill-test",
		CandidateSourceBytes: content,
	})
	if err != nil {
		t.Fatalf("Publish failed: %v", err)
	}

	if published.OriginRef == "" || published.RawSHA256 == "" || published.NormalizedSHA256 == "" {
		t.Fatalf("Incomplete published source: %+v", published)
	}

	expectedPath := filepath.Join("skills", "skill-test", "SKILL.md")
	if published.Path != expectedPath {
		t.Fatalf("Expected path %s, got %s", expectedPath, published.Path)
	}

	// Verify file was written to the repo
	fullPath := filepath.Join(tmpDir, expectedPath)
	data, err := os.ReadFile(fullPath)
	if err != nil {
		t.Fatalf("Failed to read published file: %v", err)
	}
	if string(data) != string(content) {
		t.Fatalf("Content mismatch: expected %q, got %q", string(content), string(data))
	}
}
