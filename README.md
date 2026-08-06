# Explorarte Organization Kernel

Monolito modular en Go para el plano de control organizacional de Explorarte.

## Estado de esta rama

La Rama 10 separa la identidad del *subject role* (cognitiva) de la del *dispatch actor* (infraestructura), mediante execution principals y dispatcher assignments durables y acotados, manteniendo el runtime productivo cerrado por defecto.

La fuente de verdad se divide de forma explícita:

- `docs/canonical/capability-matrix.yaml` define `model.invoke` (Rama 09) y las capabilities administrativas `model.execution_principal.register/disable` y `model.dispatch_assignment.create/revoke` (Rama 10), owner-only vía wildcard, con hard deny explícito para `execution_service`;
- `docs/canonical/model-routing.yaml` continúa siendo la única autoridad para provider, modelo y transporte;
- `docs/canonical/model-egress-policy.yaml` define qué clasificaciones pueden salir y comienza sin ninguna regla `allow`;
- PostgreSQL materializa versiones inmutables, bindings por revisión, execution principals, dispatcher assignments y decisiones pre-send sin almacenar contenido.

Toda invocación nueva fija la versión y el hash de la política de egress, además del ID de la dispatcher assignment y del execution principal. Las invocaciones anteriores a Rama 09 permanecen `legacy_unpinned` (egress `NULL/NULL`); las anteriores a Rama 10 permanecen sin assignment/principal (`NULL/NULL`) y nunca llaman al adapter. El orden pre-send es: resolver el principal local y vincular el claim, validar task/attempt/lease y la dispatcher assignment (activa, vigente, con cuota), autorizar `model.invoke` sobre el dispatch actor, evaluar egress, comprobar adapter, renderizar y persistir atómicamente `allow + assignment_use + send_started`.

Los providers reales continúan `adapter_status=unavailable` y `dispatch_enabled=false`. `orgd` no registra adapters ni ejecuta invocaciones. `test.fake` existe únicamente en fixtures aisladas. No hay HTTP a providers, shell, credenciales, worker persistente ni dispatcher productivo: ningún rol combina `model.invoke` + model policy + task assignment + binding + adapter ejecutable fuera de fixtures.

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
orgctl model invocation dispatch INVOCATION_ID --json
orgctl model invocation reconcile --json

orgctl model principal register --file principal.json --actor-role empresa/human --json
orgctl model principal disable PRINCIPAL_ID --actor-role empresa/human --reason retired --json

orgctl model assignment create --task-id T --attempt-id A --subject-role ROLE \
  --principal-key oracle-01/model-runtime-01 --max-invocations 1 --ttl 15m \
  --idempotency-key KEY --actor-role empresa/human --json
orgctl model assignment revoke ASSIGNMENT_ID --actor-role empresa/human --reason superseded --json
orgctl model assignment expire --json
```

`ORG_MODEL_EXECUTION_PRINCIPAL_KEY` identifica localmente qué principal ejecuta `dispatch`; no es un secreto ni autenticación remota. `--claimed-by`, `--principal` y `--assignment` no existen en el comando de dispatch. Aunque `ORG_MODEL_RUNTIME_ENABLED=true`, un dispatch requiere simultáneamente task assignment, attempt y lease activos, una dispatcher assignment vigente con cuota, `model.invoke` sobre el dispatch actor, egress explícitamente permitido y un adapter registrado. La configuración productiva actual no reúne esa combinación. El resultado de un modelo no completa tareas ni ejecuta tool intents.

Consulta `docs/implementation/branch-08-model-runtime-gateway/INTEGRATION.md`, `docs/implementation/branch-09-model-egress-authorization/INTEGRATION.md` y `docs/implementation/branch-10-model-dispatcher-assignments/INTEGRATION.md`.

### Cryptographic model execution identity

Model dispatch uses durable execution principals and dispatcher assignments,
but a principal label alone is not authentication. New invocations also pin a
version of `docs/canonical/model-execution-identity-policy.yaml`. Dispatch
requires a one-use Ed25519 challenge signed with a private key held outside
PostgreSQL; the claim transaction independently verifies the signature and
binds the resulting assertion to the dispatch attempt. Private keys, raw nonces
and raw signatures are never persisted.

Operational variables:

```text
ORG_MODEL_EXECUTION_PRINCIPAL_KEY=oracle-01/model-runtime-01
ORG_MODEL_EXECUTION_IDENTITY_ENABLED=false
ORG_MODEL_EXECUTION_IDENTITY_KEY_FILE=/run/explorarte/model-execution-identity.pem
```

The principal key is a non-secret locator. The key file must contain one
Ed25519 PKCS#8 `PRIVATE KEY` PEM block and should be mode `0600`. Identity
disabled means dispatch denied; there is no fallback to label-only dispatch.
Administrative policy and public-key commands are available under
`orgctl model identity`. This does not enable a real provider: `FakeAdapter`
remains the only executable adapter.

### Real provider adapter boundary

Rama 12 adds one executable HTTP adapter for the canonical `openai_compatible`
provider. It is disabled by default and cannot be selected from invocation JSON
or CLI flags. Productive egress remains default-deny: only `public` and
`sanitized` context classifications are approved for this provider;
`organizational`, `secret` and `clinical` are denied.

Operational configuration:

```text
ORG_MODEL_PROVIDER_OPENAI_COMPATIBLE_ENABLED=false
ORG_MODEL_PROVIDER_OPENAI_COMPATIBLE_ENDPOINT_URL=https://provider.example/v1/chat/completions
ORG_MODEL_PROVIDER_OPENAI_COMPATIBLE_CREDENTIAL_FILE=/run/secrets/openai-compatible-token
ORG_MODEL_PROVIDER_OPENAI_COMPATIBLE_REQUEST_TIMEOUT=2m
ORG_MODEL_PROVIDER_OPENAI_COMPATIBLE_CIRCUIT_FAILURE_THRESHOLD=5
ORG_MODEL_PROVIDER_OPENAI_COMPATIBLE_CIRCUIT_OPEN_DURATION=30s
```

The endpoint must be HTTPS and the credential file must be absolute, regular,
bounded and private (normally mode `0600`). Environment proxies and redirects
are not used. PostgreSQL persists only versioned request/outcome metadata,
hashes and provenance. It never stores the endpoint, token, headers, rendered
context, HTTP bodies or provider error messages. A durable provider-request row
is committed with egress allow, assignment consumption and `send_started`
before the adapter may perform HTTP.

See `docs/implementation/branch-12-model-provider-adapter/INTEGRATION.md`.

### Persistent cell worker

Rama 13 adds `orgctl model worker run`, a persistent process that replaces
repeated manual `orgctl model invocation dispatch <id>` calls with a poll loop
against invocations pinned to `ORG_MODEL_EXECUTION_PRINCIPAL_KEY`. It holds no
state of its own: eligibility, claims and dispatch quota stay enforced by
Ramas 08-12. It is a separate, explicitly-launched process — `orgd` still
never dispatches.

```bash
orgctl model worker run
```

Operational configuration:

```text
ORG_MODEL_WORKER_BATCH_SIZE=10
ORG_MODEL_WORKER_CONCURRENCY=1
ORG_MODEL_WORKER_MIN_BACKOFF=1s
ORG_MODEL_WORKER_MAX_BACKOFF=1m
ORG_MODEL_WORKER_SHUTDOWN_GRACE=30s
```

The process exits gracefully on `SIGINT`/`SIGTERM`: it stops accepting new
work but waits for every dispatch it already started to finish, bounded by
`ORG_MODEL_WORKER_SHUTDOWN_GRACE`. It never selects a provider, holds
credentials, or renders context — those stay behind
`internal/modelruntime.DispatchService`.

See `docs/implementation/branch-13-persistent-cell-worker/INTEGRATION.md`.
