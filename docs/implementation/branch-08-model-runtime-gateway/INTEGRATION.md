# Branch 08 — Model Runtime Gateway

## Identity

- Branch: `feat/08-model-runtime-gateway`
- Required base: `6d7b71acdeba7a70d552764c1d96f7bf9d907e6d`
- Local commit message: `feat(models): add durable model runtime gateway`
- Migration: `000007_create_model_runtime_gateway`

## Scope delivered

This branch adds a durable, one-shot model control plane. It materializes
`docs/canonical/model-routing.yaml` into immutable PostgreSQL profile versions,
creates durable invocations and dispatch attempts, validates task, role,
capability, context and model bindings, and dispatches only through the isolated
`test.fake` adapter when an explicit canonical test route enables it.

Real providers are materialized with:

```text
adapter_status = unavailable
dispatch_enabled = false
```

No HTTP, CLI provider process, shell, worker, poller, scheduler, provider health,
tool execution, fallback or `orgd` integration is included.

## Public commands

```bash
orgctl model registry validate [--json]
orgctl model registry diff [--json]
orgctl model registry sync [--apply] [--json]
orgctl model registry status [--json]

orgctl model invocation create --file invocation.json [--json]
orgctl model invocation get <id> [--json]
orgctl model invocation list [--limit N] [--json]
orgctl model invocation dispatch <id> [--claimed-by ID] [--json]
orgctl model invocation cancel <id> --actor-role ROLE [--reason TEXT] [--json]
orgctl model invocation reconcile [--batch N] [--json]
```

`CanonicalRequest` is internal and cannot be supplied through CLI JSON.
Invocation creation does not accept messages, rendered context, tools, provider
IDs, provider model IDs, URLs, headers, credentials or arbitrary instructions.

## Configuration

```text
ORG_MODEL_RUNTIME_ENABLED=false
ORG_MODEL_RUNTIME_COMMAND_TIMEOUT=30s
ORG_MODEL_RUNTIME_GLOBAL_CONCURRENCY=4
ORG_MODEL_RUNTIME_MAX_RESPONSE_BYTES=1048576
ORG_MODEL_RUNTIME_MAX_TOOL_INTENTS=8
ORG_MODEL_RUNTIME_CLAIM_TTL=2m
ORG_MODEL_RUNTIME_RECONCILE_BATCH_SIZE=100
```

`ENABLED=false` prevents dispatch and adapter execution. Registry validation,
status inspection and durable read operations remain available.

## Dependencies reused

- Organization registry: current revision, roles and canonical document hash.
- Durable tasks: task, attempt and active execution lease through public APIs.
- Context engine: `Get`, `Validate` and `Render` through its public service.
- Capability policy engine: `task.execute` evaluation scoped to
  `resource_type=model_invocation` and an immutable action digest.
- Existing `audit_events` and `outbox_events` tables.

The branch does not read context-engine tables directly and does not modify the
canonical capability matrix.

## Temporary authorization restriction

Until a canonical infrastructure capability and dispatcher assignment exist:

```text
dispatch_actor_role_id == subject_role_id == task.assigned_role_id
```

`task.execute` authorizes only fake dispatch inside the validated task attempt.
It does not authorize result acceptance, task completion, tool execution,
memory writes, skill activation, policy changes or subject-role impersonation.

## Durability and retry boundary

`send_started_at` is the external-effect boundary.

- Expired claim without `send_started_at`: safe to release to `requested`.
- Expired claim after `send_started_at`: terminal `ambiguous` outcome.
- Response received but result persistence lost: terminal `failed`; never call
  the adapter again.
- Reconciliation never invokes an adapter and never retries a sent request.
- Claim tokens are generated with `crypto/rand`; only SHA-256 is stored.

## Data handling

The runtime accepts context classes `public`, `organizational` and `sanitized`
only for the local fake adapter. `secret` and `clinical` are rejected.

Hidden reasoning is discarded before normalization. It is not persisted,
hashed, logged, audited, published to outbox or returned by CLI. Tool intents
are normalized as inert data and never executed.

## Future blockers before real adapters

1. Introduce a canonical infrastructure capability for model invocation.
2. Define a canonical egress policy for organizational data per provider.
3. Add credential boundaries and provider-specific idempotency guarantees.
4. Add explicit dispatcher assignments instead of the temporary role equality.
5. Define cancellation semantics for each real provider.

## Verification

```bash
make test-model-runtime-fitness
make test-model-runtime-integration
make verify
make build-cross
make verify-all
```

PostgreSQL integration uses PostgreSQL 17 and includes migration up/down/reapply,
canonical synchronization, immutable profile versions, idempotent invocation
creation, claim races, global concurrency, fake one-shot dispatch, response
normalization, audit/outbox leakage checks and safe reconciliation.

## Non-goals

- No push or merge is performed by the installer.
- No canonical document is modified.
- No migration before `000007` is modified.
- No model invocation completes a durable task.
