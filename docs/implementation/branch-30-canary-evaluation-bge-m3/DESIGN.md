# Rama 30 — Evaluación canaria, proyectos de prueba y transición a BGE-M3 local

## Estado y base

- Rama: commits directos sobre `main` (mismo patrón que R23-R29).
- Base: `bc6dafd` (`main` al cierre de R29, fundación de retrieval semántico — 9/9 fases, `go test -tags=integration ./...` limpio en todo el repo).
- Migraciones reservadas: a partir de `000031` (confirmar el tip real de `migrations/` antes de cada commit).
- Precondición verificada antes de iniciar: worktree limpio salvo un archivo de otro worker (`compose.integration.bugreview07.yaml`, no tocado), `HEAD` en el último commit de R29, `go build/vet/gofmt/test` limpios en todo el repo.

## Por qué esta rama existe

R29 dejó una fundación de retrieval semántico correcta pero con un solo proveedor de embeddings (Gemini, remoto, de pago) y sin ningún programa de evaluación que mida si el retrieval híbrido realmente mejora sobre el léxico puro. Esta rama: (1) convierte a Gemini en un índice de referencia congelado, de bajo costo fijo (USD 10 de por vida), en vez de dependencia productiva continua; (2) introduce BGE-M3 como motor local operativo, sostenible sin costo por token; (3) construye un programa de evaluación real (14 proyectos sintéticos, métricas, gates duros) para poder afirmar con evidencia — no por intuición — que un modo de retrieval es mejor que otro; (4) cierra D-007 (arquitectura lógica productiva) sin implementar el solver completo, que queda para una rama futura.

## Decisiones del owner que fijan el alcance (no reabrir sin nueva instrucción explícita)

- **Nunca mezclar espacios vectoriales**: Gemini (`gemini-embedding-2`, 768 dim) y BGE-M3 (`BAAI/bge-m3`, 1024 dim) viven en tablas derivadas separadas, nunca comparadas ni fusionadas entre sí en una misma consulta.
- **Gemini deja de ser un LLM generativo.** `docs/canonical/model-routing.yaml` ya no asigna ningún rol a `provider: gemini` — `research.audit`/`research.worker` pasan a `deepseek/deepseek-v4-pro` y `deepseek/deepseek-v4-flash` respectivamente, `department.leader` pasa a `deepseek/deepseek-v4-pro`. Gemini solo es alcanzable vía `internal/embeddingruntime` (ya existente desde R29), nunca vía `internal/modelruntime`. Como el dispatcher de chat resuelve el proveedor exclusivamente desde `model-routing.yaml` (invariante ya vigente: *"A role never selects its own model from request JSON"*), la ausencia de cualquier policy con `provider: gemini` hace que una solicitud generativa hacia Gemini no tenga ningún camino de dispatch que la construya — falla antes de cualquier egress por construcción, no por un guard adicional. Los precios/eventos históricos de Gemini (incluyendo `gemini-2.5-flash`/`gemini-2.5-pro`) no se borran; solo dejan de tener una ruta productiva que los use.
- **D-005 permanece abierto.** No se inventa ni se cierra la identidad real del proveedor detrás de `gpt-5.6-luna`.
- **D-007 queda resuelto** con el texto exacto del owner (ver `docs/canonical/decisions-required.yaml:resolved` y `docs/adr/ADR-0006-hybrid-logic-ir-shadow.md`): Go sigue siendo autoritativo; una IR tipada se compila hacia Prolog/Datalog aislado en shadow; ninguna divergencia cambia decisiones productivas hasta paridad+auditoría+promoción explícita. Esta rama solo fija el contrato (`internal/logicir`: esquema de IR versionado, forma de los eventos de comparación, forma de las divergencias, límites duros de tiempo/profundidad/soluciones, prohibición estructural de predicados peligrosos y de texto libre — incluida cualquier cadena de razonamiento privada del modelo). El solver real, su integración con `internal/shadowverifier`, y la persistencia de divergencias quedan para R34+.
- **Gate de hardware (autorizado, no bloqueante):** el VPS de prueba actual (2 vCPU, ~8GB RAM, ~2GB swap) es el entorno de estrés controlado para BGE-M3. *"Descargar y ejecutar el sidecar está autorizado en este VPS exclusivamente como prueba controlada de R30; el despliegue productivo será repetido en el VPS equivalente de 14 GB."* Medido al iniciar esta rama: `nproc`=2, RAM total ~7.62 GiB (~6.03 GiB disponible), swap usado ~0. Espacio en disco medido en `/`: **4.8G libres** (`df -h /` → `29G total, 24G usados, 4.8G libres, 84%`) — más bajo que el ~6.45GB que indicaba la especificación corregida del owner; se registra aquí como discrepancia real observada, a verificar de nuevo justo antes de descargar cualquier peso de BGE-M3 en la Fase 4, sin asumir que hay margen suficiente hasta confirmarlo con el tamaño real del artefacto elegido.
- Un fallo por memoria/disco en el VPS de prueba **no** se interpreta como rechazo definitivo de BGE-M3 — solo bloquea esta prueba puntual.

## Alcance de esta rama

Evaluación canaria (lexical vs. Gemini-híbrido vs. BGE-M3-híbrido) sobre 14 proyectos de prueba sintéticos versionados, adapter local BGE-M3 endurecido, tablas derivadas `vector(1024)` separadas de las de Gemini, perfil de embedding activo seleccionable con degradación auditable, harness de evidencia web efímera (sin elegir proveedor de búsqueda real todavía), cierre de gobernanza (routing, D-007/ADR-0006).

### Explícitamente fuera de alcance (candidatas a ramas futuras, no resueltas ni simuladas acá)

- El solver lógico real (Prolog/Datalog embebido o externo) y su integración productiva — solo el contrato (`internal/logicir`) queda fijado.
- Selección de proveedor real de búsqueda web — solo el harness/puerto con pruebas deterministas.
- Sparse vectors / multi-vector / ColBERT de BGE-M3 — solo el vector denso inicialmente.
- HNSW u otro índice ANN — brute-force (`ORDER BY embedding <=> $1`) hasta que un `EXPLAIN ANALYZE` con volumen real lo justifique, mismo criterio que R29.
- Consolidación automática de memoria, reranking/reformulación de RAG agéntico — igual que R29, siguen fuera.

## Orden de commits (un commit lógico por fase, mensaje en español)

1. R30: contrato, ADR, routing y cierre de D-007.
2. R30: fixtures y runner reproducible de evaluación.
3. R30: métricas, almacenamiento durable y comandos CLI.
4. R30: adapter local BGE-M3 con fake server y hardening.
5. R30: tablas derivadas BGE-M3 vector(1024).
6. R30: retrieval híbrido seleccionable por perfil.
7. R30: harness de evidencia web efímera.
8. R30: comparación canaria y reporte final.

No se mezclan arreglos incidentales fuera de estas fases; si aparece un bug no relacionado se documenta y se detiene esa línea salvo que bloquee R30 (mismo criterio que R29).

## Fase 2 — estado real de los 14 proyectos (no todos tienen runner todavía)

`internal/evaluation/fixtures` define los 14 proyectos requeridos (`CatalogR30()`), cada uno con ID+versión, objetivo, organización/roles, datos iniciales (`Scenario`), resultado esperado, invariantes duros, evidencia esperada, presupuesto máximo, reintentos/replans máximos, timeout y seed determinístico — campos obligatorios verificados por `Fixture.Validate()`.

`internal/evaluation` (ya existente, de una rama anterior) resuelve trazas ya grabadas vía `TraceSource`/`Evaluator`; no ejecuta nada por sí mismo. `fixtures.Runner` es el contrato nuevo que sí ejecuta un fixture — separado a propósito, porque cada tipo de proyecto necesita un motor distinto (grafo de decisión puro en memoria, retrieval contra Postgres real, sandbox de ejecución de código, etc.).

Honestidad de alcance — de los 14, **3 tienen un `Runner` real hoy** (`DecisionGraphRunner`, sin dependencia de Postgres, sobre el dominio puro de `internal/decisiongraph`):

- `r30-08-budget-exhaustion`
- `r30-11-dag-cycles-depth-terminal-evidence`
- `r30-12-contradictory-evidence-non-selection`

Los 11 restantes quedan con `Status: pending` y `PendingPhase` explícito (nunca `pending` sin decir qué fase lo resuelve):

- `r30-03/04/05/06/07` (identificadores, paráfrasis semántica, memoria vieja-relevante, denegación cross-namespace, candidato rechazado): fase 3, reutilizando `internal/rag.Manager`/`internal/memory.Manager` contra Postgres real.
- `r30-09` (reserva huérfana): fase 3, reutilizando `internal/costledger`.
- `r30-10` (recuperación de lease de mensajería): fase 3, reutilizando `internal/agentmessaging.Store.ClaimNext`.
- `r30-04` semántico y perfil `bge-m3-hybrid`: además depende de la fase 6 (perfil BGE-M3).
- `r30-13` (página web hostil): fase 7 (harness de evidencia web efímera, aún no existe).
- `r30-01`/`r30-02` (bug-fix de Go / migración de Postgres): requieren un sandbox real de ejecución de código, fuera del alcance de las fases 1-8 tal como están definidas; candidatas a rama futura o a la fase 8 si el tiempo lo permite.
- `r30-14` (extremo a extremo): compone todos los anteriores, solo puede ejecutarse en la fase 8.

`RunSuite` salta silenciosamente los fixtures que un `Runner` dado no soporta (`Runner.Supports`), en vez de fallar — así una corrida parcial contra el motor disponible hoy no se confunde con una corrida completa de las 14.

## Fase 3 — métricas, almacenamiento durable y CLI

`internal/evaluation/metrics` agrega las medidas de calidad de retrieval que R30 exige (Recall@K, nDCG@K, MRR, precisión de identificadores, tasa de falsos positivos numéricos) como funciones puras, listas para que los runners de retrieval de la fase 3-en-adelante las usen sobre resultados reales.

`internal/evaluation/postgres` (migración 000031, `evaluation_runs`/`evaluation_run_outcomes`) es el almacenamiento durable: cada `run` fija suite+subject(modo), y sus `outcomes` son append-only por `(run_id, fixture_id)` — nunca se sobreescribe un resultado ya grabado, así una comparación entre dos runs no puede verse alterada por un resultado que cambia por debajo. Aislamiento por organización verificado con test de integración real (una organización nunca puede leer los runs de otra).

`cmd/orgctl/evaluation.go` agrega `orgctl evaluation <seed|run|compare|report>`:
- `seed --suite r30`: valida y lista el catálogo (hoy: 14 fixtures, 3 runner-ready).
- `run --suite r30 --mode <subject>`: corre cada `Runner` disponible (`evaluationRunners()`, hoy solo `DecisionGraphRunner`) contra el catálogo, persiste cada outcome, y falla el proceso (`exitCompletionFailed`) si algún fixture ejecutado no pasó.
- `report <run-id>`: imprime un run persistido y sus outcomes.
- `compare <run-a> <run-b>`: diferencia dos runs persistidos por fixture, marcando cambios de pass/fail.

**Corrección durante esta fase**: el primer intento de esta fase reconstruyó un puente `decisiongraph`→`evaluation.TraceSource` desde cero (`TracePayload` nuevo en `internal/decisiongraph`, paquete `internal/evaluation/decisiongraphtrace`) sin revisar antes si ya existía uno — **sí existía**: `internal/decisiongraphtrace` (de una rama anterior, ya conectado en `cmd/orgctl/improvement.go`, con su propia cobertura de integración) resuelve exactamente este puente, con una decisión de diseño deliberada de no depender del hash interno no exportado de `decisiongraph.Store.TraceRef` sino generar su propio payload canónico autoverificable. La reconstrucción se revirtió por completo (`git checkout` sobre los archivos de `internal/decisiongraph` tocados, borrado del paquete duplicado) antes de commitear nada. `internal/evaluation/postgres` (almacenamiento de outcomes de fixtures) es independiente de ese puente y no se vio afectado.

Sigue pendiente para fases futuras: wirear un `Runner` de retrieval real (contra `internal/rag.Manager`/`internal/memory.Manager`) para los 5 fixtures de retrieval, y usar `internal/decisiongraphtrace` + `internal/evaluation.Service` (comparación baseline/candidato ya existente) cuando haya runs reales que comparar por trace en vez de por outcome crudo.

**Corrección importante descubierta al correr `make verify` por primera vez en esta rama** (las fases 1-3 se habían verificado solo con `go build/vet/test`, nunca con `make verify` completo): el linaje de `internal/evaluation/fixtures` violaba un límite arquitectónico real y deliberado — `scripts/check-improvement-fitness.sh` prohíbe que `internal/evaluation` (y `internal/improvement`) importen `internal/decisiongraph` directamente, precisamente para no depender de su formato de hash interno no exportado (la misma razón por la que `internal/decisiongraphtrace` ya existía como puente dedicado, sin importar el paquete `decisiongraph`). `DecisionGraphRunner`/`DecisionGraphScenario` se movieron a un paquete nuevo, `internal/decisiongraphfixtures`, que es el único que importa ambos lados; `internal/evaluation/fixtures.CatalogR30` ahora define los 3 fixtures de DAG/presupuesto como `pending` en la base, y `decisiongraphfixtures.Activate(catalog)` es quien les da su escenario real y los marca `runner_ready` — sin que el paquete base sepa nada de `decisiongraph`. `cmd/orgctl/evaluation.go` llama a `Activate` antes de listar/correr.

También se encontraron y corrigieron, con `make verify`, dos regresiones reales de gates de gobernanza ya existentes por los cambios de la Fase 1 (no relacionadas con BGE-M3, pero descubiertas en esta fase por ser la primera corrida completa de `make verify`):
- `scripts/check-task-fitness.sh` y otros 8 scripts de fitness (staging, authorization, model-runtime, model-egress, model-dispatch, model-identity, context, model-provider) tenían una lista fija de archivos canónicos autorizados a cambiar que no incluía `decisions-required.yaml` — se amplió esa lista en los 9, documentando por qué (cierre de D-007).
- `scripts/check-model-egress-fitness.sh` y `scripts/check-model-provider-fitness.sh` exigían que la tabla de allows productivos incluyera exactamente las filas de `gemini` que la Fase 1 retiró — se actualizaron para reflejar la tabla real (`deepseek`+`openai_compatible` solamente); `policy_version` se revirtió a 4 (otro gate lo fijaba en ese valor) ya que los reason codes sobrevivientes siguen siendo `_v4`, sin necesidad de un bump real de versión.

Lección para las fases restantes: correr `make verify` completo (no solo `go build/vet/test`) antes de cada commit, no solo al final de la fase.

## Fase 4 — adapter local BGE-M3 (Go real; sidecar real diferido)

`internal/embeddingruntime/adapter/bgem3` implementa `embeddingruntime.OnlineAdapter` contra un sidecar local BGE-M3, con el endurecimiento que pide R30: `Config.Validate` rechaza cualquier `BASE_URL` que no sea loopback (`127.0.0.1`/`localhost`) o `unix://`; identidad de modelo fijada (`ModelRevision`+`ArtifactSHA256` de 64 hex, nunca auto-resueltos) verificada en cada `Embed` y en `Healthy` (endpoint de readiness separado de `/v1/embed`); cola acotada (`MaxConcurrency`+`MaxQueueDepth`, falla rápido con `ErrQueueFull` en vez de bloquear sin límite); límites de bytes/ítems por request; validación de vector (dimensión exacta, sin NaN/Inf, sin vacíos) independiente de lo que el sidecar afirme; `input_hash`+`idempotency_key` en el protocolo, nunca el texto crudo, en cada request; ningún log incluye texto de entrada. No hay `BatchAdapter`: batch es un concepto de API remota facturada (Gemini) sin sentido para un proceso local no facturado.

`scripts/check-embeddingruntime-fitness.sh` (nuevo, `make test-embeddingruntime-fitness`, agregado a `verify`) fija estos invariantes más la prohibición de subproceso/exec en todo `internal/embeddingruntime` (el sidecar es un proceso Python separado, nunca embebido en `orgd`) y que ningún adapter referencie el `ProviderID` del otro.

Probado con un servidor fake (`httptest`) cubriendo: camino feliz, dimensión incorrecta, NaN/Inf, conteo de resultados distinto, clave faltante, deriva de identidad de modelo, entrada sobredimensionada, cola acotada agotada, cancelación por contexto, healthcheck con deriva de identidad — sin `-race` sin fallos.

`deployments/bgem3/RUNBOOK-deploy-sidecar.md` documenta el proceso operativo real (aprovisionamiento sin red posterior, verificación de espacio en disco, variables de entorno, smoke test, medición, criterios de parada, promoción al VPS de 14GB) citando el gate de hardware exacto del owner. **Descargar y correr el sidecar real con pesos reales queda explícitamente diferido** — esta fase entrega el adapter Go y el servidor fake, no la instalación real; el runbook es la guía para cuando se ejecute esa acción por separado, con el espacio en disco re-verificado en ese momento (no asumir que los 4.8G medidos al inicio de la rama siguen vigentes).

## Fase 5 — tablas derivadas BGE-M3 vector(1024)

Migración 000032 crea `rag_chunk_embeddings_bge_m3` y `organizational_memory_embeddings_bge_m3`, separadas de las tablas de R29 (`rag_chunk_embeddings`/`organizational_memory_embeddings`, vector(768)) en todo: dimensión fijada en 1024 vía el tipo de columna, `embedding_model_id` fijado a `'bge-m3-local'`, y metadata más rica que R29 porque un modelo autoalojado no tiene un string de versión asignado por un proveedor — la fila se identifica por `(organization_id, chunk_id/entry_key, model_revision, artifact_sha256)`, con `tokenizer_revision`, `normalization`, `pooling` y `prompt_template_version` también obligatorios. Mismo trigger de solo-INSERT (UPDATE rechazado, DELETE permitido como rollback) que R29, con un mensaje de error propio (`TG_TABLE_NAME`) en vez de reusar la función de la migración 000028. Sin tablas de batch job: BGE-M3 no tiene Batch API, cada llamada es síncrona.

`internal/rag.BGEM3ChunkEmbedding`/`BGEM3EmbeddingRepository` y `internal/memory.BGEM3EntryEmbedding`/`BGEM3EmbeddingRepository` son interfaces nuevas, aditivas — nunca extienden las de R29, nunca comparten método ni tabla. `internal/rag/postgres/embeddings_bge_m3.go` e `internal/memory/postgres/embeddings_bge_m3.go` implementan Insert/NearestNeighbor exactos, sin índice ANN (mismo criterio que R29: sin volumen real que lo justifique). `internal/memory` sigue sin poder importar `internal/rag` (gate de `check-memory-fitness.sh`), así que `BGEM3EntryEmbedding` duplica `BGEM3ChunkEmbedding` deliberadamente, igual que ya hacía `EntryEmbedding` con `ChunkEmbedding`.

Probado contra Postgres real: insert + nearest-neighbor exacto en ambas tablas, aislamiento — un chunk/entrada con embedding solo en la tabla BGE-M3 nunca aparece al consultar la tabla de Gemini (768) y viceversa —, y rechazo de UPDATE in-place. `internal/platform/postgres/integration_test.go`'s test de down/reapply se extendió (000032 agregado antes de 000017, mismo patrón que 000028/000029) porque `rag_chunk_embeddings_bge_m3` también depende de `rag_knowledge_chunks`.

## Fase 6 — retrieval híbrido seleccionable por perfil

La selección de perfil vive en el mismo lugar donde ya vivía la elección de proveedor: `internal/rag/bootstrap` e `internal/memory/bootstrap` (cada uno con su propia copia — ninguno de los dos puede importar al otro). `ORG_EMBEDDING_ACTIVE_PROFILE` (`gemini-768` por defecto, preservando el comportamiento de R29 sin cambios; o `bge-m3-local-1024`) decide, en el arranque, qué adapter (`gemini.New`/`bgem3.New`) y qué `ProviderID`/`ProviderModelID`/`OutputDimensionality` entran en `SemanticSearchDeps` — un valor desconocido falla el arranque (`unknown ORG_EMBEDDING_ACTIVE_PROFILE`), nunca cae en un default silencioso.

El "nunca mezclado" se hizo estructural, no solo de configuración: `internal/rag/postgres/hybrid_query.go` e `internal/memory/postgres/search.go` ganaron `vectorChannelTable(queryVector)`, que elige la tabla de embeddings (y el encoder con su chequeo de dimensión) **a partir de la longitud del vector de consulta** — 768 va a `rag_chunk_embeddings`/`organizational_memory_embeddings` (Gemini), 1024 va a `rag_chunk_embeddings_bge_m3`/`organizational_memory_embeddings_bge_m3` (BGE-M3), y cualquier otra longitud es un error duro. No existe ningún camino de código que pueda construir una consulta que una ambas tablas. Probado contra Postgres real: `store.Query()`/`store.Search()` (el camino RRF completo, no solo los helpers `NearestBGEM3*` de la Fase 5) encuentran un chunk/entrada vía su embedding BGE-M3, y un vector de dimensión inesperada se rechaza.

La degradación auditable ya existía en R29 (`embedQuery`/`embed` degradan a "sin canal vectorial" ante cualquier falla, sin romper la consulta) — R30 le agrega la señal explícita: un `slog.Default().Info` de salida única por llamada (`"rag query embedding channel status"` / `"memory embedding channel status"`) con `provider_id`, `provider_model_id` y `degraded` (booleano derivado de si el vector resultante es `nil`), independientemente de cuál de las muchas rutas de fallo internas se haya tomado.

**Nota operativa real, no resuelta automáticamente por código**: `internal/rag.SemanticSearchDeps`/`internal/memory.SemanticSearchDeps` exigen `Pricing`+`Wallet` junto con el adapter (mismo requisito para cualquier proveedor, ya vigente en R29). Para que el perfil `bge-m3-local-1024` resuelva precio con éxito la primera vez, el operador debe sembrar un tier (típicamente $0, ya que es un proceso local no facturado) y un saldo de wallet para `provider_id=bge-m3-local`/el `model_revision` configurado, vía los comandos ya existentes `orgctl budget set-price`/`set-balance` (documentado en `deployments/bgem3/RUNBOOK-deploy-sidecar.md`) — deliberadamente **no** se sembra nada automáticamente desde el bootstrap: son comandos genéricos que ya sirven a cualquier proveedor, no maquinaria nueva y específica de BGE-M3 en la que confiar sin haberla visto ejercitada.

`internal/memory.SemanticSearchDeps.Embeddings` (una interfaz `EmbeddingRepository` fija) se reemplazó por `InsertVector` (un closure) — memoria embebe de forma síncrona al aprobar (a diferencia de RAG, cuyo camino de inserción sigue siendo el batch job asíncrono de R29, no conectado a `Manager`), así que necesitaba poder escribir en la tabla correcta según el perfil activo sin que `Manager`/`semantic.go` supieran nada de cuál tabla es esa — el closure lo captura en el bootstrap, una vez, por perfil.
