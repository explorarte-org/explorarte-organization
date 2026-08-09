# Runbook: desplegar el sidecar local BGE-M3

Este documento cubre el proceso operativo, no el adapter Go (`internal/embeddingruntime/adapter/bgem3`, ya construido y probado contra un servidor fake — ver su paquete de tests). Descargar pesos reales y correr un proceso de inferencia real es una acción con consumo de disco/RAM/CPU no trivial; este runbook existe precisamente para no improvisarla.

## Gate de hardware (autorización exacta, no reinterpretar)

> Descargar y ejecutar el sidecar está autorizado en este VPS exclusivamente como prueba controlada de R30; el despliegue productivo será repetido en el VPS equivalente de 14 GB.

Condiciones fijadas por el owner para esta prueba en el VPS actual (2 vCPU, ~8GB RAM, ~2GB swap):
- Verificar espacio en disco para pesos+runtime+capas **antes** de descargar nada.
- Una sola instancia. Concurrencia inicial = 1. Batch de query = 1. Batches de ingesta pequeños/adaptativos.
- Límites de memoria/cola/tokens/timeouts en su lugar (ver variables de entorno abajo — ya reflejan esto).
- Medir: RSS pico, CPU, swap, latencia p50/p95/p99, throughput.
- Detener la prueba ante OOM, swap sostenido, o degradación seria de Postgres/orgd.
- Nunca borrar imágenes/volúmenes/datos para liberar espacio sin autorización explícita.
- Un fallo por memoria en este VPS **no** es un rechazo definitivo de BGE-M3 — solo bloquea esta prueba puntual.
- Si funciona correctamente en 8GB, queda aprobado como candidato para el VPS de 14GB; ahí se repite smoke test + benchmark. 14GB da margen de memoria pero no mejora sustancialmente la velocidad de inferencia (mismos 2 vCPU).
- La aprobación final requiere que el servicio no dependa de swap para operación normal, y registrar la configuración exacta probada (runtime, revisión de modelo, precisión/cuantización, hilos, longitud máxima, batch, consumo pico).

## Paso 0 — verificar espacio en disco antes de descargar nada

```
df -h /
```

BAAI/bge-m3 en `fp32` pesa aproximadamente 2.2GB; en `fp16`/cuantizado bf16 aproximadamente 1.1-1.3GB; sumar el runtime (FlagEmbedding + dependencias Python, ~1-2GB adicionales con torch CPU). Si el espacio libre medido es menor a ~2x el tamaño estimado del artefacto elegido más el runtime, **detener aquí** y reportar la discrepancia — no proceder a descargar "a ver si entra". La discrepancia real observada al iniciar esta rama (4.8GB libres medidos vs. ~6.45GB de la especificación original) ya quedó registrada en `docs/implementation/branch-30-canary-evaluation-bge-m3/DESIGN.md`; volver a medir aquí, no asumir que sigue igual.

## Paso 1 — aprovisionar el proceso (una sola vez, sin red después)

El sidecar es un proceso Python separado, nunca embebido en `orgd` (`orgd` es Go puro, sin subprocesos — ver `scripts/check-embeddingruntime-fitness.sh`). Referencia inicial: runtime oficial FlagEmbedding.

1. Crear un usuario sin privilegios dedicado (`bgem3`, sin shell de login, sin sudo).
2. Descargar el artefacto del modelo UNA vez, verificar su SHA-256 contra el valor pinneado, y montar el directorio de pesos como **solo lectura** para el proceso del sidecar.
3. Instalar el runtime (venv aislado, dependencias pinneadas por hash/lockfile).
4. Después de este paso, el proceso del sidecar **no debe tener acceso de red saliente** — ni para telemetría, ni para re-descargar el modelo. Aplicar esto a nivel de firewall/namespace, no solo por configuración de la aplicación.
5. El proceso **nunca** auto-descarga el modelo al arrancar — si el artefacto pinneado no está presente y verificado, el proceso debe fallar al arrancar (fail-closed), nunca intentar obtenerlo de la red.

## Paso 2 — exponer el contrato HTTP que el adapter Go espera

`internal/embeddingruntime/adapter/bgem3` habla HTTP JSON contra:

- `POST /v1/embed` — cuerpo `{"model_revision","prompt_template_version","idempotency_key","items":[{"key","text","task","input_hash"}]}`, respuesta `{"model_revision","dimension","results":[{"key","vector"}],"text_count","cpu_time_ms"}`.
- `GET /v1/health` — respuesta `{"status","model_revision","artifact_sha256","dimension","queue_depth","peak_rss_bytes","cpu_time_ms","processed_count"}`. Endpoint separado de `/v1/embed` a propósito — el healthcheck nunca debe competir por el mismo slot de concurrencia acotada que un embed real.

El sidecar debe escuchar **solo** en loopback (`127.0.0.1`) o un socket Unix — nunca en `0.0.0.0` ni una interfaz pública. El adapter Go (`Config.Validate`) ya rechaza cualquier `BASE_URL` que no sea loopback/`unix://`, pero eso solo protege el lado cliente; el propio proceso del sidecar debe reforzar el mismo límite en su bind address.

## Paso 3 — variables de entorno del adapter Go

```
ORG_EMBEDDING_PROVIDER_BGE_M3_ENABLED=true
ORG_EMBEDDING_PROVIDER_BGE_M3_BASE_URL=http://127.0.0.1:8091      # o unix:///run/bgem3/sidecar.sock
ORG_EMBEDDING_PROVIDER_BGE_M3_MODEL_REVISION=<revisión pinneada exacta>
ORG_EMBEDDING_PROVIDER_BGE_M3_ARTIFACT_SHA256=<sha256 de 64 hex del artefacto de pesos>
ORG_EMBEDDING_PROVIDER_BGE_M3_REQUEST_TIMEOUT=30s
ORG_EMBEDDING_PROVIDER_BGE_M3_MAX_CONCURRENCY=1                    # gate de hardware: 1 en el VPS de prueba actual
ORG_EMBEDDING_PROVIDER_BGE_M3_MAX_QUEUE_DEPTH=1
ORG_EMBEDDING_PROVIDER_BGE_M3_MAX_INPUT_BYTES=32768
ORG_EMBEDDING_PROVIDER_BGE_M3_MAX_ITEMS_PER_REQUEST=16
```

## Paso 4 — smoke test

```
curl -s http://127.0.0.1:8091/v1/health | jq .
```

Confirmar `status:"ready"`, `dimension:1024`, y que `model_revision`/`artifact_sha256` coinciden EXACTAMENTE con las variables de entorno de arriba — el adapter Go rechaza (`ErrModelIdentityDrift`) cualquier discrepancia, así que un mismatch aquí bloqueará todo antes de llegar a `orgd`.

## Paso 5 — medición y criterios de parada

Registrar, para la configuración exacta probada: RSS pico del proceso sidecar (`ps -o rss= -p <pid>` o `/proc/<pid>/status`), CPU% sostenido, uso de swap (`free -h` antes/durante/después), latencia p50/p95/p99 de `/v1/embed` con textos representativos, throughput (textos/segundo con concurrencia=1).

Detener inmediatamente y reportar si: el proceso es matado por OOM killer, `free -h` muestra swap creciendo sostenidamente durante la prueba (no solo un pico transitorio), o Postgres/`orgd` muestran latencia degradada mientras el sidecar corre en paralelo.

## Promoción al VPS de 14GB

Repetir smoke test (Paso 4) + medición (Paso 5) en el VPS de 14GB con la MISMA configuración exacta (mismo runtime, misma revisión de modelo, misma precisión/cuantización, mismos hilos, misma longitud máxima, mismo batch) antes de declarar aprobación productiva. La aprobación final exige que el servicio no dependa de swap para operación normal en ese VPS.
