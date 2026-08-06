DROP TRIGGER IF EXISTS improvement_promotion_decisions_immutable ON improvement_promotion_decisions;
DROP TRIGGER IF EXISTS improvement_candidates_update_guard ON improvement_candidates;
DROP FUNCTION IF EXISTS improvement_immutable_row();
DROP FUNCTION IF EXISTS improvement_guard_candidate_update();
DROP TABLE IF EXISTS improvement_promotion_decisions;
DROP TABLE IF EXISTS improvement_candidates;
