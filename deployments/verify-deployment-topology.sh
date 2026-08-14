#!/usr/bin/env bash
# Preflight de "deployment provenance": antes de docker compose up, resuelve
# la configuracion final de Compose y confirma que Postgres va a apuntar
# exactamente al volumen productivo esperado -- no a uno nuevo creado por
# accidente bajo el nombre namespaced del proyecto actual.
#
# Incidente que motiva este gate (2026-08-14, CUTOVER #2, host explorarte-org):
# ORG-AUDIT-007 elimino el `name:` explicito del volumen postgres-data para
# que los worktrees de rehearsal no colisionaran con produccion. La premisa
# -- "nada externo depende de ese nombre" -- era falsa: el volumen productivo
# real (explorarte-org-postgres-data, creado 2026-08-10) nunca fue migrado al
# nuevo esquema de nombres namespaced-por-proyecto. `docker compose up`
# recreo el contenedor de postgres, Compose resolvio el volumen bajo el
# nuevo nombre implicito (explorarte-organization_postgres-data), lo
# encontro vacio, y arranco ahi -- silenciosamente, sin ningun error. orgd
# reporto "48 pending migrations" en vez de 4. Ningun dato productivo se
# perdio porque el volumen real nunca fue tocado, pero produccion sirvio
# vacio durante varios minutos hasta que se detecto.
#
# Este script existe para que ese fallo sea imposible de repetir sin que
# alguien lo vea venir explicitamente: si el volumen resuelto no es
# exactamente el esperado, el script sale con codigo != 0 ANTES de que se
# ejecute `docker compose up`. No modifica nada -- solo lee.
#
# Uso:
#   ./verify-deployment-topology.sh <directorio_proyecto_compose> <volumen_postgres_esperado>
#
# Ejemplo (produccion):
#   ./verify-deployment-topology.sh /opt/explorarte/organization explorarte-org-postgres-data

set -euo pipefail

if [ "$#" -ne 2 ]; then
  echo "uso: $0 <directorio_proyecto_compose> <volumen_postgres_esperado>" >&2
  exit 2
fi

PROJECT_DIR="$1"
EXPECTED_VOLUME="$2"

cd "$PROJECT_DIR"

fail=0

echo "=== deployment provenance preflight: $PROJECT_DIR ==="

repo_head=$(git rev-parse HEAD 2>&1) || { echo "FALLO: no se pudo leer git HEAD: $repo_head" >&2; exit 1; }
compose_sha=$(sha256sum compose.yaml | awk '{print $1}')
compose_project=$(docker compose config --format json | python3 -c 'import json,sys; print(json.load(sys.stdin)["name"])')

echo "repo_head:            $repo_head"
echo "compose_file_sha256:  $compose_sha"
echo "compose_project_name: $compose_project"

resolved_volume=$(docker compose config --format json | python3 -c '
import json, sys
cfg = json.load(sys.stdin)
vol = cfg.get("volumes", {}).get("postgres-data", {})
print(vol.get("name", "<no-explicit-name -- project-namespaced default>"))
')

echo "resolved postgres-data volume: $resolved_volume"
echo "expected postgres-data volume: $EXPECTED_VOLUME"

if [ "$resolved_volume" != "$EXPECTED_VOLUME" ]; then
  echo "FALLO: el volumen resuelto ($resolved_volume) no coincide con el esperado ($EXPECTED_VOLUME)." >&2
  echo "STOP BEFORE docker compose up -- este deploy recrearia postgres contra un volumen distinto al productivo." >&2
  fail=1
else
  echo "OK: el volumen resuelto coincide con el esperado."
fi

if docker volume inspect "$EXPECTED_VOLUME" >/dev/null 2>&1; then
  created_at=$(docker volume inspect "$EXPECTED_VOLUME" --format '{{.CreatedAt}}')
  driver=$(docker volume inspect "$EXPECTED_VOLUME" --format '{{.Driver}}')
  mountpoint=$(docker volume inspect "$EXPECTED_VOLUME" --format '{{.Mountpoint}}')
  echo "OK: volumen esperado existe (created_at=$created_at, driver=$driver, mountpoint=$mountpoint)"
else
  echo "FALLO: el volumen esperado '$EXPECTED_VOLUME' no existe en este host." >&2
  fail=1
fi

for svc_image_var in "orgd" "model-worker" "postgres"; do
  image_id=$(docker compose config --format json | python3 -c "
import json, sys
cfg = json.load(sys.stdin)
svc = cfg['services'].get('$svc_image_var', {})
print(svc.get('image', '<unresolved>'))
")
  echo "service $svc_image_var image (resolved): $image_id"
done

if [ "$fail" -ne 0 ]; then
  echo "=== deployment provenance preflight: FALLO. No ejecutar docker compose up. ===" >&2
  exit 1
fi

echo "=== deployment provenance preflight: PASS ==="
