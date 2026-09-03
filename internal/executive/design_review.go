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

// FindingKind separates a judgement about the design from a judgement about
// whether the design was entitled to make a claim at all.
type FindingKind string

const (
	// FindingDesign is an ordinary finding about the design's substance.
	FindingDesign FindingKind = "design"
	// FindingUnverifiableRepositoryClaim is the design asserting something
	// concrete about the repository -- a file, a symbol, existing behaviour,
	// current structure -- with no authorized citation behind it.
	//
	// It exists as its own kind because it is a different failure. An
	// ordinary finding says the design is wrong; this one says nobody can
	// tell, because the thing it rests on was never shown to have existed.
	// AUTONOMY-SMOKE-016 produced these by the dozen while every component
	// reported success.
	FindingUnverifiableRepositoryClaim FindingKind = "unverifiable_repository_claim"
)

type AdversarialFinding struct {
	ID                  string          `json:"id"`
	Kind                FindingKind     `json:"kind"`
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
	SchemaVersion    string              `json:"schema_version"`
	Verdict          AdjudicationVerdict `json:"verdict"`
	AcceptedFindings []string            `json:"accepted_findings"`
	RejectedFindings []string            `json:"rejected_findings"`
	RequiredChanges  []string            `json:"required_changes"`
	// EvidenceRequirements is what the next round must GROUND, as data.
	//
	// AUTONOMY-SMOKE-017-R5 spent both of its rounds on the same rejection:
	// the adjudicator asked for separate citations for where each limit is
	// defined and where it is applied, and could only say so in
	// required_changes prose. The next round received an English sentence
	// where it needed a contract, and nothing downstream could check
	// whether the sentence had been honoured. This is the same loss that
	// worker-result/v2 fixed one leg later, and it is repaired here for the
	// same reason: a normative obligation carried as prose is an obligation
	// nobody can enforce.
	//
	// The model PROPOSES. The host validates, attributes and persists --
	// see EvidenceRequirement.Source. A reviewer cannot be the sole author
	// of what it will later be satisfied by.
	EvidenceRequirements     []EvidenceRequirementProposal `json:"evidence_requirements"`
	UnresolvedOwnerDecisions []string                      `json:"unresolved_owner_decisions"`
	DesignID                 string                        `json:"design_id"`
	DesignVersion            string                        `json:"design_version"`
	DesignDigest             string                        `json:"design_digest"`
	EvidenceRefs             []string                      `json:"evidence_refs"`
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
	// The HOST binds the design identity. It is not asked of the model and
	// not read back from it.
	//
	// It used to be an echo, checked field by field against this same
	// expected value. That never verified anything: a model repeating a
	// 64-character hex digest proves only that it can copy, and the host
	// generated the digest in the first place. What actually ties a verdict
	// to the exact bytes reviewed is that the host hands over one design and
	// binds that design's identity to whatever verdict comes back -- which is
	// what happens here, unconditionally.
	//
	// The echo was not merely useless, it was a check that could only fail.
	// AUTONOMY-SMOKE-001's adjudication produced a complete, well-formed
	// verdict three times and was rejected all three, dead-lettering the
	// task, because the model transcribed 63 of the digest's 64 characters.
	// Nothing about the design or the verdict was wrong.
	out.DesignID = expected.DesignID
	out.DesignVersion = expected.DesignVersion
	out.DesignDigest = expected.DesignDigest
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
	// Only a revise opens another round, so only a revise can bind what
	// that round must ground. A freeze or a reject carrying requirements
	// would be stating obligations for a round that will never happen.
	if out.Verdict != AdjudicationRevise && len(out.EvidenceRequirements) > 0 {
		return DesignAdjudication{}, fmt.Errorf("%w: only revise can carry evidence_requirements", ErrContractRejected)
	}
	if err := validateEvidenceRequirementProposals(out.EvidenceRequirements, limits); err != nil {
		return DesignAdjudication{}, err
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
        "required":["id","kind","severity","claim","affected_requirement","required_correction","evidence_refs"],
        "properties":{
          "id":{"type":"string","description":"Stable identifier such as AR-001. Uppercase prefix, hyphen, digits."},
          "kind":{
            "type":"string",
            "enum":["design","unverifiable_repository_claim"],
            "description":"unverifiable_repository_claim: the design asserts something concrete about the repository -- a file, a symbol, existing behavior, current structure -- without citing an authorized repository:// reference from this bundle. Use design for everything else."
          },
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

// designAdjudicationOutputSchema deliberately has no design_id,
// design_version or design_digest.
//
// The host binds the design identity onto the verdict from its own record, so
// the model has no business supplying it -- an echo it cannot get wrong is not
// evidence, and one it can get wrong is a way to lose a well-formed
// adjudication to a transcription slip, which is exactly how one was
// dead-lettered over a single hex character.
//
// Leaving them in properties while dropping them from required was the
// half-measure between those two positions, and it made the schema invalid:
// under strict structured outputs every property must be required, so the
// provider rejected the request before any model saw it.
// adjudicationExistingWorldRule is THE single textual authority on what an
// adjudication's evidence_requirements may bind the next round to. It is
// rendered in TWO places that must never drift apart -- the run-time
// ExecutionContract for PurposeDesignAdjudication and this output schema's
// field description -- because R14 died twice at exactly this gap: the adjudicator
// proposed creating "MaxModelCalls" and then demanded definition/application
// evidence FOR it, a symbol her own design had not yet created and which no
// snapshot at the frozen pin could ever supply. The host's
// probeAdjudicationRequirements refused correctly both times; nothing had
// told her the boundary existed before she answered.
//
// Deliberately NOT redefining what "supplyable" means: that concept lives in
// repositoryevidence's probe and its wording here ("the host probes every
// proposal against that pin") points at it rather than restating it.
const adjudicationExistingWorldRule = `Evidence requirements bind the next round to the world AS IT IS at the frozen DesignBaseSHA pin: name only literal repository symbols that already exist there -- the host probes every proposal against that pinned repository and refuses obligations it cannot supply. required_changes MAY prescribe creating, renaming or removing symbols; NEVER create an evidence requirement for a symbol this design proposes to introduce -- it does not exist yet and cannot ground anything. Ground a proposed new symbol through the existing code and behavior it is meant to change.`

// adjudicationEvidenceContractGuidance renders that authority as the
// execution-time contract for the one purpose that authors
// evidence_requirements. Like every ExecutionContract rider, it reaches the
// model before it answers without entering durable instructions or the
// repository selection text.
func adjudicationEvidenceContractGuidance() string {
	return "Existing-world rule for evidence_requirements in this adjudication:\n\n" + adjudicationExistingWorldRule
}

var designAdjudicationOutputSchema = json.RawMessage(`{
  "type":"object",
  "additionalProperties":false,
  "required":[
    "schema_version",
    "verdict",
    "accepted_findings",
    "rejected_findings",
    "required_changes",
    "evidence_requirements",
    "unresolved_owner_decisions",
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
    "evidence_requirements":{
      "type":"array",
      "description":"What the next round must ground, as data. Only meaningful with verdict=revise. subject must be a code identifier that exists verbatim in the pinned repository -- a type, function, constant or field name the host can literally find and cite. Conceptual labels or invented shapes such as Type.Method cannot be grounded: the host rejects obligations it cannot supply. relations are the roles a citation must play for it. The host can only ever supply definition (where the symbol is declared) or application (where it is used): demanding any other relation creates an obligation no snapshot could fill.",
      "items":{
        "type":"object",
        "additionalProperties":false,
        "required":["subject","relations"],
        "properties":{
          "subject":{"type":"string","description":"A literal Go identifier from the pinned repository, such as MaxDesignRounds or driveDesignFreeze."},
          "relations":{"type":"array","items":{"type":"string","enum":["definition","application"]}}
        }
      }
    },
    "unresolved_owner_decisions":{"type":"array","items":{"type":"string"}},
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
	return DesignAdjudicationOutputSchemaFor(nil)
}

// DesignAdjudicationOutputSchemaFor builds the adjudication contract for one
// specific review, enumerating the finding identifiers that actually exist in
// it.
//
// The static schema typed accepted_findings and rejected_findings as plain
// strings while the host held them to an identifier pattern. The contract the
// model was shown was looser than the one it was judged by, so a sentence --
// a perfectly valid string -- passed the schema and failed the parse. A
// campaign died at adjudication with an explanatory clause recorded as a
// finding reference.
//
// An enum closes that at the only place that can refuse it before a model
// speaks: the provider. With no findings there is nothing to enumerate and
// the subset forbids maxItems, so the field falls back to a described string
// and AssertFindingsExist carries the invariant alone. That fallback is why
// the host check exists in the first place rather than being left to the
// schema: the schema can only help when there is something to enumerate.
// legacyAdjudicationEvidenceDescription is the pre-C description text that
// DesignAdjudicationOutputSchemaFor replaces with the shared authority. It is
// kept as a named constant so the replacement target cannot silently rot.
const legacyAdjudicationEvidenceDescription = `What the next round must ground, as data. Only meaningful with verdict=revise. subject must be a code identifier that exists verbatim in the pinned repository -- a type, function, constant or field name the host can literally find and cite. Conceptual labels or invented shapes such as Type.Method cannot be grounded: the host rejects obligations it cannot supply. relations are the roles a citation must play for it. The host can only ever supply definition (where the symbol is declared) or application (where it is used): demanding any other relation creates an obligation no snapshot could fill.`

func DesignAdjudicationOutputSchemaFor(findingIDs []string) json.RawMessage {
	unique := make(map[string]struct{}, len(findingIDs))
	ordered := make([]string, 0, len(findingIDs))
	for _, id := range findingIDs {
		if !findingIDPattern.MatchString(id) {
			continue
		}
		if _, seen := unique[id]; seen {
			continue
		}
		unique[id] = struct{}{}
		ordered = append(ordered, id)
	}
	const describedString = `{"type":"string","description":"Identifier of a finding raised by the adversarial review, such as AR-001. Never prose."}`
	refItems := describedString
	if len(ordered) > 0 {
		if encoded, err := json.Marshal(ordered); err == nil {
			refItems = `{"type":"string","enum":` + string(encoded) + `}`
		}
	}
	schema := string(designAdjudicationOutputSchema)
	for _, field := range []string{"accepted_findings", "rejected_findings"} {
		schema = strings.ReplaceAll(schema,
			`"`+field+`":{"type":"array","items":`+findingRefSchemaJSON+`}`,
			`"`+field+`":{"type":"array","items":`+refItems+`}`)
	}
	// The evidence_requirements description is BUILT from the shared
	// authority, not hand-written beside it: schema and ExecutionContract are
	// two renderings of one rule, so neither can silently diverge from what
	// probeAdjudicationRequirements enforces.
	schema = strings.ReplaceAll(schema, legacyAdjudicationEvidenceDescription,
		`Only meaningful with verdict=revise. `+adjudicationExistingWorldRule+
			` relations are the roles a citation must play for a symbol: the host can only ever supply definition (where the symbol is declared) or application (where it is used); demanding any other relation creates an obligation no snapshot could fill.`)
	return json.RawMessage(schema)
}

// AssertFindingsExist refuses an adjudication that cites a finding the review
// never raised.
//
// This is the invariant the identifier pattern was standing in for. A
// well-formed identifier is not the same as a real one: "AR-009" satisfies
// every syntactic rule and still refers to nothing, and an adjudication that
// accepts findings nobody made is a verdict about a review that does not
// exist.
func AssertFindingsExist(adjudication DesignAdjudication, reviewFindingIDs []string) error {
	known := make(map[string]struct{}, len(reviewFindingIDs))
	for _, id := range reviewFindingIDs {
		known[id] = struct{}{}
	}
	for label, cited := range map[string][]string{
		"accepted": adjudication.AcceptedFindings,
		"rejected": adjudication.RejectedFindings,
	} {
		for _, id := range cited {
			if _, exists := known[id]; !exists {
				return fmt.Errorf("%w: %s finding %q was never raised by the review", ErrContractRejected, label, id)
			}
		}
	}
	return nil
}
