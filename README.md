# Explorarte Organization Kernel

Monolito modular en Go para el plano de control organizacional de Explorarte.

## Estado de esta rama

La Rama 09 agrega autorización específica para invocaciones de modelos y una política canónica de egress de contexto, manteniendo el runtime productivo cerrado por defecto.

La fuente de verdad se divide de forma explícita:

- `docs/canonical/capability-matrix.yaml` define `model.invoke`, concedida únicamente a `execution_service`, con hard deny para `owner`;
- `docs/canonical/model-routing.yaml` continúa siendo la única autoridad para provider, modelo y transporte;
- `docs/canonical/model-egress-policy.yaml` define qué clasificaciones pueden salir y comienza sin ninguna regla `allow`;
- PostgreSQL materializa versiones inmutables, bindings por revisión y decisiones pre-send sin almacenar contenido.

Toda invocación nueva fija la versión y el hash de la política de egress. Las invocaciones anteriores permanecen `legacy_unpinned` (`NULL/NULL`) y no pueden despacharse. El orden pre-send es: validar task/attempt/lease y bindings, autorizar `model.invoke`, evaluar egress, comprobar adapter, renderizar y persistir atómicamente `allow + send_started`.

Los providers reales continúan `adapter_status=unavailable` y `dispatch_enabled=false`. `orgd` no registra adapters ni ejecuta invocaciones. `test.fake` existe únicamente en fixtures aisladas. No hay HTTP a providers, shell, credenciales ni dispatcher productivo.

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

## Staging seguro y promoción explícita

Rama 05 añade workspaces Git separados, artefactos content-addressed y promoción por compare-and-swap. El módulo permanece deshabilitado por defecto y `orgd` solo puede ejecutar reconciliación segura; no crea worktrees, no ejecuta checks y no promueve refs.

```bash
orgctl staging repo validate explorarte-organization --json
printf '%s' "$LEASE_TOKEN" | orgctl staging workspace create \
  --task TASK_ID --attempt ATTEMPT_ID \
  --repository explorarte-organization \
  --base BASE_COMMIT --target-ref refs/heads/integration \
  --holder code-runner-01 \
  --actor-role ingenieria_ia/code-runner \
  --artifact-requirement REQUIREMENT_ID \
  --lease-token-stdin --json
```

El flujo posterior exige sellado, resultado exitoso del intento, checks registrados, revisión independiente y `orgctl staging promotion apply`. La promoción actualiza únicamente el ref autorizado mediante `git update-ref <target> <candidate> <expected-base>`; no hace merge, rebase, despliegue ni finaliza automáticamente la tarea.

```bash
make test-staging-fitness
make test-staging-integration
```

Consulta `docs/implementation/branch-05-staging-promotion-engine/INTEGRATION.md` antes de habilitar el módulo o provisionar roots en AWS.

## Model Runtime y egress (Ramas 08–09)

La sincronización debe respetar este orden para la revisión organizacional actual:

```bash
orgctl registry validate
orgctl registry diff
orgctl registry sync --apply

orgctl model registry diff --json
orgctl model registry sync --apply --json

orgctl model egress validate --json
orgctl model egress diff --json
orgctl model egress sync --apply --json
orgctl model egress status --json
```

La CLI no permite seleccionar provider, policy, versión, transporte, clasificaciones ni reglas. Esos valores se resuelven desde las fuentes canónicas y quedan fijados en la invocación. Con la política productiva inicial, `public`, `sanitized` y `organizational` se deniegan para todos los providers reales; `secret` y `clinical` son hard deny; `unknown` y el conjunto vacío se deniegan por defecto.

```bash
orgctl model invocation create --file invocation.json --json
orgctl model invocation dispatch INVOCATION_ID --claimed-by orgctl --json
orgctl model invocation reconcile --json
```

Aunque `ORG_MODEL_RUNTIME_ENABLED=true`, un dispatch requiere simultáneamente task assignment, attempt y lease activos, `model.invoke`, egress explícitamente permitido y un adapter registrado. La configuración productiva actual no reúne esa combinación. El resultado de un modelo no completa tareas ni ejecuta tool intents.

Consulta `docs/implementation/branch-08-model-runtime-gateway/INTEGRATION.md` y `docs/implementation/branch-09-model-egress-authorization/INTEGRATION.md`.
