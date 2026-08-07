# Rama 19 — Skill Registry Lifecycle

## Estado

Implementación cerrada para validación externa en PostgreSQL real.

- Base efectiva: `main` con Rama 18 (`organizational-memory`).
- Migración propia: `000016_create_skill_registry`.
- No hay PR ni merge a `main` en este estado.
- La validación PostgreSQL/`make verify-all` debe ejecutarse antes de considerar la rama mergeable.

## Objetivo

Rama 19 implementa un registro durable, evidence-backed, del ciclo de vida de las skills de la organización: alta de una skill, aprobación del owner, calificación como candidata, activación, suspensión/retiro, y asignación (y revocación) a roles. No implementa generación automática de skills, búsqueda/monitoreo activo de skills externas, ni sustituye la fuente de skills que hoy consume el Context Engine (ver "Fuera de alcance").

La propiedad central es la misma que en Rama 18 para memoria organizacional: un agente puede **proponer** una skill o una transición de su ciclo de vida, pero no convertirla por sí solo en estado operativo (activo/asignado) sin pasar por la puerta de autorización canónica.

## Modelo de dominio

`Skill` identifica de forma inmutable una skill dentro de una organización (`id`, `created_by_role`, `created_at`). Cada skill tiene una o más `SkillVersion`, que conservan de forma inmutable:

- `skill_id`, `version` (entero secuencial), `manifest`, `source`
- `content_hash`, `manifest_hash`, `canonical_hash`
- `supersedes_version`

Y de forma mutable, avanzando estrictamente por la máquina default-deny de `transitions.go`:

- `lifecycle`, `owner_approval`, `validation`, `activation_approval`, `revision`, `updated_at`

### Lifecycle

```text
draft -> human_approved -> candidate -> active <-> suspended
draft, human_approved, candidate, active, suspended -> retired
```

`Activate` exige, en orden, evidencia de owner approval, evidencia de validación (schema + capability review + instruction safety, las tres explícitamente `pass`) y evidencia de activation approval — ninguna puede faltar ni ser sintetizada por el propio agente proponente.

### Source provenance

`SourceRecord` distingue origen `internal` de `github`. Todo origen `github` debe venir pinneado a un commit SHA de 40 caracteres (`owner/repo@<sha>`); una referencia mutable (`@main`, una rama, un tag) es rechazada. Los imports legacy (`SKILL.md`, `SKILL(1).md`, …) requieren la bandera explícita `legacy_imported=true`; sin ella, cualquier archivo que no sea `SKILL.md` es rechazado.

### Assignment

Una `SkillAssignment` fija una versión **activa exacta** de una skill a un rol (no "la última activa"). Solo puede existir una asignación activa por `(organización, rol, skill)` — lo enforce un índice único parcial en Postgres. Revocar requiere razón explícita y dispara vía Manager la misma puerta de autorización que asignar.

## Authorization

Rama 19 reutiliza dos capabilities ya definidas en `capability-matrix.yaml`: `organization.propose_skill` (medium, approval: owner) y `organization.activate_skill` (high, approval: owner). No se agregó ninguna capability nueva a la matriz canónica — decisión deliberada para no expandir el modelo de gobierno sin sign-off explícito.

Mapeo de capability por operación (`internal/skillregistry/authz/gate.go`):

- `AuthorizeProposal` (alta de un draft) → `organization.propose_skill`.
- `AuthorizeLifecycleChange`: `draft→human_approved` y `human_approved→candidate` → `organization.propose_skill` (todavía dentro del pipeline de revisión); `→active`, `→suspended`, `→retired` → `organization.activate_skill` (cambia estado operativo).
- `AuthorizeAssignmentChange` (assign y revoke) → `organization.activate_skill`, ya que conceder o retirar acceso operativo a una skill es tan sensible como activarla.

`internal/skillregistry/manager.go` autoriza siempre antes de persistir, en cada intento — incluyendo reintentos idempotentes — igual que Rama 18. El dominio puro (`internal/skillregistry` sin `authz`) no importa `internal/authorization`; `internal/skillregistry/authz` adapta el engine canónico.

## Persistencia PostgreSQL

Migración `000016` crea:

- `skill_registry_skills`: identidad inmutable de cada skill;
- `skill_registry_versions`: contenido inmutable (manifest/source/hashes) + proyección de lifecycle mutable con optimistic concurrency;
- `skill_registry_lifecycle_events`: audit trail append-only de transiciones;
- `skill_registry_assignments`: proyección mutable de asignación (activo→revocado únicamente);
- `skill_registry_assignment_events`: audit trail append-only de assign/revoke;
- `skill_registry_skill_idempotency` / `skill_registry_assignment_idempotency`: replay protection durable.

Invariantes DB relevantes:

- `lifecycle` solo transiciona según la máquina default-deny (trigger `skill_registry_guard_version_update`), igual matriz que `internal/skillregistry/transitions.go`;
- contenido (`manifest`, `source`, `*_hash`, `supersedes_version_id`, `created_at`) es inmutable tras el insert;
- `owner_approval`, `validation`, `activation_approval` son inmutables una vez escritos (no se puede reescribir evidencia ya registrada);
- cada transición de lifecycle requiere un evento de auditoría ya insertado en la misma transacción con el mismo `updated_at`;
- solo una asignación `active` por `(organization_id, role_id, skill_id)` (índice único parcial);
- ninguna fila de auditoría, idempotencia o identidad admite `UPDATE`/`DELETE`; las filas de lifecycle/assignment no admiten `DELETE`.

El store usa transacciones serializables y `SELECT ... FOR UPDATE` para mutaciones.

## Idempotencia

`CreateSkill` e `CreateAssignment` requieren idempotency key. Misma key + mismo hash de contenido/identidad → devuelve el registro existente sin re-autorizar la persistencia (aunque el Manager sí vuelve a evaluar la autorización, ver arriba). Misma key + contenido distinto → conflicto (`ErrIdempotencyConflict`).

## CLI

`orgctl skill` agrega:

```text
propose   approve   qualify   activate   suspend   retire
assign    revoke
get-version   list-versions   get-assignment   list-assignments
```

Los comandos de mutación aceptan JSON estricto (`DisallowUnknownFields`) y rechazan múltiples valores top-level, igual que `orgctl memory`.

## Tests incluidos

Unitarios (`internal/skillregistry`, ya presentes antes de este trabajo + agregados):

- lifecycle exhaustivo default-deny (`transitions_test` vía `service_test.go`);
- origen GitHub debe estar pinneado; import legacy requiere bandera explícita;
- capability review e instruction safety no se pueden saltear;
- hash de manifest canonicaliza el orden de capabilities;
- `internal/skillregistry/authz`: ruteo de capability por tipo de transición, fail-closed en deny/approval-required, mismatch de organización;
- `internal/skillregistry/manager_test.go`: idempotencia de propuesta (autoriza en cada intento), conflicto de revisión obsoleta, flujo completo draft→active→assign→revoke, propagación de denegación de autorización.

PostgreSQL integration (`internal/skillregistry/postgres`):

- migration tip `000016`;
- alta de skill idempotente;
- round trip completo de lifecycle con evidencia persistida y visible tras `GetVersion`;
- conflicto de revisión obsoleta contra la DB real;
- unicidad de asignación activa por rol+skill;
- inmutabilidad de contenido/eventos/idempotencia;
- rechazo de una transición de lifecycle saltada escrita directamente contra la tabla.

Fitness:

```bash
make test-skillregistry-fitness
make test-skillregistry-integration
```

`make verify` incluye skill registry fitness y `make verify-all` incluye skill registry integration.

## Fuera de alcance

- **Reemplazar la fuente de skills que hoy consume el Context Engine.** `internal/contextengine/canonical.SkillProvider` (YAML `capability-matrix.yaml`, sección `imported_skills`) sigue siendo la fuente activa que usa `internal/contextengine/bootstrap`. Rama 19 no la reemplaza ni la modifica: introduce el registro durable como sistema de gobierno independiente, análogo a cómo Rama 17 (Shadow Verifier) corrió en modo "Fase 1" antes de gatear producción. Cablear `internal/skillregistry` como fuente real del Context Engine (reemplazando `canonical.SkillProvider`) es una decisión de arquitectura deliberada para una rama posterior, no algo que deba decidirse implícitamente aquí.
- generación automática o monitoreo activo de skills externas en GitHub;
- búsqueda/matching semántico de skills candidatas;
- nuevas capabilities en `capability-matrix.yaml` (se reutilizan `organization.propose_skill` y `organization.activate_skill` existentes);
- memoria clínica, chain-of-thought o scratchpad;
- DGM/self-modifying code.
