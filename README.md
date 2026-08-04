# Explorarte Organization Kernel

Monolito modular en Go para el plano de control organizacional de Explorarte.

## Estado de esta rama

La Rama 02 incorpora la infraestructura PostgreSQL sin implementar todavía el
registro organizacional, proyectos, agentes, mensajería ni el motor durable de
tareas.

Incluye:

- `orgd`, daemon HTTP privado;
- `orgctl`, CLI operativa y de migraciones;
- pool PostgreSQL con `pgx`;
- migraciones SQL embebidas, versionadas y protegidas por advisory lock;
- `schema_migrations` con checksum para detectar drift;
- tabla genérica `audit_events`;
- `UnitOfWork` para transacciones;
- PostgreSQL interno en Docker Compose, sin puerto publicado;
- tests unitarios y tests de integración reales contra PostgreSQL.

## Requisitos

- Go 1.25 o `GOTOOLCHAIN=auto`.
- Docker Engine y Docker Compose para el entorno completo.
- Arquitectura ARM64 o AMD64.

## Configuración local

```bash
cp .env.example .env
```

Reemplaza obligatoriamente las dos contraseñas de ejemplo. `.env` está
ignorado por Git y no debe compartirse.

```bash
openssl rand -base64 36
```

## Levantar el entorno

```bash
make compose-up

docker compose ps
curl -fsS http://127.0.0.1:8080/healthz
echo
curl -fsS http://127.0.0.1:8080/readyz
echo
```

`/healthz` indica que el proceso HTTP está vivo. `/readyz` depende de que
PostgreSQL responda y de que el esquema esté completamente migrado.

PostgreSQL no publica `5432` al host. Para ejecutar SQL administrativo usa el
contenedor PostgreSQL.

## Migraciones

Automáticas, controladas por `ORG_DATABASE_AUTO_MIGRATE=true`.

```bash
docker compose exec orgd /usr/local/bin/orgctl migrate status
docker compose exec orgd /usr/local/bin/orgctl migrate up
```

Las migraciones viven en `migrations/` con el formato:

```text
NNNNNN_nombre.up.sql
NNNNNN_nombre.down.sql
```

No edites una migración ya aplicada. La tabla `schema_migrations` conserva el
SHA-256 del SQL ascendente y rechaza drift.

## Verificación

```bash
make verify
make test-integration
make verify-all
```

El test de integración usa un proyecto Compose, red y volúmenes aislados; no
toca el volumen de desarrollo `explorarte-org-postgres-data`.

## Endpoints

```text
GET /healthz  liveness del proceso; no depende de PostgreSQL
GET /readyz   readiness de PostgreSQL y esquema
GET /version  información de build
```

## Límites de esta rama

Esta rama no implementa `SKIP LOCKED`, leases, tareas, outbox, dead letter,
mensajería, proyectos, agentes ni departamentos. La siguiente migración debe
comenzar en `000002`.

Consulta `docs/implementation/branch-02-postgres-storage/INTEGRATION.md`.
