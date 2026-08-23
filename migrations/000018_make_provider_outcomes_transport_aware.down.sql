DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM model_provider_outcomes WHERE transport = 'cli_adapter'
    ) THEN
        RAISE EXCEPTION 'cannot roll back 000018 while CLI provider outcome evidence exists';
    END IF;
END;
$$;

DROP TRIGGER model_provider_outcomes_transport_derivation ON model_provider_outcomes;
DROP FUNCTION set_model_provider_outcome_transport();

DO $$
DECLARE c RECORD;
BEGIN
    FOR c IN
        SELECT conname
        FROM pg_constraint
        WHERE conrelid = 'model_provider_outcomes'::regclass
          AND contype = 'c'
    LOOP
        EXECUTE format('ALTER TABLE model_provider_outcomes DROP CONSTRAINT %I', c.conname);
    END LOOP;
END;
$$;

ALTER TABLE model_provider_outcomes
    DROP COLUMN process_exit_code,
    DROP COLUMN transport;

ALTER TABLE model_provider_outcomes
    ADD CONSTRAINT model_provider_outcomes_classification_check CHECK (
        outcome_classification IN (
            'response_received',
            'provider_rejected',
            'request_not_sent',
            'ambiguous_transport',
            'cancelled_confirmed'
        )
    ),
    ADD CONSTRAINT model_provider_outcomes_http_status_check CHECK (
        http_status IS NULL OR http_status BETWEEN 100 AND 599
    ),
    ADD CONSTRAINT model_provider_outcomes_provider_request_id_check CHECK (
        provider_request_id IS NULL OR length(provider_request_id) <= 400
    ),
    ADD CONSTRAINT model_provider_outcomes_error_class_check CHECK (
        error_class IS NULL OR length(trim(error_class)) BETWEEN 1 AND 120
    ),
    ADD CONSTRAINT model_provider_outcomes_error_code_check CHECK (
        error_code IS NULL OR length(trim(error_code)) BETWEEN 1 AND 160
    ),
    ADD CONSTRAINT model_provider_outcomes_response_hash_check CHECK (
        response_hash IS NULL OR response_hash ~ '^[0-9a-f]{64}$'
    ),
    ADD CONSTRAINT model_provider_outcomes_response_schema_check CHECK (
        length(trim(response_schema_version)) BETWEEN 1 AND 120
    ),
    ADD CONSTRAINT model_provider_outcomes_success_check CHECK (
        outcome_classification <> 'response_received'
        OR (http_status BETWEEN 200 AND 299 AND response_hash IS NOT NULL AND retryable = FALSE AND cancellation_confirmed = FALSE)
    ),
    ADD CONSTRAINT model_provider_outcomes_rejected_check CHECK (
        outcome_classification <> 'provider_rejected'
        OR (http_status IS NOT NULL AND response_hash IS NOT NULL AND error_code IS NOT NULL AND cancellation_confirmed = FALSE)
    ),
    ADD CONSTRAINT model_provider_outcomes_not_sent_check CHECK (
        outcome_classification <> 'request_not_sent'
        OR (http_status IS NULL AND response_hash IS NULL AND provider_request_id IS NULL AND error_code IS NOT NULL AND cancellation_confirmed = FALSE)
    ),
    -- NOT VALID since 000059: this rule describes an ambiguous outcome as
    -- one that learned nothing, which is the meaning of request_not_sent.
    -- An incomplete read keeps the status and the hash of the partial body,
    -- and by the time this rollback can run such rows legitimately exist.
    -- Validating would fail on exactly the observations that most need to
    -- survive -- a call that may already have been billed -- so the old rule
    -- governs new writes again and the record is kept.
    ADD CONSTRAINT model_provider_outcomes_ambiguous_check CHECK (
        outcome_classification <> 'ambiguous_transport'
        OR (http_status IS NULL AND response_hash IS NULL AND error_code IS NOT NULL AND cancellation_confirmed = FALSE)
    ) NOT VALID,
    ADD CONSTRAINT model_provider_outcomes_cancelled_check CHECK (
        outcome_classification <> 'cancelled_confirmed'
        OR (cancellation_confirmed AND retryable = FALSE)
    );
