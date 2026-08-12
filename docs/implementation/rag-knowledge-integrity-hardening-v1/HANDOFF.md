# RAG/Knowledge/Memory/Embedding/Context Integrity Hardening V1 — HANDOFF.md

## Estado

5 de 30 secciones de la spec original implementadas con rigor completo (código + tests + migración donde aplica, todo verde). El resto queda **explícitamente diferido** por tamaño real de la tarea (ver `DEFERRED_BY_DESIGN` abajo) — no por decisión de que no importen.

## Completado (ver DESIGN.md para el detalle de cada uno)

- §6 — Gemini vector validation (dimensión + NaN/Inf) en el adapter boundary.
- §7 — BGE-M3 full identity attestation (tokenizer_revision/normalization/pooling).
- §13 — Object Storage immutable evidence (`PutObjectIfAbsent`, keys canónicas).
- §18.3 — ProviderRender/contextcompiler single source of truth para la partición stable/dynamic.
- §21 — RAG DB immutability trigger (migración 000041).

## Pendiente (no implementado en esta fase)

Todo lo demás de la spec original: §4 (retrieval signal / Purpose-as-QueryText), §4.1 (RAG multi-scope single embedding), §5 (embedding profile identity completa en 4 tablas Postgres), §8 (embedding financial outcome/accounting con fases BEFORE_REQUEST/AMBIGUOUS/RESPONSE_RECEIVED), §9 (backfill durable/claimed/finite), §10 (media digest/actual input hash), §11 (PDF ingestion preflight ordering), §12 (no fabricar admission provenance), §14 (chunk manifest ligado a Knowledge approval), §15 (PDF size/chunk constraint preflight), §16 (Object Storage JSON cap vs media cap), §17 (visual-only PDF safety attestation), §18.1/§18.2 (ProviderRender V2 completo: boundary determinístico + trust envelope), §19/§20 (eliminar SQL N+1 en RAG y Memory), §22 (Memory read authorization — requiere primero verificar si ya existe capability canónica), §24 (error taxonomy 57014).

**Nota sobre §24**: se investigó — el código real de `internal/rag/postgres/store.go`'s `mapError` NO reproduce el defecto exacto descrito en la spec (un SQLSTATE no reconocido cae a un wrap genérico, no a `ErrInvalidRequest`). Puede que el defecto exista en otro paquete no revisado, o que ya no aplique tal como está descrito — requiere investigación dedicada antes de tocar código, no se tocó nada en esta fase para evitar un cambio no verificado en un área donde otro trabajo (§21) ya estaba activo.

**Nota sobre §19/§20 (SQL N+1)**: no se investigó en profundidad en esta fase — es un ítem grande (batch loaders para RAG Store.List/Query/ApprovedForNamespace y Memory Store.List/Search) que amerita su propia sesión dedicada.

## BLOCKED_BY_POLICY_DESIGN

**§22 — Memory read authorization**: `memory.Manager.Get`/`List` no tienen actor/capability gate. La capability-matrix canónica auditada NO define `memory.read_own`/`memory.read_role` ni equivalente. Por instrucción explícita de la spec original, no se inventó una capability nueva dentro de esta rama. Si en el futuro se agrega una capability apropiada, debe cablearse con tests reales contra esa capability, y cualquier seam interno tipo `GetForRevalidation` debe ser explícito (no heredar un bypass interno hacia un path actor-facing), siguiendo el patrón ya usado por RAG. **No investigado en esta fase** (no llegamos a este punto) — queda pendiente confirmar en la próxima fase si la capability-matrix cambió.

## BGE_SIDECAR_CONTRACT_UPDATE_REQUIRED

El sidecar productivo BGE-M3 (`/opt/explorarte/bgem3-sidecar/server.py`, VPS) sigue sin control de versiones propio. §7 endureció el contrato Go-side para EXIGIR `tokenizer_revision`/`normalization`/`pooling` en las respuestas de `/v1/health` y `/v1/embed` — el sidecar actual NO envía estos campos, así que **cualquier llamada real a través de este adapter fallará con `ErrModelIdentityDrift` hasta que el sidecar se actualice explícitamente para atestiguarlos**. Esto es intencional (fail-closed, no fabricar identidad no atestiguada) — confirmado que BGE-M3 no está activo en el `compose.yaml` real al momento de este cambio, así que no hay impacto en producción hoy. Antes de activar el profile BGE-M3 en cualquier ambiente, el sidecar debe actualizarse para incluir esos 3 campos en ambas respuestas.

## Incidente durante §21 — pérdida de datos de runtime en DB de desarrollo

Durante la implementación de §21 (subagente delegado), un test de integración usó `TRUNCATE organizations ... CASCADE` contra la base de datos de desarrollo **compartida** del VPS (no una instancia aislada), por un conflicto de puerto al intentar levantar Postgres de integración por separado. Esto vació en cascada: `tasks`, `model_invocations`, `context_snapshots`, `organizational_memory_entries`, `rag_knowledge_versions`, `provider_wallet_events`, y otras tablas org-scoped — es decir, **todo el historial de datos en DB de las fases R9 a R10.5 de este mismo repo, generadas en sesiones previas**. `model_pricing`, `provider_wallets`, `outbox_events`, y `schema_migrations` no fueron afectadas (no están scoped a `organizations`).

**Recuperado**: estado canónico (`organizations`=1, 48 roles, 6 units) vía `orgctl registry sync --apply` + `model registry sync --apply` + `model egress sync --apply` + `model identity policy sync --apply`.

**No recuperable**: cualquier dato de runtime no derivado de sync canónico. No existe backup.

**No afectado**: el repositorio git (nada de esto tocó git), y todos los reportes markdown ya escritos en `docs/reports/` (son archivos, no filas de DB) — pero cualquier verificación futura que dependiera de re-consultar la DB para un `invocation_id`/`task_id` específico citado en esos reportes ya no es posible.

**Confirmado por el owner de esta sesión**: proceder con el resto del hardening, siendo más cuidadosos con el aislamiento de tests de integración en adelante (nunca correr contra la instancia compartida; resolver conflictos de puerto usando un puerto distinto para Postgres de integración, no cayendo de vuelta a la instancia compartida).

## Fix del incidente (2026-08-12): guard central + aislamiento real

El owner de esta sesión hizo su propio root-cause y dio una especificación exacta de 5 requisitos antes de permitir más pruebas de integración destructivas. Todo lo siguiente ya está implementado y verificado en la rama:

1. **`internal/testdbguard`** (paquete nuevo, 8 tests unitarios, 100% verde): `RequireTestDatabase(ctx, dsn, pool)` exige que el DSN nombre `explorarte_test` Y que `SELECT current_database()` en vivo confirme lo mismo — nunca confía en `ORG_ENVIRONMENT=test` porque el propio test lo auto-inyecta. `RequireDestructive` añade una segunda condición deliberada: `ORG_TEST_DESTRUCTIVE_DATABASE=explorarte_test` debe estar seteado explícitamente antes de permitir TRUNCATE/DownSQL.
2. **Colisión de puerto eliminada**: `compose.integration.yaml` ahora define `postgres: ports: !reset []`, así que el Postgres de integración (proyecto Compose separado, volumen separado `explorarte-org-integration-postgres-data`) nunca intenta publicar `127.0.0.1:5432`, nunca puede chocar con el escape hatch de BGE-M3 de `compose.yaml`, y por lo tanto nunca puede forzar una caída de vuelta a la instancia compartida. Verificado con `docker compose ... config` — cero referencias a publicación de puerto para `postgres` en la config resuelta.
3. **Separación de credenciales/DB a nivel PostgreSQL**: confirmado (no solo asumido) leyendo `deployments/postgres/init/001-create-app-user.sh` — el Postgres de integración es una instancia completamente separada (contenedor + volumen propios bajo el proyecto `explorarte-org-integration`), inicializada desde cero con `POSTGRES_DB=explorarte_test` únicamente. La base `explorarte_org` **nunca existe** dentro de esa instancia — no hace falta revocar `CONNECT` porque no hay nada que conectar. Esta es la defensa más fuerte de las 5: aunque un agente copie mal el DSN, no hay `explorarte_org` del otro lado al que conectarse dentro del proyecto de integración.
4. **Sentinel `ORG_TEST_DESTRUCTIVE_DATABASE`**: agregado a `compose.integration.yaml` (con default `explorarte_test`, seguro porque este archivo solo se usa dentro del proyecto aislado) y documentado en `scripts/test-integration.sh` junto a los demás `export ORG_POSTGRES_*`.
5. **Wiring real del guard — CERRADO 2026-08-12 (fase 2), 100% de cobertura**: los 30 archivos del repo que usan `ORG_TEST_DATABASE_URL` llaman ahora a `testdbguard.RequireTestDatabase`/`RequireDestructive`, directamente o (para 3 archivos que comparten helpers `openStore`/`openRAGStore`/`newIntegrationHarness` con un archivo ya guardado del mismo paquete) transitivamente. Ver `POST_INCIDENT_VALIDATION.md`, sección "Cierre P0", para el inventario completo, los 29 archivos modificados en esta fase, y la evidencia de ejecución real contra los 29 paquetes de integración en un único proyecto Compose aislado sin publicación de puerto host.

**Guard estático agregado**: `scripts/check-testdbguard-fitness.sh` escanea todo `*_test.go` en busca de SQL destructivo (`TRUNCATE`, `DROP TABLE/SCHEMA/DATABASE/INDEX/EXTENSION`, `RESTART IDENTITY`, `DELETE FROM schema_migrations`, `.DownSQL`) y falla el build si aparece fuera de un archivo que también referencia `testdbguard` o del allowlist explícito y documentado (10 falsos positivos confirmados: `time.Truncate`, nombres de test, un fixture `fstest.MapFS` que nunca toca Postgres real, y `canary_test.go` que reusa el helper ya guardado de su paquete).

**Hallazgo operacional adicional en esta fase**: el rol `explorarte_app` de integración no podía recrear la extensión `pgvector` tras un `DROP SCHEMA public CASCADE` (pgvector no está marcada `trusted`, exige superusuario real) — bug preexistente del propio test de `internal/platform/postgres`, nunca antes ejercitado porque el harness aislado nunca había llegado a correr limpio hasta esta sesión. Corregido otorgando `SUPERUSER` al rol de integración vía `deployments/postgres/init/002-integration-app-superuser.sh`, montado únicamente en `compose.integration.yaml` (nunca en `compose.yaml`/producción) — seguro porque ese rol y esa base de datos viven solo dentro del contenedor/volumen descartable de integración.

**Hallazgos de negocio/esquema NO corregidos en esta fase, fuera del alcance del P0 de aislamiento** (documentados aquí porque solo se hicieron visibles al poder correr el harness completo por primera vez): `imported=46/proposed=2` en `organization/registry` (drift entre el conteo hardcodeado del test y `docs/canonical`); `role_not_found` en varios subtests de `authorization/postgres`; `column "media_source_ref" of relation "rag_knowledge_chunks" does not exist` (afecta `rag/postgres`, `executive/sleep`, `cmd/orgctl` — sugiere una migración de columna faltante o fuera de orden); `column "cost_provenance" of relation "provider_wallet_events" does not exist` en `costledger/postgres`; `unknown provider "mimo"` en `modelegress/postgres`; conteo `enabled=7 available=7, want 6/6` y un `DownSQL` de migración 7 bloqueado por una FK viva en `modelruntime/postgres`; drift de `leader-worker-map.yaml` vs el grafo `reports_to` real en `shadowverifier/postgres`. Ninguno de estos es un fallo de `testdbguard` — todos ocurren después de que el guard ya autorizó la conexión/operación, y ninguno tocó ni pudo tocar la base de datos de desarrollo compartida (fingerprint verificado idéntico antes/después de correr los 29 paquetes).

## Próximos pasos recomendados (orden sugerido)

0. (Nuevo, alta prioridad) Investigar y corregir el drift de esquema/canonical descubierto en el cierre del P0 de aislamiento (ver arriba: columnas faltantes `media_source_ref`/`cost_provenance`, conteos de roles/providers, `leader-worker-map.yaml`) — bloquea que el harness de integración pase limpio en 9 de 29 paquetes.

1. §5 (embedding profile identity, 4 tablas) — el P0 de mayor impacto restante, pero requiere una migración cuidadosa que preserve todos los embeddings existentes.
2. §8 (embedding financial accounting) — reutiliza el patrón ya validado en `internal/modelruntime` esta misma sesión (fases BEFORE_REQUEST/AMBIGUOUS/RESPONSE_RECEIVED), debería ser más rápido de portar que de diseñar desde cero.
3. §4 (retrieval signal) — el fix conceptualmente más importante para la calidad real de RAG/Memory, pero también el de mayor superficie (toca contextprovider, RAG Manager, Memory Manager).
4. §19/§20 (N+1) y §9 (backfill durable) — mejoras de robustez/performance, menor riesgo de romper autoridad/seguridad si se hacen mal, buenos candidatos para paralelizar con subagentes de nuevo.
