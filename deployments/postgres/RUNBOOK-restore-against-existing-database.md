# Runbook: restaurar un pg_dump contra una base de datos PREEXISTENTE

Este runbook cubre un caso distinto de `RUNBOOK-enable-pgvector.md`: ese
otro documento habilita la extensión `vector` en un volumen que ya existe
pero nunca corrió los scripts de `docker-entrypoint-initdb.d`. Este
documento cubre restaurar un `pg_dump -Fc` completo **encima de una base
de datos que ya existía antes del restore** (el caso real de un cutover:
`explorarte_org` en producción, no un contenedor recién inicializado) --
el camino que causó el incidente descrito abajo.

## El incidente (CUTOVER-REHEARSAL-002)

Un restore real de producción hizo:

```sql
DROP SCHEMA public CASCADE;
CREATE SCHEMA public;
CREATE EXTENSION IF NOT EXISTS vector;
```

seguido de `pg_restore --no-owner` y una reasignación de ownership objeto
por objeto (tablas, funciones, secuencias) hacia `explorarte_app`.

Todo eso pasó sin error. Pero `CREATE SCHEMA public` sin `AUTHORIZATION`
explícita crea el schema con owner = el rol que ejecuta el comando
(`explorarte_admin`), no con el owner histórico de la base de datos. La
reasignación de ownership por objeto deja cada tabla individual con el
owner correcto, pero **el schema contenedor** sigue siendo de
`explorarte_admin` -- y `explorarte_app` nunca recibió `USAGE` sobre él.

El síntoma: `current_schema()` para `explorarte_app` resolvía vacío, y
cualquier consulta sin calificar (`SELECT * FROM organizations`) fallaba
con `relation "organizations" does not exist` -- pese a que la tabla
existía y `explorarte_app` era su owner. El fallo solo aparece conectando
como `explorarte_app`; conectando como `explorarte_admin` (el rol que
hizo el restore) todo se ve normal, por lo que pasó inadvertido durante
el propio restore y solo se descubrió al intentar arrancar `orgd`.

Esto **nunca ocurre** en un contenedor Postgres recién inicializado
(los rehearsals de este proyecto usan uno por defecto), porque ahí
`ALTER DATABASE ... OWNER TO explorarte_app` (parte de
`deployments/postgres/init/001-create-app-user.sh`) corre antes de que
exista ningún schema, y el schema `public` inicial de Postgres 15+ es
propiedad de `pg_database_owner`, que resuelve al owner real de la base
de datos automáticamente. Restaurar sobre una base **preexistente** rompe
esa cadena porque el schema se recrea explícitamente, con un owner fijo
en el momento de la creación, no derivado dinámicamente.

## Cuándo aplica este runbook

Cualquier restore de `pg_dump`/`pg_restore` contra un Postgres cuya base
de datos (`explorarte_org` u otra) **ya existía** antes del restore --
típicamente: un rollback de cutover, o restaurar un backup productivo
sobre el mismo volumen productivo. No aplica a un contenedor con volumen
recién creado (usar el flujo normal de `docker-entrypoint-initdb.d` +
`RUNBOOK-enable-pgvector.md` si corresponde).

## Procedimiento

1. Confirmar cero conexiones activas antes de tocar el schema:
   ```
   docker exec <contenedor_postgres> psql -U "$ORG_POSTGRES_ADMIN_USER" -d "$ORG_POSTGRES_DATABASE" \
     -c "SELECT pid, usename, state FROM pg_stat_activity WHERE datname='$ORG_POSTGRES_DATABASE' AND pid <> pg_backend_pid();"
   ```
   Debe devolver cero filas. Si no, detener los escritores (`orgd`,
   `model-worker`) primero -- nunca restaurar con conexiones activas.

2. Vaciar y recrear el schema:
   ```
   docker exec -i <contenedor_postgres> psql -U "$ORG_POSTGRES_ADMIN_USER" -d "$ORG_POSTGRES_DATABASE" \
     -c "DROP SCHEMA public CASCADE; CREATE SCHEMA public;"
   docker exec -i <contenedor_postgres> psql -U "$ORG_POSTGRES_ADMIN_USER" -d "$ORG_POSTGRES_DATABASE" \
     -c "CREATE EXTENSION IF NOT EXISTS vector;"
   ```

3. Restaurar:
   ```
   docker cp <dump.file> <contenedor_postgres>:/tmp/restore.dump
   docker exec <contenedor_postgres> pg_restore -U "$ORG_POSTGRES_ADMIN_USER" -d "$ORG_POSTGRES_DATABASE" --no-owner -v /tmp/restore.dump
   docker exec <contenedor_postgres> rm -f /tmp/restore.dump
   ```

4. Reasignar ownership de cada objeto (`pg_restore --no-owner` deja todo
   propiedad del rol que restauró):
   ```
   # ver reassign-owner.sql en este directorio -- itera tablas,
   # vistas, secuencias no ligadas a IDENTITY, y funciones en el schema
   # public, reasignando cada una a explorarte_app.
   ```

5. **Paso obligatorio, no opcional -- exactamente el que faltó en el
   incidente:**
   ```
   docker exec -i <contenedor_postgres> psql -U "$ORG_POSTGRES_ADMIN_USER" -d "$ORG_POSTGRES_DATABASE" \
     -c "ALTER SCHEMA public OWNER TO explorarte_app; GRANT USAGE ON SCHEMA public TO explorarte_app;"
   ```

6. **Gate automático, no una nota -- correr antes de arrancar la
   aplicación:**
   ```
   ./deployments/postgres/verify-restore-ownership.sh <contenedor_postgres> "$ORG_POSTGRES_ADMIN_USER" explorarte_app "$ORG_POSTGRES_DATABASE"
   ```
   Si este script sale con código != 0, **no arrancar `orgd`/`model-worker`**.
   El script prueba exactamente el síntoma real del incidente (conectar
   como `explorarte_app` y resolver una tabla sin calificar el schema),
   no solo los grants en abstracto.

7. Solo después de que el paso 6 pase, arrancar la aplicación.

## Antes de correr esto contra el volumen productivo real

Mismo criterio que `RUNBOOK-enable-pgvector.md`: confirmar explícitamente
con el dueño del sistema, probar contra una copia/staging primero, y
aplicar contra producción solo en una ventana de mantenimiento explícita
con tráfico cerrado. El paso 1 (cero conexiones activas) es la
verificación mínima de que el tráfico está efectivamente cerrado, no un
sustituto de confirmarlo operacionalmente antes de empezar.
