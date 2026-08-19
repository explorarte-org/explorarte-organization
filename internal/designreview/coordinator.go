// Package designreview sequences the three epistemic steps that stand between
// a candidate design and a frozen one: an independent adversarial review, an
// executive adjudication of that review, and the freeze gate.
//
// It is a host coordinator, not a state machine. Every decision it makes is
// deterministic and every model result passes through the typed parsers in
// internal/executive before it is allowed to mean anything. Nothing here can
// authorize implementation: the outcome of a satisfied freeze is a durable
// record, and this package has no port that could dispatch work.
//
// It lives beside the Executive orchestrator rather than inside it because the
// orchestrator's run state machine was hardened separately and this slice has
// no reason to reopen it. Triggering Run from that state machine is the
// remaining wire and is deliberately not done here.
package designreview

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Mireuz13/explorarte-organization/internal/designfreeze"
	"github.com/Mireuz13/explorarte-organization/internal/executive"
)

var (
	// ErrReviewerNotIndependent means the reviewer and the design's author
	// are the same role, or the reviewer sits inside the authoring unit. A
	// review by the author is not a second opinion.
	ErrReviewerNotIndependent = errors.New("designreview reviewer is not independent of the design author")
	// ErrProviderUnavailable is the fail-closed state while xAI is not
	// configured. It never degrades into another provider.
	ErrProviderUnavailable = errors.New("designreview adversarial review provider unavailable")
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

// RoleRef is the minimum the coordinator needs to know about a participant.
type RoleRef struct {
	ID     string
	UnitID string
}

// Bundle is the sanitized, deterministic input handed to the reviewer. The
// field list IS the contract: anything not named here does not reach the
// provider, which is the opposite of filtering a larger structure and hoping
// the filter is complete.
type Bundle struct {
	OwnerRequirements       []string            `json:"owner_requirements"`
	CandidateDesign         string              `json:"candidate_design"`
	ArchitectureConstraints []string            `json:"architecture_constraints"`
	AuthorityConstraints    []string            `json:"authority_constraints"`
	UnresolvedDecisions     []string            `json:"unresolved_decisions"`
	EvidenceRefs            []string            `json:"authorized_evidence_refs"`
	Design                  designfreeze.Design `json:"design"`
}

// forbiddenBundleKeys are substrings that must never appear in a bundle key or
// value. This is a belt-and-braces check on top of the closed field list: the
// closed list is the guarantee, and this catches a caller that stuffed a
// secret into a field that is legitimately free text.
var forbiddenBundleSubstrings = []string{
	"-----begin", "api_key", "apikey", "bearer ", "authorization:",
	"password", "secret_key", "private_key", "xai-", "sk-",
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

// ExecutionRequest is one typed cognitive execution. The coordinator supplies
// the purpose, the subject role and the output schema; it never supplies a
// provider or a model, because it does not know one.
type ExecutionRequest struct {
	Purpose      executive.ExecutionPurpose
	SubjectRole  string
	OutputSchema json.RawMessage
	Input        []byte
	Design       designfreeze.Design
}

// ExecutionResult is what the host durably recorded for that execution.
type ExecutionResult struct {
	TaskID       int64
	AttemptID    int64
	InvocationID int64
	Body         []byte
}

// TypedExecutor runs one schema-bound execution through the Harness. It is the
// only outbound port that can reach a model, and it carries no way to name one.
type TypedExecutor interface {
	Execute(context.Context, ExecutionRequest) (ExecutionResult, error)
}

// FreezeRecorder persists a satisfied freeze as durable evidence against the
// design-freeze requirement. It is deliberately write-only and deliberately
// cannot be told to record anything else.
type FreezeRecorder interface {
	RecordFreeze(context.Context, designfreeze.Record) error
}

type Coordinator struct {
	Reviewer    RoleRef
	Adjudicator RoleRef
	Author      RoleRef
	Executor    TypedExecutor
	Recorder    FreezeRecorder
	Limits      executive.Limits
}

// Outcome reports what happened, in terms that cannot be mistaken for
// permission. There is no "may implement" field, and there never should be:
// a frozen design settles what to build, not the authority to build it.
type Outcome struct {
	Review       executive.AdversarialReview
	Adjudication executive.DesignAdjudication
	Frozen       bool
	FreezeReason string
	FreezeRecord designfreeze.Record
}

// Run executes the review, then the adjudication, then the gate -- in that
// order, with the gate consulted only at the end. The intermediate assertion
// after the review is not decoration: it is the property that a review, of any
// verdict, freezes nothing on its own.
func (c Coordinator) Run(ctx context.Context, design designfreeze.Design, bundle Bundle) (Outcome, error) {
	if err := c.validateIndependence(); err != nil {
		return Outcome{}, err
	}
	bundle.Design = design
	input, err := bundle.Encode()
	if err != nil {
		return Outcome{}, err
	}

	reviewResult, err := c.Executor.Execute(ctx, ExecutionRequest{
		Purpose:      executive.PurposeAdversarialReview,
		SubjectRole:  c.Reviewer.ID,
		OutputSchema: executive.AdversarialReviewOutputSchema(),
		Input:        input,
		Design:       design,
	})
	if err != nil {
		return Outcome{}, err
	}
	review, err := executive.ParseAdversarialReview(reviewResult.Body, c.limits())
	if err != nil {
		return Outcome{}, err
	}
	reviewRef := designfreeze.ExecutionRef{
		TaskID: reviewResult.TaskID, AttemptID: reviewResult.AttemptID,
		InvocationID: reviewResult.InvocationID, ResultDigest: digestOf(reviewResult.Body),
		Verdict: string(review.Verdict),
	}

	// Explicitly evaluated, not assumed: after a completed review and before
	// any adjudication, the gate must still deny.
	if decision := designfreeze.Evaluate(designfreeze.Request{
		Design: design, Review: reviewRef, ReviewDesign: design,
	}); decision.Satisfied {
		return Outcome{}, errors.New("designreview: gate satisfied by a review alone")
	}

	adjudicationResult, err := c.Executor.Execute(ctx, ExecutionRequest{
		Purpose:      executive.PurposeDesignAdjudication,
		SubjectRole:  c.Adjudicator.ID,
		OutputSchema: executive.DesignAdjudicationOutputSchema(),
		Input:        input,
		Design:       design,
	})
	if err != nil {
		return Outcome{}, err
	}
	expected := executive.DesignIdentity{DesignID: design.ID, DesignVersion: design.Version, DesignDigest: design.Digest}
	adjudication, err := executive.ParseDesignAdjudication(adjudicationResult.Body, expected, c.limits())
	if err != nil {
		return Outcome{}, err
	}

	decision := designfreeze.Evaluate(designfreeze.Request{
		Design:       design,
		Review:       reviewRef,
		ReviewDesign: design,
		Adjudication: designfreeze.ExecutionRef{
			TaskID: adjudicationResult.TaskID, AttemptID: adjudicationResult.AttemptID,
			InvocationID: adjudicationResult.InvocationID, ResultDigest: digestOf(adjudicationResult.Body),
			Verdict: string(adjudication.Verdict),
		},
		AdjudicationDesign: design,
	})
	outcome := Outcome{Review: review, Adjudication: adjudication, Frozen: decision.Satisfied, FreezeReason: decision.ReasonCode, FreezeRecord: decision.Record}
	if !decision.Satisfied {
		return outcome, nil
	}
	if err = c.Recorder.RecordFreeze(ctx, decision.Record); err != nil {
		return Outcome{}, err
	}
	return outcome, nil
}

// validateIndependence is checked before any execution, so an improperly
// composed review never reaches a provider at all.
func (c Coordinator) validateIndependence() error {
	if strings.TrimSpace(c.Reviewer.ID) == "" || strings.TrimSpace(c.Adjudicator.ID) == "" || strings.TrimSpace(c.Author.ID) == "" {
		return fmt.Errorf("%w: participants are incomplete", ErrReviewerNotIndependent)
	}
	if c.Reviewer.ID == c.Author.ID {
		return fmt.Errorf("%w: %s reviewed its own design", ErrReviewerNotIndependent, c.Reviewer.ID)
	}
	if c.Reviewer.ID == c.Adjudicator.ID {
		return fmt.Errorf("%w: the reviewer cannot adjudicate its own findings", ErrReviewerNotIndependent)
	}
	if c.Reviewer.UnitID != ReviewerUnitID {
		return fmt.Errorf("%w: reviewer belongs to %q, not %q", ErrReviewerNotIndependent, c.Reviewer.UnitID, ReviewerUnitID)
	}
	if c.Reviewer.UnitID == c.Author.UnitID {
		return fmt.Errorf("%w: reviewer and author share unit %q", ErrReviewerNotIndependent, c.Author.UnitID)
	}
	return nil
}

func (c Coordinator) limits() executive.Limits {
	if c.Limits.MaxInputBytes <= 0 {
		return executive.DefaultLimits()
	}
	return c.Limits
}

func digestOf(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
