DROP TRIGGER IF EXISTS context_segments_no_update ON context_segments;
DROP FUNCTION IF EXISTS reject_context_segment_mutation();
DROP TABLE IF EXISTS context_segments;
DROP TABLE IF EXISTS context_snapshots;
