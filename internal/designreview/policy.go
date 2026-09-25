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
	"sort"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/contentpolicy"
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
	OwnerRequirements []string `json:"owner_requirements"`
	// CampaignTarget is the owner's own statement of what the campaign asks to be
	// designed, as the host holds it (the approved-state block the host writes into a
	// goal is not part of it). It is what the candidate is judged AGAINST.
	//
	// OwnerRequirements alone cannot carry that: they are the design phase's acceptance
	// criteria, which state properties a design must have ("proposes exactly one new
	// table case") and not the values the owner named ("digits adjacent to letters",
	// "abc123def45"). A reviewer that has never seen the values cannot tell a design
	// that omits them from one that states them, and an adjudicator that has never seen
	// them invents its own -- root 1203 (2026-09-23), whose adjudicator demanded
	// "a12b345c12d" of a campaign that had asked for something else. It is a request,
	// never evidence about the repository.
	CampaignTarget          string   `json:"campaign_target,omitempty"`
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

// AssertNoCredentialMaterial is the credential scan Encode runs, exported so every producer of
// egress-safe bytes uses the SAME detector rather than keeping a second copy that drifts out of
// step with this one.
//
// That detector is internal/contentpolicy, the repository's single deterministic content-safety
// engine. This package used to keep its own list of substrings ("sk-", "password", "api_key",
// "bearer ", ...), and it went wrong exactly the way a substring list does: root 1315
// (2026-09-25), the first run whose departments ran on DeepSeek, was blocked before its
// adversarial review because a worker wrote "task-supplied facts" and "ta[sk-]supplied" contains
// "sk-". Nothing in it was a secret.
//
// The engine recognises credential MATERIAL -- an "sk-" followed by twenty token characters, a
// GitHub or GitLab token, a private-key header, "authorization: bearer <token>", "password=<value>"
// -- and not the words a design legitimately uses to talk about credentials. So a bundle may now say
// "rotate the API key" or "the secret_key field is prohibited"; it may not carry one. That is a
// deliberate relaxation, and it is safe to make here because this scan was never the boundary:
// the closed field list of Bundle is, and this is the belt on top of it. Passing this check does
// not make arbitrary content egress-safe.
//
// The error names the category and byte offsets of the first finding and NEVER the matched value,
// so it is safe to log, persist and show.
func AssertNoCredentialMaterial(label string, body []byte) error {
	assessment := contentpolicy.Analyze(string(body))
	if !assessment.HasCredentials() {
		// A body that is JSON escapes the quotes around a value (password=\"...\"), which can hide an
		// assignment from patterns written for plain text. Look at the decoded strings too.
		assessment = contentpolicy.Analyze(decodedJSONStrings(body))
	}
	if first, found := assessment.First(); found {
		return fmt.Errorf("%w: %s contains %s", ErrBundleContaminated, label, first)
	}
	return nil
}

// decodedJSONStrings returns every string value of a JSON document, in a deterministic order,
// one per line; it returns "" when the body is not JSON.
func decodedJSONStrings(body []byte) string {
	var document any
	if err := json.Unmarshal(body, &document); err != nil {
		return ""
	}
	var lines []string
	var walk func(value any)
	walk = func(value any) {
		switch typed := value.(type) {
		case string:
			lines = append(lines, typed)
		case []any:
			for _, item := range typed {
				walk(item)
			}
		case map[string]any:
			keys := make([]string, 0, len(typed))
			for key := range typed {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				walk(typed[key])
			}
		}
	}
	walk(document)
	return strings.Join(lines, "\n")
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
