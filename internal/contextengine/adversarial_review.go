package contextengine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/designreview"
	"github.com/Mireuz13/explorarte-organization/internal/organization/registry"
)

// Adversarial review runs against a provider that is deliberately not allowed
// to receive organizational data. The ordinary source resolution gives every
// role the full canonical bundle -- role catalog, capability matrix, model
// routing, owner decisions, the organization and department AGENTs -- all
// classified organizational. A standard assembly therefore contaminates the
// reviewer's context by construction, however sanitized its task input is, and
// the egress gate refuses the call after the design, the department work and
// the review have all been paid for.
//
// The fix is not a second Context Engine and not a filter applied afterwards.
// It is an admission boundary: for this one purpose, the source set is
// enumerated rather than assembled, and everything else is simply never
// resolved. The resulting Snapshot is an ordinary durable Snapshot and travels
// the same ContextCompiler, View, Harness and egress path as every other one.
//
// Two rules govern everything below, and they are the whole point of this file:
//
//   - the mode engages on the COMPLETE durable selector, never on a subset,
//     so no semantically incorrect combination can reach it;
//   - DataSanitized is applied only to bytes this package BUILT from a closed
//     field list. Relabelling raw organizational bytes as sanitized would be
//     the very contamination this exists to prevent, dressed up as a fix.
const (
	// AdversarialReviewPurpose is the legacy purpose string the Executive
	// sends and modelegress keys on. Kept byte-identical on purpose.
	AdversarialReviewPurpose = "adversarial_review"
	// AdversarialReviewTaskClass is the durable task class the Executive
	// stamps on the review task it creates.
	AdversarialReviewTaskClass = "coordination.adversarial_review"
	// AdversarialReviewExecutionPurpose is the durable execution purpose.
	// It is hyphenated where the legacy Purpose is underscored; both spellings
	// are load-bearing and neither may be normalized into the other.
	AdversarialReviewExecutionPurpose = "adversarial-review"
	// AdversarialReviewerRoleID is the only role this mode may serve.
	AdversarialReviewerRoleID = "investigacion/revisor_adversarial"
	// AdversarialReviewerUnitID is the transversal audit unit the reviewer
	// must belong to for the review to be independent.
	AdversarialReviewerUnitID = "investigacion"
)

// adversarialSelectorMarkers is every request field whose adversarial value
// alone is enough to mean the caller intended this mode. Presence of ANY of
// them commits the request to the strict validation below, so a request that
// carries some of the selector but not all of it is refused rather than
// quietly assembled the ordinary way.
func adversarialReviewRequested(request BuildRequest) bool {
	return strings.TrimSpace(request.Purpose) == AdversarialReviewPurpose ||
		strings.TrimSpace(request.TaskClass) == AdversarialReviewTaskClass ||
		strings.TrimSpace(request.ExecutionPurpose) == AdversarialReviewExecutionPurpose ||
		strings.TrimSpace(request.ActorRoleID) == AdversarialReviewerRoleID
}

// validateAdversarialSelector requires the entire durable selector, exactly.
//
// ValidateBuildRequest treats TaskClass, ExecutionPurpose and ActorUnitID as
// optional carried metadata and does not prove they correspond to the real
// role or task. That is fine for contextcompiler, which only reads them, but
// it is not fine here, where they decide whether a restricted source set is
// admissible. So this mode does not accept a partial match: every one of the
// five facts must be present and exact, or the build is refused. A request
// missing any of them is a request whose intent this package cannot verify.
func validateAdversarialSelector(request BuildRequest) error {
	actor := strings.TrimSpace(request.ActorRoleID)
	for _, field := range []struct{ name, got, want string }{
		{"purpose", strings.TrimSpace(request.Purpose), AdversarialReviewPurpose},
		{"task_class", strings.TrimSpace(request.TaskClass), AdversarialReviewTaskClass},
		{"execution_purpose", strings.TrimSpace(request.ExecutionPurpose), AdversarialReviewExecutionPurpose},
		{"actor_role_id", actor, AdversarialReviewerRoleID},
		{"actor_unit_id", strings.TrimSpace(request.ActorUnitID), AdversarialReviewerUnitID},
	} {
		if field.got != field.want {
			return Reject(ReasonRoleNotExecutable, actor, fmt.Sprintf(
				"adversarial review context requires the complete durable selector: %s is %q, want %q",
				field.name, field.got, field.want))
		}
	}
	return nil
}

// renderedTaskFacts is the subset of the Tasks provider payload this package
// reads to corroborate the request against durable state. It is a read of
// facts, NOT an admission of the payload: nothing decoded here is forwarded to
// the provider.
type renderedTaskFacts struct {
	AssignedRoleID string `json:"assigned_role_id"`
	AssignedUnitID string `json:"assigned_unit_id"`
	Instructions   string `json:"instructions"`
}

// resolveAdversarialSources enumerates the entire admissible source set.
//
// Two things, plus explicitly authorized evidence:
//
//   - the reviewer's own operating contract, scanned before it is classified;
//   - the sanitized review bundle, rebuilt from designreview's closed field
//     list rather than lifted out of the task record that carried it.
//
// No canonical bundle, no owner decisions, no organization or department
// AGENT, no memory, no skills, no project context. Those are not filtered out
// later; they are never loaded.
func (s *contextService) resolveAdversarialSources(ctx context.Context, request BuildRequest, role registry.Role) ([]SourceRecord, error) {
	if role.UnitID != AdversarialReviewerUnitID {
		return nil, Reject(ReasonRoleNotExecutable, role.ID, fmt.Sprintf(
			"the adversarial reviewer must belong to unit %q, registry says %q",
			AdversarialReviewerUnitID, role.UnitID))
	}
	profile, err := s.adversarialReviewerContract(ctx, role)
	if err != nil {
		return nil, err
	}
	bundle, err := s.adversarialReviewBundle(ctx, request, role)
	if err != nil {
		return nil, err
	}
	sources := []SourceRecord{profile, bundle}

	evidence, err := s.rag.ListApprovedEvidence(ctx, request)
	if err != nil && !errors.Is(err, ErrSourceProviderUnavailable) {
		return nil, fmt.Errorf("resolve authorized evidence: %w", err)
	}
	for _, source := range evidence {
		// Evidence keeps whatever class its provider assigned. Anything the
		// reviewer may not receive is refused below rather than downgraded,
		// because reclassifying a source to make it fit is exactly how a
		// boundary stops meaning anything.
		sources = append(sources, normalizeSource(source, TierRAGEvidence, SourceRAGEvidence, InstructionData, TrustUntrusted, false))
	}

	if err = assertAdversarialEgressSafe(sources); err != nil {
		return nil, err
	}
	return sources, nil
}

// adversarialReviewerContract loads the reviewer's profile and classifies it
// sanitized HERE and only here.
//
// That classification is a deliberate decision about THESE bytes, not about
// whatever happens to sit behind ProfilePath. A role profile is the agent's
// own operating contract; this one describes how to review a design and
// carries no organizational data about the company, which is what the
// organizational class exists to protect. The blanket organizational default
// applied to every other profile in the ordinary path stays exactly as it is.
//
// The bytes are validated as the reviewer's own profile and scanned for
// credential material before the class is assigned, so "sanitized" is a
// statement about content that was checked rather than a label applied on
// trust. The scan is not a proof; the narrow path to this function is what
// keeps arbitrary documents from reaching it.
func (s *contextService) adversarialReviewerContract(ctx context.Context, role registry.Role) (SourceRecord, error) {
	if role.ProfilePath == nil || strings.TrimSpace(*role.ProfilePath) == "" {
		return SourceRecord{}, Reject(ReasonSourceNotFound, role.ID, "role profile path is missing")
	}
	profile, err := s.documents.Load(ctx, *role.ProfilePath, int64(s.config.MaxSegmentBytes))
	if err != nil {
		return SourceRecord{}, err
	}
	memoryDomain := ""
	if role.MemoryDomain != nil {
		memoryDomain = *role.MemoryDomain
	}
	if err = ValidateProfile(profile, role.UnitID, role.RoleSlug, memoryDomain); err != nil {
		return SourceRecord{}, err
	}
	if err = designreview.AssertNoCredentialMaterial("reviewer contract", profile.Normalized); err != nil {
		return SourceRecord{}, Reject(ReasonSourceNotFound, *role.ProfilePath, err.Error())
	}
	return documentRecord(profile, TierRoleProfile, SourceRoleProfile, InstructionRole, TrustAuthoritative, DataSanitized), nil
}

// adversarialReviewBundle turns the durable task into an egress-safe source by
// REBUILDING it, never by relabelling it.
//
// The Tasks provider renders a task as an organizational payload carrying the
// id, title, instructions, acceptance criteria, status, requirements, evidence
// and attempts. Marking that blob sanitized would sanitize nothing: it would
// take arbitrary organizational bytes and grant them passage on the strength
// of a changed field. So none of that payload is forwarded. It is read for
// facts, the review bundle is recovered from the instructions through
// designreview's closed field list, and the bytes that become the source are
// the ones designreview re-encodes from that list.
//
// This is also what actually binds the mode to the real review task. The
// request's TaskClass cannot be corroborated -- the rendered payload does not
// carry it -- so it is not the thing being trusted here. An ordinary task
// assigned to the reviewer has no decodable bundle in its instructions and is
// refused on that ground, which is the property the request metadata alone
// could never give us.
func (s *contextService) adversarialReviewBundle(ctx context.Context, request BuildRequest, role registry.Role) (SourceRecord, error) {
	task, err := s.tasks.GetTaskContext(ctx, request)
	if err != nil {
		return SourceRecord{}, fmt.Errorf("resolve task context: %w", err)
	}
	if task == nil {
		return SourceRecord{}, Reject(ReasonSourceNotFound, request.TaskRef,
			"adversarial review requires the sanitized review bundle carried by its task")
	}
	var facts renderedTaskFacts
	if err = json.Unmarshal(task.Content, &facts); err != nil {
		return SourceRecord{}, Reject(ReasonSourceNotFound, task.Reference,
			fmt.Sprintf("task context is not a readable task payload: %v", err))
	}
	if facts.AssignedRoleID != AdversarialReviewerRoleID || facts.AssignedUnitID != role.UnitID {
		return SourceRecord{}, Reject(ReasonRoleNotExecutable, task.Reference, fmt.Sprintf(
			"durable task is assigned to %q in unit %q, not to the adversarial reviewer",
			facts.AssignedRoleID, facts.AssignedUnitID))
	}
	raw, err := reviewBundlePayload(facts.Instructions)
	if err != nil {
		return SourceRecord{}, Reject(ReasonSourceNotFound, task.Reference, err.Error())
	}
	_, encoded, err := designreview.DecodeBundle(raw)
	if err != nil {
		return SourceRecord{}, Reject(ReasonSourceNotFound, task.Reference, err.Error())
	}
	// Built here, from the closed field list, with its own hash. Nothing from
	// the rendered task record survives into Content.
	record := SourceRecord{
		Kind:      SourceTaskContext,
		Reference: task.Reference,
		Version:   task.Version,
		DataClass: DataSanitized,
		Content:   encoded,
		Relevance: 1,
	}
	return normalizeSource(record, TierTask, SourceTaskContext, InstructionScoped, TrustScoped, false), nil
}

// reviewBundlePayload isolates the JSON document the Executive appends to the
// review task's instructions after its prose preamble. Anything that is not a
// single trailing JSON object is not a review task.
func reviewBundlePayload(instructions string) ([]byte, error) {
	start := strings.Index(instructions, "{")
	if start < 0 {
		return nil, errors.New("task instructions carry no review bundle")
	}
	return []byte(instructions[start:]), nil
}

// assertAdversarialEgressSafe refuses to BUILD a snapshot carrying anything
// the reviewer's provider may not receive.
//
// It is the second defence, not the mechanism: by the time it runs, every
// source was either built from a closed field list or checked at its own
// admission. Refusing at build time rather than omitting the offending segment
// is the point. An omission would produce a snapshot that looks complete,
// passes egress, and quietly reviewed less than it claimed to. A refusal says
// which source was inadmissible and stops.
func assertAdversarialEgressSafe(sources []SourceRecord) error {
	for _, source := range sources {
		switch source.DataClass {
		case DataPublic, DataSanitized:
			continue
		default:
			return Reject(ReasonSourceNotFound, string(source.Kind),
				fmt.Sprintf("adversarial review context cannot carry %q data (source %s, reference %q)",
					source.DataClass, source.Kind, source.Reference))
		}
	}
	return nil
}
