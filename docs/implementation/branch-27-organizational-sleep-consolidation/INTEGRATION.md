# Rama 27 — Organizational Sleep & Memory Consolidation

## Estado

- Rama: `feat/27-organizational-sleep-consolidation`.
- Base exacta: `feat/26-executive-closure-lesson-job` en `c84ac951ce73499dc2a4729edd24043a06583cf5`.
- SHA de implementación antes de este handoff: `adecda5ef55951f34767db86349f7158b3433b68`.
- SHA de handoff (`docs(r27)`): `6dc9ef1455607a4e08fc2a0cc29b9b2be6e1257c`.
- Migración propia: **ninguna**.
- Cambios canónicos: **ninguno**.
- PR: ninguno.
- Merge a `main`: ninguno.
- Estado de verificación: **ejecutado end-to-end en el VPS contra PostgreSQL 17 real** (`r23-integration-pg`, puerto 35432). `gofmt -l .`, `go vet ./...`, `go build ./...`, unit tests, race tests e integración real del paquete `internal/executive/sleep` corridos y en verde. Un bug real de integración fue encontrado y corregido durante esta verificación — ver "Verificación ejecutada en VPS" más abajo.

## Objetivo

R27 implementa un ciclo offline y manual de consolidación organizacional. Lee experiencia real ya persistida por R25/R26, detecta recurrencia y contradicción de forma determinista, calcula una confianza auditable y propone —nunca aprueba— un `rag.KnowledgeVersion` candidate con provenance completa.

Principio de diseño:

> Dreams can hallucinate. Memory cannot.

Esta rama no contiene ninguna llamada a LLM, no genera experiencias sintéticas, no intenta reconstruir chain-of-thought y no publica conocimiento automáticamente.

```text
orgctl sleep run
  -> PostgreSQL ExperienceReader
     -> decision_graph_runs
     -> decision_verifications
     -> tasks / task_attempts
     -> model_invocations
     -> decision_records (solo provenance opcional de pases)
     -> anti-join rag_knowledge_evidence_refs
  -> GroupExperiences(unit, role, provider)
  -> recurrence >= 3
  -> contradiction/pass-rate analysis
  -> deterministic CandidateBody + confidence + EvidenceRefs
  -> rag.Manager.Propose
     -> authorization: rag.propose_candidate
     -> lifecycle=candidate
```

## Correcciones factuales al plan previo

### 1. No fue necesario crear ni activar un rol nuevo

La investigación del plan tenía dos falsos negativos sucesivos:

- `investigacion/research_worker_hourly` sí existe con `authority_class: research_execution`, pero sigue en `status: proposed_profile_required`. El registry obliga a que ese status permanezca `enabled=false` y `executable=false`, y `D-006` continúa abierto para su activación. R27 no reabre esa decisión.
- `investigacion/auditor_cerebro_empresa` ya existe, está en `status: imported_source`, es ejecutable y tiene `authority_class: transversal_audit`. Su descripción canónica es precisamente auditar el Cerebro Empresa/RAG de forma transversal. `capability-matrix.yaml` ya concede `rag.propose_candidate` a `transversal_audit`.

Por eso R27 usa:

```text
ProposerRoleID = investigacion/auditor_cerebro_empresa
```

No se modificó `role-catalog.yaml`, `capability-matrix.yaml`, `organization.yaml` ni ningún otro archivo de `docs/canonical`. Esto además preserva la fitness existente de inmutabilidad canónica.

### 2. `decision_verifications` es la fuente autoritativa de contradicción

R25 no crea `decision_records` para `CompletionFail`/`CompletionInconclusive`: registra primero `decision_verifications` con `label=contradicted|unknown` y retorna. `decision_records` existe solamente para una selección terminal `verified|inferred`.

Por tanto, usar únicamente `decision_records.verification_label` habría eliminado las contradicciones reales del dataset de consolidación.

R27 usa:

```text
decision_verifications
  verifier_ref='internal/completion'
  verifier_version='phase2'
  label IN (verified,inferred,unknown,contradicted)
```

`decision_records` se une opcionalmente para conservar `decision_hash` cuando sí existe.

Los runs con una verificación no-pass pueden permanecer `status='running'` en R25 porque no hubo selección terminal. Por eso el lector acepta `decision_graph_runs.status IN ('succeeded','running')`, pero únicamente cuando existe una verificación real `internal/completion/phase2`, el `task_attempt` está terminado y la tarea está en estado terminal. No interpreta cualquier run `running` como experiencia.

## Paquete nuevo

`internal/executive/sleep` contiene:

- `doc.go`: boundary del paquete y regla observed-only.
- `ports.go`: puertos estrechos `ExperienceReader` y `CandidateProposer`.
- `types.go`: experiencia, grupos, análisis, portability, resultados y config.
- `grouping.go`: recurrencia, contradicción, bands de pass-rate, portability y confidence.
- `candidate.go`: `CandidateBody` determinista y mapeo a `rag.ProposeRequest`.
- `service.go`: `RunCycle` bounded.
- `postgres.go`: lector real de experiencia durable.
- unit tests.
- PostgreSQL integration tests.

No importa adapters de modelos ni ejecuta `modelruntime`. Los tests de integración sí consultan el model registry para atribuir correctamente proveedor/modelo a fixtures durables, pero eso no forma parte del código productivo del sleep cycle.

## Elegibilidad de experiencia

El SQL productivo filtra:

1. organización exacta;
2. última verificación `internal/completion` `phase2` del run;
3. label en `verified|inferred|unknown|contradicted`;
4. ventana temporal por `decision_verifications.created_at`;
5. `task_attempt.state='finished'`;
6. tarea terminal;
7. una invocación de modelo `status='succeeded'` para ese task/attempt, de donde se obtiene provider/model;
8. run `succeeded` o `running` únicamente bajo las condiciones anteriores;
9. el run no debe aparecer ya como `decisiongraph:run:<id>` en `rag_knowledge_evidence_refs` de esa organización.

El query es bounded (`MaxExperiences`, default 1024) y con ordering estable.

## Recurrencia y contradicción

Agrupación:

```text
(assigned_unit_id, assigned_role_id, provider_id)
```

No se inventó `task_type`; el esquema actual no contiene una taxonomía durable de tipo de tarea.

Defaults:

```text
min_group_size = 3
recurrence_target = 8
```

Success:

```text
verified + inferred
```

Failure/uncertainty:

```text
contradicted + unknown
```

Reglas exactas:

```text
pass_rate >= 0.85
  -> strong observed pattern

0.40 < pass_rate < 0.85
  -> mixed contradiction
  -> candidate permitido
  -> applicability_conditions declara explícitamente que NO es un claim incondicional

pass_rate <= 0.40
  -> no RAG proposal
```

No se transforma un patrón mayoritariamente fallido en “conocimiento”. Ese caso pertenece a aprendizaje correctivo/memory, no a esta rama.

## Portabilidad cross-provider

Para grupos recurrentes del mismo `(unit_id, role_id)` se calculan rates por provider.

Clasificaciones:

```text
single_provider_observation
consistent_eligibility_band_across_providers
provider_dependent
```

Una diferencia de bands entre providers no se generaliza: el candidate contiene `provider_dependent=true; do not generalize across providers`.

Una coincidencia de band entre varios providers se documenta como observación de transferabilidad, explícitamente `observational, not causal`.

Cuando se usa evidencia cross-provider, cada run sigue apareciendo individualmente en `Sources` y `EvidenceRefs`; nunca se reemplaza evidencia por un resumen sintético.

## Fórmula exacta de confidence

Implementada de forma determinista:

```text
recurrence_factor = min(1.0, recurrence_count / recurrenceTarget)
provider_multiplier = 1 + 0.1 * min(max(providers_seen-1, 0), 3)
contradiction_penalty = 0.15 si grupo primario está en banda mixed; 0 en otro caso

confidence = recurrence_factor
           * pass_rate
           * provider_multiplier
           - contradiction_penalty

confidence = clamp(confidence, 0, 1)
```

Se redondea a seis decimales para output estable. `CandidateBody` guarda tanto `confidence` como cada término de la fórmula.

## Candidate RAG

No se añadió un struct persistente paralelo a RAG. `BuildCandidate` produce `rag.ProposeRequest`.

Mapping:

```text
claim                    -> Title + CandidateBody.claim
conditions/counters      -> JSON versionado en Body
confidence               -> Body.confidence + Body.confidence_terms
source experience ids    -> EvidenceRefs
provenance               -> CandidateBody.sources
source kind               -> operational
source boundary           -> internal/executive/sleep
namespace                 -> department del grupo primario
proposer                  -> investigacion/auditor_cerebro_empresa
```

Schema del body:

```text
organizational-sleep-consolidation.v1
```

Cada source conserva:

- `run_id`
- `task_id`
- `attempt_id`
- `unit_id`
- `role_id`
- `provider_id`
- `provider_model_id`
- `verification_label`
- `evidence_digest`
- `decision_hash` si existe
- `observed_at`
- si pertenece al primary provider group

Cada `EvidenceRef`:

```text
reference = decisiongraph:run:<run-id>
digest    = decision_verifications.evidence_set_hash
```

`Admission.AttestedAt` usa el timestamp real más reciente de las verificaciones incluidas. No usa wall-clock durante la síntesis, evitando el bug de idempotencia que R26 descubrió para `memory.Admission.AttestedAt`.

## IDs e idempotencia

`groupHash` deriva de `(unit, role, provider)`.

`evidenceHash` deriva determinísticamente del conjunto ordenado de experiencias reales.

```text
document_id = sleep-<groupHash16>-<evidenceHash16>
version_id  = <document_id>-v1
idempotency = sleep:<full-groupHash>:<full-evidenceHash>
```

La key incluye grupo **y** evidencia: dos provider groups pueden compartir el mismo evidence set al calcular portability, pero representan claims primarios distintos y no deben colisionar bajo una sola key.

## Cómo se marca “ya consolidado”

No se añadió tabla ni columna.

Un run se considera consumido por consolidación si existe cualquier evidence ref RAG de esa organización:

```text
rag_knowledge_evidence_refs.reference = decisiongraph:run:<run-id>
```

El anti-join del reader impide procesarlo en ciclos posteriores. Esto conserva una única fuente durable de provenance.

Importante: el run queda marcado cuando existe el candidate RAG durable, no cuando meramente fue leído o agrupado. Un fallo antes de `rag.Manager.Propose` no consume experiencia.

## Gobernanza

`RunCycle` solo llama:

```text
rag.Manager.Propose
```

No llama:

```text
Review
Deprecate
Archive
Reindex
```

Por tanto el sleep cycle solo puede crear `LifecycleCandidate` y está sujeto a la capability real `rag.propose_candidate`.

La prueba de integración adicional demuestra el camino posterior como una acción separada del humano:

```text
candidate
 -> authorization.Service.RequestApproval(rag.publish_approved)
 -> empresa/human DecideRequest(approve)
 -> rag.Manager.Review(... ApprovalRequestID)
 -> approved
 -> nueva RequestApproval para reindex
 -> empresa/human approve
 -> rag.Manager.Reindex(... ApprovalRequestID)
 -> Context Engine Build
```

Esto valida recuperación futura sin dar al sleep cycle capacidad de auto-aprobar.

## CLI

Nuevo comando manual:

```bash
orgctl sleep run [--json] [--window=720h]
```

Default window: 720h / 30 días.

El comando:

- exige migraciones listas;
- abre el `PostgresReader` productivo;
- abre `ragbootstrap` productivo;
- ejecuta un único `RunCycle`;
- devuelve métricas del ciclo;
- termina.

No hay scheduler, polling loop, hook en `gatedComplete` ni ejecución automática por invocación.

## Observabilidad devuelta

`CycleResult` expone:

- window start/end
- eligible experiences
- groups observed
- recurring groups
- mixed contradiction groups
- skipped insufficient runs
- skipped low pass-rate
- candidates proposed
- candidates reused
- detalle por proposal con group, IDs, confidence y run IDs

No se añadió un backend de métricas nuevo.

## Tests unitarios escritos

`grouping_test.go`:

- boundary exacto 0.40;
- mixed 0.60;
- boundary 0.85;
- verified/inferred como success;
- fórmula exacta de confidence;
- clamp;
- portability consistente y provider-dependent.

`candidate_test.go`:

- determinismo ante input reordenado;
- provenance operational-only;
- latest real observation como attestation time;
- un evidence ref por run;
- mixed conditions no incondicionales;
- ausencia de synthetic/simulation provenance;
- idempotency distinta para claims provider distintos con evidence set compartido.

`service_test.go`:

- propuesta de grupo recurring mixed;
- propagación organization/proposer/namespace;
- reuse idempotente;
- weak group skip;
- insufficient group skip.

`cmd/orgctl/sleep_test.go`:

- registro del comando;
- subcomandos inválidos fallan antes de tocar DB.

## PostgreSQL 17 integration escrita

### `TestOrganizationalSleepAgainstRealPostgres`

Construye materialización canónica real y model registry real, después crea tres experiencias durables para el mismo grupo:

```text
pass
pass
fail
```

Las trazas se registran con el adapter productivo de R25:

```text
runtimeadapter.DecisionGraph.RecordAttemptDecision
```

El fixture de tasks/attempts/context/model invocation se inserta directamente, siguiendo el mismo boundary utilizado por tests de R26: el objetivo del test no es volver a ejercitar todo el CEO orchestrator, sino alimentar R25 con una ejecución durable coherente y después probar R27.

Comprueba:

- 3 experiencias elegibles;
- 1 grupo recurrente;
- 1 grupo mixed;
- 1 candidate propuesto;
- lifecycle=`candidate`;
- proposer auditor correcto;
- source=`operational`;
- source boundary correcto;
- los tres `decisiongraph:run:<id>` persisten como evidence refs reales;
- candidate no aparece en retrieval antes de review/reindex;
- segunda ejecución del sleep no vuelve a consumir los mismos runs.

### `TestApprovedSleepCandidateBecomesContextEvidenceOnlyAfterHumanGovernance`

Construye tres passes reales, ejecuta sleep y verifica `candidate`; luego realiza dos approvals humanas reales (review y reindex), ejecuta retrieval y finalmente `contextengine.Service.Build` con un source-root temporal aislado.

Comprueba que el segmento recuperado es:

```text
SourceKind            = rag_evidence
TrustClass            = untrusted
InstructionClass      = data
DataClass             = organizational
MayGrantCapabilities  = false
Included              = true
```

Así el criterio de “conocimiento consolidado recuperable por Context Engine” se cumple solamente después de la gobernanza RAG ya existente.

## Source-root del test de Context Engine

El repo no contiene un `AGENT.md` productivo en la raíz. El test crea un directorio temporal con:

- `AGENT.md`
- `ingenieria_ia/AGENT.md`
- `ingenieria_ia/orquestador/PERFIL.md`

De esta forma no depende de `/opt/explorarte/organization` ni del layout del host del VPS.

## Archivos modificados

Solo:

```text
cmd/orgctl/main.go
cmd/orgctl/sleep.go
cmd/orgctl/sleep_test.go
internal/executive/sleep/*
docs/implementation/branch-27-organizational-sleep-consolidation/INTEGRATION.md
```

Explícitamente no se tocaron:

```text
docs/canonical/*
migrations/*
internal/decisiongraph/*   # R14/R25 internals no se modifican
internal/improvement/*
internal/contextengine/*
internal/rag/*
```

## Verificación pendiente en VPS

Ejecutar desde checkout real de la rama:

```bash
git fetch origin
git checkout feat/27-organizational-sleep-consolidation
git reset --hard origin/feat/27-organizational-sleep-consolidation

gofmt -w internal/executive/sleep cmd/orgctl/sleep.go cmd/orgctl/sleep_test.go cmd/orgctl/main.go
gofmt -l .
git diff --check c84ac951ce73499dc2a4729edd24043a06583cf5...HEAD

go vet ./...
go build ./...
go test ./internal/executive/sleep/... ./cmd/orgctl/...
go test -race -short ./internal/executive/sleep/... ./cmd/orgctl/...
```

Después, contra PostgreSQL 17 real ya disponible en VPS:

```bash
export ORG_TEST_DATABASE_URL='postgres://postgres:postgres@localhost:35432/explorarte_test?sslmode=disable'
go test -tags=integration ./internal/executive/sleep -count=1 -v
```

No usar un wildcard combinado con `./internal/executive/...` para esta validación; R24–R26 ya documentaron interferencia/`SQLSTATE 40001` cuando varios integration packages comparten el mismo DB en una invocación combinada.

CLI manual, después de tener al menos tres runs elegibles que no estén consumidos por RAG:

```bash
go build -o /tmp/orgctl ./cmd/orgctl
export ORG_DATABASE_URL='postgres://postgres:postgres@localhost:35432/explorarte_test?sslmode=disable'
export ORG_ENVIRONMENT=test
export ORG_CANONICAL_DIR="$PWD/docs/canonical"
/tmp/orgctl sleep run --json --window=720h
```

La salida debe ser un `CycleResult`; un dataset sin recurrencia o ya consumido puede devolver cero proposals y eso no constituye un fallo.

## Verificación ejecutada en VPS

Ejecutado el 2026-08-08 contra `r23-integration-pg` (PostgreSQL 17 real, puerto 35432), siguiendo exactamente los pasos de la sección anterior.

Resultado inicial: `gofmt`, `vet`, `build`, unit tests y race tests en verde. La integración real (`go test -tags=integration ./internal/executive/sleep -count=1 -v`) falló en `TestApprovedSleepCandidateBecomesContextEvidenceOnlyAfterHumanGovernance`:

```
context_integration_test.go:174: build context: context PostgreSQL constraint violation:
ERROR: new row for relation "context_segments" violates check constraint
"context_segments_source_version_check" (SQLSTATE 23514)
```

**Causa raíz** (no es un bug de R27, es un bug preexistente en `internal/rag/contextprovider/provider.go` que R27 fue la primera rama en ejercitar con un `document_id`/`version_id` real lo bastante largo para exponerlo): `encodeVersion` concatenaba seis campos separados por `:` — incluyendo el content hash del chunk (`chunkHash`, 64 hex chars) — en un único string persistido en `context_segments.source_version`, columna con `CHECK (length(trim(source_version)) BETWEEN 1 AND 240)` (`migrations/000006_create_context_engine.up.sql`). Con los IDs reales que genera `internal/executive/sleep/candidate.go` (`documentID = "sleep-" + groupHash[:16] + "-" + evidenceHash[:16] + "-v1"`, 42 chars) más `namespaceID`/`generationID` reales, el string codificado llegaba a 248 caracteres — 8 por encima del límite.

`chunkHash` se parseaba de vuelta en `parseVersion` pero **nunca se leía** en `ValidateVersion` (que solo usa `versionID`, `canonicalHash`, `namespaceKind`, `namespaceID` y `generationID`). Se eliminó ese campo no usado de `encodeVersion`/`parseVersion`/`sourceRecord` (`internal/rag/contextprovider/provider.go`), reduciendo el string codificado en 65 caracteres sin perder ninguna verificación real. Tras el fix:

```
go build ./...                                                          # limpio
go test ./internal/rag/... ./internal/contextengine/...                 # verde
bash scripts/check-rag-fitness.sh                                       # PASS
bash scripts/check-context-fitness.sh                                   # PASS
go test -tags=integration ./internal/executive/sleep -count=1 -v        # 10/10 PASS, incluido el test que antes fallaba
```

Ningún archivo canónico, de migración, ni de `internal/rag`/`internal/contextengine` fuera de esta única función fue tocado. El fix vive en la misma rama `feat/27-organizational-sleep-consolidation`.

## Checks agregados al criterio de aceptación

Para marcar R27 como verificada deben ser ciertos simultáneamente:

- `gofmt -l .` vacío;
- `go vet ./...` verde;
- `go build ./...` verde;
- unit tests sleep/orgctl verdes;
- race tests verdes;
- integración PostgreSQL exacta del paquete verde;
- candidate real queda `candidate` antes de approval;
- contradicción real queda presente en provenance;
- evidence refs apuntan a runs reales;
- segundo ciclo no reprocesa runs consumidos;
- human approval + reindex hacen recuperable el conocimiento;
- Context Engine conserva RAG como untrusted data sin capabilities.

## Checks globales heredados

Las ramas R25/R26 ya documentaron problemas globales preexistentes ajenos a R27:

- algunas suites históricas de `decisiongraph`/`decisiongraphtrace` esperan migration tip 17 aunque el tip heredado ya sea 18;
- `scripts/check-executive-fitness.sh` puede señalar cambios de R14 que fueron introducidos legítimamente por R25 antes de esta rama.

R27 no toca R14 ni intenta “arreglar” esas suites. Si `make verify`/`verify-all` falla únicamente por esos puntos heredados, registrar el output exacto y comparar contra el parent `c84ac95` antes de atribuirlo a R27.

## Fuera de alcance

- LLM synthesis;
- synthetic/simulation experiences;
- dreaming;
- automatic forgetting/deprecation;
- scheduler/poller;
- auto-approval de RAG;
- publicación directa del auditor;
- pgvector;
- nueva taxonomía `task_type`;
- nuevas tablas/columnas;
- cambios en `internal/improvement`;
- cambios canónicos para activar `research_worker_hourly`.

## Rama siguiente

El “Organizational Dreaming” solicitado originalmente corresponde a la numeración real siguiente (`feat/28-...`). Sigue bloqueado hasta que esta rama sea validada con experiencia real y el camino candidate -> human approval -> indexed RAG -> Context Engine haya sido demostrado en PostgreSQL 17.
