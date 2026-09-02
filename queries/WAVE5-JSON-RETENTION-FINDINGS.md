# Wave 5 — Retention policy for large JSON-payload tables (investigation)

`reports/database-audit.md` flagged `model_invocation_inputs` and
`execution_context_views` as the two largest tables by disk despite low row
counts, with no pruning/retention/archival mechanism found or ruled out, and
marked the growth question `IMPLEMENTED_NOT_PROVEN`.

## What this investigation found

Both tables are **deliberately, unconditionally immutable by design**, not by
oversight:

```sql
-- migrations/000049_create_model_invocation_inputs.up.sql
CREATE TRIGGER model_invocation_inputs_immutable
BEFORE UPDATE OR DELETE ON model_invocation_inputs
FOR EACH ROW EXECUTE FUNCTION reject_model_invocation_input_mutation();

-- migrations/000051_create_execution_context_views.up.sql
CREATE TRIGGER execution_context_views_immutable
BEFORE UPDATE OR DELETE ON execution_context_views
FOR EACH ROW EXECUTE FUNCTION reject_execution_context_view_mutation();
```

Both triggers reject every `UPDATE` and `DELETE` unconditionally -- there is
no age-based or status-based exception built in anywhere.
`model_invocation_inputs` holds the canonical bytes actually sent to a model
provider for a given invocation; `execution_context_views` holds the exact
provider-visible context render. Both exist specifically so a past model
call's real input is reconstructable later, matching this project's broader
Gate F telemetry philosophy (preserve evidence, never silently discard).

**This means a retention/pruning tool in the shape of `orgctl outbox prune`
(G5-001) cannot be built for these two tables without first removing a
real, deliberately-installed integrity guarantee.** That is a materially
different and higher-stakes decision than G5-001's outbox retention (a
transactional-outbox pattern with no proven consumer) -- this is confirmed,
actively-used audit evidence.

## Real current growth, measured (not assumed)

Read against production on 2026-09-02:

| table | rows | total size | avg row size | oldest | newest |
|---|---|---|---|---|---|
| `model_invocation_inputs` | 145 | 9648 kB | 127 kB | 2026-08-30 19:03 | 2026-09-02 16:09 |
| `execution_context_views` | 145 | 9440 kB | 124 kB | 2026-08-30 19:03 | 2026-09-02 16:09 |

All 145 rows in each table were created within this project's first ~4 days
of real R17 activity (this session's own audit/remediation work). At this
rate (~36 rows/day, ~125 KB/row), a full year of continuous operation at the
same pace projects to roughly 13,000 rows and ~1.6 GB per table -- not an
acute problem today, but genuinely unbounded: nothing currently caps it, and
the per-row ceiling is 8 MB
(`CHECK (octet_length(...) BETWEEN 1 AND 8388608)`), so the real ceiling on
total growth is entirely usage volume, indefinitely.

## Recommendation

**Do not build an automated deletion mechanism against either table without
an explicit owner decision**, for the same reason G5-001's outbox intent
required one: unlike outbox events, these rows are proven, actively-relied-on
evidence (this session's own postmortems have read `model_invocation_inputs`
rows directly), and the immutability trigger is a real security/audit
property, not an accident to route around.

If growth is ever judged to need bounding, the two safe shapes -- requiring
their own future engagement, not decided here -- are:

1. **Archive-then-truncate by partition.** Convert both tables to be
   range-partitioned by `created_at` (a real, disruptive schema migration,
   not a config change) so whole partitions can be detached and moved to
   cold storage (e.g. `internal/objectstorage`, already proven for the RAG
   corpus) as a unit, with the detach step itself durably logged.
2. **Time-boxed superuser archival job**, run outside the application's own
   role (the immutability trigger fires for the `explorarte_app` role same
   as everyone else, by design) with its own explicit audit trail of what
   was archived and where.

Both require an owner decision on retention window and archival destination
before any implementation; this document does not choose one.

## What this investigation adds today

`queries/q6_large_json_table_growth.sql`: a read-only query an operator can
re-run periodically to watch real growth against the projection above,
without needing to reconstruct the query from scratch.
