#!/usr/bin/env bash
set -uo pipefail

# Behavioural evidence for migration 000044.
#
# 000008 enforced model-egress revision ownership with three CHECK constraints
# that read other tables. PostgreSQL documents that a CHECK must not do this,
# and the failure it warns about is the one that blocked a release: a CHECK is
# evaluated per row during COPY and cannot be deferred, so a dump of a serving
# database could not be restored. Four restore procedures were attempted and
# all four lost the same rows.
#
# session_replication_role = replica was evaluated as a workaround and
# discarded on evidence, not opinion: that mode suppresses triggers and, with
# them, foreign keys -- because PostgreSQL implements FKs as triggers -- but it
# does not suppress CHECK constraints. 101 of 103 tables restored; the two
# CHECK-guarded ones did not. A restore procedure assumed viable is not a
# restore procedure demonstrated viable.
#
# This script proves both halves of the fix: the ownership invariant still
# denies cross-organization revisions on all three surfaces, and a logical
# dump of the resulting schema restores completely. The restorability check
# stays even though the release itself rolls back via a physical base backup:
# it is the regression this migration exists to prevent, and dropping it would
# let the same defect return unnoticed.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

IMG='pgvector/pgvector@sha256:7ae6051efd0e60444282c27c7e141af07f322ce033300e727a49c3dd11075e38'
ADMIN=explorarte_admin
DB=explorarte_org
SRC=egress-fitness-src
DST=egress-fitness-dst
PORT=55470
FAILURES=0

trap 'docker rm -f "$SRC" "$DST" >/dev/null 2>&1' EXIT

ok()   { printf '  %-56s OK\n' "$1"; }
bad()  { printf '  %-56s FALLA: %s\n' "$1" "${2:-}"; FAILURES=$((FAILURES+1)); }
step() { printf '\n--- %s ---\n' "$*"; }

start_pg() {
  local name="$1" pub="${2:-}"
  docker rm -f "$name" >/dev/null 2>&1
  # shellcheck disable=SC2086
  docker run -d --name "$name" $pub -e POSTGRES_USER="$ADMIN" -e POSTGRES_PASSWORD=v -e POSTGRES_DB="$DB" "$IMG" >/dev/null 2>&1
  for _ in $(seq 1 40); do docker exec "$name" pg_isready -U "$ADMIN" -d "$DB" >/dev/null 2>&1 && break; sleep 2; done
  docker exec "$name" psql -U "$ADMIN" -d "$DB" -q -c 'CREATE EXTENSION IF NOT EXISTS vector;' >/dev/null 2>&1
}
q()   { docker exec -i "$1" psql -U "$ADMIN" -d "$DB" -t -A -q -c "$2" 2>&1; }
qf()  { docker exec -i "$1" psql -U "$ADMIN" -d "$DB" -q -v ON_ERROR_STOP=1 2>&1; }

step "1. esquema desde cero hasta 44"
start_pg "$SRC" "-p 127.0.0.1:$PORT:5432"
export ORG_DATABASE_HOST=127.0.0.1 ORG_DATABASE_PORT="$PORT" ORG_DATABASE_NAME="$DB" \
       ORG_DATABASE_USER="$ADMIN" ORG_DATABASE_PASSWORD=v ORG_DATABASE_SSLMODE=disable
go run ./cmd/orgctl migrate up >/dev/null 2>&1
TIP="$(q "$SRC" 'select max(version) from schema_migrations;')"
[[ "$TIP" == "44" ]] && ok "migracion 0 -> 44 (tip=$TIP)" || bad "migracion 0 -> 44" "tip=$TIP"

CHECKS="$(q "$SRC" "select count(*) from pg_constraint where conname ~ 'revision_owner_check';")"
TRIGS="$(q "$SRC" "select count(*) from pg_trigger where tgname ~ 'revision_owner' and not tgisinternal;")"
[[ "$CHECKS" == "0" ]] && ok "los 3 CHECK cross-table fueron eliminados" || bad "CHECK cross-table" "quedan $CHECKS"
[[ "$TRIGS" == "3" ]] && ok "3 constraint triggers diferibles creados" || bad "constraint triggers" "hay $TRIGS"

DEFERRED="$(q "$SRC" "select count(*) from pg_trigger where tgname ~ 'revision_owner' and not tgisinternal and tgdeferrable and tginitdeferred;")"
[[ "$DEFERRED" == "3" ]] && ok "los 3 son DEFERRABLE INITIALLY DEFERRED" || bad "deferimiento" "solo $DEFERRED"

step "2. down / reapply segun contrato de migraciones"
docker exec -i "$SRC" psql -U "$ADMIN" -d "$DB" -q -v ON_ERROR_STOP=1 < migrations/000044_make_egress_revision_ownership_restorable.down.sql >/dev/null 2>&1
BACK="$(q "$SRC" "select count(*) from pg_constraint where conname ~ 'revision_owner_check';")"
[[ "$BACK" == "3" ]] && ok "down restaura los 3 CHECK (inverso fiel)" || bad "down" "CHECKs=$BACK"
docker exec -i "$SRC" psql -U "$ADMIN" -d "$DB" -q -v ON_ERROR_STOP=1 < migrations/000044_make_egress_revision_ownership_restorable.up.sql >/dev/null 2>&1
AGAIN="$(q "$SRC" "select count(*) from pg_trigger where tgname ~ 'revision_owner' and not tgisinternal;")"
[[ "$AGAIN" == "3" ]] && ok "reapply vuelve a los triggers" || bad "reapply" "triggers=$AGAIN"

step "3. semilla: datos reales de produccion"
docker exec explorarte-organization-postgres-1 pg_dump -U "$ADMIN" -d "$DB" --data-only > /tmp/egress-seed.sql 2>/dev/null
{ echo 'SET session_replication_role = replica;'; cat /tmp/egress-seed.sql; echo 'SET session_replication_role = origin;'; } \
  | docker exec -i "$SRC" psql -U "$ADMIN" -d "$DB" -v ON_ERROR_STOP=0 -q >/dev/null 2>/tmp/seed-out.txt
ORG="$(q "$SRC" "select id from organizations limit 1;")"
REV="$(q "$SRC" "select current_revision_id from organizations where id = '$ORG';")"
[[ -n "$ORG" && -n "$REV" ]] && ok "semilla real cargada (org=$ORG revision=$REV)" \
  || bad "semilla" "org=$ORG rev=$REV :: $(grep -m1 -i error /tmp/seed-out.txt 2>/dev/null)"

# A registry revision owned by nobody. organization_registry_revisions has no
# organization_id column, so this row is well-formed and satisfies every
# foreign key while failing the ownership predicate -- which isolates the
# invariant under test from unrelated constraints.
ORPHAN="$(q "$SRC" "insert into organization_registry_revisions (canonical_hash, status, schema_versions, document_hashes, counts, diff, applied_at) values (repeat('d',64),'applied','{}','{}','{}','{}',now()) returning id;")"
[[ "$ORPHAN" =~ ^[0-9]+$ ]] && ok "revision huerfana creada (id=$ORPHAN)" || bad "revision huerfana" "$ORPHAN"

step "4. invariante de propiedad conservada"
VALID="$(q "$SRC" "insert into model_egress_policy_versions (organization_id, policy_id, policy_version, canonical_hash, introduced_by_organization_revision_id, status) values ('$ORG','fitness.valid',9001,repeat('c',64),$REV,'materializing') returning id;")"
[[ "$VALID" =~ ^[0-9]+$ ]] && ok "revision de la propia organizacion: ACEPTADA" || bad "same-org policy_version" "$(printf '%s' "$VALID" | head -1)"

CROSS1="$(q "$SRC" "insert into model_egress_policy_versions (organization_id, policy_id, policy_version, canonical_hash, introduced_by_organization_revision_id, status) values ('$ORG','fitness.cross',9002,repeat('d',64),$ORPHAN,'materializing');")"
[[ "$CROSS1" == *"does not belong to organization"* ]] && ok "cross-org en policy_versions: DENEGADA" || bad "cross-org policy_versions" "$(printf '%s' "$CROSS1" | head -1)"

CROSS2="$(q "$SRC" "insert into model_egress_revision_bindings (organization_id, organization_revision_id, policy_version_id, canonical_hash) values ('$ORG',$ORPHAN,$VALID,repeat('c',64));")"
[[ "$CROSS2" == *"does not belong to organization"* ]] && ok "cross-org en revision_bindings: DENEGADA" || bad "cross-org revision_bindings" "$(printf '%s' "$CROSS2" | head -1)"

# model_egress_evaluations carries five non-deferrable foreign keys to an
# invocation, a dispatch attempt and a profile version. Building that chain
# would test plumbing this migration does not touch, and a failure on any of
# those keys would make the assertion pass for the wrong reason. Dropping them
# in the disposable fixture isolates the constraint actually under test, so a
# denial here can only come from the ownership trigger.
q "$SRC" "alter table model_egress_evaluations drop constraint model_egress_evaluations_invocation_fk, drop constraint model_egress_evaluations_attempt_fk, drop constraint model_egress_evaluations_profile_fk, drop constraint model_egress_evaluations_policy_fk;" >/dev/null
CROSS3="$(q "$SRC" "insert into model_egress_evaluations (invocation_id, dispatch_attempt_id, policy_version_id, organization_id, organization_revision_id, model_profile_version_id, provider_id, provider_transport, action_digest, capability_matrix_hash, context_classifications, context_classifications_hash, authorization_effect, authorization_reason_code, egress_effect, egress_reason_codes, decision_hash) values (1,1,$VALID,'$ORG',$ORPHAN,1,'deepseek','http_adapter',repeat('f',64),repeat('0',64),jsonb_build_array('public'),repeat('1',64),'allow','ok','allow',jsonb_build_array('ok'),repeat('2',64));")"
[[ "$CROSS3" == *"does not belong to organization"* ]] && ok "cross-org en evaluations: DENEGADA" || bad "cross-org evaluations" "$(printf '%s' "$CROSS3" | head -1)"

step "5. comportamiento diferido"
# The point of deferring: inside one transaction the state may be transiently
# invalid while rows are still being loaded, and validation happens at COMMIT.
# Here the policy version is inserted before the unit that makes its revision
# legitimate exists -- the ordering a restore produces.
LATE="$(q "$SRC" "begin; insert into model_egress_policy_versions (organization_id,policy_id,policy_version,canonical_hash,introduced_by_organization_revision_id,status) values ('$ORG','fitness.deferred',9003,repeat('7',64),$ORPHAN,'materializing'); update organizations set current_revision_id = $ORPHAN where id = '$ORG'; commit; select 'commit-ok';")"
[[ "$LATE" == *"commit-ok"* ]] && ok "estado transitorio invalido, valido al COMMIT: ACEPTADO" || bad "deferido valido" "$(printf '%s' "$LATE" | head -1)"

ORPHAN2="$(q "$SRC" "insert into organization_registry_revisions (canonical_hash, status, schema_versions, document_hashes, counts, diff, applied_at) values (repeat('a',64),'applied','{}','{}','{}','{}',now()) returning id;")"
BADCOMMIT="$(q "$SRC" "insert into model_egress_policy_versions (organization_id,policy_id,policy_version,canonical_hash,introduced_by_organization_revision_id,status) values ('$ORG','fitness.bad',9004,repeat('8',64),$ORPHAN2,'materializing');")"
[[ "$BADCOMMIT" == *"does not belong to organization"* ]] && ok "invariante violada al COMMIT: DENEGADA" || bad "deferido invalido" "$(printf '%s' "$BADCOMMIT" | head -1)"

step "6. seguridad existente intacta"
IMMUT="$(q "$SRC" "update model_egress_policy_versions set canonical_hash = repeat('9',64) where id = $VALID;")"
[[ "$IMMUT" == *ERROR* ]] && ok "trigger de inmutabilidad sigue activo" || bad "inmutabilidad" "el UPDATE fue aceptado"
FKS="$(q "$SRC" "select count(*) from pg_constraint where contype='f' and connamespace='public'::regnamespace;")"
[[ "$FKS" -gt 200 ]] && ok "FK del esquema presentes ($FKS)" || bad "FK" "solo $FKS"

step "7. restaurabilidad: dump logico -> base limpia -> comparacion"
docker exec "$SRC" pg_dump -U "$ADMIN" -d "$DB" > /tmp/egress-fitness.sql 2>/dev/null
start_pg "$DST"
docker exec "$DST" psql -U "$ADMIN" -d "$DB" -q -c 'CREATE ROLE explorarte_app LOGIN;' >/dev/null 2>&1
{ echo 'SET session_replication_role = replica;'; cat /tmp/egress-fitness.sql; echo 'SET session_replication_role = origin;'; } \
  | docker exec -i "$DST" psql -U "$ADMIN" -d "$DB" -v ON_ERROR_STOP=0 -q >/dev/null 2>/tmp/egress-restore-err.txt
ERRS="$(grep -ci '^ERROR' /tmp/egress-restore-err.txt | tr -d '[:space:]')"
ERRS="${ERRS:-0}"
[[ "$ERRS" == "0" ]] && ok "restore sin errores" || bad "restore" "$ERRS errores: $(grep -m1 '^ERROR' /tmp/egress-restore-err.txt)"

cat > /tmp/egress-counts.sql <<'SQL'
SELECT string_agg(format('%s=%s', tablename, cnt), E'\n' ORDER BY tablename)
FROM (
  SELECT c.relname AS tablename,
         (xpath('/row/c/text()', query_to_xml(format('select count(*) as c from public.%I', c.relname), false, true, '')))[1]::text::bigint AS cnt
  FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
  WHERE n.nspname='public' AND c.relkind='r'
) t;
SQL
for c in "$SRC" "$DST"; do docker cp /tmp/egress-counts.sql "$c":/tmp/egress-counts.sql >/dev/null; done
docker exec "$SRC" psql -U "$ADMIN" -d "$DB" -t -A -f /tmp/egress-counts.sql | grep '=' | sort > /tmp/eg-src.txt
docker exec "$DST" psql -U "$ADMIN" -d "$DB" -t -A -f /tmp/egress-counts.sql | grep '=' | sort > /tmp/eg-dst.txt
if diff -q /tmp/eg-src.txt /tmp/eg-dst.txt >/dev/null; then
  ok "las $(wc -l < /tmp/eg-src.txt) tablas coinciden tras el restore"
else
  bad "comparacion de tablas" "$(diff /tmp/eg-src.txt /tmp/eg-dst.txt | head -4 | tr '\n' ' ')"
fi

step "8. mutacion: neutralizar el trigger de propiedad"
docker exec "$SRC" psql -U "$ADMIN" -d "$DB" -q -c 'DROP TRIGGER model_egress_policy_versions_revision_owner ON model_egress_policy_versions;' >/dev/null 2>&1
MUT="$(q "$SRC" "insert into model_egress_policy_versions (organization_id, policy_id, policy_version, canonical_hash, introduced_by_organization_revision_id, status) values ('$ORG','fitness.mut',9099,repeat('5',64),$ORPHAN2,'materializing') returning id;")"
if [[ "$MUT" =~ ^[0-9]+$ ]]; then
  ok "mutacion detectada: sin el trigger, la fila cross-org sobrevive"
else
  bad "mutacion" "el insert cross-org siguio fallando sin el trigger; el fitness no demuestra nada"
fi

step "RESULTADO"
printf '  comprobaciones fallidas: %d\n' "$FAILURES"
[[ $FAILURES -eq 0 ]] && echo "  egress-restorability fitness: PASS" || echo "  egress-restorability fitness: FAIL"
exit "$FAILURES"
