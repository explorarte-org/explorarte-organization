DROP TRIGGER IF EXISTS model_invocation_inputs_immutable ON model_invocation_inputs;
DROP FUNCTION IF EXISTS reject_model_invocation_input_mutation();
DROP TABLE IF EXISTS model_invocation_inputs;
ALTER TABLE model_invocations DROP CONSTRAINT IF EXISTS model_invocations_id_context_unique;
