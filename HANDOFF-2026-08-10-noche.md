# Handoff — sesión Model Runtime production hardening, noche 2026-08-10

Rama: `feat/model-runtime-production-hardening` (sobre `main`, sin merge, sin push).
Último commit: `409092f` (mas los pendientes de esta nota).

## Lo que quedó verde y probado con dinero real

- **Fase C (Context Source)**: inmutable, horneado en la imagen (`/opt/explorarte/context-source`), sin depender del mount del repo host. Verificado con `docker inspect --format '{{.Mounts}}'` vacío en el contenedor persistente + `orgctl context build` real para `negocio/director_negocio` e `ingenieria_ia/code-runner`: 14/14 segmentos, 0 omitidos.
- **Fase D (Execution Identity)**: `orgctl model worker run` corre como servicio propio `model-worker` en compose.yaml (orgd nunca dispatcha, confirmado en el código: `cmd/orgctl/worker.go` lo documenta explícito). Key montada solo como archivo individual, nunca el directorio de secrets completo.
- **Fase E (Provider secrets)**: DeepSeek habilitado y probado. OpenAI-compatible habilitado vía Chat Completions (Responses API queda pendiente, requiere adapter nuevo).
- **Fase F/G (canary DeepSeek)**: **éxito completo end-to-end.** task 2 → assignment → budget (`orgctl budget create-root`, comando nuevo que agregué) → context (scope `department_worker`) → invocation 3 → dispatch → `succeeded`. 76,049 input / 2,487 output tokens. Cobro real: ~$0.0113 USD debitado de la wallet de DeepSeek.

## Lo que quedó pendiente — canary CEO/OpenAI (gpt-5.6-luna)

Task 3 sigue `running` (attempt 4, lease hasta las ~06:40 UTC del 2026-08-10, se puede re-extender con `task heartbeat`). El plan de Object Storage + RAG **nunca se generó** — 4 invocaciones distintas fallaron, cada una por una causa real distinta:

| Invocation | max_output_tokens | Resultado | Causa real |
|---|---|---|---|
| 4 | 8,000 | huérfana → `ambiguous` | `response_truncated_empty`: reasoning `xhigh` se comió todo el presupuesto antes de emitir texto |
| 5 | 200,000 | `failed` en ~3s | `invalid_value`: excede el tope real del modelo. **Confirmado por el usuario: max output real de gpt-5.6-luna = 128,000 tokens** |
| 6 | 32,000 | huérfana en `send_started` | `ambiguous_after_request` / `transport_error` |
| 7 | 100,000 | huérfana en `send_started` | `ambiguous_after_request` / `transport_error` (segunda vez, mismo patrón) |

**Hallazgo importante, no resuelto:** `transport_error` (código en `internal/modelruntime/adapter/openaicompat/adapter.go:388`) es DISTINTO de `transport_timeout` (que sería `context.DeadlineExceeded`, o sea el timeout de 2 min del cliente). Descarté que sea el timeout — es un error de red real (conexión reseteada, TLS, etc.), y pasó dos veces seguidas con el mismo payload grande (~76K tokens de contexto de entrada). Conectividad general a `api.openai.com` desde el VPS está bien (`curl` responde en <1s). Sospecha sin confirmar: algo específico de requests HTTP muy grandes (76K+ tokens ≈ varios cientos de KB de body) desde este VPS/red hacia OpenAI. **A investigar de día**, no de noche.

**Bug operativo real que sí encontré y corregí a medias:** el override `ORG_MODEL_PROVIDER_OPENAI_COMPATIBLE_REQUEST_TIMEOUT=30m` que usé en mis `docker compose run` manuales **nunca se agregó al servicio persistente `model-worker` en compose.yaml** — sigue con el default de 2 minutos ahí. Como `model-worker` siempre gana la carrera de claim contra el dispatch manual, TODOS los intentos reales corrieron con el timeout de 2 min de default. Dado que el error real fue `transport_error` (no `transport_timeout`), esto no fue la causa de los fallos vistos, pero de todas formas es una discrepancia real de configuración que hay que corregir antes de seguir probando: agregar `ORG_MODEL_PROVIDER_OPENAI_COMPATIBLE_REQUEST_TIMEOUT: ${...:-5m}` al bloque `environment` de `model-worker` en `compose.yaml` (ahora mismo NO está ahí, solo en `orgd` no existe siquiera porque orgd nunca dispatcha).

## Invocaciones huérfanas pendientes de reconciliar

- Invocation 6: `claim_expires_at` = `2026-08-10T06:33:35Z`
- Invocation 7: `claim_expires_at` = `2026-08-10T06:31:29Z`

Ambas se liberan solas corriendo `orgctl model invocation reconcile --json` después de esas horas. No requieren acción manual, solo tiempo.

## Próximos pasos sugeridos para mañana

1. Agregar `ORG_MODEL_PROVIDER_OPENAI_COMPATIBLE_REQUEST_TIMEOUT` al servicio `model-worker` en `compose.yaml` (gap real encontrado).
2. Reconciliar invocations 6 y 7.
3. Reintentar con `max_output_tokens` entre 60,000-100,000 (confirmado seguro bajo el tope de 128,000), un deadline más corto y realista (5-8 min en vez de 15-30), y observar si el `transport_error` se repite — si sí, es un patrón real de red a investigar (proxy, MTU, keepalive), no una casualidad.
4. Considerar DeepSeek como plan B para pedir el plan de Object Storage/RAG si OpenAI sigue inestable — ya está probado y funciona limpio.
5. Cuando el plan esté en mano: NO empezar la ingesta de 2.3M de palabras todavía (acordado con el owner) — eso es un bloque de trabajo separado (Object Storage + pipeline), y cualquier automejora/autonomía de la organización necesita supervisión explícita, no debe correr desatendida.

## Invariantes de seguridad respetados toda la noche

Sin bypass de authorization/egress/execution identity/CostGate en ningún momento. Sin secrets impresos. Sin `docker compose config`. Sin mount del directorio completo de secrets. Sin auto-migrate. Sin staging habilitado. Sin tocar la private key de execution identity.

---

## Update — adapter de Responses API (agregado después del handoff original)

Se construyó `internal/modelruntime/adapter/openairesponses` (commit `dab4eca`), un adapter nuevo y completo para `POST /v1/responses`, distinto del `openaicompat` (Chat Completions) que ya funciona con DeepSeek.

**Estado: compilado, con 9 tests pasando, NO habilitado.** `ORG_MODEL_PROVIDER_OPENAI_RESPONSES_ENABLED` sigue en `false` en todos lados. `model-routing.yaml` sigue apuntando `executive.ceo` a `openai_compatible`. Nadie lo despachó contra la API real todavía — el shape del request/response se confirmó contra la documentación oficial y varios ejemplos de eventos `response.*` que pasó el owner durante la sesión (incluye una corrección real: el error top-level usa `code`, no `type`; `incomplete_details.reason` es `max_tokens`, no `max_output_tokens` — ambos corregidos antes de commitear).

**Próximos pasos para usar esto mañana:**
1. Crear `/etc/explorarte/secrets/openai-responses-api-key` (mismo patrón que los otros: `sudo install -m 600 -o 65532 -g 65532 /dev/null ...`) — puede ser la misma key de OpenAI que ya está en `openai-compatible-api-key`, o una nueva.
2. Agregar al `compose.yaml` (servicio `model-worker`): `ORG_MODEL_PROVIDER_OPENAI_RESPONSES_ENABLED=true`, `ORG_MODEL_PROVIDER_OPENAI_RESPONSES_ENDPOINT_URL=https://api.openai.com/v1/responses`, `ORG_MODEL_PROVIDER_OPENAI_RESPONSES_CREDENTIAL_FILE=/run/secrets/openai-responses-api-key`, y el mount del secret.
3. Decidir si `model-routing.yaml` (`executive.ceo`) pasa a `openai_responses` en vez de `openai_compatible` — eso es un cambio de canonical + `registry sync` + `model registry sync`, no solo env.
4. Probar con un canary chico primero (no repetir el error de esta noche de ir directo a 200,000 tokens — usar algo como 20,000-30,000 para la primera prueba real).
5. No está implementado: streaming, tools nativos (web_search, code_interpreter, computer_use, MCP), el beta de multi-agent, ni threading de `previous_response_id`/`conversation` — quedan fuera de alcance hasta que se necesiten.
