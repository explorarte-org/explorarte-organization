# V2 Workflow Runtime 001 — integration record

Canonical base: `89f7072cc05ec10ae1358470428d709b68a09ffe`

Branch: `v2/workflow-runtime-001`

Functional implementation commits:

- `23e0bc4ff5d7d0d4cb14b4c2963b732cf42e0ccd`
- `c2f180b1b321aef96f64cd99d8f9e75d9f79a3b7`
- `0c43efa6a2d7afd704883641e003f64e768fd524`
- `99a0b5476b16699487d5aa85dd1eb9d7c67c24c1`

The final branch SHA is reported externally after this record is committed,
because a Git commit cannot contain its own hash.

## Scope

This slice introduces `internal/workflowruntime`, a V2 application seam over
existing durable domains. It does not implement another workflow engine. The
runtime exposes behavior-oriented ports for:

- task initiation, observation, same-role execution, evidence, and terminal
  transitions;
- independent completion permission;
- decision branching that must result in an observable task action;
- fail-closed initiation, read, and mutation authorization;
- authorized cross-role coordination;
- the legacy executive owner-goal decomposition during migration.

Concrete adapters bind the seam to `internal/tasks`, `internal/completion`,
`internal/agentmessaging`, and `internal/executive`. The decision boundary is a
small strategy port in this first slice; its deterministic test strategy proves
that a selected branch changes task state instead of becoming a second source
of completion truth.

No SQL, schema, migration, model/provider API, pricing, RAG, memory, plugin,
session-message, scheduler, or harness behavior was added.

## Durable source of truth

`internal/tasks` remains the only durable owner of work state. The V2
`Snapshot` is a read model of task facts: organization, status, assignment,
correlation and causation, requirements, evidence, attempts, and ordered task
events. Its completion field is derived permission, not persisted task status.

The real task adapter calls the existing task service for every mutation and
reads the existing event sequence. It adds no repository, table, or event log.
Retries and corrections append attempts/events. They never edit earlier event
facts.

## V1 boundaries composed

### Tasks

The task adapter maps the V2 behavior port onto `tasks.Service`. Task status is
the terminal authority. Existing organization scoping, registry validation,
idempotency, lease checks, requirement checks, and task outbox behavior stay in
force.

### Completion

The completion adapter maps `pass`, `fail`, and `inconclusive` to
`ALLOW_COMPLETE`, `DENY_COMPLETE`, and `BLOCKED_UNSATISFIED`. The runtime calls
`FinalizeCompleted` only after `ALLOW_COMPLETE`. A deny leaves the durable task
at `awaiting_verification`; it does not create an independent completion
lifecycle.

### Decision graph

The V2 boundary treats decisiongraph as a branching strategy. A branch result
must preserve task/correlation/causation identity and produce an observable
task action. The first supported action is a non-terminal block transition;
the task event remains the work truth and the decision reference remains
provenance.

The current decisiongraph ledger stores candidate payload hashes, not a typed
action body or a read port capable of reconstructing one. Therefore this slice
does not pretend it can reconstruct arbitrary production branch actions from
that ledger. The real executive decision recorder remains unchanged, and the
new branch port is exercised with a predeclared deterministic strategy. Adding
a production resolver requires a separately reviewed typed read contract, not
a new table or guessed reconstruction.

### Workflow authorization

`AuthorizationPort` is evaluated before `TaskPort.Initiate`. The concrete
adapter binds an active execution principal to its organization and role,
requires active/executable registry roles, and applies the existing V1 topology
to cross-role assignment. A denied assignment therefore creates zero tasks and
zero task events. Same-role initiation still requires a valid active principal.

Task mutation is restricted to the assigned role and revalidates the principal.
Generic observation now requires `ObserveCommand{Actor, TaskID}`. The explicit
read policy permits the assignee, the CEO for organizational descendants, and a
canonical department leader for work assigned inside that leader's own unit.
A legacy `requested_by_role_id` string does not grant visibility.

### Agent messaging

The coordination adapter emits only delegation or completion messages through
the existing ledger. The ledger still verifies active execution-principal role
binding, task ownership, organization, capability grants, idempotency, payload
bounds, and V1 topology. The runtime additionally rejects same-role messages,
task/correlation/causation drift, and cross-organization task bindings before
the port call.

Coordination authorization is defense in depth after creation authorization;
it is no longer the first place at which an invalid delegation edge can fail.

Same-role work uses task transitions. Cross-role work uses durable
coordination. The authorized topology remains exactly:

- CEO to department leader;
- department leader to own worker;
- worker to own department leader;
- department leader to CEO.

### Executive

The executive adapter maps the generic V2 owner-goal request onto the existing
orchestrator and then makes the root task observable through the task port. Its
causal purpose and CEO/leader/worker authorization remain intact. No executive
package or special task representation was deleted in this slice.

## Workflow contract demonstrated

The deterministic functional test executes:

```text
owner goal
  -> CEO task
  -> authorized CEO -> leader delegation
  -> authorized leader -> worker delegation
  -> worker same-role execution
  -> evidence and required obligation
  -> completion permission
  -> durable task completion
  -> authorized worker -> leader completion
  -> leader same-role completion
  -> authorized leader -> CEO completion
```

Four and only four coordination records are emitted. Same-role execution emits
zero messages. Every descendant carries the root correlation ID; causal links
are checked against the relevant sender or recipient task before messaging.

## Security invariants

Tests exercise the allowed multi-hop flow and deny:

- worker to worker initiation with zero tasks created;
- worker directly to CEO initiation with zero tasks created;
- leader to another department's worker initiation with zero tasks created;
- execution principal role different from actor/sender role;
- disabled execution principal with zero tasks created;
- cross-organization execution principal with zero tasks created;
- cross-organization task binding;
- wrong correlation or causation;
- completion without required evidence;
- duplicated terminal transition;
- same-role messaging.

Observation tests permit assignee, own department leader, and CEO access while
denying peer workers, foreign-department leaders, disabled principals, and
cross-organization principals without returning a snapshot. Mutation by an
inactive principal is also rejected without changing task state or history.

The existing topology behavior tests and executive principal tests remain
green. RAG, memory, evidence, and text content do not participate in authority
decisions. ContentPolicy tests also remain green without semantic changes.

## Observability

Authorized `Runtime.Observe` calls deterministically return:

- workflow/task and organization IDs;
- durable status and role/unit assignment;
- correlation and causation IDs;
- requirements and evidence;
- attempts;
- current completion permission where applicable;
- ordered task events and the last transition.

Coordination returns its durable message provenance. No model reasoning or
provider-specific formatting is part of the snapshot.

## Append-only execution record

The sequence below preserves failures and later corrections.

1. Startup gate: local worktree at canonical base, clean, with merge-base equal
   to the base. Result: **PASS**.
2. Baseline focused command over tasks, completion, decisiongraph,
   agentmessaging, and executive/runtimeadapter. Result: **PASS**.
3. New workflowruntime unit/adapter tests after initial implementation. Result:
   **PASS**.
4. Focused consumer and ContentPolicy tests locally. Result: **PASS**.
5. `go test ./...` locally. Result: **FAIL**. The managed sandbox denied
   listener creation in existing `httptest`/HTTP-server packages with
   `socket: operation not permitted`. Workflow Runtime and its focused
   consumers passed within the same run.
6. `go vet ./...` locally. Result: **PASS**.
7. First focused VPS invocation. Result: **FAIL** before tests because the
   non-interactive SSH PATH did not contain Go (`go: command not found`).
8. Corrected VPS invocation using `/usr/local/go/bin/go` and an explicit
   worktree. Focused tests: **PASS**. `go test ./...`: **PASS**. `go vet ./...`:
   **PASS**.
9. Review correction: the append-only retry test was changed to create a new
   attempt instead of reusing the first attempt. Local workflowruntime tests:
   **PASS**. The correction was committed as
   `c2f180b1b321aef96f64cd99d8f9e75d9f79a3b7`.
10. Direct update of the VPS branch. Result: **FAIL** because Git correctly
    refused to update a branch checked out in a linked worktree. No receive
    configuration was weakened and no reset was used.
11. The correction was transferred through a temporary ref and applied with
    `git merge --ff-only`. Final focused tests, `go test ./...`, and
    `go vet ./...` on the corrected VPS worktree: **PASS**.
12. Static gates: gofmt clean; no SQL or model/provider dependency in
    workflowruntime; no migration, production configuration, workflow, or V1
    topology file changed. Result: **PASS**.
13. Final remote verification after the documentation commit: focused tests,
    `go test ./...`, and `go vet ./...` all completed successfully. The combined
    static-check command then reported `gofmt: command not found` because the
    non-interactive SSH PATH also omitted the formatter. Result for that static
    invocation: **FAIL**; the successful test and vet results remain valid.
14. The static gates were repeated with `/usr/local/go/bin/gofmt` and the same
    explicit worktree. Formatting, diff, dependency, migration/configuration,
    topology-file, and clean-worktree guards all passed. Result: **PASS**.
15. Human review found that `Runtime.Initiate` persisted cross-role tasks before
    any topology/principal authorization, and that `Runtime.Observe` accepted a
    bare task ID. Review disposition: **BLOCKED BEFORE PR**.
16. Correction: a mandatory authorization port was inserted before durable
    initiation and before reads/mutations. A concrete adapter now binds active
    principals, registry roles, V1 assignment topology, and explicit descendant
    visibility. The correction was committed as
    `0c43efa6a2d7afd704883641e003f64e768fd524`.
17. Regression tests proved all five requested initiation denials leave zero
    tasks, the positive same-role/delegation paths pass, unauthorized observers
    receive no snapshot, and an inactive principal cannot mutate a task. Local
    workflowruntime, focused consumer, ContentPolicy, and focused vet commands:
    **PASS**.
18. Static review found the first access-adapter version imported the
    `modeldispatch` identity type even though Workflow Runtime only needs a
    read-only principal projection. The dependency was removed in
    `99a0b5476b16699487d5aa85dd1eb9d7c67c24c1`; `PrincipalReader` now returns a
    small runtime-owned identity and cannot register, disable, assign, or invoke
    anything. Workflowruntime tests, vet, and the no-model/provider import check:
    **PASS**.

No database was opened by this work. PostgreSQL integration suites remain
guarded by their existing disposable test DB requirements and were not enabled.

## Known gaps

- No production CLI/daemon consumer is switched to the V2 façade yet. The seam
  and concrete adapters are present; consumer migration remains incremental.
- The future production composition root must project its existing active
  execution-principal reader into workflowruntime's narrow `PrincipalReader`.
  This slice deliberately does not couple the runtime to Model Runtime or add a
  second identity store.
- Decisiongraph has no typed read contract that maps a selected candidate's
  durable hash to an executable workflow action. This slice records the gap and
  does not infer or duplicate that state.
- The legacy executive orchestrator still uses its existing task subtype and
  idempotency naming conventions behind the adapter.
- Claim/reconcile scheduling remains in `internal/tasks` and existing workers;
  this slice adds no scheduler.

None of these gaps requires new persistence for this slice.

## Rejected alternatives

**REJECTED: Create a second workflow state machine.**

Reason: tasks already owns durable task state and lifecycle.

**REJECTED: Represent every workflow transition as agent messaging.**

Reason: same-role execution is state transition; messages are reserved for
authorized cross-role coordination.

**REJECTED: Delete V1 packages immediately after introducing the V2 facade.**

Reason: migration must prove consumers and behavioral parity before removing
durable implementations.

**REJECTED: Put provider/model context inside Workflow Runtime.**

Reason: workflow facts and model-request projection are separate boundaries;
request projection belongs to Execution/Cognition Harness.

**REJECTED: Add a workflow event table to satisfy append-only history.**

Reason: ordered task events and the task outbox already provide canonical work
history. A duplicate event log would split the truth.

## Follow-ups

- Wire one existing CLI/daemon execution path through the V2 façade after human
  review of this seam.
- Define a typed, read-only selected-action projection for decisiongraph before
  adding its production strategy adapter; do not add a second persistence
  representation.
- Continue replacing executive representation only after parity is proven.
- Implement Execution/Cognition Harness separately; do not place provider
  request projection in this runtime.
