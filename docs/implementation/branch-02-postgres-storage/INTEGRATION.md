# Rama 02 — Persistencia PostgreSQL y migraciones

## Identidad

- Commit base requerido: `ac79a74ecc4213b324e2920ed38d0840ca8809f8`
- Rama: `feat/02-postgres-storage`
- Módulo Go: `github.com/Mireuz13/explorarte-organization`
- Arquitectura: monolito modular, PostgreSQL como dependencia interna durable.
- Runtime objetivo: ARM64 y AMD64, 2 vCPU y 8 GiB durante la prueba.

## Decisiones técnicas

1. PostgreSQL 17 se ejecuta en Docker Compose dentro de la red del proyecto.
2. El puerto `5432` no se publica al host.
3. `orgd` usa un rol de aplicación `NOSUPERUSER`, distinto del rol de inicialización administrativa.
4. El pool usa `pgxpool`; no hay ORM.
5. El pool se crea sin exigir conectividad inicial. El HTTP puede iniciar aunque PostgreSQL esté caído.
6. `/healthz` es liveness del proceso y nunca consulta PostgreSQL.
7. `/readyz` requiere proceso iniciado, esquema actual y `Ping` PostgreSQL.
8. Las migraciones están embebidas, se aplican en una transacción y usan `pg_advisory_xact_lock`.
9. `schema_migrations` registra versión, nombre, SHA-256, momento y duración.
10. `UnitOfWork` encapsula begin/commit/rollback mediante SQL explícito y `pgx.Tx`.
11. Se usa Go 1.25 y `pgx/v5` 5.9.2.

## Proporciona

### Configuración tipada

`internal/config.DatabaseConfig`

### PostgreSQL

```text
internal/platform/postgres
```

```go
type UnitOfWork interface {
    WithinTransaction(context.Context, pgx.TxOptions, TxFunc) error
}
```

`postgres.Store` proporciona `Ping`, `Close`, `Pool` y `UnitOfWork`.

### Migraciones

```text
migrations
internal/platform/migrations
```

CLI:

```text
orgctl migrate up
orgctl migrate status
```

Migración incluida:

```text
000001_create_audit_events
```

Tablas creadas:

```text
schema_migrations
audit_events
```

## No proporciona

- Registro de departamentos, perfiles, roles o agentes.
- Proyectos o tareas.
- `FOR UPDATE SKIP LOCKED`.
- Leases, heartbeats o reconciliación.
- Outbox o mensajería.
- Dead letter.
- Repositorios de dominio.
- Adaptadores de modelos.
- Datos clínicos.

## Configuración

| Variable | Default |
|---|---:|
| `ORG_DATABASE_URL` | vacío |
| `ORG_DATABASE_HOST` | `127.0.0.1` |
| `ORG_DATABASE_PORT` | `5432` |
| `ORG_DATABASE_NAME` | `explorarte_org` |
| `ORG_DATABASE_USER` | `explorarte_app` |
| `ORG_DATABASE_PASSWORD` | vacío |
| `ORG_DATABASE_SSLMODE` | `disable` |
| `ORG_DATABASE_MAX_CONNS` | `8` |
| `ORG_DATABASE_MIN_CONNS` | `1` |
| `ORG_DATABASE_MAX_CONN_LIFETIME` | `30m` |
| `ORG_DATABASE_MAX_CONN_IDLE_TIME` | `5m` |
| `ORG_DATABASE_HEALTH_CHECK_PERIOD` | `30s` |
| `ORG_DATABASE_CONNECT_TIMEOUT` | `5s` |
| `ORG_DATABASE_PING_TIMEOUT` | `2s` |
| `ORG_DATABASE_STATEMENT_TIMEOUT` | `30s` |
| `ORG_DATABASE_LOCK_TIMEOUT` | `5s` |
| `ORG_DATABASE_AUTO_MIGRATE` | `true` |
| `ORG_DATABASE_MIGRATION_TIMEOUT` | `45s` |
| `ORG_DATABASE_MIGRATION_RETRY` | `5s` |

Variables solo de Compose:

```text
ORG_POSTGRES_ADMIN_USER
ORG_POSTGRES_ADMIN_PASSWORD
ORG_POSTGRES_DATABASE
ORG_POSTGRES_USER
ORG_POSTGRES_PASSWORD
ORG_HTTP_PORT
```

## Pruebas

```bash
make verify
make test-integration
make verify-all
```

Cobertura:

- configuración y URL segura;
- parsing, orden, pares y checksums de migraciones;
- liveness independiente de PostgreSQL;
- readiness degradada y recuperación;
- fallo de migración sin matar HTTP;
- migración real e idempotencia;
- commit y rollback reales mediante `UnitOfWork`;
- persistencia de `audit_events`.

## Contrato para Rama 03

1. La siguiente migración debe ser `000002_*`.
2. Debe inyectar repositorios desde `internal/app`, no construir pools propios.
3. Puede usar `postgres.Store.UnitOfWork()` para operaciones atómicas.
4. No debe modificar `schema_migrations`.
5. No debe convertir `audit_events` en una tabla específica de agentes.
6. Debe conservar `/healthz` independiente de PostgreSQL.
7. No debe incorporar todavía cola, leases u outbox; eso corresponde a Rama 04.

## Archivos canónicos

Los documentos de `docs/canonical/` no fueron modificados.

## Commit sugerido

```text
feat(storage): add PostgreSQL persistence and migrations
```
