# R10_3_MIMO_CREDIT_CALIBRATION.md

Parte C de R10.3. Objetivo: obtener un delta limpio de créditos del Token Plan y proyectar si Full Silver cabe en la cuota disponible.

## Snapshot del plan (owner-observed)

```
total credits:      4,100,000,000
observado antes:     106,134,737  (acumulado histórico previo a R10.3, ver MIMO_V25_INTEGRATION_AUDIT.md sección M)
```

## Ventana de calibración limpia

No existe API machine-readable de créditos verificada (confirmado ya en el audit original, sección I, NO VERIFICADO). Por instrucción explícita se paró en el checkpoint y se pidió al owner la lectura del dashboard, reutilizando el único request MiMo real de Parte A (Experimento A1, budget=4500) como la carga de calibración — sin gastar requests adicionales solo para medir cuota.

```
credits_before:        106,134,737   (asumido = última lectura conocida, sin otra carga MiMo conocida desde entonces)
credits_after:          109,433,284   (dashboard, leído inmediatamente después del request)
delta_credits:            3,298,547
measurement_window:    2026-08-11T22:51:54Z (before) -> invocation 261 en 22:52:05-22:52:32 UTC -> lectura after inmediata
provider_requests_in_window: 1 (invocation_id=261, mimo-v2.5)
contamination: false (ventana controlada, sin otra carga MiMo conocida de Organization en este intervalo)
```

## Métricas de créditos (empíricas, no contractuales)

```
credits/request (n=1):              3,298,547
provider tokens en la ventana:      38,882  (36,344 input + 2,538 output)
credits/provider_input_token:       ~90.8   (empírico, descriptivo -- NO identidad contractual)
credits/provider_output_token:      no separable de forma confiable con n=1 (un solo delta agregado input+output)
credits/token combinado:            ~84.84  (3,298,547 / 38,882)
```

**Reconciliación con el acumulado histórico** (evidencia circunstancial adicional, no prueba): el acumulado total de R10.2 (106,134,737 créditos) dividido por el total de tokens reales medidos en toda esa fase (1,224,194 tokens, 25 requests) da **~86.7 créditos/token** — notablemente cercano a la medición limpia de esta ventana (~84.84). Dos mediciones independientes (una agregada sobre 25 requests, una aislada de 1 request) convergen dentro de ~2%. Esto es evidencia razonable (no confirmación oficial) de que el contador de créditos observado en R10.2 es plausiblemente consistente con el volumen real de actividad de esa fase, y no un número de cuenta completamente desvinculado — pero **`credit_semantics` se mantiene `unknown`** porque no hay documentación oficial que confirme la fórmula, y una muestra de n=1-2 mediciones no es suficiente para declarar una tasa contractual.

## Proyección Full Silver

Corpus real: **2,009 clusters semánticos, 4,035 Works totales** (tamaño medio 2.01, mediana 1, cola larga hasta 77 Works por cluster — la mayoría son clusters pequeños de 1-2 Works, distinto de la muestra estratificada de 15 clusters usada en R10/R10.2, que sobre-representó clusters medianos/grandes).

Ajuste lineal real (mínimos cuadrados sobre los 14 puntos reales de MiMo en R10.2, `n_works` vs tokens observados):
```
input_tokens  ≈ 32,668 + 1,851.7 * n_works
output_tokens ≈  2,473 +   265.1 * n_works
```

Proyección de tokens totales sobre el corpus real (2,009 clusters, 4,035 Works):
```
LOW  (1er intento únicamente, sin reintentos):
  input  ≈ 73,126,026
  output ≈  6,037,936
  total  ≈ 79,163,962 tokens
  credits ≈ 79,163,962 * 84.84 ≈ 6,715,192,616  (~6.72B)

BASE (aplicando el ratio requests/accepted_outcome observado en R10.2 = 1.36, como proxy de reintentos a escala):
  total  ≈ 107,663,000 tokens
  credits ≈ 107,663,000 * 84.84 ≈ 9,133,839,320  (~9.13B)

HIGH (margen adicional de incertidumbre: 1.5x tokens por reintentos más pesados a escala + 20% de margen sobre el ratio credits/token):
  total  ≈ 118,745,943 tokens
  credits ≈ ~12,086,000,000  (~12.09B)
```

**Las tres proyecciones — incluso LOW, el escenario más optimista — exceden ampliamente el total del plan (4,100,000,000 créditos).** LOW sola ya representa ~164% de la cuota total disponible, antes de aplicar ninguna reserva de seguridad.

```
projected % quota (LOW):   ~163.8%
projected % quota (BASE):  ~222.8%
projected % quota (HIGH):  ~294.8%
```

## Reserva operacional

Política propuesta (no aplicada globalmente todavía, sección 33 del pedido): cuota utilizable ≤ 80% del plan = 3,280,000,000 créditos. Incluso bajo esta reserva más permisiva, **ninguna de las tres proyecciones cabe** — la brecha entre lo disponible (3.28B usable) y lo requerido (6.72B-12.09B) es de al menos ~2.44B créditos (LOW) hasta ~8.8B (HIGH).

## Off-peak discount — registrado, no aplicado a la proyección

Owner informó: 20% off durante horario off-peak (09:00-17:00 PDT), TTS gratis por tiempo limitado. **No se aplicó ningún descuento a la proyección anterior** porque no se verificó si el "20% off" aplica al consumo de créditos del Token Plan o solo a un precio PAYG paralelo (sección 34 del pedido — no asumir la semántica sin verificar). Aun aplicando un 20% de descuento hipotético sobre el escenario LOW (6.72B → ~5.37B), **la proyección seguiría excediendo el plan completo de 4.1B** — el descuento, aunque se confirmara aplicable 1:1 a créditos, no cambia la conclusión cualitativa.

## Remaining reserve tras R10.3

```
consumido acumulado tras R10.3 (1 request MiMo real, Parte A únicamente):  109,433,284
remaining:                                                                  3,990,566,716
% remaining:                                                                     ~97.33%
```

Sobra cuota amplia para seguir operando en modo canario/challenger — el problema es específicamente de **escala** (Full Silver, ~2,009 clusters), no de la operación actual.

## Gate C — Token Plan Capacity

```
existe medición limpia de créditos:        SÍ (1 ventana aislada + reconciliación cruzada con el acumulado R10.2)
se puede proyectar Full Silver:            SÍ
BASE projection cabe dentro del budget:     NO (222.8% de la cuota total, muy por encima incluso del 80% usable)
conserva reserva operacional razonable:     NO
```

**`PROJECTION = CONCLUSIVE_INSUFFICIENT`** (no `INCONCLUSIVE` — hay evidencia suficiente y convergente, de dos mediciones independientes, para concluir con confianza razonable que el plan actual no alcanza para Full Silver bajo ningún escenario de los tres calculados, incluso el más optimista).

**Gate C: FAIL.**

## No se hizo

No se gastaron los 4 requests MiMo restantes del presupuesto de calibración de créditos (máximo 5 permitidos) — la conclusión ya es inequívoca con 1 medición limpia más la reconciliación cruzada (LOW excede el plan por ~2.6B créditos, un margen demasiado grande para que precisión adicional cambie la conclusión binaria). No se investigó una API de cuota machine-readable más allá de lo ya documentado como NO VERIFICADO en `MIMO_V25_INTEGRATION_AUDIT.md`. No se convirtió ningún crédito a USD.


---

**Historical runtime evidence referenced by R9–R10.5 was destroyed in the development-database incident of 2026-08-12. The reports and committed implementation remain intact, but the referenced database rows are no longer independently queryable.**
