package executive

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/missionplan"
	"github.com/Mireuz13/explorarte-organization/internal/repositoryevidence"
)

// An implementation plan carries the patch a mission will apply. The patch is
// written by a model, and a unified diff has a fixed grammar and must apply to an
// exact tree; the commonest ways a model gets it wrong are mechanical (placeholder
// hunk coordinates, miscounted hunks, context that is not in the file).
//
// That is a fault in the ARTIFACT the planner produced, not in its execution. It is
// therefore judged where the artifact is produced -- between the implementation
// planner and the creation of the mission -- and answered with feedback the planner
// can act on, inside the planner's own retry budget. A patch that reached the
// code-runner unchecked failed there, deterministically, once per attempt: root 1007
// (2026-09-21) spent five code-runner attempts on `@@ -X,X +X,X @@`.
//
//	implementation planner -> candidate patch -> HOST PATCH VALIDATION
//	    valid   -> mission is created
//	    invalid -> PATCH_VALIDATION_FAILED, back to the planner (bounded)
//
// The validation is host-owned and deterministic: structure and paths from the
// diff text (missionplan.CheckPatchStructure), then `git apply --check` against an
// isolated copy of the tree at the design-freeze commit (PatchWorkbench).

// PatchWorkbench is the host's window onto the FROZEN repository during the
// implementation-plan phase. The Executive never shells out; this port is what lets
// it ask git two questions about one exact commit without touching any checkout.
type PatchWorkbench interface {
	// CheckPatch reports whether patch applies to the tree of baseSHA. A patch git
	// rejects is Applies=false with git's diagnostic; an error means the check could
	// not run, which says nothing about the patch.
	CheckPatch(ctx context.Context, baseSHA, patch string) (PatchCheckResult, error)
	// ReadFile returns the exact content of path at baseSHA, bounded by limit.
	ReadFile(ctx context.Context, baseSHA, path string, limit int64) ([]byte, error)
}

// PatchCheckResult is git's verdict on one patch.
type PatchCheckResult struct {
	Applies bool
	Detail  string
}

// WithPatchWorkbench enables host patch validation and the exact-source contract
// for the implementation planner. Without it only the structural checks run.
func WithPatchWorkbench(workbench PatchWorkbench) OrchestratorOption {
	return func(o *Orchestrator) { o.patchWorkbench = workbench }
}

// ReasonImplementationPlanPatchInvalid blocks a root whose implementation planner
// could not produce an applicable patch within its attempts. It is distinct from a
// failed EXECUTION: nothing reached the code-runner.
const ReasonImplementationPlanPatchInvalid = "implementation_plan_patch_invalid"

// PatchValidationFailureReference prefixes the durable evidence recorded for every
// rejected patch: patch-validation-failure://<plan task>/<invocation>. Counting
// them, next to the plan task's attempts and the mission's code-runner attempts,
// separates what it costs to THINK an applicable patch from what it costs to
// execute and verify one.
const PatchValidationFailureReference = "patch-validation-failure://"

// PatchValidationError is the feedback a planner receives when its patch is
// rejected. Its text starts with PATCH_VALIDATION_FAILED so it survives, verbatim,
// into the next attempt's task context.
type PatchValidationError struct {
	BaseSHA string
	// Findings are one line per rejected change, bounded.
	Findings []string
	// Checks names the rules that failed, for evidence and metrics.
	Checks []string
}

func (e *PatchValidationError) Error() string {
	var b strings.Builder
	b.WriteString("PATCH_VALIDATION_FAILED\nbase_sha: " + e.BaseSHA + "\n")
	for _, finding := range e.Findings {
		b.WriteString(finding + "\n")
	}
	b.WriteString("Regenerate the patch against the supplied source. Do not use placeholder hunk coordinates.")
	return b.String()
}

// Unwrap makes a rejected patch an ordinary contract rejection: the attempt fails,
// is retried within the task's attempts, and is recorded like any other.
func (e *PatchValidationError) Unwrap() error { return ErrContractRejected }

const (
	maxPatchFindingBytes = 700
	maxPatchFindings     = 4
)

// validateImplementationPlanPatches judges every planned change's patch against
// the design-freeze commit. It returns nil, a *PatchValidationError (the artifact is
// wrong), or an error wrapping ErrEvidenceSensorUnavailable (the check could not
// run, which is nobody's fault but the sensor's).
func (o *Orchestrator) validateImplementationPlanPatches(ctx context.Context, baseSHA string, plan ImplementationPlan) error {
	failure := &PatchValidationError{BaseSHA: baseSHA}
	for index, change := range plan.Changes {
		if len(failure.Findings) >= maxPatchFindings {
			break
		}
		label := fmt.Sprintf("change[%d] path=%s", index, change.Path)
		if problem := missionplan.CheckPatchStructure(missionplan.Change{Path: change.Path, Intent: change.Intent, Patch: change.Patch}); problem != nil {
			failure.Findings = append(failure.Findings, truncate(fmt.Sprintf("%s check=%s: %s", label, problem.Check, problem.Detail), maxPatchFindingBytes))
			failure.Checks = append(failure.Checks, problem.Check)
			continue
		}
		if o.patchWorkbench == nil {
			continue
		}
		verdict, err := o.patchWorkbench.CheckPatch(ctx, baseSHA, change.Patch)
		if err != nil {
			return fmt.Errorf("%w: git apply --check could not run against %s: %v", ErrEvidenceSensorUnavailable, baseSHA, err)
		}
		if !verdict.Applies {
			failure.Findings = append(failure.Findings, truncate(fmt.Sprintf("%s check=git_apply_check: %s", label, verdict.Detail), maxPatchFindingBytes))
			failure.Checks = append(failure.Checks, "git_apply_check")
		}
	}
	if len(failure.Findings) == 0 {
		return nil
	}
	return failure
}

// validateImplementationPlanForMission is the plan task's validate callback: the
// contract parse, then the host's patch validation, recording each rejection as
// durable evidence so the cost of a non-applicable patch is measurable.
func (o *Orchestrator) validateImplementationPlanForMission(ctx context.Context, root, planTask TaskRecord, missionRequirement RequirementRecord, result InvocationResult) error {
	plan, err := ParseImplementationPlan(result.JSONOutput, o.limits)
	if err != nil {
		return err
	}
	baseSHA, err := o.frozenDesignBaseSHA(ctx, root)
	if err != nil {
		return err
	}
	err = o.validateImplementationPlanPatches(ctx, baseSHA, plan)
	var rejected *PatchValidationError
	if errors.As(err, &rejected) {
		digest := sha256.Sum256([]byte(rejected.Error()))
		// Recording is best effort: it must never turn a precise rejection the
		// planner can act on into a different error.
		_ = o.tasks.RecordEvidence(ctx, EvidenceCommand{
			TaskID: root.ID, RequirementID: missionRequirement.ID, Type: "result",
			Reference:  fmt.Sprintf("%s%d/%d", PatchValidationFailureReference, planTask.ID, result.InvocationID),
			Digest:     hex.EncodeToString(digest[:]),
			RecordedBy: orchestratorWorkerID,
			Metadata: map[string]any{
				"plan_task_id": planTask.ID, "invocation_id": result.InvocationID, "base_sha": baseSHA,
				"checks": rejected.Checks, "detail": truncate(strings.Join(rejected.Findings, " | "), 500),
			},
			Satisfies: false,
		})
	}
	return err
}

// patchValidationFailures counts the rejected patches recorded for one plan task.
func patchValidationFailures(evidence []EvidenceRecord, planTaskID int64) int {
	prefix := fmt.Sprintf("%s%d/", PatchValidationFailureReference, planTaskID)
	count := 0
	for _, record := range evidence {
		if strings.HasPrefix(record.Reference, prefix) {
			count++
		}
	}
	return count
}

// The exact source the planner patches against. A model cannot compute the line
// numbers of a file it never saw in full; given the file, with its numbers and its
// commit, the two mistakes root 1007 made (placeholder hunk coordinates, and a table
// layout that did not match the code) have nothing left to stand on.
const (
	maxSourceFiles        = 3
	maxSourceFileBytes    = 48 << 10
	maxSourceFileLines    = 600
	maxSourceSectionBytes = 64 << 10
)

// implementationSourceContract renders the current content of the files the
// campaign names -- restricted to what the mission's scope could ever change -- as
// numbered lines at the design-freeze commit. It returns "" when there is nothing to
// show or no workbench.
func (o *Orchestrator) implementationSourceContract(ctx context.Context, root TaskRecord, baseSHA, query string) string {
	if o.patchWorkbench == nil || baseSHA == "" {
		return ""
	}
	scope := missionScope(root)
	var files []string
	seen := map[string]struct{}{}
	for _, candidate := range repositoryevidence.SelectionFromText(query, 24).Paths {
		clean := path.Clean(candidate)
		if _, dup := seen[clean]; dup || !strings.Contains(path.Base(clean), ".") || !missionplan.PathPermitted(scope, clean) {
			continue
		}
		seen[clean] = struct{}{}
		files = append(files, clean)
	}
	var b strings.Builder
	shown := 0
	for _, file := range files {
		if shown >= maxSourceFiles || b.Len() >= maxSourceSectionBytes {
			break
		}
		content, err := o.patchWorkbench.ReadFile(ctx, baseSHA, file, maxSourceFileBytes)
		if err != nil {
			// A named path that does not exist at the commit (a file the change
			// would create) or is too large is simply not shown.
			continue
		}
		lines := strings.Split(strings.TrimSuffix(string(content), "\n"), "\n")
		if len(lines) > maxSourceFileLines {
			continue
		}
		if shown == 0 {
			b.WriteString(sourceContractPreamble(baseSHA))
		}
		fmt.Fprintf(&b, "\nFILE %s @ %s (%d lines):\n", file, baseSHA, len(lines))
		for number, line := range lines {
			fmt.Fprintf(&b, "%5d | %s\n", number+1, line)
		}
		shown++
	}
	return strings.TrimRight(b.String(), "\n")
}

func sourceContractPreamble(baseSHA string) string {
	return "SOURCE FOR PATCHING (read by the host at commit " + baseSHA + ").\n" +
		"A patch you write is applied with `git apply` to EXACTLY this content, and the host checks that it applies before any work is done. " +
		"Hunk headers must carry the real line numbers and counts of these files; every context and removed line must match them character for character, tabs included; " +
		"and the layout of the code you add must follow the code that is here. " +
		"The number and the `|` at the start of each line below are display only and are not part of the file. " +
		"Never use placeholder coordinates such as @@ -X,X +X,X @@."
}
