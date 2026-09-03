# Core audit follow-ups

This file records verification findings that are outside the audit diff and
were intentionally not changed in `audit/core-governance-fixes`. The findings
are additive and require a separate owner decision.

Date observed: 2026-09-03

## F-001 — pre-existing format check failures

Running the repository formatter check in the coderunner tool image reports
these files:

- `internal/memory/errors.go`
- `internal/modelruntime/adapter/openaicompat/adapter_test.go`
- `internal/modelruntime/adapter/openairesponses/adapter_test.go`
- `internal/modelruntime/adapter/xai/adapter_test.go`
- `internal/modelruntime/canonical_routing.go`
- `internal/modelruntime/service_test.go`
- `internal/rag/errors.go`
- `internal/tasks/postgres/outbox.go`

None of these paths is changed by this branch relative to `origin/main`. The
new audit test was formatted separately. The unrelated files were not
rewritten because doing so would expand the audit scope.

Impact: `make verify` stops at `fmt-check` until this pre-existing repository
hygiene issue is resolved separately.

## F-002 — pre-existing task-fitness wall-clock finding

The task-fitness guard reports:

```text
internal/tasks/postgres/outbox.go:201:
cutoff := time.Now().UTC().Add(-request.OlderThan)
durable PostgreSQL code uses application wall-clock time
```

`internal/tasks/postgres/outbox.go` is unchanged relative to `origin/main`.
This was not fixed in the audit branch because it is unrelated to the
requested core-governance changes.

## F-003 — executive fitness guard/source contract mismatch

`test-executive-fitness` fails with:

```text
executive fitness: ambiguous automatic retry detected
```

The guard searches production Go files for `ambiguous.*retry|retry.*ambiguous`.
In the unchanged `internal/executive/ambiguity_resolution.go`, the match is
present in a policy comment (`ambiguous implies retry`) and in the diagnostic
reason string (`authorizes one retry`). The implementation intentionally
describes and authorizes one retry for the pure-model effect class, while
refusing other effect classes.

Neither `scripts/check-executive-fitness.sh` nor
`internal/executive/ambiguity_resolution.go` is changed by this branch relative
to `origin/main`. This is therefore recorded as a pre-existing guard/source
contract mismatch, not silently resolved by weakening the guard or changing
the retry policy.

Impact: `test-executive-fitness` cannot report PASS until the owner decides
whether the guard should inspect executable retry behavior rather than prose
and diagnostic text, or whether the existing executive policy must change.

## Verification boundary

The audit branch does not modify the files named above. All applicable
governance, evidence-contract, mission-gate, repository-hygiene, and compose
changes remain in the branch and are covered by the targeted tests reported in
the audit handoff.
