# R30 — Handoff obligatorio

Este documento es el cierre formal de la rama, per la sección "HANDOFF OBLIGATORIO" de la especificación del owner. Se escribe con la misma disciplina que el resto de la rama: números reales, nada promediado para ocultar un fallo, y una declaración explícita de qué NO está terminado.

**R30 no se declara "completa" en el sentido pleno de la especificación original.** Es una base sustancial y verificada (8 fases, 7 commits, ~6200 líneas), pero dos de las condiciones descalificantes explícitas de la propia especificación aplican hoy: BGE-M3 real nunca se instaló ni se midió en hardware (el gate de hardware lo autorizaba pero esta sesión no llegó a ejecutar esa acción separada), y solo 4 de los 14 proyectos sintéticos tienen runner real (los otros 10 quedan definidos con su fase/dependencia pendiente documentada, nunca en silencio). Ver "Qué NO está resuelto" abajo para el detalle completo.

## Commits exactos

Rama: commits directos sobre `main`, mismo patrón que R23-R29.

| Commit | Fase | Mensaje |
|---|---|---|
| `068d457` | 1 | R30: contrato, ADR, routing y cierre de D-007 |
| `8908ece` | 2 | R30: fixtures y runner reproducible de evaluación |
| `188f490` | 3 | R30: métricas, almacenamiento durable y comandos CLI |
| `992a04c` | 4 | R30: adapter local BGE-M3 con fake server y hardening |
| `0c76a83` | 5 | R30: tablas derivadas BGE-M3 vector(1024) |
| `17148cb` | 6 | R30: retrieval híbrido seleccionable por perfil |
| `9bf9afc` | 7 | R30: harness de evidencia web efímera |
| (este) | 8 | R30: comparación canaria y reporte final |

82 archivos tocados, +6203/-108 líneas (sin contar la Fase 8). Nada se pusheó — todo queda local en `main`, igual que R23-R29.

## Migraciones agregadas

- `000031_create_evaluation_runs` — `evaluation_runs`/`evaluation_run_outcomes` (almacenamiento durable de corridas de evaluación).
- `000032_create_bge_m3_embedding_tables` — `rag_chunk_embeddings_bge_m3`/`organizational_memory_embeddings_bge_m3` (vector(1024), separadas de las de Gemini).
- `000033_create_web_evidence` — `web_evidence` (efímera, TTL obligatorio, sin FK a RAG/Memory).

Tip de migraciones al cierre: `000033`. Todas probadas up/down/reapply contra Postgres 17 real con pgvector (incluida la extensión del test de ciclo down/reapply de `internal/platform/postgres` para 32, mismo patrón que 28/29 en R29).

## Archivos modificados/creados

Ver `git diff --stat 068d457^..HEAD` para el detalle completo (82 archivos). Paquetes nuevos: `internal/logicir`, `internal/evaluation/fixtures`, `internal/evaluation/metrics`, `internal/evaluation/postgres`, `internal/decisiongraphfixtures`, `internal/embeddingruntime/adapter/bgem3`, `internal/webevidence`, `internal/webevidence/postgres`, `internal/webevidencefixtures`. Paquetes modificados: `internal/organization/registry` (esquema de `decisions-required.yaml`), `internal/rag`/`internal/rag/postgres`/`internal/rag/bootstrap`, `internal/memory`/`internal/memory/postgres`/`internal/memory/bootstrap`, `internal/decisiongraph/postgres` (agregado y revertido — ver "Errores encontrados y corregidos"), `cmd/orgctl` (`evaluation.go` nuevo). 9 scripts de fitness amendados (gates de canónicos + tabla de egress productivo). Governance: `docs/canonical/model-routing.yaml`, `docs/canonical/model-egress-policy.yaml`, `docs/canonical/decisions-required.yaml`, `docs/adr/ADR-0006-hybrid-logic-ir-shadow.md`.

## Comandos ejecutados y resultado

Antes de cada commit (8 veces): `gofmt -l .`, `go build ./...`, `go vet ./...`, `go test ./... -count=1`, `go test -tags=integration ./... -count=1 -p 1` contra Postgres 17 real con pgvector (contenedor `pgvector/pgvector` pinneado por digest), y — desde la Fase 4, tras el hallazgo de la corrección de arquitectura — `make verify` completo (todos los fitness gates + `go build` final de `orgd`/`orgctl`). Las Fases 1-3 solo se habían verificado con build/vet/test antes de que `make verify` se corriera por primera vez en la Fase 4; ese primer `make verify` completo encontró y corrigió 3 problemas reales de las Fases 1 y 3 (ver abajo) — desde entonces, `make verify` corre antes de cada commit, no solo al final.

Resultado final, ahora mismo: `make verify` limpio (fmt, vet, todos los fitness gates, build de `orgd`+`orgctl`); `go test ./... -count=1` limpio; `go test -tags=integration ./... -count=1 -p 1` limpio contra Postgres real.

## Hardware medido

VPS de prueba actual, medido en esta sesión:

- `nproc`: 2 vCPU.
- RAM total: ~7.6 GiB (`free -h`), ~1.7 GiB usados, ~6.0 GiB disponibles (incluyendo cache).
- Swap: 2.0 GiB total, ~1.9 MiB usados (negligible).
- Disco (`df -h /`): al iniciar la rama, 4.8G libres (discrepancia ya señalada contra los ~6.45G de la especificación original); al cerrar la Fase 8, **4.1G libres** — bajó ~700MB durante la sesión, atribuible a artefactos de build/paquetes de Go descargados, no a BGE-M3 (nunca se descargaron pesos reales).

**No se instaló ni se corrió el sidecar BGE-M3 real en esta sesión.** El gate de hardware del owner lo autoriza explícitamente como prueba controlada, pero requiere verificar espacio en disco para pesos+runtime+capas *antes* de descargar nada (`deployments/bgem3/RUNBOOK-deploy-sidecar.md`, Paso 0) — con 4.1G libres y un estimado de ~2.2GB (fp32) a ~3-4GB (con runtime Python+torch CPU) para el artefacto completo, el margen es ajustado, no claramente suficiente. Instalar y medir el sidecar real queda como una acción separada, explícita, a ejecutar cuando el operador decida — no se hizo en esta sesión por prudencia frente al margen de disco real, no porque el gate lo bloqueara.

## BGE-M3: versión de artefacto y hash

**No aplica — nunca se descargó un artefacto real.** El adapter Go (`internal/embeddingruntime/adapter/bgem3`) y su hardening completo están construidos y probados contra un servidor fake (`httptest`), no contra pesos reales. `Config.ModelRevision`/`ArtifactSHA256` son campos pinneados que cualquier despliegue real debe fijar; en esta sesión no hay un valor real que reportar.

## Costo acumulado de Gemini y saldo restante

**USD 0.00 gastados, saldo de Gemini intacto en lo que sea que estuviera configurado antes de esta rama.** Ningún test de esta rama llamó a la API real de Gemini — todos los tests de embeddings (RAG, memoria, adapter BGE-M3, comparación canaria) usan adapters/vectores fake o sintéticos, deterministas, sin red. El tope de USD 10 de por vida para Gemini (embeddings únicamente) sigue sin tocarse por esta rama.

## Comparación lexical vs. Gemini-híbrido vs. BGE-M3-híbrido — números reales

`internal/rag/postgres/canary_test.go` (`TestR30CanaryComparisonLexicalVsGeminiVsBGEM3`), corrido contra Postgres 17 real, mismo corpus (7 documentos: 1 exacto "error-20", 1 negativo "error 2000", 1 semántico "fallo número veinte", 4 distractores sin relación léxica ni numérica) y misma query ("error 20") en los tres modos — nunca corpus/queries distintos entre modos, tal como exige la especificación.

**Aviso explícito, no oculto**: los vectores usados en los modos "gemini-hybrid" y "bge-m3-hybrid" son vectores sintéticos deterministas construidos a mano (similitud coseno 1.0 para los documentos relevantes, -1.0 para el negativo, 0.0 para los distractores) — **no son embeddings reales de Gemini ni de BGE-M3**, porque no hay clave real de Gemini ni sidecar BGE-M3 corriendo en este entorno. Esta comparación prueba que la fusión RRF y el ruteo de tabla por dimensión (768 vs. 1024) funcionan correctamente de punta a punta contra Postgres real — no es una afirmación sobre la calidad real de ninguno de los dos proveedores. Nunca se afirma "estado del arte".

| Modo | Recall@3 | nDCG@3 | Tasa de falso positivo numérico@3 |
|---|---|---|---|
| lexical (exacto+FTS, sin vector) | 0.5000 | 1.0000 | 0.0000 |
| gemini-hybrid (vector(768), sintético) | 1.0000 | 1.0000 | 0.0000 |
| bge-m3-hybrid (vector(1024), sintético) | 1.0000 | 1.0000 | 0.0000 |

Lectura honesta: el modo lexical encuentra el documento con coincidencia léxica/numérica exacta pero **no** el documento semántico sin overlap de palabras (recall 0.5, tal como se espera — es la limitación real que motivó R29). Ambos modos híbridos encuentran los dos documentos relevantes (recall 1.0) sin nunca traer el documento numéricamente parecido pero incorrecto ("error 2000"), que en ambos modos híbridos queda rankeado último (posición 7 de 7) por construcción del vector sintético opuesto — el gate duro "positive 20-vs-2000 confusion" se verificó explícitamente en el propio test y pasó en los tres modos.

## Resultados de los 14 proyectos

De los 14 fixtures definidos en `internal/evaluation/fixtures.CatalogR30`, **4 tienen runner real y pasan hoy contra código real** (no contra mocks de la propia evaluación):

| Fixture | Runner | Resultado |
|---|---|---|
| `r30-08-budget-exhaustion` | `internal/decisiongraphfixtures` (dominio puro `decisiongraph`) | PASS |
| `r30-11-dag-cycles-depth-terminal-evidence` | `internal/decisiongraphfixtures` | PASS |
| `r30-12-contradictory-evidence-non-selection` | `internal/decisiongraphfixtures` | PASS |
| `r30-13-hostile-web-page` | `internal/webevidencefixtures` (`internal/webevidence` real) | PASS |

Los 10 restantes quedan `Status: pending` con `PendingPhase` explícito (nunca "pendiente" sin decir qué lo resuelve):

- `r30-03/04/05/06/07` (identificadores, paráfrasis semántica, memoria vieja-relevante, namespace cruzado, candidato rechazado): necesitan un runner de retrieval contra `internal/rag.Manager`/`internal/memory.Manager` con Postgres real — no se construyó en esta rama; la Fase 8 sí demostró la mecánica subyacente (ver la comparación canaria arriba), pero a nivel de `Store`, no a través de un `fixtures.Runner` dedicado.
- `r30-09-orphaned-ambiguous-reservation`: necesita un runner contra `internal/costledger` real.
- `r30-10-messaging-lease-recovery`: necesita un runner contra `internal/agentmessaging` real.
- `r30-01-go-bug-fix`/`r30-02-postgres-migration`: necesitan un sandbox real de ejecución de código — fuera del alcance de las 8 fases tal como se definieron; candidatas a rama futura.
- `r30-14-end-to-end`: compone todos los anteriores, solo puede ejecutarse cuando el resto exista.

`orgctl evaluation seed --suite r30` reporta hoy "14 fixtures, 4 runner-ready" — verificable en cualquier momento, no una cifra fija en este documento.

## Gates duros: pasados vs. fallados

Verificados explícitamente y en verde en esta sesión:
- **positive 20-vs-2000 confusion**: verificado en `canary_test.go`, pasa en los tres modos.
- **namespace leakage**: verificado en la Fase 5 (aislamiento de tablas bge-m3 vs. gemini) y ya cubierto por la suite de R29 (cross-namespace/cross-role denial), que sigue en verde.
- **mixing Gemini/BGE-M3 vectors**: estructuralmente imposible por construcción (`vectorChannelTable` en `hybrid_query.go`/`search.go` — dimensión de vector decide la tabla, cualquier otra dimensión es error duro) y probado con Postgres real en las Fases 5 y 6.
- **web evidence used as instruction**: verificado estructuralmente (`Evidence` no tiene campo de `InstructionClass`) y con 5 muestras hostiles reales en la Fase 7.
- **automatic candidate activation / promoción automática a RAG/Memory**: verificado estructuralmente — `internal/webevidence`/`internal/webevidencefixtures` no importan `internal/rag` ni `internal/memory`.
- **DAG terminal sin evidencia real**: verificado en la Fase 2 (`r30-11`), incluyendo un test que prueba que el runner detecta una regresión deliberada del invariante (no es una prueba vacía).
- **Gemini wallet used for generation**: estructuralmente imposible desde la Fase 1 — ningún rol en `model-routing.yaml` enruta a `gemini` para generación; el dispatcher solo resuelve desde ese archivo.

**No verificados en esta sesión** (no por fallo, sino porque el subsistema que exigirían no está construido): external call without prior reservation (requiere el runner de costledger, `r30-09`), recovered messages/leases (requiere el runner de `r30-10`), action without capability / secret o clinical egress (ya cubiertos por la suite existente de R23-R29, no re-verificados específicamente en R30 más allá de lo que la suite completa ya corre en verde).

## Degradaciones observadas

Ninguna degradación inesperada. La señal `degraded` (nuevo log estructurado de la Fase 6, `"rag query embedding channel status"`/`"memory embedding channel status"`) no se ejercitó contra tráfico real en esta sesión — solo se probó en unitarios/integración con adapters fake, donde se comporta como se espera (degrada a `nil` en cada fallo simulado, nunca rompe la operación que lo llama).

## Bloqueadores reales

1. **Sin clave real de Gemini ni sidecar BGE-M3 corriendo** en este entorno — impide medir calidad real de embeddings, más allá de la mecánica de fusión/ruteo ya probada con vectores sintéticos.
2. **Margen de disco ajustado** (4.1G libres) para descargar un artefacto BGE-M3 real sin verificación adicional en el momento de hacerlo.
3. **10 de 14 fixtures sin runner** — la mayoría requiere trabajo real adicional (retrieval, costledger, agentmessaging) que no entraba en las 8 fases tal como se definieron, o directamente un sandbox de ejecución de código que no existe en este repo.

## Riesgos pendientes

- El perfil BGE-M3 (`ORG_EMBEDDING_ACTIVE_PROFILE=bge-m3-local-1024`) no tiene tier de precio/saldo de wallet sembrado — activar ese perfil por primera vez requiere `orgctl budget set-price`/`set-balance`, documentado pero no automatizado ni probado end-to-end contra un sidecar real todavía.
- El reaper de `web_evidence` (`Store.Reap`) existe y está probado, pero no hay ningún comando `orgctl`/cron que lo invoque periódicamente — hoy depende de que algo lo llame explícitamente.
- El comparador de retrieval (canary) vive como un test de integración (`canary_test.go`), no como un `fixtures.Runner` conectado a `orgctl evaluation run` — repetirlo requiere `go test`, no el CLI.

## Errores encontrados y corregidos durante la rama (transparencia total)

1. **Fase 3**: primer intento reconstruyó desde cero un puente `decisiongraph`→`evaluation.TraceSource` sin revisar si ya existía uno (`internal/decisiongraphtrace`, de una rama anterior, ya conectado y con su propia cobertura). Revertido por completo antes de commitear.
2. **Fase 4** (primer `make verify` completo de la rama): `internal/evaluation/fixtures` importaba `internal/decisiongraph` directamente, violando un límite arquitectónico ya existente (`scripts/check-improvement-fitness.sh`). Corregido moviendo el código dependiente a `internal/decisiongraphfixtures`. También se encontraron y corrigieron 2 regresiones de gates de gobernanza causadas por la Fase 1 (lista de canónicos autorizados desactualizada en 9 scripts; tabla de egress productivo esperando filas de `gemini` ya retiradas).
3. **Fase 5**: `internal/evaluation/postgres.Store.Save` fallaba con findings `nil` (marshalea a JSON `null`, la columna exige `array`) — corregido con un default a slice vacío antes de marshalear.
4. **Fase 8**: el primer diseño del test canario usaba un corpus de solo 3 documentos, haciendo que "top-3" incluyera trivialmente todo (incluido el documento negativo) — corregido ampliando el corpus con distractores y usando un vector "opuesto" (similitud -1.0) para el documento negativo, garantizando que rankee último de forma determinista, no por suerte de desempate.

## Confirmación: producción nunca se tocó

Todo el trabajo de esta rama corrió contra el contenedor de test local (`r23-integration-pg`, pineado por digest, puerto `127.0.0.1:35432`) y contra `go test` en memoria. Ningún comando de esta sesión apuntó a una base de datos ni a un host de producción. No se instaló el sidecar BGE-M3 en ningún entorno (ni de prueba ni productivo) — solo se construyó y probó su adapter Go contra un servidor fake. Nada se pusheó a ningún remoto.
