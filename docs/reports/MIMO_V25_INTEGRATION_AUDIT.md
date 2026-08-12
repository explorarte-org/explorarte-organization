# MIMO_V25_INTEGRATION_AUDIT.md

Auditoría del contrato real de MiMo-V2.5 (Xiaomi, Token Plan), basada en evidencia directa: llamadas reales hechas esta sesión contra la API (nunca inventadas) más documentación oficial pegada textualmente por el owner en esta conversación. Todo lo marcado **NO VERIFICADO** queda así explícitamente — no se completa con suposiciones.

## A. HEAD / branch / migration tip

- HEAD: `7cd60785683cb197b3941974d1727311447af4fa` (sin cambios).
- Branch: `feat/bootstrap-closure-observability-prolog`.
- Working tree: 37 archivos modificados/nuevos (acumulado de P0-A→F, R9.1, R10 — nada de esto se toca en esta fase salvo agregar el adapter MiMo).
- Migration tip: `000038_add_provider_failure_telemetry` (sin cambios; esta fase no requiere migración de schema nueva, solo el modelo de billing_mode=subscription, que cabe en columnas ya extensibles de `provider_wallet_events`/`model_invocation_usage`, ver sección D).

## B. Base URL y autenticación — verificado en vivo

- Base URL usada (la "dedicada" que dio el owner): `https://token-plan-sgp.xiaomimimo.com/v1`. Confirmado funcional contra `/v1/models` y `/v1/chat/completions` con llamadas reales.
- Header de autenticación **documentado oficialmente**: `api-key: $MIMO_API_KEY` (confirmado funcionando en múltiples llamadas reales de chat completions).
- **Nota no crítica**: la primera llamada de este audit contra `/v1/models` usó `Authorization: Bearer $KEY` y también devolvió 200 — no se investigó si ambos headers son aceptados de forma general o si fue específico de ese endpoint. **Para el adapter, usar `api-key` exclusivamente** (el formato documentado oficialmente, confirmado también contra `/v1/chat/completions`).
- Nunca se imprimió el valor de la key en ningún log/reporte. Se escribió a un archivo temporal en el VPS (`chmod 600`) solo durante cada smoke, y se borró (`shred -u`) inmediatamente después en cada ocasión.

## C. Modelos reales confirmados (vía `GET /v1/models`)

```
mimo-v2.5
mimo-v2.5-pro
mimo-v2.5-asr
mimo-v2.5-tts
mimo-v2.5-tts-voiceclone
mimo-v2.5-tts-voicedesign
```

Para este canario: **`mimo-v2.5`**. Se probó también `mimo-v2.5-pro` en el smoke exploratorio inicial (no forma parte del canario formal, ver sección K) y mostró un modo de fallo (`finish_reason:"abort"`) no visto en `mimo-v2.5` — razón adicional para no usarlo como challenger principal sin una investigación aparte.

## D. Protocolo — Chat Completions, forma OpenAI-compatible

`POST /v1/chat/completions`, confirmado real:

```json
{
  "model": "mimo-v2.5",
  "messages": [{"role": "system|user", "content": "..."}],
  "max_completion_tokens": 1500,
  "thinking": {"type": "enabled"|"disabled"},
  "response_format": {"type": "json_object"}
}
```

**No `max_tokens`** — el parámetro real es `max_completion_tokens` (confirmado: usar `max_tokens` habría sido silenciosamente ignorado o rechazado, no se probó esa variante por seguir la doc oficial directamente).

Respuesta real observada:
```json
{
  "id": "<string>",
  "choices": [{"finish_reason": "stop|length|abort", "index": 0, "message": {
    "content": "...", "role": "assistant", "tool_calls": null, "reasoning_content": "..."
  }}],
  "created": <unix ts>, "model": "mimo-v2.5", "object": "chat.completion",
  "usage": {
    "completion_tokens": N, "prompt_tokens": N, "total_tokens": N,
    "completion_tokens_details": {"reasoning_tokens": N},
    "prompt_tokens_details": {"cached_tokens": N}
  }
}
```

`id` es el identificador de respuesta a persistir como `provider_request_id` (no se observó un header HTTP separado tipo `x-request-id`; **NO VERIFICADO** si existe uno).

**Responses API**: NO VERIFICADO — no se probó, solo Chat Completions.

## E. Thinking (razonamiento) — confirmado, cambia el contrato de presupuesto

- `thinking.type` ∈ `{enabled, disabled}`. **Habilitado por defecto** en `mimo-v2.5`/`mimo-v2.5-pro` (documentación oficial pegada por el owner, confirmado empíricamente: sin especificar el campo, el primer smoke de esta sesión ya devolvía `reasoning_content` poblado).
- El razonamiento consume `completion_tokens` reales (`completion_tokens_details.reasoning_tokens`) — **el presupuesto de `max_completion_tokens` debe cubrir razonamiento + respuesta final**, no solo la respuesta. Confirmado en vivo: con `max_completion_tokens=20` en el primer smoke, `mimo-v2.5-pro` devolvió `content` vacío (`finish_reason:"length"`, todo el presupuesto se fue en `reasoning_content`).
- Con `thinking.type=disabled`, temperature/top_p SÍ son configurables (doc oficial); con `enabled`, se fuerzan a 1.0/0.95 sin importar lo que se envíe (doc oficial, no verificado empíricamente en esta sesión).

**Decisión para el adapter**: dejar `thinking` en su default (enabled) por ahora — es el comportamiento estándar del modelo, y desactivarlo para "ayudar" violaría la sección 26 del pedido (no dar ventaja/ninguna configuración especial a MiMo). El presupuesto de `max_completion_tokens` del adapter debe ser generoso para no truncar antes de terminar de razonar (ver sección I).

## F. Structured output — confirmado, requisitos reales

- `response_format:{"type":"json_object"}` **solo garantiza JSON sintácticamente válido**, no la estructura exacta (doc oficial, idéntico en espíritu al `json_object` de DeepSeek).
- **Confirmado empíricamente**: sin instrucción explícita de "no markdown/no explicaciones" en el prompt, `mimo-v2.5` devolvió el JSON envuelto en fence de markdown (` ```json ... ``` `) — el parser debe stripear el fence, O el prompt debe prohibirlo explícitamente. **Se eligió la segunda opción** (instrucción explícita en el prompt) para no depender de post-procesamiento frágil, siguiendo la propia recomendación de la doc oficial ("Enforce JSON-Only Output").
- No se necesita re-pedir `cluster_id` al modelo — la sección 9 del pedido ya lo prohíbe explícitamente, consistente con el fix ya aplicado al validador para DeepSeek (`cluster_id` es responsabilidad del runtime, no generativa).

## G. Cache — confirmado, mismo patrón que DeepSeek (0% hit observado)

`usage.prompt_tokens_details.cached_tokens` existe y se pobló con un valor real (192 de 252-256 tokens) en el primer smoke de esta sesión — **cache nativo del provider sí existe y se puede capturar**, a diferencia de DeepSeek donde solo se agregó soporte explícito. No se verificó el comportamiento de cache-hit para prompts largos/reales de corpus curation (los smokes de esta sesión fueron triviales) — se medirá con datos reales durante los 4 smokes del canario formal.

`prompt_tokens_details` también puede traer `audio_tokens`/`video_tokens` según la doc de video understanding pegada por el owner — no relevante para este canario (solo texto).

## H. Errores — NO VERIFICADO

No se provocó deliberadamente un error real (HTTP 4xx/5xx, JSON malformado del lado del provider) en las pruebas de esta sesión — no hay evidencia directa de la forma exacta del cuerpo de error. Se seguirá el mismo patrón defensivo que el adapter DeepSeek (parsear un envelope de error genérico tipo `{"error":{...}}`, con fallback seguro si no matchea). **A confirmar con evidencia real durante los smokes** (los 4 smokes probablemente producirán al menos un caso real de fallo dado el patrón ya visto con DeepSeek).

## I. Límites — parcialmente verificado

- `max_completion_tokens=1500` confirmado aceptado y suficiente para una respuesta corta con razonamiento. **Límite máximo real: NO VERIFICADO** — no se probó el techo. Para corpus curation con clusters grandes (18 Works), se necesitará un presupuesto sustancialmente mayor (mismo orden que DeepSeek, ~9,000-16,000, escalado por tamaño de cluster) — se usará la misma fórmula de escalado ya validada para DeepSeek (`max(3000, min(16000, 600+1200*n_works))`) como punto de partida, ajustable si se observan truncamientos por presupuesto en los smokes.
- Límite de contexto de entrada: NO VERIFICADO explícitamente, pero el ExecutionContextView proyectado de R10 (~31,000-58,000 tokens estimados) es un orden de magnitud razonable para un modelo de esta clase — se confirmará empíricamente en el Smoke 3 (18 Works, el payload más grande).
- Rate limits / cuota del Token Plan: **NO VERIFICADO**. No se observó ningún header de rate-limit en las respuestas capturadas esta sesión, y no se consultó ningún endpoint de cuenta/cuota. El cap duro de 34 requests para esta fase (impuesto por el owner) es la única protección real disponible — no hay forma de consultar cuota restante de manera confiable hoy.
- Timeout: NO VERIFICADO (todas las llamadas de esta sesión completaron en <5s).

## J. Arquitectura del adapter propuesta

`internal/modelruntime/adapter/mimo/` — mismo patrón exacto que `internal/modelruntime/adapter/deepseek/`: `Config`, `Adapter`, `Dispatch(ctx, request) (RawResponse, *AdapterError)`, reusando `AdapterFailureBeforeRequest/ResponseReceived/Ambiguous`, `ProviderOutcome`, `responseErrorOutcome`-equivalente, circuit breaker, y el mismo mecanismo de telemetría de fallos (Gate F) ya construido genéricamente en `internal/modelruntime/provider_adapter.go` (reusado sin cambios).

`response_format` se maneja igual que DeepSeek: `{"type":"json_object"}` + instrucción de schema-como-texto en el prompt (`jsonObjectModeInstruction`-equivalente) — NO se intenta ninguna variante de structured output más estricta sin evidencia de que el endpoint la soporte (consistente con la sección 9 del pedido).

## K. Capacidades a registrar en esta fase (solo verificadas)

```
text_input: verificado
long_context: parcialmente verificado (a confirmar con Smoke 3)
structured_output_json_object: verificado (con prompt explícito anti-markdown)
instruction_following: verificado (cumplió instrucciones de formato en smokes triviales)
usage_reporting: verificado (input/output/cache tokens reales)
```

**NO se registra**: `image`, `video`, `audio`, `tool_calling` (más allá del `web_search` plugin documentado, no probado con función custom) — ninguna tiene smoke evidence en esta fase, tal como exige la sección 19/57 del pedido. `mimo-v2.5-pro` tampoco se registra como capability separada todavía — mostró un modo de fallo (`abort`) sin explicación, fuera del alcance de este canario centrado en `mimo-v2.5`.

## L. Tests

Ver `internal/modelruntime/adapter/mimo/*_test.go` (implementados en la fase de código, sección siguiente) — cobertura: respuesta válida, JSON inválido, respuesta vacía, error HTTP, timeout pre-request, fallo ambiguo post-send, usage presente/ausente, request ID presente/ausente, modo `json_object` sin `strict`, cache tokens presentes/ausentes.

## M. TOKEN PLAN RESOURCE ACCOUNTING (agregado post-canario, auditoría de calidad)

El dashboard del Token Plan reporta `credits_consumed = 106,134,737` a la fecha de esta auditoría. Registrado con provenance explícita, sin conversión inventada:

```
billing_mode: subscription
provider_resource_unit: credit
provider_resource_consumed: 106,134,737
source: mimo_dashboard
observed_at: no capturado con timestamp preciso en esta sesión de herramientas (reportado por el owner)
measurement_scope: UNKNOWN
credit_semantics: unknown
```

**No se asume `1 credit = 1 token`.** No se verificó, ni en esta sesión ni en documentación oficial disponible, la fórmula credit→usage, si depende del modelo, si input/output/cached/reasoning tokens tienen multiplicadores distintos, si el contador incluye requests fallidos, si incluye los smokes, si el alcance es esta API key/plan o toda la cuenta, la ventana temporal, la cuota total, la cuota restante, ni la semántica de reset/rolling-window — todos permanecen **NO VERIFICADO**, consistente con la sección I de este mismo documento (rate limits/cuota ya marcados NO VERIFICADO antes del canario).

El valor bruto es ~87x mayor que el total de tokens reales medidos en toda la fase R10.2 (1,224,194 tokens en 25 requests reales) — evidencia circunstancial (no prueba) de que el contador probablemente no está aislado a este run. Por esto, **`credits/request` y `credits/accepted_execution` se reportan como `N/A`** (el `measurement_scope` no coincide de forma verificable con los 25 requests reales de esta fase). Detalle completo de la comparación económica en `DEEPSEEK_VS_MIMO_CURATION_CANARY.md` sección 7 y en `R10_2_FINAL_VERDICT.md`.

Este dato es económico/de capacidad, no de calidad — no altera ninguna conclusión de reliability o de la auditoría de calidad pareada (ver `DEEPSEEK_VS_MIMO_QUALITY_AUDIT.md`).

---

Con esta base evidenciada, se procede a implementar el adapter (sección 7-8 del pedido).
