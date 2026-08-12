# R10_4_1_DEEPSEEK_FULL_SILVER_PROJECTION.md

Proyección preliminar de ejecutar el corpus completo con DeepSeek como primary bajo production-config (`ProviderRender v1` + floor 4500). Disparada porque 4/4 sentinels se recuperaron (≥3/4, criterio de la sección 23 del pedido). **Proyección, no ejecución** — Full Silver no se corre en esta fase.

## Corpus real

```
cluster count:              2,009
total Works:                4,035
tamaño medio:                2.01
mediana:                     1
distribución real (buckets):
  1-2 Works:   1,689 clusters (84.1%)
  3-5 Works:     233 clusters (11.6%)
  6-10 Works:     52 clusters (2.6%)
  11-18 Works:    20 clusters (1.0%)
  19+ Works:      15 clusters (0.7%, máximo observado 77)
```

## Ajuste de tokens (regresión lineal sobre los 9 puntos reales de producción-config: 5 de R10.4 + 4 de R10.4.1, ambos bajo ProviderRender v1)

```
input_tokens  ≈ 8,734.6 + 415.6 * n_works
output_tokens ≈ 1,749.4 + 519.7 * n_works
```

Nota: esta pendiente es más plana que la del ajuste de R10.2/R10.3 (que usaba render legacy) porque `ProviderRender v1` ya elimina el desperdicio de JSON de auditoría del prefijo estable — el término constante (~8,735 input) refleja el `StablePrefix` real (27,123 bytes ≈ 7,552 tokens observados de cache) más el overhead fijo no cacheado, y la pendiente refleja el costo marginal por Work del `DynamicSuffix`.

## Proyección de tokens (usando representantes de bucket: 1.5, 4, 8, 14.5, 30 Works)

| bucket | clusters | input proyectado | output proyectado |
|---|---:|---:|---:|
| 1-2 | 1,689 | 15,807,942 | 4,271,412 |
| 3-5 | 233 | 2,422,501 | 892,171 |
| 6-10 | 52 | 627,089 | 307,164 |
| 11-18 | 20 | 295,216 | 185,701 |
| 19+ | 15 | 318,039 | 260,106 |
| **Total (1er intento, sin retry)** | **2,009** | **19,470,787** | **5,916,554** |

## Retry amplification

`requests/accepted_outcome` observado: R10.4 = 1.0 (5/5, 0 reintentos), R10.4.1 = 1.25 (5/4, 1 reintento). Blend conservador para la proyección: **1.2x** (BASE), con **1.0x** (LOW, optimista) y **1.5x** (HIGH, pesimista) como bandas.

## Escenarios de cache (eje principal LOW/BASE/HIGH, sección 24)

**LOW/BEST** — cache hit ≈ el patrón estable observado (StablePrefix completo, ~7,552 tokens/request, cerca del máximo posible dado que el prefijo es ~27,123 bytes/~7,552 tokens de un input típico de ~9,500-16,000 tokens):
```
requests proyectados (BASE retry 1.2x): 2,009 * 1.2 ≈ 2,411
hit tokens ≈ 2,411 * 7,552 ≈ 18,206,872 (capado por el input total disponible)
input total (retry 1.2x): 19,470,787 * 1.2 = 23,364,944
miss tokens = 23,364,944 - 18,206,872 ≈ 5,158,072
```

**BASE** — cache hit ratio conservador basado en el promedio real observado en R10.4+R10.4.1 combinados (más cercano al 41.1% del canario R10.4 que al 78.6% del recheck R10.4.1, para no sobreconfiar en n=9):
```
hit tokens ≈ 23,364,944 * 0.411 ≈ 9,603,000
miss tokens ≈ 13,761,944
```

**HIGH/WORST** — 0% cache hit (el escenario que demuestra viabilidad económica INCLUSO si el cache desapareciera):
```
miss tokens = input total = 23,364,944 (100% miss)
```

output tokens (no cacheable, mismo en los 3 escenarios, con retry 1.2x): 5,916,554 * 1.2 ≈ 7,099,865

## Costo estimado (USD, PAYG real de DeepSeek)

**Metodología**: tasa empírica derivada de los 9 requests reales de producción-config ($0.07217980 total / 100,428 tokens no-cacheados [miss+output] = $0.000000719/token). No se usó ninguna tabla de precios hardcodeada -- esta es una tasa efectiva observada directamente del accounting real (`provider_wallet_events`), aplicada solo a tokens no-cacheados (se asume que los tokens con cache hit tienen costo marginal ≈$0, consistente con el descuento de cache que DeepSeek ya aplica). **Limitación explícita**: no se confirmó la tabla de precios oficial exacta de DeepSeek en esta sesión -- esta es una tasa efectiva empírica, no una cifra contractual verificada.

| escenario | miss tokens | output tokens | tokens no-cacheados | costo estimado |
|---|---:|---:|---:|---:|
| LOW | 5,158,072 | 7,099,865 | 12,257,937 | **≈ $8.81** |
| BASE | 13,761,944 | 7,099,865 | 20,861,809 | **≈ $15.00** |
| HIGH | 23,364,944 | 7,099,865 | 30,464,809 | **≈ $21.90** |

## Lectura económica

**El costo de Full Silver con DeepSeek es trivial en dólares absolutos bajo cualquiera de los tres escenarios (single-digit a low-double-digit USD)** — un contraste directo y relevante para la decisión de estrategia frente al Token Plan de MiMo, que (según `R10_3_MIMO_CREDIT_CALIBRATION.md`) proyecta 6.72B-12.09B créditos contra una cuota total de 4.1B, es decir, insuficiente incluso en el escenario más optimista. Para DeepSeek, el eje que más mueve el costo no es el cache (LOW vs HIGH difieren solo ~2.5x, ambos de un orden de magnitud de decenas de dólares) sino el **retry amplification** — y ese eje ya está acotado por la política de reintentos (máx 2), no por el cache.

**Separación explícita** (sección 24 del pedido): viabilidad económica ≠ disponibilidad de cache. Incluso si el cache de DeepSeek dejara de funcionar por completo mañana (HIGH/WORST), Full Silver seguiría siendo económicamente trivial (~$22 total). El cache es valioso para throughput/latencia observada, no es la variable que determina si Full Silver es pagable.

## Supuestos y limitaciones (declarados explícitamente)

- El ajuste lineal usa solo 9 puntos reales, todos de clusters de 2-18 Works (nunca se ha corrido production-config sobre un cluster de 19+ Works real) — la proyección para el bucket 19+ (15 clusters, hasta 77 Works) es una extrapolación fuera del rango observado, con mayor incertidumbre.
- El representante "30" para el bucket 19+ es una aproximación (rango real 19-77) — no se tiene la distribución exacta dentro de ese bucket.
- La tasa $/token no-cacheado es empírica (9 requests reales), no la tabla de precios oficial verificada de DeepSeek.
- No se proyecta `retry amplification` por bucket de tamaño (se asume la misma tasa 1.2x/1.0x/1.5x en todos los tamaños) — es posible que clusters grandes tengan una tasa de reintento distinta a la observada en clusters pequeños/medianos, no medido.
