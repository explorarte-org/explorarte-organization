# V2 Execution / Cognition Harness 001

## Freeze identity

- Canonical base: `f72877ec47eb72d532801ecb5f0a62c515f798cd`
- Branch: `v2/execution-cognition-harness-001`
- Package: `internal/executionharness`
- Production database access: none
- Live provider/model calls: none

The final implementation commit is the branch head reported with the review
handoff. A commit cannot embed its own SHA without changing that SHA; the
canonical base, branch, tree diff, remote head, and handoff bind the artifact.

## Scope and architecture

This slice implements the provider-independent Harness core for executing work
that Workflow Runtime and the durable task domain have already authorized.

```text
Workflow Runtime (what work)
        |
        v
Execution/Cognition Harness (how the authorized agent executes)
        |
        v
ModelExecutor port (authorized Model Runtime boundary)
        |
        v
Model Runtime / provider adapter

Harness <-> typed ToolCatalog / ToolExecutor ports
```

The Harness does not own task status, requirements, completion, coordination,
provider selection, egress, pricing, wallets, context retrieval, RAG, memory,
or provider transport. `ModelExecutor` is deliberately a port: the existing
Model Runtime creates/pins an invocation, validates assignment and execution
principal, evaluates egress, persists pre-send decisions, dispatches through
registered adapters, and records normalized results/usage. None of that logic
is copied here.

## Run contract and immutable identity

A `RunSpec` freezes:

- run, organization, task, attempt, assigned role, execution principal,
  correlation, and causation identities;
- a lease token by digest (the token itself is never put in history);
- initial context ID, version, digest, and exact content;
- the complete, canonicalized visible tool definition set;
- turn/tool limits plus execution-profile, model-policy, and build references.

`RunStarted` carries a digest of those values. Reusing the same RunID with any
changed binding, context, tool definition, or policy fails closed before a
model/tool call. Tool ordering and JSON object-key whitespace normalize to one
logical identity; semantic tool changes do not.

`tasksauthority.Adapter` composes the existing
`tasks.ExecutionLeaseVerifier` with a narrow active-principal reader. It checks
task, attempt, organization, assigned role, holder/principal, active status,
and lease token. The Harness calls this authority before each model invocation
and again before every tool execution, so a mid-run revocation stops the next
side effect. Model output, tool output, reasoning telemetry, context, or memory
can never grant authority.

## Append-only Golden History

`ExecutionHistoryStore` exposes only:

```text
Append(runID, expectedSequence, event)
Read(runID)
```

There is no update, delete, replacement, or history-rewrite operation.
Sequences are monotonically assigned under compare-and-append semantics. The
in-memory validation store deep-copies on append and read, preventing a caller
from mutating stored slices or JSON buffers through aliases.

Model-visible corrections therefore append new model/tool events. They never
rewrite a historical response or result. Operational events such as authority
checks and request preparation are distinguishable from model-visible
assistant/tool messages.

### Persistence assessment

The existing durable surfaces are not semantically sufficient to reconstruct
this multi-turn trajectory:

- task events describe durable work lifecycle, not cognitive turns;
- model invocations/results describe individual authorized provider calls;
- existing tool intents have no Harness `ToolCallID` replay identity and do not
  persist validated tool results as a multi-turn sequence;
- outbox/audit payloads are intentionally minimal and exclude model content.

Therefore `HISTORY_PERSISTENCE = PORT_ONLY_PENDING_PRODUCTION_ADAPTER`.
This slice adds no table and no migration. Choosing a durable representation
is a later explicit persistence decision; the in-memory store is test/core
evidence only and is not presented as production durability.

## Pure deterministic request projection

`Project` accepts only the stable RunSpec and an ordered event slice. It uses no
DB, network, clock, randomness, mutable global, Memory/RAG call, or provider.
It canonicalizes tool schemas/arguments and emits stable JSON bytes plus a
SHA-256 digest. Repeating the same projection is byte-identical.

The stable prefix contains immutable context, tools, and run policy. Later
turns append assistant/tool messages without altering that prefix or prior
visible messages. Provider-exposed reasoning telemetry is deliberately not a
model-visible message. An optional opaque continuation reference is passed
through as an opaque value; it neither grants tools nor becomes agent memory.

This establishes `CANONICAL_REQUEST_REPRODUCIBLE`. It does not claim
`PROVIDER_WIRE_REQUEST_REPRODUCIBLE`: the eventual Model Runtime adapter still
owns provider rendering and transport.

## Model/tool loop and termination

The standard loop is:

```text
authorize -> project -> ModelExecutor
    final -> COMPLETED
    tool requests -> record -> validate -> reauthorize -> execute -> append
                                      -> next model turn
```

Every requested tool must exist in the catalog, appear in the frozen Run tool
set, have a valid non-replayed ToolCallID, fit the tool-call budget, and pass
adapter argument validation before execution. Denied requests have zero tool
side effect and remain evidence.

Termination is typed: `COMPLETED`, `LIMIT_REACHED`, `MODEL_ERROR`,
`TOOL_ERROR`, `AUTHORIZATION_DENIED`, `CANCELLED`, identity drift, or history
error. Limit exhaustion never fabricates an assistant answer. If the last
allowed turn produces and executes a tool result, that result remains in
history and the run returns `LIMIT_REACHED` with no final output.

## Reasoning telemetry

The only supported term is `provider_exposed_reasoning_trace`. Telemetry may
contain an exposure kind, provider-reported summary/plaintext, token counters,
opaque continuation reference, and provenance. It is optional observability
evidence. Authorization, task state, completion, and tool grants never inspect
its content.

## Verification history (append-only)

1. Startup gate: created isolated worktree/branch from
   `f72877ec47eb72d532801ecb5f0a62c515f798cd`; HEAD and merge-base matched and
   the tree was clean. **PASS**.
2. Read architecture delta, Workflow Runtime integration and implementation,
   tasks lease contracts, Model Runtime invocation/dispatch/normalization and
   bootstrap, provider adapters, Model Dispatch principals/assignments, Model
   Egress, Content Policy, persistence code, and model-egress fitness script.
3. Initial Harness compile before tests: `go test
   ./internal/executionharness/...`; packages compiled with no tests. **PASS**.
4. Added functional, mutation, history-aliasing, projection, and task-authority
   tests. `go test ./internal/executionharness/...`: **PASS**.
5. Focused suite inside the restricted sandbox: Harness vet passed; Workflow
   Runtime, Model Runtime core, Content Policy, and tasks passed. Five provider
   adapter tests failed before execution because `httptest` could not bind
   `[::1]:0` (`socket: operation not permitted`). **FAIL (environmental,
   preserved)**.
6. Repeated the same focused suite with permission for offline loopback
   listeners. DeepSeek, Gemini, MiMo, OpenAI-compatible and OpenAI Responses
   adapter tests, plus all other focused packages: **PASS**. No external
   provider endpoint was called.
7. The historical `scripts/check-model-egress-fitness.sh` was inspected and
   invoked. Its fixed comparison base predates Instrument V4 and it reports
   the already-canonical
   `docs/canonical/instrument-v4-controller-binding-001.provenance.yaml` as an
   unauthorized change even though this branch changes no canonical or Model
   Egress file. **BASELINE/PINNING BLOCKED (preserved, not attributed to this
   slice)**. The script's relevant security invariants were checked directly.
8. `go test -race ./internal/executionharness/...`, `go test ./...`, `go vet
   ./...`, gofmt, staged-diff checks, and direct static gates for forbidden
   imports, SQL, migrations/deployment, history mutation APIs, shell runtime,
   and clinical heuristics: **PASS**. Final ancestry and clean-tree results are
   recorded in the handoff after the freeze commit.

## Security invariants

- Authority is current and externally verified before every model/tool side
  effect; the Harness is never an authority source.
- Workflow organization/task/attempt/role/principal binding is exact.
- Tool requests are untrusted and default-denied.
- Tool definitions/action space cannot change silently during a Run.
- The Harness imports no provider adapter, HTTP client, SQL, RAG/Memory
  package, pricing/wallet package, shell, or programmatic tool runtime.
- Organization content is not heuristically classified as clinical. The
  Content Policy remains the existing deterministic credential boundary.
- No live model/provider, production, shared DB, migration, deployment, or
  configuration mutation is part of this slice.

## Known gaps

- Production execution-history durability has only a port; no production
  adapter is claimed.
- The production composition root does not yet wire Harness `ModelExecutor` to
  Model Runtime. The current Model Runtime invocation is a single authorized
  model call; the adapter must create/dispatch one invocation per Harness turn
  without bypassing its existing assignment, identity, egress, and cost gates.
- The narrow active-principal reader required by `tasksauthority.Adapter` is not
  wired into a daemon/CLI composition root in this slice.
- Provider wire-byte replay, real provider prefix-cache observations, durable
  cancellation commands, Context Assembly, Memory OS, compaction, and
  programmatic tool calling remain pending.
- No Harness consumer records `RunResult` into tasks; task outcome/evidence and
  completion remain Workflow Runtime responsibilities.

## Rejected alternatives

**REJECTED: Call provider adapters directly from the Harness.**

Reason: provider execution/egress/identity belongs to Model Runtime.

**REJECTED: Let model output dynamically grant a tool/capability.**

Reason: model output is untrusted cognition, never authority.

**REJECTED: Rewrite old messages after a correction.**

Reason: model-visible history must remain reconstructible and append-only.

**REJECTED: Implement Memory OS inside the Harness.**

Reason: memory selection/mounting and execution projection are distinct
boundaries.

**REJECTED: Store final answer only and discard intermediate tool trajectory.**

Reason: execution history must preserve causal evidence and enable
deterministic replay.

**REJECTED: Return a magic max-turn string as a valid agent answer.**

Reason: limit exhaustion is structured execution status, not task completion.

**REJECTED: Build another pricing/wallet ledger inside Harness.**

Reason: execution limits and financial accounting are separate
responsibilities.

**REJECTED: Add execution-history persistence in this first slice.**

Reason: existing durable surfaces do not represent the required trajectory,
and choosing a new canonical persistence model requires a separate explicit
decision rather than a hidden migration.

## Follow-ups

1. Review and decide the production append-only history representation.
2. Implement a Model Runtime adapter/composition root that preserves every
   existing authorization, egress, execution-identity, dispatch, and cost gate.
3. Wire a real active-principal reader to the tasks lease authority adapter.
4. Integrate `RunResult` with Workflow Runtime outcome/evidence in a separate
   slice without moving task truth into the Harness.
5. Implement Context Assembly and Memory OS as separate boundaries.
6. Add programmatic tool calling, plugin lifecycle, compaction artifacts, and
   execution profiles only in their authorized future slices.
