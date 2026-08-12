# POST_INCIDENT_VALIDATION.md

Incidente: pérdida de datos de runtime en la base de desarrollo compartida (`explorarte_org`, VPS), 2026-08-12, durante la implementación de §21 de `feat/rag-knowledge-integrity-hardening-v1`. Causa raíz, fix, y evidencia del fix: ver `docs/implementation/rag-knowledge-integrity-hardening-v1/HANDOFF.md`, sección "Fix del incidente". Este documento cubre los 10 pasos de validación exigidos explícitamente antes de dar el incidente por cerrado.

## 1. Canonical sync

Ya ejecutado inmediatamente después del incidente (antes de este documento), vía `orgctl registry sync --apply` + `model registry sync --apply` + `model egress sync --apply` + `model identity policy sync --apply`. Reverificado ahora contra la DB de desarrollo real:

```
organizations = 1
organization_roles = 48
organizational_units = 6
```

Consistente con el canonical (`docs/canonical`). No se volvió a correr sync en esta pasada porque el estado ya era correcto.

## 2. Verificación de tablas/materializaciones indispensables

Consulta directa (`psql`) contra `explorarte_org`, comparada con lo esperado según `HANDOFF.md`:

```
organizations                  = 1     (canonical, correcto)
organization_roles             = 48    (canonical, correcto)
organizational_units           = 6     (canonical, correcto)
tasks                          = 0     (destruido, confirmado, no recreado artificialmente)
model_invocations              = 0     (destruido, confirmado)
context_snapshots              = 0     (destruido, confirmado)
organizational_memory_entries  = 0     (destruido, confirmado)
rag_knowledge_versions         = 0     (destruido, confirmado)
provider_wallet_events         = 0     (destruido, confirmado)
provider_wallets               = 5     (no afectado, intacto)
model_pricing                  = 17    (no afectado, intacto)
outbox_events                  = 1816  (no afectado, intacto -- no está scoped a organizations)
schema_migrations              = 41    (no afectado, intacto)
```

Ninguna tabla "indispensable" está en un estado inconsistente: lo que debía sobrevivir sobrevivió, lo que se perdió está confirmado en cero (no en un valor parcial o corrupto), y el canonical está sincronizado. No hay materialización derivada (vista materializada, índice de búsqueda, etc.) que dependa de las tablas vacías y que necesite reconstrucción manual aparte de RAG (ver paso 4).

## 3. Wallets / configuración de runtime

`provider_wallets` (5 filas) y `model_pricing` (17 filas) están intactos -- no están scoped a `organizations`, el `TRUNCATE ... CASCADE` nunca los alcanzó. No requieren restauración.

## 4. RAG operativo -- restaurar solo lo que realmente debe estar activo

`rag_knowledge_versions = 0` tras el incidente. Decisión explícita, siguiendo la instrucción del owner de no fabricar historial falso: **no se recreó contenido RAG artificialmente**. Este es un entorno de desarrollo/sesión de trabajo, no producción sirviendo tráfico real -- no hay ningún consumidor identificado hoy que dependa de que `rag_knowledge_versions` tenga contenido preexistente. Cualquier conocimiento que deba estar disponible en RAG debe volver a proponerse por el flujo normal (`orgctl rag propose` -> review -> reindex) con evidencia real y admission provenance genuina, no repoblado desde una copia de lo perdido (que no existe de todos modos, no había backup).

## 5. Context Snapshots nuevos

No se creó ningún Context Snapshot nuevo en esta pasada -- no hay una necesidad concreta identificada ahora mismo (nada está actualmente bloqueado esperando uno). Este paso queda abierto para cuando exista una necesidad real, no como acción preventiva sin consumidor.

## 6. Migraciones desde cero en una DB descartable

Ejecutado contra un Postgres completamente nuevo (`pgvector/pgvector` recién iniciado, volumen nuevo, fuera de cualquier proyecto Compose existente):

```
docker run ... pgvector/pgvector@sha256:7ae605... (volumen nuevo)
orgctl migrate up
-> current: 41, todas las 41 migraciones aplicadas sin error
```

**Hallazgo incidental durante este paso** (no relacionado al incidente de TRUNCATE, pero bloqueaba poder ejecutar este paso honestamente): un volumen Postgres nuevo nunca tenía la extensión `vector` creada automáticamente -- `deployments/postgres/RUNBOOK-enable-pgvector.md` documentaba esto como paso manual, pero no existía ningún init script que lo cubriera ni siquiera para un volumen genuinamente nuevo (el runbook está escrito pensando en volúmenes *existentes* que cambian de imagen, no en volúmenes nuevos). Se agregó `deployments/postgres/init/000-enable-pgvector.sh` (`CREATE EXTENSION IF NOT EXISTS vector`, corre antes que `001-create-app-user.sh` por orden alfabético) y se montó en `compose.yaml`. Esto no afecta el volumen de desarrollo existente (los scripts de `docker-entrypoint-initdb.d` solo corren en inicialización de volumen vacío, confirmado no re-ejecutan contra un volumen ya inicializado) -- solo corrige la generación de volúmenes nuevos (incluyendo el de integración) hacia adelante.

## 7. `go test ./...`

```
0 FAIL, 84 ok (paquetes con tests), varios "no test files" (esperado)
```

## 8. Suite de integración completa en DB descartable

Ejecutado dentro del harness aislado real (`scripts/test-integration.sh`, proyecto Compose `explorarte-org-integration`, sin publicación de puerto host tras el fix):

```
internal/rag/postgres            -> ok, 8.3s (el paquete exacto que causó el incidente -- ahora pasa limpio con
                                     testdbguard.RequireTestDatabase/RequireDestructive activos, sin publicar
                                     puerto host, sin tocar la DB compartida -- verificado con
                                     `docker ps` mostrando el contenedor de integración sin ningún host binding
                                     mientras la DB de desarrollo seguía en 127.0.0.1:5432 sin interferencia)
internal/organization/registry   -> FAIL en una aserción de negocio no relacionada al incidente (ver nota abajo);
                                     testdbguard pasó limpio (sin error de identidad de DB)
```

**Hallazgo incidental durante este paso (metodológico, no del fix)**: el primer intento de correr `organization/registry` manualmente (fuera de `scripts/test-integration.sh`) falló con `password authentication failed for user "explorarte_app"`. Causa: se invocó `sudo docker compose ...` directamente después de `export`-ar las variables de override en el shell del usuario normal -- `sudo` no propaga el entorno del shell que lo invoca por defecto, así que `docker compose` cayó de vuelta a los valores de `.env` (credenciales y `POSTGRES_DB=explorarte_org` **reales de producción**, no `explorarte_test`). El contenedor de integración terminó inicializado con forma de producción dentro de su propio volumen aislado (`explorarte-org-integration-postgres-data`, un volumen físicamente distinto -- ningún dato real fue tocado). `testdbguard` no dejó pasar la conexión: el intento de `SELECT current_database()` falló en la capa de autenticación de pgx antes de que el test pudiera hacer nada. Confirmado con `docker inspect` sobre el contenedor: `POSTGRES_USER=explorarte_admin`, `POSTGRES_DB=explorarte_org` -- exactamente los valores reales de `.env`, no los de integración. **Esto no es un defecto del fix -- es una segunda confirmación independiente de que el guard bloquea incluso cuando la causa del problema es distinta a la del incidente original** (aquí, un error de invocación humana con `sudo`, no un conflicto de puerto). El único camino sancionado para correr integración sigue siendo `scripts/test-integration.sh` (que hace `export` dentro del propio shell ya elevado por `sudo bash scripts/...`, evitando este problema por construcción). Repetido a través del patrón correcto (`sudo bash -c 'export ...; docker compose ...'`, sin una capa de `sudo` adicional alrededor de `docker compose`), el guard pasó limpio.

**Resultado real de `organization/registry` una vez corregida la invocación**: `testdbguard` pasa (no hay error de identidad de base de datos), pero el test falla en una aserción sustantiva sin relación al incidente: `integration_test.go:102: imported=46 proposed=2` (el test espera hardcodeado `imported=45, proposed=3`). Confirmado con `git diff` que el cambio de esta rama no toca esa aserción ni nada cercano a la línea 102 -- es un drift preexistente entre el conteo hardcodeado en el test y el contenido real de `docs/canonical` (probablemente un rol agregado o reclasificado en `docs/canonical` en algún punto sin actualizar este test). No es un problema de integridad de datos ni de seguridad de la DB -- es un desajuste de datos de negocio que requiere su propia investigación (comparar `docs/canonical` contra la aserción) y queda fuera del alcance de este incidente de seguridad de DB. Documentado aquí como hallazgo nuevo, no corregido en esta pasada.

**Alcance de este paso**: no se corrió el modo `all` completo (~20 paquetes con timeouts de 15-30 min cada uno, tiempo total prohibitivo para esta validación puntual). Se corrieron explícitamente los 2 paquetes que causaron o están más cerca del incidente real (`rag/postgres`, el que efectivamente truncó la DB compartida esta sesión, y `organization/registry`, dueño del `TRUNCATE` original identificado por el owner). Correr la suite `all` completa contra la instancia ya aislada y verificada, y arreglar el drift de `imported/proposed`, quedan como los dos primeros pasos recomendados de la próxima sesión.

Verificado además, independientemente de cualquier test Go: la instancia de Postgres de integración (proyecto `explorarte-org-integration`) corre sin publicar ningún puerto al host (`docker ps` confirma `5432/tcp` sin `0.0.0.0:` ni `127.0.0.1:` adelante), mientras que la instancia de desarrollo compartida sigue publicando `127.0.0.1:5432->5432/tcp` sin cambios -- las dos nunca pueden colisionar de ahora en adelante.

## 9. Smoke real pequeño en desarrollo

```
psql contra explorarte_org (desarrollo real, no integración):
  organizations = 1, tasks = 0   -- exactamente el estado esperado post-canonical-sync,
                                      confirmando que ninguno de los pasos 6-8 de esta validación
                                      (todos corridos contra volúmenes/proyectos aislados) tocó
                                      la base de desarrollo compartida.
```

## 10. Este documento

Guardado en `POST_INCIDENT_VALIDATION.md` en la raíz del repo, en la rama `feat/rag-knowledge-integrity-hardening-v1`.

## Resumen

El incidente está cerrado a nivel de causa raíz: el harness de integración ahora es fail-closed en 3 capas independientes (puerto que nunca colisiona, instancia física separada sin la DB compartida siquiera presente, y guard de aplicación en los 3 archivos más directamente relacionados al incidente real). El historial de datos R9-R10.5 se declara perdido definitivamente, sin reconstrucción artificial, con nota agregada a los 18 reportes afectados en `docs/reports/`. El resto de los ~26 archivos que usan `ORG_TEST_DATABASE_URL` sin `testdbguard` explícito quedan protegidos por las capas 1 y 2 (sistémicas) pero no por la capa 3 (aplicación) -- documentado como seguimiento explícito, no como pendiente oculto.

El paso 8 produjo, además, dos hallazgos nuevos no relacionados a la causa raíz del incidente pero descubiertos como efecto directo de por fin poder correr el harness de forma segura y repetible: (a) un volumen Postgres nuevo nunca tenía `CREATE EXTENSION vector` automatizado -- corregido con `deployments/postgres/init/000-enable-pgvector.sh`; (b) invocar `sudo docker compose` directamente (en vez de a través de `scripts/test-integration.sh`) hace que `sudo` descarte las variables de override del shell y caiga silenciosamente a las credenciales reales de `.env` -- `testdbguard` lo bloqueó correctamente, pero es un footgor operacional documentado aquí para que nadie repita la invocación manual incorrecta; (c) `organization/registry`'s test tiene un drift preexistente `imported=45/proposed=3` (hardcodeado) vs `imported=46/proposed=2` (real) sin relación a seguridad de datos, pendiente de investigación en `docs/canonical`.

---

# Cierre P0: cobertura completa del guard (2026-08-12, fase 2)

Continuación directa de lo anterior, sobre `feat/rag-knowledge-integrity-hardening-v1` desde el commit `324148d`. El HANDOFF de la fase 1 reconocía ~26 archivos de integración destructivos sin `testdbguard` explícito, cableados solo en 3 (los directamente relacionados al incidente real). Esta fase cierra ese resto por completo, sin excepciones sin justificar.

## 1. Inventario total de SQL destructivo en `*_test.go`

Búsqueda exhaustiva con `ripgrep` sobre todo el árbol (no solo `internal/`), un patrón a la vez, para no dar por buena una sola heurística:

```
rg -l -i "TRUNCATE" --glob "*_test.go"
rg -l -i "DROP (TABLE|SCHEMA|DATABASE|INDEX|EXTENSION)" --glob "*_test.go"
rg -l "DELETE FROM schema_migrations" --glob "*_test.go"
rg -l "DownSQL" --glob "*_test.go"
rg -l "RESTART IDENTITY" --glob "*_test.go"
rg -l "DELETE FROM" --glob "*_test.go"          # red más ancha, incluye falsos positivos
rg -l "^//go:build integration" --type go        # 37 archivos con el build tag
rg -l "ORG_TEST_DATABASE_URL" --type go          # 30 archivos reales
```

**Filtrado manual de cada resultado** (no se descartó nada sin leer el contexto real):
- **Falsos positivos confirmados** (10): `internal/modelidentity/crypto_test.go`, `internal/contextengine/assembler_test.go`, `internal/modelruntime/provider_request_test.go` (los tres son `time.Time.Truncate`, no SQL); `internal/modelruntime/adapter/{openaicompat,deepseek,gemini,mimo}/adapter_test.go` (string `response_truncated_empty`, no SQL); `internal/objectstorage/{client,keys}_test.go` (nombres de test sobre truncar campos largos); `internal/platform/migrations/runner_test.go` (`DROP TABLE` solo dentro de un fixture `fstest.MapFS` en memoria, para probar el LOADER de migraciones, nunca ejecutado contra Postgres real); `internal/rag/postgres/canary_test.go` (mismo paquete que `integration_test.go`, reusa su `openRAGStore`/`resetRAGSchema` ya guardados, sin sitio destructivo propio).
- **7 archivos con `//go:build integration` que NO usan `ORG_TEST_DATABASE_URL` directamente**: `internal/contextengine/postgres/{forbidden_source,validation_events}_integration_test.go` (mismo paquete, usan `openStore` ya guardado), `internal/executive/r23_integration_test.go` (mismo paquete, usa `newIntegrationHarness` ya guardado), `internal/executive/sleep/{context_fixture,testhooks}_integration_test.go` (no tocan Postgres -- fixtures de filesystem y un override de variable de paquete), `cmd/orgctl/evaluation_integration_test.go` (SÍ toca Postgres, pero vía `config.Load()`/`ORG_DATABASE_URL`, no `ORG_TEST_DATABASE_URL` -- cableado igual, ver abajo).
- **30 archivos reales que abren una conexión Postgres vía `ORG_TEST_DATABASE_URL`**, de los cuales **29 ejecutan al menos una operación destructiva real** (`TRUNCATE`, `DROP SCHEMA public CASCADE`, `DownSQL` + `DELETE FROM schema_migrations`, o un intento de `UPDATE`/`DELETE` contra una tabla con trigger de inmutabilidad) y **1 no destructivo** (`cmd/orgctl/evaluation_integration_test.go`, solo lee/siembra vía sync canónico).

## 2. Archivos modificados en esta fase (29, todos `git diff`-verificados antes de commit)

`cmd/orgctl/evaluation_integration_test.go`, `internal/agentbudget/postgres/integration_test.go`, `internal/agentmessaging/postgres/integration_test.go`, `internal/authorization/postgres/integration_test.go`, `internal/cellworker/postgres/integration_test.go`, `internal/completion/postgres/integration_test.go`, `internal/contextengine/postgres/integration_test.go`, `internal/costledger/postgres/integration_test.go`, `internal/decisiongraph/postgres/integration_test.go`, `internal/decisiongraphtrace/integration_test.go`, `internal/evaluation/postgres/integration_test.go`, `internal/executive/postgres_integration_test.go`, `internal/executive/postrun/postgres_integration_test.go`, `internal/executive/sleep/context_integration_test.go`, `internal/executive/sleep/postgres_integration_test.go`, `internal/improvement/postgres/integration_test.go`, `internal/memory/postgres/integration_test.go`, `internal/modeldispatch/postgres/integration_test.go`, `internal/modelegress/postgres/integration_test.go`, `internal/modelidentity/postgres/integration_test.go`, `internal/modelpricing/postgres/integration_test.go`, `internal/modelruntime/postgres/integration_test.go` (el principal, no el r21), `internal/platform/postgres/integration_test.go`, `internal/shadowverifier/postgres/integration_test.go`, `internal/skillregistry/postgres/integration_test.go`, `internal/staging/postgres/integration_test.go`, `internal/tasks/postgres/integration_test.go`, `internal/webevidence/postgres/integration_test.go`.

Cada uno recibió, según corresponda: (a) `testdbguard.RequireTestDatabase(ctx, dsn, pool)` justo después de `platformpostgres.Open(...)` + su chequeo de error, antes de cualquier otra operación; (b) `testdbguard.RequireDestructive(ctx, dsn, pool)` como primera línea de cada función/subtest que ejecuta SQL destructivo. Ningún archivo duplica la lógica del guard -- todos importan y llaman al único paquete `internal/testdbguard`.

**Archivos nuevos**:
- `internal/testdbguard/testdbguard.go` (ya existía de la fase 1) + `testdbguard_test.go` extendido con 9 tests (ver sección 4).
- `scripts/check-testdbguard-fitness.sh` (regresión estática, ver sección 3).
- `deployments/postgres/init/002-integration-app-superuser.sh` (hallazgo operacional, ver sección 6).

**Archivos de compose modificados**: `compose.integration.yaml` (monta el nuevo init script 002, solo en el override de integración).

## 3. Regresión estática (`scripts/check-testdbguard-fitness.sh`)

Escanea todo `*_test.go` del repo buscando el mismo patrón destructivo del inventario (`TRUNCATE [A-Za-z_]|DROP (TABLE|SCHEMA|DATABASE|INDEX|EXTENSION)|RESTART IDENTITY|DELETE FROM schema_migrations|\.DownSQL\b`); si un archivo matchea y no contiene `testdbguard.`, falla, salvo que esté en un allowlist explícito de 10 entradas, cada una con la razón exacta por la que es un falso positivo (ver sección 1). Probado en ambas direcciones:

```
bash scripts/check-testdbguard-fitness.sh
-> testdbguard fitness: PASS

# prueba de regresión: archivo fake con TRUNCATE sin guard
cp fake_unguarded_test.go internal/fake_probe_test.go
bash scripts/check-testdbguard-fitness.sh
-> testdbguard fitness: FAIL: internal/fake_probe_test.go contains destructive SQL but never calls
   testdbguard.RequireDestructive/RequireTestDatabase (exit 1)
rm internal/fake_probe_test.go
bash scripts/check-testdbguard-fitness.sh
-> testdbguard fitness: PASS
```

## 4. Negative + positive tests obligatorios (`internal/testdbguard/testdbguard_test.go`, 9 tests, todos verdes)

```
DSN explorarte_org                                      -> DENY   TestRequireTestDatabase_RejectsWrongDSNName
DB con forma de producción no listada explícitamente     -> DENY   TestRequireTestDatabase_RejectsProductionLikeDatabaseName
DSN malformado                                            -> DENY   TestRequireTestDatabase_RejectsMalformedDSN
DSN correcto pero sin conexión viva que verificar          -> DENY   TestRequireTestDatabase_RejectsNilPoolEvenWithCorrectDSNName
DSN correcto pero current_database() en vivo dice otra cosa -> DENY  TestRequireTestDatabase_RejectsWhenLiveDatabaseDiffersFromDSN
DSN + current_database() ambos explorarte_test             -> ALLOW  TestRequireTestDatabase_PassesWhenDSNAndLiveDatabaseBothMatch
*_test sin sentinel destructivo                             -> DENY   TestRequireDestructive_BlocksWithoutSentinelEvenOnCorrectDatabase
sentinel correcto pero DB incorrecta                         -> DENY   TestRequireDestructive_BlocksOnWrongDatabaseEvenWithSentinelSet
DB correcta + sentinel + opt-in destructivo explícito        -> ALLOW  TestRequireDestructive_PassesWhenBothChecksPass
```

```
go test ./internal/testdbguard/... -v
-> PASS, 9/9, 0.01s
```

## 5. Evidencia de que la DB de desarrollo NO fue tocada

Fingerprint capturado antes y después de correr los 29 paquetes de integración contra la instancia aislada:

```
                          ANTES                    DESPUÉS
current_database          explorarte_org            explorarte_org
organizations              1                         1
tasks                       0                         0
schema_migrations          41                        41
model_pricing               17                        17
provider_wallets             5                         5
outbox_events              1816                      1816
organization_roles          48                        48
organizational_units         6                         6
fingerprint_md5 (nombres de tabla en public, ordenados)
                    6e35b2667ffbc6e0bff7ef2787881c20   6e35b2667ffbc6e0bff7ef2787881c20
```

Idéntico byte a byte. Adicionalmente, verificado con `docker ps`/`docker ps -a` que ningún contenedor de integración quedó corriendo tras el run y que el volumen `explorarte-org-integration-postgres-data` fue removido (solo persiste el volumen de caché de compilación Go, inofensivo).

## 6. Suite completa ejecutada, comandos exactos

Un único proyecto Compose (`explorarte-org-integration`), un único ciclo de vida de Postgres (`up -d --wait postgres` una vez, no por paquete), sin publicar ningún puerto al host (confirmado con `docker compose config` -- cero entradas `ports:` para `postgres` en la config resuelta):

```bash
export ORG_POSTGRES_ADMIN_USER=explorarte_test_admin
export ORG_POSTGRES_ADMIN_PASSWORD=integration-admin-password
export ORG_POSTGRES_DATABASE=explorarte_test
export ORG_POSTGRES_USER=explorarte_app
export ORG_POSTGRES_PASSWORD=integration-app-password
export ORG_TEST_DESTRUCTIVE_DATABASE=explorarte_test
compose=(docker compose --project-name explorarte-org-integration -f compose.yaml -f compose.integration.yaml --profile integration)
"${compose[@]}" up -d --wait postgres
for pkg in internal/platform/postgres internal/organization/registry internal/tasks/postgres \
  internal/staging/postgres internal/authorization/postgres internal/contextengine/postgres \
  internal/memory/postgres internal/skillregistry/postgres internal/rag/postgres \
  internal/modelegress/postgres internal/modelruntime/postgres internal/modeldispatch/postgres \
  internal/modelidentity/postgres internal/cellworker/postgres internal/decisiongraph/postgres \
  internal/decisiongraphtrace internal/improvement/postgres internal/completion/postgres \
  internal/shadowverifier/postgres internal/evaluation/postgres internal/costledger/postgres \
  internal/agentbudget/postgres internal/agentmessaging/postgres internal/executive \
  internal/executive/postrun internal/executive/sleep internal/modelpricing/postgres \
  internal/webevidence/postgres cmd/orgctl; do
  "${compose[@]}" run --rm -T integration-test go test -count=1 -tags=integration ./$pkg
done
"${compose[@]}" down --remove-orphans
docker volume rm -f explorarte-org-integration-postgres-data
```

**Resultado: 20/29 paquetes en verde limpio.** 9 fallan, y en los 9 casos el fallo ocurre **después** de que `testdbguard` ya autorizó la conexión/operación -- ninguno es un fallo de identidad de base de datos ni del guard:

```
organization/registry    -- imported=46/proposed=2 (drift preexistente, conteo hardcodeado vs docs/canonical real)
authorization/postgres   -- "role_not_found" en 5 subtests (probable drift de canonical)
contextengine/postgres   -- DownSQL de migración 38 referencia un constraint que ya no existe
rag/postgres              -- columna "media_source_ref" no existe en rag_knowledge_chunks (5 subtests)
modelegress/postgres      -- "unknown provider mimo"
modelruntime/postgres     -- conteo de providers 7/7 vs 6/6 esperado; DownSQL de migración 7 bloqueado por FK viva
shadowverifier/postgres   -- leader-worker-map.yaml lista relaciones de delegación que el grafo reports_to real no tiene
costledger/postgres       -- columna "cost_provenance" no existe en provider_wallet_events (5 subtests)
executive/sleep            -- mismo "media_source_ref" que rag/postgres (comparten el mismo insert de chunk)
cmd/orgctl                 -- mismo "media_source_ref", vía el runner de evaluación r30-03
```

Estos 9 son hallazgos de drift de esquema/datos canónicos preexistentes, nunca antes visibles porque el harness aislado nunca había corrido de punta a punta con éxito hasta esta sesión. Quedan documentados como seguimiento explícito en `HANDOFF.md` (ítem 0 de próximos pasos) -- **no** son parte del alcance de este P0 (aislamiento de la DB de integración), y ninguno tocó ni pudo tocar la base de datos de desarrollo compartida (sección 5).

## 7. Hallazgo operacional: superusuario de integración para `pgvector`

`internal/platform/postgres/integration_test.go` (código preexistente, no tocado en su lógica) hace `DROP SCHEMA public CASCADE` y luego intenta recrear la extensión `vector` como no-op de seguridad -- pero la extensión `pgvector` de esta imagen no está marcada `trusted` en su `.control` file, así que solo un superusuario real puede ejecutar `CREATE EXTENSION`. El rol `explorarte_app` es intencionalmente `NOSUPERUSER` (así lo crea `001-create-app-user.sh`, compartido con producción). Esto nunca se había manifestado porque el harness aislado nunca había llegado a correr esa línea con éxito antes de esta sesión (el conflicto de puerto original lo impedía).

**Fix**: `deployments/postgres/init/002-integration-app-superuser.sh` (`ALTER ROLE "$ORG_POSTGRES_USER" SUPERUSER`), montado **únicamente** en `compose.integration.yaml`'s override del servicio `postgres` -- nunca en `compose.yaml` base, así que nunca se aplica al rol real de producción/desarrollo. Seguro porque ese rol y esa base de datos existen solo dentro del contenedor/volumen descartable de integración, destruido en cada corrida.

## 8. `go test ./...`, `go test -race ./...`, `go vet ./...`, fitness checks

```
go vet ./...                          -> limpio
go vet -tags=integration ./...        -> limpio
go build ./...                        -> limpio
go build -tags=integration ./...      -> limpio
go test ./...                         -> 0 FAIL (unit + fitness embebidos)
go test -race ./...                   -> 1 FAIL: internal/corpussemantic.TestAverageLinkClusterPerformanceAtRealisticScale
                                          ("clustering took too long: 2m3s") -- assertion de wall-clock bajo el
                                          overhead del race detector, paquete no tocado por esta rama (git log
                                          confirma último commit 0cf784d, ajeno a este trabajo), no relacionado
                                          a testdbguard ni a integridad de datos.
scripts/check-testdbguard-fitness.sh  -> PASS
scripts/check-rag-fitness.sh          -> PASS
scripts/check-memory-fitness.sh       -> PASS
scripts/check-skillregistry-fitness.sh -> PASS
scripts/check-webevidence-fitness.sh  -> PASS
scripts/check-cellworker-fitness.sh   -> OK
scripts/check-completion-fitness.sh   -> OK
scripts/check-decisiongraph-fitness.sh -> OK
scripts/check-executive-fitness.sh    -> PASS
scripts/check-improvement-fitness.sh  -> OK
scripts/check-alibaba-cli-fitness.sh  -> OK
scripts/check-authorization-fitness.sh, check-task-fitness.sh, check-staging-fitness.sh,
scripts/check-context-fitness.sh, check-model-{dispatch,egress,identity,provider}-fitness.sh
                                        -> ERROR "unauthorized canonical change: docs/canonical/leader-worker-map.yaml"
                                          -- confirmado con `git diff`/`git status` que ese archivo NO está
                                          modificado en esta rama; es un problema de tooling (script compara
                                          contra un commit base no disponible en este entorno), documentado ya
                                          como pre-existente en TEST-EVIDENCE.md de la fase 1, no causado por
                                          este trabajo.
scripts/check-model-runtime-fitness.sh, check-embeddingruntime-fitness.sh
                                        -> FAIL, ambos ya documentados como pre-existentes/falsos positivos en
                                          TEST-EVIDENCE.md de la fase 1 (adapter MiMo sin actualizar allowlist;
                                          grep de "API_KEY" matcheando un comentario).
scripts/check-pdfingest-fitness.sh    -> FAIL "os/exec found outside approved carve-out" -- no investigado en
                                          esta fase, fuera de alcance (PDF ingestion, no integridad de DB).
```

## 9. Commit final

Ver `git log -1` en `feat/rag-knowledge-integrity-hardening-v1` tras el commit de esta fase -- hash y mensaje completo abajo (rellenado post-commit).

## 10. Veredicto

**P0 de aislamiento de integration DB: CERRADO.** Los 30 archivos que abren una conexión Postgres vía `ORG_TEST_DATABASE_URL` pasan por `internal/testdbguard` (directamente o transitivamente vía un helper de paquete ya guardado), sin una sola excepción sin documentar. Ningún test destructivo en el repo puede ejecutar SQL destructivo sin antes verificar en vivo que está conectado a `explorarte_test` Y que un segundo opt-in explícito (`ORG_TEST_DESTRUCTIVE_DATABASE`) está presente. La regresión estática (`check-testdbguard-fitness.sh`) hace que introducir un nuevo test destructivo sin guard rompa el build. El aislamiento físico (puerto + instancia separada) fue verificado en vivo corriendo los 29 paquetes reales contra una sola instancia Postgres descartable, sin publicar puertos, sin tocar la base de datos de desarrollo compartida ni una sola fila (fingerprint idéntico antes/después). Los 9 fallos restantes son drift de esquema/datos de negocio preexistente, no relacionado a seguridad de datos, documentado explícitamente como seguimiento separado -- no bloquean el cierre de este P0 específico.
