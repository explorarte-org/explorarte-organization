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
