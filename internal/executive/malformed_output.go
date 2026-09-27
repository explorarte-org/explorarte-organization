package executive

import "strings"

// A response the model wrote wrong is retried like any other contract rejection.
//
// Model Runtime records a provider outcome's retryability for the transport: a response that was
// received in full and then refused by normalization is recorded as not retryable, because that
// rule protects against repeating a call whose effect is ambiguous. Nothing is ambiguous about a
// response that arrived and is malformed. Local smoke #26 (root 1601, 2026-09-27) stopped at its
// implementation plan because the planner closed one object twice in otherwise valid JSON ("}}]"
// after its patch): the task had three attempts, used one, and the root was blocked as
// executive_phase_failed. The same death killed root 1147's department in smoke #5.
//
// So when the invocation failed normalization because of what the model wrote -- JSON that does
// not parse, does not decode, does not match the schema, is too long, or is not UTF-8 -- the attempt
// fails retryably with a correction the next attempt reads. The attempt budget still bounds it: the
// last attempt ends the task exactly as before. Normalization failures that are the host's own
// (stored schema, response limits, hashing, output mode) stay terminal: retrying them would only
// spend attempts on the same refusal.

const normalizationFailedErrorCode = "response_normalization_failed"

var modelOutputDefectMarkers = []string{
	"invalid JSON response",
	"decode JSON response",
	"schema mismatch",
	"response exceeds byte limit",
	"text output is not UTF-8",
}

const malformedOutputCorrection = "your previous response was refused before anyone read it because it was not well-formed for its " +
	"contract; return exactly one JSON object that parses and matches the output schema, with every string " +
	"escaped and every brace and bracket closed once"

// truncatedEmptyErrorCode is a response that ended at the output ceiling before any answer was
// written: finish_reason=length with no content. Local smoke #31 (root 1701, 2026-09-27): a design
// worker spent its whole output budget reasoning, the attempt was failed as not retryable with two of
// its three attempts unused, and its department was left without a design. The response arrived in
// full, so nothing about repeating it is ambiguous; it is the model's output, retried with a correction.
const truncatedEmptyErrorCode = "response_truncated_empty"

const truncatedEmptyCorrection = "your previous response reached its output limit while reasoning and wrote no answer; " +
	"reason briefly, then write the JSON answer: a shorter complete answer is accepted, an unfinished one is not"

// modelOutputCorrection returns the correction for an invocation that failed because of the model's
// own output, and false for any other failure.
func modelOutputCorrection(errorCode, reason string) (string, bool) {
	if errorCode == truncatedEmptyErrorCode {
		return truncatedEmptyCorrection, true
	}
	if modelOutputDefect(errorCode, reason) {
		return malformedOutputCorrection, true
	}
	return "", false
}

// modelOutputDefect reports whether an invocation failed normalization because of the model's own
// output, as opposed to the host's configuration.
func modelOutputDefect(errorCode, reason string) bool {
	if errorCode != normalizationFailedErrorCode {
		return false
	}
	for _, marker := range modelOutputDefectMarkers {
		if strings.Contains(reason, marker) {
			return true
		}
	}
	return false
}
