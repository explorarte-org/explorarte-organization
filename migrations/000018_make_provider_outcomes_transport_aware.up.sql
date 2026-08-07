ALTER TABLE model_provider_outcomes
    ADD COLUMN transport TEXT NOT NULL DEFAULT 'http_adapter',
    ADD COLUMN process_exit_code INTEGER;

-- 000011 made outcomes immutable. Temporarily remove only the mutation
-- trigger while this migration backfills transport metadata, then restore it
-- before the migration completes.
DROP TRIGGER model_provider_outcomes_no_mutation ON model_provider_outcomes;

-- Backfill from the immutable provider request rather than assuming every
-- historical row was HTTP. This also keeps test.fake evidence honest.
UPDATE model_provider_outcomes o
SET transport = CASE
    WHEN r.provider_id = 'alibaba_token_plan_via_claude_code'
         AND r.adapter_id = 'alibaba_claude_code_print' THEN 'cli_adapter'
    WHEN r.provider_id = 'test.fake'
         AND r.adapter_id = 'fake' THEN 'fake_adapter'
    ELSE 'http_adapter'
END,
process_exit_code = CASE
    WHEN r.provider_id = 'alibaba_token_plan_via_claude_code'
         AND r.adapter_id = 'alibaba_claude_code_print'
         AND o.outcome_classification = 'response_received' THEN 0
    WHEN r.provider_id = 'alibaba_token_plan_via_claude_code'
         AND r.adapter_id = 'alibaba_claude_code_print'
         AND o.error_code ~ '^process_exit_([0-9]{1,3})$'
         AND substring(o.error_code FROM '^process_exit_([0-9]{1,3})$')::INTEGER BETWEEN 0 AND 255
      THEN substring(o.error_code FROM '^process_exit_([0-9]{1,3})$')::INTEGER
    ELSE NULL
END
FROM model_provider_requests r
WHERE r.id = o.provider_request_record_id;

CREATE TRIGGER model_provider_outcomes_no_mutation
BEFORE UPDATE OR DELETE ON model_provider_outcomes
FOR EACH ROW EXECUTE FUNCTION reject_model_provider_outcome_mutation();

ALTER TABLE model_provider_outcomes ALTER COLUMN transport DROP DEFAULT;

-- 000011 used unnamed CHECK constraints. Replace them atomically with named,
-- transport-aware constraints so a CLI success is never represented as fake
-- HTTP 200 evidence.
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
    ADD CONSTRAINT model_provider_outcomes_classification_check CHECK (
        outcome_classification IN (
            'response_received',
            'provider_rejected',
            'request_not_sent',
            'ambiguous_transport',
            'cancelled_confirmed'
        )
    ),
    ADD CONSTRAINT model_provider_outcomes_transport_check CHECK (
        transport IN ('http_adapter', 'cli_adapter', 'fake_adapter')
    ),
    ADD CONSTRAINT model_provider_outcomes_http_status_check CHECK (
        http_status IS NULL OR http_status BETWEEN 100 AND 599
    ),
    ADD CONSTRAINT model_provider_outcomes_process_exit_check CHECK (
        process_exit_code IS NULL OR process_exit_code BETWEEN 0 AND 255
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
    ADD CONSTRAINT model_provider_outcomes_transport_fields_check CHECK (
        (transport <> 'http_adapter' OR process_exit_code IS NULL)
        AND (transport <> 'cli_adapter' OR http_status IS NULL)
    ),
    ADD CONSTRAINT model_provider_outcomes_success_check CHECK (
        outcome_classification <> 'response_received'
        OR (
            response_hash IS NOT NULL
            AND retryable = FALSE
            AND cancellation_confirmed = FALSE
            AND (
                (transport = 'http_adapter' AND http_status BETWEEN 200 AND 299 AND process_exit_code IS NULL)
                OR (transport = 'cli_adapter' AND http_status IS NULL AND process_exit_code = 0)
                OR (transport = 'fake_adapter')
            )
        )
    ),
    ADD CONSTRAINT model_provider_outcomes_rejected_check CHECK (
        outcome_classification <> 'provider_rejected'
        OR (
            response_hash IS NOT NULL
            AND error_code IS NOT NULL
            AND cancellation_confirmed = FALSE
            AND (
                (transport = 'http_adapter' AND http_status IS NOT NULL AND process_exit_code IS NULL)
                OR (transport = 'cli_adapter' AND http_status IS NULL AND process_exit_code IS NOT NULL)
                OR (transport = 'fake_adapter')
            )
        )
    ),
    ADD CONSTRAINT model_provider_outcomes_not_sent_check CHECK (
        outcome_classification <> 'request_not_sent'
        OR (
            http_status IS NULL
            AND process_exit_code IS NULL
            AND response_hash IS NULL
            AND provider_request_id IS NULL
            AND error_code IS NOT NULL
            AND cancellation_confirmed = FALSE
        )
    ),
    ADD CONSTRAINT model_provider_outcomes_ambiguous_check CHECK (
        outcome_classification <> 'ambiguous_transport'
        OR (
            http_status IS NULL
            AND response_hash IS NULL
            AND error_code IS NOT NULL
            AND cancellation_confirmed = FALSE
        )
    ),
    ADD CONSTRAINT model_provider_outcomes_cancelled_check CHECK (
        outcome_classification <> 'cancelled_confirmed'
        OR (cancellation_confirmed AND retryable = FALSE)
    );

CREATE FUNCTION set_model_provider_outcome_transport()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    request_provider TEXT;
    request_adapter TEXT;
    parsed_exit_code INTEGER;
BEGIN
    SELECT provider_id, adapter_id
      INTO request_provider, request_adapter
      FROM model_provider_requests
     WHERE id = NEW.provider_request_record_id;

    IF NOT FOUND THEN
        RAISE EXCEPTION 'provider request record % not found', NEW.provider_request_record_id;
    END IF;

    NEW.transport := CASE
        WHEN request_provider = 'alibaba_token_plan_via_claude_code'
             AND request_adapter = 'alibaba_claude_code_print' THEN 'cli_adapter'
        WHEN request_provider = 'test.fake'
             AND request_adapter = 'fake' THEN 'fake_adapter'
        ELSE 'http_adapter'
    END;

    IF NEW.transport = 'cli_adapter' THEN
        -- response_received is emitted only after Wait returned exit code 0.
        IF NEW.outcome_classification = 'response_received' THEN
            NEW.process_exit_code := 0;
        ELSIF NEW.error_code ~ '^process_exit_([0-9]{1,3})$' THEN
            parsed_exit_code := substring(NEW.error_code FROM '^process_exit_([0-9]{1,3})$')::INTEGER;
            IF parsed_exit_code BETWEEN 0 AND 255 THEN
                NEW.process_exit_code := parsed_exit_code;
            END IF;
        END IF;
    END IF;

    RETURN NEW;
END;
$$;

CREATE TRIGGER model_provider_outcomes_transport_derivation
BEFORE INSERT ON model_provider_outcomes
FOR EACH ROW EXECUTE FUNCTION set_model_provider_outcome_transport();
