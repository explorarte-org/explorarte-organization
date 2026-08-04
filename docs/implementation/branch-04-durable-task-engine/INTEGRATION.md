# Rama 04 — Motor durable de tareas

## Base y alcance

- Base obligatoria: `a199d1eea1f4f28d1b9f346e9ccbd670d5e8b69a`.
- Rama: `feat/04-durable-task-engine`.
- Migración nueva: `000003_create_durable_task_engine`.
- No modifica `docs/canonical` ni las migraciones `000001` y `000002`.
- No incorpora brokers externos, ejecución de modelos, shell, contenedores de workers ni nuevos endpoints HTTP.

## Modelo de persistencia

`tasks` conserva la identidad, revisión organizacional, rol/unidad asignados, hash de idempotencia, prioridad, disponibilidad, intentos, versión y razones de estado.

Tablas subordinadas:

- `task_dependencies`: grafo acíclico; solo `completed` satisface una arista.
- `task_requirements`: `artifact`, `check`, `approval`, `condition` o `result`.
- `task_evidence`: referencias opacas y digest SHA-256 opcional; no almacena artefactos.
- `task_attempts`: historial de ejecución independiente del estado de la tarea.
- `task_leases`: un lease activo máximo por tarea; solo hash SHA-256 del token.
- `task_events`: secuencia append-only única por tarea.
- `task_dead_letters`: agotamiento formal; sin redrive automático.
- `outbox_events`: entrega durable general con claim, ack, nack y recuperación.

Cada cambio observable de tarea inserta evento, outbox y `audit_events` dentro de la misma transacción.

## Máquina de estados

No terminales:

```text
pending, ready, leased, running, awaiting_verification, blocked, retry_wait
```

Terminales:

```text
completed, no_action, failed, dead_letter, rejected, cancelled
```

Reglas críticas:

1. `ready`, `leased` y `running` nunca transicionan directamente a `completed`.
2. Un resultado `succeeded` termina el intento y deja la tarea en `awaiting_verification`.
3. `completed` exige `awaiting_verification` y todos los requisitos obligatorios en `satisfied`.
4. Ningún estado terminal puede reabrirse.
5. `no_action` requiere razón y no satisface dependencias.
6. Un fallo terminal de dependencia bloquea al dependiente con `dependency_terminal`.

## Claim y lease

La selección usa:

```sql
FOR UPDATE SKIP LOCKED
ORDER BY priority DESC, available_at ASC, created_at ASC, id ASC
```

El batch permitido es 1–32. En la misma transacción se crean intento, lease, transición, evento, outbox y auditoría.

El token se genera con entropía criptográfica, se devuelve una vez y se persiste exclusivamente como SHA-256. `start`, `heartbeat`, `result`, `outbox ack` y `outbox nack` reciben el token por stdin para evitar historial de shell y listados de procesos.

## Reconciliador

`orgd` ejecuta el reconciliador cuando `ORG_TASK_RECONCILER_ENABLED=true`. No reclama ni ejecuta tareas. Por lote:

1. expira leases vencidos y programa retry o dead letter;
2. recupera claims de outbox vencidos;
3. promueve `pending`/`retry_wait` elegibles;
4. bloquea dependencias terminales;
5. revalida rol, unidad, estado habilitado, ejecutable y retiro lógico.

Usa `clock_timestamp()` de PostgreSQL. Un fallo de base de datos se registra y el proceso HTTP continúa vivo.

## Variables

```text
ORG_TASK_ORGANIZATION_ID
ORG_TASK_RECONCILER_ENABLED
ORG_TASK_RECONCILE_INTERVAL
ORG_TASK_RECONCILE_BATCH_SIZE
ORG_TASK_DEFAULT_MAX_ATTEMPTS
ORG_TASK_DEFAULT_LEASE_DURATION
ORG_TASK_MAX_LEASE_DURATION
ORG_TASK_RETRY_BASE_DELAY
ORG_TASK_RETRY_MAX_DELAY
ORG_TASK_OUTBOX_MAX_ATTEMPTS
ORG_TASK_OUTBOX_CLAIM_DURATION
ORG_TASK_COMMAND_TIMEOUT
```

## Aplicación en AWS

```bash
cd /opt/explorarte/repos/explorarte-organization

test "$(git rev-parse HEAD)" = "a199d1eea1f4f28d1b9f346e9ccbd670d5e8b69a"
test -z "$(git status --porcelain)"

git switch -c feat/04-durable-task-engine
# Copiar payload o aplicar el paquete firmado.

make verify
make build-cross
make test-integration

docker compose up -d --build --wait postgres orgd
docker compose exec -T orgd /usr/local/bin/orgctl migrate status
docker compose exec -T orgd /usr/local/bin/orgctl registry status --json
docker compose exec -T orgd /usr/local/bin/orgctl outbox status --json
curl -fsS http://127.0.0.1:8080/healthz
curl -fsS http://127.0.0.1:8080/readyz
```

No publique 5432. No ejecute `registry sync --apply` salvo que el registro tenga drift esperado y revisado.

## Rollback

El rollback de código consiste en detener `orgd`, volver al commit anterior y reconstruir. No ejecute el down de `000003` mientras existan tareas que deban conservarse.

Rollback destructivo de esquema, únicamente con respaldo y aprobación humana:

```bash
pg_dump --format=custom --file=/ruta/segura/explorarte-before-rama04.dump "$ORG_DATABASE_URL"
# ejecutar 000003_create_durable_task_engine.down.sql en una ventana controlada
```

La migración down elimina todas las tareas, intentos, leases, evidencia, eventos, dead letters y outbox. No altera el registro organizacional ni `audit_events` históricos.

## Evidencia de validación requerida

La Rama 04 se considera validada solamente cuando el entorno con Go 1.25, Docker y PostgreSQL 17 registra código 0 para:

```bash
make verify
make build-cross
make test-integration
```

Además deben comprobarse:

```bash
git diff --exit-code a199d1eea1f4f28d1b9f346e9ccbd670d5e8b69a -- docs/canonical
git diff --exit-code a199d1eea1f4f28d1b9f346e9ccbd670d5e8b69a -- migrations/000001_create_audit_events.up.sql migrations/000001_create_audit_events.down.sql migrations/000002_create_organization_registry.up.sql migrations/000002_create_organization_registry.down.sql
```
