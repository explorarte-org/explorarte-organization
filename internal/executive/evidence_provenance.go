package executive

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// VerifyEvidenceProvenance answers the third question about a voluntarily
// offered evidence reference, distinct from the two the rest of this package
// already answers:
//
//	document structure  -- is evidence_refs a subset of evidence[].ref?
//	                        (refsAreAllStructured, worker_result.go)
//	requirement coverage -- did the worker satisfy what was REQUIRED?
//	                        (ValidateEvidenceStructure, evidence_structure.go)
//	provenance           -- does this reference actually name something the
//	                        host verified was shown to THIS invocation?
//	                        (here)
//
// It is deliberately unconditional on EvidenceRequirements. Requiredness
// decides what MUST be grounded; it says nothing about whether a reference a
// worker chose to offer beyond that is real. AUTONOMY-SMOKE-017-R17-V3's task
// 34 had zero requirements and offered "task:34" as evidence: structurally
// valid after fix/worker-result-v2-structural-contract, and never checked
// against anything real, because suppliedEvidence and
// ValidateEvidenceStructure both no-op when required is empty.
//
// Today a repository citation is the only namespace a model can supply as
// evidentiary content -- see repository_citation_verifier.go's doc comment on
// VerifyRepositoryCitations. A reference is admissible only if it is exactly
// one of the genuine, included repository_evidence sources this invocation's
// snapshot actually carried for baseSHA; anything else is inadmissible
// regardless of its shape. This is deliberately NOT a check on any one
// namespace's prefix: an invented repository:// URI and a bare "task:34"
// fail for the same reason, because neither traces to anything the host put
// in front of the model. A prefix blacklist would protect against this one
// incident's specific string and nothing else; this checks the property that
// actually matters.
//
// refs may repeat; the returned list of invalid references is deduplicated
// and sorted, matching describeUnverified's shape for the same reason: a
// finding names what is wrong once, not once per occurrence.
func (o *Orchestrator) VerifyEvidenceProvenance(ctx context.Context, sources SnapshotSourceReader, snapshotID int64, baseSHA string, refs []string) ([]string, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	genuine, err := genuineRepositoryCitations(ctx, sources, snapshotID, baseSHA)
	if err != nil {
		return nil, err
	}
	seen := map[string]struct{}{}
	var invalid []string
	for _, ref := range refs {
		if _, already := seen[ref]; already {
			continue
		}
		seen[ref] = struct{}{}
		if _, genuine := genuine[ref]; !genuine {
			invalid = append(invalid, ref)
		}
	}
	sort.Strings(invalid)
	return invalid, nil
}

// genuineRepositoryCitations is what VerifyRepositoryCitations and
// VerifyEvidenceProvenance both check candidates against: the set of
// repository_evidence sources this invocation's snapshot actually carried,
// for the commit the design is about, that survived assembly. Sharing this
// set is what keeps "was this shown to the model" a single answer instead of
// two mechanisms that could quietly drift.
func genuineRepositoryCitations(ctx context.Context, sources SnapshotSourceReader, snapshotID int64, baseSHA string) (map[string]struct{}, error) {
	if sources == nil || snapshotID <= 0 || baseSHA == "" {
		return map[string]struct{}{}, nil
	}
	available, err := sources.SnapshotSources(ctx, snapshotID)
	if err != nil {
		return nil, err
	}
	genuine := map[string]struct{}{}
	for _, source := range available {
		if source.Kind != "repository_evidence" {
			continue
		}
		if source.Version != baseSHA {
			continue
		}
		if !source.Included {
			continue
		}
		genuine[source.Reference] = struct{}{}
	}
	return genuine, nil
}

// verifyOfferedEvidenceProvenance is driveTypedTask's call-site wrapper: it
// turns an unverifiable voluntary reference into the same kind of contract
// rejection every other structural or requirement violation on this path
// produces, so it is recorded, retried, and eventually dead-lettered exactly
// like any other attempt failure -- never silently dropped, rewritten to
// empty, or waved through with the rest of the document.
func (o *Orchestrator) verifyOfferedEvidenceProvenance(ctx context.Context, snapshotID int64, baseSHA string, refs []string) error {
	invalid, err := o.VerifyEvidenceProvenance(ctx, o.snapshotSources, snapshotID, baseSHA, refs)
	if err != nil {
		return err
	}
	if len(invalid) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %s",
		ErrContractRejected, describeInadmissibleReferences(ctx, o.snapshotSources, snapshotID, invalid))
}

// nonRepositoryEvidenceGuidance states, to every department worker asked for
// worker-result/v2, that organizational context in its snapshot -- canonical
// or policy documents, role and department profiles, and similar material --
// is guidance to follow, not evidence to cite. Only repository_evidence (a
// real repository:// citation actually shown to this execution) may appear
// in evidence_refs/evidence[]; VerifyEvidenceProvenance already enforces
// exactly this, unconditionally, whether or not any EvidenceRequirement
// exists.
//
// Before this guidance, a worker was measured against that boundary without
// ever being told where it runs. R17-v4's task 11992 cited real
// canonical/policy documents across all three attempts -- accurate about
// their content, wrong about their admissibility -- because nothing said
// organizational context and repository evidence are different things. This
// is the same repair shape as workerResultV2StructureGuidance (worker_result.go):
// that states a v2 document's STRUCTURE; this states which CONTENT is
// admissible within it.
func nonRepositoryEvidenceGuidance() string {
	return `Organizational context in your snapshot -- canonical or policy documents, role and department profiles, and similar material -- is guidance for you to follow, not evidence to cite: none of it belongs in evidence_refs or evidence[].
Only material sourced from repository evidence (a real repository:// citation actually shown to you) may appear in evidence_refs/evidence[].
If no repository evidence was shown to you, evidence_refs: [] and evidence: [] is a complete and correct answer -- it is not a failure to cite the organizational context instead.`
}

// nonRepositoryEvidenceKindLabel turns a SnapshotSource.Kind other than
// "repository_evidence" into the phrase used in rejection feedback, so a
// worker learns not just that a reference failed but what class of context
// it actually was -- naming the mistake instead of merely repeating the
// rule it already broke.
func nonRepositoryEvidenceKindLabel(kind string) string {
	switch kind {
	case "canonical_document":
		return "canonical/policy context"
	case "role_profile":
		return "role-profile context"
	case "organization_agent", "department_agent":
		return "organizational-agent context"
	case "task_context":
		return "task context"
	case "owner_constraint":
		return "an owner constraint"
	default:
		return kind + " context"
	}
}

// describeInadmissibleReferences turns each reference VerifyEvidenceProvenance
// rejected into feedback naming why. A reference that traces to a real,
// included, non-repository_evidence source in THIS invocation's own snapshot
// is named for what it actually is; R17-v4's task 11992 answered a bare
// "cannot verify was shown" by citing MORE, not less, because that message
// never told it the reference was real, only that it was inadmissible --
// which reads exactly like "try a different citation" rather than "stop
// citing this class of thing at all". A reference that traces to nothing in
// context keeps the original, honest wording: this must never claim to know
// what something is when it does not.
//
// This reads the snapshot a second time rather than threading Verify's
// internal genuine-set outward, so VerifyEvidenceProvenance's own decision
// logic -- and its signature -- is untouched by this fix.
func describeInadmissibleReferences(ctx context.Context, sources SnapshotSourceReader, snapshotID int64, refs []string) string {
	known := map[string]string{}
	if sources != nil && snapshotID > 0 {
		if shown, err := sources.SnapshotSources(ctx, snapshotID); err == nil {
			for _, source := range shown {
				if !source.Included || source.Kind == "repository_evidence" {
					continue
				}
				known[source.Reference] = source.Kind
			}
		}
	}
	parts := make([]string, 0, len(refs))
	for _, ref := range refs {
		if kind, ok := known[ref]; ok {
			parts = append(parts, fmt.Sprintf("evidence_refs names %s, which is %s, not citable repository evidence", ref, nonRepositoryEvidenceKindLabel(kind)))
			continue
		}
		parts = append(parts, fmt.Sprintf("evidence_refs names %s, which the host cannot verify was shown to this execution", ref))
	}
	return strings.Join(parts, "; ")
}
