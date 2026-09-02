# G5-002 — Corrected FK-index coverage query

The original `queries/q4_missing_fk_indexes.sql` checked FK coverage per-column
(`a.attnum = any(i.indkey)`), which matches a column appearing anywhere in any
index -- including as a non-leading column of an unrelated composite index, or
inside a partial index that doesn't cover the FK's own lookup pattern. It also
evaluated multi-column FKs one column at a time instead of as an ordered set,
which is not the semantic that actually predicts whether a typical
`WHERE fk_col = X` / cascade-delete lookup can use an index.

`queries/q5_missing_fk_indexes_corrected.sql` fixes this: it groups each FK
constraint's columns (in declared order), excludes partial indexes, and
requires an index whose **leading** key columns exactly match the FK's
columns in order -- the only shape a B-tree index actually helps for this
access pattern.

**Result: 206 FK constraints have no such covering index** (vs. the original
query's 131, which undercounted composite FKs it happened to check by
individual column). This is a larger number, not smaller -- but per this
finding's own explicit `do_not_fix_with` guidance ("Do not add indexes
speculatively based on the current list -- most are likely noise"), **no
migration is included here**.

## Spot-check on the 3 originally-named candidates

`EXPLAIN` against real production data for the finding's own named examples:

```
EXPLAIN SELECT * FROM tasks WHERE organization_revision_id = 12;
 Seq Scan on tasks  (cost=0.00..33.75 rows=11 width=948)

EXPLAIN SELECT * FROM model_invocations WHERE organization_revision_id = 12;
 Seq Scan on model_invocations  (cost=0.00..28.84 rows=8 width=1156)

EXPLAIN SELECT * FROM agent_budgets WHERE organization_id = 'explorarte';
 Seq Scan on agent_budgets  (cost=0.00..1.32 rows=26 width=191)
```

All three genuinely lack a useful index and the planner falls back to a
sequential scan -- confirming these ARE real gaps, not query artifacts. But
at current table sizes (8-26 rows for these three; the whole schema's
largest table is ~2.8K rows per the original audit), a seq scan is the
*correct, cheaper* plan regardless of whether an index exists. This is not
evidence of a current performance problem.

## Recommendation (not applied)

Revisit this once any of these tables' row counts grow into the tens-of-
thousands range (worth an operator alert/periodic check, not a one-time
fix). At that point, use `queries/q5_missing_fk_indexes_corrected.sql`'s
206-row output as the real candidate list (filtered to whichever tables have
actually grown), confirm each with `EXPLAIN ANALYZE` under real query
patterns before adding any index -- an unused index has a real write-path
cost, so "FK exists" alone is not sufficient justification.
