# Rama 12 — Real model provider adapter and credential boundary

## Base y alcance

- Base exacta: `c34e0f489ee84de99ba61fb89a75062752c4f065`.
- Rama: `feat/12-model-provider-adapter`.
- Objetivo: incorporar el primer adapter real, `openai_compatible`, mediante un contrato HTTP compatible con Chat Completions, credenciales externas, egress productivo mínimo y evidencia durable del request/outcome del provider.
- Migración: `000011_create_model_provider_adapter`.
- SHA final: pendiente hasta cerrar la rama.

Fuera de alcance: worker persistente, polling, scheduler, ejecución de tools, completion de tasks por resultados de modelo, memoria, RAG, proveedores DeepSeek/Alibaba ejecutables, CLI adapters, streaming, retries automáticos después de `send_started`, Vault/KMS, mTLS, selección pública de endpoint/provider/model/policy o credencial, y almacenamiento de prompts, contexto renderizado, cuerpos HTTP o hidden reasoning.

## Fuentes de verdad

| Fuente | Autoridad exclusiva |
|---|---|
| `docs/canonical/model-routing.yaml` | provider, provider model, transport y disponibilidad compilada por profile |
| `docs/canonical/model-egress-policy.yaml` | clases que pueden salir por cada provider y precedencia default-deny |
| `docs/canonical/model-execution-identity-policy.yaml` | identidad criptográfica requerida antes del claim |
| `docs/canonical/capability-matrix.yaml` | autorización `model.invoke` y hard denies administrativos |
| configuración operacional | activación del adapter, endpoint HTTPS y referencia al archivo de credencial |
| PostgreSQL | request barrier, procedencia y outcome inmutable por dispatch attempt |

No existe un registro paralelo de provider/model. El adapter nunca acepta provider, model, endpoint, policy o credential reference desde `CreateInvocationCommand`, la CLI de dispatch ni el contenido de contexto.

## Provider habilitado

La rama compila únicamente:

```text
provider_id: openai_compatible
transport: http_adapter
adapter_id: openai_chat_completions
adapter_version: 1
request_schema_version: openai.chat.completions.request.v1
response_schema_version: openai.chat.completions.response.v1
```

`test.fake` continúa disponible solo en fixtures. `deepseek` y `alibaba_token_plan_via_claude_code` permanecen sin adapter ejecutable. Los transports `cli_adapter` siguen sin implementación.

La disponibilidad canónica indica que el binario conoce el adapter. La disponibilidad operacional requiere además que el adapter esté activado y registrado mediante configuración válida. Si está desactivado o su preflight falla, no se renderiza contexto ni se realiza una llamada externa.

## Egress policy v2

La policy productiva avanza a versión 2 y mantiene:

```text
secret   -> hard deny
clinical -> hard deny
default  -> deny
```

Los únicos allows productivos son:

```text
openai_compatible + public
openai_compatible + sanitized
```

`openai_compatible + organizational` permanece deny. Todas las clases de `deepseek` y `alibaba_token_plan_via_claude_code` permanecen deny. `test.fake` no aparece en el documento productivo.

El parser recibe una allowlist compilada exacta. Un `allow` adicional, aunque el YAML sea sintácticamente válido, se rechaza durante validate/sync. Por tanto, modificar solo el documento no puede ampliar egress sin modificar código, fitness y pruebas.

## Frontera de credenciales

Variables operacionales:

```text
ORG_MODEL_PROVIDER_OPENAI_COMPATIBLE_ENABLED=false
ORG_MODEL_PROVIDER_OPENAI_COMPATIBLE_ENDPOINT_URL=
ORG_MODEL_PROVIDER_OPENAI_COMPATIBLE_CREDENTIAL_FILE=
ORG_MODEL_PROVIDER_OPENAI_COMPATIBLE_REQUEST_TIMEOUT=2m
ORG_MODEL_PROVIDER_OPENAI_COMPATIBLE_CIRCUIT_FAILURE_THRESHOLD=5
ORG_MODEL_PROVIDER_OPENAI_COMPATIBLE_CIRCUIT_OPEN_DURATION=30s
```

Reglas:

- activación default `false`;
- endpoint absoluto HTTPS, sin userinfo, query ni fragment;
- path terminado en `/v1/chat/completions`;
- credential file absoluto, regular, acotado y sin permisos group/other en POSIX;
- symlinks rechazados para la credencial del provider;
- el token no puede contener whitespace ni control characters;
- raw token nunca entra a PostgreSQL, audit, outbox, errores clasificados ni logs del adapter;
- el archivo se lee en preflight y nuevamente inmediatamente antes de construir el request; el buffer se sobrescribe después de usarlo;
- solo se persiste `SHA-256(credential_file_path)` como referencia opaca;
- solo se persiste `SHA-256(normalized_endpoint_url)`, nunca el endpoint en claro.

No existen variables `API_KEY`, `ACCESS_TOKEN`, `BASE_URL`, selector de variable secreta ni headers arbitrarios.

## Transporte HTTP

El adapter usa exclusivamente la biblioteca estándar y configura:

- `Proxy: nil`, por lo que no hereda proxies del entorno;
- TLS mínimo 1.2;
- redirects rechazados;
- timeouts de dial, TLS, headers y request total;
- conexiones e idle pool acotados;
- respuesta leída con límite `ORG_MODEL_RUNTIME_MAX_RESPONSE_BYTES`;
- `stream: false`;
- `Authorization: Bearer <token>` solo en memoria;
- `X-Client-Request-Id` con el hash durable de idempotencia del dispatch attempt.

El adapter convierte `CanonicalRequest` al contrato del provider. No recibe mensajes libres ni headers desde el caller. El contexto renderizado se usa como un único mensaje `user`; la evidencia durable contiene su hash ya fijado, no los bytes.

Para `OutputJSON`, el adapter solicita `json_schema` cuando existe schema y `json_object` en caso contrario. La respuesta puede entregar texto simple o partes de texto. Tool calls se normalizan como intents; Rama 12 no los ejecuta.

## Preflight y circuit breaker

`Preflight` ocurre después de autorización y egress, pero antes del render y antes del request barrier. Verifica:

- scope provider/model/deadline;
- deadline vigente;
- circuit breaker cerrado;
- credencial legible y segura.

Un fallo de preflight termina `failed_before_send`, con cero render y cero HTTP. No crea `model_provider_requests`, porque no se cruzó el request barrier.

El circuit breaker es local al proceso y se abre por fallos de transporte o respuestas retryable según umbral. Una cancelación del caller no cuenta como inestabilidad del provider. El breaker no es un scheduler, no hace probes en background y se reinicia con el proceso.

## Request hash y evidencia durable

`BuildProviderRequestEvidence` calcula un hash canónico versionado que fija:

```text
adapter ID/version/schema versions
invocation y dispatch attempt
organization y revision
task y attempt
dispatch actor y subject
model profile/version
provider y provider model
provider idempotency hash
context snapshot y rendered hash
capabilities normalizadas
output mode y output-schema hash
límites de tokens, temperature, thinking/reasoning
deadline normalizado a microsegundos
endpoint fingerprint
credential-reference hash
```

No incluye los bytes de contexto, output schema en claro, token, URL, headers ni payload HTTP.

## Migración 000011

### `model_provider_requests`

Ledger append-only creado dentro de la transacción pre-send. Cada fila queda vinculada directamente a:

- invocation y dispatch attempt;
- egress evaluation allow;
- dispatcher assignment use;
- execution identity assertion;
- organization revision;
- model profile/version;
- provider/model y adapter version;
- request/response schema versions;
- request hash;
- endpoint y credential-reference fingerprints;
- provider idempotency hash;
- deadline.

Hay una fila como máximo por dispatch attempt. Evaluation, assignment use e identity assertion también son únicos en el ledger. UPDATE y DELETE se rechazan mediante trigger.

### `model_provider_outcomes`

Ledger append-only con una fila como máximo por provider request/dispatch attempt. Clasificaciones:

```text
response_received
provider_rejected
request_not_sent
ambiguous_transport
cancelled_confirmed
```

Persiste únicamente metadata acotada: provider request ID, HTTP status, error class/code normalizados, retryable, response hash, schema version y confirmación de cancelación. Nunca persiste cuerpo o mensaje de error del provider.

## Atomicidad y orden de dispatch

Orden obligatorio:

1. runtime e identidad configurados;
2. assertion Ed25519 y claim autenticado;
3. task/attempt/lease/assignment/revision/binding;
4. policy pins y context metadata/drift;
5. `model.invoke`;
6. egress evaluation;
7. adapter descriptor y preflight;
8. hard guard de clasificaciones;
9. render y rendered-hash verification;
10. request evidence;
11. una transacción PostgreSQL que inserta egress allow, consume assignment quota, inserta provider request y marca `send_started`;
12. commit;
13. HTTP externo;
14. provider outcome durable;
15. normalización y resultado terminal.

No existe HTTP antes del commit de `model_provider_requests`. Tampoco existe `send_started` sin egress allow, assignment use, identity assertion y provider request durable.

## Semántica de fallos y retry

### Antes del request barrier

Authorization deny, egress deny, adapter unavailable, preflight failure o render failure:

```text
failed_before_send
cero HTTP
sin provider request row
```

### Después del barrier, request probado como no enviado

Errores de encoding/build/credential detectados antes de `http.Client.Do`:

```text
request_not_sent
retry_safety = safe_before_send
terminal failed
```

La request row existe porque el barrier ya se confirmó, pero el outcome prueba que no hubo request externo.

### Respuesta conocida

HTTP no 2xx, body demasiado grande, JSON inválido o contrato inválido:

```text
provider_rejected
retry_safety = not_retryable en esta rama
terminal failed
```

Aunque un status pueda ser retryable semánticamente, Rama 12 no reintenta automáticamente después del barrier. Un worker futuro debe respetar el ledger y no volver a llamar la misma invocation.

### Resultado ambiguo

Timeout, redirect rechazado o error de transporte después de entregar el request al cliente HTTP:

```text
ambiguous_transport
invocation = ambiguous
retry_safety = not_retryable
```

No se intenta de nuevo porque el provider pudo haber aceptado el request.

### Respuesta exitosa

Se inserta `response_received` antes de normalizar o persistir el resultado. Si la normalización falla, el provider outcome exitoso se conserva y la invocation termina failed-after-response sin repetir la llamada.

### Cancelación

Una cancelación confirmada antes de `response_received` puede insertar `cancelled_confirmed`. Si ya existe un outcome `response_received`, la transición de runtime a cancelled no inserta un segundo outcome. Esto mantiene el invariante una llamada/una evidencia de outcome.

## Auditoría y privacidad

Audit/outbox conservan los eventos de invocation ya existentes y metadata acotada. No se añaden payloads con:

```text
rendered context
prompt/messages
request/response body
provider error message
Authorization header
token o credential path
endpoint URL
hidden reasoning
tool arguments sin normalizar
```

Errores del adapter exponen phase, class y code; `Cause` puede conservarse para control de flujo con `errors.Is`, pero `Error()` no incorpora el texto del provider/transport.

## Pruebas

La rama debe superar:

```bash
go test ./...
go test -race -short ./internal/modelruntime/...
go test -race -short ./internal/modelruntime/adapter/openaicompat/... ./internal/secrets/...
make test-model-provider-fitness
make test-model-runtime-integration
make test-model-egress-integration
make verify-all
```

Cobertura específica:

- endpoint y credential reference inválidos;
- permisos inseguros y whitespace en token;
- headers y request canónico;
- JSON schema y tool-call normalization;
- body bounded;
- error provider sin leak de message/token;
- timeout/transport ambiguity;
- circuit open;
- request/outcome provenance e immutability en PostgreSQL 17;
- rollback transaccional;
- `000011` up/down/reapply;
- resultados `request_not_sent`, `provider_rejected` y `ambiguous_transport`.

## Rollback

Rollback operacional preferido:

1. detener dispatchers/workers;
2. fijar `ORG_MODEL_PROVIDER_OPENAI_COMPATIBLE_ENABLED=false`;
3. restaurar una política canónica default-deny y sincronizar una revision explícita;
4. desplegar el binario anterior;
5. conservar `000011` y sus ledgers mientras exista evidencia sujeta a retención.

La down migration se usa solo en un entorno controlado cuando:

- no existe migración posterior;
- no hay procesos leyendo/escribiendo provider evidence;
- los registros se preservaron según retención;
- se acepta eliminar `model_provider_requests` y `model_provider_outcomes`.

La down elimina outcomes antes de requests y luego sus triggers/functions, sin CASCADE ni cambios a migraciones anteriores.

## Riesgos residuales

- El circuit breaker es in-memory y no coordina varios procesos.
- No hay active health probe ni health ledger; readiness se evalúa con config/preflight y fallos observados.
- El token existe brevemente en memoria de proceso y en el header HTTP; file boundary no equivale a HSM/Vault.
- TLS autentica el endpoint configurado, pero Rama 12 no implementa certificate pinning o mTLS.
- `X-Client-Request-Id` depende de que el provider compatible lo use; el ledger local sigue siendo la fuente para evitar retries inseguros.
- No hay streaming ni cancel API específica del provider.
- Un host comprometido puede leer memoria/archivo con los privilegios del proceso. Aislamiento de workload/host queda fuera de esta rama.

## Siguiente rama compatible

Rama 13 puede incorporar el persistent cell worker. Debe consumir `Dispatch(context.Context, invocationID)` mediante un puerto local, respetar `ambiguous`/terminal states y no implementar retries que creen una segunda llamada para una invocation con provider request durable. Rama 13 no debe recibir ni resolver credenciales del provider directamente.
