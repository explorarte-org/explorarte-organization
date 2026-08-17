package coderunner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/staging"
	"github.com/Mireuz13/explorarte-organization/internal/tasks"
)

const evidenceSchemaVersion = "code-runner-attempt-evidence/v1"

type operationEvidence struct {
	Ordinal       int    `json:"ordinal"`
	Type          string `json:"type"`
	ExitCode      int    `json:"exit_code"`
	Success       bool   `json:"success"`
	BytesProduced int64  `json:"bytes_produced"`
	OutputDigest  string `json:"output_digest_sha256,omitempty"`
	Truncated     bool   `json:"truncated"`
}

type checkEvidence struct {
	Ordinal      int      `json:"ordinal"`
	Type         string   `json:"type"`
	Packages     []string `json:"packages,omitempty"`
	Race         bool     `json:"race,omitempty"`
	Integration  bool     `json:"integration,omitempty"`
	Success      bool     `json:"success"`
	OutputDigest string   `json:"output_digest_sha256,omitempty"`
}

type candidateRevision struct {
	WorkspaceID     int64  `json:"workspace_id"`
	WorkspaceKey    string `json:"workspace_key,omitempty"`
	CandidateCommit string `json:"candidate_commit,omitempty"`
	CandidateTree   string `json:"candidate_tree,omitempty"`
}

type changedFilesRef struct {
	Count          int    `json:"count"`
	ManifestDigest string `json:"manifest_digest,omitempty"`
}

type executionEnvironment struct {
	CodeRunnerVersion string `json:"code_runner_version"`
	GoVersion         string `json:"go_version,omitempty"`
	GitVersion        string `json:"git_version,omitempty"`
	RipgrepVersion    string `json:"ripgrep_version,omitempty"`
}

type attemptEvidence struct {
	SchemaVersion      string               `json:"schema_version"`
	TaskID             int64                `json:"task_id"`
	AttemptID          int64                `json:"attempt_id"`
	OperationsExecuted []operationEvidence  `json:"operations_executed"`
	ChecksRun          []checkEvidence      `json:"checks_run"`
	CandidateRevision  *candidateRevision   `json:"candidate_revision,omitempty"`
	ChangedFiles       changedFilesRef      `json:"changed_files"`
	ArtifactDigests    map[string]string    `json:"artifact_digests,omitempty"`
	Environment        executionEnvironment `json:"environment"`
}

// buildAttemptEvidence assembles the durable evidence payload for one
// CodeRunner attempt from the plan actually executed, the results actually
// observed, and the workspace actually sealed by staging. It never invents
// data staging did not produce: candidate identity and artifact digests are
// read directly off the sealed staging.Workspace.
func buildAttemptEvidence(taskID, attemptID int64, ops []Operation, results []Result, sealed staging.Workspace, env executionEnvironment) attemptEvidence {
	ev := attemptEvidence{
		SchemaVersion: evidenceSchemaVersion,
		TaskID:        taskID,
		AttemptID:     attemptID,
		Environment:   env,
	}
	for i, r := range results {
		var op Operation
		if i < len(ops) {
			op = ops[i]
		}
		oe := operationEvidence{
			Ordinal:       i + 1,
			Type:          string(r.Type),
			ExitCode:      r.ExitCode,
			Success:       r.Success,
			BytesProduced: r.BytesProduced,
			OutputDigest:  r.OutputDigest,
			Truncated:     r.Truncated,
		}
		ev.OperationsExecuted = append(ev.OperationsExecuted, oe)
		if r.Type.isCheck() {
			ev.ChecksRun = append(ev.ChecksRun, checkEvidence{
				Ordinal:      i + 1,
				Type:         string(r.Type),
				Packages:     op.Packages,
				Race:         op.Race,
				Integration:  op.Integration,
				Success:      r.Success,
				OutputDigest: r.OutputDigest,
			})
		}
	}
	ev.CandidateRevision = &candidateRevision{WorkspaceID: sealed.ID, WorkspaceKey: sealed.WorkspaceKey}
	if sealed.CandidateCommit != nil {
		ev.CandidateRevision.CandidateCommit = *sealed.CandidateCommit
	}
	if sealed.CandidateTree != nil {
		ev.CandidateRevision.CandidateTree = *sealed.CandidateTree
	}
	if sealed.ChangedFileCount != nil {
		ev.ChangedFiles.Count = *sealed.ChangedFileCount
	}
	digests := map[string]string{}
	if sealed.ManifestDigest != nil {
		digests["manifest_digest"] = *sealed.ManifestDigest
		ev.ChangedFiles.ManifestDigest = *sealed.ManifestDigest
	}
	if sealed.PatchDigest != nil {
		digests["patch_digest"] = *sealed.PatchDigest
	}
	if len(digests) > 0 {
		ev.ArtifactDigests = digests
	}
	return ev
}

// recordAttemptEvidence persists ev through the durable evidence ledger
// (tasks.RecordEvidence, backed by PostgreSQL) before the caller is allowed
// to report the attempt as succeeded. If this fails, the caller MUST NOT
// report success: there would be no durable proof the sealed candidate was
// ever actually verified.
func recordAttemptEvidence(ctx context.Context, queue Queue, recordedBy string, ev attemptEvidence) error {
	raw, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("marshal attempt evidence: %w", err)
	}
	var metadata map[string]any
	if err := json.Unmarshal(raw, &metadata); err != nil {
		return fmt.Errorf("decode attempt evidence: %w", err)
	}
	sum := sha256.Sum256(raw)
	_, err = queue.RecordEvidence(ctx, tasks.RecordEvidenceCommand{
		TaskID:     ev.TaskID,
		Type:       tasks.RequirementResult,
		Reference:  fmt.Sprintf("code-runner-attempt-evidence://task/%d/attempt/%d", ev.TaskID, ev.AttemptID),
		Digest:     hex.EncodeToString(sum[:]),
		RecordedBy: recordedBy,
		Metadata:   metadata,
	})
	if err != nil {
		return fmt.Errorf("record attempt evidence: %w", err)
	}
	return nil
}

// detectEnvironment captures the toolchain identity CodeRunner actually
// executed with, so a later reviewer can reproduce the run. runtimeVersion
// comes from trusted build/deploy metadata (never from task input).
func detectEnvironment(ctx context.Context, runtimeVersion string) executionEnvironment {
	env := executionEnvironment{CodeRunnerVersion: runtimeVersion}
	env.GoVersion = shortVersion(ctx, "go", "version")
	env.GitVersion = shortVersion(ctx, "git", "--version")
	env.RipgrepVersion = shortVersion(ctx, "rg", "--version")
	return env
}

func shortVersion(ctx context.Context, name string, args ...string) string {
	runCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(runCtx, name, args...).Output()
	if err != nil {
		return ""
	}
	line := strings.SplitN(string(out), "\n", 2)[0]
	return strings.TrimSpace(line)
}
