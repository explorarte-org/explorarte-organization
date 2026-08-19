package executive

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// This file carries the two typed contracts that separate a candidate design
// from a frozen one:
//
//	adversarial-review/v1   -- an independent reviewer states what is wrong
//	design-adjudication/v1  -- the executive states which of that stands
//
// They are deliberately two contracts executed by two different roles under
// two different purposes. The reviewer finds problems; it does not approve,
// adjudicate, freeze, or propose work. That asymmetry is the whole point, and
// it is enforced here in the host parser rather than requested in a prompt.

// designDigestPattern is the byte-exact identity of the design under review:
// a lowercase SHA-256 hex digest. Freeze binds to this value, so anything
// looser (a version label, a title, a "yes") would let a freeze earned by one
// design silently apply to a different one.
var designDigestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// findingIDPattern bounds the reviewer's own identifiers. Ids are how an
// adjudication accepts or rejects individual findings, so they must be stable,
// comparable tokens rather than free prose.
var findingIDPattern = regexp.MustCompile(`^[A-Z]{2,8}-[0-9]{1,6}$`)

const (
	maxAdversarialFindings = 64
	maxEvidenceRefsPerItem = 16
)

// DesignIdentity is the host's own record of what is being reviewed. It is
// never taken from model output on the review path: the host created the
// review task for a specific design and knows which one it is.
type DesignIdentity struct {
	DesignID      string `json:"design_id"`
	DesignVersion string `json:"design_version"`
	DesignDigest  string `json:"design_digest"`
}

func (d DesignIdentity) Valid() bool {
	return strings.TrimSpace(d.DesignID) != "" && len(d.DesignID) <= 240 &&
		strings.TrimSpace(d.DesignVersion) != "" && len(d.DesignVersion) <= 120 &&
		designDigestPattern.MatchString(d.DesignDigest)
}

type AdversarialVerdict string

const (
	AdversarialAccept AdversarialVerdict = "accept"
	AdversarialRevise AdversarialVerdict = "revise"
	AdversarialBlock  AdversarialVerdict = "block"
)

type FindingSeverity string

const (
	SeverityCritical FindingSeverity = "critical"
	SeverityHigh     FindingSeverity = "high"
	SeverityMedium   FindingSeverity = "medium"
	SeverityLow      FindingSeverity = "low"
)

type AdversarialFinding struct {
	ID                  string          `json:"id"`
	Severity            FindingSeverity `json:"severity"`
	Claim               string          `json:"claim"`
	AffectedRequirement string          `json:"affected_requirement"`
	RequiredCorrection  string          `json:"required_correction"`
	EvidenceRefs        []string        `json:"evidence_refs"`
}

// AdversarialReview carries no proposed_followup_tasks and no approval field,
// and that absence is load-bearing: the reviewer has no authority over the
// task graph and none over the decision. A model result that tries to add
// either is rejected by DisallowUnknownFields before anything reads it.
type AdversarialReview struct {
	SchemaVersion           string               `json:"schema_version"`
	Verdict                 AdversarialVerdict   `json:"verdict"`
	Findings                []AdversarialFinding `json:"findings"`
	Contradictions          []string             `json:"contradictions"`
	UnverifiedAssumptions   []string             `json:"unverified_assumptions"`
	SecurityFindings        []string             `json:"security_findings"`
	AuthorityFindings       []string             `json:"authority_findings"`
	RecoveryFindings        []string             `json:"recovery_findings"`
	MemoryEpistemicFindings []string             `json:"memory_epistemic_findings"`
	EvidenceRefs            []string             `json:"evidence_refs"`
}

type AdjudicationVerdict string

const (
	AdjudicationFreeze AdjudicationVerdict = "freeze"
	AdjudicationRevise AdjudicationVerdict = "revise"
	AdjudicationReject AdjudicationVerdict = "reject"
)

// DesignAdjudication echoes the design identity back. Unlike the review, this
// echo is required and is checked against the host's own record: an
// adjudication is a statement ABOUT a specific design, and one that names a
// different digest than the one it was handed is not a disagreement to
// reconcile, it is a result about something else.
type DesignAdjudication struct {
	SchemaVersion            string              `json:"schema_version"`
	Verdict                  AdjudicationVerdict `json:"verdict"`
	AcceptedFindings         []string            `json:"accepted_findings"`
	RejectedFindings         []string            `json:"rejected_findings"`
	RequiredChanges          []string            `json:"required_changes"`
	UnresolvedOwnerDecisions []string            `json:"unresolved_owner_decisions"`
	DesignID                 string              `json:"design_id"`
	DesignVersion            string              `json:"design_version"`
	DesignDigest             string              `json:"design_digest"`
	EvidenceRefs             []string            `json:"evidence_refs"`
}

func (a DesignAdjudication) Identity() DesignIdentity {
	return DesignIdentity{DesignID: a.DesignID, DesignVersion: a.DesignVersion, DesignDigest: a.DesignDigest}
}

// ParseAdversarialReview validates a reviewer result. It never consults the
// design identity: a review is bound to its design by the host, through the
// durable task it was created for, not by anything the model echoes back.
func ParseAdversarialReview(body []byte, limits Limits) (AdversarialReview, error) {
	var out AdversarialReview
	if err := decodeStrictModelJSON(body, &out, limits); err != nil {
		return AdversarialReview{}, err
	}
	if out.SchemaVersion != AdversarialReviewSchemaVersion {
		return AdversarialReview{}, fmt.Errorf("%w: schema_version", ErrContractRejected)
	}
	switch out.Verdict {
	case AdversarialAccept, AdversarialRevise, AdversarialBlock:
	default:
		return AdversarialReview{}, fmt.Errorf("%w: invalid adversarial verdict", ErrContractRejected)
	}
	if len(out.Findings) > maxAdversarialFindings {
		return AdversarialReview{}, ErrPlanTooLarge
	}
	for name, values := range map[string][]string{
		"contradictions":            out.Contradictions,
		"unverified_assumptions":    out.UnverifiedAssumptions,
		"security_findings":         out.SecurityFindings,
		"authority_findings":        out.AuthorityFindings,
		"recovery_findings":         out.RecoveryFindings,
		"memory_epistemic_findings": out.MemoryEpistemicFindings,
		"evidence_refs":             out.EvidenceRefs,
	} {
		if err := validateStrings(values, limits, name); err != nil {
			return AdversarialReview{}, err
		}
	}
	seen := make(map[string]struct{}, len(out.Findings))
	for i, finding := range out.Findings {
		if !findingIDPattern.MatchString(finding.ID) {
			return AdversarialReview{}, fmt.Errorf("%w: finding[%d].id is invalid", ErrContractRejected, i)
		}
		// Duplicate ids would make an adjudication's accepted/rejected lists
		// ambiguous: "AR-001 rejected" must name exactly one finding.
		if _, duplicate := seen[finding.ID]; duplicate {
			return AdversarialReview{}, fmt.Errorf("%w: duplicate finding id %s", ErrContractRejected, finding.ID)
		}
		seen[finding.ID] = struct{}{}
		switch finding.Severity {
		case SeverityCritical, SeverityHigh, SeverityMedium, SeverityLow:
		default:
			return AdversarialReview{}, fmt.Errorf("%w: finding[%d].severity is invalid", ErrContractRejected, i)
		}
		for name, value := range map[string]string{
			"claim":                finding.Claim,
			"affected_requirement": finding.AffectedRequirement,
			"required_correction":  finding.RequiredCorrection,
		} {
			if err := validateRequiredString(value, limits.MaxStringBytes, name); err != nil {
				return AdversarialReview{}, fmt.Errorf("finding[%d]: %w", i, err)
			}
		}
		if len(finding.EvidenceRefs) > maxEvidenceRefsPerItem {
			return AdversarialReview{}, ErrPlanTooLarge
		}
		if err := validateStrings(finding.EvidenceRefs, limits, "evidence_refs"); err != nil {
			return AdversarialReview{}, fmt.Errorf("finding[%d]: %w", i, err)
		}
	}
	// A verdict that claims problems exist while listing none, or claims none
	// while listing critical ones, is internally inconsistent. Rejecting it
	// keeps the verdict a summary of the findings rather than a mood.
	if out.Verdict != AdversarialAccept && len(out.Findings) == 0 {
		return AdversarialReview{}, fmt.Errorf("%w: verdict %q requires at least one finding", ErrContractRejected, out.Verdict)
	}
	if out.Verdict == AdversarialAccept {
		for _, finding := range out.Findings {
			if finding.Severity == SeverityCritical || finding.Severity == SeverityHigh {
				return AdversarialReview{}, fmt.Errorf("%w: verdict accept contradicts %s finding %s", ErrContractRejected, finding.Severity, finding.ID)
			}
		}
	}
	return out, nil
}

// ParseDesignAdjudication validates an executive adjudication AGAINST the
// design the host handed it. The identity check is not a formality: freeze is
// the one verdict that changes what the organization may do next, and it may
// only ever apply to the exact bytes that were reviewed.
func ParseDesignAdjudication(body []byte, expected DesignIdentity, limits Limits) (DesignAdjudication, error) {
	if !expected.Valid() {
		return DesignAdjudication{}, fmt.Errorf("%w: host design identity is invalid", ErrInvalidInput)
	}
	var out DesignAdjudication
	if err := decodeStrictModelJSON(body, &out, limits); err != nil {
		return DesignAdjudication{}, err
	}
	if out.SchemaVersion != DesignAdjudicationSchemaVersion {
		return DesignAdjudication{}, fmt.Errorf("%w: schema_version", ErrContractRejected)
	}
	switch out.Verdict {
	case AdjudicationFreeze, AdjudicationRevise, AdjudicationReject:
	default:
		return DesignAdjudication{}, fmt.Errorf("%w: invalid adjudication verdict", ErrContractRejected)
	}
	if !out.Identity().Valid() {
		return DesignAdjudication{}, fmt.Errorf("%w: adjudication design identity is invalid", ErrContractRejected)
	}
	if out.DesignDigest != expected.DesignDigest || out.DesignID != expected.DesignID || out.DesignVersion != expected.DesignVersion {
		return DesignAdjudication{}, fmt.Errorf("%w: adjudication identity does not match the design under adjudication", ErrDesignIdentityMismatch)
	}
	for name, values := range map[string][]string{
		"accepted_findings":          out.AcceptedFindings,
		"rejected_findings":          out.RejectedFindings,
		"required_changes":           out.RequiredChanges,
		"unresolved_owner_decisions": out.UnresolvedOwnerDecisions,
		"evidence_refs":              out.EvidenceRefs,
	} {
		if err := validateStrings(values, limits, name); err != nil {
			return DesignAdjudication{}, err
		}
	}
	for _, list := range [][]string{out.AcceptedFindings, out.RejectedFindings} {
		for _, id := range list {
			if !findingIDPattern.MatchString(id) {
				return DesignAdjudication{}, fmt.Errorf("%w: finding reference %q is invalid", ErrContractRejected, id)
			}
		}
	}
	// The same finding cannot be both accepted and rejected.
	accepted := make(map[string]struct{}, len(out.AcceptedFindings))
	for _, id := range out.AcceptedFindings {
		accepted[id] = struct{}{}
	}
	for _, id := range out.RejectedFindings {
		if _, both := accepted[id]; both {
			return DesignAdjudication{}, fmt.Errorf("%w: finding %s is both accepted and rejected", ErrContractRejected, id)
		}
	}
	// A freeze that still names required changes or unresolved owner
	// decisions is not a freeze. Refusing it here means the freeze gate never
	// has to interpret a half-verdict.
	if out.Verdict == AdjudicationFreeze {
		if len(out.RequiredChanges) > 0 {
			return DesignAdjudication{}, fmt.Errorf("%w: freeze cannot carry required_changes", ErrContractRejected)
		}
		if len(out.UnresolvedOwnerDecisions) > 0 {
			return DesignAdjudication{}, fmt.Errorf("%w: freeze cannot carry unresolved_owner_decisions", ErrContractRejected)
		}
	}
	if out.Verdict == AdjudicationRevise && len(out.RequiredChanges) == 0 {
		return DesignAdjudication{}, fmt.Errorf("%w: revise requires at least one required change", ErrContractRejected)
	}
	return out, nil
}

// findingRefSchemaJSON is shared by accepted_findings and rejected_findings so
// the two lists cannot drift apart.
const findingRefSchemaJSON = `{"type":"string"}`

var adversarialReviewOutputSchema = json.RawMessage(`{
  "type":"object",
  "additionalProperties":false,
  "required":[
    "schema_version",
    "verdict",
    "findings",
    "contradictions",
    "unverified_assumptions",
    "security_findings",
    "authority_findings",
    "recovery_findings",
    "memory_epistemic_findings",
    "evidence_refs"
  ],
  "properties":{
    "schema_version":{
      "type":"string",
      "enum":["adversarial-review/v1"]
    },
    "verdict":{
      "type":"string",
      "enum":["accept","revise","block"],
      "description":"accept: no blocking problem found. revise: findings must be addressed. block: the design cannot proceed as written."
    },
    "findings":{
      "type":"array",
      "items":{
        "type":"object",
        "additionalProperties":false,
        "required":["id","severity","claim","affected_requirement","required_correction","evidence_refs"],
        "properties":{
          "id":{"type":"string","description":"Stable identifier such as AR-001. Uppercase prefix, hyphen, digits."},
          "severity":{"type":"string","enum":["critical","high","medium","low"]},
          "claim":{"type":"string","description":"What is wrong, stated as a falsifiable claim."},
          "affected_requirement":{"type":"string"},
          "required_correction":{"type":"string"},
          "evidence_refs":{"type":"array","items":{"type":"string"}}
        }
      }
    },
    "contradictions":{"type":"array","items":{"type":"string"}},
    "unverified_assumptions":{"type":"array","items":{"type":"string"}},
    "security_findings":{"type":"array","items":{"type":"string"}},
    "authority_findings":{"type":"array","items":{"type":"string"}},
    "recovery_findings":{"type":"array","items":{"type":"string"}},
    "memory_epistemic_findings":{"type":"array","items":{"type":"string"}},
    "evidence_refs":{"type":"array","items":{"type":"string"}}
  }
}`)

var designAdjudicationOutputSchema = json.RawMessage(`{
  "type":"object",
  "additionalProperties":false,
  "required":[
    "schema_version",
    "verdict",
    "accepted_findings",
    "rejected_findings",
    "required_changes",
    "unresolved_owner_decisions",
    "design_id",
    "design_version",
    "design_digest",
    "evidence_refs"
  ],
  "properties":{
    "schema_version":{
      "type":"string",
      "enum":["design-adjudication/v1"]
    },
    "verdict":{
      "type":"string",
      "enum":["freeze","revise","reject"],
      "description":"freeze: this exact design is settled. revise: return it for changes. reject: abandon this design."
    },
    "accepted_findings":{"type":"array","items":` + findingRefSchemaJSON + `},
    "rejected_findings":{"type":"array","items":` + findingRefSchemaJSON + `},
    "required_changes":{"type":"array","items":{"type":"string"}},
    "unresolved_owner_decisions":{"type":"array","items":{"type":"string"}},
    "design_id":{"type":"string"},
    "design_version":{"type":"string"},
    "design_digest":{"type":"string","description":"Lowercase SHA-256 hex digest of the design under adjudication. Echo it exactly as supplied."},
    "evidence_refs":{"type":"array","items":{"type":"string"}}
  }
}`)

// AdversarialReviewOutputSchema and DesignAdjudicationOutputSchema expose the
// provider-facing contracts to the orchestrator without letting a caller
// mutate the package-level values.
func AdversarialReviewOutputSchema() json.RawMessage {
	return append(json.RawMessage(nil), adversarialReviewOutputSchema...)
}

func DesignAdjudicationOutputSchema() json.RawMessage {
	return append(json.RawMessage(nil), designAdjudicationOutputSchema...)
}
