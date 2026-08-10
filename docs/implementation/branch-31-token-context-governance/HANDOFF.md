# R31 — Handoff inicial

## Estado

- Rama: `branch-31-token-context-governance`.
- Base: `1e2b3966bbcba99724c31f3f95887858be721089` (`docs: documentar el cierre correctivo R30.1 en HANDOFF.md`).
- Worktree: `/opt/explorarte/worktrees/branch-31-token-context-governance`.
- `main` no fue cambiado ni cambiado de rama.
- Los archivos ajenos `compose.integration.bugreview07.yaml` y `organization canonical` no existen en este worktree y no fueron tocados.

## Qué contiene el primer slice

- `DESIGN.md`: contrato de arquitectura Go para medición de tokens/caché, renderer dual, divulgación progresiva, checkpoints, mensajería esparsa, autoauditoría y canario de ingesta.
- `EVIDENCE.md`: contraste de los dos informes 2026 contra fuentes primarias y contra el código real; distingue especificaciones, papers experimentales y evidencia comercial.
- Este handoff: estado reproducible, artifacts y orden de ejecución.

No contiene cambios de Go, SQL, configuración viva, routing, secretos ni adapters.

## Estado real heredado de R30.1

- Los seis hallazgos correctivos quedaron cerrados y documentados.
- Cada commit de R30.1 pasó `gofmt`, `go vet`, `go build`, unit tests, integración completa contra PostgreSQL real y `make verify`.
- BGE-M3 real sigue sin instalarse ni medirse; el adapter fue verificado con servidor fake.
- El catálogo contiene 14 fixtures. El primer slice ejecutable de R31 elevó la cobertura de 4 a 9 runners reales.
- Un segundo tramo de esta misma rama (ver "Cierre de cobertura: 14/14" abajo) agregó los cinco runners restantes (`costledgerfixtures`, `agentmessagingfixtures`, `codeexecutionfixtures` ×2, `endtoendfixtures`). El catálogo queda **14/14 `runner_ready`**, sin ningún fixture en `pending`.

R31 no declara la plataforma "terminada" por esto: 14/14 fixtures con runner real significa que cada fixture tiene un mecanismo real que lo ejecuta contra código productivo — no que BGE-M3 real esté instalado, ni que el corpus AI esté ingerido, ni que la autoauditoría esté habilitada. Esas tres cosas siguen sin resolverse, ver "Bloqueadores y orden siguiente".

## Slice ejecutable: retrieval 03–07

- Se añadió `internal/retrievalfixtures`, conectado a los managers productivos de RAG y Memory y a sus repositorios PostgreSQL reales.
- Los fixtures cubren identificadores `20`/`2000`, paráfrasis semántica, memoria vieja relevante, denegación cross-namespace/cross-role y candidatos rechazados no recuperables.
- Los vectores son sintéticos y deterministas. Prueban el cableado exacto/léxico/RRF/pgvector y las invariantes de autorización; no prueban que el sidecar BGE-M3 real esté instalado o sano.
- `internal/evaluationdb.RequireDisposable` rechaza cualquier base cuyo nombre no contenga `test`, `fixture` o `integration`, usando `current_database()` del servidor. Los fixtures nunca pueden mutar el Postgres vivo por una variable de entorno engañosa.
- Se verificó replay sobre el mismo Postgres: el orden de documentos y los instantes de admisión son deterministas incluso cuando un candidato ya estaba aprobado.

Canarios observados contra PostgreSQL 17 real con pgvector:

| Perfil | Ejecutados | Aprobados | Resultado relevante |
|---|---:|---:|---|
| `gemini-hybrid` de referencia, vectores sintéticos | 9/9 | 9 | PASS |
| `bge-m3-hybrid`, vectores sintéticos | 9/9 | 9 | PASS |
| `lexical` | 9/9 | 8 | falla únicamente memoria vieja relevante frente a reciente irrelevante, la brecha esperada |

Quedan pendientes los runners 01, 02, 09, 10 y 14 — cerrados en el tramo siguiente.

## Cierre de cobertura: 14/14

Cuatro paquetes nuevos, cada uno wireado a `cmd/orgctl/evaluation.go` (`evaluationRunners`/`fixturesForSuite`) con el mismo patrón de `Activate`/`Runner`/`evaluationdb.RequireDisposable` que `retrievalfixtures`:

- **`internal/costledgerfixtures`** (r30-09): reserva-commit, reserva-release, reintento idempotente sin duplicar el monto reservado, reconciliación contra una reserva inexistente que falla explícitamente, y visibilidad de una reserva huérfana vía `ListOrphanedReservations` sin confundirse con eventos terminales ni con reservas recientes — todo contra `internal/costledger` real vía Postgres.
- **`internal/agentmessagingfixtures`** (r30-10): `Send` idempotente, `ClaimNext`/`Ack` con expiración de lease real, recuperación por un segundo consumidor, rechazo de un `claim_token` viejo sobre un mensaje ya recuperado, y un mensaje que agota `max_attempts` terminando en dead-letter — contra `internal/agentmessaging` real vía Postgres.
- **`internal/codeexecutionfixtures`** (r30-01/02): r30-01 corre en un directorio temporal descartable con su propio `go.mod`, aislado del módulo del repo — siembra un bug real de off-by-one, corre `go test` real, confirma rojo antes del fix y verde después, y verifica que el diff tocó únicamente el archivo del paquete sintético. r30-02 corre dos ciclos `up`/`down` reales sobre un esquema PostgreSQL sintético y desechable, confirmando que `down.sql` nunca borra las filas preexistentes y que el esquema resultante es idéntico tras cada ciclo. Ninguno de los dos simula un agente autónomo de codificación — el runner prueba el mecanismo de sandbox, no una corrección generada por IA.
- **`internal/endtoendfixtures`** (r30-14): compone, contra los mismos componentes reales que usa el propio test de integración de `internal/executive` (registry, tasks, authorization, completion, decisiongraph, agentmessaging, agentbudget), un caso real de punta a punta: `investigacion` (research.worker propone, research.audit aprueba un documento RAG y una entrada de memoria reales vía `rag.Manager`/`memory.Manager`) -> `ingenieria_ia` (líder/worker, vía `internal/executive.Orchestrator` real) -> cierre del CEO. Solo se simula la llamada al modelo/LLM (`executive.ModelCoordinator`) — el resto del stack es productivo. Verifica juntos: el task raíz cierra `tasks.StatusCompleted` con respuesta real; el `decision_record` del task raíz queda `verification_label='verified'` (no `'inferred'`); la mensajería delega en cada salto; el presupuesto de agente queda registrado; y el resultado del worker cita, en los bytes de invocación realmente persistidos, la evidencia de investigación real — no solo que ambas mitades existan por separado.

`investigacion` es `leaderless` (`organization.yaml`) y el despacho de departamentos del orquestador exige una unidad liderada — por eso la fase de investigación de r30-14 corre antes de `Submit`, directamente contra `rag.Manager`/`memory.Manager`, no como un department task orquestado.

Dos errores reales encontrados y corregidos durante este tramo (transparencia total, mismo patrón que R30/R30.1):

1. **Reloj acumulativo rompía el replay.** `seedResearchEvidence` avanzaba el reloj con `clock.now.Add(time.Second)` de forma secuencial; en un replay donde el documento/entrada ya estaba aprobado, se saltaba `Review` y por lo tanto consumía menos avances de reloj que la primera corrida — cambiando el timestamp de `AttestedAt` del siguiente paso y convirtiendo un replay idéntico en un conflicto de hash canónico contra la misma `idempotency_key`. `internal/retrievalfixtures` ya documentaba exactamente esta clase de bug; se aplicó la misma corrección: cada paso usa un offset fijo desde un `base` común, nunca un acumulado.
2. **Reutilización de idempotencia contra una fake sin memoria.** El `IdempotencyKey` de `orchestrator.Submit` se derivaba solo del `subjectID`, igual que en `costledgerfixtures`/`agentmessagingfixtures`/`retrievalfixtures` — pero a diferencia de esos paquetes, `fakeModelRuntime` guarda su historial de invocaciones solo en memoria de Go, nunca persiste. Si `Submit` reusaba un root task de una corrida anterior (mismo `subjectID`), `Resume` conducía ese árbol de tareas existente contra una fake fresca sin memoria de lo que ya había devuelto para él — el run quedaba en un estado incoherente que ninguna de las dos corridas podía explicar sola. Corregido incorporando un componente por-invocación (`time.Now().UnixNano()`) a la clave — la fase de investigación (RAG/Memory, sistemas productivos y durables de verdad) conserva su propia clave estable, solo la clave de `Submit` cambió.

Verificado con `orgctl evaluation run --suite r30 --mode bge-m3-hybrid` (y repetido dos veces más para confirmar que el fix de idempotencia no deja el sistema en un estado inconsistente entre corridas) contra PostgreSQL 17 real:

| Fixture | Runner | Resultado |
|---|---|---|
| r30-01-go-bug-fix | `codeexecutionfixtures` | PASS |
| r30-02-postgres-migration | `codeexecutionfixtures` | PASS |
| r30-03..07 (retrieval) | `retrievalfixtures` | PASS |
| r30-08/11/12 (decisiongraph) | `decisiongraphfixtures` | PASS |
| r30-09-orphaned-ambiguous-reservation | `costledgerfixtures` | PASS |
| r30-10-messaging-lease-recovery | `agentmessagingfixtures` | PASS |
| r30-13-hostile-web-page | `webevidencefixtures` | PASS |
| r30-14-end-to-end | `endtoendfixtures` | PASS |

`executed=14 failed=0 skipped=0 expected_ready=14 coverage_complete=true` — verificado también por
`TestEvaluationRunReportsFullCoverageForTodaysActivatedFixtures`, que ahora exige `skipped=0` en vez de tolerarlo (antes de este tramo, un `skipped>0` era el estado honesto esperado; ahora sería una regresión real y el test lo trata como tal).

Corrección adicional de datos: el catálogo (`internal/evaluation/fixtures/catalog.go`) listaba `investigacion/razonamiento_logico` entre los roles de r30-14 — ese id no existe en `role-catalog.yaml` (el real es `ingenieria_ia/razonamiento_logico`, un departamento distinto). Se corrigió a `investigacion/auditor_cerebro_empresa`, el rol real de research.audit — el campo `Roles` es metadata informativa (no se valida contra el registro), pero una lista de roles incorrecta en el catálogo de un fixture de evaluación es exactamente el tipo de inexactitud silenciosa que esta rama existe para eliminar.

## Corpus AI transferido, no ingerido

Ubicación:

```text
/opt/explorarte/artifacts/rag-ingestion/ai-corpus-v4/
```

| Archivo | SHA-256 | Tamaño observado |
|---|---|---:|
| `documents.jsonl` | `d7d620b534cd8a22836a2a5037d4e5b66c39b92a061e367448b73a41fba7912c` | 17,208,331 bytes |
| `manifest.jsonl` | `365fd7fd91bac170b5adf887341ab6255bbad6c3553a51922c04e56d86114e56` | 3,007,785 bytes |
| `summary.json` | `2253ef065e84655075e988b247b80e3cf7a6059c6d7de25520d18efdd6a22dd1` | 2,550 bytes |
| `CURATION_REPORT.md` | `343dedaaed7c2505ca0a6d3d97ff7fafbe202843ded5e84cf252e41f259b8e18` | 3,093 bytes |

- `documents.jsonl`: 1,418 registros/líneas.
- `manifest.jsonl`: 3,390 registros/líneas.
- El VPS tenía 4.0 GiB libres en `/` al verificar la transferencia (`29G`, 86% usado).
- No se ejecutó admisión, reindex, backfill ni llamada de embeddings.

## Decisiones resultantes del contraste

Se adoptan como patrones:

1. snapshot canónico íntegro separado de vista compacta de ejecución;
2. contabilidad de fresh/cache-read/cache-write/output/reasoning reportada por proveedor;
3. prefijo estable y cacheable, medido por endpoint real;
4. detalle externo recuperable por referencia autorizada;
5. checkpoints verificados sobre el DAG y logs append-only;
6. comunicación punto a punto por delta/artifact y revisión selectiva;
7. autoauditoría capaz de proponer, incapaz de autopromover.

Se rechazan o difieren:

- TOON como requisito (el informe incluso expande incorrectamente su nombre);
- MCP Tool Search sin existir catálogo MCP en el runtime actual;
- MixKV/VisPruner/VTW para LLMs consumidos por API;
- CA-MCP/WIMSE hasta existir una red cross-organización;
- porcentajes comerciales como gates de aceptación;
- LLMLingua sobre contexto autoritativo sin canario de fidelidad.

## Bloqueadores y orden siguiente

1. ~~Completar los runners 01, 02, 09, 10 y 14~~ — cerrado, catálogo 14/14 `runner_ready`.
2. Fases 1–4: telemetría real, renderer dual, caché y divulgación progresiva.
3. Los runners RAG/Memory/namespace necesarios para evaluar ingesta ya están activos; falta el smoke de hardware BGE-M3.
4. Instalar y medir BGE-M3 real con identidad fijada; el disco actual obliga a verificar tamaño/margen antes de descargar pesos.
5. Ingerir un canario estratificado del corpus AI y ejecutar recuperación en español.
6. Solo entonces habilitar la autoauditoría organizacional sobre las implementaciones posteriores — 14/14 runners es condición necesaria (ya cumplida) pero no suficiente: sigue exigiendo BGE-M3 real medido y el corpus ingerido, per la tabla de "Estado heredado y gates de arranque" en DESIGN.md.

La ingesta completa y la autoejecución no son parte de este primer commit documental.

## Verificaciones de este commit

Ejecutadas en el worktree R31 antes de commitear:

| Comando | Resultado |
|---|---|
| `git diff --check` | PASS, sin salida |
| `gofmt -l .` | PASS, sin archivos |
| `go vet ./...` | PASS |
| `go build ./...` | PASS |
| `go test ./... -count=1` | PASS, exit 0 |
| `go test -tags=integration ./... -count=1 -p 1` | PASS, exit 0 contra `r23-integration-pg` en `127.0.0.1:35432` |
| `make verify` | PASS, incluido `check-webevidence-fitness.sh` |

La primera invocación de integración de esta sesión se ejecutó sin exportar `ORG_DATABASE_URL`/`ORG_TEST_DATABASE_URL`: tres tests de `cmd/orgctl` intentaron el default `127.0.0.1:5432` y fallaron con `connection refused`; el resto de paquetes continuó contra sus DSN de test. No se interpretó como éxito ni se ocultó. Se verificó que `r23-integration-pg` estaba `Up` y publicado exclusivamente en `127.0.0.1:35432`, y se repitió toda `./...` con ambos DSN y `ORG_CANONICAL_DIR` explícitos; esa corrida completa terminó en verde. Ningún comando apuntó a Postgres de producción.

## Verificaciones del tramo "cierre de cobertura 14/14"

Ejecutadas antes de cada uno de los tres commits de este tramo (`costledgerfixtures`+`agentmessagingfixtures`, `codeexecutionfixtures`, `endtoendfixtures`):

| Comando | Resultado |
|---|---|
| `gofmt -l .` | PASS, sin archivos |
| `go build ./...` | PASS |
| `go vet ./...` | PASS |
| `go test ./... -count=1` | PASS |
| `go test -tags=integration ./... -count=1 -p 1` contra `r23-integration-pg` (`127.0.0.1:35432`) | PASS |
| `make verify` | PASS |
| `TestEvaluationRunReportsFullCoverageForTodaysActivatedFixtures` (real `orgctl evaluation run` contra Postgres real) | PASS, `executed=14 failed=0 skipped=0 coverage_complete=true`, repetido 3 veces seguidas para confirmar ausencia de estado incoherente entre corridas |
| `orgctl evaluation report --json <run-id>` sobre una corrida real (fuera de los tests, binario compilado) | 14/14 fixtures `Passed: true`, invariantes individuales inspeccionadas una por una |

Nada de este tramo tocó producción — todo corrió contra `r23-integration-pg`, mismo contenedor de test pineado por digest que el resto de la rama. `ORG_CANONICAL_DIR` requirió fijarse explícitamente al invocar el binario `orgctl` fuera de `go test` (el default relativo asume cwd=raíz del repo, cierto para el binario real en despliegue pero no para un proceso de test cuyo cwd es el directorio del paquete) — documentado en el propio test (`evaluation_integration_test.go`) para que no vuelva a sorprender.
