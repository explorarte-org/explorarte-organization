package skillpublisher_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Mireuz13/explorarte-organization/internal/contextengine/document"
	"github.com/Mireuz13/explorarte-organization/internal/platform/skillpublisher"
	"github.com/Mireuz13/explorarte-organization/internal/skillforge/source"
)

func runCmd(t *testing.T, dir string, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cmd %s %v in %s failed: %v\nOutput:\n%s", name, args, dir, err, string(out))
	}
	return string(out)
}

func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	runCmd(t, dir, "git", "init", "-b", "main")
	runCmd(t, dir, "git", "config", "user.name", "Skill Publisher Test")
	runCmd(t, dir, "git", "config", "user.email", "publisher@explorarte.test")
	readme := filepath.Join(dir, "README.md")
	if err := os.WriteFile(readme, []byte("# Skills Repository\n"), 0644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	runCmd(t, dir, "git", "add", "README.md")
	runCmd(t, dir, "git", "commit", "-m", "initial commit")
}

func initBareRepo(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	runCmd(t, dir, "git", "init", "--bare", "-b", "main")
}

func setupBareRemote(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "explorarte-org", "skills.git")
	initBareRepo(t, dir)
	return dir
}

func TestRemotePublicationAndAttestation(t *testing.T) {
	ctx := context.Background()

	bareRemoteDir := setupBareRemote(t)

	publisherDir := t.TempDir()
	initGitRepo(t, publisherDir)
	runCmd(t, publisherDir, "git", "remote", "add", "origin", bareRemoteDir)
	runCmd(t, publisherDir, "git", "push", "-u", "origin", "main")

	publisher, err := skillpublisher.NewGitPublisher(skillpublisher.GitPublisherConfig{
		RepoDir:           publisherDir,
		RemoteName:        "origin",
		ExpectedRemoteURL: bareRemoteDir,
		Branch:            "main",
		Owner:             "explorarte-org",
		Repo:              "skills",
		RequireRemote:     true,
	})
	if err != nil {
		t.Fatalf("NewGitPublisher failed: %v", err)
	}

	content := []byte("# Remote Attested Skill\n\nAttested publication.\n")
	res, err := publisher.Publish(ctx, source.PublishRequest{
		SkillID:              "remote-test-skill",
		CandidateSourceBytes: content,
		Metadata:             map[string]string{"organization_id": "explorarte"},
	})
	if err != nil {
		t.Fatalf("Publish failed: %v", err)
	}

	if !strings.HasPrefix(res.OriginRef, "explorarte-org/skills@") {
		t.Fatalf("unexpected origin ref: %s", res.OriginRef)
	}

	parts := strings.Split(res.OriginRef, "@")
	commitSHA := parts[1]

	lsOut := runCmd(t, publisherDir, "git", "ls-remote", "origin", res.PublicationRef)
	if !strings.Contains(lsOut, commitSHA) {
		t.Fatalf("remote tag %s does not point to commit %s in remote repo: %s", res.PublicationRef, commitSHA, lsOut)
	}
}

func TestLocalOnlyCommitRejectedWithoutRemote(t *testing.T) {
	ctx := context.Background()
	publisherDir := t.TempDir()
	initGitRepo(t, publisherDir)

	_, err := skillpublisher.NewGitPublisher(skillpublisher.GitPublisherConfig{
		RepoDir:       publisherDir,
		RemoteName:    "",
		Branch:        "main",
		Owner:         "explorarte-org",
		Repo:          "skills",
		RequireRemote: true,
	})
	if err == nil {
		t.Fatal("expected NewGitPublisher to fail when RequireRemote is true and RemoteName is empty")
	}
	if !errors.Is(err, skillpublisher.ErrRemoteRequired) {
		t.Fatalf("expected ErrRemoteRequired, got %v", err)
	}

	publisherNoRemote, _ := skillpublisher.NewGitPublisher(skillpublisher.GitPublisherConfig{
		RepoDir:       publisherDir,
		RemoteName:    "",
		Branch:        "main",
		Owner:         "explorarte-org",
		Repo:          "skills",
		RequireRemote: false,
	})
	content := []byte("# Local Only Skill\n")
	res, err := publisherNoRemote.Publish(ctx, source.PublishRequest{
		SkillID:              "local-skill",
		CandidateSourceBytes: content,
	})
	if err != nil {
		t.Fatalf("local publish failed: %v", err)
	}
	if !strings.HasPrefix(res.OriginRef, "explorarte-org/skills@") {
		t.Fatalf("unexpected local origin ref: %s", res.OriginRef)
	}
}

func TestPublicationIdempotencyAndCrashRecovery(t *testing.T) {
	ctx := context.Background()

	bareRemoteDir := setupBareRemote(t)

	publisherDir := t.TempDir()
	initGitRepo(t, publisherDir)
	runCmd(t, publisherDir, "git", "remote", "add", "origin", bareRemoteDir)
	runCmd(t, publisherDir, "git", "push", "-u", "origin", "main")

	publisher, _ := skillpublisher.NewGitPublisher(skillpublisher.GitPublisherConfig{
		RepoDir:           publisherDir,
		RemoteName:        "origin",
		ExpectedRemoteURL: bareRemoteDir,
		Branch:            "main",
		Owner:             "explorarte-org",
		Repo:              "skills",
		RequireRemote:     true,
	})

	content := []byte("# Idempotent Skill\n\nMust produce exact same commit on retry.\n")
	req := source.PublishRequest{
		SkillID:              "idempotent-skill",
		CandidateSourceBytes: content,
		Metadata:             map[string]string{"organization_id": "explorarte"},
	}

	pub1, err := publisher.Publish(ctx, req)
	if err != nil {
		t.Fatalf("pub1 failed: %v", err)
	}

	pub2, err := publisher.Publish(ctx, req)
	if err != nil {
		t.Fatalf("pub2 failed: %v", err)
	}

	if pub1.OriginRef != pub2.OriginRef {
		t.Fatalf("expected identical commit SHA on retry: %s vs %s", pub1.OriginRef, pub2.OriginRef)
	}
	if pub1.PublicationRef != pub2.PublicationRef {
		t.Fatalf("expected identical publication ref: %s vs %s", pub1.PublicationRef, pub2.PublicationRef)
	}
}

func TestGitPinnedSourceReaderAndIsolation(t *testing.T) {
	ctx := context.Background()

	bareRemoteDir := setupBareRemote(t)

	publisherDir := t.TempDir()
	initGitRepo(t, publisherDir)
	runCmd(t, publisherDir, "git", "remote", "add", "origin", bareRemoteDir)
	runCmd(t, publisherDir, "git", "push", "-u", "origin", "main")

	publisher, _ := skillpublisher.NewGitPublisher(skillpublisher.GitPublisherConfig{
		RepoDir:           publisherDir,
		RemoteName:        "origin",
		ExpectedRemoteURL: bareRemoteDir,
		Branch:            "main",
		Owner:             "explorarte-org",
		Repo:              "skills",
		RequireRemote:     true,
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

	mirrorDir := t.TempDir()
	runCmd(t, mirrorDir, "git", "clone", bareRemoteDir, ".")

	reader, err := skillpublisher.NewGitPinnedSourceReader(mirrorDir)
	if err != nil {
		t.Fatalf("create GitPinnedSourceReader: %v", err)
	}

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

	// WORKING_TREE_DRIFT_ISOLATED:
	mirrorFilePath := filepath.Join(mirrorDir, filepath.FromSlash(pubRes.Path))
	_ = os.MkdirAll(filepath.Dir(mirrorFilePath), 0755)
	if err := os.WriteFile(mirrorFilePath, []byte("CORRUPTED WORKING TREE CONTENT"), 0644); err != nil {
		t.Fatalf("corrupt working tree file: %v", err)
	}

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

	// BRANCH_MOVEMENT_REPRODUCIBLE:
	contentV2 := []byte("# Version 2\n\nNew version content.\n")
	_, err = publisher.Publish(ctx, source.PublishRequest{
		SkillID:              "pinned-test-skill-v2",
		CandidateSourceBytes: contentV2,
		Metadata:             map[string]string{"organization_id": "explorarte"},
	})
	if err != nil {
		t.Fatalf("publish v2 failed: %v", err)
	}

	runCmd(t, mirrorDir, "git", "fetch", "origin")

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

	// Missing commit / missing path
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

	bareRemoteDir := setupBareRemote(t)

	publisherDir := t.TempDir()
	initGitRepo(t, publisherDir)
	runCmd(t, publisherDir, "git", "remote", "add", "origin", bareRemoteDir)
	runCmd(t, publisherDir, "git", "push", "-u", "origin", "main")

	publisher, _ := skillpublisher.NewGitPublisher(skillpublisher.GitPublisherConfig{
		RepoDir:           publisherDir,
		RemoteName:        "origin",
		ExpectedRemoteURL: bareRemoteDir,
		Branch:            "main",
		Owner:             "explorarte-org",
		Repo:              "skills",
		RequireRemote:     true,
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

	// Wrong raw hash -> DENY
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
}

// Section A Test: REMOTE_BRANCH_ADVANCES_AFTER_PUBLICATION
func TestRemoteBranchAdvancesAfterPublication(t *testing.T) {
	ctx := context.Background()

	bareRemoteDir := setupBareRemote(t)

	publisherDir := t.TempDir()
	initGitRepo(t, publisherDir)
	runCmd(t, publisherDir, "git", "remote", "add", "origin", bareRemoteDir)
	runCmd(t, publisherDir, "git", "push", "-u", "origin", "main")

	publisher, err := skillpublisher.NewGitPublisher(skillpublisher.GitPublisherConfig{
		RepoDir:           publisherDir,
		RemoteName:        "origin",
		ExpectedRemoteURL: bareRemoteDir,
		Branch:            "main",
		Owner:             "explorarte-org",
		Repo:              "skills",
		RequireRemote:     true,
	})
	if err != nil {
		t.Fatalf("NewGitPublisher failed: %v", err)
	}

	contentC := []byte("# Skill C\n\nCandidate C procedure.\n")
	pubC, err := publisher.Publish(ctx, source.PublishRequest{
		SkillID:              "skill-branch-advance",
		CandidateSourceBytes: contentC,
		Metadata:             map[string]string{"organization_id": "explorarte"},
	})
	if err != nil {
		t.Fatalf("Publish C failed: %v", err)
	}

	// Another publisher advances main branch C -> D on remote
	otherDir := t.TempDir()
	runCmd(t, otherDir, "git", "clone", bareRemoteDir, ".")
	runCmd(t, otherDir, "git", "config", "user.name", "Other Publisher")
	runCmd(t, otherDir, "git", "config", "user.email", "other@explorarte.test")
	dummyFile := filepath.Join(otherDir, "dummy.txt")
	_ = os.WriteFile(dummyFile, []byte("Branch advance D\n"), 0644)
	runCmd(t, otherDir, "git", "add", "dummy.txt")
	runCmd(t, otherDir, "git", "commit", "-m", "advance branch C -> D")
	runCmd(t, otherDir, "git", "push", "origin", "main")

	// Verify remote branch now points to D, NOT C
	remoteHead := runCmd(t, bareRemoteDir, "git", "rev-parse", "refs/heads/main")
	commitC := strings.Split(pubC.OriginRef, "@")[1]
	if strings.TrimSpace(remoteHead) == commitC {
		t.Fatal("expected remote branch to have advanced beyond commit C")
	}

	// Verify immutable publication ref for C remains C on remote
	lsTag := runCmd(t, publisherDir, "git", "ls-remote", "origin", pubC.PublicationRef)
	if !strings.Contains(lsTag, commitC) {
		t.Fatalf("immutable publication ref %s does not point to commit C: %s", pubC.PublicationRef, lsTag)
	}
}

// Section A Test: CRASH_AFTER_BRANCH_PUSH_BEFORE_TAG
func TestCrashAfterBranchPushBeforeTag(t *testing.T) {
	ctx := context.Background()

	bareRemoteDir := setupBareRemote(t)

	publisherDir := t.TempDir()
	initGitRepo(t, publisherDir)
	runCmd(t, publisherDir, "git", "remote", "add", "origin", bareRemoteDir)
	runCmd(t, publisherDir, "git", "push", "-u", "origin", "main")

	content := []byte("# Skill Crash Tag\n\nContent.\n")
	rawSum := sha256.Sum256(content)
	rawSHA := hex.EncodeToString(rawSum[:])
	pubKeySum := sha256.Sum256([]byte("explorarte" + "crash-skill" + rawSHA))
	pubKey := hex.EncodeToString(pubKeySum[:])

	// Simulate crash: Commit created and pushed to branch, but tag NOT created/pushed
	skillPath := filepath.Join(publisherDir, "skills", "crash-skill", "SKILL.md")
	_ = os.MkdirAll(filepath.Dir(skillPath), 0755)
	_ = os.WriteFile(skillPath, content, 0644)
	runCmd(t, publisherDir, "git", "add", "skills/crash-skill/SKILL.md")
	commitMsg := fmt.Sprintf("chore(skills): publish crash-skill candidate\n\nPublication-Key: %s", pubKey)
	runCmd(t, publisherDir, "git", "commit", "-m", commitMsg)
	commitSHA := strings.TrimSpace(runCmd(t, publisherDir, "git", "rev-parse", "HEAD"))
	runCmd(t, publisherDir, "git", "push", "origin", "main")

	// Verify tag does NOT exist yet on remote
	lsTagBefore := runCmd(t, publisherDir, "git", "ls-remote", "origin", fmt.Sprintf("refs/tags/skillforge/%s", pubKey))
	if strings.TrimSpace(lsTagBefore) != "" {
		t.Fatal("expected tag to not exist prior to retry")
	}

	publisher, _ := skillpublisher.NewGitPublisher(skillpublisher.GitPublisherConfig{
		RepoDir:           publisherDir,
		RemoteName:        "origin",
		ExpectedRemoteURL: bareRemoteDir,
		Branch:            "main",
		Owner:             "explorarte-org",
		Repo:              "skills",
		RequireRemote:     true,
	})

	pubRes, err := publisher.Publish(ctx, source.PublishRequest{
		SkillID:              "crash-skill",
		CandidateSourceBytes: content,
		Metadata:             map[string]string{"organization_id": "explorarte"},
	})
	if err != nil {
		t.Fatalf("retry publish failed: %v", err)
	}

	if !strings.HasSuffix(pubRes.OriginRef, commitSHA) {
		t.Fatalf("expected retry to reuse commit %s, got %s", commitSHA, pubRes.OriginRef)
	}

	lsTagAfter := runCmd(t, publisherDir, "git", "ls-remote", "origin", fmt.Sprintf("refs/tags/skillforge/%s", pubKey))
	if !strings.Contains(lsTagAfter, commitSHA) {
		t.Fatalf("expected remote tag to point to %s, got %s", commitSHA, lsTagAfter)
	}
}

// Section A Test: CRASH_AFTER_TAG_BEFORE_LOCAL_PERSIST
func TestCrashAfterTagBeforeLocalPersist(t *testing.T) {
	ctx := context.Background()

	bareRemoteDir := setupBareRemote(t)

	publisherDir := t.TempDir()
	initGitRepo(t, publisherDir)
	runCmd(t, publisherDir, "git", "remote", "add", "origin", bareRemoteDir)
	runCmd(t, publisherDir, "git", "push", "-u", "origin", "main")

	publisher1, _ := skillpublisher.NewGitPublisher(skillpublisher.GitPublisherConfig{
		RepoDir:           publisherDir,
		RemoteName:        "origin",
		ExpectedRemoteURL: bareRemoteDir,
		Branch:            "main",
		Owner:             "explorarte-org",
		Repo:              "skills",
		RequireRemote:     true,
	})

	content := []byte("# Fresh Clone Skill\n\nDeterministic content.\n")
	pub1, err := publisher1.Publish(ctx, source.PublishRequest{
		SkillID:              "fresh-clone-skill",
		CandidateSourceBytes: content,
		Metadata:             map[string]string{"organization_id": "explorarte"},
	})
	if err != nil {
		t.Fatalf("initial publish failed: %v", err)
	}

	freshHostDir := t.TempDir()
	runCmd(t, freshHostDir, "git", "clone", bareRemoteDir, ".")
	runCmd(t, freshHostDir, "git", "config", "user.name", "Fresh Host Publisher")
	runCmd(t, freshHostDir, "git", "config", "user.email", "fresh@explorarte.test")

	publisherFresh, _ := skillpublisher.NewGitPublisher(skillpublisher.GitPublisherConfig{
		RepoDir:           freshHostDir,
		RemoteName:        "origin",
		ExpectedRemoteURL: bareRemoteDir,
		Branch:            "main",
		Owner:             "explorarte-org",
		Repo:              "skills",
		RequireRemote:     true,
	})

	pub2, err := publisherFresh.Publish(ctx, source.PublishRequest{
		SkillID:              "fresh-clone-skill",
		CandidateSourceBytes: content,
		Metadata:             map[string]string{"organization_id": "explorarte"},
	})
	if err != nil {
		t.Fatalf("fresh host publish failed: %v", err)
	}

	if pub2.OriginRef != pub1.OriginRef {
		t.Fatalf("expected fresh host to reuse exact commit %s, got %s", pub1.OriginRef, pub2.OriginRef)
	}
	if pub2.PublicationRef != pub1.PublicationRef {
		t.Fatalf("expected publication ref %s, got %s", pub1.PublicationRef, pub2.PublicationRef)
	}
}

// Section A Test: PUBLICATION_TAG_EXISTS_DIFFERENT_SHA
func TestPublicationTagExistsDifferentSHA(t *testing.T) {
	ctx := context.Background()

	bareRemoteDir := setupBareRemote(t)

	publisherDir := t.TempDir()
	initGitRepo(t, publisherDir)
	runCmd(t, publisherDir, "git", "remote", "add", "origin", bareRemoteDir)
	runCmd(t, publisherDir, "git", "push", "-u", "origin", "main")

	contentOriginal := []byte("# Original Content\n")
	rawSum := sha256.Sum256(contentOriginal)
	rawSHA := hex.EncodeToString(rawSum[:])
	pubKeySum := sha256.Sum256([]byte("explorarte" + "collision-skill" + rawSHA))
	pubKey := hex.EncodeToString(pubKeySum[:])
	tagRef := fmt.Sprintf("refs/tags/skillforge/%s", pubKey)

	maliciousDir := t.TempDir()
	runCmd(t, maliciousDir, "git", "clone", bareRemoteDir, ".")
	runCmd(t, maliciousDir, "git", "config", "user.name", "Malicious")
	runCmd(t, maliciousDir, "git", "config", "user.email", "malicious@explorarte.test")
	fakeSkillPath := filepath.Join(maliciousDir, "skills", "collision-skill", "SKILL.md")
	_ = os.MkdirAll(filepath.Dir(fakeSkillPath), 0755)
	_ = os.WriteFile(fakeSkillPath, []byte("# Tampered Content\n"), 0644)
	runCmd(t, maliciousDir, "git", "add", "skills/collision-skill/SKILL.md")
	runCmd(t, maliciousDir, "git", "commit", "-m", "tampered commit")
	tamperedSHA := strings.TrimSpace(runCmd(t, maliciousDir, "git", "rev-parse", "HEAD"))
	runCmd(t, maliciousDir, "git", "tag", fmt.Sprintf("skillforge/%s", pubKey), tamperedSHA)
	runCmd(t, maliciousDir, "git", "push", "origin", tagRef)

	publisher, _ := skillpublisher.NewGitPublisher(skillpublisher.GitPublisherConfig{
		RepoDir:           publisherDir,
		RemoteName:        "origin",
		ExpectedRemoteURL: bareRemoteDir,
		Branch:            "main",
		Owner:             "explorarte-org",
		Repo:              "skills",
		RequireRemote:     true,
	})

	_, err := publisher.Publish(ctx, source.PublishRequest{
		SkillID:              "collision-skill",
		CandidateSourceBytes: contentOriginal,
		Metadata:             map[string]string{"organization_id": "explorarte"},
	})
	if err == nil || !errors.Is(err, skillpublisher.ErrPublicationCollision) {
		t.Fatalf("expected ErrPublicationCollision, got %v", err)
	}
}

// Section B Test: MATERIALIZE_V1_WHILE_REPO_HEAD_V2
func TestMaterializeV1WhileRepoHeadV2(t *testing.T) {
	ctx := context.Background()

	bareRemoteDir := setupBareRemote(t)

	publisherDir := t.TempDir()
	initGitRepo(t, publisherDir)
	runCmd(t, publisherDir, "git", "remote", "add", "origin", bareRemoteDir)
	runCmd(t, publisherDir, "git", "push", "-u", "origin", "main")

	publisher, err := skillpublisher.NewGitPublisher(skillpublisher.GitPublisherConfig{
		RepoDir:           publisherDir,
		RemoteName:        "origin",
		ExpectedRemoteURL: bareRemoteDir,
		Branch:            "main",
		Owner:             "explorarte-org",
		Repo:              "skills",
		RequireRemote:     true,
	})
	if err != nil {
		t.Fatalf("NewGitPublisher failed: %v", err)
	}

	contentV1 := []byte("# Skill V1\n\nVersion 1 procedure.\n")
	pubV1, err := publisher.Publish(ctx, source.PublishRequest{
		SkillID:              "repo-isolation-skill",
		CandidateSourceBytes: contentV1,
		Metadata:             map[string]string{"organization_id": "explorarte"},
	})
	if err != nil {
		t.Fatalf("publish v1 failed: %v", err)
	}

	contentV2 := []byte("# Skill V2\n\nVersion 2 procedure.\n")
	_, err = publisher.Publish(ctx, source.PublishRequest{
		SkillID:              "repo-isolation-skill",
		CandidateSourceBytes: contentV2,
		Metadata:             map[string]string{"organization_id": "explorarte"},
	})
	if err != nil {
		t.Fatalf("publish v2 failed: %v", err)
	}

	workingTreeBytes, _ := os.ReadFile(filepath.Join(publisherDir, pubV1.Path))
	if string(workingTreeBytes) != string(contentV2) {
		t.Fatalf("expected repo working tree to be at V2, got %q", string(workingTreeBytes))
	}

	reader, err := skillpublisher.NewGitPinnedSourceReader(publisherDir)
	if err != nil {
		t.Fatalf("NewGitPinnedSourceReader failed: %v", err)
	}

	runtimeRoot := t.TempDir()
	materializer, err := source.NewLocalMaterializer(runtimeRoot, reader)
	if err != nil {
		t.Fatalf("NewLocalMaterializer failed: %v", err)
	}

	matRec, err := materializer.Materialize(ctx, source.MaterializeRequest{
		OriginRef:       pubV1.OriginRef,
		RelativePath:    pubV1.Path,
		ExpectedRawSHA:  pubV1.RawSHA256,
		ExpectedNormSHA: pubV1.NormalizedSHA256,
		RecordedBy:      "empresa/test",
		RecordRef:       "mat-hist-v1",
	})
	if err != nil {
		t.Fatalf("materialize historical v1 failed: %v", err)
	}

	matBytes, _ := os.ReadFile(filepath.Join(runtimeRoot, matRec.Path))
	if string(matBytes) != string(contentV1) {
		t.Fatalf("expected runtime root to contain V1, got %q", string(matBytes))
	}

	statusOut := runCmd(t, publisherDir, "git", "status", "--porcelain")
	if strings.TrimSpace(statusOut) != "" {
		t.Fatalf("expected git working tree to be clean, got:\n%s", statusOut)
	}
	repoBytesAfter, _ := os.ReadFile(filepath.Join(publisherDir, pubV1.Path))
	if string(repoBytesAfter) != string(contentV2) {
		t.Fatalf("git working tree was mutated by materialization: expected V2, got %q", string(repoBytesAfter))
	}
}

// Section B Test: PUBLISH_V3_WHILE_RUNTIME_V1
func TestPublishV3WhileRuntimeV1(t *testing.T) {
	ctx := context.Background()

	bareRemoteDir := setupBareRemote(t)

	publisherDir := t.TempDir()
	initGitRepo(t, publisherDir)
	runCmd(t, publisherDir, "git", "remote", "add", "origin", bareRemoteDir)
	runCmd(t, publisherDir, "git", "push", "-u", "origin", "main")

	publisher, err := skillpublisher.NewGitPublisher(skillpublisher.GitPublisherConfig{
		RepoDir:           publisherDir,
		RemoteName:        "origin",
		ExpectedRemoteURL: bareRemoteDir,
		Branch:            "main",
		Owner:             "explorarte-org",
		Repo:              "skills",
		RequireRemote:     true,
	})
	if err != nil {
		t.Fatalf("NewGitPublisher failed: %v", err)
	}

	contentV1 := []byte("# Runtime Test Skill V1\n\nContent V1.\n")
	pubV1, err := publisher.Publish(ctx, source.PublishRequest{
		SkillID:              "runtime-isolation-skill",
		CandidateSourceBytes: contentV1,
		Metadata:             map[string]string{"organization_id": "explorarte"},
	})
	if err != nil {
		t.Fatalf("publish v1 failed: %v", err)
	}

	runtimeRoot := t.TempDir()
	reader, _ := skillpublisher.NewGitPinnedSourceReader(publisherDir)
	materializer, _ := source.NewLocalMaterializer(runtimeRoot, reader)

	_, err = materializer.Materialize(ctx, source.MaterializeRequest{
		OriginRef:       pubV1.OriginRef,
		RelativePath:    pubV1.Path,
		ExpectedRawSHA:  pubV1.RawSHA256,
		ExpectedNormSHA: pubV1.NormalizedSHA256,
		RecordedBy:      "empresa/test",
		RecordRef:       "mat-v1",
	})
	if err != nil {
		t.Fatalf("materialize v1 failed: %v", err)
	}

	contentV3 := []byte("# Runtime Test Skill V3\n\nContent V3 Candidate.\n")
	_, err = publisher.Publish(ctx, source.PublishRequest{
		SkillID:              "runtime-isolation-skill",
		CandidateSourceBytes: contentV3,
		Metadata:             map[string]string{"organization_id": "explorarte"},
	})
	if err != nil {
		t.Fatalf("publish v3 failed: %v", err)
	}

	runtimeBytes, err := os.ReadFile(filepath.Join(runtimeRoot, pubV1.Path))
	if err != nil {
		t.Fatalf("read runtime file: %v", err)
	}
	if string(runtimeBytes) != string(contentV1) {
		t.Fatalf("runtime root was mutated by publish: expected V1, got %q", string(runtimeBytes))
	}
}

// Section B Test: CONCURRENT_PUBLISH_AND_MATERIALIZE
func TestConcurrentPublishAndMaterialize(t *testing.T) {
	ctx := context.Background()

	bareRemoteDir := setupBareRemote(t)

	publisherDir := t.TempDir()
	initGitRepo(t, publisherDir)
	runCmd(t, publisherDir, "git", "remote", "add", "origin", bareRemoteDir)
	runCmd(t, publisherDir, "git", "push", "-u", "origin", "main")

	publisher, err := skillpublisher.NewGitPublisher(skillpublisher.GitPublisherConfig{
		RepoDir:           publisherDir,
		RemoteName:        "origin",
		ExpectedRemoteURL: bareRemoteDir,
		Branch:            "main",
		Owner:             "explorarte-org",
		Repo:              "skills",
		RequireRemote:     true,
	})
	if err != nil {
		t.Fatalf("NewGitPublisher failed: %v", err)
	}

	runtimeRoot := t.TempDir()
	reader, _ := skillpublisher.NewGitPinnedSourceReader(publisherDir)
	materializer, _ := source.NewLocalMaterializer(runtimeRoot, reader)

	contentV0 := []byte("# Concurrent Base Skill V0\n\nSeed content.\n")
	pubV0, err := publisher.Publish(ctx, source.PublishRequest{
		SkillID:              "concurrent-base",
		CandidateSourceBytes: contentV0,
		Metadata:             map[string]string{"organization_id": "explorarte"},
	})
	if err != nil {
		t.Fatalf("seed publish failed: %v", err)
	}

	var wg sync.WaitGroup
	errCh := make(chan error, 10)

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 5; i++ {
			rec, err := materializer.Materialize(ctx, source.MaterializeRequest{
				OriginRef:       pubV0.OriginRef,
				RelativePath:    pubV0.Path,
				ExpectedRawSHA:  pubV0.RawSHA256,
				ExpectedNormSHA: pubV0.NormalizedSHA256,
				RecordedBy:      "empresa/concurrent",
				RecordRef:       fmt.Sprintf("mat-%d", i),
			})
			if err != nil {
				errCh <- fmt.Errorf("concurrent materialize %d failed: %w", i, err)
				return
			}
			if rec.SHA256 != pubV0.RawSHA256 {
				errCh <- fmt.Errorf("digest mismatch on concurrent materialize")
				return
			}
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 1; i <= 5; i++ {
			c := []byte(fmt.Sprintf("# Candidate %d\n\nContent %d.\n", i, i))
			pub, err := publisher.Publish(ctx, source.PublishRequest{
				SkillID:              fmt.Sprintf("concurrent-cand-%d", i),
				CandidateSourceBytes: c,
				Metadata:             map[string]string{"organization_id": "explorarte"},
			})
			if err != nil {
				errCh <- fmt.Errorf("concurrent publish %d failed: %w", i, err)
				return
			}
			sum := sha256.Sum256(c)
			if pub.RawSHA256 != hex.EncodeToString(sum[:]) {
				errCh <- fmt.Errorf("publish %d raw sha mismatch", i)
				return
			}
		}
	}()

	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}
}

// Section B Test: RUNTIME_ROOT_CONTAINS_NO_GIT_AUTHORITY & RUNTIME_ROOT_REPO_ROOT_MUST_DIFFER
func TestRuntimeRootAuthorityAndSeparation(t *testing.T) {
	publisherDir := t.TempDir()
	initGitRepo(t, publisherDir)

	reader, err := skillpublisher.NewGitPinnedSourceReader(publisherDir)
	if err != nil {
		t.Fatalf("create reader: %v", err)
	}

	// 1. RUNTIME_ROOT_REPO_ROOT_MUST_DIFFER: configuring runtime root identical to repo root fails
	_, err = source.NewLocalMaterializer(publisherDir, reader)
	if err == nil || !errors.Is(err, source.ErrInvalidRuntimeRoot) {
		t.Fatalf("expected ErrInvalidRuntimeRoot when runtime root == repo root, got %v", err)
	}

	// 2. RUNTIME_ROOT_CONTAINS_NO_GIT_AUTHORITY: configuring runtime root with .git directory fails
	fakeGitDir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(fakeGitDir, ".git"), 0755)
	_, err = source.NewLocalMaterializer(fakeGitDir, reader)
	if err == nil || !errors.Is(err, source.ErrInvalidRuntimeRoot) {
		t.Fatalf("expected ErrInvalidRuntimeRoot when runtime root contains .git, got %v", err)
	}

	// 3. Valid separated runtime root passes and has NO .git authority
	validRuntime := t.TempDir()
	mat, err := source.NewLocalMaterializer(validRuntime, reader)
	if err != nil {
		t.Fatalf("expected valid runtime root to succeed, got %v", err)
	}
	if mat == nil {
		t.Fatal("materializer is nil")
	}
	if _, err := os.Stat(filepath.Join(validRuntime, ".git")); err == nil {
		t.Fatal("runtime root must NOT contain .git directory")
	}
}

func TestRemoteIdentityVerification(t *testing.T) {
	ctx := context.Background()

	t.Run("EXPECTED_REMOTE_MATCHES_ORIGIN", func(t *testing.T) {
		bareRemote := setupBareRemote(t)
		publisherDir := t.TempDir()
		initGitRepo(t, publisherDir)
		runCmd(t, publisherDir, "git", "remote", "add", "origin", bareRemote)
		runCmd(t, publisherDir, "git", "push", "-u", "origin", "main")

		pub, err := skillpublisher.NewGitPublisher(skillpublisher.GitPublisherConfig{
			RepoDir:           publisherDir,
			RemoteName:        "origin",
			ExpectedRemoteURL: bareRemote,
			Branch:            "main",
			Owner:             "explorarte-org",
			Repo:              "skills",
			RequireRemote:     true,
		})
		if err != nil {
			t.Fatalf("NewGitPublisher: %v", err)
		}

		ident, err := pub.VerifyRemote(ctx)
		if err != nil {
			t.Fatalf("expected remote to match origin, got error: %v", err)
		}
		if ident.Owner != "explorarte-org" || ident.Repo != "skills" {
			t.Fatalf("unexpected identity: %+v", ident)
		}

		// Also verify via Publish
		res, err := pub.Publish(ctx, source.PublishRequest{
			SkillID:              "matches-origin-skill",
			CandidateSourceBytes: []byte("# Matches Origin\n"),
			Metadata:             map[string]string{"organization_id": "explorarte"},
		})
		if err != nil {
			t.Fatalf("Publish: %v", err)
		}
		if !strings.HasPrefix(res.OriginRef, "explorarte-org/skills@") {
			t.Fatalf("unexpected origin ref: %s", res.OriginRef)
		}
		t.Log("✓ EXPECTED_REMOTE_MATCHES_ORIGIN PASS")
	})

	t.Run("EXPECTED_REMOTE_DIFFERS_FROM_ORIGIN", func(t *testing.T) {
		repoDir := t.TempDir()
		initGitRepo(t, repoDir)
		runCmd(t, repoDir, "git", "remote", "add", "origin", "git@github.com:explorarte-org/skills.git")

		pub, err := skillpublisher.NewGitPublisher(skillpublisher.GitPublisherConfig{
			RepoDir:           repoDir,
			RemoteName:        "origin",
			ExpectedRemoteURL: "git@github.com:explorarte-org/different-skills.git",
			Branch:            "main",
			Owner:             "explorarte-org",
			Repo:              "skills",
			RequireRemote:     true,
		})
		if err != nil {
			t.Fatalf("NewGitPublisher: %v", err)
		}

		_, err = pub.VerifyRemote(ctx)
		if err == nil {
			t.Fatal("expected remote verification to DENY when expected remote differs from origin")
		}
		if !errors.Is(err, skillpublisher.ErrRemoteVerificationFailed) {
			t.Fatalf("expected ErrRemoteVerificationFailed, got: %v", err)
		}

		_, err = pub.Publish(ctx, source.PublishRequest{
			SkillID:              "differs-skill",
			CandidateSourceBytes: []byte("# Differs\n"),
		})
		if err == nil {
			t.Fatal("expected Publish to fail closed when expected remote differs")
		}
		t.Log("✓ EXPECTED_REMOTE_DIFFERS_FROM_ORIGIN DENY")
	})

	t.Run("RIGHT_OWNER_WRONG_REPO", func(t *testing.T) {
		repoDir := t.TempDir()
		initGitRepo(t, repoDir)
		runCmd(t, repoDir, "git", "remote", "add", "origin", "git@github.com:explorarte-org/skills-evil.git")

		pub, err := skillpublisher.NewGitPublisher(skillpublisher.GitPublisherConfig{
			RepoDir:           repoDir,
			RemoteName:        "origin",
			ExpectedRemoteURL: "git@github.com:explorarte-org/skills.git",
			Branch:            "main",
			Owner:             "explorarte-org",
			Repo:              "skills",
			RequireRemote:     true,
		})
		if err != nil {
			t.Fatalf("NewGitPublisher: %v", err)
		}

		_, err = pub.VerifyRemote(ctx)
		if err == nil {
			t.Fatal("expected remote verification to DENY on right owner wrong repo")
		}
		if !strings.Contains(err.Error(), "repository mismatch") {
			t.Fatalf("expected repository mismatch error, got: %v", err)
		}
		t.Log("✓ RIGHT_OWNER_WRONG_REPO DENY")
	})

	t.Run("WRONG_OWNER_RIGHT_REPO", func(t *testing.T) {
		repoDir := t.TempDir()
		initGitRepo(t, repoDir)
		runCmd(t, repoDir, "git", "remote", "add", "origin", "git@github.com:other-org/skills.git")

		pub, err := skillpublisher.NewGitPublisher(skillpublisher.GitPublisherConfig{
			RepoDir:           repoDir,
			RemoteName:        "origin",
			ExpectedRemoteURL: "git@github.com:explorarte-org/skills.git",
			Branch:            "main",
			Owner:             "explorarte-org",
			Repo:              "skills",
			RequireRemote:     true,
		})
		if err != nil {
			t.Fatalf("NewGitPublisher: %v", err)
		}

		_, err = pub.VerifyRemote(ctx)
		if err == nil {
			t.Fatal("expected remote verification to DENY on wrong owner right repo")
		}
		if !strings.Contains(err.Error(), "owner mismatch") {
			t.Fatalf("expected owner mismatch error, got: %v", err)
		}
		t.Log("✓ WRONG_OWNER_RIGHT_REPO DENY")
	})

	t.Run("WRONG_HOST", func(t *testing.T) {
		repoDir := t.TempDir()
		initGitRepo(t, repoDir)
		runCmd(t, repoDir, "git", "remote", "add", "origin", "https://gitlab.com/explorarte-org/skills.git")

		pub, err := skillpublisher.NewGitPublisher(skillpublisher.GitPublisherConfig{
			RepoDir:           repoDir,
			RemoteName:        "origin",
			ExpectedRemoteURL: "https://github.com/explorarte-org/skills.git",
			Branch:            "main",
			Owner:             "explorarte-org",
			Repo:              "skills",
			RequireRemote:     true,
		})
		if err != nil {
			t.Fatalf("NewGitPublisher: %v", err)
		}

		_, err = pub.VerifyRemote(ctx)
		if err == nil {
			t.Fatal("expected remote verification to DENY on wrong host")
		}
		if !strings.Contains(err.Error(), "host mismatch") {
			t.Fatalf("expected host mismatch error, got: %v", err)
		}
		t.Log("✓ WRONG_HOST DENY")
	})

	t.Run("SSH_AND_HTTPS_EQUIVALENT_FOR_SAME_GITHUB_REPO", func(t *testing.T) {
		// Test A: actual remote is git@github.com, expected is https://github.com
		repoDirA := t.TempDir()
		initGitRepo(t, repoDirA)
		runCmd(t, repoDirA, "git", "remote", "add", "origin", "git@github.com:explorarte-org/skills.git")

		pubA, err := skillpublisher.NewGitPublisher(skillpublisher.GitPublisherConfig{
			RepoDir:           repoDirA,
			RemoteName:        "origin",
			ExpectedRemoteURL: "https://github.com/explorarte-org/skills.git",
			Branch:            "main",
			Owner:             "explorarte-org",
			Repo:              "skills",
			RequireRemote:     true,
		})
		if err != nil {
			t.Fatalf("NewGitPublisher: %v", err)
		}
		identA, err := pubA.VerifyRemote(ctx)
		if err != nil {
			t.Fatalf("expected SSH and HTTPS to be equivalent for same repo: %v", err)
		}
		if identA.Host != "github.com" || identA.Owner != "explorarte-org" || identA.Repo != "skills" {
			t.Fatalf("unexpected canonical identity: %+v", identA)
		}

		// Test B: actual remote is https://github.com, expected is git@github.com
		repoDirB := t.TempDir()
		initGitRepo(t, repoDirB)
		runCmd(t, repoDirB, "git", "remote", "add", "origin", "https://github.com/explorarte-org/skills.git")

		pubB, err := skillpublisher.NewGitPublisher(skillpublisher.GitPublisherConfig{
			RepoDir:           repoDirB,
			RemoteName:        "origin",
			ExpectedRemoteURL: "git@github.com:explorarte-org/skills.git",
			Branch:            "main",
			Owner:             "explorarte-org",
			Repo:              "skills",
			RequireRemote:     true,
		})
		if err != nil {
			t.Fatalf("NewGitPublisher: %v", err)
		}
		identB, err := pubB.VerifyRemote(ctx)
		if err != nil {
			t.Fatalf("expected HTTPS and SSH to be equivalent for same repo: %v", err)
		}
		if identB.Host != "github.com" || identB.Owner != "explorarte-org" || identB.Repo != "skills" {
			t.Fatalf("unexpected canonical identity: %+v", identB)
		}
		t.Log("✓ SSH_AND_HTTPS_EQUIVALENT_FOR_SAME_GITHUB_REPO PASS")
	})

	t.Run("ORIGINREF_DERIVED_FROM_VERIFIED_REMOTE", func(t *testing.T) {
		bareRemote := setupBareRemote(t)
		publisherDir := t.TempDir()
		initGitRepo(t, publisherDir)
		runCmd(t, publisherDir, "git", "remote", "add", "origin", bareRemote)
		runCmd(t, publisherDir, "git", "push", "-u", "origin", "main")

		pub, err := skillpublisher.NewGitPublisher(skillpublisher.GitPublisherConfig{
			RepoDir:           publisherDir,
			RemoteName:        "origin",
			ExpectedRemoteURL: bareRemote,
			Branch:            "main",
			Owner:             "explorarte-org",
			Repo:              "skills",
			RequireRemote:     true,
		})
		if err != nil {
			t.Fatalf("NewGitPublisher: %v", err)
		}

		res, err := pub.Publish(ctx, source.PublishRequest{
			SkillID:              "origin-derived-skill",
			CandidateSourceBytes: []byte("# Verified Origin\n"),
			Metadata:             map[string]string{"organization_id": "explorarte"},
		})
		if err != nil {
			t.Fatalf("Publish: %v", err)
		}

		parts := strings.Split(res.OriginRef, "@")
		if len(parts) != 2 || parts[0] != "explorarte-org/skills" || len(parts[1]) != 40 {
			t.Fatalf("expected OriginRef format explorarte-org/skills@40hex, got %q", res.OriginRef)
		}

		tamperedPub, err := skillpublisher.NewGitPublisher(skillpublisher.GitPublisherConfig{
			RepoDir:           publisherDir,
			RemoteName:        "origin",
			ExpectedRemoteURL: bareRemote,
			Branch:            "main",
			Owner:             "tampered-owner",
			Repo:              "skills",
			RequireRemote:     true,
		})
		if err != nil {
			t.Fatalf("NewGitPublisher: %v", err)
		}
		_, err = tamperedPub.Publish(ctx, source.PublishRequest{
			SkillID:              "tampered-skill",
			CandidateSourceBytes: []byte("# Tampered\n"),
		})
		if err == nil {
			t.Fatal("expected Publish to fail closed when configured Owner differs from verified remote")
		}
		t.Log("✓ ORIGINREF_DERIVED_FROM_VERIFIED_REMOTE PASS")
	})

	t.Run("WRONG_PUSHURL_FAILS_BEFORE_PUBLICATION", func(t *testing.T) {
		wrongBareRemote := filepath.Join(t.TempDir(), "wrong-owner", "wrong-repo.git")
		initBareRepo(t, wrongBareRemote)

		publisherDir := t.TempDir()
		initGitRepo(t, publisherDir)
		runCmd(t, publisherDir, "git", "remote", "add", "origin", "git@github.com:explorarte-org/skills.git")
		runCmd(t, publisherDir, "git", "remote", "set-url", "--push", "origin", wrongBareRemote)

		pub, err := skillpublisher.NewGitPublisher(skillpublisher.GitPublisherConfig{
			RepoDir:           publisherDir,
			RemoteName:        "origin",
			ExpectedRemoteURL: "git@github.com:explorarte-org/skills.git",
			Branch:            "main",
			Owner:             "explorarte-org",
			Repo:              "skills",
			RequireRemote:     true,
		})
		if err != nil {
			t.Fatalf("NewGitPublisher: %v", err)
		}

		// 1. VerifyRemote must DENY
		_, err = pub.VerifyRemote(ctx)
		if err == nil {
			t.Fatal("expected VerifyRemote to DENY when push URL differs from expected")
		}
		if !errors.Is(err, skillpublisher.ErrRemoteVerificationFailed) {
			t.Fatalf("expected ErrRemoteVerificationFailed, got %v", err)
		}

		// 2. Publish must DENY BEFORE git add/commit/push side effects to wrong remote
		_, err = pub.Publish(ctx, source.PublishRequest{
			SkillID:              "pushurl-attack-skill",
			CandidateSourceBytes: []byte("# Push URL Attack Candidate\n"),
			Metadata:             map[string]string{"organization_id": "explorarte"},
		})
		if err == nil {
			t.Fatal("expected Publish to fail closed before publishing to wrong pushurl")
		}

		// 3. Verify that wrong remote received ZERO bytes, ZERO branch commits, ZERO tags, ZERO refs
		checkHead := exec.Command("git", "--git-dir="+wrongBareRemote, "rev-parse", "--verify", "refs/heads/main")
		if out, err := checkHead.CombinedOutput(); err == nil {
			t.Fatalf("expected wrong remote to have no main branch, got commit %s", string(out))
		}

		checkTags := exec.Command("git", "--git-dir="+wrongBareRemote, "tag", "-l")
		if out, err := checkTags.CombinedOutput(); err == nil && strings.TrimSpace(string(out)) != "" {
			t.Fatalf("expected wrong remote to have no tags, got %s", string(out))
		}

		checkRefs := exec.Command("git", "--git-dir="+wrongBareRemote, "for-each-ref")
		if out, err := checkRefs.CombinedOutput(); err == nil && strings.TrimSpace(string(out)) != "" {
			t.Fatalf("expected wrong remote to have no refs, got %s", string(out))
		}

		t.Log("✓ WRONG_PUSHURL_FAILS_BEFORE_PUBLICATION PASS")
		t.Log("✓ WRONG_PUSHURL_RECEIVES_ZERO_BYTES PASS")
	})

	t.Run("MATCHING_PUSHURL_SSH_FETCH_HTTPS_PUSH", func(t *testing.T) {
		repoDir := t.TempDir()
		initGitRepo(t, repoDir)
		runCmd(t, repoDir, "git", "remote", "add", "origin", "git@github.com:explorarte-org/skills.git")
		runCmd(t, repoDir, "git", "remote", "set-url", "--push", "origin", "https://github.com/explorarte-org/skills.git")

		pub, err := skillpublisher.NewGitPublisher(skillpublisher.GitPublisherConfig{
			RepoDir:           repoDir,
			RemoteName:        "origin",
			ExpectedRemoteURL: "git@github.com:explorarte-org/skills.git",
			Branch:            "main",
			Owner:             "explorarte-org",
			Repo:              "skills",
			RequireRemote:     true,
		})
		if err != nil {
			t.Fatalf("NewGitPublisher: %v", err)
		}

		ident, err := pub.VerifyRemote(ctx)
		if err != nil {
			t.Fatalf("expected matching fetch/push canonical identities to PASS: %v", err)
		}
		if ident.Host != "github.com" || ident.Owner != "explorarte-org" || ident.Repo != "skills" {
			t.Fatalf("unexpected canonical identity: %+v", ident)
		}
		t.Log("✓ MATCHING_PUSHURL PASS")
	})

	t.Run("MULTIPLE_PUSH_URLS_ONE_WRONG_DENIED", func(t *testing.T) {
		repoDir := t.TempDir()
		initGitRepo(t, repoDir)
		runCmd(t, repoDir, "git", "remote", "add", "origin", "git@github.com:explorarte-org/skills.git")
		// pushurl #1 -> explorarte-org/skills
		runCmd(t, repoDir, "git", "remote", "set-url", "--push", "origin", "git@github.com:explorarte-org/skills.git")
		// pushurl #2 -> wrong/repo
		runCmd(t, repoDir, "git", "remote", "set-url", "--add", "--push", "origin", "git@github.com:wrong-owner/wrong-repo.git")

		pub, err := skillpublisher.NewGitPublisher(skillpublisher.GitPublisherConfig{
			RepoDir:           repoDir,
			RemoteName:        "origin",
			ExpectedRemoteURL: "git@github.com:explorarte-org/skills.git",
			Branch:            "main",
			Owner:             "explorarte-org",
			Repo:              "skills",
			RequireRemote:     true,
		})
		if err != nil {
			t.Fatalf("NewGitPublisher: %v", err)
		}

		_, err = pub.VerifyRemote(ctx)
		if err == nil {
			t.Fatal("expected VerifyRemote to DENY when one of multiple push URLs is wrong")
		}
		if !errors.Is(err, skillpublisher.ErrRemoteVerificationFailed) {
			t.Fatalf("expected ErrRemoteVerificationFailed, got %v", err)
		}
		t.Log("✓ MULTIPLE_PUSH_URLS_ONE_WRONG DENY")
	})

	t.Run("ALL_PUSH_URLS_SAME_EXPECTED_IDENTITY", func(t *testing.T) {
		repoDir := t.TempDir()
		initGitRepo(t, repoDir)
		runCmd(t, repoDir, "git", "remote", "add", "origin", "git@github.com:explorarte-org/skills.git")
		// pushurl #1 -> git@github.com:explorarte-org/skills.git
		runCmd(t, repoDir, "git", "remote", "set-url", "--push", "origin", "git@github.com:explorarte-org/skills.git")
		// pushurl #2 -> https://github.com/explorarte-org/skills.git
		runCmd(t, repoDir, "git", "remote", "set-url", "--add", "--push", "origin", "https://github.com/explorarte-org/skills.git")

		pub, err := skillpublisher.NewGitPublisher(skillpublisher.GitPublisherConfig{
			RepoDir:           repoDir,
			RemoteName:        "origin",
			ExpectedRemoteURL: "git@github.com:explorarte-org/skills.git",
			Branch:            "main",
			Owner:             "explorarte-org",
			Repo:              "skills",
			RequireRemote:     true,
		})
		if err != nil {
			t.Fatalf("NewGitPublisher: %v", err)
		}

		ident, err := pub.VerifyRemote(ctx)
		if err != nil {
			t.Fatalf("expected multiple push URLs with identical canonical identity to PASS: %v", err)
		}
		if ident.Host != "github.com" || ident.Owner != "explorarte-org" || ident.Repo != "skills" {
			t.Fatalf("unexpected canonical identity: %+v", ident)
		}
		t.Log("✓ ALL_PUSH_URLS_SAME_EXPECTED_IDENTITY PASS")
	})
}
