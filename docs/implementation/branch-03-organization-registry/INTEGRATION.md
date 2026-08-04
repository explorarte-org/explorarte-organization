# Rama 03 — Registro organizacional canónico

## Identidad de integración

- **Commit base obligatorio:** `4c1153d5e78a0eea4df9c96e5872de2ad79e3c80`
- **Rama:** `feat/03-organization-registry`
- **Commit sugerido:** `feat(registry): add canonical organization registry`
- **Módulo Go:** `internal/organization/registry`
- **Migración:** `000002_create_organization_registry`

La fuente declarativa continúa en Git bajo `docs/canonical`. PostgreSQL contiene una
materialización consultable, versionada y auditable. Esta rama no convierte roles en
agentes, procesos, pods ni goroutines.

## Documentos canónicos leídos

El hash y la materialización usan exactamente estos ocho archivos, en este orden fijo:

1. `organization.yaml`
2. `role-catalog.yaml`
3. `leader-worker-map.yaml`
4. `model-routing.yaml`
5. `capability-matrix.yaml`
6. `instruction-precedence.yaml`
7. `decisions-required.yaml`
8. `source-manifest.yaml`

`source-manifest.yaml` se trata como procedencia histórica. Sus rutas se normalizan a
referencias estables y nunca se abren ni se ejecutan.

## Hash canónico

1. Se rechazan documentos vacíos, múltiples documentos YAML, campos desconocidos,
   anchors, aliases, merge keys, tags no seguros, profundidad excesiva y más de
   100.000 nodos.
2. Se normalizan CRLF y CR a LF.
3. Se ordenan colecciones semánticamente no ordenadas por identificador; se conserva
   el orden de `prompt_assembly_order`, porque sí tiene semántica.
4. `source-manifest.generated_at` no participa en el hash.
5. Cada documento tipado normalizado se serializa como JSON y recibe SHA-256.
6. El hash agregado es SHA-256 de la concatenación ordenada:
   `nombre + NUL + hash_semántico + LF`.

No participan hostname, arquitectura, ruta absoluta del checkout, timestamps de
sincronización ni orden de mapas Go.

## Tablas

- `organization_registry_revisions`
- `organization_registry_revision_documents`
- `organizations`
- `organizational_units`
- `organization_roles`
- `organization_reporting_lines`

Los identificadores canónicos son `TEXT`: son claves de dominio legibles y estables
definidas en Git. Las revisiones usan `BIGINT GENERATED ALWAYS AS IDENTITY`, porque
son identidades internas monotónicas sin necesidad de una extensión UUID.

Las entidades desaparecidas se retiran lógicamente con `retired_at`; no se eliminan.
Las relaciones `reports_to` se guardan por revisión para conservar procedencia.

## Interfaces proporcionadas

```go
type Reader interface {
    GetOrganization(context.Context, string) (Organization, error)
    ListUnits(context.Context, string) ([]Unit, error)
    GetUnit(context.Context, string, string) (Unit, error)
    GetRole(context.Context, string, string) (Role, error)
    ListRoles(context.Context, string, RoleFilter) ([]Role, error)
    GetLeader(context.Context, string, string) (Role, error)
    ListWorkers(context.Context, string, string) ([]Role, error)
    GetCurrentRevision(context.Context, string) (*Revision, error)
    LoadCurrentSnapshot(context.Context, string) (*Snapshot, error)
}
```

`Service` expone las variantes acotadas a la organización configurada, además de:

- `ValidateCanonical`
- `CompareCanonical`
- `SynchronizeCanonical`

`SynchronizeCanonical(..., false)` es dry-run. La aplicación real recalcula el diff
dentro de la misma transacción serializable y advisory lock que persiste la revisión.

## CLI

```text
orgctl registry validate [--json]
orgctl registry diff [--json]
orgctl registry sync [--apply] [--json]
orgctl registry status [--json]
orgctl registry list-units [--json]
orgctl registry list-roles [--unit ID] [--enabled] [--json]
orgctl registry get-role UNIT/ROLE [--json]
orgctl registry get-leader UNIT [--json]
```

Códigos de salida:

- `0`: válido y sincronizado, aplicado o no-op;
- `2`: uso incorrecto;
- `3`: válido con drift o migraciones pendientes;
- `4`: documentos canónicos inválidos;
- `5`: PostgreSQL no disponible o timeout;
- `1`: error interno.

## Configuración

- `ORG_CANONICAL_DIR` — default local `docs/canonical`; imagen:
  `/opt/explorarte/docs/canonical`.
- `ORG_REGISTRY_SYNC_TIMEOUT` — default `30s`.

No existe `ORG_REGISTRY_AUTO_SYNC`. `orgd` no sincroniza el registro al arrancar.

## Invariantes

- siete departamentos operativos exactos;
- `empresa` e `investigacion` son unidades transversales leaderless;
- un líder canónico exacto por departamento operativo;
- 48 roles: 45 `imported_source` y 3 `proposed_profile_required`;
- perfiles propuestos siempre deshabilitados y no ejecutables;
- IDs y referencias normalizados, sin traversal;
- referencias de líder, worker, modelo, autoridad y reporting verificadas;
- grafo `reports_to` sin autorreferencias ni ciclos;
- `branch_0_candidate` se conserva sin promoción implícita;
- la autoridad `executive_observer`, aún abierta en los documentos, permanece
  default-deny mediante warning porque el rol está propuesto y deshabilitado;
- una sincronización con el hash actual es no-op y no escribe auditoría;
- toda sincronización nueva es atómica;
- no se hacen hard deletes.

## Auditoría

Cada revisión aplicada agrega un único evento `organization.registry_synced` a
`audit_events`. El payload contiene hashes, conteos y totales de cambios, nunca los
documentos completos ni datos clínicos.

## Pruebas

- parser estricto y documentos canónicos reales;
- IDs y paths;
- validaciones cruzadas y ciclos;
- hash determinista frente a orden y finales de línea;
- source manifest no dereferenciado;
- dry-run y no-op;
- integración real con PostgreSQL 17;
- migración up/down en base aislada;
- materialización, consultas, revisión, auditoría, diff, retiro lógico y rollback.

```bash
make verify
make test-integration
make verify-all
```

## No implementado

No se implementan tareas, proyectos, mensajería, SKIP LOCKED, leases, outbox,
dead-letter, CEO, observador, modelos, capabilities ejecutables, skills, memoria,
RAG, Prolog, worktrees, células ni endpoints HTTP del registro.

## Contrato para Rama 04

Rama 04 puede usar el registro únicamente como catálogo declarativo:

- consultar unidades, líderes y roles mediante `registry.Reader` o `Service`;
- considerar ejecutable solo un rol con `Enabled && Executable`;
- no inferir capacidades a partir de `AuthorityClass`;
- no ejecutar `ProfilePath`, `DepartmentAgentPath` ni `MemoryPath`;
- no modificar tablas del registro desde el motor de tareas;
- persistir referencias por ID canónico, con FK o validación en la frontera;
- tratar roles retirados o propuestos como no asignables;
- usar la revisión vigente para trazabilidad cuando cree tareas o asignaciones.

`ListWorkers` significa “roles no líderes del catálogo actual”; puede incluir roles
propuestos deshabilitados. La Rama 04 debe filtrar por `Enabled && Executable` antes
de asignar trabajo.
