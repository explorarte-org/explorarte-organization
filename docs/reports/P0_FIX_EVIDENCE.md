# P0_FIX_EVIDENCE.md

Implementación de los fixes P0-A → P0-F aprobados sobre `P0_FIX_AUDIT.md`, ejecutada con 3 subagentes en paralelo (financiero/telemetría/build-identity; task-identity+curation-contract; dedup canónico), consolidada y verificada por mí. **No se ejecutó r9. No se hizo commit. No se abrió PR. No se tocó Context Engine, rubric_version, clustering, threshold 0.88, ni el model route de DeepSeek.**

---

## 1-3. HEAD / migration tip antes y después

- HEAD: **sin cambios**, `7cd60785683cb197b3941974d1727311447af4fa`, branch `feat/bootstrap-closure-observability-prolog` (nada commiteado, todo queda como working-tree diff).
- Migration tip **antes**: `000036_add_chunk_provenance`.
- Migration tip **después**: `000037_add_cost_settlement_provenance` (única migración creada, por el agente P0-A). Verificado vía `TestMigrationTipIs37AndContiguous` (test actualizado por mí desde `36`→`37`, ver sección 18) y aplicada de verdad contra el Postgres real del VPS (`{"applied":[37],"current":37}`), con down verificado dentro de una transacción con rollback (no se dejó la DB abajo).

## 4. Archivos modificados/creados (consolidado, verificado vía `git status --short`)

```
M  cmd/orgctl/tasks.go                                  (P0-B: task claim-specific)
M  internal/costledger/ports.go                         (P0-A: PendingReconciliationMarker)
M  internal/costledger/postgres/store.go                (P0-A: provenance/outcome stamping)
M  internal/modelruntime/adapter/deepseek/adapter.go     (P0-A: usage-survival + cache tokens)
M  internal/modelruntime/adapter/deepseek/adapter_test.go
M  internal/modelruntime/costgate.go                     (P0-A: MarkPendingReconciliation en la interfaz)
M  internal/modelruntime/costgate/gate.go                (P0-A: implementación)
M  internal/modelruntime/dispatch_service.go             (P0-A: EL FIX CENTRAL)
M  internal/modelruntime/domain.go                       (P0-A/F: BuildSHA, cache tokens en Usage)
M  internal/modelruntime/interfaces.go                   (P0-A: Usage en FailureCommand)
M  internal/modelruntime/postgres/claims.go               (P0-F: runtime_build_sha)
M  internal/modelruntime/postgres/results.go              (P0-A: insertRecoveredUsage)
M  internal/modelruntime/service_test.go                 (P0-A: suite de la máquina de estados)
M  migrations/r21_tip_test.go                            (yo: 36→37, test desactualizado, no relacionado a lógica)
?? internal/corpuscuration/corpuscuration_output_contract.go       (P0-C, nuevo)
?? internal/corpuscuration/corpuscuration_output_contract_test.go  (P0-C, nuevo)
?? internal/corpuscuration/corpuscuration_identity_preflight.go    (P0-E: reescrito — ya existía, modificado por el agente 3)
?? internal/corpuscuration/corpuscuration_identity_preflight_test.go (P0-E: reescrito)
?? cmd/orgctl/corpuscuration_dedup_cli.go                (P0-E, nuevo, opcional, no wireado en corpus.go)
?? migrations/000037_add_cost_settlement_provenance.up.sql
?? migrations/000037_add_cost_settlement_provenance.down.sql
```

(`cmd/orgctl/corpus.go`, `compose.yaml`, `internal/corpuscluster/`, `internal/corpusenrich/`, `internal/corpussemantic/`, `docs/reports/` son dirty de sesiones anteriores, sin relación con este trabajo, no tocados aquí.)

Nota de disclosure honesta del agente P0-A (no oculto): tocó `internal/modelruntime/costgate.go` (archivo raíz, distinto de `costgate/gate.go`) fuera de su lista de ownership literal, porque `CostBudgetGate` (la interfaz) se define ahí y `MarkPendingReconciliation` tenía que agregarse a la interfaz para compilar. Verificado que no genera colisión con los otros dos agentes (ninguno tocó ese archivo).

## 5-6. Máquina de estados financiera exacta implementada

Dos campos nuevos, ortogonales, en `provider_wallet_events` (no un nuevo `kind` — se respetó la corrección de diseño del owner):

```
cost_provenance   TEXT  ∈ {actual_provider_reported, estimated_locally, reconciled_provider, unknown}
financial_outcome TEXT  ∈ {released_not_sent, actual, estimated_pending_reconciliation, reconciled}
```

Mapeo implementado por fase de `AdapterError.Phase`:

| Fase | Antes | Ahora |
|---|---|---|
| `AdapterFailureBeforeRequest` | Release automático (correcto) | Release automático, ahora **explícitamente** etiquetado `unknown`/`released_not_sent` |
| `AdapterFailureResponseReceived`, con usage recuperable del envelope | **BUG: release automático (tratado como gratis)** | `Reconcile` con el usage real → `actual_provider_reported`/`actual`. La invocación sigue `failed` a nivel de negocio — éxito financiero y éxito de tarea quedan explícitamente desacoplados |
| `AdapterFailureResponseReceived`, sin usage recuperable (`response_read_failed`, `response_json_invalid`) | Release automático (bug) | `MarkPendingReconciliation` → `estimated_locally`/`estimated_pending_reconciliation`, la reserva queda parqueada, no se libera |
| `response_normalization_failed` (`FailAfterResponse`) | Quedaba `reserved` para siempre, sin resolución | El adapter ya había decodificado bien el envelope externo → usage recuperado y comprometido como `actual` |
| `AdapterFailureAmbiguous` / after-send no clasificado | `reserved` sin anotación | `MarkPendingReconciliation` explícito |

**Hallazgo del propio agente durante la implementación, no anticipado en el audit**: `response_truncated_empty` en este código SIEMPRE ocurre DESPUÉS de que `json.Unmarshal` ya decodificó el envelope externo — es decir, cae en el caso "con usage recuperable" (`actual`), no en el caso "sin usage" como el audit había hipotetizado. Solo `response_read_failed` (fallo de lectura de bytes, antes de completar el JSON) y `response_json_invalid` (el envelope externo ni siquiera parseó) caen genuinamente en el caso sin usage.

## 7. `provider_wallet_events` sigue append-only

La tabla tiene un trigger append-only (de la migración 000021). Se relajó (`CREATE OR REPLACE FUNCTION`) para permitir **exactamente una** anotación posterior de `cost_provenance`/`financial_outcome` sobre una fila por lo demás intacta — `kind`/`amount`/`provider_id`/`invocation_id`/`created_at` siguen siendo inmutables. El down-migration restaura el trigger estricto original.

## 8. Cómo sobrevive el usage a un fallo de negocio

`RawResponse` (lo que el adapter retorna) ya tenía campos `InputTokens`/`OutputTokens`/`ProviderReported`, sin usar en los caminos de error. Ahora el adapter los rellena (más los tokens de cache) en TODA rama que ocurre después de que su propio `json.Unmarshal` tuvo éxito, aunque el resultado se rechace después (`response_choice_count_invalid`, `response_content_invalid`, `tool_call_name_missing`, `response_content_filtered`, `response_truncated_empty`). `dispatch_service.go` convierte eso en un `*Usage` (`recoveredUsage()`), lo pasa vía el nuevo campo `FailureCommand.Usage` hasta `RejectProviderResponse`/`FailAfterResponse`, que ahora insertan una fila en `model_invocation_usage` (antes solo `CompleteInvocation`, el camino de éxito, escribía esa tabla).

## 9. Before/after por AdapterFailurePhase (tabla pedida explícitamente)

| escenario | provider_reached | usage conocido? | wallet event | financial_outcome | cost_provenance | efecto en budget | invocation outcome |
|---|---|---|---|---|---|---|---|
| pre-provider (credential/circuit/encoding) | no | n/a | released | released_not_sent | unknown | libera, sin cambio | failed |
| provider success | sí | sí | committed | actual | actual_provider_reported | resta del budget (sin cambio de comportamiento, regresión verde) | succeeded |
| `response_read_failed` | sí | **no** | (antes: released) → ahora: **queda reservado** | estimated_pending_reconciliation | estimated_locally | sigue restando del budget disponible | failed |
| `response_truncated_empty` | sí | **sí** (hallazgo nuevo) | (antes: released) → ahora: committed | actual | actual_provider_reported | resta del budget con el monto real | failed |
| `response_json_invalid` | sí | no | (antes: released) → ahora: queda reservado | estimated_pending_reconciliation | estimated_locally | sigue restando | failed |
| `response_content_invalid` | sí | sí | (antes: released) → ahora: committed | actual | actual_provider_reported | resta el monto real | failed |
| `response_normalization_failed` | sí | sí (recuperado del adapter) | (antes: atascado `reserved` sin resolver) → ahora: committed | actual | actual_provider_reported | resta el monto real | failed |
| HTTP error del provider (no-2xx) | sí | depende | según disponibilidad de usage, igual que arriba | actual o estimated_pending_reconciliation | según caso | resta correctamente | failed |
| transport ambiguo (timeout post-send) | desconocido | no | (antes: reserved sin anotar) → ahora: anotado explícito | estimated_pending_reconciliation | estimated_locally | sigue restando | ambiguous |
| curation output contract inválido (P0-C, semántico, no financiero) | sí | sí (la llamada al provider ya se contabilizó arriba) | n/a (es un fallo de negocio posterior a la contabilidad) | (hereda el financial_outcome de la respuesta que sí llegó) | (idem) | sin efecto adicional | failed, `curation_output_contract_invalid` |

## 10. Fórmula de CostGate effective-budget

**No requirió código nuevo.** `ProviderWallet.Available() = BalanceUSD - ReservedUSD` ya restaba todo lo no liberado. El bug era que se liberaba de más (ver sección 6) — al dejar de liberar incorrectamente, `ReservedUSD` ahora cuenta correctamente los montos `estimated_pending_reconciliation` sin que se necesitara una fórmula nueva. Confirmado por el agente como hallazgo, no como cambio de código adicional.

## 11. Cache-token support

`model_invocation_usage` gana `prompt_cache_hit_tokens`/`prompt_cache_miss_tokens` (BIGINT nullable). El adapter DeepSeek los captura como `*int64` (NULL si el provider no los envía, nunca asumidos en cero), con `validateCacheTokens` que loguea (no falla la request) si `prompt_tokens != cache_hit + cache_miss`.

**Limitación disclosed explícitamente por el agente**: queda completo en los caminos de FALLO (`insertRecoveredUsage`). En el camino de ÉXITO (`CompleteInvocation`), el usage se construye en `internal/modelruntime/normalizer.go` — archivo fuera del ownership asignado a este agente — así que hoy los cache tokens leerán NULL en invocaciones exitosas hasta que se agregue esa misma copia de campo ahí. Documentado en código como TODO. **Pendiente real, no resuelto en esta fase.**

## 12. Contrato CLI de `task claim-specific`

```
orgctl task claim-specific TASK_ID --worker ID [--role ID] [--lease DURATION] [--json]
```

Envuelve `Service.ClaimTaskByID` (que ya existía committeado en el branch, junto con `Store.ClaimSpecific` — el único gap real era la exposición CLI). En éxito escribe UN objeto `ClaimedTask` (no una lista, a diferencia del `claim` genérico). **Fail-closed real**: tras el claim, verifica `value.Task.ID != id` y si alguna vez fuera cierto, imprime error y retorna `exitDrift` (3) sin emitir JSON — nunca continúa. En la práctica esto es defensa en profundidad: el SQL de `ClaimSpecific` (`WHERE id=$1 ... FOR UPDATE SKIP LOCKED`) solo puede devolver la fila pedida o ninguna, así que el chequeo a nivel CLI debería ser inalcanzable, pero garantiza que nunca haya sustitución silenciosa.

## 13. Cambio en el driver

`canary15_driver_v2.py` (local, nunca ejecutado):
1. `task claim --batch 1` → `task claim-specific <task_id_creado>`; si el ID reclamado no coincide, **aborta ese cluster inmediatamente** (antes: `claim_mismatch` se registraba pero el driver seguía usando la tarea equivocada como fuente de verdad — ese comportamiento fue eliminado).
2. Rama `FAILED_AFTER_RETRIES` ahora llama `task result --outcome failed` + `task finalize --outcome failed`, espejo exacto de las llamadas que ya existían en la rama de éxito — ninguna tarea vuelve a quedar reclamable indefinidamente.
3. Comentario `TODO(next agent/session)` dejado sobre el punto donde `ValidateCurationOutputContract` debería invocarse — el validador Go existe y está testeado, pero no se wireó vía CLI en el driver en esta fase (corte de alcance explícitamente aprobado en el brief del agente, para no tocar archivos fuera de su ownership).
4. Comentario extenso agregado sobre `collapse_duplicates()`/`normalize_title()` marcándolas como reimplementación Python desactualizada frente al Go corregido (P0-E) — riesgo de drift documentado, no eliminado.

## 14. Validador de contrato semántico de curación (P0-C)

`internal/corpuscuration/corpuscuration_output_contract.go`: `ValidateCurationOutputContract(expectedClusterID string, expectedWorkIDs []string, output CurationOutput) *OutputContractViolation`. Rechaza (con `Classification="curation_output_contract_invalid"` y detalle de qué exactamente falló):
- `cluster_id` distinto al esperado
- cualquier Work faltante, extra, duplicado o desconocido (igualdad de conjuntos exacta)
- cualquier Work sin tier o con tier fuera de `{P0, P1, silver_only, review_required}`

9/9 tests: output válido exacto aceptado; Work faltante rechazado; Work extra rechazado; Work duplicado rechazado; Work desconocido rechazado; `cluster_id` incorrecto rechazado; `works` vacío rechazado; tier inválido rechazado; tier faltante rechazado. **El caso real de r8 (8 enviados, 7 en el output) queda cubierto exactamente por el test de "Work faltante rechazado".**

## 15. Fusión de metadata canónica en dedup (P0-E)

`internal/corpuscuration/corpuscuration_identity_preflight.go` reescrito. Prioridad de selección de canónico (reemplaza "lexicográficamente menor gana"):
1. Abstract presente y no degenerado > abstract ausente/degenerado
2. Entre empates, título verificado (ej. Semantic Scholar) > título sin verificar
3. Desempate final: lexicográficamente menor (determinismo de última instancia, se conserva)

Nuevo tipo `CollapseResult{Canonical, AliasOf, AliasesOf, MergedIdentifiers}` — `MergedIdentifiers` es la unión de DOI/arXiv/ACL de TODO el grupo colapsado, no solo del ganador. La función original `CollapseDuplicateWorksInCluster` se conserva como wrapper deprecado con la misma firma (ningún caller real encontrado vía grep, además de la reimplementación Python que sigue separada).

Test obligatorio del caso real (GraphReader): Work A (abstract ausente, DOI presente, ID lexicográficamente menor) vs Work B (abstract presente, arXiv presente, mismo título normalizado) → el canónico resultante ahora es B (gana por abstract presente), y el conjunto de identificadores fusionado contiene AMBOS el DOI de A y el arXiv de B. 8/8 tests nuevos pasan, incluyendo un test que prueba explícitamente que el comportamiento lexicográfico viejo ya NO ocurre.

## 16. Telemetría de fallos del provider (P0-D)

**No implementada.** El agente P0-A determinó que el lugar natural (`ProviderOutcome`, en `internal/modelruntime/provider_adapter.go`) está fuera de su lista de archivos autorizados, y extenderlo para `finish_reason`/`json_error_class`/etc. hubiera requerido tocarlo. En vez de agregar schema sin poblar o tocar un archivo no asignado, lo omitió por completo y lo reportó explícitamente como pendiente, en vez de hacerlo a medias en silencio. **Esto es un blocker real pendiente antes de considerar el Gate F (Observability) cumplido — ver sección "Bloqueadores restantes".**

## 17. Build/adapter identity (P0-F)

`modelruntime.BuildSHA` (var de paquete, default `"unknown"`, pensado para overridearse vía `-ldflags`), persistido en la nueva columna `model_dispatch_attempts.runtime_build_sha` al momento del claim. **La inyección real de `-ldflags` en el build de Docker NO se hizo** — se consideró fuera del alcance seguro de esta sesión (cambiar `Dockerfile`/`compose.yaml` sin poder verificar el build de imagen completo). Documentado como seguimiento explícito. Hoy, sin ese wiring, `runtime_build_sha` se persistirá como el string literal `"unknown"` en producción — el campo existe y el pipeline de persistencia funciona, pero no lleva información real todavía.

## 18. Tests agregados y resultado de `go test`

Consolidado (verificado por mí, no solo reportado por los agentes): **`go build ./...` limpio sobre el repo completo. `go test ./...` completo: 100% verde** tras un fix mío de un test preexistente desactualizado (`migrations/r21_tip_test.go` tenía `36` hardcodeado como conteo esperado de migraciones; lo actualicé a `37` y renombré `TestMigrationTipIs36AndContiguous`→`TestMigrationTipIs37AndContiguous`, verificando que el nombre de la migración 37 coincide con el archivo real creado). Este fue el único fallo en el run completo, no relacionado a ningún bug de lógica de los fixes — era simplemente un contador desactualizado.

Nuevos tests, por área:
- **Financiero** (`internal/modelruntime/service_test.go`): `TestDispatchBeforeRequestFailureReleasesReservation`, `TestDispatchSuccessCommitsActualUsageRegression`, `TestDispatchResponseReceivedWithRecoveredUsageCommitsActualEvenThoughFailed`, `TestDispatchResponseReceivedWithoutUsageIsParkedNotReleased`, `TestDispatchNormalizationFailureCommitsActualFromAdapterUsage`, `TestDispatchAmbiguousTransportIsParkedNotReleased`, `TestDispatchEverySettlementPathSettlesExactlyOnce`.
- **Adapter/cache** (`adapter_test.go`): `TestDispatchPreservesUsageOnBusinessFailuresAfterDecodeSucceeded`, `TestDispatchHasNoRecoverableUsageWhenDecodeNeverSucceeded`, `TestDispatchParsesCacheTokens`.
- **Curación** (`corpuscuration_output_contract_test.go`): 9 tests (sección 14).
- **Dedup** (`corpuscuration_identity_preflight_test.go`): 8 tests (sección 15).
- **Task identity**: cubierto indirectamente por los 18 tests existentes de `internal/tasks` + 29 de `cmd/orgctl` que ya pasaban (no se encontró necesidad de tests nuevos separados más allá de los que el CLI wrapper ya ejercita, dado que `ClaimSpecific`/`ClaimTaskByID` ya tenían cobertura previa en el branch).

## 19-20. `go test ./...` — resultado final consolidado

```
ok  para TODOS los paquetes del repo (incluye internal/modelruntime + subpaquetes,
    internal/costledger*, internal/corpuscuration, internal/tasks, cmd/orgctl,
    internal/rag, internal/memory, internal/executive, migrations, etc.)
0 FAIL
```
(antes de mi fix del test de migración: 1 FAIL, `migrations` package, por el contador hardcodeado — ya corregido y verificado en verde).

Migración 000037 aplicada de verdad contra el Postgres real del VPS (`orgctl migrate up` → `{"applied":[37],"current":37}`); down verificado dentro de una transacción con rollback explícito (no se dejó la base de datos en el estado "abajo").

## 21. Smoke calls reales a DeepSeek

**Ninguno.** Todo el comportamiento del adapter (incluyendo el parseo de cache tokens) se verificó con `httptest.NewTLSServer` (servidores fake locales), sin costo real incurrido contra la API de DeepSeek en esta fase.

## 22. Riesgos residuales conocidos

1. **P0-D (telemetría de fallos del provider) no implementada** — requiere tocar `internal/modelruntime/provider_adapter.go` (fuera del ownership asignado en esta ronda). El Gate F (Observability) del pedido original NO se puede dar por cumplido todavía.
2. **Cache tokens no llegan al camino de éxito** (`normalizer.go`, mismo problema de ownership) — solo se capturan hoy en los caminos de fallo con usage recuperado. Fix de una línea, pero no hecho.
3. **`runtime_build_sha` no lleva información real** — el campo y el pipeline existen, pero sin inyección de `-ldflags` en el build de Docker, persistirá `"unknown"` en producción hasta que se haga ese wiring.
4. **CLI wrapper de dedup (`corpuscuration_dedup_cli.go`) no está conectado** a `orgctl corpus` — existe pero no es alcanzable desde la CLI real todavía.
5. **La reimplementación Python del dedup en el driver no fue eliminada**, solo marcada con un comentario de riesgo de drift — si alguien vuelve a usar el driver sin revisar ese comentario, seguiría corriendo la lógica vieja (aunque ya no exactamente la misma que tenía el bug original del Go, porque nunca se tocó la lógica Python en sí).
6. **El validador de contrato de curación (P0-C) no está wireado en el driver** — existe y está testeado en Go, pero un futuro run del driver no lo llamaría automáticamente sin ese trabajo adicional de plomería.

## 23. Bloqueadores exactos restantes antes de r9

Repasando los Gates del pedido original:

- **Gate A (Financial Correctness)**: ✅ cumplido — el bug central está corregido y verificado con tests.
- **Gate B (Budget Safety)**: ✅ cumplido — sin cambio de fórmula necesario, el fix del Gate A lo arregla de raíz.
- **Gate C (Task Identity)**: ✅ cumplido — `claim-specific` expuesto y fail-closed, driver corregido.
- **Gate D (Curation Completeness)**: ✅ el validador Go existe y está testeado — **pero no está conectado al driver todavía** (punto 6 de riesgos). Antes de r9, alguien debe wirearlo o el driver seguirá aceptando outputs incompletos como "succeeded" igual que en r8.
- **Gate E (Dedup)**: ✅ cumplido a nivel Go — el driver sigue usando su propia reimplementación vieja (marcada, no reemplazada). Antes de r9, decidir si se acepta ese riesgo o se fuerza el wiring del CLI.
- **Gate F (Observability)**: ❌ **no cumplido** — P0-D no implementado.
- **Gate G (Reproducibility)**: ⚠️ parcial — el campo existe pero no lleva SHA real todavía.
- **Gate H (Tests)**: ✅ cumplido — `go test ./...` en verde.

**No recomiendo autorizar r9 todavía** con Gate F en cero y Gate D/E con el wiring del driver pendiente — son exactamente los mecanismos que evitarían que r9 repita los mismos problemas que r8. Quedan como trabajo pendiente explícito, no silenciado.

## 24. Confirmación final

- **R9 NO fue ejecutado.** Ningún proceso del driver corrió, ninguna tarea de canario se creó, ningún dispatch a DeepSeek relacionado con corpus curation ocurrió.
- Context Engine no fue tocado por ningún agente.
- `rubric_version`, clustering, threshold 0.88, model route de DeepSeek: sin cambios.
- Ningún `git add`/`commit`/PR — el working tree del VPS queda con los diffs descritos arriba, sin commitear, esperando tu revisión.
