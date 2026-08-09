# Rama 31 — Gobernanza de tokens y contexto de larga duración

## Estado y base

- Rama: `branch-31-token-context-governance`.
- Base exacta: `1e2b3966bbcba99724c31f3f95887858be721089`, cierre documentado de R30.1.
- Worktree aislado: `/opt/explorarte/worktrees/branch-31-token-context-governance`.
- R30.1 llegó a esta base con `gofmt`, `go vet`, `go build`, unit tests, integración completa contra PostgreSQL real y `make verify` limpios en cada commit.
- Los archivos ajenos `compose.integration.bugreview07.yaml` y `organization canonical` permanecen únicamente en el worktree de `main`; no forman parte de esta rama.
- Migraciones: reconfirmar el tip real inmediatamente antes de cada fase; no reservar números desde este diseño.

## Por qué existe esta rama

La organización ya tiene varias propiedades correctas que no deben perderse: snapshots de contexto inmutables y revalidables, precedencia por autoridad, clasificación de datos, autorización por namespace, ledger de costes con reserva previa, mensajería durable, DAG de decisiones y retrieval híbrido RAG/Memory.

La brecha no es “tener más memoria”. La brecha es que el mismo artefacto JSON exhaustivo usado como evidencia auditable también se envía completo al modelo. Hoy el sistema limita por bytes, no por tokens; serializa `[]byte` como base64; descarta parte de la relevancia calculada por RAG; no contabiliza tokens cacheados/reasoning informados por el proveedor; y no posee una representación compacta y progresivamente desplegable para tareas largas.

R31 separa esas dos responsabilidades:

```text
fuentes gobernadas
  -> snapshot canónico íntegro, inmutable, revalidable       # auditoría
  -> proyección de ejecución compacta, determinista y acotada # inferencia
       -> prefijo estable/cacheable
       -> estado activo verificado
       -> evidencia relevante
       -> referencias recuperables a detalle externo
```

El objetivo no es alcanzar un porcentaje publicitario. Es reducir tokens frescos y coste real sin degradar calidad, procedencia, autorización ni capacidad de reconstruir cada decisión.

## Estado heredado y gates de arranque

R30.1 cerró sus seis hallazgos correctivos, pero no convirtió R30 en una plataforma completamente ejercitada:

- BGE-M3 real no está instalado ni medido; solo existe el adapter Go probado contra servidor fake.
- 9 de 14 fixtures tienen runner real tras el primer slice ejecutable de R31; 5 continúan declarados `pending`.

Estos pendientes bloquean cosas distintas:

| Acción | BGE-M3 real | 14/14 runners |
|---|---:|---:|
| Abrir R31 y medir tokens/caché actuales | no bloquea | no bloquea, pero el reporte debe decir 9/14 mientras queden cinco pendientes |
| Cambiar renderer/contexto y promoverlo globalmente | no bloquea | exige runners específicos de contexto más la suite existente; no puede llamarse validación total |
| Ingerir el corpus AI en el espacio BGE-M3 | **bloquea** | exige al menos todos los runners RAG/Memory/namespace relacionados |
| Habilitar autoauditoría/autoterminación organizacional | **bloquea** para flujos que dependan del corpus | **bloquea: requiere 14/14** |

La instrumentación puede comenzar con cobertura honesta. Ningún `run` 4/14 puede presentarse como aprobación completa ni habilitar promoción automática.

## Decisiones de arquitectura fijadas

1. **El snapshot canónico no se comprime ni se reemplaza.** Continúa siendo la fuente de verdad auditable.
2. **El modelo no recibe el snapshot canónico serializado literalmente.** Recibe una vista derivada, inmutable y enlazada por hash al snapshot original.
3. **No se elimina información para “ahorrar”.** El detalle sale de la ventana activa y queda recuperable por identificadores autorizados.
4. **Los presupuestos pasan a ser de tokens además de bytes.** Los bytes siguen siendo un límite defensivo, no el estimador principal de inferencia.
5. **Los contadores reportados por el proveedor prevalecen para reconciliación.** Las estimaciones solo sirven para reservar antes de la llamada y deben quedar etiquetadas como estimaciones.
6. **El caché se mide, no se supone.** La identidad desconocida detrás de `gpt-5.6-luna` (D-005) impide prometer semántica de caché específica de OpenAI hasta verificar el endpoint real. DeepSeek sí expone hit/miss y debe contabilizarse.
7. **La selección de contexto conserva autoridad, procedencia y diversidad.** La relevancia nunca puede elevar privilegios ni convertir datos no confiables en instrucciones.
8. **La memoria de ejecución larga es un árbol de estado verificado, no un top-k plano de trazas.** Las ramas fallidas se conservan fuera de la ruta activa; no se mezclan silenciosamente con el estado vigente.
9. **La comunicación multiagente sigue siendo punto a punto y durable.** No se introduce debate totalmente conectado. La revisión divergente solo se activa por riesgo o contradicción verificable.
10. **La organización puede autoauditar y proponer trabajo, pero no autopromover arquitectura, migraciones vivas ni acciones irreversibles.** Los gates humanos permanecen encendidos.
11. **La ingesta del corpus AI se hace después de R31 y por canario.** La transferencia al VPS no equivale a admisión ni indexación.

## Diagnóstico de la arquitectura actual

### Fortalezas que se preservan

- `internal/contextengine`: orden determinista por autoridad, límites fail-closed, snapshot inmutable, hashes y revalidación.
- `internal/rag` e `internal/memory`: autorización y lifecycle; R29/R30 permiten búsqueda híbrida y separan físicamente los espacios Gemini 768/BGE-M3 1024.
- `internal/modelruntime`: reserva antes de tocar el proveedor; schemas de salida, límites de respuesta y modos de razonamiento ya existen.
- `internal/modelpricing` y `internal/costledger`: ya modelan precio de entrada cacheada y escritura de caché, aunque el adapter no entregue aún ese desglose.
- `internal/decisiongraph`: evidencia terminal verificada, profundidad dirigida y referencias a snapshots; es una base adecuada para checkpoints de subobjetivos.
- `internal/agentmessaging`: mensajes durables con correlación, causalidad, lease e idempotencia; la topología ya es más esparsa que MAD clásico.

### Brechas demostradas en código

- `AssemblyInput` limita `MaxTotalBytes`, `MaxSegmentBytes` y conteos, no tokens por modelo.
- El límite por defecto de contexto es 512 KiB, muy superior a lo razonable para varios modelos y sin relación exacta con su tokenizer.
- `portableSegment.Content []byte` se serializa en JSON como base64, añadiendo aproximadamente un tercio en bytes antes de tokenizar.
- Memory/RAG ya construyen payloads JSON y luego esos bytes vuelven a encapsularse en el JSON exterior.
- `SourceRecord.Relevance` y `ProviderPriority` se fijan uniformemente, aunque RAG calculó scores; la selección pierde una señal disponible.
- La reserva usa una aproximación `len(renderedContext)/3 + 1`, independiente del modelo.
- `Usage` no distingue entrada fresca, lectura de caché, escritura de caché ni tokens de razonamiento.
- La reconciliación entrega cero tokens cacheados; por diseño cobra toda la entrada como fresca y sobreestima.
- Los adapters OpenAI-compatible/DeepSeek descartan detalles de caché que sus APIs pueden reportar.
- Los mensajes permiten JSON arbitrario sin presupuesto de tokens ni contrato de delta/artifact reference.
- No existe hoy un catálogo MCP masivo inyectado al prompt. Tool Search para MCP no resuelve un problema actual y queda fuera.

## Arquitectura objetivo

### Dos artefactos, una procedencia

```text
ContextSnapshot (canónico)
  id, content_hash, policy_hash, segmentos completos
           |
           | renderer_version + token_profile + selection_policy
           v
ExecutionContextView (derivado e inmutable)
  snapshot_id
  content_hash
  stable_prefix_hash
  fresh_input_tokens_estimated
  segmentos seleccionados + referencias a omitidos
  texto compacto estructurado, sin base64
```

La vista derivada debe poder regenerarse de forma determinista. Su hash, versión de renderer, perfil de tokenizer y política de selección quedan persistidos. Una vista nunca altera el snapshot del cual deriva.

### Forma compacta propuesta

No adoptar XML ni TOON por dogma. Primero comparar, con los tokenizers y modelos realmente ruteados, al menos:

1. JSON portable actual (baseline).
2. Texto delimitado compacto y estable.
3. JSON compacto con proyección de campos.

La representación candidata debe conservar explícitamente:

```text
[CTX v1 snapshot=<id> hash=<hash>]
[SEG id=<id> authority=<tier> trust=<class> data=<class> source=<ref>]
contenido textual sin base64
[/SEG]
[OMITTED id=<id> reason=<reason> fetchable=true]
```

Los delimitadores son datos, no una nueva autoridad. Ningún texto recuperado puede cerrar un segmento o inyectar metadatos: debe escaparse canónicamente.

### Estado de larga duración

```text
objetivo raíz
  checkpoint verificado A
    checkpoint verificado B             # ruta activa enviada
      intento C fallido                  # fuera de ventana, consultable
      checkpoint verificado D (activo)
        evidencia/artifacts por referencia
```

- El log crudo de acciones, observaciones y resultados permanece append-only.
- Se crea un checkpoint al cerrar un subobjetivo, no por alcanzar un porcentaje arbitrario de ventana.
- `Maintain` se implementa como verificación determinista de evidencia/referencias antes de promover un resumen.
- `Revise` bifurca el DAG; no reescribe la historia ni borra el intento fallido.
- La ventana recibe la ruta raíz→hoja activa, las restricciones vigentes y los IDs necesarios para consultar detalle.

## Fases y commits verticales

Cada fase debe ser tocable end-to-end, tener un comando real de verificación y cerrar con un único commit lógico en español.

### Fase 0 — contrato, evidencia y baseline reproducible

- Crear `docs/implementation/branch-31-token-context-governance/{DESIGN.md,EVIDENCE.md,HANDOFF.md}`.
- Registrar para cada técnica del informe: fuente primaria, madurez, aplicabilidad, decisión y test requerido.
- Corregir explícitamente TOON: **Token-Oriented Object Notation**, no “Targeted Output Optimization Network”.
- Sembrar una suite baseline con los roles/modelos reales y los fixtures actualmente ejecutables; ampliar la misma suite a medida que cada runner pendiente se vuelva real.
- Registrar explícitamente `executed/runner_ready/catalog_total`; tras el primer slice de runners se espera 9/9/14, nunca “14 aprobados”.
- Definir la secuencia para implementar los 10 runners pendientes como slices separados. Los runners RAG/Memory/namespace son requisito previo del canario de ingesta; 14/14 es requisito previo de la autoauditoría final.
- Registrar bytes del snapshot, bytes renderizados, tokens reservados, tokens reportados, coste y resultado de calidad.

Gate: no avanzar si la baseline no es reproducible, si alguna llamada queda sin correlación `task -> invocation -> snapshot -> ledger`, o si el reporte confunde cobertura ejecutada con tamaño total del catálogo.

### Fase 1 — contabilidad real de uso y caché

Extender el dominio de uso sin romper proveedores que no reportan detalle:

```go
type Usage struct {
    FreshInputTokens      int64
    CacheReadInputTokens  int64
    CacheWriteInputTokens int64
    OutputTokens          int64
    ReasoningTokens       int64
    TotalTokens           int64
    ProviderReported      bool
}
```

- Parsear `prompt_tokens_details.cached_tokens` donde el endpoint OpenAI-compatible lo entregue.
- Parsear `prompt_cache_hit_tokens` y `prompt_cache_miss_tokens` en DeepSeek.
- Persistir el desglose y reconciliar con los tiers ya soportados por `modelpricing`.
- No inferir `CacheReadInputTokens` desde diferencias si el proveedor no lo informa.
- Exponer CLI por organización, departamento, rol, modelo, tarea y rango temporal.

Gate: conservación exacta del total reportado, terminal único del ledger, reintentos idempotentes y reserva fail-closed.

### Fase 2 — renderer dual y presupuestos por tokens

- Mantener el renderer portable actual para auditoría.
- Añadir un renderer de ejecución compacto, versionado y determinista.
- Añadir `TokenCounter` por perfil de proveedor/modelo, con fallback conservador declarado.
- Presupuestar prefijo estable, estado activo, memoria, RAG y margen de salida por separado.
- Persistir métricas por segmento antes/después de renderizar.
- Escapar contenido hostil y probar que nunca altera los delimitadores de autoridad.

Call stack objetivo:

```diff
 dispatch
   ContextProvider.GetAndRevalidate(snapshot)
-  snapshot.RenderPortableJSON()
-  estimateTokens(len(bytes)/3)
+  ExecutionViewBuilder.Build(snapshot, modelProfile, budget)
+    SelectSegments(authority, relevance, diversity, tokenBudget)
+    RenderCompact(version)
+    TokenCounter.Count(modelProfile, rendered)
+  reserve(worstCaseMeasured)
   adapter.Dispatch(executionView)
```

Gate: el snapshot canónico y su hash no cambian; la vista reconstruida conserva el mismo hash derivado; pruebas de inyección y clasificación pasan.

### Fase 3 — prefijo estable y caché medible

- Ordenar: política estática y herramientas estables primero; estado/tarea/evidencia dinámica después.
- Calcular `stable_prefix_hash` y registrar cambios que invalidan el caché.
- Mantener prompts canónicos idénticos byte a byte entre llamadas equivalentes.
- Medir hit/miss por DeepSeek antes de introducir heurísticas.
- Añadir configuración específica de OpenAI solo después de resolver D-005 y verificar el endpoint de Luna.

Gate: nunca cobrar como cache hit algo no informado; una invalidación de política cambia el prefix hash; aislamiento total entre organizaciones.

### Fase 4 — divulgación progresiva de memoria y RAG

- Propagar el score RRF real de RAG hacia `SourceRecord.Relevance`.
- Seleccionar con autoridad primero y relevancia/diversidad dentro de la misma clase; un score nunca vence una prohibición.
- Deduplicar candidatos semánticamente cercanos sin borrar sus fuentes.
- Enviar extractos compactos y IDs recuperables; añadir una lectura autorizada por ID para desplegar detalle.
- Mantener fallback léxico y trazabilidad del perfil embedding.

Gate: Recall@K/nDCG/MRR de R30.1 no pueden caer fuera del umbral fijado por la baseline; pruebas numéricas `20`/`2000`, cross-namespace y candidato rechazado siguen pasando.

### Fase 5 — checkpoints verificados para tareas largas

- Añadir checkpoints tipados sobre el `decisiongraph` existente.
- Referenciar logs/evidencia cruda por artifact ID; no copiar volcados completos al mensaje.
- Construir el contexto activo desde la ruta verificada raíz→hoja.
- Conservar y consultar ramas fallidas fuera de la vista activa.
- Implementar resume/replan desde checkpoint sin reinyectar toda la conversación.

Gate: reanudar una tarea tras compacción produce el mismo estado verificable; contradicciones bloquean promoción; ningún resumen sin evidencia se convierte en estado activo.

### Fase 6 — mensajería compacta y revisión esparsa

- Definir payloads tipados para `delegation`, `status`, `completion` y `review_request`.
- Enviar deltas y referencias a artifacts, con límites de bytes/tokens.
- Activar un segundo revisor solo por clase de riesgo, evidencia contradictoria o fallo de verificador.
- Implementar decisión de parada con límites de replan/retry ya existentes.
- No introducir broadcast ni debate all-to-all.

Gate: causalidad e idempotencia preservadas; una carga sobredimensionada falla antes de persistirse; ningún artifact cruza namespace sin autorización.

### Fase 7 — políticas por clase de tarea y autoauditoría

- Clasificador inicial determinista basado en riesgo/capability, no otro LLM.
- Mantener el routing autorizado: CEO Luna; coordinador DeepSeek v4 Pro; worker DeepSeek v4 Flash, salvo decisión posterior del owner.
- Aplicar output schema, `MaxOutputTokens`, reasoning/thinking budget y herramientas por clase de tarea.
- Ejecutar la organización sobre su propia suite y generar propuestas de mejora con evidencia.
- Requerir aprobación humana para arquitectura, migraciones de infraestructura viva, secrets, egress nuevo y acciones irreversibles.

Gate: una autoauditoría puede abrir una propuesta y adjuntar evidencia, pero no cambiar su propio gate ni autopromoverse.

### Fase 8 — canario de ingesta del corpus AI

Precondiciones:

- BGE-M3 real instalado, identidad fijada y medido en el VPS objetivo; no basta el fake de R30.
- Espacio en disco y RAM verificados de nuevo.
- Perfil `bge-m3-local-1024` sano para escritura y consulta.
- Corpus v4 validado contra hashes del handoff.

Fuente ya transferida:

```text
/opt/explorarte/artifacts/rag-ingestion/ai-corpus-v4/
  documents.jsonl
  manifest.jsonl
  summary.json
  CURATION_REPORT.md
```

Flujo:

1. Crear namespace AI candidato y registrar attestations de admisión.
2. Ingerir un canario estratificado (libros, cursos/transcripciones Whisper y material técnico) con IDs deterministas.
3. Generar embeddings BGE-M3 1024; nunca escribirlos en tablas Gemini 768.
4. Ejecutar consultas de evaluación en español, identificadores, negación y relaciones entre temas.
5. Medir recall, diversidad, latencia, RAM, tokens del contexto de respuesta y procedencia.
6. Solo si pasa, continuar en lotes reanudables; nunca promover todo el corpus por la mera existencia del archivo.

Gate: cada resultado debe citar documento/chunk; no hay cruces de namespace; rollback elimina solo la generación candidata.

## Métricas y criterios de promoción

No se usan como aceptación cifras generales de 60–95% tomadas de otros productos.

| Dimensión | Gate mínimo |
|---|---|
| Contabilidad | 100% de invocaciones correlacionadas; uso estimado y reportado nunca mezclados |
| Presupuesto | 0 llamadas al proveedor sin reserva válida |
| Calidad | sin regresión no explicada en fixtures R30.1 y canario AI |
| Seguridad | 0 lecturas cross-namespace, 0 elevaciones de autoridad, 0 promoción de contenido hostil |
| Procedencia | 100% de segmentos/resúmenes enlazados a fuente y hash |
| Contexto | reportar p50/p95 de entrada fresca, cacheada, salida y reasoning por rol/modelo |
| Caché | hit ratio y ahorro calculados desde contadores reales del proveedor |
| Larga duración | resume determinista desde checkpoint y rama fallida fuera de la ruta activa |
| Autoauditoría | 0 autopromociones o mutaciones irreversibles sin aprobación humana |

Una optimización se promociona solo si reduce coste/token fresco en su workload objetivo y conserva los gates de calidad y seguridad. Si mejora calidad a mayor coste, requiere una decisión explícita del owner, no una promoción automática.

## Exclusiones deliberadas

- **LLMLingua:** solo canario experimental posterior a la baseline; su compresión es potencialmente con pérdida.
- **TOON:** no se adopta sin benchmark con el tokenizer real; además el informe expandió mal el acrónimo.
- **MCP Tool Search:** fuera mientras la aplicación no inyecte un catálogo MCP al modelo.
- **CA-MCP/WIMSE/cross-organización:** investigación futura; hoy no existe esa topología productiva.
- **MixKV, VisPruner, VTW, SeTok:** optimizan internals de modelos autoalojados en GPU, no las APIs de generación usadas por esta organización ni BGE-M3 como embedder CPU.
- **Mem0/NeuralTrust/KanseiLink como dependencias:** se estudian patrones, no se incorporan plataformas externas por cifras comerciales.
- **Debate multiagente genérico:** prohibido por defecto; revisión selectiva y acotada solamente.
- **Compresión destructiva de logs:** los logs crudos permanecen externos y append-only.

## Verificación general por commit

```text
gofmt -l .
go vet ./...
go build ./...
go test ./...
make verify
go test -tags=integration ./... -count=1 -p 1
```

Además, cada fase debe ejecutar su canario vertical por CLI contra PostgreSQL real y guardar el reporte como artifact correlacionado. No se probarán APIs reales de proveedores sin una reserva de coste y autorización explícita; los adapters primero se verifican con servidores fake.

## Handoff requerido

El handoff final debe incluir: base exacta, commits, migraciones, esquema antes/después, mediciones baseline/candidato, contadores de caché por proveedor, fixtures pasados/fallados, hashes del corpus, lote ingerido, consumo BGE-M3 real, errores descubiertos, rollback probado y lista de afirmaciones descartadas por falta de evidencia.
