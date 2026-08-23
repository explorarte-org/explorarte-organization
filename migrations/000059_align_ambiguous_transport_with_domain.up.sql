-- AUTONOMY-SMOKE-017-R1 died on a transient transport failure it already knew
-- how to survive.
--
-- DeepSeek began answering a department review and the body stopped arriving.
-- modelruntime.IncompleteReadOutcome describes that state deliberately: the
-- classification is ambiguous_transport, and it KEEPS what was actually
-- observed -- the HTTP status, the provider request ID, and the hash of the
-- partial body -- because the call may already have been billed and the
-- caller must be able to say so later. ProviderOutcome.Validate accepts
-- exactly that object, and documents why the older, stricter rule was wrong.
--
-- This constraint was never told. It still required an ambiguous outcome to
-- carry no status and no hash, which is the semantics of request_not_sent,
-- not of ambiguity. So the write failed with 23514, the invocation could not
-- leave send_started, the attempt was recorded as not retryable, and a
-- recoverable hiccup ended the campaign. The outcome that most needed to be
-- durable was the one the schema refused.
--
-- This is domain-persistence contract drift, not a change of retry policy.
-- The domain is the correct side and is left untouched; the schema is brought
-- to it.
--
-- What stays true of an ambiguous outcome: it must name why it is ambiguous,
-- and it must not claim a confirmed cancellation -- a confirmed cancellation
-- is knowledge, and knowing is the opposite of this classification. The
-- general column constraints still hold: a status that is present is between
-- 100 and 599, and a hash that is present is a valid SHA-256.
--
-- request_not_sent is deliberately NOT relaxed. Its rule really does mean
-- "we learned nothing", and the defect here was the schema applying that
-- meaning to a different classification.

ALTER TABLE model_provider_outcomes DROP CONSTRAINT model_provider_outcomes_ambiguous_check;

ALTER TABLE model_provider_outcomes ADD CONSTRAINT model_provider_outcomes_ambiguous_check
    CHECK (
        outcome_classification <> 'ambiguous_transport'
        OR (error_code IS NOT NULL AND cancellation_confirmed = FALSE)
    );
