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

## Próximos pasos recomendados (orden sugerido)

1. §5 (embedding profile identity, 4 tablas) — el P0 de mayor impacto restante, pero requiere una migración cuidadosa que preserve todos los embeddings existentes.
2. §8 (embedding financial accounting) — reutiliza el patrón ya validado en `internal/modelruntime` esta misma sesión (fases BEFORE_REQUEST/AMBIGUOUS/RESPONSE_RECEIVED), debería ser más rápido de portar que de diseñar desde cero.
3. §4 (retrieval signal) — el fix conceptualmente más importante para la calidad real de RAG/Memory, pero también el de mayor superficie (toca contextprovider, RAG Manager, Memory Manager).
4. §19/§20 (N+1) y §9 (backfill durable) — mejoras de robustez/performance, menor riesgo de romper autoridad/seguridad si se hacen mal, buenos candidatos para paralelizar con subagentes de nuevo.
