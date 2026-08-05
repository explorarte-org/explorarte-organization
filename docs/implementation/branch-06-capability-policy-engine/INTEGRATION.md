# Branch 06 — Durable capability policy engine

## Scope

This branch adds deterministic capability authorization to the modular Go kernel. The module is implemented under `internal/authorization`; it does not call an LLM and does not accept grants from tasks, prompts, memories, RAG content, agents, or skills. The canonical capability matrix remains read-only and unchanged.

The engine returns one policy effect:

- `allow`
- `deny`
- `approval_required`

Operational failures remain Go errors and are never converted into `deny` decisions.

## Architecture

The module has three boundaries:

1. `Authorizer` loads and validates `capability-matrix.yaml`, verifies the active registry revision and evaluates static policy.
2. `Service` orchestrates durable approval requests, decisions, single-use consumption, cancellation, expiration, and exact scope revalidation.
3. `authorization/postgres.Store` owns transactional persistence and reuses the platform `audit_events` and `outbox_events` tables.

`internal/authorization` may depend on the organization registry and platform abstractions. It does not import staging, tasks, application bootstrap, commands, or cells.

## Policy precedence

Evaluation is deterministic and default deny:

1. Validate the request.
2. Validate organization identity and active state.
3. Validate the active organization revision.
4. Compare the semantic hash of `capability-matrix.yaml`.
5. Resolve the capability.
6. Resolve the actor role.
7. Reject disabled or retired roles.
8. Reject non-executable roles, except the human owner role.
9. Resolve the authority class.
10. Apply global hard denies.
11. Apply authority-class hard denies.
12. Resolve the static grant, including the owner wildcard.
13. Resolve the capability `approval` field.
14. Validate and consume a durable approval when supplied.
15. Return the structured effect and reason code.

Hard denies always win, including against the owner wildcard and an existing owner decision. `risk: high` alone does not require approval. Approval is required only when the canonical capability declares a non-empty `approval` mode.

## Approval modes

The supported canonical modes are:

- `owner`: only `organizations.owner_role_id` may decide.
- `policy_or_human`: this branch implements only the human-owner path.
- `owner_or_cell_policy`: this branch implements only the owner path.

No automatic or cell evaluator is simulated. A positive owner decision may grant a single scoped action even when the requester's authority class lacks the static grant. It cannot override a hard deny and does not create a permanent grant.

## Durable schema

Migration `000005_create_capability_policy_engine` creates:

- `authorization_requests`
- `authorization_decisions`
- `authorization_uses`

Requests bind the organization, active revision, semantic matrix hash, requester role, capability, resource type, resource ID, action digest, approval mode, expiration, and idempotency material. Decisions are append-only through the service and exactly one decision is permitted per request. Uses are unique by `request_id`, enforcing single consumption.

The migration does not create a new audit table or outbox. Its down migration drops only the three authorization tables, in child-to-parent order.

## State machine

Permitted transitions:

- `pending -> approved`
- `pending -> rejected`
- `pending -> cancelled`
- `pending -> expired`
- `approved -> consumed`
- `approved -> cancelled`
- `approved -> expired`

Terminal states are `rejected`, `cancelled`, `expired`, and `consumed`. Exact retries of a successful consume return the existing `authorization_uses` row; a second use is never inserted.

Every mutating transition locks the request with `SELECT ... FOR UPDATE`, validates the current state, writes the decision or use, updates the request, appends an audit event, appends an outbox event, and commits in one PostgreSQL transaction.

## Idempotency and scope

`RequestApproval` calculates a SHA-256 `request_hash` from a length-prefixed deterministic representation of:

- organization and revision;
- semantic matrix hash;
- requester role;
- capability and approval mode;
- resource type and resource ID;
- canonical action digest;
- normalized TTL;
- normalized reason.

The unique key is `(organization_id, idempotency_key)`. Reusing a key with the same hash returns the existing request. Reusing it with a different hash returns `ErrIdempotencyConflict`.

`DigestAction` hashes caller-provided canonical bytes. It does not serialize unordered Go maps.

Before returning authorization success, consumption revalidates the current organization revision, semantic matrix hash, role state, authority hard denies, capability approval mode, requester identity, resource scope, and action digest. The approval is persisted as consumed before `allow` is returned.

## Audit and outbox

The aggregate type is `authorization_request`; the aggregate ID is the decimal request ID. Events include:

- `authorization.request_created`
- `authorization.request_reused`
- `authorization.decision_approved`
- `authorization.decision_rejected`
- `authorization.request_cancelled`
- `authorization.request_expired`
- `authorization.approval_consumed`
- `authorization.scope_mismatch`
- `authorization.policy_drift_denied`

Outbox payloads contain only the schema version, request ID, organization, capability, effect or status, stable reason code, resource identifiers, and actor role when applicable. Free-form reasons, decision references, secrets, and arbitrary payloads are excluded. Audit payloads contain the constrained authorization scope but no secrets or approval tokens.

## Configuration

The configuration block is always active and has no bypass flag:

- `ORG_AUTHORIZATION_DEFAULT_TTL` — default `30m`
- `ORG_AUTHORIZATION_MAX_TTL` — default `24h`
- `ORG_AUTHORIZATION_COMMAND_TIMEOUT` — default `30s`
- `ORG_AUTHORIZATION_EXPIRE_BATCH_SIZE` — default `100`, valid range `1..1000`

The shared task outbox maximum-attempt setting is reused.

## CLI

`orgctl authorization` provides:

- `evaluate`
- `request`
- `get`
- `list`
- `decide`
- `consume`
- `cancel`
- `expire`

Exit code `6` means policy denial and exit code `7` means approval is required. Existing exit codes `1..5` retain their prior meaning. `--json` on `evaluate` returns the full structured `Evaluation`.

## Staging compatibility

The legacy interface remains:

```go
type CapabilityAuthorizer interface {
    Authorize(context.Context, string, int64, string, string) error
}
```

Staging constructs only the static authorizer and translates authorization errors at its own boundary. Authorization does not import staging. The existing staging capabilities (`code.stage_write`, `code.run_tests`, `code.commit`, and `task.review`) have no approval field, so their behavior remains grant-or-deny. Capability authorization and staging promotion approval remain independent and cumulative controls.

## Tests and fitness checks

Unit tests cover default deny, grants, owner wildcard, both hard-deny levels, unknown capability and authority class, role states, revision and matrix drift, approval modes, high risk without approval, exact scope, TTL validation, deterministic hashes, state transitions, operational-error separation, and legacy compatibility.

PostgreSQL 17 integration tests cover request creation, idempotency conflict, owner-only decisions, rejection, consumption, concurrent consumption, exact retry, expiration, cancellation, revision drift, matrix drift, role retirement, hard-deny rejection, audit/outbox events, transactional rollback, and the down migration.

Commands:

```text
make test-authorization-fitness
make test-authorization-integration
make verify
make verify-all
```

The fitness check requires `rg`, rejects forbidden imports, verifies the durable schema and foreign keys, checks hard-deny precedence, requires audit/outbox transition evidence, confirms the legacy interface, requires concurrency and policy-drift tests, and fails if `docs/canonical` changed from the Branch 06 base.

## Limits

This branch does not add deployment, UI, external authentication, memory or skill management, cell policy evaluation, automatic approval policy, or authorization by an LLM. It does not store approval tokens. External authentication must eventually bind a human identity to the canonical owner role before exposing decision commands outside trusted operations.

## Rollback and safe operation

Before rollback, stop authorization writers and ensure no in-flight request transaction remains. Run the migration down only in an explicitly controlled maintenance operation. The down migration preserves `audit_events` and `outbox_events`; those records remain as historical evidence even after the authorization tables are removed.

Operators should run expiration periodically, monitor pending/approved requests approaching expiry, consume approvals only through the service, and never write decisions or uses directly with SQL.
