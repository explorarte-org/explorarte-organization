package modelruntime

import "time"

// CapacityFeedbackKind names the capacity-state action one classified
// ProviderOutcome projects onto model_routing_capacity_state (Model
// Capacity State V1, migration 000072). A closed set on purpose: adding a
// classification widens what capacity state can express and must be a
// deliberate, reviewed change here, never inferred ad hoc at a call site.
type CapacityFeedbackKind string

const (
	// CapacityFeedbackNoop makes NO change to the persisted availability
	// signals (disabled/quota_exhausted/cooldown_until). The durable
	// projection still records the outcome's telemetry for observability
	// (last_outcome_classification/last_http_status/...) -- only the
	// AVAILABILITY decision is a no-op.
	CapacityFeedbackNoop CapacityFeedbackKind = "noop"
	// CapacityFeedbackHealthy clears any transient cooldown and quota
	// exhaustion, and resets the consecutive-failure counter. It never
	// clears Disabled (Section 4.A) -- Disabled stays reserved for
	// administrative/canonical control, outside this classifier's
	// authority entirely.
	CapacityFeedbackHealthy CapacityFeedbackKind = "healthy"
	// CapacityFeedbackCooldown sets a temporary, expiring ineligibility
	// window (Duration from now). Never sets QuotaExhausted or Disabled.
	CapacityFeedbackCooldown CapacityFeedbackKind = "cooldown"
	// CapacityFeedbackQuota marks quota exhaustion. ClassifyCapacityFeedback
	// never returns this in V1 -- see its own doc comment and
	// QUOTA_SIGNAL_NOT_CANONICAL_YET -- but the projection and read model
	// support it so a future, explicitly-approved exact-token rule (or a
	// manual operator action) has somewhere durable to land without a
	// schema change.
	CapacityFeedbackQuota CapacityFeedbackKind = "quota"
)

// CapacityFeedback is the pure, single-value output of classifying one
// ProviderOutcome. Duration is meaningful only for CapacityFeedbackCooldown
// and is a LENGTH, not an absolute time -- the caller/projection adds "now".
type CapacityFeedback struct {
	Kind     CapacityFeedbackKind
	Duration time.Duration
	// Reason is a short, stable, internal token (e.g. "ambiguous_transport",
	// "http_429") -- never provider-supplied text, never a secret. Persisted
	// nowhere by itself today; carried for callers/tests that want to assert
	// WHY a Kind was chosen, not just which Kind.
	Reason string
}

const (
	// capacityCooldownAmbiguousTransport: Section 4.D.
	capacityCooldownAmbiguousTransport = 30 * time.Second
	// capacityCooldownRetryable5xx: Section 4.E.
	capacityCooldownRetryable5xx = 30 * time.Second
	// capacityCooldownHTTP429: Section 4.F. Longer than the two 30s
	// cooldowns above because an HTTP 429 is the one signal here we know
	// FOR CERTAIN means "do not retry immediately" -- as opposed to the
	// other two, which are our own best guess at a transient condition.
	capacityCooldownHTTP429 = 5 * time.Minute
)

// ClassifyCapacityFeedback translates one durable ProviderOutcome into a
// CapacityFeedback action. Pure: no I/O, no clock read, no randomness --
// Duration is a length the caller/projection applies against its own "now".
//
// phase is accepted because every real call site (see
// postgres/results.go's five insertProviderOutcome callers) always pairs a
// specific OutcomeClassification with a specific AdapterFailurePhase (e.g.
// ProviderOutcomeNotSent only ever arrives at AdapterFailureBeforeRequest),
// and this function's signature names that pairing explicitly rather than
// leaving it an implicit, undocumented assumption. Every rule below already
// follows deterministically from outcome.OutcomeClassification and its own
// fields; phase is not currently branched on, and is here so a future,
// deliberately-added phase-specific rule has a place to go without changing
// this function's signature again.
//
// The policy is intentionally CONSERVATIVE (spec Section 4): anything not
// explicitly covered by rules A-H below returns CapacityFeedbackNoop, never
// a guess.
//
// QUOTA_SIGNAL_NOT_CANONICAL_YET: rule G required an inventory, done here,
// of every ErrorClass/ErrorCode adapters/tests currently emit, before
// deciding whether an automatic QuotaExhausted rule could exist at all.
// Every adapter's error class/code is either (a) a small set of adapter-
// fixed literals describing OUR OWN local classification (e.g. "transport",
// "credential", "circuit_breaker" -- never provider-authored), or (b) a
// normalized PASS-THROUGH of the provider's own free-form error envelope
// (see e.g. deepseek/adapter.go's parseProviderError, which copies
// envelope.Error.Type/Code almost verbatim through a character-set
// normalizer; xai/stream.go's decodeStreamError, which does the same for
// its streaming error events). Case (b) is external, per-provider,
// uncontracted text -- there is no closed, exact, cross-provider token in
// it that means "quota/credit exhausted" today. Matching on provider text
// by substring is explicitly prohibited by the spec this implements
// (a provider could rename or rephrase the string at any time, silently
// breaking or misfiring the match); matching on an exact token that does
// not exist would mean inventing one. So: this function never returns
// CapacityFeedbackQuota. If a future adapter round adds a genuinely
// closed, documented, exact quota token, ClassifyCapacityFeedback is the
// one place a new rule would be added, with its own test per token
// (Section 11).
func ClassifyCapacityFeedback(outcome ProviderOutcome, phase AdapterFailurePhase) CapacityFeedback {
	switch outcome.OutcomeClassification {
	case ProviderOutcomeResponseReceived:
		// Section 4.A: a provider that answered demonstrated availability.
		// consecutive_capacity_failures resets; cooldown/quota_exhausted
		// clear. Disabled is untouched -- clearing it is not this
		// function's authority.
		return CapacityFeedback{Kind: CapacityFeedbackHealthy, Reason: "response_received"}

	case ProviderOutcomeNotSent:
		// Section 4.B: the request may never have reached the provider at
		// all (credential unavailable, local encoding failure, local
		// circuit breaker, deadline before send, other preflight
		// failures). Never penalize capacity for a call that may not have
		// left this process.
		return CapacityFeedback{Kind: CapacityFeedbackNoop, Reason: "request_not_sent"}

	case ProviderOutcomeCancelled:
		// Section 4.C: a cancellation WE requested and the provider
		// confirmed says nothing about the provider's own capacity.
		return CapacityFeedback{Kind: CapacityFeedbackNoop, Reason: "cancelled_confirmed"}

	case ProviderOutcomeAmbiguous:
		// Section 4.D: only a retryable ambiguity gets the short cooldown.
		// A non-retryable ambiguity (theoretically possible; every current
		// call site sets Retryable=true for this classification) stays
		// noop -- we do not know enough to penalize capacity.
		if outcome.Retryable {
			return CapacityFeedback{Kind: CapacityFeedbackCooldown, Duration: capacityCooldownAmbiguousTransport, Reason: "ambiguous_transport"}
		}
		return CapacityFeedback{Kind: CapacityFeedbackNoop, Reason: "ambiguous_not_retryable"}

	case ProviderOutcomeRejected:
		// Section 4.F: HTTP 429 always gets a 5-minute cooldown -- we KNOW
		// not to retry immediately -- but 429 ALONE never implies quota
		// exhaustion (rate limit, burst limit, daily/monthly quota, and
		// provider-side throttling are all indistinguishable from a bare
		// 429). Checked before the >=500 rule since 429 is itself a 4xx.
		if outcome.HTTPStatus == 429 {
			return CapacityFeedback{Kind: CapacityFeedbackCooldown, Duration: capacityCooldownHTTP429, Reason: "http_429"}
		}
		// Section 4.E: any other retryable 5xx gets the short cooldown.
		if outcome.HTTPStatus >= 500 && outcome.Retryable {
			return CapacityFeedback{Kind: CapacityFeedbackCooldown, Duration: capacityCooldownRetryable5xx, Reason: "retryable_5xx"}
		}
		// Section 4.G (see QUOTA_SIGNAL_NOT_CANONICAL_YET above) and
		// Section 4.H: every other rejection -- 401/403/404/400/422, a
		// non-retryable 5xx, a retryable-but-not-{429,5xx} status like 408
		// -- is a permanent-looking or otherwise unapproved-for-cooldown
		// failure. It must not silently convert a canonical candidate into
		// disabled-forever, and gets no cooldown either, absent an
		// explicit approved rule above.
		return CapacityFeedback{Kind: CapacityFeedbackNoop, Reason: "rejected_no_automatic_rule"}
	}
	return CapacityFeedback{Kind: CapacityFeedbackNoop, Reason: "unclassified_outcome"}
}
