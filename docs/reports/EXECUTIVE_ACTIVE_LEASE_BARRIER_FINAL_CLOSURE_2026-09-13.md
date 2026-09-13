# Executive Active Lease Barrier — Final Closure

Date: 2026-09-13  
Program: `EXECUTIVE_ACTIVE_LEASE_BARRIER`  
Final verdict: **PASS — ACTIVE_LEASE_BARRIER_FIX_PROVEN_IN_PRODUCTION**

## Incident

The production defect occurred when two independent Executive Orchestrator instances observed the same durable task attempt. The legitimate owner held the active lease and its opaque token only in process-local memory. A second Orchestrator could see the durable leased/running task but did not possess that local token.

Before the fix, that transient observation was surfaced through generic `ErrRunBlocked`. `handlePhaseError` could then write a durable `blocked / executive_phase_failed` verdict on the root even though the legitimate owner was still executing the attempt successfully. The incident shape therefore combined a completed child execution with an incorrectly and permanently blocked root.

The defect was not a lease-ownership failure by the legitimate process. The invariant was that a process which cannot prove possession of the active lease token must wait without adopting the lease, starting another attempt, calling the Harness, or writing a durable root verdict.

## Fix

Reviewed PR: #211 — `fix(executive): keep active unowned leases transient`  
Reviewed head: `51d1454aed0d791d102a32b48073fe26b9b0b927`  
Merged production commit: `d30972c1b36b6fba711e25099acf3a35b46bf5f7`  
Tree: `abed675da73a23cb1dac8c1779e21bd5a9c8d12b`  
Parent: `2df49dae0b826d152839821a1bd7dfd6c8dab522`

The change introduced `ErrActiveLeaseBarrier` as a sentinel semantically distinct from both `ErrRunBlocked` and `ErrLeaseLost`.

Required behavior:

- an active durable lease with no process-local opaque token returns `ErrActiveLeaseBarrier`;
- the observer does not infer ownership from `holder_id`, worker identity, or principal identity;
- the observer does not reconstruct, recover, or persist the lease token;
- the observer does not heartbeat or adopt the lease;
- the observer does not create a second attempt or lease;
- the observer does not invoke the Harness or provider;
- the transient barrier does not consume retry budget, including under `WithNoRetries`;
- the transient barrier does not durably block the root;
- generic `ErrRunBlocked` remains durable for genuinely permanent conditions;
- `ErrLeaseLost` retains its separate meaning for a process that did possess a lease and subsequently lost it.

The persistent Executive worker also treats the active-lease barrier as self-resolving, avoiding false failure-observer noise while the legitimate holder finishes or the Task Engine later expires/reconciles the lease.

## Deterministic regression proof

The regression suite constructed two independent Orchestrator instances over the same durable task/model fixtures. ORCH_A claimed and executed the CEO-plan task while ORCH_B observed the same leased/running attempt with an empty local lease map.

Post-fix expectations were verified repeatedly:

- ORCH_B returns `ErrActiveLeaseBarrier`;
- ORCH_B does not also return `ErrRunBlocked`;
- root durable status/reason is unchanged by ORCH_B;
- ORCH_B creates no claim and makes no Harness call;
- ORCH_A completes its original attempt;
- attempts = 1;
- leases/claims = 1;
- model invocations = 1;
- Harness executions = 1;
- duplicate execution = 0.

The deterministic race regression passed 100/100 normally and 100/100 under the race detector. Full Executive tests, full Executive race tests, and `make verify` passed before merge.

## Merge and deployment

PR #211 was squash-merged because protected `main` requires linear history. The squash postconditions were verified:

- `NEW_MAIN_PARENT == 2df49dae0b826d152839821a1bd7dfd6c8dab522`;
- `NEW_MAIN_TREE == abed675da73a23cb1dac8c1779e21bd5a9c8d12b`;
- the merged tree was identical to the reviewed PR head tree.

Post-merge CI on `main` succeeded for both `verify` and `postgres-final-validation`.

Production was deployed from a clean detached target worktree using the repository deployment image helper with `--expect-commit`. Runtime provenance reported the exact full commit `d30972c1b36b6fba711e25099acf3a35b46bf5f7`.

Deployment invariants:

- only `orgd`, `model-worker`, and `code-runner` were recreated;
- PostgreSQL was not stopped or recreated;
- the PostgreSQL container/image/volume remained unchanged;
- migrations remained current at 73/73 with 0 pending and no migration run;
- rollback tags were created from the immutable pre-deploy image IDs before application recreation;
- no provider call was made during deployment;
- production returned healthy/ready on the target commit.

## Production E2E sequence

Three bounded production rounds were retained as forensic evidence.

### V1 — root 723

The active lease window was captured, but ORCH_A completed its provider call before the second observer could be launched. ORCH_B was intentionally not run against a closed window. Result: `INCONCLUSIVE — OBSERVER_MISSED_ACTIVE_LEASE_WINDOW`.

The CEO-plan execution itself completed with exactly one attempt, one lease, one real provider call, and one successful invocation. The later root block was caused by the smoke campaign budget guard, not by lease handling.

### V2 — root 726

The automated watcher captured the active window and launched ORCH_B within 3 ms, but the test harness invoked the CLI as `resume ROOT_TASK_ID --json`. Go's `flag.Parse` stopped at the positional argument and the process exited at CLI usage parsing before entering Executive runtime logic.

No durable task/model state was touched by ORCH_B. A second ORCH_B was not launched because the round explicitly authorized only one observer process. Result: `INCONCLUSIVE — ORCH_B_INVOCATION_SYNTAX_ERROR`.

### V3 — root 729 — decisive production proof

Forensic identifiers:

- root task: `729`;
- correlation: `executive:1f0c082caffb2bf81fc5e46bdc5444eb`;
- CEO-plan task: `730`;
- attempt: `158`;
- lease: `158`;
- model invocation: `155`;
- provider: `openai_responses`;
- model: `gpt-5.6-luna`.

The syntax probe first proved the corrected CLI order `executive resume --json ROOT_TASK_ID` reached business logic without creating any task/model/provider state.

The watcher was ready before ORCH_A, captured the CEO-plan attempt in `running` with lease 158 active and invocation 155 still in `requested`, and launched a fresh ORCH_B 4 ms later.

ORCH_B returned exactly:

`executive active task lease is not owned by this process`

The observer did not return `ErrRunBlocked` and did not durably change root 729. There was genuine temporal overlap: the provider send for ORCH_A began while ORCH_B was still executing.

Production invariants observed during the race:

- ORCH_B entered Executive runtime;
- `ErrActiveLeaseBarrier` observed = yes;
- `ErrRunBlocked` observed = no;
- root durable block caused by ORCH_B = no;
- second attempt = no;
- second lease = no;
- second plan invocation = no;
- second provider send = no;
- lease adoption by ORCH_B = no;
- lease-token hash read/recovery = no;
- plan cardinality remained 1 attempt / 1 lease / 1 invocation;
- total real provider sends for the campaign = 1.

The legitimate provider invocation succeeded. Its generated Executive plan was then rejected by host-side semantic validation with `executive plan exceeds configured bounds`, leaving the plan task in `dead_letter` and the root blocked with `model_result_contract_rejected`.

That terminal outcome is orthogonal to the active-lease fix: the provider call completed successfully, ORCH_B had already returned the transient barrier without durable mutation, and the later root block was caused by model-output contract validation.

Therefore the lease-barrier mechanism itself is proven under real production concurrency.

## Final operational state

After V3:

- production commit remained `d30972c1b36b6fba711e25099acf3a35b46bf5f7`;
- services remained healthy and `orgd` ready;
- running attempts = 0;
- active unexpired leases = 0;
- nonterminal model invocations = 0;
- migrations were unchanged;
- no reconciliation was run;
- no forensic rows were deleted;
- rollback image tags remained available.

The deployment build worktree `/home/ubuntu/deploy-worktrees/active-lease-barrier-d30972c` was subsequently removed after verifying deployment provenance and retaining rollback images. The older local reproduction worktree `executive-lease-repro` was intentionally left untouched pending final housekeeping.

## Closure decision

`EXECUTIVE_ACTIVE_LEASE_BARRIER` is closed.

**Final verdict: `PASS — ACTIVE_LEASE_BARRIER_FIX_PROVEN_IN_PRODUCTION`.**

No further production retry is warranted for this defect.

## Separate follow-ups

The following findings are explicitly outside this incident and must not reopen it:

1. `orgctl executive resume` documents `ROOT_TASK_ID [--json]` but currently uses the standard Go `flag.Parse`, so a trailing `--json` is rejected. Align `resume` with the CLI's interspersed-flag convention.
2. `executive external-smoke` is a one-provider-call probe but currently propagates later campaign-level failures as command failure. Clarify and harden its operational success contract so one bounded provider-path proof is distinguishable from full-campaign completion.
3. Model-output rejection such as `executive plan exceeds configured bounds` is a separate model/contract robustness concern.
4. Rollback tags should remain until a later healthy deployment establishes a newer rollback point.
