# Branch 07 — Deterministic durable context engine

## Scope

Branch 07 adds a deterministic context assembly module under `internal/contextengine`, PostgreSQL snapshot persistence, strict Markdown/YAML source validation, portable rendering, drift validation, auditing/outbox integration, and `orgctl context` operations. It does not execute models, authorize capabilities, approve memories or skills, search RAG, mutate projects/tasks, or change any document under `docs/canonical`.

The module consumes the organization registry and narrow provider interfaces. Future Branch 08 model adapters consume the portable render. Future Branch 11 memory and Branch 12 RAG implementations replace the explicit unavailable providers without changing assembly semantics.

## Architectural boundaries

`internal/contextengine` may import the organization registry, configuration and platform PostgreSQL abstractions. It must not import staging, tasks, model adapters, commands, the application bootstrap, or legacy Python stores. Project, task, memory, skill and RAG data cross the boundary only through interfaces in `interfaces.go`.

The pure assembler performs no I/O, database writes, event publication, authorization, lifecycle mutation, model calls or tool execution. PostgreSQL is the durable source of snapshot state. `context_segments` is append-only.

## Sources and allowlist

The canonical provider uses an explicit allowlist rather than embedding every file in `docs/canonical`:

- `cell-boundaries.yaml` as immutable safety;
- `decisions-required.yaml` as owner decisions;
- `organization.yaml`, `role-catalog.yaml`, `leader-worker-map.yaml`, `model-routing.yaml`, `capability-matrix.yaml`, `instruction-precedence.yaml`, `memory-policy.yaml`, `reasoning-assurance.yaml`, and `architecture-characteristics.yaml` as canonical policy.

`source-manifest.yaml` is not automatically rendered. Canonical documents remain authoritative for organization structure, roles, runtime kind, authority class, reporting, model routing, capabilities and skill lifecycle. Text in AGENT, PERFIL, SKILL, memory, project, task or RAG cannot replace those fields.

## Precedence versus rendering

Authority and rendering are independent metadata.

Render order is exactly:

1. immutable safety;
2. owner decisions;
3. canonical registry and policies;
4. organization AGENT;
5. department AGENT;
6. role profile;
7. approved memory;
8. matched approved skills;
9. project context;
10. task context;
11. RAG evidence.

Authority priority is:

- 0 immutable safety;
- 1 owner decisions;
- 2 canonical registry and policy;
- 3 organization and department AGENT;
- 4 role profile and approved skill;
- 5 project and task;
- 6 memory and RAG.

Consequently memory renders before skills but remains lower authority. No position in the rendered document grants authority. Only priority 1 or 2 records may carry `may_grant_capabilities=true`, and the context engine never evaluates those grants.

## Instruction, trust and data classes

Each segment persists its authority tier/priority, instruction class, trust class and data class independently.

Memory and RAG are always `instruction_class=data`, `trust_class=untrusted`, priority 6 and incapable of granting capabilities. Project and task are scoped instructions, not policy. Approved skills are procedures but require canonical `active` lifecycle and explicit role assignment.

Only `public`, `organizational` and `sanitized` data can enter a snapshot. `secret`, `clinical` and unknown dynamic classifications are rejected before persistence. The module does not claim to detect natural-language contradictions or reliably discover secrets from text; providers must classify dynamic data and the module enforces the classification boundary.

## PERFIL.md validation

Profiles require strict YAML frontmatter with `departamento`, `rol`, `dominio_memoria` and `agente_base`. Identity and memory domain must match the canonical role. Legacy fields such as `lider`, `modelo`, `reporta_a` or `authority_class` do not modify canonical authority, leadership, model routing, reports-to or capabilities.

The validated Markdown body is rendered as `role_profile`. The source path always comes from the registry, never from a request.

## AGENT.md validation

Organization and department AGENT paths come from configuration/registry and are loaded separately. AGENT documents cannot grant capabilities, change model routing, redefine owner or authority class, include arbitrary files, or escape the configured document root.

## SKILL.md schema and lifecycle

The accepted frontmatter is `name`, `description`, `departamento`, `rol`, `dominio_memoria`, `verificador`, `origen`, `protocolo_base`, with legacy `agente_base` optional. Name, owner role, department and memory domain must match the canonical skill record. Known protocols are allowlisted; origins are `interno` or `github:<owner>/<repo>`. Auxiliary references are validated relative paths inside the skill directory and are not loaded automatically.

Lifecycle is not read from SKILL.md. Canonical states are `draft`, `human_approved`, `candidate`, `active`, `suspended`, and `retired`. Only `active` plus explicit role assignment is executable context. The four currently imported skills have status `imported_draft_requires_owner_approval`, mapped conservatively to `draft`; they are therefore excluded. Branch 07 neither approves nor activates them.

## Filesystem root and path security

`ORG_CONTEXT_SOURCE_ROOT` is an explicit absolute root. The loader rejects empty mandatory paths, absolute paths, traversal, directories, FIFOs/special files, invalid UTF-8, NUL bytes and oversized files. It cleans relative paths, resolves symlinks, verifies containment with `filepath.Rel`, opens a descriptor and checks the resolved descriptor path where supported. Internal symlinks are allowed; escapes to `/etc` or another repository are rejected.

The Markdown parser normalizes CRLF to LF, separates frontmatter/body, rejects duplicate YAML keys, aliases, anchors, merge keys and unexpected YAML tags, and bounds node count/depth. It executes no templates, HTML or includes.

## Hashing and determinism

All hashes are lowercase SHA-256. Canonical YAML is parsed strictly and serialized recursively with sorted mapping keys while retaining sequence order. Build requests sort requested skill IDs and source descriptors before hashing. Maps are never serialized directly for security-sensitive hashes.

- `precedence_hash` is the semantic hash of `instruction-precedence.yaml`;
- `canonical_bundle_hash` length-prefixes each allowlisted logical name, version and semantic hash;
- `request_hash` includes request identity, revision, purpose/scopes, sorted skills, policy/bundle hashes, resolved source descriptors and configured limits;
- `rendered_hash` hashes the exact portable render.

Changing a revision, source hash/version/classification/order, precedence or budget changes the relevant digest.

## Portable render

The renderer emits deterministic JSON with a fixed schema and ordered segment slice. It includes snapshot identity, organization/revision/actor, hashes, segment metadata, provenance, byte counts and content. It does not emit provider-specific OpenAI, Anthropic, Qwen or DeepSeek roles. Branch 08 must map the portable authority/trust classes to provider payloads without elevating memory, RAG, project or task.

Delimiter escaping is not treated as prompt-injection protection. Protection comes from explicit authority, trust, instruction/data classes, provenance and downstream model-adapter separation.

## Budgets and omissions

Limits are byte/segment based and model-independent. Mandatory sources—policies, AGENTs, profile, active skills, project and task—are never partially truncated. Exceeding their segment or total budget rejects the build.

Only approved memory and RAG evidence are optional. They may be omitted whole, never cut. Omission order is deterministic by declared relevance, provider priority and source reference. Omitted segments remain in the manifest with `included=false`, a reason, zero byte count, null content and the known source hash. Snapshot counters expose all omissions.

## Snapshot persistence and migration 000006

Migration `000006_create_context_engine` creates only `context_snapshots` and `context_segments`. It reuses organization/revision/role foreign keys, `audit_events` and `outbox_events`; it creates no memory, skill or RAG lifecycle tables.

Snapshots are `ready` or terminal `invalidated`. Segments are append-only via a database trigger. Composite foreign keys keep tenant organization IDs aligned. SQL constraints enforce hash shape, authority mappings, permitted data classes, included/omitted coherence, content cap and capability metadata.

## Idempotency and concurrency

`UNIQUE(organization_id,idempotency_key)` provides durable idempotency. The same key plus request hash returns the existing intact snapshot and records a reuse audit without duplicating segments or outbox messages. A different request hash returns `ErrIdempotencyConflict`. Concurrent builders allocate candidate IDs but exactly one snapshot wins the unique key; gaps in the identity sequence are acceptable and have no semantic meaning.

Creation writes snapshot, segments, audit and one outbox event in one transaction. Failure in audit/outbox rolls the entire operation back. A ready snapshot is immutable; source changes require another idempotency key and snapshot.

## Invalidation

Only the snapshot actor or organization owner may request invalidation. The only transition is `ready -> invalidated`; invalidated is terminal. Exact retries by the same actor and reason are idempotent. Invalidation updates only status, timestamp, reason and version, and appends audit/outbox in the same transaction. Rendering invalidated snapshots fails by default.

## Drift validation

`ValidateSnapshot` rechecks status, organization revision, role state/executability, precedence/bundle hashes, profile and AGENT hashes, active/assigned skill state and source hash, dynamic provider versions, stored segment classes and reconstructed rendered hash. It returns structured findings and never mutates/invalidate automatically.

Supported findings include revision, precedence, canonical bundle, profile, AGENT, skill state/source, memory/project/task/RAG version and rendered hash drift. Natural-language semantic contradictions remain outside Branch 07.

## Audit and outbox

Aggregate type is `context_snapshot` and aggregate ID is the decimal snapshot ID. Creation/reuse/invalidation use existing audit/outbox tables. Outbox payloads contain only schema version, IDs, status, purpose, hashes, counts and reason code. They never contain source content, memory, RAG evidence, profiles, full skills, absolute paths, secrets or arbitrary reasons. Audit payloads preserve safe hashes/references/classification information, not complete source bodies.

## Configuration

- `ORG_CONTEXT_SOURCE_ROOT` (default `/opt/explorarte/organization`);
- `ORG_CONTEXT_COMMAND_TIMEOUT` (30s);
- `ORG_CONTEXT_MAX_TOTAL_BYTES` (524288);
- `ORG_CONTEXT_MAX_SEGMENT_BYTES` (65536);
- `ORG_CONTEXT_MAX_SEGMENTS` (128);
- `ORG_CONTEXT_MAX_SKILLS` (16);
- `ORG_CONTEXT_MAX_MEMORY_SEGMENTS` (32);
- `ORG_CONTEXT_MAX_RAG_SEGMENTS` (20).

`ORG_CANONICAL_DIR` remains the sole canonical directory setting. Configuration validates absolute source root after resolution and bounded positive limits. There is no enabled flag.

## CLI

`orgctl context` provides:

- `validate-source` for a relative profile/agent/skill source;
- `build` for durable snapshots;
- `get` and `list` for metadata;
- `render` as the explicit full-content operation;
- `validate` for drift;
- `invalidate` for terminal invalidation.

JSON build output redacts content; `get` includes content only with `--include-segments`; `render` returns the portable context. Exit code 8 is validation/policy rejection and 9 is stale/invalidated. The CLI cannot inject raw memory/RAG, activate skills, alter priorities, grant capabilities or label data sanitized.

## Provider integration

Branch 07 bootstraps no-op owner constraints and explicit unavailable memory, project, task and RAG providers. Basic organization/department/profile snapshots work. Explicit project/task refs fail while providers are unavailable rather than inventing content. Branch 11 and 12 providers must return classified, versioned, hashed records and implement validation without importing their stores into contextengine.

## Rollback and safe operation

Before rollback, stop context builders and preserve audit/outbox records as required by retention policy. Run the 000006 down migration only in a controlled environment after confirming no dependent code reads snapshots. It removes `context_segments` first, then `context_snapshots`; it does not remove shared audit, outbox or authorization tables.

For production, keep the source root read-only, synchronize the canonical registry before building, use PostgreSQL 17 migrations, monitor context rejection/stale events, and treat rendered output as organizationally sensitive even though secret/clinical classes are rejected.

## Known limitations

- No natural-language contradiction resolver or LLM safety classifier.
- No tokenizer/model-specific budget.
- No memory approval, skill activation, semantic RAG retrieval or model execution.
- Dynamic providers must classify and sanitize content correctly.
- Filesystem checks reduce path/symlink races; complete cross-platform TOCTOU elimination would require platform-specific `openat2` handling.

## Rejected forbidden-source attempts

A source rejected as `secret`, `clinical`, forbidden, path-escaping, symlink-escaping, or capability-granting from an unsafe tier never creates a `context_snapshots` row. PostgreSQL allocates an attempt identifier from the same snapshot sequence only for audit correlation and emits one idempotent `context.forbidden_source_rejected` audit/outbox pair per organization, idempotency key, and reason. The payload contains bounded metadata and hashes, never source content. This preserves the no-partial-snapshot invariant while satisfying rejection traceability.
