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
	OwnerRequirements       []string            `json:"owner_requirements"`
	CandidateDesign         string              `json:"candidate_design"`
	ArchitectureConstraints []string            `json:"architecture_constraints"`
	AuthorityConstraints    []string            `json:"authority_constraints"`
	UnresolvedDecisions     []string            `json:"unresolved_decisions"`
	EvidenceRefs            []string            `json:"authorized_evidence_refs"`
	Design                  designfreeze.Design `json:"design"`
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
	lowered := strings.ToLower(string(body))
	for _, needle := range forbiddenBundleSubstrings {
		if strings.Contains(lowered, needle) {
			return nil, fmt.Errorf("%w: bundle contains %q", ErrBundleContaminated, needle)
		}
	}
	return body, nil
}
