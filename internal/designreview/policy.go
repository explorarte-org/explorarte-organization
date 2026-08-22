// Package designreview holds the policy that governs an adversarial design
// review: who is allowed to perform one, and exactly what they are allowed to
// see.
//
// It deliberately owns NO control flow. The Executive orchestrator drives the
// review and adjudication executions through the same driveTypedTask path as
// every other phase, so there is one state machine in this system and it is
// the Executive's. An earlier revision of this package ran the sequence
// itself; that made it a second orchestrator with its own notion of ordering,
// resume and failure, which is exactly the duplication that produces two
// divergent answers to "what happened".
//
// What remains here is the part that genuinely is policy rather than
// sequencing: the independence rule and the closed-field review bundle.
package designreview

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/designfreeze"
)

var (
	// ErrReviewerNotIndependent means the reviewer and the design's author
	// are the same role, or the reviewer sits inside the authoring unit. A
	// review by the author is not a second opinion.
	ErrReviewerNotIndependent = errors.New("designreview reviewer is not independent of the design author")
	// ErrBundleContaminated means the sanitized bundle carried something it
	// must never carry.
	ErrBundleContaminated = errors.New("designreview bundle is not sanitized")
)

// ProviderUnavailableReason is the operational diagnostic surfaced when the
// reviewer's provider is not configured. It is a stable string so operators
// can grep for it.
const ProviderUnavailableReason = "GROK_REVIEW_UNAVAILABLE=provider_not_configured"

// ReviewerUnitID pins the reviewer to the transversal audit unit. A reviewer
// inside an operational department would report, eventually, to the person
// whose design it is reviewing.
const ReviewerUnitID = "investigacion"

// Participant is the minimum the policy needs to know about a role.
type Participant struct {
	RoleID     string
	UnitID     string
	Enabled    bool
	Executable bool
}

// ValidateIndependence is evaluated BEFORE any execution is created, so an
// improperly composed review never reaches a provider at all.
//
// authoringUnits is every unit that contributed to the candidate design, not
// just the one that led it: a reviewer sitting in any contributing department
// is reviewing its own house.
func ValidateIndependence(reviewer, adjudicator Participant, authoringUnits []string) error {
	if strings.TrimSpace(reviewer.RoleID) == "" || strings.TrimSpace(adjudicator.RoleID) == "" {
		return fmt.Errorf("%w: participants are incomplete", ErrReviewerNotIndependent)
	}
	if reviewer.RoleID == adjudicator.RoleID {
		return fmt.Errorf("%w: the reviewer cannot adjudicate its own findings", ErrReviewerNotIndependent)
	}
	if reviewer.UnitID != ReviewerUnitID {
		return fmt.Errorf("%w: reviewer belongs to %q, not %q", ErrReviewerNotIndependent, reviewer.UnitID, ReviewerUnitID)
	}
	for _, unit := range authoringUnits {
		if strings.TrimSpace(unit) == "" {
			continue
		}
		if reviewer.UnitID == unit {
			return fmt.Errorf("%w: reviewer shares authoring unit %q", ErrReviewerNotIndependent, unit)
		}
		if adjudicator.UnitID == unit {
			return fmt.Errorf("%w: adjudicator shares authoring unit %q", ErrReviewerNotIndependent, unit)
		}
	}
	return nil
}

// Bundle is the sanitized, deterministic input handed to the reviewer. The
// field list IS the contract: anything not named here does not reach the
// provider. That is the opposite of filtering a larger structure and trusting
// the filter to be complete.
type Bundle struct {
	OwnerRequirements       []string `json:"owner_requirements"`
	CandidateDesign         string   `json:"candidate_design"`
	ArchitectureConstraints []string `json:"architecture_constraints"`
	AuthorityConstraints    []string `json:"authority_constraints"`
	UnresolvedDecisions     []string `json:"unresolved_decisions"`
	EvidenceRefs            []string `json:"authorized_evidence_refs"`
	// Deliverables carries, per contributing deliverable, the repository
	// citations the host confirmed were in front of THAT model.
	//
	// Per deliverable and not as one list, because authorization is not a
	// property of a citation -- it is a property of a citation AND the model
	// that used it. Two workers in the same round see different excerpts, so
	// a flat union would let a claim made by a designer who never saw a file
	// inherit the grounding of one who did. Verifying each deliverable
	// individually and then merging the results would throw away exactly the
	// distinction the verification established.
	Deliverables []DeliverableCitations `json:"deliverables"`
	Design       designfreeze.Design    `json:"design"`
}

// DeliverableCitations binds authorized repository references to the one
// deliverable entitled to use them.
type DeliverableCitations struct {
	TaskID       int64  `json:"task_id"`
	InvocationID int64  `json:"invocation_id"`
	ResultDigest string `json:"result_digest"`
	// VerifiedRepositoryRefs are references only. The reviewer never receives
	// the source behind them: its context admits public and sanitized data,
	// and repository evidence is organizational.
	VerifiedRepositoryRefs []string `json:"verified_repository_refs"`
}

// forbiddenBundleSubstrings is a belt-and-braces check on top of the closed
// field list. The closed list is the guarantee; this catches a caller that
// stuffed a secret into a field that is legitimately free text.
var forbiddenBundleSubstrings = []string{
	"-----begin", "api_key", "apikey", "bearer ", "authorization:",
	"password", "secret_key", "private_key", "sk-",
}

// Encode renders the bundle deterministically and refuses to emit one that
// carries obvious credential material.
func (b Bundle) Encode() ([]byte, error) {
	if strings.TrimSpace(b.CandidateDesign) == "" {
		return nil, fmt.Errorf("%w: candidate design is empty", ErrBundleContaminated)
	}
	body, err := json.Marshal(b)
	if err != nil {
		return nil, err
	}
	if err = AssertNoCredentialMaterial("bundle", body); err != nil {
		return nil, err
	}
	return body, nil
}

// AssertNoCredentialMaterial is the credential scan Encode has always run,
// exported so every producer of egress-safe bytes uses the SAME list rather
// than keeping a second copy that drifts out of step with this one.
//
// It is a scan, not a proof. It is the belt on top of a closed field list,
// never a substitute for one: passing this check does not make arbitrary
// content egress-safe.
func AssertNoCredentialMaterial(label string, body []byte) error {
	lowered := strings.ToLower(string(body))
	for _, needle := range forbiddenBundleSubstrings {
		if strings.Contains(lowered, needle) {
			return fmt.Errorf("%w: %s contains %q", ErrBundleContaminated, label, needle)
		}
	}
	return nil
}

// DecodeBundle recovers a Bundle from bytes that claim to be one, and is the
// only supported way to turn untrusted stored text back into an egress-safe
// bundle.
//
// It is deliberately strict. Unknown fields are refused rather than dropped,
// because a payload carrying fields this contract does not name is not a
// bundle that happens to have extras -- it is a different document, and
// silently trimming it would let arbitrary content ride in under the bundle's
// classification. Trailing content is refused for the same reason.
//
// The returned bytes come from re-encoding the decoded value, so what the
// caller ends up carrying is generated from the closed field list rather than
// copied from the input.
func DecodeBundle(raw []byte) (Bundle, []byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var bundle Bundle
	if err := decoder.Decode(&bundle); err != nil {
		return Bundle{}, nil, fmt.Errorf("%w: not a well-formed review bundle: %v", ErrBundleContaminated, err)
	}
	if decoder.More() {
		return Bundle{}, nil, fmt.Errorf("%w: trailing content after the review bundle", ErrBundleContaminated)
	}
	encoded, err := bundle.Encode()
	if err != nil {
		return Bundle{}, nil, err
	}
	return bundle, encoded, nil
}
