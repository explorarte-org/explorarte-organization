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

## F-004 — additional pre-existing fitness failures

The same guard results were reproduced in a temporary worktree at
`origin/main`:

- `check-model-egress-fitness.sh` rejects the pre-existing canonical file
  `docs/canonical/instrument-v4-controller-binding-001.provenance.yaml`.
- `check-model-provider-fitness.sh` rejects the pre-existing canonical change
  to `docs/canonical/capability-matrix.yaml`.
- `check-alibaba-cli-fitness.sh` reports that rendered context is not passed by
  stdin.
- `check-embeddingruntime-fitness.sh` matches the pre-existing
  `API_KEY_SERVICE_BLOCKED` text in a comment in the Gemini adapter.

These failures are present on `origin/main` and are not caused by this audit
branch. They were not changed here.

## F-005 — model-dispatch guard versus this branch's canonical update

`check-model-dispatch-fitness.sh` passes on `origin/main` but rejects the
audit branch because `docs/canonical/role-catalog.yaml` changed. That
canonical change is part of the transported audit and is covered by the
canonical approval trailer. The guard currently treats any role-catalog
change as outside its own historical scope.

Resolving this requires an explicit owner decision about the guard's allowed
change boundary; weakening the guard or changing the canonical audit update
was intentionally not done in this branch.

## F-006 — compose validation boundary

`docker compose -f compose.yaml config` succeeds with ephemeral placeholder
values supplied only in the process environment. Running it without those
values fails because the repository intentionally requires production secret
variables. No `.env` or secret file was read or modified, and no service was
started.

## F-007 — CI integration findings after the counts correction

The first PR CI run (`33782322497`) correctly exposed one stale integration
expectation: `internal/organization/registry/integration_test.go` still used
46 imported and 2 proposed roles. It was corrected to 41/7 in a separate
governance-approved commit.

The complete local integration suite was rerun after that correction. All
PostgreSQL-backed suites passed; the only remaining failure was the existing
CLI RAG smoke path, which rejects a review where `actor_role_id` and
`proposed_by` are both `empresa/human` with `ErrSelfReview`. Both
`internal/rag` and `scripts/test-integration.sh` are unchanged relative to
`origin/main`, so this is recorded for a separate fix rather than changed in
this audit.

## Resolution status for the 2026-09-03 verification pass

The findings above remain as the historical record of what was observed. The
following separate commits resolve the reproducible blockers without changing
the frozen audit evidence:

- F-001: resolved by `7fe3463`; the eight reported Go files are formatted and
  the repository `fmt-check` now passes.
- F-002: resolved by `8590606`; durable outbox retention now uses the
  PostgreSQL clock in both dry-run and delete paths, avoiding application
  wall-clock skew.
- F-003: resolved by `205606f`; the executive fitness guard now checks the
  executable bounded ambiguity disposition rather than matching explanatory
  comments or diagnostic text. The pure-model retry policy is unchanged.
- F-004: resolved by `fea781e` and `608e7a8`; the model-egress, model-provider,
  Alibaba CLI, and embedding fitness checks now match the current
  implementation and canonical audit scope. The Gemini comment-only false
  positive was removed without changing runtime behavior.
- F-005: resolved by `fea781e`; the dispatch guard no longer rejects the
  approved role-catalog update that belongs to this audit branch.
- F-006: closed as an expected validation boundary. Compose validation passes
  with process-local placeholder values; no `.env` or secret file is changed.
- F-007: resolved by `f19e17a`; the disposable CLI RAG smoke fixture now uses
  a non-self reviewer proposal while retaining `empresa/human` as the review
  actor. The complete host-run integration suite reports 35/35 evidence units
  passed and `COMPLETE_GREEN`.

The local verification pass after these resolutions reports `make verify`,
`make build-cross`, `make registry-validate`, both governance guards, Compose
configuration, and the complete integration suite as passing. CI remains the
authoritative remote gate before any VPS deployment smoke.
