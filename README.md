# Explorarte Organization Kernel

Monolito modular en Go para el plano de control organizacional de Explorarte.

## Estado de esta rama

La Rama 04 agrega un motor durable de tareas sobre PostgreSQL a la base validada de la Rama 03.

La fuente de verdad continúa siendo PostgreSQL. `orgd` reconcilia estados durables, recupera leases y claims del outbox expirados, promueve tareas elegibles y bloquea dependencias o asignaciones inválidas. `orgd` no ejecuta tareas ni contiene un worker autónomo.

Componentes principales:

- máquina de estados tipada con terminalidad irreversible;
- creación JSON estricta e idempotencia explícita;
- dependencias acíclicas y semántica estricta: solo `completed` satisface;
- requisitos y evidencia opaca para verificación;
- intentos separados de leases criptográficos;
- claims concurrentes con `FOR UPDATE SKIP LOCKED`;
- reintentos acotados, dead letters formales y redrive no automático;
- eventos append-only, auditoría mínima y outbox transaccional;
- reconciliador interno tolerante a indisponibilidad temporal de PostgreSQL;
- CLI administrativa y de worker mediante `orgctl task` y `orgctl outbox`.

Los documentos de `docs/canonical` no se modifican en esta rama.

## Requisitos

- Go 1.25 o `GOTOOLCHAIN=auto`;
- Docker Engine y Docker Compose;
- PostgreSQL 17 para las pruebas de integración;
- arquitectura ARM64 o AMD64.

## Configuración local

```bash
cp .env.example .env
```

Reemplaza todos los secretos de ejemplo. `.env` está ignorado por Git. PostgreSQL no publica el puerto 5432 al host.

## Entorno Docker

```bash
make compose-up
docker compose ps
curl -fsS http://127.0.0.1:8080/healthz
curl -fsS http://127.0.0.1:8080/readyz
```

`/healthz` conserva liveness del proceso. `/readyz` sigue dependiendo de PostgreSQL y de que no existan migraciones pendientes. Una caída de PostgreSQL no termina el proceso HTTP; el reconciliador registra el fallo y vuelve a intentarlo.

## Migraciones y registro

```bash
docker compose exec -T orgd /usr/local/bin/orgctl migrate status
docker compose exec -T orgd /usr/local/bin/orgctl migrate up
docker compose exec -T orgd /usr/local/bin/orgctl registry validate
docker compose exec -T orgd /usr/local/bin/orgctl registry status --json
```

No edites una migración aplicada. `000003_create_durable_task_engine` crea todas las tablas del motor durable y su rollback elimina únicamente esas tablas.

## Crear una tarea

```json
{
  "assigned_role_id": "ingenieria_ia/qa",
  "idempotency_key": "qa-release-2026-08-04",
  "title": "Verificar release",
  "instructions": "Ejecutar las comprobaciones aprobadas y registrar evidencia.",
  "acceptance_criteria": [
    "build reproducible",
    "pruebas aprobadas"
  ],
  "max_attempts": 5,
  "requirements": [
    {
      "key": "qa-report",
      "type": "check",
      "description": "Informe de QA aprobado"
    }
  ]
}
```

```bash
docker compose exec -T orgd \
  /usr/local/bin/orgctl task create \
  --file /tmp/task.json \
  --actor-id empresa/human \
  --json
```

La petición rechaza campos de autoridad, modelo, capacidades, herramientas, perfiles, memoria, shell, contenedores o Kubernetes. La misma clave y el mismo hash devuelven la tarea existente; una petición distinta con la misma clave produce conflicto.

## Ciclo de worker

```bash
# Reclamar. El token aparece una sola vez en la respuesta.
orgctl task claim --worker worker-01 --role ingenieria_ia/qa --batch 1 --json

# El token nunca se pasa como argumento. Se entrega exclusivamente por stdin.
printf '%s' "$LEASE_TOKEN" | orgctl task start TASK_ID --attempt ATTEMPT_ID --worker worker-01
printf '%s' "$LEASE_TOKEN" | orgctl task heartbeat TASK_ID --attempt ATTEMPT_ID --worker worker-01 --extend 2m
printf '%s' "$LEASE_TOKEN" | orgctl task result TASK_ID --attempt ATTEMPT_ID --worker worker-01 --result-file result.json --json
```

Resultado exitoso:

```json
{"outcome":"succeeded","summary":"ejecución terminada"}
```

Un resultado exitoso deja la tarea en `awaiting_verification`; nunca la completa. La terminalidad requiere `task finalize` y todos los requisitos obligatorios satisfechos.

## Verificación y terminalidad

```bash
orgctl task evidence add TASK_ID \
  --requirement REQUIREMENT_ID \
  --type check \
  --reference artifact://qa/report-42 \
  --recorded-by ingenieria_ia/qa \
  --satisfies

orgctl task finalize TASK_ID \
  --outcome completed \
  --actor-id ingenieria_ia/qa
```

`no_action`, `failed`, `dead_letter`, `rejected` y `cancelled` son terminales, pero no satisfacen dependencias. No existe reapertura silenciosa ni redrive automático de dead letters.

## Outbox

```bash
orgctl outbox claim --consumer publisher-01 --batch 10 --json
printf '%s' "$CLAIM_TOKEN" | orgctl outbox ack EVENT_ID --consumer publisher-01
printf '%s' "$CLAIM_TOKEN" | orgctl outbox nack EVENT_ID --consumer publisher-01 --error 'delivery failed'
orgctl outbox recover --batch 100 --json
orgctl outbox status --json
```

Los payloads del outbox son mínimos y versionados: identificador de tarea, tipo de evento, versión y estados. No contienen instrucciones ni tokens.

## Verificación

```bash
make verify
make build-cross
make test-integration
make verify-all
```

`make test-integration` levanta PostgreSQL 17 en un proyecto Compose aislado y cubre migraciones, registro, concurrencia de claims, leases, recuperación, terminalidad, dependencias, dead letters, outbox y smoke tests de CLI.

Consulta `docs/implementation/branch-04-durable-task-engine/INTEGRATION.md` para aplicación, rollback y matriz de invariantes.
