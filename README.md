# Explorarte Organization Kernel

Monolito modular en Go para el plano de control organizacional de Explorarte.

## Estado de esta rama

La Rama 03 agrega el registro organizacional canónico sobre la persistencia de la
Rama 02.

Incluye:

- lectura tipada y estricta de los documentos requeridos en `docs/canonical`;
- validaciones cruzadas del organigrama, líderes, workers, reporting, modelos y
  clases de autoridad;
- hash canónico determinista por documento y agregado;
- materialización versionada en PostgreSQL;
- diff, dry-run y sincronización administrativa explícita;
- retiro lógico de roles y unidades;
- historial de relaciones `reports_to` por revisión;
- evento genérico `organization.registry_synced`;
- consultas internas y comandos `orgctl registry`;
- pruebas unitarias y de integración con PostgreSQL real.

La fuente declarativa sigue siendo Git. PostgreSQL es una representación materializada,
consultable y auditable. No hay sincronización automática durante el arranque.

## Requisitos

- Go 1.25 o `GOTOOLCHAIN=auto`;
- Docker Engine y Docker Compose;
- arquitectura ARM64 o AMD64.

## Configuración local

```bash
cp .env.example .env
```

Reemplaza las contraseñas de ejemplo. `.env` está ignorado por Git.

## Entorno Docker

```bash
make compose-up
docker compose ps
curl -fsS http://127.0.0.1:8080/healthz
curl -fsS http://127.0.0.1:8080/readyz
```

PostgreSQL no publica el puerto 5432 al host. `orgd` solo publica HTTP en
`127.0.0.1`.

`/healthz` indica liveness del proceso. `/readyz` conserva la semántica de la Rama
02: depende de PostgreSQL y de las migraciones, no de que el registro ya haya sido
sincronizado.

## Migraciones

```bash
docker compose exec orgd /usr/local/bin/orgctl migrate status
docker compose exec orgd /usr/local/bin/orgctl migrate up
```

No edites una migración aplicada. `schema_migrations` conserva el SHA-256 del SQL
ascendente.

## Registro organizacional

```bash
make registry-validate
make registry-diff
make registry-sync
make registry-status
```

Consultas:

```bash
go run ./cmd/orgctl registry list-units
go run ./cmd/orgctl registry list-roles --unit ingenieria_ia
go run ./cmd/orgctl registry list-roles --enabled --json
go run ./cmd/orgctl registry get-role ingenieria_ia/orquestador
go run ./cmd/orgctl registry get-leader ingenieria_ia
```

`registry diff` y `registry sync` sin `--apply` nunca escriben. La primera
materialización se aplica mediante:

```bash
go run ./cmd/orgctl registry sync --apply
```

## Verificación

```bash
make verify
make test-integration
make verify-all
```

El entorno de integración usa proyecto Compose, red y volúmenes aislados. No toca
el volumen de desarrollo.

## Endpoints

```text
GET /healthz
GET /readyz
GET /version
```

No se agregan endpoints HTTP del registro en esta rama.

## Límites

Esta rama no implementa proyectos, tareas, mensajes, SKIP LOCKED, leases, outbox,
dead letter, agentes ejecutables, modelos, skills, memoria, RAG, Prolog ni células.

Consulta `docs/implementation/branch-03-organization-registry/INTEGRATION.md`.
