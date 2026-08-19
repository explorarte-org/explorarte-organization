// Package designfreeze decides one question and nothing else: may this exact
// candidate design be treated as settled?
//
// It exists because "frozen" is the first property in this system that a model
// result alone could otherwise assert. A design is not frozen because a
// reviewer liked it, and not because an adjudicator said the word freeze -- it
// is frozen when a specific adjudication, over a specific review, of a
// specific digest, returned that verdict. Every one of those bindings is
// checked here, deterministically, before anything durable is written.
//
// The package holds no ports, no storage and no clock. It is a pure decision
// so that the same inputs always produce the same verdict, and so that the
// verdict can be recomputed from durable evidence long after the run ended.
package designfreeze

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strings"
)

// RequirementKey is the durable task requirement this gate satisfies. It uses
// hyphens because internal/tasks rejects dots in requirement keys -- the same
// constraint that renamed engineering.required_gates to
// engineering-required-gates.
const RequirementKey = "design-freeze"

// RecordSchemaVersion versions the evidence payload written when the gate is
// satisfied, so a stored freeze can be re-read by a later build that knows the
// shape changed.
const RecordSchemaVersion = "design-freeze/v1"

var digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Design is the host's record of what is being frozen.
type Design struct {
	ID      string `json:"design_id"`
	Version string `json:"design_version"`
	Digest  string `json:"design_digest"`
}

func (d Design) valid() bool {
	return strings.TrimSpace(d.ID) != "" && len(d.ID) <= 240 &&
		strings.TrimSpace(d.Version) != "" && len(d.Version) <= 120 &&
		digestPattern.MatchString(d.Digest)
}

// Equal is how a stored freeze is compared against a design presented later.
// It compares the digest AND the labels: a design whose bytes are unchanged
// but which is being presented under a different identity is not the same
// artifact for the purpose of authorizing work.
func (d Design) Equal(other Design) bool {
	return d.ID == other.ID && d.Version == other.Version && d.Digest == other.Digest
}

// ExecutionRef points at one durable cognitive execution. Every field is
// host-side: task, attempt and invocation identifiers the orchestrator already
// holds, plus the digest of the result body that was accepted. Nothing here is
// model-attested.
type ExecutionRef struct {
	TaskID       int64  `json:"task_id"`
	AttemptID    int64  `json:"attempt_id"`
	InvocationID int64  `json:"invocation_id"`
	ResultDigest string `json:"result_digest"`
	Verdict      string `json:"verdict"`
}

func (r ExecutionRef) valid() bool {
	return r.TaskID > 0 && r.AttemptID > 0 && r.InvocationID > 0 &&
		digestPattern.MatchString(r.ResultDigest) && strings.TrimSpace(r.Verdict) != ""
}

// Request is everything the gate is allowed to consider.
//
// Review.Design and Adjudication.Design are carried separately from Design on
// purpose. They are the host's record of which design each execution actually
// ran against, and requiring all three to agree is what stops a review of
// design A and an adjudication of design B from combining into a freeze of
// either.
type Request struct {
	Design             Design
	Review             ExecutionRef
	ReviewDesign       Design
	Adjudication       ExecutionRef
	AdjudicationDesign Design
}

type Decision struct {
	Satisfied  bool
	ReasonCode string
	Record     Record
}

// Record is the durable evidence written when, and only when, the gate is
// satisfied. Digest is a hash over the whole binding, so a stored freeze whose
// fields were later edited stops verifying.
type Record struct {
	SchemaVersion string       `json:"schema_version"`
	Design        Design       `json:"design"`
	Review        ExecutionRef `json:"adversarial_review"`
	Adjudication  ExecutionRef `json:"design_adjudication"`
	Digest        string       `json:"-"`
}

const (
	ReasonSatisfied                  = "design_freeze_satisfied"
	ReasonDesignInvalid              = "design_identity_invalid"
	ReasonReviewMissing              = "adversarial_review_missing"
	ReasonReviewDesignMismatch       = "adversarial_review_design_mismatch"
	ReasonAdjudicationMissing        = "design_adjudication_missing"
	ReasonAdjudicationDesignMismatch = "design_adjudication_design_mismatch"
	ReasonVerdictNotFreeze           = "design_adjudication_verdict_not_freeze"
	ReasonReviewVerdictUnknown       = "adversarial_review_verdict_unknown"
)

// FreezeVerdict is the single adjudication verdict that can satisfy the gate.
const FreezeVerdict = "freeze"

var knownReviewVerdicts = map[string]struct{}{"accept": {}, "revise": {}, "block": {}}

// Evaluate is fail-closed in the ordinary sense -- anything missing or
// inconsistent denies -- and deliberately has no path that returns Satisfied
// without all five bindings present: a valid design identity, a durable
// review of THAT design, a durable adjudication of THAT design, a freeze
// verdict, and agreement between all three identities.
func Evaluate(request Request) Decision {
	if !request.Design.valid() {
		return Decision{ReasonCode: ReasonDesignInvalid}
	}
	if !request.Review.valid() {
		return Decision{ReasonCode: ReasonReviewMissing}
	}
	if _, known := knownReviewVerdicts[request.Review.Verdict]; !known {
		return Decision{ReasonCode: ReasonReviewVerdictUnknown}
	}
	if !request.ReviewDesign.valid() || !request.ReviewDesign.Equal(request.Design) {
		return Decision{ReasonCode: ReasonReviewDesignMismatch}
	}
	if !request.Adjudication.valid() {
		return Decision{ReasonCode: ReasonAdjudicationMissing}
	}
	if !request.AdjudicationDesign.valid() || !request.AdjudicationDesign.Equal(request.Design) {
		return Decision{ReasonCode: ReasonAdjudicationDesignMismatch}
	}
	// Note what is NOT checked: the review's own verdict. An adversarial
	// review that returned "accept" does not freeze anything on its own, and
	// one that returned "block" does not prevent the executive from
	// adjudicating. The reviewer informs the decision; it does not hold a
	// veto and it does not hold an approval. Only the adjudication verdict
	// decides, which is why it is the last thing examined.
	if request.Adjudication.Verdict != FreezeVerdict {
		return Decision{ReasonCode: ReasonVerdictNotFreeze}
	}
	record := Record{
		SchemaVersion: RecordSchemaVersion,
		Design:        request.Design,
		Review:        request.Review,
		Adjudication:  request.Adjudication,
	}
	record.Digest = recordDigest(record)
	return Decision{Satisfied: true, ReasonCode: ReasonSatisfied, Record: record}
}

// Satisfies answers whether an already-stored freeze authorizes a design being
// presented now. A freeze earned by one digest never carries over to another:
// this is the property that makes a design revision start from unfrozen, with
// no way to inherit the previous decision by keeping the same title.
func Satisfies(record Record, design Design) bool {
	if record.SchemaVersion != RecordSchemaVersion || !design.valid() {
		return false
	}
	if record.Digest == "" || record.Digest != recordDigest(record) {
		return false
	}
	if record.Adjudication.Verdict != FreezeVerdict {
		return false
	}
	return record.Design.Equal(design)
}

// EvidencePayload is the canonical body stored alongside the satisfied
// requirement. Marshalling a struct with fixed field order keeps it byte
// stable, which is what lets Digest be recomputed and compared later.
func EvidencePayload(record Record) ([]byte, error) {
	return json.Marshal(record)
}

func recordDigest(record Record) string {
	record.Digest = ""
	body, err := json.Marshal(record)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
