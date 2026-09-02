BEGIN READ ONLY;
-- Corrected version of q4_missing_fk_indexes.sql: evaluates FK coverage per
-- CONSTRAINT (not per column), and requires an index whose LEADING columns
-- (in order) exactly match the FK's columns (in order) -- the only shape
-- that actually helps a typical FK-lookup/join query plan. The original
-- query's `a.attnum = any(i.indkey)` matched a column appearing ANYWHERE in
-- any index, including as a non-leading column of an unrelated composite
-- index or inside a partial/expression index that doesn't cover the FK's
-- own lookup pattern -- producing false positives (flagged as "indexed"
-- when it isn't usefully so) and, in the composite-FK case, also
-- undercounting real gaps by checking columns independently instead of as
-- an ordered set.
with fk_cols as (
  select
    c.oid as con_oid,
    c.conname,
    c.conrelid,
    c.conrelid::regclass as table_name,
    array_agg(a.attname order by ord.n) as fk_columns,
    array_agg(k.attnum order by ord.n) as fk_attnums
  from pg_constraint c
  join lateral unnest(c.conkey) with ordinality as ord(attnum, n) on true
  join pg_attribute a on a.attrelid = c.conrelid and a.attnum = ord.attnum
  join lateral (select ord.attnum) as k(attnum) on true
  where c.contype = 'f'
  group by c.oid, c.conname, c.conrelid
),
covering_indexes as (
  select
    i.indrelid,
    i.indexrelid,
    -- leading key columns only (indkey includes included/non-key columns
    -- for covering indexes too; restrict to the first indnkeyatts, which
    -- are the actual key columns in order)
    (i.indkey::int2[])[1:i.indnkeyatts] as leading_attnums
  from pg_index i
  where i.indpred is null  -- exclude partial indexes: they don't cover all rows, so they can't be relied on for general FK-lookup coverage
)
select
  fk.conname,
  fk.table_name,
  fk.fk_columns
from fk_cols fk
where not exists (
  select 1
  from covering_indexes ci
  where ci.indrelid = fk.conrelid
    and array_length(ci.leading_attnums, 1) >= array_length(fk.fk_attnums, 1)
    and ci.leading_attnums[1:array_length(fk.fk_attnums,1)] = fk.fk_attnums
)
order by fk.table_name, fk.conname;
ROLLBACK;
