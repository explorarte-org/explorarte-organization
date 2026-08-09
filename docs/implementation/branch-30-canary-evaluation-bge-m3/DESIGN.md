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
