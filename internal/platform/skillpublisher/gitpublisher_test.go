package skillpublisher_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/contextengine/document"
	"github.com/Mireuz13/explorarte-organization/internal/platform/skillpublisher"
	"github.com/Mireuz13/explorarte-organization/internal/skillforge/source"
)

func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	runCmd(t, dir, "git", "init")
	runCmd(t, dir, "git", "config", "user.email", "audit@explorarte.org")
	runCmd(t, dir, "git", "config", "user.name", "Explorarte Audit")
	runCmd(t, dir, "git", "commit", "--allow-empty", "-m", "initial commit")
	runCmd(t, dir, "git", "branch", "-M", "main")
}

func initBareRepo(t *testing.T, dir string) {
	t.Helper()
	runCmd(t, dir, "git", "init", "--bare")
}

func runCmd(t *testing.T, dir string, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("command %s %v in %s failed: %v\nOutput: %s", name, args, dir, err, string(out))
	}
	return strings.TrimSpace(string(out))
}

func TestRemotePublicationAndAttestation(t *testing.T) {
	ctx := context.Background()

	// 1. Setup bare remote repo (simulating explorarte-org/skills on remote host)
	bareRemoteDir := t.TempDir()
	initBareRepo(t, bareRemoteDir)

	// 2. Setup local publisher repo
	publisherDir := t.TempDir()
	initGitRepo(t, publisherDir)
	runCmd(t, publisherDir, "git", "remote", "add", "origin", bareRemoteDir)
	runCmd(t, publisherDir, "git", "push", "-u", "origin", "main")

	publisher, err := skillpublisher.NewGitPublisher(skillpublisher.GitPublisherConfig{
		RepoDir:       publisherDir,
		RemoteName:    "origin",
		Branch:        "main",
		Owner:         "explorarte-org",
		Repo:          "skills",
		RequireRemote: true,
	})
	if err != nil {
		t.Fatalf("create GitPublisher: %v", err)
	}

	content := []byte("# Remote Attested Skill\r\n\r\nProcedure content.\r\n")
	res, err := publisher.Publish(ctx, source.PublishRequest{
		SkillID:              "remote-attested-skill",
		CandidateSourceBytes: content,
		Metadata:             map[string]string{"organization_id": "explorarte"},
	})
	if err != nil {
		t.Fatalf("Publish failed: %v", err)
	}

	parts := strings.Split(res.OriginRef, "@")
	if len(parts) != 2 || len(parts[1]) != 40 {
		t.Fatalf("invalid origin ref: %s", res.OriginRef)
	}
	commitSHA := parts[1]

	// Attestation check on bare remote: verify commit exists on bare remote refs
	remoteHead := runCmd(t, bareRemoteDir, "git", "rev-parse", "main")
	if remoteHead != commitSHA {
		t.Fatalf("bare remote main does not match published commit: remote=%s, published=%s", remoteHead, commitSHA)
	}
}

func TestLocalOnlyCommitRejectedWithoutRemote(t *testing.T) {
	publisherDir := t.TempDir()
	initGitRepo(t, publisherDir)

	// Publisher configured with RequireRemote: true but no valid remote
	_, err := skillpublisher.NewGitPublisher(skillpublisher.GitPublisherConfig{
		RepoDir:       publisherDir,
		RemoteName:    "",
		Branch:        "main",
		Owner:         "explorarte-org",
		Repo:          "skills",
		RequireRemote: true,
	})
	if err == nil || !errors.Is(err, skillpublisher.ErrRemoteRequired) {
		t.Fatalf("expected ErrRemoteRequired for empty remote when RequireRemote=true, got %v", err)
	}
}

func TestPublicationIdempotencyAndCrashRecovery(t *testing.T) {
	ctx := context.Background()

	bareRemoteDir := t.TempDir()
	initBareRepo(t, bareRemoteDir)

	publisherDir := t.TempDir()
	initGitRepo(t, publisherDir)
	runCmd(t, publisherDir, "git", "remote", "add", "origin", bareRemoteDir)
	runCmd(t, publisherDir, "git", "push", "-u", "origin", "main")

	publisher, err := skillpublisher.NewGitPublisher(skillpublisher.GitPublisherConfig{
		RepoDir:       publisherDir,
		RemoteName:    "origin",
		Branch:        "main",
		Owner:         "explorarte-org",
		Repo:          "skills",
		RequireRemote: true,
	})
	if err != nil {
		t.Fatalf("create GitPublisher: %v", err)
	}

	content := []byte("# Idempotent Skill\n\nContent.\n")
	req := source.PublishRequest{
		SkillID:              "idempotent-skill",
		CandidateSourceBytes: content,
		Metadata:             map[string]string{"organization_id": "explorarte"},
	}

	// 1. Initial publication
	res1, err := publisher.Publish(ctx, req)
	if err != nil {
		t.Fatalf("initial publish failed: %v", err)
	}

	commitCountBefore := runCmd(t, publisherDir, "git", "rev-list", "--count", "HEAD")

	// 2. Simulate crash and retry: create brand new publisher instance with same request
	publisher2, err := skillpublisher.NewGitPublisher(skillpublisher.GitPublisherConfig{
		RepoDir:       publisherDir,
		RemoteName:    "origin",
		Branch:        "main",
		Owner:         "explorarte-org",
		Repo:          "skills",
		RequireRemote: true,
	})
	if err != nil {
		t.Fatalf("recreate publisher: %v", err)
	}

	res2, err := publisher2.Publish(ctx, req)
	if err != nil {
		t.Fatalf("retry publish failed: %v", err)
	}

	if res1.OriginRef != res2.OriginRef {
		t.Fatalf("expected identical OriginRef on retry, got %s vs %s", res1.OriginRef, res2.OriginRef)
	}

	commitCountAfter := runCmd(t, publisherDir, "git", "rev-list", "--count", "HEAD")
	if commitCountBefore != commitCountAfter {
		t.Fatalf("duplicate commit created on retry! count before=%s, after=%s", commitCountBefore, commitCountAfter)
	}
}

func TestGitPinnedSourceReaderAndIsolation(t *testing.T) {
	ctx := context.Background()

	bareRemoteDir := t.TempDir()
	initBareRepo(t, bareRemoteDir)

	publisherDir := t.TempDir()
	initGitRepo(t, publisherDir)
	runCmd(t, publisherDir, "git", "remote", "add", "origin", bareRemoteDir)
	runCmd(t, publisherDir, "git", "push", "-u", "origin", "main")

	publisher, _ := skillpublisher.NewGitPublisher(skillpublisher.GitPublisherConfig{
		RepoDir:       publisherDir,
		RemoteName:    "origin",
		Branch:        "main",
		Owner:         "explorarte-org",
		Repo:          "skills",
		RequireRemote: true,
	})

	contentV1 := []byte("# Version 1\n\nOriginal immutable content.\n")
	pubRes, err := publisher.Publish(ctx, source.PublishRequest{
		SkillID:              "pinned-test-skill",
		CandidateSourceBytes: contentV1,
		Metadata:             map[string]string{"organization_id": "explorarte"},
	})
	if err != nil {
		t.Fatalf("publish v1 failed: %v", err)
	}

	// 3. Setup runtime mirror repo (cloned from bare remote, host-owned runtime cache)
	mirrorDir := t.TempDir()
	runCmd(t, mirrorDir, "git", "clone", bareRemoteDir, ".")

	reader, err := skillpublisher.NewGitPinnedSourceReader(mirrorDir)
	if err != nil {
		t.Fatalf("create GitPinnedSourceReader: %v", err)
	}

	// Read exact pinned source
	artifact, err := reader.ReadPinned(ctx, source.PinnedSourceRef{
		OriginRef: pubRes.OriginRef,
		Path:      pubRes.Path,
	})
	if err != nil {
		t.Fatalf("ReadPinned failed: %v", err)
	}
	if string(artifact.Bytes) != string(contentV1) {
		t.Fatalf("read pinned bytes mismatch: got %q", string(artifact.Bytes))
	}

	// 4. Test WORKING_TREE_DRIFT_ISOLATED:
	// Intentionally corrupt working tree file in mirror
	mirrorFilePath := filepath.Join(mirrorDir, filepath.FromSlash(pubRes.Path))
	_ = os.MkdirAll(filepath.Dir(mirrorFilePath), 0755)
	if err := os.WriteFile(mirrorFilePath, []byte("CORRUPTED WORKING TREE CONTENT"), 0644); err != nil {
		t.Fatalf("corrupt working tree file: %v", err)
	}

	// Reader MUST still return exact immutable bytes from commit object
	artifactAfterDrift, err := reader.ReadPinned(ctx, source.PinnedSourceRef{
		OriginRef: pubRes.OriginRef,
		Path:      pubRes.Path,
	})
	if err != nil {
		t.Fatalf("ReadPinned after drift failed: %v", err)
	}
	if string(artifactAfterDrift.Bytes) != string(contentV1) {
		t.Fatalf("WORKING_TREE_DRIFT_ISOLATED failed: read drift bytes instead of commit blob: %q", string(artifactAfterDrift.Bytes))
	}

	// 5. Test BRANCH_MOVEMENT_REPRODUCIBLE:
	// Move main forward with a new commit in publisher and push to remote
	contentV2 := []byte("# Version 2\n\nNew version content.\n")
	_, err = publisher.Publish(ctx, source.PublishRequest{
		SkillID:              "pinned-test-skill-v2",
		CandidateSourceBytes: contentV2,
		Metadata:             map[string]string{"organization_id": "explorarte"},
	})
	if err != nil {
		t.Fatalf("publish v2 failed: %v", err)
	}

	// Fetch new commits into mirror
	runCmd(t, mirrorDir, "git", "fetch", "origin")

	// Historical pinned ref for V1 MUST still be reproducible
	artifactV1AfterBranchMove, err := reader.ReadPinned(ctx, source.PinnedSourceRef{
		OriginRef: pubRes.OriginRef,
		Path:      pubRes.Path,
	})
	if err != nil {
		t.Fatalf("ReadPinned v1 after branch move failed: %v", err)
	}
	if string(artifactV1AfterBranchMove.Bytes) != string(contentV1) {
		t.Fatalf("BRANCH_MOVEMENT_REPRODUCIBLE failed: expected v1 bytes, got %q", string(artifactV1AfterBranchMove.Bytes))
	}

	// 6. Test missing commit / missing path
	_, err = reader.ReadPinned(ctx, source.PinnedSourceRef{
		OriginRef: "explorarte-org/skills@0000000000000000000000000000000000000000",
		Path:      pubRes.Path,
	})
	if err == nil || !errors.Is(err, source.ErrPinnedCommitNotFound) {
		t.Fatalf("expected ErrPinnedCommitNotFound for missing commit, got %v", err)
	}

	_, err = reader.ReadPinned(ctx, source.PinnedSourceRef{
		OriginRef: pubRes.OriginRef,
		Path:      "skills/non-existent/SKILL.md",
	})
	if err == nil || !errors.Is(err, source.ErrPinnedPathNotFound) {
		t.Fatalf("expected ErrPinnedPathNotFound for missing path, got %v", err)
	}
}

func TestLocalMaterializerWithPinnedReaderContract(t *testing.T) {
	ctx := context.Background()

	bareRemoteDir := t.TempDir()
	initBareRepo(t, bareRemoteDir)

	publisherDir := t.TempDir()
	initGitRepo(t, publisherDir)
	runCmd(t, publisherDir, "git", "remote", "add", "origin", bareRemoteDir)
	runCmd(t, publisherDir, "git", "push", "-u", "origin", "main")

	publisher, _ := skillpublisher.NewGitPublisher(skillpublisher.GitPublisherConfig{
		RepoDir:       publisherDir,
		RemoteName:    "origin",
		Branch:        "main",
		Owner:         "explorarte-org",
		Repo:          "skills",
		RequireRemote: true,
	})

	rawContent := []byte("# Pinned Contract Skill\r\n\r\nProcedure.\r\n")
	pubRes, err := publisher.Publish(ctx, source.PublishRequest{
		SkillID:              "contract-skill",
		CandidateSourceBytes: rawContent,
		Metadata:             map[string]string{"organization_id": "explorarte"},
	})
	if err != nil {
		t.Fatalf("publish failed: %v", err)
	}

	reader, err := skillpublisher.NewGitPinnedSourceReader(publisherDir)
	if err != nil {
		t.Fatalf("create reader: %v", err)
	}

	skillsRoot := t.TempDir()
	materializer, err := source.NewLocalMaterializer(skillsRoot, reader)
	if err != nil {
		t.Fatalf("create materializer: %v", err)
	}

	rawSum := sha256.Sum256(rawContent)
	rawHex := hex.EncodeToString(rawSum[:])
	normText, _ := document.NormalizeText(rawContent)
	normSum := sha256.Sum256(normText)
	normHex := hex.EncodeToString(normSum[:])

	// 1. Valid exact SHA:path + matching digests -> PASS
	matRec, err := materializer.Materialize(ctx, source.MaterializeRequest{
		OriginRef:       pubRes.OriginRef,
		RelativePath:    pubRes.Path,
		ExpectedRawSHA:  rawHex,
		ExpectedNormSHA: normHex,
		RecordedBy:      "empresa/human",
		RecordRef:       "mat-1",
	})
	if err != nil {
		t.Fatalf("materialize valid failed: %v", err)
	}
	if matRec.SHA256 != rawHex || matRec.NormalizedSHA256 != normHex {
		t.Fatalf("unexpected matRec digests: %+v", matRec)
	}

	// 2. Commit absent -> DENY
	_, err = materializer.Materialize(ctx, source.MaterializeRequest{
		OriginRef:       "explorarte-org/skills@bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		RelativePath:    pubRes.Path,
		ExpectedRawSHA:  rawHex,
		ExpectedNormSHA: normHex,
		RecordedBy:      "empresa/human",
		RecordRef:       "mat-2",
	})
	if err == nil {
		t.Fatal("expected error for absent commit, got nil")
	}

	// 3. Path absent in commit -> DENY
	_, err = materializer.Materialize(ctx, source.MaterializeRequest{
		OriginRef:       pubRes.OriginRef,
		RelativePath:    "skills/absent/SKILL.md",
		ExpectedRawSHA:  rawHex,
		ExpectedNormSHA: normHex,
		RecordedBy:      "empresa/human",
		RecordRef:       "mat-3",
	})
	if err == nil {
		t.Fatal("expected error for absent path, got nil")
	}

	// 4. Raw hash differs -> DENY
	_, err = materializer.Materialize(ctx, source.MaterializeRequest{
		OriginRef:       pubRes.OriginRef,
		RelativePath:    pubRes.Path,
		ExpectedRawSHA:  "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
		ExpectedNormSHA: normHex,
		RecordedBy:      "empresa/human",
		RecordRef:       "mat-4",
	})
	if err == nil || !errors.Is(err, source.ErrDigestMismatch) {
		t.Fatalf("expected ErrDigestMismatch for wrong raw hash, got %v", err)
	}

	// 5. Normalized hash differs -> DENY
	_, err = materializer.Materialize(ctx, source.MaterializeRequest{
		OriginRef:       pubRes.OriginRef,
		RelativePath:    pubRes.Path,
		ExpectedRawSHA:  rawHex,
		ExpectedNormSHA: "0000000000000000000000000000000000000000000000000000000000000000",
		RecordedBy:      "empresa/human",
		RecordRef:       "mat-5",
	})
	if err == nil || !errors.Is(err, source.ErrDigestMismatch) {
		t.Fatalf("expected ErrDigestMismatch for wrong normalized hash, got %v", err)
	}
}
