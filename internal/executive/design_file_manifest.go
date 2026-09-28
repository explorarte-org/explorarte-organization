package executive

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
)

// The files a frozen design may change are fixed when it freezes, and the mission may change no other.
//
// External audit A3 (2026-09-27): the design froze with an identity and a digest, but the mission's
// allowed paths were derived from the implementation plan -- a model's output written AFTER the
// freeze -- and nothing compared them to what was approved. The general scope and the kernel denylist
// held; the particular approved scope did not. A design now declares the files it proposes to change
// (proposed_files); the freeze records their union as an immutable manifest; the implementation plan
// may name only manifest files, and the sealed candidate's changed paths are compared to it again
// before its execution is accepted.

// DesignFileManifestKey is the freeze evidence metadata key the manifest is recorded under.
const DesignFileManifestKey = "design_file_manifest"

// MaxProposedFiles bounds one deliverable's proposed_files.
const MaxProposedFiles = 16

func validProposedFile(file string) bool {
	clean := path.Clean(file)
	return file != "" && clean == file && !path.IsAbs(file) && !strings.HasPrefix(file, "../") && file != "." && !strings.Contains(file, "..")
}

// designFileManifest is the sorted union of the proposed_files of the artifact's deliverables.
func (o *Orchestrator) designFileManifest(ctx context.Context, artifact designArtifact) ([]string, error) {
	seen := map[string]bool{}
	for _, unit := range artifact.Units {
		task, err := o.tasks.GetTask(ctx, unit.TaskID)
		if err != nil {
			return nil, err
		}
		result, ok := o.resultForCompletedTask(ctx, task)
		if !ok {
			continue
		}
		parsed, err := ParseWorkerResult(result.JSONOutput, o.limits)
		if err != nil {
			continue
		}
		for _, file := range parsed.ProposedFiles {
			seen[file] = true
		}
	}
	manifest := make([]string, 0, len(seen))
	for file := range seen {
		manifest = append(manifest, file)
	}
	sort.Strings(manifest)
	return manifest, nil
}

// frozenDesignManifest reads the manifest recorded with the root's freeze. found is false for a
// freeze recorded before manifests existed.
func (o *Orchestrator) frozenDesignManifest(ctx context.Context, rootID int64) (manifest []string, found bool, err error) {
	root, err := o.tasks.GetTask(ctx, rootID)
	if err != nil {
		return nil, false, err
	}
	for _, evidence := range root.Evidence {
		raw, present := evidence.Metadata[DesignFileManifestKey]
		if !present {
			continue
		}
		encoded, marshalErr := json.Marshal(raw)
		if marshalErr != nil {
			return nil, true, marshalErr
		}
		if unmarshalErr := json.Unmarshal(encoded, &manifest); unmarshalErr != nil {
			return nil, true, unmarshalErr
		}
		return manifest, true, nil
	}
	return nil, false, nil
}

// frozenDesignImplementationContract projects the immutable design decision into the
// implementation planner. It reconstructs the body from the frozen round's durable deliverables
// and verifies the artifact digest before exposing it, so an unbound metadata copy can never
// substitute different work after adjudication.
func (o *Orchestrator) frozenDesignImplementationContract(ctx context.Context, root TaskRecord, all []TaskRecord) (string, error) {
	detail, err := o.tasks.GetTask(ctx, root.ID)
	if err != nil {
		return "", err
	}
	var freeze EvidenceRecord
	for _, evidence := range detail.Evidence {
		if _, present := evidence.Metadata[DesignFileManifestKey]; present {
			freeze = evidence
			break
		}
	}
	if freeze.Reference == "" {
		return "", fmt.Errorf("%w: the frozen design evidence is missing", ErrContractRejected)
	}
	manifest, recorded, err := o.frozenDesignManifest(ctx, root.ID)
	if err != nil {
		return "", err
	}
	if !recorded {
		return "", fmt.Errorf("%w: the frozen design manifest is missing", ErrContractRejected)
	}
	version, _ := freeze.Metadata["design_version"].(string)
	round, parseErr := strconv.Atoi(strings.TrimPrefix(version, "v"))
	if parseErr != nil || round < 1 {
		return "", fmt.Errorf("%w: frozen design version %q cannot identify its round", ErrContractRejected, version)
	}
	artifact, _, ok, artifactErr := o.candidateDesign(ctx, root, all, round)
	if artifactErr != nil {
		return "", artifactErr
	}
	if !ok {
		return "", fmt.Errorf("%w: frozen design %s has no durable candidate", ErrContractRejected, version)
	}
	digest, _ := freeze.Metadata["design_digest"].(string)
	if digest == "" || artifactDigest(artifact) != digest {
		return "", fmt.Errorf("%w: reconstructed candidate does not match frozen design digest", ErrContractRejected)
	}
	candidate, err := o.candidateBody(ctx, artifact)
	if err != nil {
		return "", err
	}
	identity := map[string]any{
		"reference":           freeze.Reference,
		"design_id":           freeze.Metadata["design_id"],
		"design_version":      freeze.Metadata["design_version"],
		"design_digest":       freeze.Metadata["design_digest"],
		"design_base_sha":     freeze.Metadata["design_base_sha"],
		DesignFileManifestKey: manifest,
	}
	encoded, err := json.Marshal(identity)
	if err != nil {
		return "", err
	}
	return "FROZEN DESIGN (host-owned; implement exactly this decision, never substitute another defect):\n" +
		string(encoded) + "\n\nFROZEN CANDIDATE:\n" + candidate + "\n\n" +
		"Every changed file must appear in design_file_manifest. Produce patches only for this candidate and these files.", nil
}

// outsideManifest returns the paths that the manifest does not list.
func outsideManifest(paths, manifest []string) []string {
	allowed := map[string]bool{}
	for _, file := range manifest {
		allowed[file] = true
	}
	var outside []string
	for _, file := range paths {
		if !allowed[file] {
			outside = append(outside, file)
		}
	}
	return outside
}

// checkPlanAgainstManifest refuses an implementation plan naming a file the frozen design does not.
func checkPlanAgainstManifest(planPaths, manifest []string) error {
	if len(manifest) == 0 {
		return fmt.Errorf("%w: the frozen design names no file to change (proposed_files), so no implementation can be bound to it", ErrContractRejected)
	}
	if outside := outsideManifest(planPaths, manifest); len(outside) > 0 {
		return fmt.Errorf("%w: the implementation plan changes %s, which the frozen design does not name; it may change only %s",
			ErrContractRejected, strings.Join(outside, ", "), strings.Join(manifest, ", "))
	}
	return nil
}
