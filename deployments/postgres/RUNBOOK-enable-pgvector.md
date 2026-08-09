# Runbook: habilitar pgvector en un volumen Postgres existente

`docker-entrypoint-initdb.d` (donde vive `001-create-app-user.sh`) solo se
ejecuta la primera vez que Postgres inicializa un volumen de datos vacío.
Todo volumen que ya existe hoy (local, staging, o el productivo real de
`orgd`) **no** vuelve a correr esos scripts al simplemente cambiar la imagen
en `compose.yaml` — por eso este paso es un procedimiento manual, no un
script de init nuevo. Aplica igual a un volumen local recién creado: mantener
un solo camino de provisión evita que "en mi máquina funciona porque el
volumen era nuevo" oculte que el paso falta en un volumen real.

## Cuándo correr esto

Una sola vez por cada volumen Postgres físico distinto (contenedor de test
local, staging, producción), **después** de que el contenedor ya está
corriendo la imagen `pgvector/pgvector:pg17` (ver `compose.yaml`) pero
**antes** de aplicar cualquier migración de aplicación que use el tipo
`vector(N)`.

## Pasos

1. Confirmar que el contenedor está corriendo la imagen correcta:
   ```
   docker inspect --format='{{.Config.Image}}' <nombre-del-contenedor-postgres>
   ```
   Debe mostrar el digest de `pgvector/pgvector:pg17` fijado en `compose.yaml`.

2. Correr `CREATE EXTENSION` como el rol admin (`POSTGRES_USER`, nunca como
   `explorarte_app` — ese rol es `NOSUPERUSER NOCREATEDB NOCREATEROLE` y no
   puede crear extensiones):
   ```
   docker exec -i <nombre-del-contenedor-postgres> \
     psql --set=ON_ERROR_STOP=1 -U "$ORG_POSTGRES_ADMIN_USER" -d "$ORG_POSTGRES_DATABASE" \
     -c "CREATE EXTENSION IF NOT EXISTS vector;"
   ```

3. Verificar:
   ```
   docker exec -i <nombre-del-contenedor-postgres> \
     psql -U "$ORG_POSTGRES_ADMIN_USER" -d "$ORG_POSTGRES_DATABASE" \
     -c "SELECT extname, extversion FROM pg_extension WHERE extname='vector';"
   ```
   Debe devolver exactamente una fila.

## Antes de correr esto contra el volumen productivo real

No hacerlo sin, en este orden:

1. Confirmar explícitamente con el dueño del sistema el estado real de la
   tarea de deployment de `orgd`/Postgres — no asumir a partir de lo que diga
   el repo local o un reporte de otra sesión sin verificarlo contra la VPS
   misma.
2. Confirmar espacio en disco libre suficiente y un plan de backup
   restaurable que conserve la extensión ya creada (un rollback de imagen
   solo, sin restaurar el backup, dejaría `CREATE EXTENSION` aplicado pero
   con una imagen que no la necesita — inofensivo, pero documentarlo).
3. Probar los tres pasos de arriba contra una copia/staging del volumen
   productivo primero, no directo en producción.
4. Recién entonces, aplicar contra producción en una ventana de mantenimiento
   explícita.

Este runbook solo cubre el paso de habilitar la extensión. No cubre la
migración de la imagen en sí (`docker compose up -d postgres` para recrear el
contenedor sobre el mismo volumen con la nueva imagen) ni las migraciones de
aplicación que agregan columnas `vector(N)` — esas son pasos separados.
