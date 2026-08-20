package contextengine

import (
	"context"
	"errors"
	"fmt"
	"strings"

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
const (
	// AdversarialReviewPurpose is the legacy purpose string the Executive
	// sends and modelegress keys on. Kept byte-identical on purpose.
	AdversarialReviewPurpose = "adversarial_review"
	// AdversarialReviewerRoleID is the only role this mode may serve.
	AdversarialReviewerRoleID = "investigacion/revisor_adversarial"
)

// adversarialReviewRequested reports whether either half of the adversarial
// pair is present, so a mismatched half can be refused rather than silently
// falling back to the ordinary assembly.
func adversarialReviewRequested(request BuildRequest) bool {
	return strings.TrimSpace(request.Purpose) == AdversarialReviewPurpose ||
		strings.TrimSpace(request.ActorRoleID) == AdversarialReviewerRoleID
}

// validateAdversarialPairing fails closed on any combination that is not
// exactly the reviewer running an adversarial review.
//
// Both directions matter. The purpose without the role would hand a restricted
// context to somebody else; the role without the purpose would hand the
// reviewer an ordinary organizational context, which is the contamination this
// exists to prevent. Neither may quietly degrade into the normal path.
func validateAdversarialPairing(request BuildRequest) error {
	purpose := strings.TrimSpace(request.Purpose)
	actor := strings.TrimSpace(request.ActorRoleID)
	if purpose == AdversarialReviewPurpose && actor != AdversarialReviewerRoleID {
		return Reject(ReasonRoleNotExecutable, actor,
			"adversarial review context may only be built for "+AdversarialReviewerRoleID)
	}
	if actor == AdversarialReviewerRoleID && purpose != AdversarialReviewPurpose {
		return Reject(ReasonRoleNotExecutable, actor,
			"the adversarial reviewer may only build context under purpose "+AdversarialReviewPurpose)
	}
	return nil
}

// resolveAdversarialSources enumerates the entire admissible source set.
//
// Three things and nothing else:
//
//   - the reviewer's own profile, which is its operating contract;
//   - the task context, which carries the sanitized review bundle the
//     Executive already embedded;
//   - authorized evidence the task itself references.
//
// No canonical bundle, no owner decisions, no organization or department
// AGENT, no memory, no skills, no project context. Those are not filtered out
// later; they are never loaded.
func (s *contextService) resolveAdversarialSources(ctx context.Context, request BuildRequest, role registry.Role) ([]SourceRecord, error) {
	if role.ProfilePath == nil || strings.TrimSpace(*role.ProfilePath) == "" {
		return nil, Reject(ReasonSourceNotFound, role.ID, "role profile path is missing")
	}
	profile, err := s.documents.Load(ctx, *role.ProfilePath, int64(s.config.MaxSegmentBytes))
	if err != nil {
		return nil, err
	}
	memoryDomain := ""
	if role.MemoryDomain != nil {
		memoryDomain = *role.MemoryDomain
	}
	if err = ValidateProfile(profile, role.UnitID, role.RoleSlug, memoryDomain); err != nil {
		return nil, err
	}

	// The reviewer's own profile is classified sanitized HERE and only here.
	// That is a deliberate classification decision, not a way around the gate:
	// a role profile is the agent's own operating contract, and this one
	// describes how to review a design. It carries no organizational data
	// about the company, which is what the organizational class exists to
	// protect. The blanket organizational default applied to every profile in
	// the ordinary path stays exactly as it is.
	sources := []SourceRecord{
		documentRecord(profile, TierRoleProfile, SourceRoleProfile, InstructionRole, TrustAuthoritative, DataSanitized),
	}

	task, err := s.tasks.GetTaskContext(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("resolve task context: %w", err)
	}
	if task == nil {
		return nil, Reject(ReasonSourceNotFound, request.TaskRef,
			"adversarial review requires the sanitized review bundle carried by its task")
	}
	bundle := normalizeSource(*task, TierTask, SourceTaskContext, InstructionScoped, TrustScoped, false)
	bundle.DataClass = DataSanitized
	sources = append(sources, bundle)

	evidence, err := s.rag.ListApprovedEvidence(ctx, request)
	if err != nil && !errors.Is(err, ErrSourceProviderUnavailable) {
		return nil, fmt.Errorf("resolve authorized evidence: %w", err)
	}
	for _, source := range evidence {
		record := normalizeSource(source, TierRAGEvidence, SourceRAGEvidence, InstructionData, TrustUntrusted, false)
		// Evidence keeps whatever class its provider assigned. Anything the
		// reviewer's provider may not receive is refused below rather than
		// downgraded, because silently reclassifying evidence to make it fit
		// is exactly how a boundary stops meaning anything.
		sources = append(sources, record)
	}

	if err = assertAdversarialEgressSafe(sources); err != nil {
		return nil, err
	}
	return sources, nil
}

// assertAdversarialEgressSafe refuses to BUILD a snapshot carrying anything
// the reviewer's provider may not receive.
//
// Refusing at build time rather than omitting the offending segment is the
// point. An omission would produce a snapshot that looks complete, passes
// egress, and quietly reviewed less than it claimed to. A refusal says which
// source was inadmissible and stops.
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
