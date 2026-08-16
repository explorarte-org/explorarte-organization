# V2 Harness Reliability Closeout 001

Closes the three properties the Execution Harness still lacked: authority
failure semantics, durable execution history, and lease expiry during a run.
It changes no authority decision and relaxes no check. Base dependency:
`v2/harness-model-runtime-integration-001` @ `33d2db8`.

## Authority failure taxonomy

The Harness now answers two different questions with two different errors.
Collapsing them is what previously turned a database outage into a fabricated
revocation.

| | `AuthorizationDenied` | `AuthorityUnavailable` |
|---|---|---|
| Meaning | Authority was consulted and refused | Authority could not be consulted |
| Examples | principal missing, disabled, wrong organization, role mismatch, lease invalid, revoked, expired, holder mismatch | PostgreSQL unreachable, context deadline, connection lost |
| Provider calls after it | 0 | 0 |
| Run outcome | terminal `authorization_denied` | non-terminal `authority_unavailable` |
| History written | terminal `run_failed` event | nothing |
| `RunResult.Retryable` | `false` | `true` |

Both fail closed. The difference is what happens next: a denial is a durable
statement that the principal or lease lost standing, and a run that receives
one is over. An outage is a statement about the infrastructure, so the run is
left exactly as it was and the same run identity resumes in place.

`StatusAuthorityUnavailable` is deliberately absent from
`terminalStatusMatches`, so no terminal event can ever carry it: a history that
claimed otherwise is rejected as corrupt.

Classification lives in `tasksauthority.authorityFailure`. Only causes meaning
"could not consult" are treated as unavailable — `tasks.ErrDatabaseUnavailable`,
`modeldispatch.ErrDatabaseUnavailable`, `context.Canceled`,
`context.DeadlineExceeded`. Definite answers stay denials, **including
not-found**: a principal or lease that does not exist is a real refusal, not an
outage. Both branches wrap with `%w`, so `errors.Is`/`errors.As` still reach the
original cause; the previous `%v` flattening destroyed exactly that.

## Durable history

`internal/executionharness/postgres` implements `ExecutionHistoryStore` against
`execution_run_events` (migration `000050`). The Harness itself never imports
pgx: the port stays a two-method contract and this is one adapter beside the
memory store.

**Persistence boundary.** The table stores trajectory only. No task state, no
authority decision, no pricing, and no reasoning telemetry that Model Runtime
does not expose.

**Ordering** is an explicit per-run ordinal, never a timestamp. `clock_timestamp()`
is not monotonic enough to reconstruct a trajectory, and two events written in
the same millisecond must still have an unambiguous order. Reads are
`ORDER BY sequence`, and the `sequence` column is authoritative over the
payload copy.

**Identity.** Every event carries organization, task, attempt and run. The store
is constructed per organization, so two organizations may use the same run
identifier and neither can read, extend, or interleave the other's history. An
event stamped with a foreign organization is refused rather than rewritten into
scope.

**Idempotency.** Append is optimistic on the sequence, matching the in-memory
store: the caller states the ordinal it believes is current and a mismatch is a
conflict, never a silent overwrite. Re-appending an already-confirmed event
therefore conflicts instead of producing a second ambiguous copy. Concurrent
writers race on `UNIQUE (organization_id, run_id, sequence)` and the loser gets
the same conflict.

**Append-only in the schema, not only in the code.** A trigger rejects UPDATE
and DELETE, matching every other durable ledger here.

**Resume.** A new process with a new pool, a new store and a new runtime loads
the history and continues. A non-terminal history resumes at the right point
without repeating the model turn or the tool side effect. A terminal history
replays its result and calls neither the provider nor a tool.

One resume correction was required: the replay guard used to be seeded from
every `tool_call_requested` event. A tool call that was requested and never
resolved was never surfaced to the model, so a resumed run re-proposing that
same id is ordinary continuation, not a replay. The guard now seeds only from
resolved calls — executed or denied. The in-process guarantee is unchanged.

## Lease expiry

Authority is verified before every model turn and before every tool call, and
the lease check is a deadline (`clock_timestamp() < expires_at`), not a one-shot
budget. A lease that lapses mid-run denies turn two with zero additional
provider calls, and the turn-one evidence stays intact and append-only.

Expiry and revocation are prepared as genuinely different scenarios even though
both must deny. Revocation flips the row status; expiry leaves the lease
`active` and only moves its deadline into the past — the state a lease reaper
has not swept yet, which is the dangerous one. Because `task_leases` enforces
`CHECK (expires_at > issued_at)`, an expired lease is modelled as one issued two
hours ago that lapsed an hour ago. The integration test asserts the row is
still `active` at the moment of expiry, so the case cannot silently degrade
into a second revocation test.

The mirror case is proven too: a lease renewed through `Heartbeat` while still
valid is **not** denied, and the run completes. Without it, an authority that
simply refused every second turn would pass every denial test above.

## Observation left for review

`authority_unavailable` can never be produced as a terminal status by the
Harness: both unavailability branches return without appending, and
`terminalStatusMatches` does not accept it. The durable store, however, does
**not** refuse such an event at write time -- a caller that violated the port
contract could persist one. What protects the system is the read side: a
history carrying that impossible state is rejected as `ErrHistoryCorrupt` on
reload, the run reports `history_error`, and no provider or tool is touched.
`TestForgedAuthorityUnavailableTerminalFailsClosedOnReload` pins exactly that.

This is weaker than a schema `CHECK` on `terminal_status`, and it is recorded
here rather than hardened preemptively. Whether it needs the constraint before
merge is a decision for review, not for the worker who wrote the code.

## Known gaps

- No daemon/CLI consumer starts Harness runs yet. The composition seam exists
  through Model Runtime bootstrap and the durable store exists and is proven
  against PostgreSQL 17, but nothing in production is wired to either. This
  slice does not claim a production daemon has been migrated.
- Consumer migration carries a hard precondition: any consumer running work
  under Harness authority must claim its lease with
  `ClaimRequest.HolderPrincipalID` set to the canonical execution principal ID,
  and use that same ID as `ActorID` for the lease lifecycle.
  `internal/executive/orchestrator.go` still claims with
  `WorkerID = "executive-orchestrator"` and no holder principal; that remains
  correct while it does not run Harness work.
- `AuthorityUnavailable` is classified and surfaced, but nothing schedules the
  retry. The Harness reports `Retryable`; deciding when to re-enter the run
  belongs to the consumer that owns the attempt.
- Retry classification recognises the two stores' `ErrDatabaseUnavailable` and
  context errors. A transport failure that neither store maps to those
  sentinels is conservatively treated as a denial.
- Only fake providers are exercised. `LIVE_PROVIDER_CALLS` remains 0 by design.
- The PostgreSQL integration suites are not idempotent across runs on the same
  database: a second run fails because the down-migration subtest leaves the
  schema behind. This predates the Harness work and every result here was taken
  on a fresh database.
- Context Assembly V2, Memory OS, compaction, and Workflow completion bridging
  remain out of scope.
