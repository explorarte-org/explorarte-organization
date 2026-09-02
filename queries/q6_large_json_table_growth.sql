BEGIN READ ONLY;
-- Wave 5 (large JSON-payload table retention investigation): watches real
-- growth of the two tables reports/database-audit.md flagged as large-by-
-- disk despite low row counts (model_invocation_inputs,
-- execution_context_views). Both are deliberately immutable (see
-- WAVE5-JSON-RETENTION-FINDINGS.md) -- this query is diagnostic only, it
-- never proposes or performs any deletion.
SELECT
    'model_invocation_inputs' AS table_name,
    count(*) AS rows,
    pg_size_pretty(pg_total_relation_size('model_invocation_inputs')) AS total_size,
    pg_size_pretty(avg(octet_length(canonical_bytes))::bigint) AS avg_row_bytes,
    min(created_at) AS oldest,
    max(created_at) AS newest
FROM model_invocation_inputs
UNION ALL
SELECT
    'execution_context_views',
    count(*),
    pg_size_pretty(pg_total_relation_size('execution_context_views')),
    pg_size_pretty(avg(octet_length(provider_visible_bytes))::bigint),
    min(created_at),
    max(created_at)
FROM execution_context_views;
ROLLBACK;
