-- Reverses migration 000081: restores the unconditional trigger from
-- migration 000011, rejecting every UPDATE again including the one this
-- migration permitted. Safe to reverse -- it only widens what BLOCKS a
-- write; no data this migration itself wrote needs to be undone.
CREATE OR REPLACE FUNCTION reject_model_provider_outcome_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'model provider outcomes are immutable';
END;
$$;
