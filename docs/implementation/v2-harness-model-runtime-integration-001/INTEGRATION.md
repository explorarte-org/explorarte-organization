# V2 Harness / Model Runtime Integration 001

## Identity

- Canonical base: `28be984653bf8af29677072ba80b588f53f8b449`
- Branch: `v2/harness-model-runtime-integration-001`
- Functional implementation commits:
  - `6a8ebb2` — initial Harness to Model Runtime adapter
  - `ddb8f56` — append-only correction for durable idempotent recovery
- Predecessors: V2 Workflow Runtime, Execution/Cognition Harness Core, and Model Input Envelope
- Production mutation: none
- Live provider calls: zero
- Database access: PostgreSQL 17 disposable integration environment only, protected by `testdbguard`

## Scope

This slice connects one authorized Harness turn to the existing Model Runtime:

```text
Workflow-bound RunIdentity
        |
        v
Execution Harness pure projection
        |
        v
executionharness/modelruntimeadapter
        |
        +--> InvocationService.Create
        |       assignment, task/attempt/lease, context, egress and
        |       immutable ModelInputEnvelope admission
        |
        +--> DispatchService.Dispatch
                technical execution identity, capability authorization,
                egress, cost/budget gate, provider adapter, result and usage
```

The adapter is the only new composition boundary. It has no store, SQL,
provider registry, provider adapter, HTTP client, pricing table, wallet, RAG,
Memory OS, or workflow-state dependency.

`bootstrap.Runtime.NewHarnessModelExecutor` wires the adapter to the exact
`InvocationService` and `DispatchService` created by productive Model Runtime
bootstrap. It does not expose lower-level dependencies to the Harness.

## Contracts

### Workflow binding

Each Model Runtime invocation preserves the Harness `OrganizationID`, `TaskID`,
`AttemptID`, `RoleID`, `CorrelationID`, and `CausationID`. The Harness execution
principal and immutable Run identity remain inside the canonical Harness
projection digest. Harness authority is rechecked before `ModelExecutor.Invoke`;
Model Runtime independently rechecks the live task/attempt/lease and its own
pinned technical execution principal before provider dispatch. These are
distinct identities and are not conflated.

The `InitialContext.ID` is required to be the decimal ID of the durable
`ContextSnapshot`. Its content must still match the snapshot's rendered hash at
Model Runtime admission.

### Model input

The adapter accepts only a self-consistent `executionharness.request.v1`:

- SHA-256 must match `CanonicalDigest`;
- canonical bytes must decode without unknown or trailing fields;
- call identity, embedded identity, prefix, visible history, and continuation
  must agree exactly;
- the stable prefix must remain canonical;
- tool results must be canonical JSON.

It then produces one `modelruntime.input.v1` envelope per Harness turn:

- exact ContextSnapshot render as the first stable-prefix user message;
- stable tool definitions;
- append-only assistant/tool visible history;
- opaque provider continuation reference, unchanged;
- Harness canonical projection digest.

Harness policy is control-plane state, not provider text. It is nevertheless
bound by the full canonical projection digest and therefore cannot drift
without changing the invocation identity.

### Routing and authority

`RunPolicy.ModelPolicyRef` never selects a provider. Model Runtime continues to
resolve the canonical role binding and dispatcher assignment. Model output,
tool output, continuation artifacts, and reasoning telemetry never grant a
model, tool, role, principal, or egress capability.

### Results and telemetry

Persisted Model Runtime text and tool intents map back to the provider-neutral
Harness result. Tool requests without stable provider call IDs fail closed.
Input, output, and reported prompt-cache-hit tokens are preserved as telemetry;
the Harness does not price them.

Current Model Runtime deliberately discards hidden provider reasoning. The
adapter therefore returns empty reasoning telemetry rather than fabricating a
`provider_exposed_reasoning_trace`.

### Idempotent recovery

The invocation idempotency key is `execution-harness:<canonical digest>`.
Before creation, the adapter performs a targeted durable lookup. A match is
accepted only when workflow binding, context ID, envelope schema, envelope
digest, and Harness projection digest all agree.

- `requested`: the existing invocation may enter `DispatchService` once;
- `succeeded`: invocation, result, and usage are reconstructed from durable
  rows and the provider is not called again;
- any other state: fail closed; no implicit retry or redispatch.

A concurrent create conflict is resolved through the same durable lookup. The
global Model Runtime request hash remains unchanged and continues to bind its
deadline and all existing security pins.

## Persistence

No migration and no new persistence were added. This slice reuses:

- `model_invocations`;
- immutable `model_invocation_inputs`;
- `model_invocation_results`;
- `model_invocation_usage`;
- existing dispatch, identity, egress, cost, audit, and outbox records.

`InvocationService.Outcome` and the PostgreSQL outcome reader expose the
already-atomic successful result for recovery. They do not add an update or
rewrite path.

## Security invariants

- Harness never imports or calls a concrete provider adapter.
- Admission still rejects credentials before `model_invocation_inputs` is
  persisted; Dispatch retains the independent corrupt-record egress defense.
- Assignment, technical principal, execution identity signature, egress,
  provider availability, and cost/budget checks remain inside Model Runtime.
- Returned durable invocation/result scope is checked before becoming a
  Harness result.
- Existing or returned binding drift causes zero new dispatches.
- No provider continuation value can grant authority.
- No clinical vocabulary heuristic was introduced.

## Test coverage

Unit and integration tests cover:

- model -> tool -> model with one Model Runtime invocation per turn;
- direct completion;
- workflow/correlation/causation binding;
- stable prefix and tool-definition identity across turns;
- canonical projection and detached-field drift;
- invalid ContextSnapshot reference;
- invocation admission failure with zero dispatch;
- returned durable binding drift with zero dispatch;
- deterministic tool-denial projection;
- missing provider tool-call ID rejection;
- usage/cache telemetry mapping without reasoning fabrication;
- successful durable recovery with zero redispatch;
- nonterminal or mismatched durable recovery denial;
- PostgreSQL 17 durable envelope, result, usage, provider request, execution
  identity assertion, and dispatcher-assignment-use evidence.

## Chronological implementation evidence

History is intentionally not rewritten.

1. `go test ./internal/executionharness/... ./internal/modelruntime/...` inside
   the restricted local sandbox failed because `httptest` could not bind a
   loopback socket (`socket: operation not permitted`). This was environmental,
   not a test assertion.
2. The same focused command outside that socket restriction passed all Harness,
   Model Runtime, bootstrap, and provider-adapter packages.
3. `go test ./...` passed.
4. `go vet ./...` passed.
5. `go test -race ./internal/executionharness/... ./internal/modelruntime ./internal/modelruntime/bootstrap ./internal/modelruntime/postgres` passed.
6. `scripts/check-model-runtime-fitness.sh` failed on the branch because its
   provider allow-list predates the existing MiMo adapter. The exact same
   failure was reproduced from a clean detached worktree at canonical base
   `28be984...`; changed files causing this failure: zero.
7. PostgreSQL disposable run at `6a8ebb2` passed all preconditions but failed
   the new idempotent recovery assertion: recomputing `Deadline` changed the
   global request hash. Evidence manifest SHA-256:
   `3652ba136dfd9209bfb1a596a57d0578a992faae43020d191f7f3490f448cbe9`.
8. Commit `ddb8f56` added targeted durable idempotency lookup and explicit
   state-dependent recovery without weakening the request hash.
9. PostgreSQL 17 disposable rerun at exact detached `ddb8f56` passed:
   preconditions 4/4; suites 1 passed, 0 failed; accounting 31/31; final status
   `COMPLETE_GREEN`. Evidence manifest SHA-256:
   `bafea7c4de4202c193d59150ee1e026334f486c35e473720ca4a5c19b67700f4`.
10. Human review identified that the initial PostgreSQL Harness composition
    used an allow-all authority and therefore did not prove productive task
    lease/principal reauthorization. This finding was preserved; no prior
    result was rewritten.
11. The closure adds `CanonicalPrincipalReader`, backed by the canonical
    `modeldispatch.PrincipalStore`, and composes it with the real task lease
    verifier through `modelruntime/bootstrap.NewHarnessAuthority`.
12. The PostgreSQL Harness flow now uses the real `tasks/postgres` lease
    verifier and canonical execution-principal store. It proves two model
    invocations with one tool call and distinct invocation references.
13. PostgreSQL mutation checks revoke the principal or revoke the lease after
    turn one. In both cases turn two is denied and no additional provider
    dispatch occurs; the original turn-one evidence remains append-only.

## Known gaps

- Productive Execution Harness history persistence remains pending; this slice
  does not claim that its in-memory history store is production durable.
- No daemon/CLI consumer starts Harness runs yet. The productive composition
  seam is available through Model Runtime bootstrap, but consumer migration is
  a later slice. The current integration proof uses the disposable PostgreSQL
  harness and concrete task/model-runtime services; it does not claim a
  production daemon has been migrated.
- Provider-exposed reasoning telemetry is unavailable through current Model
  Runtime normalization and is not invented here.
- Provider continuation remains opaque and currently fails closed for adapters
  that do not support it.
- Context Assembly V2, Memory OS, compaction, programmatic tool execution, and
  Workflow completion bridging remain out of scope.
- The stale model-runtime fitness allow-list should be updated in a separate,
  isolated maintenance change; it is not caused by this branch.

## Rejected alternatives

### Call provider adapters from the Harness

Rejected because it bypasses Model Runtime assignment, egress, execution
identity, provider dispatch, usage, and economic gates.

### Convert each turn into a ContextSnapshot

Rejected because a turn projection is invocation input, not canonical task
context. It would duplicate execution history inside Context Assembly.

### Remove Deadline from the Model Runtime request hash

Rejected after the first PostgreSQL failure. Global request identity was not
weakened to make Harness retry easier; the adapter instead finds and validates
the already-durable invocation.

### Redispatch any reused invocation

Rejected because an invocation may already have crossed the provider boundary.
Only `requested` can dispatch; `succeeded` is reconstructed; other states fail
closed pending an explicit recovery protocol.

### Persist Harness history in Model Runtime

Rejected because `model_invocation_inputs` records what one invocation saw,
not the canonical multi-turn Harness event history.

### Recalculate pricing in the adapter

Rejected because trajectory limits and financial accounting are separate;
Model Runtime's existing cost/budget gate remains authoritative.
