package executive

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
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
// TYPED-EVIDENCE-VISIBILITY-FIX-005 does not change this decision.
// R17-v5's task 12512 cited model-invocation:21-24 -- genuinely shown to it,
// embedded in an executive-evidence bundle the host itself attached, not
// fabricated -- and this correctly rejects them anyway: VISIBLE and CITABLE
// are different questions, and repository_evidence remains the only citable
// class regardless of what else was genuinely shown. What that incident
// exposed was a false DIAGNOSTIC, not a wrong decision -- see
// describeInadmissibleReferences.
//
// refs may repeat; the returned list of invalid references is deduplicated
// and sorted, matching describeInadmissibleReferences' shape for the same
// reason: a finding names what is wrong once, not once per occurrence.
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
		if !citationCovered(ref, genuine) {
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
// lineRange is one shown excerpt's [start, end] line span, 1-based and
// inclusive, matching Fragment's own convention.
type lineRange struct{ start, end int }

// citationLineRangePattern splits a citation into its file-level identity
// (everything before #L, i.e. repo@sha/path -- what a single shown excerpt
// and a citation naming a DIFFERENT sub-range of the same file share) and
// the specific [start,end] the citation names.
var citationLineRangePattern = regexp.MustCompile(`^(repository://[A-Za-z0-9._-]+@[0-9a-f]{40}/[^\s"'` + "`" + `,;)\]]+)#L(\d+)-L(\d+)$`)

func parseCitationRange(candidate string) (fileKey string, span lineRange, ok bool) {
	match := citationLineRangePattern.FindStringSubmatch(candidate)
	if match == nil {
		return "", lineRange{}, false
	}
	start, errStart := strconv.Atoi(match[2])
	end, errEnd := strconv.Atoi(match[3])
	if errStart != nil || errEnd != nil || start > end {
		return "", lineRange{}, false
	}
	return match[1], lineRange{start: start, end: end}, true
}

// citationCovered answers whether candidate's exact cited span is fully
// covered by the union of genuine spans shown for the same file -- not
// merely whether candidate matches one shown span exactly.
//
// The repository evidence provider shows a symbol as one or more excerpts,
// sometimes as overlapping sliding windows over the same region (e.g.
// worker.go#L38-L86 and worker.go#L76-L124 together covering L38-L124 with
// no gap). A model that read both windows and cited the natural merged
// range it actually saw -- worker.go#L38-L124 -- was citing something
// entirely real; rejecting it for not matching either individual window's
// exact string would reject a true, grounded claim as unverifiable. Found
// live: SELF-AUDIT-001's first real self-audit worker (2026-09-02) did
// exactly this and was rejected before this fix existed.
//
// A gap between shown spans still fails: candidate must be covered end to
// end, not merely overlap one shown span.
func citationCovered(candidate string, genuine map[string][]lineRange) bool {
	fileKey, span, ok := parseCitationRange(candidate)
	if !ok {
		return false
	}
	spans := genuine[fileKey]
	if len(spans) == 0 {
		return false
	}
	sorted := make([]lineRange, len(spans))
	copy(sorted, spans)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].start < sorted[j].start })
	coveredThrough := span.start - 1
	for _, shown := range sorted {
		if shown.start > coveredThrough+1 {
			break
		}
		if shown.end > coveredThrough {
			coveredThrough = shown.end
		}
		if coveredThrough >= span.end {
			return true
		}
	}
	return coveredThrough >= span.end
}

func genuineRepositoryCitations(ctx context.Context, sources SnapshotSourceReader, snapshotID int64, baseSHA string) (map[string][]lineRange, error) {
	if sources == nil || snapshotID <= 0 || baseSHA == "" {
		return map[string][]lineRange{}, nil
	}
	available, err := sources.SnapshotSources(ctx, snapshotID)
	if err != nil {
		return nil, err
	}
	genuine := map[string][]lineRange{}
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
		fileKey, span, ok := parseCitationRange(source.Reference)
		if !ok {
			continue
		}
		genuine[fileKey] = append(genuine[fileKey], span)
	}
	return genuine, nil
}

// verifyOfferedEvidenceProvenance is driveTypedTask's call-site wrapper: it
// turns an unverifiable voluntary reference into the same kind of contract
// rejection every other structural or requirement violation on this path
// produces, so it is recorded, retried, and eventually dead-lettered exactly
// like any other attempt failure -- never silently dropped, rewritten to
// empty, or waved through with the rest of the document.
//
// taskID and evidence identify the CURRENT task's own attached evidence --
// exactly the TaskRecord.Evidence already loaded on the task driveTypedTask
// is executing, threaded through so describeInadmissibleReferences can tell
// a reference that was genuinely shown (inside a bundle the host itself
// attached to this task) from one that never was, without a second read of
// anything: this is the same data driveTypedTask's caller already holds.
func (o *Orchestrator) verifyOfferedEvidenceProvenance(ctx context.Context, snapshotID int64, baseSHA string, taskID int64, evidence []EvidenceRecord, refs []string) error {
	invalid, err := o.VerifyEvidenceProvenance(ctx, o.snapshotSources, snapshotID, baseSHA, refs)
	if err != nil {
		return err
	}
	if len(invalid) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %s",
		ErrContractRejected, describeInadmissibleReferences(ctx, o.snapshotSources, snapshotID, taskID, evidence, invalid))
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

// nonRepositoryReviewEvidenceGuidance is nonRepositoryEvidenceGuidance's
// analogue for PurposeDepartmentReview, which never received any evidence
// guidance at all before TYPED-EVIDENCE-VISIBILITY-FIX-005 -- worker
// guidance is not reused here because department-review/v2 carries a flat
// evidence_refs []string with no structured evidence[] array to reference.
//
// R17-v5's task 12512 died citing model-invocation:21-24: real identifiers,
// genuinely shown to it inside an executive-evidence bundle the host itself
// attached before dispatch, summarizing the very deliverables the review was
// asked to judge -- accurate content, wrong admissibility, the same failure
// shape as R17-v4's task 11992, one purpose over. A review may read that
// bookkeeping and let it inform findings/verdict; it may not cite it as
// evidence_refs, because nothing about being real makes it repository
// evidence.
func nonRepositoryReviewEvidenceGuidance() string {
	return `Your snapshot may include executive/task bookkeeping -- an evidence bundle summarizing the deliverables under review, task and model-invocation identifiers, and similar material. Read it; let it inform your findings and verdict. None of it belongs in evidence_refs: it is not repository evidence, however real the underlying task or invocation is.
Only material sourced from repository evidence (a real repository:// citation actually shown to you) may appear in evidence_refs.
If no repository evidence was shown to you, evidence_refs: [] is a complete and correct answer -- it is not a failure to cite the bookkeeping instead.`
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
//
// TYPED-EVIDENCE-VISIBILITY-FIX-005: a reference can also be genuinely shown
// WITHOUT being a top-level segment Reference at all -- R17-v5's task 12512
// cited model-invocation:21-24, real identifiers embedded inside the
// executive-evidence bundle the host attached to that exact task and
// rendered into its task_context segment's own content. taskID/evidence let
// this recognize that case from the same structured TaskRecord.Evidence the
// caller already loaded -- never by re-scanning the segment's rendered
// Content, which would turn any incidental substring match into apparent
// authority (see embeddedExecutiveEvidenceRefs). Recognizing a reference this
// way changes only what this function SAYS about it: VerifyEvidenceProvenance
// still rejected it, and still would with evidence==nil.
func describeInadmissibleReferences(ctx context.Context, sources SnapshotSourceReader, snapshotID int64, taskID int64, evidence []EvidenceRecord, refs []string) string {
	known := map[string]string{}
	taskSegmentShown := false
	if sources != nil && snapshotID > 0 {
		if shown, err := sources.SnapshotSources(ctx, snapshotID); err == nil {
			ownReference := "task:" + strconv.FormatInt(taskID, 10)
			for _, source := range shown {
				if !source.Included {
					continue
				}
				if source.Reference == ownReference && source.Kind == "task_context" {
					taskSegmentShown = true
				}
				if source.Kind == "repository_evidence" {
					continue
				}
				known[source.Reference] = source.Kind
			}
		}
	}
	// Embedded refs only count as shown if the segment they ride inside of
	// (this task's own task_context) actually survived assembly. A bundle
	// attached to task_evidence but dropped from the snapshot for budget was
	// not read by the model any more than a dropped repository excerpt was.
	var embedded map[string]struct{}
	if taskSegmentShown {
		embedded = embeddedExecutiveEvidenceRefs(evidence)
	}
	parts := make([]string, 0, len(refs))
	for _, ref := range refs {
		if kind, ok := known[ref]; ok {
			parts = append(parts, fmt.Sprintf("evidence_refs names %s, which is %s, not citable repository evidence", ref, nonRepositoryEvidenceKindLabel(kind)))
			continue
		}
		if _, ok := embedded[ref]; ok {
			parts = append(parts, fmt.Sprintf("evidence_refs names %s, which was shown to this execution inside executive/task evidence context, not citable repository evidence", ref))
			continue
		}
		parts = append(parts, fmt.Sprintf("evidence_refs names %s, which the host cannot verify was shown to this execution", ref))
	}
	return strings.Join(parts, "; ")
}

// executiveEvidenceReferencePrefix mirrors the exact prefix
// contextprovider/provider.go's sourceRecord checks before it will copy an
// EvidenceRecord's Metadata into what the model actually sees -- for any
// other reference, the renderer sends metadata:{} regardless of what the DB
// row stores. A bundle-shaped Metadata attached to a differently-prefixed
// reference was therefore never rendered to the model; this classifier must
// not trust it either.
const executiveEvidenceReferencePrefix = "executive-evidence:"

// executiveEvidenceSchemaVersion mirrors the schema_version
// EvidenceTasks.recordBundle (runtimeadapter/evidence_tasks.go) actually
// writes. This guard grants no authority by itself -- it exists so this
// reader does not treat an arbitrary map that happens to have
// workers/reviews-shaped fields as the specific typed schema it claims to
// interpret.
const executiveEvidenceSchemaVersion = "executive-evidence.v1"

// embeddedExecutiveEvidenceRefs extracts the reference identifiers an
// executive-evidence bundle declares about the work it summarizes -- the
// model-invocation:<id> and task-evidence identifiers each projected
// worker/review already carries in its own typed fields
// (runtimeadapter/evidence_tasks.go's projectedWorker.TaskEvidence/
// EvidenceRefs and projectedReview's equivalents).
//
// A bundle only counts if its OWN Reference and schema_version are ones the
// real renderer would have preserved Metadata for (see
// executiveEvidenceReferencePrefix/executiveEvidenceSchemaVersion above) --
// otherwise the model was shown metadata:{}, not this bundle, regardless of
// what the DB row happens to store.
//
// This then walks the bundle's known schema (a top-level "workers" or
// "reviews" array, each entry carrying "evidence_refs"/"task_evidence_refs"
// string arrays) inside evidence[].Metadata["bundle"] -- decoded JSON
// already sitting in the row recordBundle wrote before this task was ever
// dispatched, read here as the map[string]any/[]any tree
// encoding/json.Unmarshal produced, never as re-parsed prose. A mention
// inside an unrelated field (a free-text summary, say) is not a
// reference-bearing field in this schema and is deliberately not collected:
// recognizing it would make an incidental substring match indistinguishable
// from a real, typed citation, which is exactly the authority-from-Content
// shortcut this fix does not take.
//
// evidence is scoped to the CURRENT task by construction: a bundle is only
// ever attached to the review/closure task it summarizes
// (EvidenceTasks.CreateTask), so every row in a TaskRecord's own Evidence
// already proves that bundle belongs to this task -- no separate lookup, no
// broader search.
func embeddedExecutiveEvidenceRefs(evidence []EvidenceRecord) map[string]struct{} {
	refs := map[string]struct{}{}
	for _, item := range evidence {
		if !strings.HasPrefix(item.Reference, executiveEvidenceReferencePrefix) {
			continue
		}
		bundle, ok := item.Metadata["bundle"].(map[string]any)
		if !ok {
			continue
		}
		if version, ok := bundle["schema_version"].(string); !ok || version != executiveEvidenceSchemaVersion {
			continue
		}
		for _, arrayKey := range [...]string{"workers", "reviews"} {
			entries, ok := bundle[arrayKey].([]any)
			if !ok {
				continue
			}
			for _, entry := range entries {
				object, ok := entry.(map[string]any)
				if !ok {
					continue
				}
				for _, fieldKey := range [...]string{"evidence_refs", "task_evidence_refs"} {
					collectStringRefs(object[fieldKey], refs)
				}
			}
		}
	}
	return refs
}

// collectStringRefs adds every string element of a decoded JSON array
// (value's runtime shape after encoding/json.Unmarshal into `any`) into into.
// Anything not a []any of strings -- a missing field, a differently-shaped
// value -- is silently skipped rather than guessed at: this is a narrow,
// typed reader of a known schema, not a general-purpose extractor.
func collectStringRefs(value any, into map[string]struct{}) {
	items, ok := value.([]any)
	if !ok {
		return
	}
	for _, item := range items {
		if s, ok := item.(string); ok && s != "" {
			into[s] = struct{}{}
		}
	}
}
