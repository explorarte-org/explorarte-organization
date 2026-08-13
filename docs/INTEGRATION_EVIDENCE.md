# Integration Evidence Reliability

**The harness distinguishes execution failure from evidence incompleteness.**

A run that stops at the first failing suite and a run that observes every
suite and finds one failure both exit non-zero. They are not the same claim.
The first says nothing at all about the suites it never reached, and until
this front there was no way for a consumer to tell them apart.

That gap was not theoretical. A CLI contract changed (`memory list --role`
became `--actor`), the smoke test that would have caught it went stale, an
unrelated package failed earlier in the run, the suite aborted, the smoke
test never executed, and the regression reached `main` under an evidence set
nobody could see was partial.

## What COMPLETE_GREEN means

It does **not** mean "the process exited 0". It means all of:

- every critical precondition passed;
- every applicable unit in the manifest was observed;
- no unit ended `UNKNOWN`, and none was `BLOCKED` by a failed dependency;
- no unit failed.

## Two classes of failure

The harness knows the difference, and getting this wrong in either direction
is a defect:

| Class | Examples | Behaviour |
|---|---|---|
| **SAFETY** | database is not disposable, destructive guard unauthorized, compose/worktree isolation invalid | **abort immediately**, no suite is attempted |
| **INFRASTRUCTURE** | postgres will not come up, schema cannot be bootstrapped | **abort immediately**, no valid environment to observe from |
| **TEST / CONTRACT** | a package fails, a smoke assertion fails, a fitness check fails | **record and continue**, aggregate into the final status |

"Always continue" would be a safety defect: destructive suites could reach a
database the guard just refused to authorize. "Always abort" is the
observability defect this front exists to remove. The harness must know which
it is looking at.

## Accounting completeness vs evidence completeness

Deliberately separate fields, because they answer different questions.

```
expected   20        accounting_complete = do we know what happened to all 20?
accounted  20        evidence_complete   = do we have behaviour from all 20?
PASS       15
FAIL        1
BLOCKED     4
UNKNOWN     0
```

Here accounting is complete and evidence is not: four units were accounted
for precisely, and we still have no behavioural result from them. That run is
`INCOMPLETE_RUN`, never `COMPLETE_WITH_FAILURES`.

`SKIPPED_NOT_APPLICABLE` is different. A unit the requested mode excludes was
never expected to produce evidence, so it does not compromise completeness.
`BLOCKED` and `UNKNOWN` do.

## Run states

```
COMPLETE_GREEN           everything applicable observed, nothing failed
COMPLETE_WITH_FAILURES   everything applicable observed, some failed
INCOMPLETE_RUN           finished without observing everything expected
SAFETY_ABORT             a safety precondition failed; continuing was unsafe
INFRASTRUCTURE_ABORT     no valid environment could be created
```

Per-unit: `PASS`, `FAIL`, `BLOCKED`, `SKIPPED_NOT_APPLICABLE`, `UNKNOWN`.

`UNKNOWN` means "we expected to observe this and have no result for it". It is
not a synonym for skipped. Every declared unit starts `UNKNOWN` and only
leaves that state by being run, blocked, or excluded by mode — which is how a
unit the runner never reaches stays visible instead of vanishing.

## Exit codes

`0` is reserved for `COMPLETE_GREEN`. Every other state is non-zero.

The exit code was deliberately **not** extended into a richer taxonomy. No
consumer needs it: `Makefile` and `.github/workflows/ci.yml` only test
zero/non-zero, and no consumer parses stdout. The semantics live in the
evidence manifest, so existing callers keep working unchanged.

## Source of truth

`scripts/integration-suites.tsv` and `scripts/integration-preconditions.tsv`
are the canonical definitions. `expected`, `attempted` and `completed` are
derived from them; there is no second list and no hand-maintained counter.
Adding a suite to the TSV is the only step needed to make it part of the
expected set.

This matters because the project has repeatedly been bitten by manual
mirrors of canonical state. The previous harness had no suite list at all —
the list *was* the control flow, nineteen inline `if` blocks — which is why
nothing could report how many suites were expected.

## Dependencies

`depends_on` is declared in the schema and empty for every unit in V1. That
is a finding, not an omission: every integration suite applies the migrations
it needs, and the CLI smoke bootstraps its own state. The apparent
"`platform/postgres` must run first" relationship was positional, created by
`set -e` rather than by any real dependency. Schema bootstrap is now an
explicit precondition, so `platform/postgres` is an ordinary suite whose
failure blocks nothing.

## Fitness

`make check-integration-evidence` runs three behavioural checks, each paired
with a mutation that must break it:

| Check | Mutation |
|---|---|
| independent failures are aggregated | restore fail-fast → late failure disappears |
| a failed safety precondition stops everything | remove the hard abort → suites run unsafely |
| an unobserved unit cannot be green | silently skip a unit → must yield `INCOMPLETE_RUN` |

The third includes the assertion that matters most: every executed unit
passes and the loop has nothing to complain about, yet `COMPLETE_GREEN`
remains impossible because one expected unit was never observed. The runner's
own exit code is not trusted over the manifest.

A control assertion runs alongside it — the same manifest unmutated *does*
reach `COMPLETE_GREEN` — so the third check cannot pass merely because
nothing is ever green.

## Known limitation

`cli-smoke` is one observable unit covering a 239-line shell block with many
independent assertions. If it fails, the manifest reports one failure and
cannot say which assertion broke. Splitting it is tracked as a separate
front; modelling it as one unit is the honest description of the granularity
the harness can currently observe.
