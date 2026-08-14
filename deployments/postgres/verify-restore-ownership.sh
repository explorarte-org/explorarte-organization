#!/usr/bin/env bash
# Verifica que un restore de pg_dump/pg_restore contra una base de datos
# PREEXISTENTE dejó el schema public y sus objetos con el owner/grants
# correctos para el rol de aplicación, antes de arrancar orgd/model-worker.
#
# CUTOVER-REHEARSAL-002: un restore real contra producción hizo
# DROP SCHEMA public CASCADE; CREATE SCHEMA public; -- la nueva schema
# quedó con owner = el rol admin que ejecutó el comando (explorarte_admin),
# no con el rol de aplicación (explorarte_app), porque CREATE SCHEMA sin
# AUTHORIZATION explícita usa siempre el rol actual, no el owner histórico
# de la base de datos. explorarte_app pudo entonces SER dueño de cada
# tabla individualmente (pg_restore --no-owner + reasignación de
# ownership por objeto) pero no tenía USAGE sobre el schema contenedor,
# así que current_schema() resolvía vacío y cualquier lookup sin calificar
# fallaba con "relation ... does not exist" -- un fallo que solo aparece
# al conectar como explorarte_app, nunca como el admin que hizo el
# restore, por lo que pasó inadvertido hasta que orgd intentó arrancar.
#
# Este script existe para que ese fallo nunca vuelva a descubrirse
# manualmente en el momento del cutover: correrlo es un paso obligatorio
# del runbook de restore contra una base existente (ver
# RUNBOOK-restore-against-existing-database.md), no opcional.
#
# Uso:
#   ./verify-restore-ownership.sh <contenedor_postgres> <admin_user> <app_user> <database>
#
# Sale con código != 0 y un mensaje explícito si cualquier verificación
# falla. No modifica nada -- solo lee.

set -euo pipefail

if [ "$#" -ne 4 ]; then
  echo "uso: $0 <contenedor_postgres> <admin_user> <app_user> <database>" >&2
  exit 2
fi

CONTAINER="$1"
ADMIN_USER="$2"
APP_USER="$3"
DATABASE="$4"

fail=0

check() {
  local description="$1"
  local query="$2"
  local expect="$3"
  local got
  got=$(docker exec -i "$CONTAINER" psql -U "$ADMIN_USER" -d "$DATABASE" -tA -c "$query" 2>&1) || {
    echo "FALLO: $description -- query error: $got" >&2
    fail=1
    return
  }
  got=$(echo "$got" | xargs) # trim whitespace
  if [ "$got" != "$expect" ]; then
    echo "FALLO: $description -- esperado '$expect', obtenido '$got'" >&2
    fail=1
  else
    echo "OK: $description ($got)"
  fi
}

echo "=== verify-restore-ownership: $CONTAINER / $DATABASE / app_user=$APP_USER ==="

check "public schema owner es $APP_USER" \
  "SELECT nspowner::regrole::text FROM pg_namespace WHERE nspname='public';" \
  "$APP_USER"

check "$APP_USER tiene USAGE sobre public" \
  "SELECT has_schema_privilege('$APP_USER', 'public', 'USAGE')::text;" \
  "true"

check "$APP_USER tiene CREATE sobre public" \
  "SELECT has_schema_privilege('$APP_USER', 'public', 'CREATE')::text;" \
  "true"

# Prueba end-to-end real: conectar COMO el rol de aplicación (no el admin)
# y resolver una tabla central sin calificar el schema. Esto es lo que
# realmente falló en el incidente -- las tres verificaciones anteriores
# pueden pasar en teoría y esta seguir fallando por algún otro motivo, así
# que no se sustituye una por la otra.
got=$(docker exec -i "$CONTAINER" psql -U "$APP_USER" -d "$DATABASE" -tA -c "SELECT current_schema();" 2>&1) || {
  echo "FALLO: $APP_USER no pudo conectar/consultar current_schema(): $got" >&2
  fail=1
}
got_trimmed=$(echo "${got:-}" | xargs)
if [ "$got_trimmed" != "public" ]; then
  echo "FALLO: current_schema() como $APP_USER = '$got_trimmed', esperado 'public'" >&2
  fail=1
else
  echo "OK: current_schema() como $APP_USER = public"
fi

got=$(docker exec -i "$CONTAINER" psql -U "$APP_USER" -d "$DATABASE" -tA -c "SELECT count(*) FROM organizations;" 2>&1) || {
  echo "FALLO: $APP_USER no pudo leer organizations sin calificar el schema: $got" >&2
  fail=1
}

if [ "$fail" -ne 0 ]; then
  echo "=== verify-restore-ownership: FALLÓ. No arrancar la aplicación contra esta base. ===" >&2
  exit 1
fi

echo "=== verify-restore-ownership: PASS ==="
